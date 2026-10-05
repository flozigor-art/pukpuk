package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"langplant-crm/internal/proto"
)

var (
	errNodeOffline = errors.New("storage node offline")
	errBlobMissing = errors.New("blob not found on node")
)

// Hub is the VPS side of the storage node connection.
type Hub struct {
	s *Server

	mu      sync.Mutex
	ctrl    *nodeConn
	pending map[string]*pendingStream
	checks  map[string][]string
	info    NodeInfo
}

type NodeInfo struct {
	Online      bool   `json:"online"`
	ConnectedAt int64  `json:"connected_at"`
	LastSeen    int64  `json:"last_seen"`
	Version     string `json:"version"`
	NodeID      string `json:"node_id"`
	Addr        string `json:"addr"`
	proto.Stats
}

type nodeConn struct {
	ws     *websocket.Conn
	send   chan proto.Msg
	ctx    context.Context
	cancel context.CancelFunc
}

type pendingStream struct {
	connc chan *websocket.Conn
	errc  chan string
	done  chan struct{}
	once  sync.Once
}

func newHub(s *Server) *Hub {
	h := &Hub{s: s, pending: map[string]*pendingStream{}, checks: map[string][]string{}}
	// restore last known node info for the status page
	var raw string
	if err := s.db.QueryRow(`SELECT value FROM node_state WHERE key = 'info'`).Scan(&raw); err == nil {
		json.Unmarshal([]byte(raw), &h.info)
		h.info.Online = false
	}
	return h
}

func (h *Hub) online() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ctrl != nil
}

func (h *Hub) nodeInfo() NodeInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	i := h.info
	i.Online = h.ctrl != nil
	return i
}

