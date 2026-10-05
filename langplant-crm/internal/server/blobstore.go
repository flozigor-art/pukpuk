package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"langplant-crm/internal/proto"
	"langplant-crm/internal/sysutil"
)

// BlobStore manages files kept on the VPS:
//
//	blobs/     uploaded files: pinned until the node confirms them, then an LRU cache
//	uploads/   partial uploads (*.part)
//	derived/   posters and thumbnails (kept), preview proxies (LRU)
//	tmp/       scratch space for warm-ups, derived pushes and DB snapshots
type BlobStore struct {
	s       *Server
	root    string
	mu      sync.Mutex // serialises admission and eviction
	touchMu sync.Mutex
	touched map[string]time.Time
	warmMu  sync.Mutex
	warming map[string]bool
}

var errInsufficientStorage = &apiError{http.StatusInsufficientStorage, "buffer_full", ""}

func newBlobStore(s *Server, root string) (*BlobStore, error) {
	for _, d := range []string{"blobs", "uploads", "derived", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return nil, err
		}
	}
	// leftovers from a crash
	if ents, err := os.ReadDir(filepath.Join(root, "tmp")); err == nil {
		for _, e := range ents {
			os.RemoveAll(filepath.Join(root, "tmp", e.Name()))
		}
	}
	return &BlobStore{s: s, root: root, touched: map[string]time.Time{}, warming: map[string]bool{}}, nil
}

func (b *BlobStore) blobPath(sha string) string {
	return filepath.Join(b.root, "blobs", sha[:2], sha)
}

func (b *BlobStore) derivedPath(sha, name string) string {
	return filepath.Join(b.root, "derived", sha[:2], sha, name)
}

func (b *BlobStore) uploadPath(id string) string {
	return filepath.Join(b.root, "uploads", id+".part")
}

func (b *BlobStore) tmpFile(prefix string) (*os.File, error) {
	return os.CreateTemp(filepath.Join(b.root, "tmp"), prefix+"-*")
}

func (b *BlobStore) sum(q string) int64 {
	var n sql.NullInt64
	b.s.db.QueryRow(q).Scan(&n)
	return n.Int64
}

// Usage numbers in bytes.
type StoreUsage struct {
	Pinned    int64 `json:"pinned"`    // local files not yet confirmed by the node
	Uploading int64 `json:"uploading"` // reserved by unfinished uploads
	Cached    int64 `json:"cached"`    // local copies of files already on the node
	Previews  int64 `json:"previews"`
	DiskFree  int64 `json:"disk_free"`
	DiskTotal int64 `json:"disk_total"`
}

func (b *BlobStore) usage() StoreUsage {
	u := StoreUsage{
		Pinned:    b.sum(`SELECT SUM(size) FROM blobs WHERE local = 1 AND state != 'stored'`),
		Uploading: b.sum(`SELECT SUM(size) FROM uploads`),
		Cached:    b.sum(`SELECT SUM(size) FROM blobs WHERE local = 1 AND state = 'stored'`),
		Previews:  b.sum(`SELECT SUM(size) FROM derived_files WHERE name = 'preview.mp4'`),
	}
	u.DiskFree, u.DiskTotal, _ = sysutil.DiskUsage(b.root)
	return u
}

// admit reserves room for a new upload of size bytes, evicting cache if needed.
func (b *BlobStore) admit(size int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	cfg := b.s.cfg
	u := b.usage()
	if u.Pinned+u.Uploading+size > cfg.BufferMax {
		msg := fmt.Sprintf("Буфер на сервере заполнен: %s из %s ждут переноса в хранилище.", humanBytes(u.Pinned+u.Uploading), humanBytes(cfg.BufferMax))
		if !b.s.hub.online() {
			msg += " Хранилище (комп) сейчас офлайн — загрузка продолжится, когда оно подключится."
		} else {
			msg += " Файлы уже переносятся — попробуйте через несколько минут."
		}
		return &apiError{http.StatusInsufficientStorage, "buffer_full", msg}
	}
	if u.DiskFree >= 0 && u.DiskFree-size < cfg.DiskReserve {
		b.evictLocked(size + cfg.DiskReserve - u.DiskFree)
		u.DiskFree, _, _ = sysutil.DiskUsage(b.root)
		if u.DiskFree-size < cfg.DiskReserve {
			return &apiError{http.StatusInsufficientStorage, "disk_full", "На сервере закончилось место на диске. Дождитесь переноса файлов в хранилище."}
		}
	}
	return nil
}

// enforceLimits trims the cache and the previews to their configured sizes.
func (b *BlobStore) enforceLimits(extra int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.evictLocked(extra)
}

