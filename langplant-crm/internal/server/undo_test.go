package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"langplant-crm/internal/config"
)

type undoClient struct {
	t    *testing.T
	base string
	hc   *http.Client
}

type undoResp struct {
	code   int
	body   []byte
	step   int64
	label  string
	merged bool
}

func (c *undoClient) call(method, path string, body any) undoResp {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("X-CRM", "1")
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	r := undoResp{code: resp.StatusCode, body: b}
	r.step, _ = strconv.ParseInt(resp.Header.Get("X-Undo-Step"), 10, 64)
	r.label, _ = url.PathUnescape(resp.Header.Get("X-Undo-Label"))
	r.merged = resp.Header.Get("X-Undo-Merged") == "1"
	return r
}

func (c *undoClient) must(method, path string, body any, out any) undoResp {
	c.t.Helper()
	r := c.call(method, path, body)
	if r.code >= 300 {
		c.t.Fatalf("%s %s → %d %s", method, path, r.code, r.body)
	}
	if out != nil {
		if err := json.Unmarshal(r.body, out); err != nil {
			c.t.Fatal(err)
		}
	}
	return r
}

func undoSetup(t *testing.T) (*Server, *undoClient, *undoClient) {
	root := t.TempDir()
	cfg := config.Server{
		Addr: ":0", DataDir: filepath.Join(root, "vps"), NodeToken: testToken, TZ: "Europe/Moscow",
		BufferMax: 1 << 30, CacheMax: 1 << 30, PreviewMax: 1 << 30, DiskReserve: 1 << 20, WarmMax: 64 << 20,
		ChunkSize: 64 << 10, MaxFile: 1 << 30,
	}
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
	})
	hash, _ := HashPassword("secret-pass")
	srv.DB().Exec(`UPDATE users SET password_hash = ?`, hash)
	login := func(name string) *undoClient {
		jar, _ := cookiejar.New(nil)
		c := &undoClient{t: t, base: ts.URL, hc: &http.Client{Jar: jar}}
		c.must("POST", "/api/auth/login", map[string]string{"login": name, "password": "secret-pass"}, nil)
		return c
	}
	return srv, login("sasha"), login("ksenia")
}

