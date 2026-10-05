package server

import (
	"database/sql"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// Day statuses of the publication plan (п.6):
//
//	off      before the plan start date
//	done     enough new unique videos had their first publication that day
//	planned  future day (or today) already covered by planned videos
//	today    today, not covered yet
//	empty    future day, not covered
//	missed   past day without enough publications
//	excused  past day without publications but with a valid reason (п.6.6–6.8)
type Day struct {
	Date    string    `json:"date"`
	Status  string    `json:"status"`
	Done    int       `json:"done"`
	Planned int       `json:"planned"`
	Excused bool      `json:"excused"`
	Reason  string    `json:"reason"`
	Note    string    `json:"note"`
	Items   []CalItem `json:"items"`
}

type CalItem struct {
	VideoID int64    `json:"video_id"`
	Code    string   `json:"code"`
	Title   string   `json:"title"`
	StageID *int64   `json:"stage_id"`
	Thumb   *string  `json:"thumb"`
	Unique  bool     `json:"unique"`
	Kind    string   `json:"kind"` // first | planned | pub
	Pubs    []CalPub `json:"pubs"`
}

type CalPub struct {
	ID        int64  `json:"id"`
	ChannelID int64  `json:"channel_id"`
	Lang      string `json:"lang"`
	Status    string `json:"status"`
	At        int64  `json:"at"`
}

type planState struct {
	today  string
	start  string
	quota  int
	days   map[string]*Day
	videos []*Video
}

func (s *Server) planState(from, to string) (*planState, error) {
	videos, err := s.loadVideos("v.deleted_at IS NULL", nil, false)
	if err != nil {
		return nil, err
	}
	loc := s.location()
	ps := &planState{
		today:  time.Now().In(loc).Format("2006-01-02"),
		start:  s.setting("quota_start"),
		days:   map[string]*Day{},
		videos: videos,
	}
	ps.quota, _ = strconv.Atoi(s.setting("quota_per_day"))
	if ps.quota < 1 {
		ps.quota = 1
	}
	day := func(d string) *Day {
		x := ps.days[d]
		if x == nil {
			x = &Day{Date: d, Items: []CalItem{}}
			ps.days[d] = x
		}
		return x
	}

	for _, v := range videos {
		firstDay := ""
		if v.FirstPublishedAt != nil {
			firstDay = s.dayOf(*v.FirstPublishedAt)
		}
		planDay := ""
		if v.FirstPublishedAt == nil {
			if v.NextPlanAt != nil {
				planDay = s.dayOf(*v.NextPlanAt)
			} else if v.PlanDate != nil {
				planDay = *v.PlanDate
			}
		}
		if v.IsUnique {
			if firstDay != "" {
				day(firstDay).Done++
			} else if planDay != "" {
				day(planDay).Planned++
			}
		}
		items := map[string]*CalItem{}
		item := func(d string) *CalItem {
			it := items[d]
			if it == nil {
				it = &CalItem{VideoID: v.ID, Code: v.Code, Title: v.Title, StageID: v.StageID, Thumb: v.Thumb, Unique: v.IsUnique, Kind: "pub", Pubs: []CalPub{}}
				switch d {
				case firstDay:
					it.Kind = "first"
				case planDay:
					it.Kind = "planned"
				}
				items[d] = it
			}
			return it
		}
		for _, vs := range v.Variants {
			for _, p := range vs.Pubs {
				var at int64
				switch {
				case p.Status == "published" && p.PublishedAt != nil:
					at = *p.PublishedAt
				case (p.Status == "planned" || p.Status == "scheduled") && p.PlanAt != nil:
					at = *p.PlanAt
				default:
					continue
				}
				d := s.dayOf(at)
				it := item(d)
				it.Pubs = append(it.Pubs, CalPub{ID: p.ID, ChannelID: p.ChannelID, Lang: vs.Lang, Status: p.Status, At: at})
			}
		}
		if planDay != "" {
			item(planDay)
		}
		for d, it := range items {
			if d < from || d > to {
				continue
			}
			sort.Slice(it.Pubs, func(i, j int) bool { return it.Pubs[i].At < it.Pubs[j].At })
			day(d).Items = append(day(d).Items, *it)
		}
	}

	if err := s.eachRow(`SELECT date, excused, reason, note FROM day_notes`, nil, func(r *sql.Rows) error {
		var d string
		var ex bool
		var reason, note string
		if err := r.Scan(&d, &ex, &reason, &note); err != nil {
			return err
		}
		x := day(d)
		x.Excused, x.Reason, x.Note = ex, reason, note
		return nil
	}); err != nil {
		return nil, err
	}
	return ps, nil
}

func (ps *planState) status(d string) string {
	x := ps.days[d]
	done, planned, excused := 0, 0, false
	if x != nil {
		done, planned, excused = x.Done, x.Planned, x.Excused
	}
	switch {
	case ps.start == "" || d < ps.start:
		return "off"
	case done >= ps.quota:
		return "done"
	case d > ps.today:
		if done+planned >= ps.quota {
			return "planned"
		}
		return "empty"
	case d == ps.today:
		if done+planned >= ps.quota {
			return "planned"
		}
		return "today"
	case excused:
		return "excused"
	default:
		return "missed"
	}
}

func (ps *planState) dayList(from, to string) []Day {
	var out []Day
	for d := from; d <= to; d = addDays(d, 1) {
		x := ps.days[d]
		if x == nil {
			x = &Day{Date: d, Items: []CalItem{}}
		}
		dd := *x
		dd.Status = ps.status(d)
		sort.Slice(dd.Items, func(i, j int) bool {
			rank := map[string]int{"first": 0, "planned": 1, "pub": 2}
			if rank[dd.Items[i].Kind] != rank[dd.Items[j].Kind] {
				return rank[dd.Items[i].Kind] < rank[dd.Items[j].Kind]
			}
			return dd.Items[i].Code < dd.Items[j].Code
		})
		out = append(out, dd)
	}
	return out
}

func addDays(d string, n int) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

func (s *Server) handleCalendar(w http.ResponseWriter, r *http.Request) error {
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	if !validDate(from) || !validDate(to) || to < from {
		return errBad("Укажите from и to в формате YYYY-MM-DD")
	}
	if addDays(from, 120) < to {
		return errBad("Слишком большой период")
	}
	ps, err := s.planState(from, to)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{
		"today": ps.today,
		"start": ps.start,
		"quota": ps.quota,
		"days":  ps.dayList(from, to),
	})
}