func (b *BlobStore) evictLocked(needFree int64) {
	cfg := b.s.cfg
	cached := b.sum(`SELECT SUM(size) FROM blobs WHERE local = 1 AND state = 'stored'`)
	over := cached - cfg.CacheMax
	if needFree > over {
		over = needFree
	}
	if over > 0 {
		var victims []struct {
			sha  string
			size int64
		}
		b.s.eachRow(`SELECT sha256, size FROM blobs WHERE local = 1 AND state = 'stored' ORDER BY last_access ASC`, nil, func(r *sql.Rows) error {
			var v struct {
				sha  string
				size int64
			}
			if err := r.Scan(&v.sha, &v.size); err != nil {
				return err
			}
			victims = append(victims, v)
			return nil
		})
		for _, v := range victims {
			if over <= 0 {
				break
			}
			if err := os.Remove(b.blobPath(v.sha)); err != nil && !os.IsNotExist(err) {
				slog.Warn("evict blob", "sha", v.sha, "err", err)
				continue
			}
			b.s.db.Exec(`UPDATE blobs SET local = 0 WHERE sha256 = ?`, v.sha)
			over -= v.size
		}
	}
	previews := b.sum(`SELECT SUM(size) FROM derived_files WHERE name = 'preview.mp4'`)
	pover := previews - cfg.PreviewMax
	if pover > 0 {
		var victims []struct {
			sha  string
			size int64
		}
		b.s.eachRow(`SELECT sha256, size FROM derived_files WHERE name = 'preview.mp4' ORDER BY last_access ASC`, nil, func(r *sql.Rows) error {
			var v struct {
				sha  string
				size int64
			}
			if err := r.Scan(&v.sha, &v.size); err != nil {
				return err
			}
			victims = append(victims, v)
			return nil
		})
		for _, v := range victims {
			if pover <= 0 {
				break
			}
			os.Remove(b.derivedPath(v.sha, proto.DerivedPreview))
			b.s.db.Exec(`DELETE FROM derived_files WHERE sha256 = ? AND name = ?`, v.sha, proto.DerivedPreview)
			pover -= v.size
		}
	}
}

// touch records an access for LRU purposes (at most once a minute per blob).
func (b *BlobStore) touch(sha string, derived string) {
	key := sha + "/" + derived
	b.touchMu.Lock()
	last := b.touched[key]
	now := time.Now()
	if now.Sub(last) < time.Minute {
		b.touchMu.Unlock()
		return
	}
	b.touched[key] = now
	if len(b.touched) > 10000 {
		b.touched = map[string]time.Time{}
	}
	b.touchMu.Unlock()
	if derived == "" {
		b.s.db.Exec(`UPDATE blobs SET last_access = ? WHERE sha256 = ?`, now.UnixMilli(), sha)
	} else {
		b.s.db.Exec(`UPDATE derived_files SET last_access = ? WHERE sha256 = ? AND name = ?`, now.UnixMilli(), sha, derived)
	}
}

// localFile opens the local copy of a blob if there is one.
func (b *BlobStore) localFile(sha string) (*os.File, bool) {
	f, err := os.Open(b.blobPath(sha))
	if err != nil {
		return nil, false
	}
	return f, true
}

// saveDerived stores a derived file from r (validated by the caller).
func (b *BlobStore) saveDerived(sha, name string, r io.Reader, limit int64) error {
	tmp, err := b.tmpFile("derived")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, io.LimitReader(r, limit+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > limit {
		return errBad("Файл слишком большой")
	}
	if n == 0 {
		return errBad("Пустой файл")
	}
	dst := b.derivedPath(sha, name)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return err
	}
	now := nowMs()
	_, err = b.s.db.Exec(`INSERT INTO derived_files (sha256, name, size, created_at, last_access) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(sha256, name) DO UPDATE SET size = excluded.size, created_at = excluded.created_at`, sha, name, n, now, now)
	return err
}

func (b *BlobStore) hasDerived(sha, name string) bool {
	var n int
	b.s.db.QueryRow(`SELECT COUNT(*) FROM derived_files WHERE sha256 = ? AND name = ?`, sha, name).Scan(&n)
	if n == 0 {
		return false
	}
	if _, err := os.Stat(b.derivedPath(sha, name)); err != nil {
		b.s.db.Exec(`DELETE FROM derived_files WHERE sha256 = ? AND name = ?`, sha, name)
		return false
	}
	return true
}

// removeBlob deletes local copies and derived files of a blob that is no
// longer referenced, and queues its deletion on the node.
func (b *BlobStore) removeBlob(sha string) {
	os.Remove(b.blobPath(sha))
	os.RemoveAll(filepath.Dir(b.derivedPath(sha, "x")))
	b.s.db.Exec(`DELETE FROM blobs WHERE sha256 = ?`, sha)
	b.s.db.Exec(`INSERT OR IGNORE INTO node_deletes (sha256, created_at) VALUES (?, ?)`, sha, nowMs())
	b.s.hub.flushDeletes()
}

