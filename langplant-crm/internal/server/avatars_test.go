package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"strings"
	"testing"
)

func (c *undoClient) raw(method, path string, body []byte) (int, []byte, http.Header) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	req.Header.Set("X-CRM", "1")
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header
}

func testJPEG(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for i := range img.Pix {
		img.Pix[i] = byte(i)
	}
	img.Set(0, 0, color.White)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func avatarAt(t *testing.T, c *undoClient, login string) *int64 {
	t.Helper()
	var boot struct {
		Users []User `json:"users"`
	}
	c.must("GET", "/api/bootstrap", nil, &boot)
	for _, u := range boot.Users {
		if u.Login == login {
			return u.AvatarAt
		}
	}
	t.Fatalf("user %s not found", login)
	return nil
}

func TestAvatars(t *testing.T) {
	_, sasha, ksenia := undoSetup(t)
	pic := testJPEG(t)

	// everyone sets their own picture
	if code, body, _ := ksenia.raw("PUT", "/api/users/2/avatar", pic); code != 200 {
		t.Fatalf("own avatar: %d %s", code, body)
	}
	at := avatarAt(t, sasha, "ksenia")
	if at == nil {
		t.Fatal("avatar_at not set")
	}
	code, got, hdr := sasha.raw("GET", "/api/users/2/avatar", nil)
	if code != 200 || !bytes.Equal(got, pic) || hdr.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("get avatar: %d %s %d bytes", code, hdr.Get("Content-Type"), len(got))
	}

	// but not someone else's, unless admin
	if code, _, _ := ksenia.raw("PUT", "/api/users/1/avatar", pic); code != 403 {
		t.Fatalf("member set admin's avatar: %d", code)
	}
	if code, _, _ := sasha.raw("PUT", "/api/users/3/avatar", pic); code != 200 {
		t.Fatalf("admin set member's avatar: %d", code)
	}

	// only pictures, and not huge ones
	if code, body, _ := ksenia.raw("PUT", "/api/users/2/avatar", []byte("<svg onload=alert(1)>")); code != 400 || !strings.Contains(string(body), "картинка") {
		t.Fatalf("non-image accepted: %d %s", code, body)
	}
	if code, _, _ := ksenia.raw("PUT", "/api/users/2/avatar", append(pic, make([]byte, avatarMax)...)); code != 400 {
		t.Fatalf("huge image accepted: %d", code)
	}

	// /auth/me carries the version too (the sidebar avatar)
	var me User
	ksenia.must("GET", "/api/auth/me", nil, &me)
	if me.AvatarAt == nil || *me.AvatarAt != *at {
		t.Fatalf("me.avatar_at = %v, want %d", me.AvatarAt, *at)
	}

	if code, _, _ := ksenia.raw("DELETE", "/api/users/2/avatar", nil); code != 200 {
		t.Fatalf("delete avatar: %d", code)
	}
	if avatarAt(t, sasha, "ksenia") != nil {
		t.Fatal("avatar_at still set after delete")
	}
	if code, _, _ := sasha.raw("GET", "/api/users/2/avatar", nil); code != 404 {
		t.Fatalf("deleted avatar served: %d", code)
	}
}

func TestWaveformResolution(t *testing.T) {
	srv, sasha, _ := undoSetup(t)
	sha := strings.Repeat("ab", 32)
	if _, err := srv.DB().Exec(`INSERT INTO blobs (sha256, size, mime, state, created_at, peaks) VALUES (?, 1, 'audio/mpeg', 'stored', 0, '[1,2,3]')`, sha); err != nil {
		t.Fatal(err)
	}
	peaks := func() []int {
		var s string
		srv.DB().QueryRow(`SELECT peaks FROM blobs WHERE sha256 = ?`, sha).Scan(&s)
		var p []int
		json.Unmarshal([]byte(s), &p)
		return p
	}
	put := func(n int) {
		p := make([]int, n)
		for i := range p {
			p[i] = i % 101
		}
		sasha.must("PUT", "/api/files/"+sha+"/peaks", map[string]any{"peaks": p}, nil)
	}
	put(1000) // a finer waveform replaces the old coarse one
	if n := len(peaks()); n != 1000 {
		t.Fatalf("peaks not upgraded: %d", n)
	}
	put(200) // a coarser one does not
	if n := len(peaks()); n != 1000 {
		t.Fatalf("peaks downgraded: %d", n)
	}

	full := make([]int, 1000)
	full[999] = 100
	b, _ := json.Marshal(full)
	var small []int
	json.Unmarshal(downsamplePeaks(b, listWaveformBins), &small)
	if len(small) != listWaveformBins || small[len(small)-1] != 100 {
		t.Fatalf("downsample keeps the loudest value of each group: len %d, last %d", len(small), small[len(small)-1])
	}
	if got := string(downsamplePeaks(json.RawMessage("null"), 10)); got != "null" {
		t.Fatalf("null peaks: %s", got)
	}
}
