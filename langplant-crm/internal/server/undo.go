package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Undo history.
//
// Every state-changing API request runs as a "step". While a step is active,
// row triggers on the content tables copy the before/after image of each
// touched row into undo_log (the step id is read from the one-row undo_ctx
// table). Undoing a step writes the before images back in reverse order; that
// write is itself recorded as a new step, so an undo can be undone (redo).
//
// Steps are recorded one at a time (undoMu), so concurrent requests never mix
// their rows. Background jobs only touch storage tables (blobs, derived files,
// uploads, node state), which are not tracked.
//
// Before undoing, every row of the step must still be in the state the step
// left it in; otherwise someone changed it later and the undo is refused with
// a message naming that later change, instead of silently overwriting it.

// undoTables are the tables whose changes can be undone.
var undoTables = []string{
	"settings", "languages", "platforms", "channels", "stages", "asset_kinds", "tag_groups", "tags", "checklist_template",
	"videos", "video_tags", "variants", "assets", "publications", "checklist", "comments", "day_notes",
	"tracks", "track_tags", "video_tracks",
}

// undoVolatile columns are bumped as a side effect of unrelated changes; they
// are ignored when comparing row states.
var undoVolatile = map[string]bool{"updated_at": true}

const (
	undoMergeWindow = 3 * time.Minute     // repeated edits of the same fields merge into one step
	undoKeep        = 30 * 24 * time.Hour // history retention
)

// installUndo (re)creates the recording triggers for the current schema. It
// runs on every start, so columns added by later migrations are covered.
func (s *Server) installUndo() error {
	s.undoCols = map[string][]string{}
	for _, t := range undoTables {
		cols, err := tableColumns(s.db, t)
		if err != nil {
			return err
		}
		s.undoCols[t] = cols
		obj := func(prefix string) string {
			parts := make([]string, 0, len(cols))
			for _, c := range cols {
				parts = append(parts, "'"+c+"', "+prefix+`"`+c+`"`)
			}
			return "json_object(" + strings.Join(parts, ", ") + ")"
		}
		ctx := `(SELECT step FROM undo_ctx WHERE id = 1)`
		stmts := []string{
			`DROP TRIGGER IF EXISTS undo_` + t + `_i`,
			`DROP TRIGGER IF EXISTS undo_` + t + `_u`,
			`DROP TRIGGER IF EXISTS undo_` + t + `_d`,
			`CREATE TRIGGER undo_` + t + `_i AFTER INSERT ON ` + t + ` WHEN ` + ctx + ` IS NOT NULL BEGIN
				INSERT INTO undo_log (step_id, tbl, rid, op, old, new) VALUES (` + ctx + `, '` + t + `', NEW.rowid, 'I', NULL, ` + obj("NEW.") + `); END`,
			`CREATE TRIGGER undo_` + t + `_u AFTER UPDATE ON ` + t + ` WHEN ` + ctx + ` IS NOT NULL BEGIN
				INSERT INTO undo_log (step_id, tbl, rid, op, old, new) VALUES (` + ctx + `, '` + t + `', NEW.rowid, 'U', ` + obj("OLD.") + `, ` + obj("NEW.") + `); END`,
			`CREATE TRIGGER undo_` + t + `_d AFTER DELETE ON ` + t + ` WHEN ` + ctx + ` IS NOT NULL BEGIN
				INSERT INTO undo_log (step_id, tbl, rid, op, old, new) VALUES (` + ctx + `, '` + t + `', OLD.rowid, 'D', ` + obj("OLD.") + `, NULL); END`,
		}
		for _, q := range stmts {
			if _, err := s.db.Exec(q); err != nil {
				return fmt.Errorf("undo trigger %s: %w", t, err)
			}
		}
	}
	// a crash in the middle of a request must not leave recording switched on
	_, err := s.db.Exec(`UPDATE undo_ctx SET step = NULL WHERE id = 1`)
	return err
}

func tableColumns(d *sql.DB, table string) ([]string, error) {
	rows, err := d.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("table %s not found", table)
	}
	return cols, rows.Err()
}

// ---- recording ---------------------------------------------------------------

// undoBegin starts recording a step for the user. It holds undoMu until undoEnd.
func (s *Server) undoBegin(userID int64, kind string) (int64, error) {
	s.undoMu.Lock()
	now := nowMs()
	var uid any
	if userID > 0 {
		uid = userID
	}
	res, err := s.db.Exec(`INSERT INTO undo_steps (user_id, kind, created_at, updated_at) VALUES (?, ?, ?, ?)`, uid, kind, now, now)
	if err == nil {
		var id int64
		id, err = res.LastInsertId()
		if err == nil {
			if _, err = s.db.Exec(`UPDATE undo_ctx SET step = ? WHERE id = 1`, id); err == nil {
				return id, nil
			}
		}
	}
	s.undoMu.Unlock()
	return 0, err
}