var dayReasons = map[string]bool{"": true, "illness": true, "platform": true, "blocked": true, "force": true, "vacation": true, "other": true}

func (s *Server) handlePutDay(w http.ResponseWriter, r *http.Request) error {
	date := r.PathValue("date")
	if !validDate(date) {
		return errBad("Некорректная дата")
	}
	var req struct {
		Excused bool   `json:"excused"`
		Reason  string `json:"reason"`
		Note    string `json:"note"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if !dayReasons[req.Reason] {
		return errBad("Неизвестная причина")
	}
	u := currentUser(r)
	if _, err := s.db.Exec(`INSERT INTO day_notes (date, excused, reason, note, user_id, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(date) DO UPDATE SET excused = excluded.excused, reason = excluded.reason, note = excluded.note, user_id = excluded.user_id, updated_at = excluded.updated_at`,
		date, boolInt(req.Excused), req.Reason, cleanText(req.Note, 2000), u.ID, nowMs()); err != nil {
		return err
	}
	if req.Excused {
		s.logActivity(u.ID, 0, "day.excused", map[string]any{"date": date, "reason": req.Reason})
	}
	s.events.Publish("calendar")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteDay(w http.ResponseWriter, r *http.Request) error {
	date := r.PathValue("date")
	if !validDate(date) {
		return errBad("Некорректная дата")
	}
	if _, err := s.db.Exec(`DELETE FROM day_notes WHERE date = ?`, date); err != nil {
		return err
	}
	s.events.Publish("calendar")
	return ok(w, map[string]bool{"ok": true})
}

// ---- dashboard ------------------------------------------------------------

type PlanStats struct {
	Today        string `json:"today"`
	TodayStatus  string `json:"today_status"`
	Quota        int    `json:"quota"`
	Start        string `json:"start"`
	Streak       int    `json:"streak"`        // consecutive days with publications (excused days do not break it)
	MissedRow    int    `json:"missed_row"`    // consecutive missed days up to yesterday
	MissedWindow int    `json:"missed_window"` // missed days in the last `window` days
	Window       int    `json:"window"`
	Reserve      int    `json:"reserve"`       // ready unique videos not yet published or planned
	CoveredUntil string `json:"covered_until"` // last day of the continuous planned run starting today
	Days         []Day  `json:"days"`
}

func (s *Server) planStats() (*PlanStats, *planState, error) {
	window, _ := strconv.Atoi(s.setting("window_days"))
	if window <= 0 {
		window = 30
	}
	loc := s.location()
	today := time.Now().In(loc).Format("2006-01-02")
	from := addDays(today, -window-400)
	to := addDays(today, 120)
	ps, err := s.planState(from, to)
	if err != nil {
		return nil, nil, err
	}
	st := &PlanStats{Today: today, TodayStatus: ps.status(today), Quota: ps.quota, Start: ps.start, Window: window}

	d := today
	if st.TodayStatus != "done" {
		d = addDays(today, -1)
	}
	for ; d >= from; d = addDays(d, -1) {
		x := ps.status(d)
		if x == "done" {
			st.Streak++
		} else if x != "excused" {
			break
		}
	}
	for d := addDays(today, -1); d >= from; d = addDays(d, -1) {
		if ps.status(d) != "missed" {
			break
		}
		st.MissedRow++
	}
	for d := addDays(today, -window); d < today; d = addDays(d, 1) {
		if ps.status(d) == "missed" {
			st.MissedWindow++
		}
	}
	for d := today; d <= to; d = addDays(d, 1) {
		x := ps.status(d)
		if x != "done" && x != "planned" {
			break
		}
		st.CoveredUntil = d
	}

	readyStages := map[int64]bool{}
	s.eachRow(`SELECT id FROM stages WHERE kind = 'ready'`, nil, func(r *sql.Rows) error {
		var id int64
		if err := r.Scan(&id); err != nil {
			return err
		}
		readyStages[id] = true
		return nil
	})
	for _, v := range ps.videos {
		if v.IsUnique && v.FirstPublishedAt == nil && v.NextPlanAt == nil && v.PlanDate == nil && v.StageID != nil && readyStages[*v.StageID] {
			st.Reserve++
		}
	}
	st.Days = ps.dayList(addDays(today, -13), addDays(today, 13))
	return st, ps, nil
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) error {
	st, ps, err := s.planStats()
	if err != nil {
		return err
	}
	archive := []*Video{}
	stages := map[string]int{}
	inWork := []*Video{}
	stageKind := map[int64]string{}
	s.eachRow(`SELECT id, kind FROM stages`, nil, func(r *sql.Rows) error {
		var id int64
		var k string
		if err := r.Scan(&id, &k); err != nil {
			return err
		}
		stageKind[id] = k
		return nil
	})
	for _, v := range ps.videos {
		if v.Archive.Published && !v.Archive.Complete {
			archive = append(archive, v)
		}
		key := "none"
		if v.StageID != nil {
			key = strconv.FormatInt(*v.StageID, 10)
			if k := stageKind[*v.StageID]; k == "work" || k == "ready" {
				inWork = append(inWork, v)
			}
		}
		stages[key]++
	}
	sort.Slice(archive, func(i, j int) bool {
		a, b := archive[i].Archive.DeadlineAt, archive[j].Archive.DeadlineAt
		if a == nil || b == nil {
			return b == nil && a != nil
		}
		return *a < *b
	})
	if len(archive) > 30 {
		archive = archive[:30]
	}
	sort.Slice(inWork, func(i, j int) bool { return inWork[i].UpdatedAt > inWork[j].UpdatedAt })
	if len(inWork) > 8 {
		inWork = inWork[:8]
	}
	activity, err := s.queryActivity("1=1", nil, 25)
	if err != nil {
		return err
	}
	storage, err := s.storageSummary()
	if err != nil {
		return err
	}
	return ok(w, map[string]any{
		"plan":     st,
		"archive":  archive,
		"stages":   stages,
		"in_work":  inWork,
		"activity": activity,
		"storage":  storage,
		"total":    len(ps.videos),
	})
}