func (h *Hub) saveInfo() {
	h.mu.Lock()
	b, _ := json.Marshal(h.info)
	h.mu.Unlock()
	h.s.db.Exec(`INSERT INTO node_state (key, value) VALUES ('info', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(b))
}

func (h *Hub) closeAll() {
	h.mu.Lock()
	c := h.ctrl
	h.mu.Unlock()
	if c != nil {
		c.ws.Close(websocket.StatusGoingAway, "server shutdown")
	}
}

// send queues a control message for the node.
func (h *Hub) send(m proto.Msg) error {
	h.mu.Lock()
	c := h.ctrl
	h.mu.Unlock()
	if c == nil {
		return errNodeOffline
	}
	select {
	case c.send <- m:
		return nil
	case <-c.ctx.Done():
		return errNodeOffline
	case <-time.After(30 * time.Second):
		return fmt.Errorf("node control queue is full")
	}
}

// handleControl is the long-lived control websocket of the node.
func (h *Hub) handleControl(w http.ResponseWriter, r *http.Request) error {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return nil
	}
	ws.SetReadLimit(64 << 20)
	ctx, cancel := context.WithCancel(h.s.ctx)
	defer cancel()

	// the first message must be hello
	hctx, hcancel := context.WithTimeout(ctx, 15*time.Second)
	var hello proto.Msg
	err = readMsg(hctx, ws, &hello)
	hcancel()
	if err != nil || hello.T != proto.THello || hello.Hello == nil {
		ws.Close(websocket.StatusPolicyViolation, "expected hello")
		return nil
	}
	if hello.Hello.Proto != proto.Version {
		ws.Close(websocket.StatusPolicyViolation, fmt.Sprintf("protocol mismatch: server %d, node %d", proto.Version, hello.Hello.Proto))
		return nil
	}

	nc := &nodeConn{ws: ws, send: make(chan proto.Msg, 1024), ctx: ctx, cancel: cancel}
	h.mu.Lock()
	old := h.ctrl
	h.ctrl = nc
	now := nowMs()
	h.info = NodeInfo{Online: true, ConnectedAt: now, LastSeen: now, Version: hello.Hello.Version, NodeID: hello.Hello.NodeID, Addr: clientIP(r), Stats: hello.Hello.Stats}
	h.mu.Unlock()
	if old != nil {
		old.ws.Close(websocket.StatusNormalClosure, "replaced by a new connection")
		old.cancel()
	}
	slog.Info("storage node connected", "node", hello.Hello.NodeID, "version", hello.Hello.Version, "addr", clientIP(r))
	h.saveInfo()
	h.s.events.Publish("storage")

	go h.writer(nc)
	go h.pinger(nc)
	go h.reconcile()

	for {
		var m proto.Msg
		if err := readMsg(ctx, ws, &m); err != nil {
			break
		}
		h.mu.Lock()
		h.info.LastSeen = nowMs()
		h.mu.Unlock()
		h.dispatch(m)
	}

	h.mu.Lock()
	if h.ctrl == nc {
		h.ctrl = nil
		h.info.LastSeen = nowMs()
	}
	h.mu.Unlock()
	ws.CloseNow()
	slog.Info("storage node disconnected", "node", hello.Hello.NodeID)
	h.saveInfo()
	h.s.events.Publish("storage")
	return nil
}

func readMsg(ctx context.Context, ws *websocket.Conn, v any) error {
	typ, data, err := ws.Read(ctx)
	if err != nil {
		return err
	}
	if typ != websocket.MessageText {
		return errors.New("unexpected binary message")
	}
	return json.Unmarshal(data, v)
}

func (h *Hub) writer(nc *nodeConn) {
	for {
		select {
		case <-nc.ctx.Done():
			return
		case m := <-nc.send:
			b, _ := json.Marshal(m)
			wctx, cancel := context.WithTimeout(nc.ctx, 30*time.Second)
			err := nc.ws.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				nc.cancel()
				nc.ws.CloseNow()
				return
			}
		}
	}
}

func (h *Hub) pinger(nc *nodeConn) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-nc.ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(nc.ctx, 15*time.Second)
			err := nc.ws.Ping(pctx)
			cancel()
			if err != nil {
				slog.Warn("storage node ping failed", "err", err)
				nc.cancel()
				nc.ws.CloseNow()
				return
			}
			h.mu.Lock()
			h.info.LastSeen = nowMs()
			h.mu.Unlock()
		}
	}
}

func (h *Hub) dispatch(m proto.Msg) {
	db := h.s.db
	switch m.T {
	case proto.TStats:
		if m.Stats != nil {
			h.mu.Lock()
			h.info.Stats = *m.Stats
			h.mu.Unlock()
			h.saveInfo()
			h.s.events.Publish("storage")
		}
	case proto.TStored:
		var size int64
		var mime string
		if err := db.QueryRow(`SELECT size, mime FROM blobs WHERE sha256 = ?`, m.Sha).Scan(&size, &mime); err != nil {
			// purged meanwhile: ask the node to drop it
			db.Exec(`INSERT OR IGNORE INTO node_deletes (sha256, created_at) VALUES (?, ?)`, m.Sha, nowMs())
			go h.flushDeletes()
			return
		}
		if m.Size != size {
			slog.Error("node stored a blob with a wrong size", "sha", m.Sha, "want", size, "got", m.Size)
			return
		}
		db.Exec(`UPDATE blobs SET state = 'stored', stored_at = ?, sync_error = '' WHERE sha256 = ?`, nowMs(), m.Sha)
		go h.s.store.enforceLimits(0)
		go h.deriveIfNeeded(m.Sha, mime)
		h.s.events.Publish("storage", "videos", "music")
	case proto.TFetchFailed:
		db.Exec(`UPDATE blobs SET sync_error = ? WHERE sha256 = ?`, cleanText(m.Error, 500), m.Sha)
		slog.Warn("node failed to fetch blob", "sha", m.Sha, "err", m.Error)
		h.s.events.Publish("storage")
	case proto.TMissing:
		h.mu.Lock()
		asked := h.checks[m.ID]
		delete(h.checks, m.ID)
		h.mu.Unlock()
		missing := map[string]bool{}
		for _, sh := range m.Shas {
			missing[sh] = true
		}
		for _, sh := range asked {
			if missing[sh] {
				var local bool
				var size int64
				if err := db.QueryRow(`SELECT local, size FROM blobs WHERE sha256 = ?`, sh).Scan(&local, &size); err != nil {
					continue
				}
				if local {
					db.Exec(`UPDATE blobs SET state = 'buffered' WHERE sha256 = ?`, sh)
					h.send(proto.Msg{T: proto.TFetch, Sha: sh, Size: size})
				} else {
					db.Exec(`UPDATE blobs SET state = 'missing' WHERE sha256 = ?`, sh)
				}
			} else {
				db.Exec(`UPDATE blobs SET state = 'stored' WHERE sha256 = ? AND state = 'missing'`, sh)
			}
		}
		h.s.events.Publish("storage")
	case proto.TProbe:
		if m.Probe == nil {
			return
		}
		p := m.Probe
		meta, _ := json.Marshal(p.Meta)
		var peaks any
		if len(p.Peaks) > 0 {
			b, _ := json.Marshal(p.Peaks)
			peaks = string(b)
		}
		db.Exec(`UPDATE blobs SET duration_ms = COALESCE(NULLIF(?, 0), duration_ms), width = COALESCE(NULLIF(?, 0), width), height = COALESCE(NULLIF(?, 0), height),
			meta = ?, peaks = COALESCE(peaks, ?), derive_state = 'done', derive_at = ? WHERE sha256 = ?`,
			p.DurationMs, p.Width, p.Height, string(meta), peaks, nowMs(), m.Sha)
		h.s.events.Publish("videos", "music", "storage")
	case proto.TDeriveFailed:
		db.Exec(`UPDATE blobs SET derive_state = 'failed', derive_at = ? WHERE sha256 = ?`, nowMs(), m.Sha)
		slog.Warn("node failed to process blob", "sha", m.Sha, "err", m.Error)
	case proto.TServeError:
		h.mu.Lock()
		ps := h.pending[m.ID]
		h.mu.Unlock()
		if ps != nil {
			select {
			case ps.errc <- m.Error:
			default:
			}
		}
	}
}

// reconcile runs after every (re)connect: verify that the node still has the
// files we believe it has, push pending uploads, request missing derivatives.
func (h *Hub) reconcile() {
	db := h.s.db
	var stored []string
	h.s.eachRow(`SELECT sha256 FROM blobs WHERE state IN ('stored', 'missing')`, nil, func(r *sql.Rows) error {
		var sh string
		if err := r.Scan(&sh); err != nil {
			return err
		}
		stored = append(stored, sh)
		return nil
	})
	for i := 0; i < len(stored); i += 1000 {
		batch := stored[i:min(i+1000, len(stored))]
		id := randomID(8)
		h.mu.Lock()
		h.checks[id] = batch
		h.mu.Unlock()
		if err := h.send(proto.Msg{T: proto.TCheck, ID: id, Shas: batch}); err != nil {
			return
		}
	}

	type pend struct {
		sha  string
		size int64
	}
	var pending []pend
	h.s.eachRow(`SELECT sha256, size FROM blobs WHERE state = 'buffered' AND local = 1 ORDER BY created_at`, nil, func(r *sql.Rows) error {
		var p pend
		if err := r.Scan(&p.sha, &p.size); err != nil {
			return err
		}
		pending = append(pending, p)
		return nil
	})
	for _, p := range pending {
		if err := h.send(proto.Msg{T: proto.TFetch, Sha: p.sha, Size: p.size}); err != nil {
			return
		}
	}

	type der struct{ sha, mime string }
	var derive []der
	h.s.eachRow(`SELECT sha256, mime FROM blobs WHERE state = 'stored' AND derive_state IN ('', 'pending')`, nil, func(r *sql.Rows) error {
		var d der
		if err := r.Scan(&d.sha, &d.mime); err != nil {
			return err
		}
		derive = append(derive, d)
		return nil
	})
	for _, d := range derive {
		h.deriveIfNeeded(d.sha, d.mime)
	}
	db.Exec(`UPDATE node_state SET value = value WHERE key = 'info'`)
	h.flushDeletes()
}

// resendPending re-announces uploads that are still waiting for the node
// (e.g. after the node gave up on a flaky connection).
func (h *Hub) resendPending() {
	if !h.online() {
		return
	}
	cutoff := nowMs() - 5*60*1000
	type pend struct {
		sha  string
		size int64
	}
	var list []pend
	h.s.eachRow(`SELECT sha256, size FROM blobs WHERE state = 'buffered' AND local = 1 AND created_at < ?`, []any{cutoff}, func(r *sql.Rows) error {
		var p pend
		if err := r.Scan(&p.sha, &p.size); err != nil {
			return err
		}
		list = append(list, p)
		return nil
	})
	for _, p := range list {
		if h.send(proto.Msg{T: proto.TFetch, Sha: p.sha, Size: p.size}) != nil {
			return
		}
	}
}

// kick asks the node to pull a freshly buffered blob.
func (h *Hub) kick(sha string, size int64) {
	go h.send(proto.Msg{T: proto.TFetch, Sha: sha, Size: size})
}

func (h *Hub) flushDeletes() {
	if !h.online() {
		return
	}
	var shas []string
	h.s.eachRow(`SELECT sha256 FROM node_deletes`, nil, func(r *sql.Rows) error {
		var sh string
		if err := r.Scan(&sh); err != nil {
			return err
		}
		shas = append(shas, sh)
		return nil
	})
	for _, sh := range shas {
		// a blob that was re-uploaded after purging must not be deleted
		var n int
		h.s.db.QueryRow(`SELECT COUNT(*) FROM blobs WHERE sha256 = ?`, sh).Scan(&n)
		if n == 0 {
			if err := h.send(proto.Msg{T: proto.TDelete, Sha: sh}); err != nil {
				return
			}
		}
		h.s.db.Exec(`DELETE FROM node_deletes WHERE sha256 = ?`, sh)
	}
}

func deriveWants(mime string) []string {
	switch {
	case strings.HasPrefix(mime, "video/"):
		return []string{proto.DerivedPoster, proto.DerivedThumb, proto.DerivedPreview}
	case strings.HasPrefix(mime, "image/"):
		return []string{proto.DerivedThumb}
	case strings.HasPrefix(mime, "audio/"):
		return []string{}
	}
	return nil
}

func (h *Hub) deriveIfNeeded(sha, mime string) {
	want := deriveWants(mime)
	if want == nil {
		h.s.db.Exec(`UPDATE blobs SET derive_state = 'done' WHERE sha256 = ?`, sha)
		return
	}
	h.requestDerive(sha, mime, want)
}

func (h *Hub) requestDerive(sha, mime string, want []string) {
	if err := h.send(proto.Msg{T: proto.TDerive, Sha: sha, Mime: mime, Want: want}); err == nil {
		h.s.db.Exec(`UPDATE blobs SET derive_state = CASE WHEN derive_state = 'done' THEN 'done' ELSE 'pending' END, derive_at = ? WHERE sha256 = ?`, nowMs(), sha)
	}
}

// handlePull lets the node download a buffered blob (supports Range for resume).
func (h *Hub) handlePull(w http.ResponseWriter, r *http.Request) error {
	sha := r.PathValue("sha")
	if !proto.ValidSha(sha) {
		return errBad("bad sha")
	}
	f, okf := h.s.store.localFile(sha)
	if !okf {
		return errNotFound("Blob")
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	http.ServeContent(w, r, "", time.Time{}, f)
	return nil
}

// ---- streams: node → server bytes ------------------------------------------

type nodeStream struct {
	ctx context.Context
	ws  *websocket.Conn
	ps  *pendingStream
	hdr proto.StreamHeader
	cur io.Reader
	got int64
	h   *Hub
	id  string
}

// openStream asks the node to send length bytes of a blob starting at off.
func (h *Hub) openStream(ctx context.Context, sha string, off, length int64) (*nodeStream, error) {
	if !h.online() {
		return nil, errNodeOffline
	}
	id := randomID(12)
	ps := &pendingStream{connc: make(chan *websocket.Conn, 1), errc: make(chan string, 1), done: make(chan struct{})}
	h.mu.Lock()
	h.pending[id] = ps
	h.mu.Unlock()
	cleanup := func() {
		h.mu.Lock()
		delete(h.pending, id)
		h.mu.Unlock()
	}
	fail := func() {
		cleanup()
		// release a data socket that may have arrived concurrently
		ps.once.Do(func() { close(ps.done) })
	}
	if err := h.send(proto.Msg{T: proto.TServe, ID: id, Sha: sha, Off: off, Len: length}); err != nil {
		fail()
		return nil, err
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	var ws *websocket.Conn
	select {
	case ws = <-ps.connc:
	case e := <-ps.errc:
		fail()
		if e == "not_found" {
			return nil, errBlobMissing
		}
		return nil, errors.New(e)
	case <-timer.C:
		fail()
		return nil, errors.New("storage node did not respond in time")
	case <-ctx.Done():
		fail()
		return nil, ctx.Err()
	}
	cleanup()
	ns := &nodeStream{ctx: ctx, ws: ws, ps: ps, h: h, id: id}
	var hdr proto.StreamHeader
	if err := readMsg(ctx, ws, &hdr); err != nil {
		ns.Close()
		return nil, err
	}
	if hdr.Error != "" {
		ns.Close()
		if hdr.Error == "not_found" {
			return nil, errBlobMissing
		}
		return nil, errors.New(hdr.Error)
	}
	if hdr.Off != off || hdr.Len != length {
		ns.Close()
		return nil, errors.New("node answered with a different range")
	}
	ns.hdr = hdr
	return ns, nil
}

func (ns *nodeStream) Read(p []byte) (int, error) {
	for {
		if ns.cur != nil {
			n, err := ns.cur.Read(p)
			ns.got += int64(n)
			if errors.Is(err, io.EOF) {
				ns.cur = nil
				if n > 0 {
					return n, nil
				}
				continue
			}
			return n, err
		}
		typ, r, err := ns.ws.Reader(ns.ctx)
		if err != nil {
			return 0, err
		}
		if typ == websocket.MessageText {
			var end proto.StreamEnd
			if err := json.NewDecoder(r).Decode(&end); err != nil {
				return 0, err
			}
			if end.Error != "" {
				return 0, errors.New(end.Error)
			}
			if ns.got != ns.hdr.Len {
				return 0, io.ErrUnexpectedEOF
			}
			return 0, io.EOF
		}
		ns.cur = r
	}
}

func (ns *nodeStream) Close() error {
	ns.ps.once.Do(func() { close(ns.ps.done) })
	if ns.got == ns.hdr.Len && ns.hdr.Len > 0 {
		return ns.ws.Close(websocket.StatusNormalClosure, "")
	}
	return ns.ws.CloseNow()
}

// handleStream receives the data websocket the node opens for a serve request.
func (h *Hub) handleStream(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	h.mu.Lock()
	ps := h.pending[id]
	h.mu.Unlock()
	if ps == nil {
		return errNotFound("Stream")
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return nil
	}
	ws.SetReadLimit(4 << 20)
	select {
	case ps.connc <- ws:
	default:
		ws.Close(websocket.StatusPolicyViolation, "duplicate stream")
		return nil
	}
	// keep the handler alive while the consumer reads from the socket
	select {
	case <-ps.done:
	case <-h.s.ctx.Done():
	}
	ws.CloseNow()
	return nil
}

// handleDerivedPush receives a poster/thumb/preview generated by the node.
func (h *Hub) handleDerivedPush(w http.ResponseWriter, r *http.Request) error {
	sha, name := r.PathValue("sha"), r.PathValue("name")
	if !proto.ValidSha(sha) || !proto.ValidDerived(name) {
		return errBad("bad derived name")
	}
	if _, err := h.s.getBlob(sha); err != nil {
		return err
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return nil
	}
	ws.SetReadLimit(4 << 20)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	var hdr proto.StreamHeader
	if err := readMsg(ctx, ws, &hdr); err != nil {
		ws.CloseNow()
		return nil
	}
	limit := int64(10 << 20)
	if name == proto.DerivedPreview {
		limit = 1 << 30
	}
	if hdr.Len <= 0 || hdr.Len > limit {
		ws.Close(websocket.StatusPolicyViolation, "bad size")
		return nil
	}
	ns := &nodeStream{ctx: ctx, ws: ws, hdr: hdr, ps: &pendingStream{done: make(chan struct{})}}
	if err := h.s.store.saveDerived(sha, name, ns, hdr.Len); err != nil || ns.got != hdr.Len {
		slog.Warn("derived push failed", "sha", sha, "name", name, "err", err)
		ws.Close(websocket.StatusInternalError, "save failed")
		return nil
	}
	ws.Close(websocket.StatusNormalClosure, "")
	if name == proto.DerivedPreview {
		go h.s.store.enforceLimits(0)
	}
	h.s.events.Publish("videos", "music")
	return nil
}

// handleBackup streams a consistent snapshot of the database to the node.
func (h *Hub) handleBackup(w http.ResponseWriter, r *http.Request) error {
	tmp, err := h.s.store.tmpFile("backup")
	if err != nil {
		return err
	}
	name := tmp.Name()
	tmp.Close()
	os.Remove(name)
	defer os.Remove(name)
	if _, err := h.s.db.Exec(`VACUUM INTO ?`, name); err != nil {
		return err
	}
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("X-Accel-Buffering", "no")
	http.ServeContent(w, r, "", time.Time{}, f)
	return nil
}
