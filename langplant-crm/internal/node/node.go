// Package node implements crm-node: the storage service that runs on the PC.
// It keeps the authoritative copy of every file, dials out to the VPS (no
// inbound ports, works behind NAT / grey IP) and serves bytes on request.
package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"langplant-crm/internal/config"
	"langplant-crm/internal/proto"
	"langplant-crm/internal/sysutil"
)

// Version is set at build time via -ldflags.
var Version = "dev"

type Node struct {
	cfg    config.Node
	client *http.Client
	wsBase string
	id     string

	mu     sync.Mutex
	out    chan proto.Msg // control messages for the current connection, nil when offline
	online atomic.Bool

	fetchMu  sync.Mutex
	fetching map[string]bool
	fetchQ   chan proto.Msg
	deriveQ  chan proto.Msg
	deriveN  atomic.Int32

	blobs      atomic.Int64
	bytes      atomic.Int64
	lastBackup atomic.Int64
}

func New(cfg config.Node) (*Node, error) {
	n := &Node{
		cfg:      cfg,
		client:   &http.Client{Transport: http.DefaultTransport},
		fetching: map[string]bool{},
		fetchQ:   make(chan proto.Msg, 100000),
		deriveQ:  make(chan proto.Msg, 100000),
	}
	switch {
	case strings.HasPrefix(cfg.ServerURL, "https://"):
		n.wsBase = "wss://" + strings.TrimPrefix(cfg.ServerURL, "https://")
	case strings.HasPrefix(cfg.ServerURL, "http://"):
		n.wsBase = "ws://" + strings.TrimPrefix(cfg.ServerURL, "http://")
	default:
		return nil, fmt.Errorf("NODE_SERVER_URL must start with https:// or http://")
	}
	for _, d := range []string{"blobs", "tmp", "trash", "derived", "backups"} {
		if err := os.MkdirAll(filepath.Join(cfg.DataDir, d), 0o755); err != nil {
			return nil, err
		}
	}
	idPath := filepath.Join(cfg.DataDir, "node-id")
	if b, err := os.ReadFile(idPath); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		n.id = strings.TrimSpace(string(b))
	} else {
		buf := make([]byte, 8)
		rand.Read(buf)
		n.id = hex.EncodeToString(buf)
		if err := os.WriteFile(idPath, []byte(n.id+"\n"), 0o644); err != nil {
			return nil, err
		}
	}
	n.scan()
	return n, nil
}

func (n *Node) blobPath(sha string) string {
	return filepath.Join(n.cfg.DataDir, "blobs", sha[:2], sha)
}

func (n *Node) derivedPath(sha, name string) string {
	return filepath.Join(n.cfg.DataDir, "derived", sha[:2], sha, name)
}

// scan counts stored blobs and finds the latest backup.
func (n *Node) scan() {
	var count, size int64
	filepath.WalkDir(filepath.Join(n.cfg.DataDir, "blobs"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			count++
			size += info.Size()
		}
		return nil
	})
	n.blobs.Store(count)
	n.bytes.Store(size)
	// leftovers of interrupted downloads are kept in tmp/ for resume
	if ents, err := os.ReadDir(filepath.Join(n.cfg.DataDir, "backups")); err == nil {
		for _, e := range ents {
			if info, err := e.Info(); err == nil && strings.HasSuffix(e.Name(), ".db") {
				if t := info.ModTime().UnixMilli(); t > n.lastBackup.Load() {
					n.lastBackup.Store(t)
				}
			}
		}
	}
	slog.Info("storage scanned", "blobs", count, "bytes", size, "dir", n.cfg.DataDir)
}

func (n *Node) stats() proto.Stats {
	free, total, _ := sysutil.DiskUsage(n.cfg.DataDir)
	return proto.Stats{
		DiskFree:  free,
		DiskTotal: total,
		Blobs:     n.blobs.Load(),
		Bytes:     n.bytes.Load(),
		Queue:     len(n.fetchQ),
		Derive:    int(n.deriveN.Load()),
		LastBack:  n.lastBackup.Load(),
	}
}

// Run keeps the node connected until ctx is cancelled.
func (n *Node) Run(ctx context.Context) error {
	for i := 0; i < n.cfg.Downloads; i++ {
		go n.fetchWorker(ctx)
	}
	go n.deriveWorker(ctx)
	go n.backupLoop(ctx)
	go n.trashLoop(ctx)

	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := n.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		slog.Warn("disconnected from server, retrying", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff + jitter(backoff/2)):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
	return nil
}

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	b := make([]byte, 2)
	rand.Read(b)
	return time.Duration(int64(b[0])<<8|int64(b[1])) * d / 65536
}

