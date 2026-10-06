package server

import (
	"crypto/sha256"
	"database/sql"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Resumable uploads (simplified tus):
//
//	POST   /api/uploads          {filename, size, mime, target} → {id, chunk_size, received}
//	PUT    /api/uploads/{id}     Upload-Offset: N, body = next chunk → {received, done, result?}
//	GET    /api/uploads/{id}     → {received} (resume after a network error)
//	DELETE /api/uploads/{id}     cancel
//
// Chunks are atomic: a chunk interrupted midway is discarded and re-sent.
// The running sha256 state is persisted with every chunk, so finishing an
// upload never re-reads the file.

type uploadTarget struct {
	Type string `json:"type"` // asset | track

	// asset
	VideoID   int64  `json:"video_id,omitempty"`
	VariantID *int64 `json:"variant_id,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Note      string `json:"note,omitempty"`

	// track
	Title      string  `json:"title,omitempty"`
	Artist     string  `json:"artist,omitempty"`
	Album      string  `json:"album,omitempty"`
	BPM        *int64  `json:"bpm,omitempty"`
	Key        string  `json:"musical_key,omitempty"`
	DurationMs *int64  `json:"duration_ms,omitempty"`
	Tags       []int64 `json:"tags,omitempty"`
	Source     string  `json:"source,omitempty"`
	License    string  `json:"license,omitempty"`
	LicenseURL string  `json:"license_url,omitempty"`
	Platforms  []int64 `json:"platforms,omitempty"`
	Notes      string  `json:"notes,omitempty"`
}

var uploadLocks sync.Map // id → *sync.Mutex

func (s *Server) handleCreateUpload(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Filename string       `json:"filename"`
		Size     int64        `json:"size"`
		Mime     string       `json:"mime"`
		Target   uploadTarget `json:"target"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	name := safeFilename(req.Filename)
	if req.Size <= 0 {
		return errBad("Пустой файл")
	}
	if req.Size > s.cfg.MaxFile {
		return errBad("Файл больше допустимого размера (%s)", humanBytes(s.cfg.MaxFile))
	}
	t := req.Target
	switch t.Type {
	case "asset":
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM videos WHERE id = ? AND deleted_at IS NULL`, t.VideoID).Scan(&n)
		if n == 0 {
			return errNotFound("Ролик")
		}
		s.db.QueryRow(`SELECT COUNT(*) FROM asset_kinds WHERE key = ?`, t.Kind).Scan(&n)
		if n == 0 {
			return errBad("Неизвестный тип файла")
		}
		if t.VariantID != nil {
			owner, err := s.videoOfVariant(*t.VariantID)
			if err != nil {
				return err
			}
			if owner != t.VideoID {
				return errBad("Языковая версия принадлежит другому ролику")
			}
		}
	case "track":
		m := detectMime(name, req.Mime)
		if !strings.HasPrefix(m, "audio/") {
			return errBad("В библиотеку музыки загружаются аудиофайлы (MP3)")
		}
	default:
		return errBad("Неизвестное назначение загрузки")
	}
	if err := s.store.admit(req.Size); err != nil {
		return err
	}
	targetJSON, _ := json.Marshal(t)
	id := randomID(18)
	now := nowMs()
	if _, err := s.db.Exec(`INSERT INTO uploads (id, user_id, filename, size, mime, received, target, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?)`,
		id, currentUser(r).ID, name, req.Size, detectMime(name, req.Mime), string(targetJSON), now, now); err != nil {
		return err
	}
	f, err := os.Create(s.store.uploadPath(id))
	if err != nil {
		return err
	}
	f.Close()
	return ok(w, map[string]any{"id": id, "chunk_size": s.cfg.ChunkSize, "received": 0, "size": req.Size})
}

type uploadRow struct {
	id, filename, mime, target string
	userID, size, received     int64
	hashState                  []byte
}

func (s *Server) getUpload(id string) (*uploadRow, error) {
	u := &uploadRow{id: id}
	err := s.db.QueryRow(`SELECT user_id, filename, size, mime, received, hash_state, target FROM uploads WHERE id = ?`, id).
		Scan(&u.userID, &u.filename, &u.size, &u.mime, &u.received, &u.hashState, &u.target)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &apiError{http.StatusNotFound, "upload_not_found", "Загрузка не найдена или устарела — начните заново"}
	}
	return u, err
}

func (s *Server) handleGetUpload(w http.ResponseWriter, r *http.Request) error {
	u, err := s.getUpload(r.PathValue("id"))
	if err != nil {
		return err
	}
	if u.userID != currentUser(r).ID {
		return errForbidden()
	}
	return ok(w, map[string]any{"id": u.id, "received": u.received, "size": u.size, "chunk_size": s.cfg.ChunkSize})
}

func (s *Server) handleDeleteUpload(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	u, err := s.getUpload(id)
	if err != nil {
		return err
	}
	if u.userID != currentUser(r).ID {
		return errForbidden()
	}
	mu := lockFor(id)
	mu.Lock()
	defer mu.Unlock()
	s.db.Exec(`DELETE FROM uploads WHERE id = ?`, id)
	os.Remove(s.store.uploadPath(id))
	return ok(w, map[string]bool{"ok": true})
}

func lockFor(id string) *sync.Mutex {
	m, _ := uploadLocks.LoadOrStore(id, &sync.Mutex{})
	return m.(*sync.Mutex)
}

func (s *Server) handlePutUpload(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	mu := lockFor(id)
	if !mu.TryLock() {
		return errConflict("Этот фрагмент уже загружается")
	}
	defer mu.Unlock()

	u, err := s.getUpload(id)
	if err != nil {
		return err
	}
	if u.userID != currentUser(r).ID {
		return errForbidden()
	}
	off, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	if err != nil || off != u.received {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "offset_mismatch", "message": "Смещение не совпадает", "received": u.received})
		return nil
	}
	remaining := u.size - u.received
	limit := s.cfg.ChunkSize
	if remaining < limit {
		limit = remaining
	}

	h := sha256.New()
	if len(u.hashState) > 0 {
		if err := h.(encoding.BinaryUnmarshaler).UnmarshalBinary(u.hashState); err != nil {
			return err
		}
	} else if u.received > 0 {
		return errConflict("Состояние загрузки потеряно — начните заново")
	}

	f, err := os.OpenFile(s.store.uploadPath(id), os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		f.Close()
		return err
	}
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r.Body, limit))
	if err == nil {
		// the chunk must not be larger than announced
		var probe [1]byte
		if m, _ := r.Body.Read(probe[:]); m > 0 {
			err = errBad("Фрагмент больше допустимого размера")
		}
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) {
			return err
		}
		return &apiError{http.StatusBadRequest, "chunk_interrupted", "Фрагмент получен не полностью, повторите"}
	}
	if n == 0 && remaining > 0 {
		return errBad("Пустой фрагмент")
	}
	state, err := h.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		return err
	}
	received := u.received + n
	if _, err := s.db.Exec(`UPDATE uploads SET received = ?, hash_state = ?, updated_at = ? WHERE id = ?`, received, state, nowMs(), id); err != nil {
		return err
	}
	if received < u.size {
		return ok(w, map[string]any{"received": received, "done": false})
	}
	// the new file or track becomes one undoable step
	step, berr := s.undoBegin(currentUser(r).ID, "action")
	result, err := s.finishUpload(u, hex.EncodeToString(h.Sum(nil)), currentUser(r))
	if berr == nil {
		if st, _ := s.undoEnd(step); st != nil && err == nil {
			result["undo_step"] = st.ID
		}
	}
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"received": received, "done": true, "result": result})
}

func (s *Server) finishUpload(u *uploadRow, sha string, user *User) (map[string]any, error) {
	var target uploadTarget
	if err := json.Unmarshal([]byte(u.target), &target); err != nil {
		return nil, err
	}
	part := s.store.uploadPath(u.id)
	dst := s.store.blobPath(sha)
	now := nowMs()

	var state string
	var local bool
	err := s.db.QueryRow(`SELECT state, local FROM blobs WHERE sha256 = ?`, sha).Scan(&state, &local)
	duplicate := err == nil
	newBlob := false
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		if err := os.Rename(part, dst); err != nil {
			return nil, err
		}
		if _, err := s.db.Exec(`INSERT INTO blobs (sha256, size, mime, state, local, last_access, created_at) VALUES (?, ?, ?, 'buffered', 1, ?, ?)`,
			sha, u.size, u.mime, now, now); err != nil {
			os.Remove(dst)
			return nil, err
		}
		newBlob = true
	case err != nil:
		return nil, err
	case state == "missing" && !local:
		// the node lost this file and somebody uploaded it again: re-sync
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		if err := os.Rename(part, dst); err != nil {
			return nil, err
		}
		s.db.Exec(`UPDATE blobs SET state = 'buffered', local = 1, last_access = ?, sync_error = '' WHERE sha256 = ?`, now, sha)
		newBlob = true
	default:
		os.Remove(part)
	}

	result := map[string]any{"sha256": sha, "duplicate": duplicate}
	switch target.Type {
	case "asset":
		var version int64
		s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) + 1 FROM assets WHERE video_id = ? AND kind = ? AND variant_id IS ?`,
			target.VideoID, target.Kind, target.VariantID).Scan(&version)
		res, err := s.db.Exec(`INSERT INTO assets (video_id, variant_id, kind, filename, sha256, size, mime, version, note, uploaded_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			target.VideoID, target.VariantID, target.Kind, u.filename, sha, u.size, u.mime, version, cleanText(target.Note, 2000), user.ID, now)
		if err != nil {
			return nil, err
		}
		aid, _ := res.LastInsertId()
		result["asset_id"] = aid
		s.db.Exec(`UPDATE videos SET updated_at = ? WHERE id = ?`, now, target.VideoID)
		lang := ""
		if target.VariantID != nil {
			s.db.QueryRow(`SELECT language_code FROM variants WHERE id = ?`, *target.VariantID).Scan(&lang)
		}
		s.logActivity(user.ID, target.VideoID, "asset.uploaded", map[string]any{"filename": u.filename, "kind": target.Kind, "lang": lang, "size": u.size, "version": version})
		s.events.Publish("videos", "video:"+strconv.FormatInt(target.VideoID, 10), "storage")
	case "track":
		title := cleanText(target.Title, 300)
		if title == "" {
			title = strings.TrimSuffix(u.filename, filepath.Ext(u.filename))
		}
		platforms := target.Platforms
		if platforms == nil {
			platforms = []int64{}
		}
		pj, _ := json.Marshal(platforms)
		res, err := s.db.Exec(`INSERT INTO tracks (title, artist, album, bpm, musical_key, duration_ms, sha256, filename, size, source, license, license_url, platforms, notes, uploaded_by, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			title, cleanText(target.Artist, 300), cleanText(target.Album, 300), target.BPM, cleanText(target.Key, 20), target.DurationMs,
			sha, u.filename, u.size, cleanText(target.Source, 300), cleanText(target.License, 2000), cleanText(target.LicenseURL, 1000), string(pj),
			cleanText(target.Notes, 10000), user.ID, now, now)
		if err != nil {
			return nil, err
		}
		tid, _ := res.LastInsertId()
		if len(target.Tags) > 0 {
			s.setTrackTags(tid, target.Tags)
		}
		if target.DurationMs != nil {
			s.db.Exec(`UPDATE blobs SET duration_ms = ? WHERE sha256 = ? AND duration_ms IS NULL`, *target.DurationMs, sha)
		}
		result["track_id"] = tid
		s.events.Publish("music", "storage")
	}
	s.db.Exec(`DELETE FROM uploads WHERE id = ?`, u.id)
	uploadLocks.Delete(u.id)
	if newBlob {
		s.hub.kick(sha, u.size)
	}
	return result, nil
}
