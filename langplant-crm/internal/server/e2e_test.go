package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"langplant-crm/internal/config"
	"langplant-crm/internal/node"
)

const testToken = "test-node-token-0123456789abcdef"

type client struct {
	t    *testing.T
	base string
	hc   *http.Client
}

func (c *client) do(method, path string, body any, out any) int {
	c.t.Helper()
	var rd io.Reader
	if b, ok := body.([]byte); ok {
		rd = bytes.NewReader(b)
	} else if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("X-CRM", "1")
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(data, out); err != nil {
			c.t.Fatalf("%s %s: decode %q: %v", method, path, data, err)
		}
	}
	if resp.StatusCode >= 300 && out != nil {
		c.t.Logf("%s %s → %d %s", method, path, resp.StatusCode, data)
	}
	return resp.StatusCode
}

func (c *client) get(path string, hdr map[string]string) (int, []byte, http.Header) {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, c.base+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func testVideo(t *testing.T, dir string) ([]byte, string) {
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		p := filepath.Join(dir, "clip.mp4")
		cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=360x640:rate=25:duration=2",
			"-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", "-y", p)
		if out, err := cmd.CombinedOutput(); err == nil {
			b, _ := os.ReadFile(p)
			return b, "video/mp4"
		} else {
			t.Logf("ffmpeg failed, using random bytes: %s", out)
		}
	}
	b := make([]byte, 300_000)
	rand.Read(b)
	return b, "application/octet-stream"
}