func (n *Node) authHeader() http.Header {
	return http.Header{"Authorization": []string{"Bearer " + n.cfg.Token}, "User-Agent": []string{"crm-node/" + Version}}
}

func (n *Node) dial(ctx context.Context, path string) (*websocket.Conn, error) {
	ws, resp, err := websocket.Dial(ctx, n.wsBase+path, &websocket.DialOptions{
		HTTPClient:      n.client,
		HTTPHeader:      n.authHeader(),
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("server rejected NODE_TOKEN (401)")
		}
		return nil, err
	}
	return ws, nil
}

// session runs one control connection.
func (n *Node) session(ctx context.Context) error {
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	ws, err := n.dial(dctx, "/api/node/ws")
	cancel()
	if err != nil {
		return err
	}
	ws.SetReadLimit(64 << 20)
	sctx, scancel := context.WithCancel(ctx)
	defer scancel()
	defer ws.CloseNow()

	hello := proto.Msg{T: proto.THello, Hello: &proto.Hello{Proto: proto.Version, Version: Version, NodeID: n.id, Stats: n.stats()}}
	b, _ := json.Marshal(hello)
	if err := ws.Write(sctx, websocket.MessageText, b); err != nil {
		return err
	}

	out := make(chan proto.Msg, 4096)
	n.mu.Lock()
	n.out = out
	n.mu.Unlock()
	n.online.Store(true)
	defer func() {
		n.online.Store(false)
		n.mu.Lock()
		if n.out == out {
			n.out = nil
		}
		n.mu.Unlock()
	}()
	slog.Info("connected to server", "url", n.cfg.ServerURL, "node", n.id)

	errc := make(chan error, 3)
	go func() { // writer
		for {
			select {
			case <-sctx.Done():
				return
			case m := <-out:
				b, _ := json.Marshal(m)
				wctx, cancel := context.WithTimeout(sctx, 30*time.Second)
				err := ws.Write(wctx, websocket.MessageText, b)
				cancel()
				if err != nil {
					errc <- err
					return
				}
			}
		}
	}()
	go func() { // keepalive + periodic stats
		ping := time.NewTicker(20 * time.Second)
		stats := time.NewTicker(time.Minute)
		defer ping.Stop()
		defer stats.Stop()
		for {
			select {
			case <-sctx.Done():
				return
			case <-ping.C:
				pctx, cancel := context.WithTimeout(sctx, 15*time.Second)
				err := ws.Ping(pctx)
				cancel()
				if err != nil {
					errc <- fmt.Errorf("ping: %w", err)
					return
				}
			case <-stats.C:
				st := n.stats()
				n.send(proto.Msg{T: proto.TStats, Stats: &st})
			}
		}
	}()
	go func() { // reader
		for {
			_, data, err := ws.Read(sctx)
			if err != nil {
				errc <- err
				return
			}
			var m proto.Msg
			if err := json.Unmarshal(data, &m); err != nil {
				slog.Warn("bad control message", "err", err)
				continue
			}
			n.handle(sctx, m)
		}
	}()
	return <-errc
}

// send queues a control message; it is dropped when offline (the server
// re-synchronises state on every reconnect).
func (n *Node) send(m proto.Msg) {
	n.mu.Lock()
	out := n.out
	n.mu.Unlock()
	if out == nil {
		return
	}
	select {
	case out <- m:
	case <-time.After(10 * time.Second):
		slog.Warn("control queue full, dropping message", "type", m.T)
	}
}

func (n *Node) handle(ctx context.Context, m proto.Msg) {
	switch m.T {
	case proto.TWelcome:
	case proto.TFetch:
		if !proto.ValidSha(m.Sha) {
			return
		}
		n.fetchMu.Lock()
		dup := n.fetching[m.Sha]
		if !dup {
			n.fetching[m.Sha] = true
		}
		n.fetchMu.Unlock()
		if dup {
			return
		}
		select {
		case n.fetchQ <- m:
		default:
			n.fetchMu.Lock()
			delete(n.fetching, m.Sha)
			n.fetchMu.Unlock()
		}
	case proto.TServe:
		go n.serve(ctx, m)
	case proto.TDerive:
		if !proto.ValidSha(m.Sha) {
			return
		}
		n.deriveN.Add(1)
		select {
		case n.deriveQ <- m:
		default:
			n.deriveN.Add(-1)
		}
	case proto.TCheck:
		missing := []string{}
		for _, sh := range m.Shas {
			if !proto.ValidSha(sh) {
				continue
			}
			if _, err := os.Stat(n.blobPath(sh)); err != nil {
				missing = append(missing, sh)
			}
		}
		go n.send(proto.Msg{T: proto.TMissing, ID: m.ID, Shas: missing})
	case proto.TDelete:
		if proto.ValidSha(m.Sha) {
			n.trash(m.Sha)
		}
	}
}

