package server

import (
	"database/sql"
	"net/http"
	"strconv"
)

type pendingBlob struct {
	Sha       string `json:"sha256"`
	Size      int64  `json:"size"`
	Mime      string `json:"mime"`
	State     string `json:"state"`
	CreatedAt int64  `json:"created_at"`
	SyncError string `json:"sync_error"`
	Name      string `json:"name"`
	VideoID   *int64 `json:"video_id"`
}

func (s *Server) blobList(where string, limit int) ([]pendingBlob, error) {
	out := []pendingBlob{}
	err := s.eachRow(`SELECT b.sha256, b.size, b.mime, b.state, b.created_at, b.sync_error,
			COALESCE((SELECT a.filename FROM assets a WHERE a.sha256 = b.sha256 LIMIT 1), (SELECT t.title FROM tracks t WHERE t.sha256 = b.sha256 LIMIT 1), ''),
			(SELECT a.video_id FROM assets a WHERE a.sha256 = b.sha256 LIMIT 1)
		FROM blobs b WHERE `+where+` ORDER BY b.created_at LIMIT ?`, []any{limit}, func(r *sql.Rows) error {
		var p pendingBlob
		var vid nullInt64
		if err := r.Scan(&p.Sha, &p.Size, &p.Mime, &p.State, &p.CreatedAt, &p.SyncError, &p.Name, &vid); err != nil {
			return err
		}
		p.VideoID = vid.ptr()
		out = append(out, p)
		return nil
	})
	return out, err
}

type StorageSummary struct {
	Online       bool  `json:"online"`
	LastSeen     int64 `json:"last_seen"`
	PendingCount int64 `json:"pending_count"`
	PendingBytes int64 `json:"pending_bytes"`
	MissingCount int64 `json:"missing_count"`
	BufferUsed   int64 `json:"buffer_used"`
	BufferMax    int64 `json:"buffer_max"`
	LastBackup   int64 `json:"last_backup"`
}

func (s *Server) storageSummary() (StorageSummary, error) {
	info := s.hub.nodeInfo()
	st := StorageSummary{Online: info.Online, LastSeen: info.LastSeen, BufferMax: s.cfg.BufferMax, LastBackup: info.LastBack}
	var cnt, bytes sql.NullInt64
	if err := s.db.QueryRow(`SELECT COUNT(*), SUM(size) FROM blobs WHERE state = 'buffered'`).Scan(&cnt, &bytes); err != nil {
		return st, err
	}
	st.PendingCount, st.PendingBytes = cnt.Int64, bytes.Int64
	s.db.QueryRow(`SELECT COUNT(*) FROM blobs WHERE state = 'missing'`).Scan(&st.MissingCount)
	u := s.store.usage()
	st.BufferUsed = u.Pinned + u.Uploading
	return st, nil
}

func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) error {
	sum, err := s.storageSummary()
	if err != nil {
		return err
	}
	pending, err := s.blobList(`b.state = 'buffered'`, 100)
	if err != nil {
		return err
	}
	missing, err := s.blobList(`b.state = 'missing'`, 100)
	if err != nil {
		return err
	}
	var totalCount, totalBytes, storedCount, storedBytes sql.NullInt64
	s.db.QueryRow(`SELECT COUNT(*), SUM(size) FROM blobs`).Scan(&totalCount, &totalBytes)
	s.db.QueryRow(`SELECT COUNT(*), SUM(size) FROM blobs WHERE state = 'stored'`).Scan(&storedCount, &storedBytes)
	var uploads int64
	s.db.QueryRow(`SELECT COUNT(*) FROM uploads`).Scan(&uploads)
	return ok(w, map[string]any{
		"summary": sum,
		"node":    s.hub.nodeInfo(),
		"usage":   s.store.usage(),
		"limits":  map[string]int64{"buffer": s.cfg.BufferMax, "cache": s.cfg.CacheMax, "previews": s.cfg.PreviewMax, "reserve": s.cfg.DiskReserve, "max_file": s.cfg.MaxFile},
		"pending": pending,
		"missing": missing,
		"uploads": uploads,
		"totals":  map[string]int64{"count": totalCount.Int64, "bytes": totalBytes.Int64, "stored_count": storedCount.Int64, "stored_bytes": storedBytes.Int64},
		"version": Version,
	})
}

