package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// EventBus fans out change notifications to connected browsers (SSE). The
// payload is a list of topics; clients refetch whatever depends on them.
type EventBus struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func newEventBus() *EventBus { return &EventBus{subs: map[chan []byte]struct{}{}} }

// Publish notifies all subscribers. Topics: videos, video:<id>, music, calendar,
// storage, dicts, activity, users.
func (b *EventBus) Publish(topics ...string) {
	msg, _ := json.Marshal(map[string]any{"topics": topics})
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- msg:
		default: // slow client: drop, it will catch up on the next refetch
		}
	}
}

func (b *EventBus) subscribe() chan []byte {
	ch := make(chan []byte, 32)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *EventBus) unsubscribe(ch chan []byte) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) error {
	fl, okf := w.(http.Flusher)
	if !okf {
		return errBad("streaming unsupported")
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "retry: 3000\n\n")
	fl.Flush()

	ch := s.events.subscribe()
	defer s.events.unsubscribe(ch)
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-s.ctx.Done():
			return nil
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			fl.Flush()
		case <-ping.C:
			fmt.Fprintf(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

// Activity is one entry of the activity feed.
type Activity struct {
	ID        int64           `json:"id"`
	UserID    *int64          `json:"user_id"`
	VideoID   *int64          `json:"video_id"`
	Action    string          `json:"action"`
	Data      json.RawMessage `json:"data"`
	CreatedAt int64           `json:"created_at"`
	VideoNum  *int64          `json:"video_num,omitempty"`
	VideoName *string         `json:"video_title,omitempty"`
}

// logActivity records an action; errors are not fatal for the request.
func (s *Server) logActivity(userID int64, videoID int64, action string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	b, _ := json.Marshal(data)
	var vid any
	if videoID > 0 {
		vid = videoID
	}
	var uid any
	if userID > 0 {
		uid = userID
	}
	s.db.Exec(`INSERT INTO activity (user_id, video_id, action, data, created_at) VALUES (?, ?, ?, ?, ?)`, uid, vid, action, string(b), nowMs())
	s.events.Publish("activity")
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	limit := 50
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	where := "1=1"
	args := []any{}
	if v, err := strconv.ParseInt(q.Get("video_id"), 10, 64); err == nil {
		where += " AND a.video_id = ?"
		args = append(args, v)
	}
	if v, err := strconv.ParseInt(q.Get("before"), 10, 64); err == nil {
		where += " AND a.id < ?"
		args = append(args, v)
	}
	list, err := s.queryActivity(where, args, limit)
	if err != nil {
		return err
	}
	return ok(w, list)
}

func (s *Server) queryActivity(where string, args []any, limit int) ([]Activity, error) {
	args = append(args, limit)
	rows, err := s.db.Query(`SELECT a.id, a.user_id, a.video_id, a.action, a.data, a.created_at, v.num, v.title
		FROM activity a LEFT JOIN videos v ON v.id = a.video_id
		WHERE `+where+` ORDER BY a.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		var uid, vid, num nullInt64
		var title nullString
		var data string
		if err := rows.Scan(&a.ID, &uid, &vid, &a.Action, &data, &a.CreatedAt, &num, &title); err != nil {
			return nil, err
		}
		a.UserID, a.VideoID, a.VideoNum, a.VideoName = uid.ptr(), vid.ptr(), num.ptr(), title.ptr()
		a.Data = json.RawMessage(data)
		out = append(out, a)
	}
	return out, rows.Err()
}
