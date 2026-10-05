package server

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// apiError is returned by handlers and rendered as {"error": code, "message": msg}.
type apiError struct {
	Status int
	Code   string
	Msg    string
}

func (e *apiError) Error() string { return e.Msg }

func errBad(format string, a ...any) error {
	return &apiError{http.StatusBadRequest, "bad_request", fmt.Sprintf(format, a...)}
}
func errNotFound(what string) error {
	return &apiError{http.StatusNotFound, "not_found", what + " не найден(о)"}
}
func errForbidden() error {
	return &apiError{http.StatusForbidden, "forbidden", "Недостаточно прав"}
}
func errConflict(format string, a ...any) error {
	return &apiError{http.StatusConflict, "conflict", fmt.Sprintf(format, a...)}
}

type handler func(w http.ResponseWriter, r *http.Request) error

// wrap turns a handler returning an error into an http.HandlerFunc.
func wrap(fn handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := fn(w, r)
		if err == nil {
			return
		}
		var ae *apiError
		if errors.As(err, &ae) {
			writeJSON(w, ae.Status, map[string]string{"error": ae.Code, "message": ae.Msg})
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "Не найдено"})
			return
		}
		if isConstraint(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "conflict", "message": "Запись используется или нарушает ограничения. Вместо удаления её можно архивировать."})
			return
		}
		if errors.Is(err, errClientGone) {
			return
		}
		slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal", "message": "Внутренняя ошибка сервера"})
	}
}

var errClientGone = errors.New("client gone")

func isConstraint(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "constraint failed") || strings.Contains(s, "FOREIGN KEY")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func ok(w http.ResponseWriter, v any) error {
	writeJSON(w, http.StatusOK, v)
	return nil
}

func readJSON(r *http.Request, v any) error {
	body := http.MaxBytesReader(nil, r.Body, 4<<20)
	dec := json.NewDecoder(body)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return errBad("Пустой запрос")
		}
		return errBad("Некорректный JSON: %v", err)
	}
	return nil
}

func nowMs() int64 { return time.Now().UnixMilli() }

func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, errBad("Некорректный идентификатор")
	}
	return id, nil
}

func nullInt(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func nullStr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// cleanText trims and limits a user supplied string.
func cleanText(s string, max int) string {
	s = strings.TrimSpace(strings.ToValidUTF8(s, ""))
	if utf8.RuneCountInString(s) > max {
		r := []rune(s)
		s = string(r[:max])
	}
	return s
}

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func validDate(s string) bool {
	if !dateRe.MatchString(s) {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// safeFilename strips path elements and characters that are troublesome on disk.
func safeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base("/" + name)
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		name = "file"
	}
	if utf8.RuneCountInString(name) > 200 {
		ext := filepath.Ext(name)
		r := []rune(strings.TrimSuffix(name, ext))
		name = string(r[:200-utf8.RuneCountInString(ext)]) + ext
	}
	return name
}

func contentDisposition(kind, filename string) string {
	ascii := strings.Map(func(r rune) rune {
		if r > 126 || r < 32 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, filename)
	return mime.FormatMediaType(kind, map[string]string{"filename": ascii}) + "; filename*=UTF-8''" + url.PathEscape(filename)
}

// detectMime picks a mime type from the client-provided value and the extension.
func detectMime(filename, given string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if m, ok := extraMime[ext]; ok {
		return m
	}
	if m := mime.TypeByExtension(ext); m != "" {
		if i := strings.Index(m, ";"); i > 0 {
			m = m[:i]
		}
		return m
	}
	given = strings.ToLower(strings.TrimSpace(given))
	if i := strings.Index(given, ";"); i > 0 {
		given = given[:i]
	}
	if regexp.MustCompile(`^[a-z0-9.+-]+/[a-z0-9.+-]+$`).MatchString(given) {
		return given
	}
	return "application/octet-stream"
}

var extraMime = map[string]string{
	".mp4": "video/mp4", ".m4v": "video/mp4", ".mov": "video/quicktime", ".webm": "video/webm", ".mkv": "video/x-matroska",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".m4a": "audio/mp4", ".aac": "audio/aac", ".flac": "audio/flac", ".ogg": "audio/ogg", ".opus": "audio/ogg",
	".srt": "application/x-subrip", ".vtt": "text/vtt", ".ass": "text/x-ssa",
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".webp": "image/webp", ".gif": "image/gif", ".heic": "image/heic",
	".psd": "image/vnd.adobe.photoshop", ".prproj": "application/octet-stream", ".drp": "application/octet-stream",
	".zip": "application/zip", ".txt": "text/plain", ".md": "text/markdown", ".pdf": "application/pdf",
}

// inlineSafe reports whether a mime type may be rendered inline by browsers
// without risk of script execution on our origin.
func inlineSafe(m string) bool {
	switch {
	case strings.HasPrefix(m, "video/"), strings.HasPrefix(m, "audio/"):
		return true
	case m == "image/jpeg", m == "image/png", m == "image/webp", m == "image/gif":
		return true
	}
	return false
}

type nullInt64 struct{ sql.NullInt64 }

func (n nullInt64) ptr() *int64 { return nullInt(n.NullInt64) }

type nullString struct{ sql.NullString }

func (n nullString) ptr() *string { return nullStr(n.NullString) }