func TestEndToEnd(t *testing.T) {
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
	srv.Start()
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	hash, _ := HashPassword("secret-pass")
	srv.DB().Exec(`UPDATE users SET password_hash = ? WHERE login = 'sasha'`, hash)
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: ts.URL, hc: &http.Client{Jar: jar}}

	// CSRF header is required for mutations
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/login", bytes.NewReader([]byte(`{}`)))
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without X-CRM, got %d", resp.StatusCode)
	}
	if code := c.do("POST", "/api/auth/login", map[string]string{"login": "sasha", "password": "wrong"}, nil); code != 401 {
		t.Fatalf("bad password: %d", code)
	}
	var me User
	if code := c.do("POST", "/api/auth/login", map[string]string{"login": "Sasha", "password": "secret-pass"}, &me); code != 200 || me.Role != "admin" {
		t.Fatalf("login: %d %+v", code, me)
	}

	var boot map[string]json.RawMessage
	c.do("GET", "/api/bootstrap", nil, &boot)
	var users []User
	json.Unmarshal(boot["users"], &users)
	if len(users) != 4 {
		t.Fatalf("expected 4 seeded users, got %d", len(users))
	}
	var channels []map[string]any
	json.Unmarshal(boot["channels"], &channels)

	var v Video
	if code := c.do("POST", "/api/videos", map[string]any{"title": "Как заказать кофе"}, &v); code != 200 {
		t.Fatalf("create video: %d", code)
	}
	if v.Code != "LP-0001" || len(v.Variants) != 1 || v.Variants[0].Lang != "ru" {
		t.Fatalf("unexpected video: %+v", v)
	}
	variant := v.Variants[0].ID

	// --- resumable chunked upload
	data, mime := testVideo(t, root)
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	var up struct {
		ID        string `json:"id"`
		ChunkSize int64  `json:"chunk_size"`
	}
	c.do("POST", "/api/uploads", map[string]any{"filename": "final.mp4", "size": len(data), "mime": mime,
		"target": map[string]any{"type": "asset", "video_id": v.ID, "variant_id": variant, "kind": "final"}}, &up)
	if up.ID == "" {
		t.Fatal("no upload id")
	}
	var last map[string]any
	for off := int64(0); off < int64(len(data)); {
		end := min(off+up.ChunkSize, int64(len(data)))
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/uploads/"+up.ID, bytes.NewReader(data[off:end]))
		req.Header.Set("X-CRM", "1")
		req.Header.Set("Upload-Offset", strconv.FormatInt(off, 10))
		resp, err := c.hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		last = nil
		json.NewDecoder(resp.Body).Decode(&last)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("chunk at %d: %d %v", off, resp.StatusCode, last)
		}
		off = end
	}
	if last["done"] != true {
		t.Fatalf("upload not finished: %v", last)
	}
	if got := last["result"].(map[string]any)["sha256"]; got != sha {
		t.Fatalf("sha mismatch: %v vs %s", got, sha)
	}

	// --- storage node pulls the file
	ndir := filepath.Join(root, "pc")
	ncfg := config.Node{ServerURL: ts.URL, Token: testToken, DataDir: ndir, FFmpeg: "ffmpeg", FFprobe: "ffprobe", PreviewHeight: 320, Downloads: 2, TrashDays: 30}
	n, err := node.New(ncfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.Run(ctx)

	blobState := func() string {
		var st string
		srv.DB().QueryRow(`SELECT state FROM blobs WHERE sha256 = ?`, sha).Scan(&st)
		return st
	}
	waitFor(t, "blob stored on node", 10*time.Second, func() bool { return blobState() == "stored" })
	if b, err := os.ReadFile(filepath.Join(ndir, "blobs", sha[:2], sha)); err != nil || !bytes.Equal(b, data) {
		t.Fatalf("node copy differs: %v", err)
	}

	if mime == "video/mp4" {
		waitFor(t, "derivatives", 60*time.Second, func() bool {
			var st string
			srv.DB().QueryRow(`SELECT derive_state FROM blobs WHERE sha256 = ?`, sha).Scan(&st)
			return st == "done"
		})
		for _, name := range []string{"poster.jpg", "thumb.jpg", "preview.mp4"} {
			code, body, _ := c.get("/api/files/"+sha+"/"+name, nil)
			if code != 200 || len(body) == 0 {
				t.Fatalf("derived %s: %d", name, code)
			}
		}
		var w, h int
		srv.DB().QueryRow(`SELECT width, height FROM blobs WHERE sha256 = ?`, sha).Scan(&w, &h)
		if w != 360 || h != 640 {
			t.Fatalf("probe: %dx%d", w, h)
		}
	}

	// --- evict the local copy: reads must now stream from the node
	srv.store.mu.Lock()
	os.Remove(srv.store.blobPath(sha))
	srv.DB().Exec(`UPDATE blobs SET local = 0 WHERE sha256 = ?`, sha)
	srv.store.mu.Unlock()
	srv.cfg.WarmMax = 0 // keep streaming from the node for the checks below

	code, body, hdr := c.get("/api/files/"+sha, nil)
	if code != 200 || !bytes.Equal(body, data) {
		t.Fatalf("full read via node: %d, %d bytes", code, len(body))
	}
	if hdr.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("no Accept-Ranges")
	}
	code, body, hdr = c.get("/api/files/"+sha, map[string]string{"Range": "bytes=1000-70999"})
	if code != 206 || !bytes.Equal(body, data[1000:71000]) {
		t.Fatalf("range read via node: %d, %d bytes", code, len(body))
	}
	if hdr.Get("Content-Range") != fmt.Sprintf("bytes 1000-70999/%d", len(data)) {
		t.Fatalf("content-range: %s", hdr.Get("Content-Range"))
	}
	code, body, _ = c.get("/api/files/"+sha, map[string]string{"Range": "bytes=-100"})
	if code != 206 || !bytes.Equal(body, data[len(data)-100:]) {
		t.Fatalf("suffix range: %d", code)
	}
	code, _, hdr = c.get("/api/files/"+sha+"?dl=1&name="+"Кофе.mp4", nil)
	if code != 200 || hdr.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("download headers: %d %v", code, hdr)
	}

	// --- zip bundle streams through the node too
	code, body, _ = c.get(fmt.Sprintf("/api/videos/%d/zip", v.ID), nil)
	if code != 200 || len(body) < len(data) {
		t.Fatalf("zip: %d %d", code, len(body))
	}

	// --- warm cache pulls small files to the VPS after a view
	srv.cfg.WarmMax = 64 << 20
	c.get("/api/files/"+sha, map[string]string{"Range": "bytes=0-9"})
	waitFor(t, "warm cache", 10*time.Second, func() bool {
		var local bool
		srv.DB().QueryRow(`SELECT local FROM blobs WHERE sha256 = ?`, sha).Scan(&local)
		return local
	})

	// --- publication → plan statistics
	c.do("POST", "/api/publications", map[string]any{"variant_id": variant, "channel_id": channels[0]["id"], "status": "published"}, nil)
	var dash struct {
		Plan    PlanStats `json:"plan"`
		Archive []Video   `json:"archive"`
	}
	c.do("GET", "/api/dashboard", nil, &dash)
	if dash.Plan.TodayStatus != "done" || dash.Plan.Streak != 1 {
		t.Fatalf("plan stats: %+v", dash.Plan)
	}
	if len(dash.Archive) != 1 || dash.Archive[0].Archive.Missing != 2 {
		t.Fatalf("archive check: %+v", dash.Archive)
	}
	var vd VideoDetail
	c.do("GET", fmt.Sprintf("/api/videos/%d", v.ID), nil, &vd)
	var stageKind string
	srv.DB().QueryRow(`SELECT kind FROM stages WHERE id = ?`, *vd.StageID).Scan(&stageKind)
	if stageKind != "done" {
		t.Fatalf("video should be moved to the done stage, got %s", stageKind)
	}

	// --- database backup lands on the node
	if err := n.BackupNow(ctx); err != nil {
		t.Fatal(err)
	}
	if m, _ := filepath.Glob(filepath.Join(ndir, "backups", "crm-*.db")); len(m) != 1 {
		t.Fatalf("backup files: %v", m)
	}
	out := filepath.Join(root, "export")
	if err := node.Export(ndir, "", out, false); err != nil {
		t.Fatal(err)
	}
	if m, _ := filepath.Glob(filepath.Join(out, "Ролики", "*", "ru", "*", "final.mp4")); len(m) != 1 {
		t.Fatalf("export tree: %v", m)
	}

	// --- delete + purge moves the file to the node trash
	assetID := vd.Assets[0].ID
	c.do("DELETE", fmt.Sprintf("/api/assets/%d", assetID), nil, nil)
	if code := c.do("DELETE", fmt.Sprintf("/api/trash/asset/%d", assetID), nil, &map[string]any{}); code != 200 {
		t.Fatalf("purge: %d", code)
	}
	waitFor(t, "node trash", 5*time.Second, func() bool {
		m, _ := filepath.Glob(filepath.Join(ndir, "trash", "*-"+sha))
		return len(m) == 1
	})
	if _, err := os.Stat(filepath.Join(ndir, "blobs", sha[:2], sha)); !os.IsNotExist(err) {
		t.Fatal("blob still in node storage")
	}
}

func TestParseRange(t *testing.T) {
	cases := []struct {
		h          string
		off, ln    int64
		partial    bool
		shouldFail bool
	}{
		{"", 0, 100, false, false},
		{"bytes=0-", 0, 100, true, false},
		{"bytes=10-19", 10, 10, true, false},
		{"bytes=90-200", 90, 10, true, false},
		{"bytes=-30", 70, 30, true, false},
		{"bytes=100-", 0, 0, false, true},
		{"bytes=5-1", 0, 100, false, true},
	}
	for _, c := range cases {
		off, ln, partial, err := parseRange(c.h, 100)
		if (err != nil) != c.shouldFail || (err == nil && (off != c.off || ln != c.ln || partial != c.partial)) {
			t.Errorf("%q: got %d %d %v %v", c.h, off, ln, partial, err)
		}
	}
}
