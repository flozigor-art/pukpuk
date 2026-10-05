package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Editable dictionaries: languages, platforms, accounts (channels), pipeline
// stages, file kinds, tag groups, tags and the checklist template.

type dictField struct {
	col        string
	typ        string // str | optstr | int | optint | bool | color | enum
	max        int
	enum       []string
	required   bool
	createOnly bool
}

type dictSpec struct {
	table  string
	pk     string
	textPK bool
	order  string
	fields []dictField
}

var dicts = map[string]dictSpec{
	"languages": {table: "languages", pk: "code", textPK: true, order: "is_primary DESC, sort, code", fields: []dictField{
		{col: "code", typ: "str", max: 16, required: true, createOnly: true},
		{col: "name", typ: "str", max: 60, required: true},
		{col: "flag", typ: "str", max: 16},
		{col: "sort", typ: "int"},
		{col: "is_primary", typ: "bool"},
		{col: "archived", typ: "bool"},
	}},
	"platforms": {table: "platforms", pk: "id", order: "sort, id", fields: []dictField{
		{col: "name", typ: "str", max: 60, required: true},
		{col: "color", typ: "color"},
		{col: "icon", typ: "str", max: 30},
		{col: "counts_for_quota", typ: "bool"},
		{col: "sort", typ: "int"},
		{col: "archived", typ: "bool"},
	}},
	"channels": {table: "channels", pk: "id", order: "sort, id", fields: []dictField{
		{col: "platform_id", typ: "int", required: true},
		{col: "language_code", typ: "optstr", max: 16},
		{col: "name", typ: "str", max: 100, required: true},
		{col: "url", typ: "str", max: 500},
		{col: "sort", typ: "int"},
		{col: "archived", typ: "bool"},
	}},
	"stages": {table: "stages", pk: "id", order: "sort, id", fields: []dictField{
		{col: "name", typ: "str", max: 60, required: true},
		{col: "color", typ: "color"},
		{col: "kind", typ: "enum", enum: []string{"idea", "work", "ready", "done"}},
		{col: "sort", typ: "int"},
		{col: "archived", typ: "bool"},
	}},
	"kinds": {table: "asset_kinds", pk: "key", textPK: true, order: "sort, key", fields: []dictField{
		{col: "key", typ: "str", max: 40, createOnly: true},
		{col: "name", typ: "str", max: 60, required: true},
		{col: "hint", typ: "str", max: 200},
		{col: "scope", typ: "enum", enum: []string{"variant", "shared"}},
		{col: "required", typ: "bool"},
		{col: "accept", typ: "str", max: 200},
		{col: "sort", typ: "int"},
		{col: "archived", typ: "bool"},
	}},
	"tag_groups": {table: "tag_groups", pk: "id", order: "scope, sort, id", fields: []dictField{
		{col: "scope", typ: "enum", enum: []string{"video", "music"}, required: true, createOnly: true},
		{col: "name", typ: "str", max: 60, required: true},
		{col: "sort", typ: "int"},
	}},
	"tags": {table: "tags", pk: "id", order: "scope, sort, name", fields: []dictField{
		{col: "scope", typ: "enum", enum: []string{"video", "music"}, required: true, createOnly: true},
		{col: "group_id", typ: "optint"},
		{col: "name", typ: "str", max: 60, required: true},
		{col: "color", typ: "str", max: 7},
		{col: "sort", typ: "int"},
	}},
	"checklist": {table: "checklist_template", pk: "id", order: "sort, id", fields: []dictField{
		{col: "label", typ: "str", max: 300, required: true},
		{col: "sort", typ: "int"},
	}},
}

var boolCols = map[string]bool{"is_primary": true, "archived": true, "counts_for_quota": true, "required": true, "disabled": true}

