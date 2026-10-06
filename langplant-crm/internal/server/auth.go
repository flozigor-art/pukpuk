package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie = "crm_session"
	sessionTTL    = 60 * 24 * time.Hour
)

type User struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Color     string `json:"color"`
	Disabled  bool   `json:"disabled"`
	CreatedAt int64  `json:"created_at"`
}

func (u *User) admin() bool { return u.Role == "admin" }

type ctxKey int

const userKey ctxKey = 1

func currentUser(r *http.Request) *User {
	u, _ := r.Context().Value(userKey).(*User)
	return u
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// HashPassword is exported for the CLI.
func HashPassword(p string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(p), 11)
	return string(b), err
}

func (s *Server) authed(fn handler, adminOnly bool) http.Handler {
	h := wrap(fn)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := s.sessionUser(w, r)
		if err != nil || u == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized", "message": "Нужно войти"})
			return
		}
		if adminOnly && !u.admin() {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden", "message": "Только для администратора"})
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), userKey, u))
		if undoTracked(r) {
			s.serveRecorded(h, w, r, u)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) sessionUser(w http.ResponseWriter, r *http.Request) (*User, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	th := hashToken(c.Value)
	var u User
	var exp, seen int64
	err = s.db.QueryRow(`SELECT u.id, u.login, u.name, u.role, u.color, u.disabled, u.created_at, s.expires_at, s.last_seen
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ?`, th).
		Scan(&u.ID, &u.Login, &u.Name, &u.Role, &u.Color, &u.Disabled, &u.CreatedAt, &exp, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	now := nowMs()
	if exp < now || u.Disabled {
		s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, th)
		return nil, nil
	}
	// Sliding expiration, refreshed at most once an hour.
	if now-seen > int64(time.Hour/time.Millisecond) {
		newExp := now + int64(sessionTTL/time.Millisecond)
		s.db.Exec(`UPDATE sessions SET last_seen = ?, expires_at = ? WHERE token_hash = ?`, now, newExp, th)
		s.setSessionCookie(w, c.Value, time.UnixMilli(newExp))
	}
	return &u, nil
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		Secure:   strings.HasPrefix(s.cfg.PublicURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	login := strings.ToLower(strings.TrimSpace(req.Login))
	keys := []string{"ip|" + clientIP(r), "login|" + login}
	if wait := max(s.limit.blocked(keys[0]), s.limit.blocked(keys[1])); wait > 0 {
		return &apiError{http.StatusTooManyRequests, "rate_limited", "Слишком много попыток. Подождите " + wait.Round(time.Second).String()}
	}
	var id int64
	var hash string
	var disabled bool
	err := s.db.QueryRow(`SELECT id, password_hash, disabled FROM users WHERE login = ?`, login).Scan(&id, &hash, &disabled)
	if err == nil && !disabled && bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) == nil {
		s.limit.reset(keys[1])
		token := randomID(32)
		now := nowMs()
		exp := now + int64(sessionTTL/time.Millisecond)
		ua := r.UserAgent()
		if len(ua) > 300 {
			ua = ua[:300]
		}
		if _, err := s.db.Exec(`INSERT INTO sessions (token_hash, user_id, created_at, expires_at, last_seen, user_agent) VALUES (?, ?, ?, ?, ?, ?)`,
			hashToken(token), id, now, exp, now, ua); err != nil {
			return err
		}
		s.setSessionCookie(w, token, time.UnixMilli(exp))
		u, err := s.userByID(id)
		if err != nil {
			return err
		}
		return ok(w, u)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	s.limit.fail(keys[0])
	s.limit.fail(keys[1])
	return &apiError{http.StatusUnauthorized, "bad_credentials", "Неверный логин или пароль"}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) error {
	return ok(w, currentUser(r))
}

func (s *Server) userByID(id int64) (*User, error) {
	var u User
	err := s.db.QueryRow(`SELECT id, login, name, role, color, disabled, created_at FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Login, &u.Name, &u.Role, &u.Color, &u.Disabled, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Server) listUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT id, login, name, role, color, disabled, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Login, &u.Name, &u.Role, &u.Color, &u.Disabled, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// nodeAuth checks the storage node's bearer token.
func (s *Server) nodeAuth(fn handler) http.Handler {
	h := wrap(fn)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if s.cfg.NodeToken == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(s.cfg.NodeToken)) != 1 {
			s.limit.fail("node|" + clientIP(r))
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized", "message": "bad node token"})
			return
		}
		h.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	// Behind a reverse proxy the real address arrives in X-Forwarded-For / X-Real-IP.
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.Index(v, ","); i >= 0 {
			v = v[:i]
		}
		return strings.TrimSpace(v)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// loginLimiter slows down password guessing: after 5 failures a key is
// blocked for a growing period.
type loginLimiter struct {
	mu   sync.Mutex
	hits map[string]*limitEntry
}

type limitEntry struct {
	fails int
	until time.Time
	last  time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{hits: map[string]*limitEntry{}} }

func (l *loginLimiter) blocked(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.hits[key]; ok && time.Now().Before(e.until) {
		return time.Until(e.until)
	}
	return 0
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, e := range l.hits {
		if now.Sub(e.last) > time.Hour {
			delete(l.hits, k)
		}
	}
	e := l.hits[key]
	if e == nil {
		e = &limitEntry{}
		l.hits[key] = e
	}
	e.fails++
	e.last = now
	if e.fails >= 5 {
		d := time.Duration(e.fails-4) * 30 * time.Second
		if d > 15*time.Minute {
			d = 15 * time.Minute
		}
		e.until = now.Add(d)
	}
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.hits, key)
	l.mu.Unlock()
}