// ---- fetching uploads from the VPS buffer ----------------------------------

var errGone = errors.New("blob is no longer on the server")

func (n *Node) fetchWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-n.fetchQ:
			n.fetchWithRetry(ctx, m)
			n.fetchMu.Lock()
			delete(n.fetching, m.Sha)
			n.fetchMu.Unlock()
		}
	}
}

func (n *Node) fetchWithRetry(ctx context.Context, m proto.Msg) {
	delay := 2 * time.Second
	for attempt := 1; ; attempt++ {
		err := n.fetch(ctx, m.Sha, m.Size)
		if err == nil || errors.Is(err, errGone) || ctx.Err() != nil {
			return
		}
		slog.Warn("fetch failed", "sha", m.Sha[:12], "attempt", attempt, "err", err)
		if attempt >= 8 {
			n.send(proto.Msg{T: proto.TFetchFailed, Sha: m.Sha, Error: err.Error()})
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay *= 2
	}
}

func (n *Node) fetch(ctx context.Context, sha string, size int64) error {
	final := n.blobPath(sha)
	if st, err := os.Stat(final); err == nil && st.Size() == size {
		n.send(proto.Msg{T: proto.TStored, Sha: sha, Size: size})
		return nil
	}
	if free, _, err := sysutil.DiskUsage(n.cfg.DataDir); err == nil && free < size+512<<20 {
		return fmt.Errorf("not enough disk space on the storage PC")
	}
	part := filepath.Join(n.cfg.DataDir, "tmp", sha+".part")
	var offset int64
	if st, err := os.Stat(part); err == nil {
		offset = st.Size()
		if offset > size {
			os.Remove(part)
			offset = 0
		}
	}
	if offset < size {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.cfg.ServerURL+"/api/node/blob/"+sha, nil)
		if err != nil {
			return err
		}
		req.Header = n.authHeader()
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		resp, err := n.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		flags := os.O_WRONLY | os.O_CREATE
		switch resp.StatusCode {
		case http.StatusPartialContent:
			flags |= os.O_APPEND
		case http.StatusOK:
			flags |= os.O_TRUNC
			offset = 0
		case http.StatusNotFound:
			return errGone
		default:
			return fmt.Errorf("server answered %s", resp.Status)
		}
		f, err := os.OpenFile(part, flags, 0o644)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, resp.Body)
		if err == nil {
			err = f.Sync()
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	got, err := hashFile(part)
	if err != nil {
		return err
	}
	if st, _ := os.Stat(part); st == nil || st.Size() != size || got != sha {
		os.Remove(part)
		return fmt.Errorf("checksum mismatch, downloading again")
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	if err := os.Rename(part, final); err != nil {
		return err
	}
	syncDir(filepath.Dir(final))
	n.blobs.Add(1)
	n.bytes.Add(size)
	slog.Info("stored", "sha", sha[:12], "size", size)
	n.send(proto.Msg{T: proto.TStored, Sha: sha, Size: size})
	return nil
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}

// ---- serving bytes to the VPS ----------------------------------------------

func (n *Node) serve(ctx context.Context, m proto.Msg) {
	if !proto.ValidSha(m.Sha) {
		return
	}
	f, err := os.Open(n.blobPath(m.Sha))
	if err != nil {
		n.send(proto.Msg{T: proto.TServeError, ID: m.ID, Error: "not_found"})
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || m.Off < 0 || m.Len <= 0 || m.Off+m.Len > st.Size() {
		n.send(proto.Msg{T: proto.TServeError, ID: m.ID, Error: "bad_range"})
		return
	}
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	ws, err := n.dial(dctx, "/api/node/stream/"+m.ID)
	cancel()
	if err != nil {
		slog.Warn("open stream", "err", err)
		return
	}
	defer ws.CloseNow()
	if _, err := f.Seek(m.Off, io.SeekStart); err != nil {
		return
	}
	if err := streamFile(ctx, ws, proto.StreamHeader{Size: st.Size(), Off: m.Off, Len: m.Len}, io.LimitReader(f, m.Len)); err != nil {
		return
	}
	ws.Close(websocket.StatusNormalClosure, "")
}

// streamFile writes header, binary chunks and the end marker to ws.
func streamFile(ctx context.Context, ws *websocket.Conn, hdr proto.StreamHeader, r io.Reader) error {
	b, _ := json.Marshal(hdr)
	if err := ws.Write(ctx, websocket.MessageText, b); err != nil {
		return err
	}
	buf := make([]byte, proto.StreamChunk)
	var sent int64
	for sent < hdr.Len {
		k, err := io.ReadFull(r, buf[:min(int64(len(buf)), hdr.Len-sent)])
		if k > 0 {
			// a stalled reader (paused video) eventually times out; the browser re-requests
			wctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			werr := ws.Write(wctx, websocket.MessageBinary, buf[:k])
			cancel()
			if werr != nil {
				return werr
			}
			sent += int64(k)
		}
		if err != nil && sent < hdr.Len {
			end, _ := json.Marshal(proto.StreamEnd{Error: "read error: " + err.Error()})
			ws.Write(ctx, websocket.MessageText, end)
			return err
		}
	}
	end, _ := json.Marshal(proto.StreamEnd{Done: true})
	return ws.Write(ctx, websocket.MessageText, end)
}

// ---- deletion ---------------------------------------------------------------

// trash moves a blob purged on the server into trash/, kept NODE_TRASH_DAYS days.
func (n *Node) trash(sha string) {
	src := n.blobPath(sha)
	st, err := os.Stat(src)
	if err != nil {
		return
	}
	dst := filepath.Join(n.cfg.DataDir, "trash", time.Now().Format("20060102-150405")+"-"+sha)
	if err := os.Rename(src, dst); err != nil {
		slog.Warn("trash", "sha", sha, "err", err)
		return
	}
	os.RemoveAll(filepath.Dir(n.derivedPath(sha, "x")))
	n.blobs.Add(-1)
	n.bytes.Add(-st.Size())
	slog.Info("moved to trash", "sha", sha[:12])
}

func (n *Node) trashLoop(ctx context.Context) {
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		cutoff := time.Now().AddDate(0, 0, -n.cfg.TrashDays)
		dir := filepath.Join(n.cfg.DataDir, "trash")
		if ents, err := os.ReadDir(dir); err == nil {
			for _, e := range ents {
				if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
					os.Remove(filepath.Join(dir, e.Name()))
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ---- database backups -------------------------------------------------------

func (n *Node) backupLoop(ctx context.Context) {
	if n.cfg.BackupInterval <= 0 {
		return
	}
	// first backup shortly after start if the last one is old
	wait := time.Minute
	if last := n.lastBackup.Load(); last > 0 {
		next := time.UnixMilli(last).Add(n.cfg.BackupInterval)
		if d := time.Until(next); d > wait {
			wait = d
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err := n.backup(ctx); err != nil {
			slog.Warn("database backup failed", "err", err)
			wait = 10 * time.Minute
			continue
		}
		wait = n.cfg.BackupInterval
	}
}

// BackupNow pulls a database snapshot immediately.
func (n *Node) BackupNow(ctx context.Context) error { return n.backup(ctx) }

func (n *Node) backup(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.cfg.ServerURL+"/api/node/backup", nil)
	if err != nil {
		return err
	}
	req.Header = n.authHeader()
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server answered %s", resp.Status)
	}
	dir := filepath.Join(n.cfg.DataDir, "backups")
	name := filepath.Join(dir, "crm-"+time.Now().Format("20060102-150405")+".db")
	f, err := os.Create(name + ".tmp")
	if err != nil {
		return err
	}
	_, err = io.Copy(f, resp.Body)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(name + ".tmp")
		return err
	}
	if err := os.Rename(name+".tmp", name); err != nil {
		return err
	}
	n.lastBackup.Store(time.Now().UnixMilli())
	slog.Info("database backup saved", "file", name)
	// prune old backups
	if ents, err := os.ReadDir(dir); err == nil {
		var dbs []string
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), "crm-") && strings.HasSuffix(e.Name(), ".db") {
				dbs = append(dbs, e.Name())
			}
		}
		for i := 0; i < len(dbs)-max(n.cfg.BackupKeep, 1); i++ {
			os.Remove(filepath.Join(dir, dbs[i]))
		}
	}
	st := n.stats()
	n.send(proto.Msg{T: proto.TStats, Stats: &st})
	return nil
}