func (s *Server) listDict(spec dictSpec) ([]map[string]any, error) {
	rows, err := s.db.Query(`SELECT * FROM ` + spec.table + ` ORDER BY ` + spec.order)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := map[string]any{}
		for i, c := range cols {
			v := vals[i]
			if b, okb := v.([]byte); okb {
				v = string(b)
			}
			if boolCols[c] {
				n, _ := v.(int64)
				v = n != 0
			}
			m[c] = v
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (f dictField) parse(raw json.RawMessage) (any, error) {
	switch f.typ {
	case "str", "color", "enum":
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, errBad("%s: ожидается строка", f.col)
		}
		v = cleanText(v, max(f.max, 7))
		if f.required && v == "" {
			return nil, errBad("%s: обязательное поле", f.col)
		}
		if f.typ == "color" && !colorRe.MatchString(v) {
			return nil, errBad("%s: цвет в формате #rrggbb", f.col)
		}
		if f.typ == "enum" {
			okv := false
			for _, e := range f.enum {
				okv = okv || e == v
			}
			if !okv {
				return nil, errBad("%s: недопустимое значение", f.col)
			}
		}
		return v, nil
	case "optstr":
		var v *string
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, errBad("%s: ожидается строка или null", f.col)
		}
		if v == nil || strings.TrimSpace(*v) == "" {
			return nil, nil
		}
		return cleanText(*v, f.max), nil
	case "int":
		var v int64
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, errBad("%s: ожидается число", f.col)
		}
		return v, nil
	case "optint":
		var v *int64
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, errBad("%s: ожидается число или null", f.col)
		}
		if v == nil {
			return nil, nil
		}
		return *v, nil
	case "bool":
		var v bool
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, errBad("%s: ожидается true/false", f.col)
		}
		return boolInt(v), nil
	}
	return nil, errBad("unknown field type")
}

