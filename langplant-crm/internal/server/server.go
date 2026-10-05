// Package server implements crm-server: the HTTP API, the web UI, the upload
// buffer on the VPS and the hub that talks to the storage node.
package server

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"langplant-crm/internal/config"
	"langplant-crm/internal/db"
	"langplant-crm/internal/webui"
)

// Version is set at build time via -ldflags.
var Version = "dev"

type Server struct {
	cfg    config.Server
	db     *sql.DB
	store  *BlobStore
	hub    *Hub
	events *EventBus
	limit  *loginLimiter

	locMu sync.RWMutex
	loc   *time.Location

	bg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

// New opens the database, seeds it on first start and prepares all components.
func New(cfg config.Server) (*Server, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}
	d, err := db.Open(filepath.Join(cfg.DataDir, "crm.db"))
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{cfg: cfg, db: d, events: newEventBus(), limit: newLoginLimiter(), ctx: ctx, cancel: cancel}
	if err := s.seed(); err != nil {
		d.Close()
		return nil, fmt.Errorf("seed: %w", err)
	}
	s.reloadLocation()
	s.store, err = newBlobStore(s, filepath.Join(cfg.DataDir))
	if err != nil {
		d.Close()
		return nil, err
	}
	s.hub = newHub(s)
	return s, nil
}

// DB exposes the database handle for CLI commands.
func (s *Server) DB() *sql.DB { return s.db }

// Start launches background jobs.
func (s *Server) Start() {
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		s.janitor()
	}()
}

// Close stops background jobs and closes the database.
func (s *Server) Close() {
	s.cancel()
	s.hub.closeAll()
	s.bg.Wait()
	s.db.Close()
}

func (s *Server) location() *time.Location {
	s.locMu.RLock()
	defer s.locMu.RUnlock()
	return s.loc
}

func (s *Server) reloadLocation() {
	name := s.setting("timezone")
	if name == "" {
		name = s.cfg.TZ
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		slog.Warn("unknown time zone, using UTC", "tz", name)
		loc = time.UTC
	}
	s.locMu.Lock()
	s.loc = loc
	s.locMu.Unlock()
}