// gcBlob removes a blob if nothing references it any more.
func (b *BlobStore) gcBlob(sha string) {
	var n int
	b.s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM assets WHERE sha256 = ?) + (SELECT COUNT(*) FROM tracks WHERE sha256 = ?)`, sha, sha).Scan(&n)
	if n == 0 {
		b.removeBlob(sha)
	}
}

// warm pulls a small blob from the node into the local cache in the background
// so that repeated views (music previews, short clips) are served locally.
func (b *BlobStore) warm(sha string, size int64) {
	if size > b.s.cfg.WarmMax || size > b.s.cfg.CacheMax/4 {
		return
	}
	b.warmMu.Lock()
	if b.warming[sha] {
		b.warmMu.Unlock()
		return
	}
	b.warming[sha] = true
	b.warmMu.Unlock()
	go func() {
		defer func() {
			b.warmMu.Lock()
			delete(b.warming, sha)
			b.warmMu.Unlock()
		}()
		if err := b.fetchFromNode(sha, size); err != nil {
			slog.Debug("warm cache", "sha", sha, "err", err)
		}
	}()
}

func (b *BlobStore) fetchFromNode(sha string, size int64) error {
	if free, _, err := sysutil.DiskUsage(b.root); err == nil && free-size < b.s.cfg.DiskReserve {
		b.enforceLimits(size + b.s.cfg.DiskReserve - free)
	}
	ctx, cancel := context.WithTimeout(b.s.ctx, 15*time.Minute)
	defer cancel()
	ns, err := b.s.hub.openStream(ctx, sha, 0, size)
	if err != nil {
		return err
	}
	defer ns.Close()
	tmp, err := b.tmpFile("warm")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), ns)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != sha {
		return fmt.Errorf("checksum mismatch")
	}
	dst := b.blobPath(sha)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return err
	}
	b.s.db.Exec(`UPDATE blobs SET local = 1, last_access = ? WHERE sha256 = ?`, nowMs(), sha)
	b.enforceLimits(0)
	return nil
}

// cleanupStaleUploads drops partial uploads untouched for longer than maxAge.
func (b *BlobStore) cleanupStaleUploads(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge).UnixMilli()
	var ids []string
	b.s.eachRow(`SELECT id FROM uploads WHERE updated_at < ?`, []any{cutoff}, func(r *sql.Rows) error {
		var id string
		if err := r.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
		return nil
	})
	for _, id := range ids {
		os.Remove(b.uploadPath(id))
		b.s.db.Exec(`DELETE FROM uploads WHERE id = ?`, id)
	}
	// part files without a row
	if ents, err := os.ReadDir(filepath.Join(b.root, "uploads")); err == nil {
		for _, e := range ents {
			id := strings.TrimSuffix(e.Name(), ".part")
			var n int
			b.s.db.QueryRow(`SELECT COUNT(*) FROM uploads WHERE id = ?`, id).Scan(&n)
			if n == 0 {
				if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
					os.Remove(filepath.Join(b.root, "uploads", e.Name()))
				}
			}
		}
	}
}

type BlobInfo struct {
	State      string   `json:"state"`
	Local      bool     `json:"local"`
	DurationMs *int64   `json:"duration_ms"`
	Width      *int64   `json:"width"`
	Height     *int64   `json:"height"`
	Derived    []string `json:"derived"`
	SyncError  string   `json:"sync_error,omitempty"`
}

func (s *Server) blobInfos(shas []string) (map[string]*BlobInfo, error) {
	out := map[string]*BlobInfo{}
	if len(shas) == 0 {
		return out, nil
	}
	want := map[string]bool{}
	for _, sh := range shas {
		want[sh] = true
	}
	// small tables in practice; filter in Go instead of building huge IN lists
	ph := strings.TrimSuffix(strings.Repeat("?,", len(want)), ",")
	args := make([]any, 0, len(want))
	for sh := range want {
		args = append(args, sh)
	}
	if err := s.eachRow(`SELECT sha256, state, local, duration_ms, width, height, sync_error FROM blobs WHERE sha256 IN (`+ph+`)`, args, func(r *sql.Rows) error {
		bi := &BlobInfo{Derived: []string{}}
		var sha string
		var dur, wd, ht nullInt64
		if err := r.Scan(&sha, &bi.State, &bi.Local, &dur, &wd, &ht, &bi.SyncError); err != nil {
			return err
		}
		bi.DurationMs, bi.Width, bi.Height = dur.ptr(), wd.ptr(), ht.ptr()
		out[sha] = bi
		return nil
	}); err != nil {
		return nil, err
	}
	if err := s.eachRow(`SELECT sha256, name FROM derived_files WHERE sha256 IN (`+ph+`)`, args, func(r *sql.Rows) error {
		var sha, name string
		if err := r.Scan(&sha, &name); err != nil {
			return err
		}
		if bi := out[sha]; bi != nil {
			bi.Derived = append(bi.Derived, name)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d Б", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cБ", float64(n)/float64(div), []rune("КМГТП")[exp])
}

var _ = errInsufficientStorage