// undoEnd stops recording and turns what was recorded into a history entry:
// no-op updates are dropped, an empty step is removed and quick repeated edits
// of the same fields are merged into the previous step. It returns the
// resulting step (nil when nothing was changed) and whether it was merged.
func (s *Server) undoEnd(id int64) (*undoStepInfo, bool) {
	defer s.undoMu.Unlock()
	s.db.Exec(`UPDATE undo_ctx SET step = NULL WHERE id = 1`)
	st, merged, err := s.finalizeStep(id)
	if err != nil {
		s.db.Exec(`DELETE FROM undo_steps WHERE id = ?`, id)
		return nil, false
	}
	if st != nil {
		s.events.Publish("undo")
	}
	return st, merged
}

type undoStepInfo struct {
	ID    int64
	Label string
}

func (s *Server) finalizeStep(id int64) (*undoStepInfo, bool, error) {
	entries, err := s.stepEntries(id)
	if err != nil {
		return nil, false, err
	}
	kept := entries[:0]
	for _, e := range entries {
		if e.Op == "U" && sameRow(e.Old, e.New) {
			s.db.Exec(`DELETE FROM undo_log WHERE id = ?`, e.ID)
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) == 0 {
		_, err := s.db.Exec(`DELETE FROM undo_steps WHERE id = ?`, id)
		return nil, false, err
	}
	var userID nullInt64
	var kind string
	if err := s.db.QueryRow(`SELECT user_id, kind FROM undo_steps WHERE id = ?`, id).Scan(&userID, &kind); err != nil {
		return nil, false, err
	}
	if kind == "action" && userID.Valid {
		if prev, ok := s.mergeTarget(id, userID.Int64, kept); ok {
			for _, e := range kept {
				if _, err := s.db.Exec(`UPDATE undo_log SET new = ? WHERE id = (SELECT MAX(id) FROM undo_log WHERE step_id = ? AND tbl = ? AND rid = ?)`,
					e.rawNew, prev, e.Tbl, e.Rid); err != nil {
					return nil, false, err
				}
			}
			s.db.Exec(`DELETE FROM undo_steps WHERE id = ?`, id)
			all, err := s.stepEntries(prev)
			if err != nil {
				return nil, false, err
			}
			label, videoID := s.describeStep(all)
			s.db.Exec(`UPDATE undo_steps SET label = ?, video_id = ?, updated_at = ? WHERE id = ?`, label, nullID(videoID), nowMs(), prev)
			return &undoStepInfo{ID: prev, Label: label}, true, nil
		}
	}
	label, videoID := s.describeStep(kept)
	adminOnly := false
	for _, e := range kept {
		adminOnly = adminOnly || e.Tbl == "settings"
	}
	if _, err := s.db.Exec(`UPDATE undo_steps SET label = ?, video_id = ?, admin_only = ? WHERE id = ?`,
		label, nullID(videoID), boolInt(adminOnly), id); err != nil {
		return nil, false, err
	}
	return &undoStepInfo{ID: id, Label: label}, false, nil
}

// mergeTarget finds the user's previous step that the new one continues: the
// same rows, only updates of the same columns, recent, not undone and with
// nobody else touching those rows in between (e.g. typing a script that is
// saved every second).
func (s *Server) mergeTarget(id, userID int64, entries []*undoEntry) (int64, bool) {
	cur, ok := updateShape(entries)
	if !ok {
		return 0, false
	}
	var prev int64
	err := s.db.QueryRow(`SELECT id FROM undo_steps WHERE user_id = ? AND id < ? AND kind = 'action' AND undone_by IS NULL AND updated_at >= ?
		ORDER BY id DESC LIMIT 1`, userID, id, nowMs()-undoMergeWindow.Milliseconds()).Scan(&prev)
	if err != nil {
		return 0, false
	}
	pe, err := s.stepEntries(prev)
	if err != nil {
		return 0, false
	}
	prevShape, ok := updateShape(pe)
	if !ok || !reflect.DeepEqual(cur, prevShape) {
		return 0, false
	}
	for key := range cur {
		tbl, rid, _ := strings.Cut(key, "#")
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM undo_log WHERE tbl = ? AND rid = ? AND step_id > ? AND step_id != ?`, tbl, rid, prev, id).Scan(&n)
		if n > 0 {
			return 0, false
		}
	}
	return prev, true
}

// updateShape describes a step made only of updates: row → changed columns.
func updateShape(entries []*undoEntry) (map[string]string, bool) {
	shape := map[string]string{}
	for _, e := range entries {
		if e.Op != "U" {
			return nil, false
		}
		key := e.Tbl + "#" + strconv.FormatInt(e.Rid, 10)
		cols := changedCols(e.Old, e.New)
		if prev, ok := shape[key]; ok && prev != "" {
			cols = append(cols, strings.Split(prev, ",")...)
		}
		shape[key] = strings.Join(uniqSorted(cols), ",")
	}
	return shape, len(shape) > 0
}

// undoWriter finishes the step right before the response is written, so the
// step id can be sent in the X-Undo-Step header.
type undoWriter struct {
	http.ResponseWriter
	s    *Server
	step int64
	done bool
}

func (w *undoWriter) finish() {
	if w.done {
		return
	}
	w.done = true
	st, merged := w.s.undoEnd(w.step)
	if st != nil {
		h := w.ResponseWriter.Header()
		h.Set("X-Undo-Step", strconv.FormatInt(st.ID, 10))
		h.Set("X-Undo-Label", url.PathEscape(st.Label))
		if merged {
			h.Set("X-Undo-Merged", "1")
		}
	}
}

func (w *undoWriter) WriteHeader(code int) {
	w.finish()
	w.ResponseWriter.WriteHeader(code)
}

func (w *undoWriter) Write(b []byte) (int, error) {
	w.finish()
	return w.ResponseWriter.Write(b)
}

func (w *undoWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// undoTracked reports whether a request records an undo step. Uploads record
// their own step when the last chunk arrives; permanent deletion from the
// trash, storage maintenance, accounts and the undo endpoints are not undoable.
func undoTracked(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	for _, p := range []string{"/api/uploads", "/api/files/", "/api/undo", "/api/trash/", "/api/storage/", "/api/users", "/api/auth/"} {
		if strings.HasPrefix(r.URL.Path, p) {
			return false
		}
	}
	return true
}

// serveRecorded runs a handler as one undo step.
func (s *Server) serveRecorded(h http.Handler, w http.ResponseWriter, r *http.Request, u *User) {
	id, err := s.undoBegin(u.ID, "action")
	if err != nil {
		h.ServeHTTP(w, r) // recording is best effort; never block the user
		return
	}
	uw := &undoWriter{ResponseWriter: w, s: s, step: id}
	defer uw.finish()
	h.ServeHTTP(uw, r)
}

// ---- log entries -------------------------------------------------------------

type undoEntry struct {
	ID       int64
	Tbl      string
	Rid      int64
	Op       string
	Old, New map[string]any
	rawOld   sql.NullString
	rawNew   sql.NullString
}

func (s *Server) stepEntries(step int64) ([]*undoEntry, error) {
	rows, err := s.db.Query(`SELECT id, tbl, rid, op, old, new FROM undo_log WHERE step_id = ? ORDER BY id`, step)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*undoEntry
	for rows.Next() {
		e := &undoEntry{}
		if err := rows.Scan(&e.ID, &e.Tbl, &e.Rid, &e.Op, &e.rawOld, &e.rawNew); err != nil {
			return nil, err
		}
		e.Old = decodeRow(e.rawOld)
		e.New = decodeRow(e.rawNew)
		out = append(out, e)
	}
	return out, rows.Err()
}

func decodeRow(raw sql.NullString) map[string]any {
	if !raw.Valid {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(raw.String))
	dec.UseNumber()
	var m map[string]any
	if dec.Decode(&m) != nil {
		return nil
	}
	return m
}

// sqlValue converts a decoded JSON value back to a database argument.
func sqlValue(v any) any {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		f, _ := x.Float64()
		return f
	case bool:
		return boolInt(x)
	case map[string]any, []any:
		b, _ := json.Marshal(x)
		return string(b)
	}
	return v
}

// sameRow compares two row images, ignoring volatile columns. Columns present
// in only one image (added by a later migration) are ignored as well.
func sameRow(a, b map[string]any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	for k, av := range a {
		if undoVolatile[k] {
			continue
		}
		if bv, ok := b[k]; ok && !reflect.DeepEqual(av, bv) {
			return false
		}
	}
	return true
}

func changedCols(a, b map[string]any) []string {
	var out []string
	for k, av := range a {
		if undoVolatile[k] {
			continue
		}
		if bv, ok := b[k]; ok && !reflect.DeepEqual(av, bv) {
			out = append(out, k)
		}
	}
	return uniqSorted(out)
}

func uniqSorted(xs []string) []string {
	seen := map[string]bool{}
	out := xs[:0:0]
	for _, x := range xs {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (s *Server) currentRow(q interface {
	QueryRow(string, ...any) *sql.Row
}, tbl string, rid int64) (map[string]any, error) {
	cols := s.undoCols[tbl]
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		parts = append(parts, "'"+c+"', \""+c+"\"")
	}
	var raw sql.NullString
	err := q.QueryRow(`SELECT json_object(`+strings.Join(parts, ", ")+`) FROM `+tbl+` WHERE rowid = ?`, rid).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeRow(raw), nil
}

// ---- undo --------------------------------------------------------------------

type undoMeta struct {
	ID        int64
	UserID    nullInt64
	Kind      string
	Label     string
	VideoID   nullInt64
	AdminOnly bool
	TargetID  nullInt64
	UndoneBy  nullInt64
	CreatedAt int64
	UpdatedAt int64
}

func (s *Server) loadStep(id int64) (*undoMeta, error) {
	m := &undoMeta{}
	err := s.db.QueryRow(`SELECT id, user_id, kind, label, video_id, admin_only, target_id, undone_by, created_at, updated_at FROM undo_steps WHERE id = ?`, id).
		Scan(&m.ID, &m.UserID, &m.Kind, &m.Label, &m.VideoID, &m.AdminOnly, &m.TargetID, &m.UndoneBy, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound("Шаг истории")
	}
	return m, err
}

// baseLabel strips the "Отмена: " / "Повтор: " prefixes of undo and redo steps.
func baseLabel(l string) string {
	for {
		t := strings.TrimPrefix(strings.TrimPrefix(l, "Отмена: "), "Повтор: ")
		if t == l {
			return l
		}
		l = t
	}
}

// revert undoes step id on behalf of u and returns the new (undo or redo) step.
func (s *Server) revert(id int64, u *User) (*undoMeta, error) {
	s.undoMu.Lock()
	locked := true
	defer func() {
		if locked {
			s.undoMu.Unlock()
		}
	}()
	target, err := s.loadStep(id)
	if err != nil {
		return nil, err
	}
	if target.UndoneBy.Valid {
		if target.Kind == "undo" {
			return nil, errConflict("Это уже возвращено")
		}
		return nil, errConflict("Это уже отменено")
	}
	if target.AdminOnly && !u.admin() {
		return nil, &apiError{http.StatusForbidden, "forbidden", "Изменения общих настроек может отменить только администратор"}
	}
	entries, err := s.stepEntries(id)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errConflict("Для этого шага нет данных для отмены")
	}
	if err := s.checkUnchanged(id, entries); err != nil {
		return nil, err
	}

	kind := "undo"
	label := "Отмена: " + baseLabel(target.Label)
	if target.Kind == "undo" {
		kind = "redo"
		label = "Повтор: " + baseLabel(target.Label)
	}
	now := nowMs()
	res, err := s.db.Exec(`INSERT INTO undo_steps (user_id, kind, label, video_id, admin_only, target_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, kind, label, target.VideoID.ptr(), boolInt(target.AdminOnly), id, now, now)
	if err != nil {
		return nil, err
	}
	newID, _ := res.LastInsertId()
	if err := s.applyInverse(newID, entries); err != nil {
		s.db.Exec(`DELETE FROM undo_steps WHERE id = ?`, newID)
		if isConstraint(err) {
			return nil, errConflict("Не получилось: связанные данные с тех пор изменились (например, запись удалена навсегда или используется в другом месте)")
		}
		return nil, err
	}
	s.db.Exec(`UPDATE undo_steps SET undone_by = ? WHERE id = ?`, newID, id)
	s.undoMu.Unlock()
	locked = false

	s.reloadLocation()
	s.events.Publish("undo", "videos", "music", "calendar", "dicts", "users", "trash", "storage", "activity")
	if target.VideoID.Valid {
		var exists int
		s.db.QueryRow(`SELECT COUNT(*) FROM videos WHERE id = ?`, target.VideoID.Int64).Scan(&exists)
		if exists > 0 {
			s.events.Publish("video:" + strconv.FormatInt(target.VideoID.Int64, 10))
			s.logActivity(u.ID, target.VideoID.Int64, kind, map[string]any{"label": baseLabel(target.Label)})
		}
	}
	return s.loadStep(newID)
}

// checkUnchanged verifies that every row is still as the step left it.
func (s *Server) checkUnchanged(id int64, entries []*undoEntry) error {
	changed, later, err := s.findBlocker(id, entries)
	if err != nil || !changed {
		return err
	}
	if later > 0 {
		if m, err := s.loadStep(later); err == nil {
			who := "кто-то"
			if m.UserID.Valid {
				s.db.QueryRow(`SELECT name FROM users WHERE id = ?`, m.UserID.Int64).Scan(&who)
			}
			when := time.UnixMilli(m.UpdatedAt).In(s.location())
			ts := when.Format("15:04")
			if y, mo, d := time.Now().In(s.location()).Date(); when.Year() != y || when.Month() != mo || when.Day() != d {
				ts = when.Format("02.01 15:04")
			}
			return errConflict("Не получилось: эти данные уже изменили позже — «%s» (%s, %s). Сначала отмените то изменение", m.Label, who, ts)
		}
	}
	return errConflict("Не получилось: эти данные с тех пор изменились или удалены навсегда")
}

// findBlocker reports whether a row of the step changed after it, and the
// latest later step that touched such a row (0 if the change is not in the
// history, e.g. permanent deletion from the trash).
func (s *Server) findBlocker(id int64, entries []*undoEntry) (bool, int64, error) {
	type rowKey struct {
		tbl string
		rid int64
	}
	final := map[rowKey]map[string]any{}
	var order []rowKey
	for _, e := range entries {
		k := rowKey{e.Tbl, e.Rid}
		if _, seen := final[k]; !seen {
			order = append(order, k)
		}
		final[k] = e.New // nil after a delete
	}
	for _, k := range order {
		if _, ok := s.undoCols[k.tbl]; !ok {
			return true, 0, nil
		}
		cur, err := s.currentRow(s.db, k.tbl, k.rid)
		if err != nil {
			return false, 0, err
		}
		if sameRow(final[k], cur) && (final[k] == nil) == (cur == nil) {
			continue
		}
		var later sql.NullInt64
		s.db.QueryRow(`SELECT MAX(l.step_id) FROM undo_log l JOIN undo_steps st ON st.id = l.step_id
			WHERE l.tbl = ? AND l.rid = ? AND l.step_id > ? AND st.undone_by IS NULL`, k.tbl, k.rid, id).Scan(&later)
		return true, later.Int64, nil
	}
	return false, 0, nil
}

// applyInverse writes the before images of entries back, newest first, while
// recording the writes as step newID.
func (s *Server) applyInverse(newID int64, entries []*undoEntry) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// children and parents are restored in one go; check references at commit
	if _, err := tx.Exec(`PRAGMA defer_foreign_keys = ON`); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE undo_ctx SET step = ? WHERE id = 1`, newID); err != nil {
		return err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		known := map[string]bool{}
		for _, c := range s.undoCols[e.Tbl] {
			known[c] = true
		}
		switch e.Op {
		case "I":
			if _, err := tx.Exec(`DELETE FROM `+e.Tbl+` WHERE rowid = ?`, e.Rid); err != nil {
				return err
			}
		case "U":
			var sets []string
			var args []any
			for _, c := range sortedKeys(e.Old) {
				if known[c] {
					sets = append(sets, `"`+c+`" = ?`)
					args = append(args, sqlValue(e.Old[c]))
				}
			}
			if len(sets) == 0 {
				continue
			}
			args = append(args, e.Rid)
			if _, err := tx.Exec(`UPDATE `+e.Tbl+` SET `+strings.Join(sets, ", ")+` WHERE rowid = ?`, args...); err != nil {
				return err
			}
		case "D":
			cols := []string{"rowid"}
			args := []any{e.Rid}
			for _, c := range sortedKeys(e.Old) {
				if known[c] {
					cols = append(cols, `"`+c+`"`)
					args = append(args, sqlValue(e.Old[c]))
				}
			}
			ph := strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", ")
			if _, err := tx.Exec(`INSERT INTO `+e.Tbl+` (`+strings.Join(cols, ", ")+`) VALUES (`+ph+`)`, args...); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`UPDATE undo_ctx SET step = NULL WHERE id = 1`); err != nil {
		return err
	}
	return tx.Commit()
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return uniqSorted(keys)
}

// pruneUndo drops history older than the retention period.
func (s *Server) pruneUndo() {
	s.db.Exec(`DELETE FROM undo_steps WHERE updated_at < ?`, nowMs()-undoKeep.Milliseconds())
}

// ---- API ---------------------------------------------------------------------

// UndoItem is one entry of the change history.
type UndoItem struct {
	ID         int64   `json:"id"`
	Kind       string  `json:"kind"`
	Label      string  `json:"label"`
	UserID     *int64  `json:"user_id"`
	VideoID    *int64  `json:"video_id"`
	AdminOnly  bool    `json:"admin_only"`
	TargetID   *int64  `json:"target_id"`
	Undone     bool    `json:"undone"`
	UndoneByID *int64  `json:"undone_by_user"`
	UndoneAt   *int64  `json:"undone_at"`
	CreatedAt  int64   `json:"created_at"`
	UpdatedAt  int64   `json:"updated_at"`
	VideoCode  *string `json:"video_code,omitempty"`
	Blocked    bool    `json:"blocked"`    // rows changed later: undo would be refused
	BlockedBy  *int64  `json:"blocked_by"` // the later step to undo first
}

func (s *Server) handleUndoList(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	limit := 50
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	where := "1=1"
	args := []any{}
	if v, err := strconv.ParseInt(q.Get("before"), 10, 64); err == nil {
		where += " AND st.id < ?"
		args = append(args, v)
	}
	args = append(args, limit)
	rows, err := s.db.Query(`SELECT st.id, st.kind, st.label, st.user_id, st.video_id, st.admin_only, st.target_id, st.undone_by IS NOT NULL,
			ub.user_id, ub.created_at, st.created_at, st.updated_at, v.num
		FROM undo_steps st
		LEFT JOIN undo_steps ub ON ub.id = st.undone_by
		LEFT JOIN videos v ON v.id = st.video_id
		WHERE `+where+` ORDER BY st.id DESC LIMIT ?`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	prefix := s.setting("code_prefix")
	out := []UndoItem{}
	for rows.Next() {
		var it UndoItem
		var uid, vid, target, ubUser, ubAt, num nullInt64
		if err := rows.Scan(&it.ID, &it.Kind, &it.Label, &uid, &vid, &it.AdminOnly, &target, &it.Undone, &ubUser, &ubAt, &it.CreatedAt, &it.UpdatedAt, &num); err != nil {
			return err
		}
		it.UserID, it.VideoID, it.TargetID, it.UndoneByID, it.UndoneAt = uid.ptr(), vid.ptr(), target.ptr(), ubUser.ptr(), ubAt.ptr()
		if num.Valid {
			code := fmt.Sprintf("%s-%04d", prefix, num.Int64)
			it.VideoCode = &code
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for i := range out {
		it := &out[i]
		if it.Undone {
			continue
		}
		entries, err := s.stepEntries(it.ID)
		if err != nil {
			return err
		}
		changed, later, err := s.findBlocker(it.ID, entries)
		if err != nil {
			return err
		}
		if changed {
			it.Blocked = true
			if later > 0 {
				it.BlockedBy = &later
			}
		}
	}
	return ok(w, out)
}

func (s *Server) handleUndo(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	st, err := s.revert(id, currentUser(r))
	if err != nil {
		return err
	}
	return ok(w, stepJSON(st))
}

// handleUndoLast undoes the user's own latest change (Ctrl+Z) or brings back
// what they undid last (Ctrl+Shift+Z).
func (s *Server) handleUndoLast(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Redo bool `json:"redo"`
	}
	if r.ContentLength != 0 {
		if err := readJSON(r, &req); err != nil {
			return err
		}
	}
	u := currentUser(r)
	since := nowMs() - undoKeep.Milliseconds()
	var id int64
	var err error
	if req.Redo {
		err = s.db.QueryRow(`SELECT id FROM undo_steps WHERE user_id = ? AND kind = 'undo' AND undone_by IS NULL AND updated_at >= ?
			AND id > COALESCE((SELECT MAX(id) FROM undo_steps WHERE user_id = ? AND kind = 'action'), 0)
			ORDER BY id DESC LIMIT 1`, u.ID, since, u.ID).Scan(&id)
	} else {
		err = s.db.QueryRow(`SELECT id FROM undo_steps WHERE user_id = ? AND kind IN ('action', 'redo') AND undone_by IS NULL AND updated_at >= ?
			ORDER BY id DESC LIMIT 1`, u.ID, since).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		if req.Redo {
			return &apiError{http.StatusNotFound, "nothing", "Нечего возвращать"}
		}
		return &apiError{http.StatusNotFound, "nothing", "Нечего отменять"}
	}
	if err != nil {
		return err
	}
	st, err := s.revert(id, u)
	if err != nil {
		return err
	}
	return ok(w, stepJSON(st))
}

func stepJSON(m *undoMeta) map[string]any {
	return map[string]any{"id": m.ID, "kind": m.Kind, "label": m.Label, "target_id": m.TargetID.ptr()}
}

func nullID(id int64) any {
	if id > 0 {
		return id
	}
	return nil
}

// ---- labels ------------------------------------------------------------------

type undoNoun struct {
	one, many string
	fem       bool
	name      string // column holding a human name
}

var undoNouns = map[string]undoNoun{
	"settings":           {"настройка", "настройки", true, "key"},
	"languages":          {"язык", "языки", false, "name"},
	"platforms":          {"площадка", "площадки", true, "name"},
	"channels":           {"аккаунт", "аккаунты", false, "name"},
	"stages":             {"этап", "этапы", false, "name"},
	"asset_kinds":        {"тип файлов", "типы файлов", false, "name"},
	"tag_groups":         {"группа тегов", "группы тегов", true, "name"},
	"tags":               {"тег", "теги", false, "name"},
	"checklist_template": {"пункт шаблона чек-листа", "шаблон чек-листа", false, "label"},
	"videos":             {"ролик", "ролики", false, "title"},
	"variants":           {"языковая версия", "языковые версии", true, "language_code"},
	"assets":             {"файл", "файлы", false, "filename"},
	"publications":       {"публикация", "публикации", true, ""},
	"checklist":          {"пункт чек-листа", "чек-лист", false, "label"},
	"comments":           {"комментарий", "комментарии", false, "body"},
	"day_notes":          {"отметка дня", "отметки дней", true, "date"},
	"tracks":             {"трек", "треки", false, "title"},
}

var undoLinkTables = map[string]string{
	"video_tags":   "теги ролика",
	"video_tracks": "музыка ролика",
	"track_tags":   "теги трека",
}

var undoColNames = map[string]string{
	"title": "название", "name": "название", "label": "текст", "color": "цвет", "sort": "порядок", "archived": "архив",
	"stage_id": "этап", "assignee_id": "исполнитель", "plan_date": "дата плана", "is_unique": "уникальность", "original_id": "оригинал",
	"script": "сценарий", "notes": "заметки", "music_note": "заметка о музыке", "caption": "описание", "voice": "озвучка", "status": "статус",
	"kind": "тип", "filename": "имя файла", "note": "заметка", "variant_id": "язык", "plan_at": "время публикации", "published_at": "дата публикации",
	"url": "ссылка", "views": "просмотры", "body": "текст", "excused": "причина", "reason": "причина", "artist": "исполнитель",
	"album": "альбом", "bpm": "BPM", "musical_key": "тональность", "source": "источник", "license": "лицензия", "license_url": "ссылка на лицензию",
	"platforms": "площадки", "counts_for_quota": "учёт в плане", "icon": "иконка", "flag": "флаг", "is_primary": "основной язык",
	"hint": "подсказка", "scope": "раздел", "required": "обязательность", "accept": "форматы", "group_id": "группа", "platform_id": "площадка",
	"language_code": "язык", "done_at": "отметка", "deleted_at": "корзина",
}

var undoSettingNames = map[string]string{
	"code_prefix": "префикс кода роликов", "timezone": "часовой пояс", "quota_per_day": "план в день", "quota_start": "начало учёта плана",
	"archive_hours": "срок архива", "window_days": "окно пропусков", "auto_done_stage": "автосмена этапа",
}

var pubStatusNames = map[string]string{"planned": "в плане", "scheduled": "отложена", "published": "опубликовано", "skipped": "не публикуем", "removed": "снято"}

func gendered(fem bool, m, f string) string {
	if fem {
		return f
	}
	return m
}

func capitalize(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return strings.ToUpper(string(r)) + s[n:]
}

func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

func rowStr(row map[string]any, col string) string {
	switch v := row[col].(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	}
	return ""
}

func rowInt(row map[string]any, col string) int64 {
	if v, ok := row[col].(json.Number); ok {
		n, _ := v.Int64()
		return n
	}
	return 0
}

type undoEntity struct {
	tbl        string
	rid        int64
	first      *undoEntry
	last       *undoEntry
	before     map[string]any
	after      map[string]any
	class      string // I (created), D (deleted), U (changed)
	videoID    int64
	changeCols []string
}

// describeStep builds a short human label like «Изменён этап «Монтаж»: цвет».
func (s *Server) describeStep(entries []*undoEntry) (string, int64) {
	var ents []*undoEntity
	byKey := map[string]*undoEntity{}
	for _, e := range entries {
		key := e.Tbl + "#" + strconv.FormatInt(e.Rid, 10)
		en := byKey[key]
		if en == nil {
			en = &undoEntity{tbl: e.Tbl, rid: e.Rid, first: e}
			byKey[key] = en
			ents = append(ents, en)
		}
		en.last = e
	}
	videos := map[int64]bool{}
	var live []*undoEntity
	for _, en := range ents {
		en.before, en.after = en.first.Old, en.last.New
		switch {
		case en.first.Op == "I" && en.last.Op == "D":
			continue
		case en.first.Op == "I":
			en.class = "I"
		case en.last.Op == "D":
			en.class = "D"
		default:
			en.class = "U"
			en.changeCols = changedCols(en.before, en.after)
		}
		row := en.after
		if row == nil {
			row = en.before
		}
		if en.tbl == "videos" {
			en.videoID = en.rid
		} else {
			en.videoID = rowInt(row, "video_id")
		}
		if en.videoID > 0 {
			videos[en.videoID] = true
		}
		live = append(live, en)
	}
	var videoID int64
	if len(videos) == 1 {
		for id := range videos {
			videoID = id
		}
	}
	if len(live) == 0 {
		return "Изменение", videoID
	}

	// primary entity: the first non-link one, else a link table change
	var primary *undoEntity
	for _, en := range live {
		if _, ok := undoNouns[en.tbl]; ok {
			primary = en
			break
		}
	}
	if primary == nil {
		en := live[0]
		what := undoLinkTables[en.tbl]
		if en.tbl == "track_tags" {
			tracks := map[int64]bool{}
			for _, x := range live {
				if x.tbl == "track_tags" {
					tracks[rowInt(rowOf(x), "track_id")] = true
				}
			}
			if len(tracks) > 1 {
				return fmt.Sprintf("Изменены теги треков (%d)", len(tracks)), videoID
			}
			return capitalize("изменены " + what + " " + s.entityName("tracks", rowInt(rowOf(en), "track_id"), nil)), videoID
		}
		if videoID > 0 {
			return capitalize("изменены " + what + " " + s.videoRef(videoID)), videoID
		}
		return capitalize("изменены " + strings.Replace(what, "ролика", "роликов", 1)), videoID
	}

	noun := undoNouns[primary.tbl]
	same := 0
	for _, en := range live {
		if en.tbl == primary.tbl && en.class == primary.class {
			same++
		}
	}
	if primary.tbl == "settings" {
		var names []string
		for _, en := range live {
			if en.tbl == "settings" {
				k := rowStr(rowOf(en), "key")
				if n, ok := undoSettingNames[k]; ok {
					k = n
				}
				names = append(names, k)
			}
		}
		return "Изменены настройки: " + strings.Join(uniqSorted(names), ", "), 0
	}
	if same > 1 {
		if primary.class == "U" {
			onlySort := true
			for _, en := range live {
				if en.tbl == primary.tbl && (len(en.changeCols) != 1 || en.changeCols[0] != "sort") {
					onlySort = false
				}
			}
			if onlySort {
				return "Изменён порядок: " + noun.many, videoID
			}
		}
		verb := map[string]string{"I": "Добавлены", "D": "Удалены", "U": "Изменены"}[primary.class]
		if primary.class == "U" && allCols(live, primary.tbl, "deleted_at") {
			verb = "В корзину:"
			if primary.after["deleted_at"] == nil {
				verb = "Восстановлены:"
			}
		}
		label := fmt.Sprintf("%s %s (%d)", verb, noun.many, same)
		if videoID > 0 && primary.tbl != "videos" {
			label += " · " + s.videoRef(videoID)
		}
		return label, videoID
	}

	en := primary
	row := rowOf(en)
	name := s.entityName(en.tbl, en.rid, row)
	ctx := ""
	if en.tbl != "videos" && videoID > 0 {
		ctx = " · " + s.videoRef(videoID)
	}
	fem := noun.fem
	switch en.class {
	case "I":
		return capitalize(gendered(fem, "добавлен ", "добавлена ") + noun.one + " " + name + ctx), videoID
	case "D":
		return capitalize(gendered(fem, "удалён ", "удалена ") + noun.one + " " + name + ctx), videoID
	}
	cols := en.changeCols
	has := func(c string) bool {
		for _, x := range cols {
			if x == c {
				return true
			}
		}
		return false
	}
	switch {
	case has("deleted_at"):
		if en.after["deleted_at"] == nil {
			return capitalize(gendered(fem, "восстановлен ", "восстановлена ") + noun.one + " " + name + ctx), videoID
		}
		return capitalize(noun.one + " " + name + gendered(fem, " удалён в корзину", " удалена в корзину") + ctx), videoID
	case has("archived"):
		if rowInt(en.after, "archived") != 0 {
			return capitalize(noun.one + " " + name + gendered(fem, " отправлен в архив", " отправлена в архив") + ctx), videoID
		}
		return capitalize(noun.one + " " + name + gendered(fem, " возвращён из архива", " возвращена из архива") + ctx), videoID
	case en.tbl == "videos" && len(cols) == 1 && cols[0] == "stage_id":
		return capitalize("ролик " + name + ": этап " + s.entityName("stages", rowInt(en.after, "stage_id"), nil)), videoID
	case en.tbl == "videos" && len(cols) == 1 && cols[0] == "plan_date":
		d := rowStr(en.after, "plan_date")
		if d == "" {
			return capitalize("ролик " + name + ": без даты плана"), videoID
		}
		if t, err := time.Parse("2006-01-02", d); err == nil {
			d = t.Format("02.01")
		}
		return capitalize("ролик " + name + ": план на " + d), videoID
	case en.tbl == "checklist" && has("done_at"):
		if en.after["done_at"] == nil {
			return "Снята отметка " + name + ctx, videoID
		}
		return "Отмечен пункт " + name + ctx, videoID
	case en.tbl == "publications" && has("status"):
		return capitalize(noun.one + " " + name + ": " + pubStatusNames[rowStr(en.after, "status")] + ctx), videoID
	}
	if nameCol := undoNouns[en.tbl].name; len(cols) == 1 && cols[0] == nameCol && en.tbl != "settings" {
		from := shorten(rowStr(en.before, nameCol), 30)
		to := shorten(rowStr(en.after, nameCol), 30)
		subject := noun.one
		if en.tbl == "videos" {
			subject = "ролик " + s.videoCode(rowInt(en.after, "num"))
		}
		return capitalize(gendered(fem, "переименован ", "переименована ") + subject + " «" + from + "» → «" + to + "»" + ctx), videoID
	}
	var what []string
	for _, c := range cols {
		if n, ok := undoColNames[c]; ok {
			what = append(what, n)
		}
	}
	what = uniqSorted(what)
	label := gendered(fem, "изменён ", "изменена ") + noun.one + " " + name
	if en.tbl == "videos" {
		label = "изменён ролик " + name
	}
	if len(what) > 0 && len(what) <= 3 {
		label += ": " + strings.Join(what, ", ")
	}
	return capitalize(label + ctx), videoID
}

func rowOf(en *undoEntity) map[string]any {
	if en.after != nil {
		return en.after
	}
	return en.before
}

func allCols(ents []*undoEntity, tbl, col string) bool {
	for _, en := range ents {
		if en.tbl == tbl && (len(en.changeCols) != 1 || en.changeCols[0] != col) {
			return false
		}
	}
	return true
}

// videoRef renders a video as its code, like LP-0042.
func (s *Server) videoRef(id int64) string {
	var num int64
	if err := s.db.QueryRow(`SELECT num FROM videos WHERE id = ?`, id).Scan(&num); err != nil {
		return "ролика"
	}
	return s.videoCode(num)
}

// entityName renders the name of a row: «Монтаж», LP-0042 «Название», EN…
func (s *Server) entityName(tbl string, rid int64, row map[string]any) string {
	if row == nil && rid > 0 {
		if _, ok := s.undoCols[tbl]; ok {
			row, _ = s.currentRow(s.db, tbl, rid)
		}
	}
	if row == nil {
		return ""
	}
	switch tbl {
	case "videos":
		return s.videoCode(rowInt(row, "num")) + " «" + shorten(rowStr(row, "title"), 40) + "»"
	case "variants":
		return strings.ToUpper(rowStr(row, "language_code"))
	case "publications":
		var name string
		s.db.QueryRow(`SELECT name FROM channels WHERE id = ?`, rowInt(row, "channel_id")).Scan(&name)
		return "«" + name + "»"
	case "day_notes":
		d := rowStr(row, "date")
		if t, err := time.Parse("2006-01-02", d); err == nil {
			d = t.Format("02.01")
		}
		return d
	}
	col := undoNouns[tbl].name
	if col == "" {
		return ""
	}
	return "«" + shorten(rowStr(row, col), 40) + "»"
}