// Handler builds the full HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	api := func(pattern string, fn handler) { mux.Handle(pattern, s.authed(fn, false)) }
	admin := func(pattern string, fn handler) { mux.Handle(pattern, s.authed(fn, true)) }

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "version": Version, "node": s.hub.online()})
	})
	mux.Handle("POST /api/auth/login", wrap(s.handleLogin))
	mux.Handle("POST /api/auth/logout", wrap(s.handleLogout))
	api("GET /api/auth/me", s.handleMe)

	api("GET /api/bootstrap", s.handleBootstrap)
	api("GET /api/events", s.handleEvents)
	api("GET /api/dashboard", s.handleDashboard)
	api("GET /api/calendar", s.handleCalendar)
	api("PUT /api/calendar/days/{date}", s.handlePutDay)
	api("DELETE /api/calendar/days/{date}", s.handleDeleteDay)

	api("GET /api/videos", s.handleListVideos)
	api("POST /api/videos", s.handleCreateVideo)
	api("GET /api/videos/{id}", s.handleGetVideo)
	api("PATCH /api/videos/{id}", s.handlePatchVideo)
	api("DELETE /api/videos/{id}", s.handleDeleteVideo)
	api("POST /api/videos/{id}/restore", s.handleRestoreVideo)
	api("PUT /api/videos/{id}/tags", s.handlePutVideoTags)
	api("PUT /api/videos/{id}/tracks", s.handlePutVideoTracks)
	api("POST /api/videos/{id}/variants", s.handleCreateVariant)
	api("GET /api/videos/{id}/zip", s.handleVideoZip)
	api("PATCH /api/variants/{id}", s.handlePatchVariant)
	api("DELETE /api/variants/{id}", s.handleDeleteVariant)
	api("PATCH /api/assets/{id}", s.handlePatchAsset)
	api("DELETE /api/assets/{id}", s.handleDeleteAsset)
	api("POST /api/assets/{id}/restore", s.handleRestoreAsset)
	api("POST /api/publications", s.handleCreatePublication)
	api("PATCH /api/publications/{id}", s.handlePatchPublication)
	api("DELETE /api/publications/{id}", s.handleDeletePublication)
	api("POST /api/videos/{id}/checklist", s.handleAddCheck)
	api("PATCH /api/checklist/{id}", s.handlePatchCheck)
	api("DELETE /api/checklist/{id}", s.handleDeleteCheck)
	api("GET /api/videos/{id}/comments", s.handleListComments)
	api("POST /api/videos/{id}/comments", s.handleAddComment)
	api("PATCH /api/comments/{id}", s.handlePatchComment)
	api("DELETE /api/comments/{id}", s.handleDeleteComment)
	api("GET /api/activity", s.handleActivity)

	api("GET /api/music", s.handleListTracks)
	api("GET /api/music/{id}", s.handleGetTrack)
	api("PATCH /api/music/{id}", s.handlePatchTrack)
	api("DELETE /api/music/{id}", s.handleDeleteTrack)
	api("POST /api/music/{id}/restore", s.handleRestoreTrack)
	api("POST /api/music/bulk", s.handleBulkTracks)
	api("PUT /api/music/{id}/favorite", s.handleFavoriteTrack)
	api("DELETE /api/music/{id}/favorite", s.handleFavoriteTrack)

	api("POST /api/uploads", s.handleCreateUpload)
	api("GET /api/uploads/{id}", s.handleGetUpload)
	api("PUT /api/uploads/{id}", s.handlePutUpload)
	api("DELETE /api/uploads/{id}", s.handleDeleteUpload)

	api("GET /api/files/{sha}", s.handleFile)
	api("GET /api/files/{sha}/{name}", s.handleDerivedFile)
	api("PUT /api/files/{sha}/client/{name}", s.handleClientDerived)
	api("PUT /api/files/{sha}/peaks", s.handleClientPeaks)

	api("POST /api/dict/{type}", s.handleDictCreate)
	api("PATCH /api/dict/{type}/{id}", s.handleDictPatch)
	api("DELETE /api/dict/{type}/{id}", s.handleDictDelete)
	api("POST /api/dict/{type}/reorder", s.handleDictReorder)
	admin("PATCH /api/settings", s.handlePatchSettings)

	admin("POST /api/users", s.handleCreateUser)
	api("PATCH /api/users/{id}", s.handlePatchUser)
	api("POST /api/users/{id}/password", s.handleUserPassword)

	api("GET /api/storage", s.handleStorage)
	admin("POST /api/storage/resync", s.handleResync)
	api("GET /api/trash", s.handleTrash)
	admin("DELETE /api/trash/{type}/{id}", s.handlePurge)

	mux.Handle("GET /api/node/ws", s.nodeAuth(s.hub.handleControl))
	mux.Handle("GET /api/node/blob/{sha}", s.nodeAuth(s.hub.handlePull))
	mux.Handle("GET /api/node/stream/{id}", s.nodeAuth(s.hub.handleStream))
	mux.Handle("GET /api/node/derived/{sha}/{name}", s.nodeAuth(s.hub.handleDerivedPush))
	mux.Handle("GET /api/node/backup", s.nodeAuth(s.hub.handleBackup))

	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "Нет такого метода API"})
	}))
	mux.Handle("/", s.spa())

	return s.middleware(mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		// CSRF: state-changing API calls must carry a custom header, which a
		// cross-site form or image cannot set without a CORS preflight.
		if strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/api/node/") {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
			default:
				if r.Header.Get("X-CRM") != "1" {
					writeJSON(w, http.StatusForbidden, map[string]string{"error": "csrf", "message": "Отсутствует заголовок X-CRM"})
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func init() {
	mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// spa serves the built web app with an index.html fallback for client routes.
func (s *Server) spa() http.Handler {
	var root fs.FS
	if s.cfg.WebDir != "" {
		root = os.DirFS(s.cfg.WebDir)
	} else {
		sub, err := fs.Sub(webui.Dist, "dist")
		if err != nil {
			panic(err)
		}
		root = sub
	}
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if st, err := fs.Stat(root, p); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(root, "index.html")
		if err != nil {
			http.Error(w, "web UI is not built: run `make web` (or use the Docker image)", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		w.Write(index)
	})
}

// janitor runs periodic maintenance: expired sessions, stale uploads, cache limits.
func (s *Server) janitor() {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, nowMs())
		s.store.cleanupStaleUploads(48 * time.Hour)
		s.store.enforceLimits(0)
		s.hub.resendPending()
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
	}
}
