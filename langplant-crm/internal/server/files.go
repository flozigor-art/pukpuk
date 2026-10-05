package server

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"langplant-crm/internal/proto"
)

type blobRow struct {
	sha, mime, state string
	size             int64
	local            bool
}

func (s *Server) getBlob(sha string) (*blobRow, error) {
	if !proto.ValidSha(sha) {
		return nil, errBad("Некорректный идентификатор файла")
	}
	b := &blobRow{sha: sha}
	err := s.db.QueryRow(`SELECT size, mime, state, local FROM blobs WHERE sha256 = ?`, sha).Scan(&b.size, &b.mime, &b.state, &b.local)
	if err != nil {
		return nil, errNotFound("Файл")
	}
	return b, nil
}

var errStorageOffline = &apiError{http.StatusServiceUnavailable, "storage_offline", "Хранилище (комп) сейчас офлайн — файл станет доступен, когда оно подключится"}

// handleFile serves an original file: from the local copy when there is one,
// otherwise streamed from the storage node (with Range support).
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) error {
	b, err := s.getBlob(r.PathValue("sha"))
	if err != nil {
		return err
	}
	q := r.URL.Query()
	download := q.Get("dl") == "1"
	name := safeFilename(q.Get("name"))
	if q.Get("name") == "" {
		name = b.sha[:12]
	}
	s.fileHeaders(w, b.mime, name, download)
	return s.serveBlob(w, r, b)
}

func (s *Server) fileHeaders(w http.ResponseWriter, mime, name string, download bool) {
	h := w.Header()
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; media-src 'self'; img-src 'self'")
	h.Set("Cache-Control", "private, max-age=86400")
	if download || !inlineSafe(mime) {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", contentDisposition("attachment", name))
	} else {
		h.Set("Content-Type", mime)
		h.Set("Content-Disposition", contentDisposition("inline", name))
	}
}

func (s *Server) serveBlob(w http.ResponseWriter, r *http.Request, b *blobRow) error {
	etag := `"` + b.sha + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Accept-Ranges", "bytes")
	if f, okf := s.store.localFile(b.sha); okf {
		defer f.Close()
		s.store.touch(b.sha, "")
		http.ServeContent(w, r, "", time.Time{}, f)
		return nil
	}
	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return nil
	}
	if b.state == "missing" {
		return &apiError{http.StatusGone, "missing", "Файл отсутствует в хранилище. Загрузите его заново"}
	}
	if !s.hub.online() {
		return errStorageOffline
	}
	off, length, partial, err := parseRange(r.Header.Get("Range"), b.size)
	if err != nil || (r.Header.Get("If-Range") != "" && r.Header.Get("If-Range") != etag) {
		off, length, partial = 0, b.size, false
	}
	if errors.Is(err, errUnsatisfiable) {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", b.size))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return nil
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
		if partial {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", off, off+length-1, b.size))
			w.WriteHeader(http.StatusPartialContent)
		}
		return nil
	}
	ns, err := s.hub.openStream(r.Context(), b.sha, off, length)
	if err != nil {
		if errors.Is(err, errBlobMissing) {
			s.db.Exec(`UPDATE blobs SET state = 'missing' WHERE sha256 = ? AND local = 0`, b.sha)
			s.events.Publish("storage")
			return &apiError{http.StatusGone, "missing", "Файл не найден в хранилище"}
		}
		if errors.Is(err, errNodeOffline) {
			return errStorageOffline
		}
		if r.Context().Err() != nil {
			return errClientGone
		}
		return &apiError{http.StatusBadGateway, "storage_error", "Хранилище не ответило: " + err.Error()}
	}
	defer ns.Close()
	h := w.Header()
	h.Set("Content-Length", strconv.FormatInt(length, 10))
	h.Set("X-Accel-Buffering", "no")
	if partial {
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", off, off+length-1, b.size))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	s.store.warm(b.sha, b.size)
	if _, err := io.Copy(w, ns); err != nil {
		return errClientGone // headers are sent; nothing else to report
	}
	return nil
}