var (
	langCodeRe = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})?$`)
	kindKeyRe  = regexp.MustCompile(`^[a-z][a-z0-9_]{1,39}$`)
)

func (s *Server) handleDictCreate(w http.ResponseWriter, r *http.Request) error {
	spec, okd := dicts[r.PathValue("type")]
	if !okd {
		return errNotFound("Справочник")
	}
	var req map[string]json.RawMessage
	if err := readJSON(r, &req); err != nil {
		return err
	}
	var cols []string
	var args []any
	vals := map[string]any{}
	for _, f := range spec.fields {
		raw, given := req[f.col]
		if !given {
			if f.required {
				return errBad("%s: обязательное поле", f.col)
			}
			continue
		}
		v, err := f.parse(raw)
		if err != nil {
			return err
		}
		cols = append(cols, f.col)
		args = append(args, v)
		vals[f.col] = v
	}
	switch spec.table {
	case "languages":
		code, _ := vals["code"].(string)
		if !langCodeRe.MatchString(code) {
			return errBad("Код языка: например ru, en, es, pt-BR")
		}
	case "asset_kinds":
		key, _ := vals["key"].(string)
		if key == "" {
			key = "custom_" + strings.ToLower(randomHex(3))
			cols = append(cols, "key")
			args = append(args, key)
			vals["key"] = key
		} else if !kindKeyRe.MatchString(key) {
			return errBad("Ключ: латиница, цифры и _")
		}
	}
	if _, has := vals["sort"]; !has {
		var next int64
		s.db.QueryRow(`SELECT COALESCE(MAX(sort), -1) + 1 FROM ` + spec.table).Scan(&next)
		cols = append(cols, "sort")
		args = append(args, next)
	}
	ph := strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", ")
	res, err := s.db.Exec(`INSERT INTO `+spec.table+` (`+strings.Join(cols, ", ")+`) VALUES (`+ph+`)`, args...)
	if err != nil {
		if isConstraint(err) {
			return errConflict("Такая запись уже есть или ссылка некорректна")
		}
		return err
	}
	var id any
	if spec.textPK {
		id = vals[spec.pk]
	} else {
		id, _ = res.LastInsertId()
	}
	if spec.table == "languages" && vals["is_primary"] == 1 {
		s.db.Exec(`UPDATE languages SET is_primary = 0 WHERE code != ?`, id)
	}
	s.events.Publish("dicts")
	return ok(w, map[string]any{"id": id})
}

func dictPK(spec dictSpec, r *http.Request) (any, error) {
	raw := r.PathValue("id")
	if spec.textPK {
		if raw == "" {
			return nil, errBad("Некорректный ключ")
		}
		return raw, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, errBad("Некорректный идентификатор")
	}
	return id, nil
}

func (s *Server) handleDictPatch(w http.ResponseWriter, r *http.Request) error {
	spec, okd := dicts[r.PathValue("type")]
	if !okd {
		return errNotFound("Справочник")
	}
	id, err := dictPK(spec, r)
	if err != nil {
		return err
	}
	var req map[string]json.RawMessage
	if err := readJSON(r, &req); err != nil {
		return err
	}
	p := newPatch()
	makePrimary := false
	for key, raw := range req {
		var field *dictField
		for i := range spec.fields {
			if spec.fields[i].col == key && !spec.fields[i].createOnly {
				field = &spec.fields[i]
			}
		}
		if field == nil {
			return errBad("Поле %s нельзя изменить", key)
		}
		v, err := field.parse(raw)
		if err != nil {
			return err
		}
		if key == "is_primary" && v == 1 {
			makePrimary = true
		}
		p.set(key, v)
	}
	if err := p.exec(s.db, spec.table, spec.pk, id); err != nil {
		if isConstraint(err) {
			return errConflict("Некорректная ссылка или дубликат")
		}
		return err
	}
	if makePrimary {
		s.db.Exec(`UPDATE languages SET is_primary = 0 WHERE code != ?`, id)
	}
	s.events.Publish("dicts", "videos")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleDictDelete(w http.ResponseWriter, r *http.Request) error {
	spec, okd := dicts[r.PathValue("type")]
	if !okd {
		return errNotFound("Справочник")
	}
	id, err := dictPK(spec, r)
	if err != nil {
		return err
	}
	if spec.table == "asset_kinds" {
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE kind = ?`, id).Scan(&n)
		if n > 0 {
			return errConflict("Есть файлы этого типа (%d). Его можно архивировать — он пропадёт из списков, файлы останутся", n)
		}
	}
	res, err := s.db.Exec(`DELETE FROM `+spec.table+` WHERE `+spec.pk+` = ?`, id)
	if err != nil {
		if isConstraint(err) {
			return errConflict("Запись используется. Вместо удаления её можно архивировать")
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound("Запись")
	}
	s.events.Publish("dicts", "videos", "music")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleDictReorder(w http.ResponseWriter, r *http.Request) error {
	spec, okd := dicts[r.PathValue("type")]
	if !okd {
		return errNotFound("Справочник")
	}
	var req struct {
		IDs []json.RawMessage `json:"ids"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, raw := range req.IDs {
		var id any
		if spec.textPK {
			var sid string
			if err := json.Unmarshal(raw, &sid); err != nil {
				return errBad("ids")
			}
			id = sid
		} else {
			var nid int64
			if err := json.Unmarshal(raw, &nid); err != nil {
				return errBad("ids")
			}
			id = nid
		}
		if _, err := tx.Exec(`UPDATE `+spec.table+` SET sort = ? WHERE `+spec.pk+` = ?`, i, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.events.Publish("dicts")
	return ok(w, map[string]bool{"ok": true})
}

// ---- bootstrap & settings -------------------------------------------------

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) error {
	out := map[string]any{"me": currentUser(r)}
	users, err := s.listUsers()
	if err != nil {
		return err
	}
	out["users"] = users
	for name, spec := range dicts {
		list, err := s.listDict(spec)
		if err != nil {
			return err
		}
		out[name] = list
	}
	settings, err := s.settings()
	if err != nil {
		return err
	}
	out["settings"] = settings
	out["config"] = map[string]any{
		"version":    Version,
		"chunk_size": s.cfg.ChunkSize,
		"max_file":   s.cfg.MaxFile,
	}
	return ok(w, out)
}

func (s *Server) handlePatchSettings(w http.ResponseWriter, r *http.Request) error {
	var req map[string]string
	if err := readJSON(r, &req); err != nil {
		return err
	}
	for k, v := range req {
		v = strings.TrimSpace(v)
		if _, known := defaultSettings[k]; !known {
			return errBad("Неизвестная настройка %s", k)
		}
		bad := false
		switch k {
		case "code_prefix":
			bad = !regexp.MustCompile(`^[A-Za-z0-9А-Яа-я]{1,8}$`).MatchString(v)
		case "timezone":
			if v != "" {
				_, err := time.LoadLocation(v)
				bad = err != nil
			}
		case "quota_per_day":
			n, err := strconv.Atoi(v)
			bad = err != nil || n < 1 || n > 20
		case "quota_start":
			bad = v != "" && !validDate(v)
		case "archive_hours":
			n, err := strconv.Atoi(v)
			bad = err != nil || n < 1 || n > 24*60
		case "window_days":
			n, err := strconv.Atoi(v)
			bad = err != nil || n < 7 || n > 365
		case "auto_done_stage":
			bad = v != "0" && v != "1"
		}
		if bad {
			return errBad("Некорректное значение для %s", k)
		}
		if _, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return err
		}
	}
	s.reloadLocation()
	s.events.Publish("dicts", "videos", "calendar")
	return ok(w, map[string]bool{"ok": true})
}

// ---- users ----------------------------------------------------------------

var loginRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,31}$`)

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Login    string `json:"login"`
		Name     string `json:"name"`
		Password string `json:"password"`
		Role     string `json:"role"`
		Color    string `json:"color"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	req.Login = strings.ToLower(strings.TrimSpace(req.Login))
	if !loginRe.MatchString(req.Login) {
		return errBad("Логин: латиница, цифры, точка, дефис; 2–32 символа")
	}
	if len(req.Password) < 8 {
		return errBad("Пароль — минимум 8 символов")
	}
	if req.Role != "admin" {
		req.Role = "member"
	}
	if !colorRe.MatchString(req.Color) {
		req.Color = "#6366f1"
	}
	name := cleanText(req.Name, 60)
	if name == "" {
		name = req.Login
	}
	hash, err := HashPassword(req.Password)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`INSERT INTO users (login, name, role, color, password_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		req.Login, name, req.Role, req.Color, hash, nowMs())
	if err != nil {
		if isConstraint(err) {
			return errConflict("Такой логин уже есть")
		}
		return err
	}
	id, _ := res.LastInsertId()
	s.events.Publish("users", "dicts")
	return ok(w, map[string]int64{"id": id})
}

func (s *Server) handlePatchUser(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	u := currentUser(r)
	if u.ID != id && !u.admin() {
		return errForbidden()
	}
	var req map[string]json.RawMessage
	if err := readJSON(r, &req); err != nil {
		return err
	}
	p := newPatch()
	for key, raw := range req {
		switch key {
		case "name":
			v, err := p.str(raw, 60)
			if err != nil || v == "" {
				return errBad("Имя не может быть пустым")
			}
			p.set(key, v)
		case "color":
			v, err := p.str(raw, 7)
			if err != nil || !colorRe.MatchString(v) {
				return errBad("Цвет в формате #rrggbb")
			}
			p.set(key, v)
		case "role", "disabled":
			if !u.admin() {
				return errForbidden()
			}
			if u.ID == id {
				return errBad("Нельзя менять собственную роль или блокировать себя")
			}
			if key == "role" {
				v, err := p.str(raw, 10)
				if err != nil || (v != "admin" && v != "member") {
					return errBad("Роль: admin или member")
				}
				p.set(key, v)
			} else {
				var b bool
				if err := json.Unmarshal(raw, &b); err != nil {
					return errBad("disabled: true/false")
				}
				p.set(key, boolInt(b))
				if b {
					s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, id)
				}
			}
		default:
			return errBad("Поле %s нельзя изменить", key)
		}
	}
	if err := p.exec(s.db, "users", "id", id); err != nil {
		return err
	}
	s.events.Publish("users", "dicts")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleUserPassword(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	u := currentUser(r)
	if u.ID != id && !u.admin() {
		return errForbidden()
	}
	var req struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if len(req.New) < 8 {
		return errBad("Пароль — минимум 8 символов")
	}
	if u.ID == id {
		var hash string
		if err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, id).Scan(&hash); err != nil {
			return err
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Old)) != nil {
			return errBad("Текущий пароль указан неверно")
		}
	}
	hash, err := HashPassword(req.New)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, id); err != nil {
		return err
	}
	// sign out other sessions of this user
	cur := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		cur = hashToken(c.Value)
	}
	s.db.Exec(`DELETE FROM sessions WHERE user_id = ? AND token_hash != ?`, id, cur)
	return ok(w, map[string]bool{"ok": true})
}

var _ = sql.ErrNoRows