func (s *Server) handleResync(w http.ResponseWriter, r *http.Request) error {
	if !s.hub.online() {
		return errStorageOffline
	}
	s.db.Exec(`UPDATE blobs SET derive_state = '' WHERE derive_state = 'failed'`)
	go s.hub.reconcile()
	return ok(w, map[string]bool{"ok": true})
}

// ---- trash ----------------------------------------------------------------

type trashAsset struct {
	Asset
	VideoCode  string `json:"video_code"`
	VideoTitle string `json:"video_title"`
}

func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) error {
	videos, err := s.loadVideos("v.deleted_at IS NOT NULL", nil, false)
	if err != nil {
		return err
	}
	assets, err := s.queryAssets(`a.deleted_at IS NOT NULL`)
	if err != nil {
		return err
	}
	titles := map[int64][2]string{}
	s.eachRow(`SELECT id, num, title FROM videos`, nil, func(r *sql.Rows) error {
		var id, num int64
		var t string
		if err := r.Scan(&id, &num, &t); err != nil {
			return err
		}
		titles[id] = [2]string{s.videoCode(num), t}
		return nil
	})
	ta := []trashAsset{}
	for _, a := range assets {
		x := trashAsset{Asset: a}
		x.VideoCode, x.VideoTitle = titles[a.VideoID][0], titles[a.VideoID][1]
		ta = append(ta, x)
	}
	tracks, err := s.loadTracks("t.deleted_at IS NOT NULL", nil, currentUser(r).ID)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"videos": videos, "assets": ta, "tracks": tracks})
}

// handlePurge permanently deletes an item that is already in the trash. Blobs
// that are no longer referenced are removed from the VPS and the node moves
// them to its own trash folder (kept NODE_TRASH_DAYS days).
func (s *Server) handlePurge(w http.ResponseWriter, r *http.Request) error {
	typ := r.PathValue("type")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return errBad("Некорректный идентификатор")
	}
	var shas []string
	collect := func(q string, args ...any) error {
		return s.eachRow(q, args, func(r *sql.Rows) error {
			var sh string
			if err := r.Scan(&sh); err != nil {
				return err
			}
			shas = append(shas, sh)
			return nil
		})
	}
	u := currentUser(r)
	switch typ {
	case "video":
		var title string
		var num int64
		if err := s.db.QueryRow(`SELECT title, num FROM videos WHERE id = ? AND deleted_at IS NOT NULL`, id).Scan(&title, &num); err != nil {
			return errNotFound("Ролик в корзине")
		}
		if err := collect(`SELECT sha256 FROM assets WHERE video_id = ?`, id); err != nil {
			return err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, q := range []string{
			`UPDATE videos SET original_id = NULL WHERE original_id = ?`,
			`DELETE FROM assets WHERE video_id = ?`,
			`DELETE FROM videos WHERE id = ?`,
		} {
			if _, err := tx.Exec(q, id); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		s.logActivity(u.ID, 0, "video.purged", map[string]any{"code": s.videoCode(num), "title": title})
	case "asset":
		if err := collect(`SELECT sha256 FROM assets WHERE id = ? AND deleted_at IS NOT NULL`, id); err != nil {
			return err
		}
		if len(shas) == 0 {
			return errNotFound("Файл в корзине")
		}
		if _, err := s.db.Exec(`DELETE FROM assets WHERE id = ?`, id); err != nil {
			return err
		}
	case "track":
		if err := collect(`SELECT sha256 FROM tracks WHERE id = ? AND deleted_at IS NOT NULL`, id); err != nil {
			return err
		}
		if len(shas) == 0 {
			return errNotFound("Трек в корзине")
		}
		if _, err := s.db.Exec(`DELETE FROM tracks WHERE id = ?`, id); err != nil {
			return err
		}
	default:
		return errBad("Неизвестный тип")
	}
	for _, sh := range shas {
		s.store.gcBlob(sh)
	}
	s.events.Publish("trash", "videos", "music", "storage")
	return ok(w, map[string]bool{"ok": true})
}
