package server

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
)

// Profile pictures: every user sets their own (an admin can set anyone's).
// The browser crops and scales the picture, the server only checks it.

const avatarMax = 512 << 10

func (s *Server) handleGetAvatar(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var mime string
	var data []byte
	err = s.db.QueryRow(`SELECT mime, data FROM user_avatars WHERE user_id = ?`, id).Scan(&mime, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return errNotFound("Аватар")
	}
	if err != nil {
		return err
	}
	h := w.Header()
	h.Set("Content-Type", mime)
	// the URL carries ?v=avatar_at, so a new picture gets a new URL
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	_, err = w.Write(data)
	return err
}

func (s *Server) handlePutAvatar(w http.ResponseWriter, r *http.Request) error {
	id, err := s.avatarTarget(r)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, avatarMax+1))
	if err != nil || len(data) > avatarMax {
		return errBad("Картинка слишком большая")
	}
	mime := http.DetectContentType(data)
	if mime != "image/jpeg" && mime != "image/png" && mime != "image/webp" {
		return errBad("Нужна картинка JPEG, PNG или WebP")
	}
	now := nowMs()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO user_avatars (user_id, mime, data) VALUES (?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET mime = excluded.mime, data = excluded.data`, id, mime, data); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE users SET avatar_at = ? WHERE id = ?`, now, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.events.Publish("users", "dicts")
	return ok(w, map[string]int64{"avatar_at": now})
}

func (s *Server) handleDeleteAvatar(w http.ResponseWriter, r *http.Request) error {
	id, err := s.avatarTarget(r)
	if err != nil {
		return err
	}
	s.db.Exec(`DELETE FROM user_avatars WHERE user_id = ?`, id)
	s.db.Exec(`UPDATE users SET avatar_at = NULL WHERE id = ?`, id)
	s.events.Publish("users", "dicts")
	return ok(w, map[string]bool{"ok": true})
}

// avatarTarget is the user whose picture the request changes: yourself, or anyone for an admin.
func (s *Server) avatarTarget(r *http.Request) (int64, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return 0, err
	}
	if u := currentUser(r); u.ID != id && !u.admin() {
		return 0, errForbidden()
	}
	if _, err := s.userByID(id); err != nil {
		return 0, errNotFound("Пользователь")
	}
	return id, nil
}
