package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
)

type Track struct {
	ID         int64           `json:"id"`
	Title      string          `json:"title"`
	Artist     string          `json:"artist"`
	Album      string          `json:"album"`
	BPM        *int64          `json:"bpm"`
	Key        string          `json:"musical_key"`
	DurationMs *int64          `json:"duration_ms"`
	Sha        string          `json:"sha256"`
	Filename   string          `json:"filename"`
	Size       int64           `json:"size"`
	Source     string          `json:"source"`
	License    string          `json:"license"`
	LicenseURL string          `json:"license_url"`
	Platforms  json.RawMessage `json:"platforms"`
	Notes      string          `json:"notes"`
	UploadedBy *int64          `json:"uploaded_by"`
	CreatedAt  int64           `json:"created_at"`
	UpdatedAt  int64           `json:"updated_at"`
	DeletedAt  *int64          `json:"deleted_at,omitempty"`
	Tags       []int64         `json:"tags"`
	Favorite   bool            `json:"favorite"`
	UsedIn     int             `json:"used_in"`
	Peaks      json.RawMessage `json:"peaks"`
	HasCover   bool            `json:"has_cover"`
	State      string          `json:"state"`
	Local      bool            `json:"local"`
}

func (s *Server) loadTracks(where string, args []any, userID int64) ([]*Track, error) {
	out := []*Track{}
	byID := map[int64]*Track{}
	err := s.eachRow(`SELECT t.id, t.title, t.artist, t.album, t.bpm, t.musical_key, COALESCE(t.duration_ms, b.duration_ms), t.sha256, t.filename, t.size,
			t.source, t.license, t.license_url, t.platforms, t.notes, t.uploaded_by, t.created_at, t.updated_at, t.deleted_at,
			COALESCE(b.peaks, 'null'), b.state, b.local,
			EXISTS (SELECT 1 FROM derived_files d WHERE d.sha256 = t.sha256 AND d.name = 'thumb.jpg')
		FROM tracks t JOIN blobs b ON b.sha256 = t.sha256
		WHERE `+where+` ORDER BY t.id DESC`, args, func(r *sql.Rows) error {
		t := &Track{Tags: []int64{}}
		var bpm, dur, by, del nullInt64
		var platforms, peaks string
		if err := r.Scan(&t.ID, &t.Title, &t.Artist, &t.Album, &bpm, &t.Key, &dur, &t.Sha, &t.Filename, &t.Size,
			&t.Source, &t.License, &t.LicenseURL, &platforms, &t.Notes, &by, &t.CreatedAt, &t.UpdatedAt, &del,
			&peaks, &t.State, &t.Local, &t.HasCover); err != nil {
			return err
		}
		t.BPM, t.DurationMs, t.UploadedBy, t.DeletedAt = bpm.ptr(), dur.ptr(), by.ptr(), del.ptr()
		t.Platforms = json.RawMessage(platforms)
		t.Peaks = json.RawMessage(peaks)
		out = append(out, t)
		byID[t.ID] = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	if err := s.eachRow(`SELECT track_id, tag_id FROM track_tags`, nil, func(r *sql.Rows) error {
		var tid, tag int64
		if err := r.Scan(&tid, &tag); err != nil {
			return err
		}
		if t := byID[tid]; t != nil {
			t.Tags = append(t.Tags, tag)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := s.eachRow(`SELECT track_id FROM track_favorites WHERE user_id = ?`, []any{userID}, func(r *sql.Rows) error {
		var tid int64
		if err := r.Scan(&tid); err != nil {
			return err
		}
		if t := byID[tid]; t != nil {
			t.Favorite = true
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := s.eachRow(`SELECT vt.track_id, COUNT(*) FROM video_tracks vt JOIN videos v ON v.id = vt.video_id WHERE v.deleted_at IS NULL GROUP BY vt.track_id`, nil, func(r *sql.Rows) error {
		var tid int64
		var n int
		if err := r.Scan(&tid, &n); err != nil {
			return err
		}
		if t := byID[tid]; t != nil {
			t.UsedIn = n
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Server) handleListTracks(w http.ResponseWriter, r *http.Request) error {
	where := "t.deleted_at IS NULL"
	if r.URL.Query().Get("deleted") == "1" {
		where = "t.deleted_at IS NOT NULL"
	}
	list, err := s.loadTracks(where, nil, currentUser(r).ID)
	if err != nil {
		return err
	}
	return ok(w, list)
}

func (s *Server) handleGetTrack(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	list, err := s.loadTracks("t.id = ?", []any{id}, currentUser(r).ID)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return errNotFound("Трек")
	}
	type usage struct {
		VideoID int64  `json:"video_id"`
		Code    string `json:"code"`
		Title   string `json:"title"`
	}
	used := []usage{}
	s.eachRow(`SELECT v.id, v.num, v.title FROM video_tracks vt JOIN videos v ON v.id = vt.video_id WHERE vt.track_id = ? AND v.deleted_at IS NULL ORDER BY v.num DESC`, []any{id}, func(r *sql.Rows) error {
		var u usage
		var num int64
		if err := r.Scan(&u.VideoID, &num, &u.Title); err != nil {
			return err
		}
		u.Code = s.videoCode(num)
		used = append(used, u)
		return nil
	})
	return ok(w, map[string]any{"track": list[0], "videos": used})
}

func (s *Server) handlePatchTrack(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req map[string]json.RawMessage
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if err := s.applyTrackPatch(id, req); err != nil {
		return err
	}
	s.events.Publish("music")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) applyTrackPatch(id int64, req map[string]json.RawMessage) error {
	p := newPatch()
	for key, raw := range req {
		switch key {
		case "title":
			v, err := p.str(raw, 300)
			if err != nil {
				return err
			}
			if v == "" {
				return errBad("Название не может быть пустым")
			}
			p.set(key, v)
		case "artist", "album", "source", "license", "license_url", "musical_key":
			v, err := p.str(raw, 1000)
			if err != nil {
				return err
			}
			p.set(key, v)
		case "notes":
			v, err := p.str(raw, 10000)
			if err != nil {
				return err
			}
			p.set(key, v)
		case "bpm", "duration_ms":
			v, err := p.optInt(raw)
			if err != nil {
				return err
			}
			p.set(key, v)
		case "platforms":
			var ids []int64
			if err := json.Unmarshal(raw, &ids); err != nil {
				return errBad("platforms: ожидается массив")
			}
			if ids == nil {
				ids = []int64{}
			}
			b, _ := json.Marshal(ids)
			p.set(key, string(b))
		case "tags":
			var ids []int64
			if err := json.Unmarshal(raw, &ids); err != nil {
				return errBad("tags: ожидается массив")
			}
			if err := s.setTrackTags(id, ids); err != nil {
				return err
			}
		default:
			return errBad("Поле %s нельзя изменить", key)
		}
	}
	p.set("updated_at", nowMs())
	return p.exec(s.db, "tracks", "id", id)
}

func (s *Server) setTrackTags(id int64, ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM track_tags WHERE track_id = ?`, id); err != nil {
		return err
	}
	for _, t := range ids {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO track_tags (track_id, tag_id) SELECT ?, id FROM tags WHERE id = ? AND scope = 'music'`, id, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Server) handleDeleteTrack(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE tracks SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`, nowMs(), id); err != nil {
		return err
	}
	s.events.Publish("music", "trash")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleRestoreTrack(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE tracks SET deleted_at = NULL WHERE id = ?`, id); err != nil {
		return err
	}
	s.events.Publish("music", "trash")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleBulkTracks(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		IDs        []int64                    `json:"ids"`
		AddTags    []int64                    `json:"add_tags"`
		RemoveTags []int64                    `json:"remove_tags"`
		Set        map[string]json.RawMessage `json:"set"`
		Delete     bool                       `json:"delete"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if len(req.IDs) == 0 {
		return errBad("Не выбраны треки")
	}
	for k := range req.Set {
		if k == "title" || k == "tags" {
			return errBad("Поле %s нельзя менять массово", k)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := nowMs()
	for _, id := range req.IDs {
		for _, t := range req.AddTags {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO track_tags (track_id, tag_id) SELECT ?, id FROM tags WHERE id = ? AND scope = 'music'`, id, t); err != nil {
				return err
			}
		}
		for _, t := range req.RemoveTags {
			if _, err := tx.Exec(`DELETE FROM track_tags WHERE track_id = ? AND tag_id = ?`, id, t); err != nil {
				return err
			}
		}
		if req.Delete {
			if _, err := tx.Exec(`UPDATE tracks SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`, now, id); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if len(req.Set) > 0 {
		for _, id := range req.IDs {
			if err := s.applyTrackPatch(id, req.Set); err != nil {
				return err
			}
		}
	}
	s.events.Publish("music", "trash")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleFavoriteTrack(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	u := currentUser(r)
	if r.Method == http.MethodPut {
		_, err = s.db.Exec(`INSERT OR IGNORE INTO track_favorites (user_id, track_id) VALUES (?, ?)`, u.ID, id)
	} else {
		_, err = s.db.Exec(`DELETE FROM track_favorites WHERE user_id = ? AND track_id = ?`, u.ID, id)
	}
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"ok": true, "id": strconv.FormatInt(id, 10)})
}