var errUnsatisfiable = errors.New("range not satisfiable")

// parseRange understands a single "bytes=a-b" range.
func parseRange(h string, size int64) (off, length int64, partial bool, err error) {
	if h == "" {
		return 0, size, false, nil
	}
	if !strings.HasPrefix(h, "bytes=") || strings.Contains(h, ",") {
		return 0, size, false, errors.New("unsupported range")
	}
	spec := strings.TrimSpace(strings.TrimPrefix(h, "bytes="))
	a, bpart, okr := strings.Cut(spec, "-")
	if !okr {
		return 0, size, false, errors.New("bad range")
	}
	if a == "" {
		n, err := strconv.ParseInt(bpart, 10, 64)
		if err != nil || n <= 0 {
			return 0, size, false, errors.New("bad range")
		}
		if n > size {
			n = size
		}
		return size - n, n, true, nil
	}
	start, err := strconv.ParseInt(a, 10, 64)
	if err != nil || start < 0 {
		return 0, size, false, errors.New("bad range")
	}
	if start >= size {
		return 0, 0, false, errUnsatisfiable
	}
	end := size - 1
	if bpart != "" {
		e, err := strconv.ParseInt(bpart, 10, 64)
		if err != nil || e < start {
			return 0, size, false, errors.New("bad range")
		}
		if e < end {
			end = e
		}
	}
	return start, end - start + 1, true, nil
}

// handleDerivedFile serves poster.jpg / thumb.jpg / preview.mp4. A missing
// preview falls back to the original file.
func (s *Server) handleDerivedFile(w http.ResponseWriter, r *http.Request) error {
	sha, name := r.PathValue("sha"), r.PathValue("name")
	if !proto.ValidSha(sha) || !proto.ValidDerived(name) {
		return errNotFound("Файл")
	}
	if s.store.hasDerived(sha, name) {
		f, err := os.Open(s.store.derivedPath(sha, name))
		if err == nil {
			defer f.Close()
			s.store.touch(sha, name)
			h := w.Header()
			h.Set("Cache-Control", "private, max-age=3600")
			h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
			if name == proto.DerivedPreview {
				h.Set("Content-Type", "video/mp4")
			} else {
				h.Set("Content-Type", "image/jpeg")
			}
			http.ServeContent(w, r, "", time.Time{}, f)
			return nil
		}
	}
	if name != proto.DerivedPreview {
		return errNotFound("Превью")
	}
	b, err := s.getBlob(sha)
	if err != nil {
		return err
	}
	if b.state == "stored" && s.hub.online() && strings.HasPrefix(b.mime, "video/") {
		s.hub.requestDerive(sha, b.mime, []string{proto.DerivedPreview})
	}
	s.fileHeaders(w, b.mime, sha[:12], false)
	return s.serveBlob(w, r, b)
}

// handleClientDerived accepts a poster/thumbnail generated in the browser at
// upload time, so lists have pictures before the node has processed the file.
func (s *Server) handleClientDerived(w http.ResponseWriter, r *http.Request) error {
	sha, name := r.PathValue("sha"), r.PathValue("name")
	if !proto.ValidSha(sha) || (name != proto.DerivedPoster && name != proto.DerivedThumb) {
		return errBad("Некорректный запрос")
	}
	if _, err := s.getBlob(sha); err != nil {
		return err
	}
	if s.store.hasDerived(sha, name) {
		return ok(w, map[string]bool{"ok": true, "skipped": true})
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 3<<20+1))
	if err != nil {
		return err
	}
	if len(body) > 3<<20 || !bytes.HasPrefix(body, []byte{0xFF, 0xD8, 0xFF}) {
		return errBad("Ожидается JPEG до 3 МБ")
	}
	if err := s.store.saveDerived(sha, name, bytes.NewReader(body), 3<<20); err != nil {
		return err
	}
	s.events.Publish("videos", "music")
	return ok(w, map[string]bool{"ok": true})
}