func stageName(t *testing.T, srv *Server, id int64) string {
	var name string
	if err := srv.DB().QueryRow(`SELECT name FROM stages WHERE id = ?`, id).Scan(&name); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestUndoDictionaries(t *testing.T) {
	srv, admin, member := undoSetup(t)
	var stageID int64
	srv.DB().QueryRow(`SELECT id FROM stages ORDER BY sort LIMIT 1`).Scan(&stageID)
	orig := stageName(t, srv, stageID)

	// a member renames a stage; the response carries the step and a label
	r := member.must("PATCH", "/api/dict/stages/"+strconv.FormatInt(stageID, 10), map[string]any{"name": "Черновик", "color": "#ff0000"}, nil)
	if r.step == 0 || !strings.Contains(r.label, "этап") || !strings.Contains(r.label, "Черновик") {
		t.Fatalf("step/label: %d %q", r.step, r.label)
	}
	// a request that changes nothing records no step
	if r2 := member.must("PATCH", "/api/dict/stages/"+strconv.FormatInt(stageID, 10), map[string]any{"name": "Черновик"}, nil); r2.step != 0 {
		t.Fatalf("no-op recorded a step: %+v", r2)
	}
	// anyone may undo it
	var undo struct {
		ID   int64  `json:"id"`
		Kind string `json:"kind"`
	}
	admin.must("POST", "/api/undo/"+strconv.FormatInt(r.step, 10), nil, &undo)
	if got := stageName(t, srv, stageID); got != orig || undo.Kind != "undo" {
		t.Fatalf("after undo: %q (want %q), kind %s", got, orig, undo.Kind)
	}
	// undoing twice is refused
	if r := admin.call("POST", "/api/undo/"+strconv.FormatInt(r.step, 10), nil); r.code != 409 {
		t.Fatalf("double undo: %d %s", r.code, r.body)
	}
	// the undo itself can be undone (redo)
	var redo struct{ Kind string }
	member.must("POST", "/api/undo/"+strconv.FormatInt(undo.ID, 10), nil, &redo)
	if got := stageName(t, srv, stageID); got != "Черновик" || redo.Kind != "redo" {
		t.Fatalf("after redo: %q %s", got, redo.Kind)
	}

	// deleting a tag group sets tags.group_id to NULL (FK action); undo brings both back
	var groupID, tagCount int64
	srv.DB().QueryRow(`SELECT g.id, COUNT(t.id) FROM tag_groups g JOIN tags t ON t.group_id = g.id GROUP BY g.id ORDER BY g.id LIMIT 1`).Scan(&groupID, &tagCount)
	if groupID == 0 || tagCount == 0 {
		t.Fatal("seed has no tag group with tags")
	}
	d := member.must("DELETE", "/api/dict/tag_groups/"+strconv.FormatInt(groupID, 10), nil, nil)
	var n int64
	srv.DB().QueryRow(`SELECT COUNT(*) FROM tags WHERE group_id = ?`, groupID).Scan(&n)
	if n != 0 {
		t.Fatalf("tags still in group: %d", n)
	}
	member.must("POST", "/api/undo/"+strconv.FormatInt(d.step, 10), nil, nil)
	srv.DB().QueryRow(`SELECT COUNT(*) FROM tags WHERE group_id = ?`, groupID).Scan(&n)
	if n != tagCount {
		t.Fatalf("tags back in group: %d, want %d", n, tagCount)
	}

	// settings are admin-only, and so is undoing them
	s := admin.must("PATCH", "/api/settings", map[string]string{"quota_per_day": "3"}, nil)
	if !strings.Contains(s.label, "план в день") {
		t.Fatalf("settings label %q", s.label)
	}
	if r := member.call("POST", "/api/undo/"+strconv.FormatInt(s.step, 10), nil); r.code != 403 {
		t.Fatalf("member undid settings: %d", r.code)
	}
	admin.must("POST", "/api/undo/"+strconv.FormatInt(s.step, 10), nil, nil)
	if v := srv.setting("quota_per_day"); v != "1" {
		t.Fatalf("quota after undo %q", v)
	}
}

func TestUndoConflictsAndCascades(t *testing.T) {
	srv, admin, member := undoSetup(t)
	var v struct {
		ID int64 `json:"id"`
	}
	create := member.must("POST", "/api/videos", map[string]any{"title": "Первый"}, &v)
	if !strings.Contains(create.label, "Добавлен ролик") {
		t.Fatalf("create label %q", create.label)
	}
	vid := strconv.FormatInt(v.ID, 10)

	// two people edit the same video; undoing the older edit would overwrite the newer one
	rename := member.must("PATCH", "/api/videos/"+vid, map[string]any{"title": "Второй"}, nil)
	admin.must("PATCH", "/api/videos/"+vid, map[string]any{"title": "Третий"}, nil)
	r := member.call("POST", "/api/undo/"+strconv.FormatInt(rename.step, 10), nil)
	if r.code != 409 || !strings.Contains(string(r.body), "Саша") {
		t.Fatalf("conflict expected: %d %s", r.code, r.body)
	}
	// the history shows the blocked step and which later step blocks it
	var hist []UndoItem
	member.must("GET", "/api/undo", nil, &hist)
	for _, it := range hist {
		if it.ID == rename.step && (!it.Blocked || it.BlockedBy == nil || *it.BlockedBy != hist[0].ID) {
			t.Fatalf("rename step not blocked by the newer edit: %+v", it)
		}
		if it.ID == hist[0].ID && it.Blocked {
			t.Fatalf("latest step reported blocked: %+v", it)
		}
	}

	// tags and a checklist item belong to the video; undoing the creation removes everything,
	// redo brings everything back
	var tagID int64
	srv.DB().QueryRow(`SELECT id FROM tags WHERE scope = 'video' LIMIT 1`).Scan(&tagID)
	tags := member.must("PUT", "/api/videos/"+vid+"/tags", map[string]any{"tags": []int64{tagID}}, nil)
	if !strings.Contains(tags.label, "теги ролика") {
		t.Fatalf("tags label %q", tags.label)
	}
	// undoing the creation conflicts with the later rename by Sasha
	if r := member.call("POST", "/api/undo/"+strconv.FormatInt(create.step, 10), nil); r.code != 409 {
		t.Fatalf("expected conflict on create undo: %d %s", r.code, r.body)
	}

	var w struct {
		ID int64 `json:"id"`
	}
	c2 := member.must("POST", "/api/videos", map[string]any{"title": "Каскад", "tags": []int64{tagID}}, &w)
	wid := strconv.FormatInt(w.ID, 10)
	member.must("POST", "/api/videos/"+wid+"/checklist", map[string]any{"label": "Снять"}, nil)
	var undo struct{ ID int64 }
	member.must("POST", "/api/undo/"+strconv.FormatInt(c2.step, 10), nil, &undo)
	var n int
	srv.DB().QueryRow(`SELECT COUNT(*) FROM videos WHERE id = ?`, w.ID).Scan(&n)
	if n != 0 {
		t.Fatal("video still exists after undoing its creation")
	}
	member.must("POST", "/api/undo/"+strconv.FormatInt(undo.ID, 10), nil, nil)
	var title string
	srv.DB().QueryRow(`SELECT title FROM videos WHERE id = ?`, w.ID).Scan(&title)
	srv.DB().QueryRow(`SELECT COUNT(*) FROM checklist WHERE video_id = ? AND label = 'Снять'`, w.ID).Scan(&n)
	if title != "Каскад" || n != 1 {
		t.Fatalf("redo restored %q, checklist %d", title, n)
	}
	srv.DB().QueryRow(`SELECT COUNT(*) FROM video_tags WHERE video_id = ?`, w.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("tags after redo: %d", n)
	}

	// trash: delete and undo
	del := member.must("DELETE", "/api/videos/"+wid, nil, nil)
	if !strings.Contains(del.label, "корзину") {
		t.Fatalf("delete label %q", del.label)
	}
	member.must("POST", "/api/undo/"+strconv.FormatInt(del.step, 10), nil, nil)
	var deleted *int64
	srv.DB().QueryRow(`SELECT deleted_at FROM videos WHERE id = ?`, w.ID).Scan(&deleted)
	if deleted != nil {
		t.Fatal("video still in trash")
	}
}

func TestUndoMergeAndLast(t *testing.T) {
	srv, _, member := undoSetup(t)
	var v struct {
		ID int64 `json:"id"`
	}
	member.must("POST", "/api/videos", map[string]any{"title": "Сценарий"}, &v)
	vid := strconv.FormatInt(v.ID, 10)

	// autosave of the script: several saves merge into one step
	first := member.must("PATCH", "/api/videos/"+vid, map[string]any{"script": "Ра"}, nil)
	second := member.must("PATCH", "/api/videos/"+vid, map[string]any{"script": "Раз два"}, nil)
	third := member.must("PATCH", "/api/videos/"+vid, map[string]any{"script": "Раз два три"}, nil)
	if first.merged || !second.merged || !third.merged || second.step != first.step || third.step != first.step {
		t.Fatalf("merge: %+v %+v %+v", first, second, third)
	}
	// a different field is a new step
	notes := member.must("PATCH", "/api/videos/"+vid, map[string]any{"notes": "x"}, nil)
	if notes.merged || notes.step == first.step {
		t.Fatalf("notes merged into script step")
	}

	// Ctrl+Z walks back through own changes, Ctrl+Shift+Z brings them back
	member.must("POST", "/api/undo/last", map[string]bool{"redo": false}, nil)
	member.must("POST", "/api/undo/last", map[string]bool{"redo": false}, nil)
	var script, notesV string
	srv.DB().QueryRow(`SELECT script, notes FROM videos WHERE id = ?`, v.ID).Scan(&script, &notesV)
	if script != "" || notesV != "" {
		t.Fatalf("after two undos: %q %q", script, notesV)
	}
	member.must("POST", "/api/undo/last", map[string]bool{"redo": true}, nil)
	srv.DB().QueryRow(`SELECT script, notes FROM videos WHERE id = ?`, v.ID).Scan(&script, &notesV)
	if script != "Раз два три" || notesV != "" {
		t.Fatalf("after redo: %q %q", script, notesV)
	}

	// history lists steps newest first, with undone flags
	var list []UndoItem
	member.must("GET", "/api/undo?limit=10", nil, &list)
	if len(list) < 4 || list[0].Kind != "redo" {
		t.Fatalf("history: %+v", list)
	}
	for _, it := range list {
		if it.Label == "" {
			t.Fatalf("empty label: %+v", it)
		}
	}
}