// handleClientPeaks stores an audio waveform computed in the browser.
func (s *Server) handleClientPeaks(w http.ResponseWriter, r *http.Request) error {
	sha := r.PathValue("sha")
	if _, err := s.getBlob(sha); err != nil {
		return err
	}
	var req struct {
		Peaks      []int  `json:"peaks"`
		DurationMs *int64 `json:"duration_ms"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if len(req.Peaks) == 0 || len(req.Peaks) > 2000 {
		return errBad("peaks: от 1 до 2000 значений")
	}
	for i, p := range req.Peaks {
		req.Peaks[i] = min(max(p, 0), 100)
	}
	pj, _ := json.Marshal(req.Peaks)
	s.db.Exec(`UPDATE blobs SET peaks = ? WHERE sha256 = ? AND peaks IS NULL`, string(pj), sha)
	if req.DurationMs != nil && *req.DurationMs > 0 {
		s.db.Exec(`UPDATE blobs SET duration_ms = ? WHERE sha256 = ? AND duration_ms IS NULL`, *req.DurationMs, sha)
	}
	s.events.Publish("music")
	return ok(w, map[string]bool{"ok": true})
}

// handleVideoZip streams all current files of a video (or of one language
// version) as an uncompressed zip, fetching from the node as needed.
func (s *Server) handleVideoZip(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	v, err := s.getVideo(id, false)
	if err != nil {
		return err
	}
	where := `a.video_id = ? AND a.deleted_at IS NULL`
	args := []any{id}
	scope := r.URL.Query().Get("variant")
	switch scope {
	case "", "all":
	case "shared":
		where += ` AND a.variant_id IS NULL`
	default:
		vid, err := strconv.ParseInt(scope, 10, 64)
		if err != nil {
			return errBad("variant")
		}
		where += ` AND (a.variant_id = ? OR a.variant_id IS NULL)`
		args = append(args, vid)
	}
	assets, err := s.queryAssets(where, args...)
	if err != nil {
		return err
	}
	if len(assets) == 0 {
		return errNotFound("Файлы")
	}
	langs := map[int64]string{}
	for _, vs := range v.Variants {
		langs[vs.ID] = vs.Lang
	}
	kindNames := map[string]string{}
	s.eachRow(`SELECT key, name FROM asset_kinds`, nil, func(rr *sql.Rows) error {
		var k, n string
		if err := rr.Scan(&k, &n); err != nil {
			return err
		}
		kindNames[k] = n
		return nil
	})
	needNode := false
	for _, a := range assets {
		if a.Blob == nil || !a.Blob.Local {
			needNode = true
		}
	}
	if needNode && !s.hub.online() {
		return errStorageOffline
	}

	folder := safeFilename(v.Code + " " + v.Title)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition("attachment", folder+".zip"))
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Cache-Control", "no-store")
	zw := zip.NewWriter(w)
	used := map[string]bool{}
	for _, a := range assets {
		dir := "общие"
		if a.VariantID != nil {
			dir = langs[*a.VariantID]
		}
		kn := kindNames[a.Kind]
		if kn == "" {
			kn = a.Kind
		}
		p := path.Join(folder, dir, safeFilename(kn), safeFilename(a.Filename))
		if used[p] {
			ext := path.Ext(p)
			p = strings.TrimSuffix(p, ext) + fmt.Sprintf(" (v%d)", a.Version) + ext
		}
		used[p] = true
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: p, Method: zip.Store, Modified: time.UnixMilli(a.CreatedAt)})
		if err != nil {
			return errClientGone
		}
		if err := s.copyBlob(r.Context(), fw, a.Sha, a.Size); err != nil {
			return errClientGone
		}
	}
	zw.Close()
	return nil
}

func (s *Server) copyBlob(ctx context.Context, dst io.Writer, sha string, size int64) error {
	if f, okf := s.store.localFile(sha); okf {
		defer f.Close()
		_, err := io.Copy(dst, f)
		return err
	}
	ns, err := s.hub.openStream(ctx, sha, 0, size)
	if err != nil {
		return err
	}
	defer ns.Close()
	_, err = io.Copy(dst, ns)
	return err
}
