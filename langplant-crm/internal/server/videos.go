package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Video struct {
	ID         int64   `json:"id"`
	Num        int64   `json:"num"`
	Code       string  `json:"code"`
	Title      string  `json:"title"`
	StageID    *int64  `json:"stage_id"`
	AssigneeID *int64  `json:"assignee_id"`
	PlanDate   *string `json:"plan_date"`
	IsUnique   bool    `json:"is_unique"`
	OriginalID *int64  `json:"original_id"`
	CreatedBy  *int64  `json:"created_by"`
	CreatedAt  int64   `json:"created_at"`
	UpdatedAt  int64   `json:"updated_at"`
	DeletedAt  *int64  `json:"deleted_at,omitempty"`

	Script    *string `json:"script,omitempty"`
	Notes     *string `json:"notes,omitempty"`
	MusicNote *string `json:"music_note,omitempty"`

	Tags     []int64           `json:"tags"`
	Thumb    *string           `json:"thumb"`
	Variants []*VariantSummary `json:"variants"`

	FirstPublishedAt *int64  `json:"first_published_at"`
	NextPlanAt       *int64  `json:"next_plan_at"`
	Archive          Archive `json:"archive"`
	AssetCount       int     `json:"asset_count"`
	CommentCount     int     `json:"comment_count"`
	CheckDone        int     `json:"check_done"`
	CheckTotal       int     `json:"check_total"`

	firstPubVariant map[int64]int64
}

type Archive struct {
	Published  bool   `json:"published"`
	Complete   bool   `json:"complete"`
	Missing    int    `json:"missing"`     // total number of missing mandatory files across published versions
	DeadlineAt *int64 `json:"deadline_at"` // earliest deadline among incomplete published versions
}

type VariantSummary struct {
	ID               int64        `json:"id"`
	Lang             string       `json:"lang"`
	Title            string       `json:"title"`
	Caption          *string      `json:"caption,omitempty"`
	Voice            string       `json:"voice"`
	Status           string       `json:"status"`
	FirstPublishedAt *int64       `json:"first_published_at"`
	Missing          []string     `json:"missing"`
	DeadlineAt       *int64       `json:"deadline_at"`
	Pubs             []PubSummary `json:"pubs"`
}

type PubSummary struct {
	ID          int64  `json:"id"`
	ChannelID   int64  `json:"channel_id"`
	Status      string `json:"status"`
	PlanAt      *int64 `json:"plan_at"`
	PublishedAt *int64 `json:"published_at"`
	URL         string `json:"url"`
}

func (s *Server) videoCode(num int64) string {
	return fmt.Sprintf("%s-%04d", s.setting("code_prefix"), num)
}

// loadVideos loads videos matching where (on table alias v) with all summary data.
func (s *Server) loadVideos(where string, args []any, detail bool) ([]*Video, error) {
	cols := `v.id, v.num, v.title, v.stage_id, v.assignee_id, v.plan_date, v.is_unique, v.original_id, v.created_by, v.created_at, v.updated_at, v.deleted_at, v.script, v.notes, v.music_note`
	rows, err := s.db.Query(`SELECT `+cols+` FROM videos v WHERE `+where+` ORDER BY v.num DESC`, args...)
	if err != nil {
		return nil, err
	}
	prefix := s.setting("code_prefix")
	var list []*Video
	byID := map[int64]*Video{}
	for rows.Next() {
		v := &Video{Tags: []int64{}, Variants: []*VariantSummary{}, firstPubVariant: map[int64]int64{}}
		var stage, assignee, orig, createdBy, deleted nullInt64
		var plan nullString
		var script, notes, musicNote string
		if err := rows.Scan(&v.ID, &v.Num, &v.Title, &stage, &assignee, &plan, &v.IsUnique, &orig, &createdBy, &v.CreatedAt, &v.UpdatedAt, &deleted, &script, &notes, &musicNote); err != nil {
			rows.Close()
			return nil, err
		}
		v.Code = fmt.Sprintf("%s-%04d", prefix, v.Num)
		v.StageID, v.AssigneeID, v.OriginalID, v.CreatedBy, v.DeletedAt = stage.ptr(), assignee.ptr(), orig.ptr(), createdBy.ptr(), deleted.ptr()
		v.PlanDate = plan.ptr()
		if detail {
			v.Script, v.Notes, v.MusicNote = &script, &notes, &musicNote
		}
		list = append(list, v)
		byID[v.ID] = v
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return []*Video{}, nil
	}
	sub := `SELECT v.id FROM videos v WHERE ` + where

	// tags
	if err := s.eachRow(`SELECT video_id, tag_id FROM video_tags WHERE video_id IN (`+sub+`)`, args, func(r *sql.Rows) error {
		var vid, tid int64
		if err := r.Scan(&vid, &tid); err != nil {
			return err
		}
		if v := byID[vid]; v != nil {
			v.Tags = append(v.Tags, tid)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// variants
	primary := s.primaryLanguage()
	langSort := s.languageOrder()
	variants := map[int64]*VariantSummary{}
	variantVideo := map[int64]int64{}
	if err := s.eachRow(`SELECT id, video_id, language_code, title, caption, voice, status FROM variants WHERE video_id IN (`+sub+`)`, args, func(r *sql.Rows) error {
		vs := &VariantSummary{Missing: []string{}, Pubs: []PubSummary{}}
		var vid int64
		var caption string
		if err := r.Scan(&vs.ID, &vid, &vs.Lang, &vs.Title, &caption, &vs.Voice, &vs.Status); err != nil {
			return err
		}
		if detail {
			vs.Caption = &caption
		}
		if v := byID[vid]; v != nil {
			v.Variants = append(v.Variants, vs)
			variants[vs.ID] = vs
			variantVideo[vs.ID] = vid
		}
		return nil
	}); err != nil {
		return nil, err
	}
	for _, v := range list {
		sort.SliceStable(v.Variants, func(i, j int) bool { return langSort[v.Variants[i].Lang] < langSort[v.Variants[j].Lang] })
	}

	// assets: kinds present per variant and shared, thumbnail candidates
	thumbs := map[string]bool{}
	if err := s.eachRow(`SELECT sha256 FROM derived_files WHERE name = 'thumb.jpg'`, nil, func(r *sql.Rows) error {
		var sha string
		if err := r.Scan(&sha); err != nil {
			return err
		}
		thumbs[sha] = true
		return nil
	}); err != nil {
		return nil, err
	}
	type kindSet map[string]bool
	variantKinds := map[int64]kindSet{}
	sharedKinds := map[int64]kindSet{}
	thumbRank := map[int64]int{}
	if err := s.eachRow(`SELECT video_id, variant_id, kind, sha256 FROM assets WHERE deleted_at IS NULL AND video_id IN (`+sub+`) ORDER BY id`, args, func(r *sql.Rows) error {
		var vid int64
		var varID nullInt64
		var kind, sha string
		if err := r.Scan(&vid, &varID, &kind, &sha); err != nil {
			return err
		}
		v := byID[vid]
		if v == nil {
			return nil
		}
		v.AssetCount++
		if varID.Valid {
			if variantKinds[varID.Int64] == nil {
				variantKinds[varID.Int64] = kindSet{}
			}
			variantKinds[varID.Int64][kind] = true
		} else {
			if sharedKinds[vid] == nil {
				sharedKinds[vid] = kindSet{}
			}
			sharedKinds[vid][kind] = true
		}
		if thumbs[sha] {
			// prefer: cover of the primary language, final of the primary language, any cover, any final, anything
			isPrimary := varID.Valid && variants[varID.Int64] != nil && variants[varID.Int64].Lang == primary
			rank := 1
			switch {
			case kind == "cover" && isPrimary:
				rank = 5
			case kind == "final" && isPrimary:
				rank = 4
			case kind == "cover":
				rank = 3
			case kind == "final":
				rank = 2
			}
			if rank >= thumbRank[vid] {
				thumbRank[vid] = rank
				sh := sha
				v.Thumb = &sh
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// publications
	quotaPlatforms := s.quotaChannels()
	if err := s.eachRow(`SELECT id, video_id, variant_id, channel_id, status, plan_at, published_at, url FROM publications WHERE video_id IN (`+sub+`)`, args, func(r *sql.Rows) error {
		var p PubSummary
		var vid, varID int64
		var plan, pub nullInt64
		if err := r.Scan(&p.ID, &vid, &varID, &p.ChannelID, &p.Status, &plan, &pub, &p.URL); err != nil {
			return err
		}
		p.PlanAt, p.PublishedAt = plan.ptr(), pub.ptr()
		v := byID[vid]
		vs := variants[varID]
		if v == nil || vs == nil {
			return nil
		}
		vs.Pubs = append(vs.Pubs, p)
		if p.Status == "published" && pub.Valid {
			if vs.FirstPublishedAt == nil || pub.Int64 < *vs.FirstPublishedAt {
				t := pub.Int64
				vs.FirstPublishedAt = &t
			}
			if quotaPlatforms[p.ChannelID] && (v.FirstPublishedAt == nil || pub.Int64 < *v.FirstPublishedAt) {
				t := pub.Int64
				v.FirstPublishedAt = &t
			}
		}
		if (p.Status == "planned" || p.Status == "scheduled") && plan.Valid {
			if v.NextPlanAt == nil || plan.Int64 < *v.NextPlanAt {
				t := plan.Int64
				v.NextPlanAt = &t
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// comments and checklist counters
	if err := s.eachRow(`SELECT video_id, COUNT(*) FROM comments WHERE deleted_at IS NULL AND video_id IN (`+sub+`) GROUP BY video_id`, args, func(r *sql.Rows) error {
		var vid int64
		var n int
		if err := r.Scan(&vid, &n); err != nil {
			return err
		}
		if v := byID[vid]; v != nil {
			v.CommentCount = n
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := s.eachRow(`SELECT video_id, COUNT(*), COUNT(done_at) FROM checklist WHERE video_id IN (`+sub+`) GROUP BY video_id`, args, func(r *sql.Rows) error {
		var vid int64
		var n, d int
		if err := r.Scan(&vid, &n, &d); err != nil {
			return err
		}
		if v := byID[vid]; v != nil {
			v.CheckTotal, v.CheckDone = n, d
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// archive completeness (п.7.2, 7.6)
	required, err := s.requiredKinds()
	if err != nil {
		return nil, err
	}
	hours, _ := strconv.Atoi(s.setting("archive_hours"))
	if hours <= 0 {
		hours = 48
	}
	for _, v := range list {
		v.Archive.Complete = true
		for _, vs := range v.Variants {
			for _, k := range required {
				if !variantKinds[vs.ID][k] && !sharedKinds[v.ID][k] {
					vs.Missing = append(vs.Missing, k)
				}
			}
			if vs.FirstPublishedAt != nil {
				v.Archive.Published = true
				d := *vs.FirstPublishedAt + int64(hours)*3600*1000
				vs.DeadlineAt = &d
				if len(vs.Missing) > 0 {
					v.Archive.Complete = false
					v.Archive.Missing += len(vs.Missing)
					if v.Archive.DeadlineAt == nil || d < *v.Archive.DeadlineAt {
						dd := d
						v.Archive.DeadlineAt = &dd
					}
				}
			}
		}
		if !v.Archive.Published {
			v.Archive.Complete = false
		}
	}
	return list, nil
}

func (s *Server) eachRow(q string, args []any, fn func(*sql.Rows) error) error {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Server) primaryLanguage() string {
	var code string
	if err := s.db.QueryRow(`SELECT code FROM languages WHERE is_primary = 1 ORDER BY sort LIMIT 1`).Scan(&code); err != nil {
		s.db.QueryRow(`SELECT code FROM languages ORDER BY sort LIMIT 1`).Scan(&code)
	}
	return code
}

func (s *Server) languageOrder() map[string]int {
	out := map[string]int{}
	s.eachRow(`SELECT code, sort, is_primary FROM languages`, nil, func(r *sql.Rows) error {
		var code string
		var sortN int
		var primary bool
		if err := r.Scan(&code, &sortN, &primary); err != nil {
			return err
		}
		if primary {
			sortN = -1 << 20
		}
		out[code] = sortN
		return nil
	})
	return out
}

// quotaChannels returns channels whose platform counts for the daily plan.
func (s *Server) quotaChannels() map[int64]bool {
	out := map[int64]bool{}
	s.eachRow(`SELECT c.id FROM channels c JOIN platforms p ON p.id = c.platform_id WHERE p.counts_for_quota = 1`, nil, func(r *sql.Rows) error {
		var id int64
		if err := r.Scan(&id); err != nil {
			return err
		}
		out[id] = true
		return nil
	})
	return out
}

func (s *Server) requiredKinds() ([]string, error) {
	var out []string
	err := s.eachRow(`SELECT key FROM asset_kinds WHERE required = 1 AND archived = 0 ORDER BY sort`, nil, func(r *sql.Rows) error {
		var k string
		if err := r.Scan(&k); err != nil {
			return err
		}
		out = append(out, k)
		return nil
	})
	return out, err
}

func (s *Server) handleListVideos(w http.ResponseWriter, r *http.Request) error {
	where := "v.deleted_at IS NULL"
	if r.URL.Query().Get("deleted") == "1" {
		where = "v.deleted_at IS NOT NULL"
	}
	list, err := s.loadVideos(where, nil, false)
	if err != nil {
		return err
	}
	return ok(w, list)
}

func (s *Server) getVideo(id int64, detail bool) (*Video, error) {
	list, err := s.loadVideos("v.id = ?", []any{id}, detail)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, errNotFound("Ролик")
	}
	return list[0], nil
}

type Asset struct {
	ID         int64     `json:"id"`
	VideoID    int64     `json:"video_id"`
	VariantID  *int64    `json:"variant_id"`
	Kind       string    `json:"kind"`
	Filename   string    `json:"filename"`
	Sha        string    `json:"sha256"`
	Size       int64     `json:"size"`
	Mime       string    `json:"mime"`
	Version    int       `json:"version"`
	Note       string    `json:"note"`
	UploadedBy *int64    `json:"uploaded_by"`
	CreatedAt  int64     `json:"created_at"`
	DeletedAt  *int64    `json:"deleted_at,omitempty"`
	Blob       *BlobInfo `json:"blob"`
}

type Publication struct {
	ID          int64  `json:"id"`
	VideoID     int64  `json:"video_id"`
	VariantID   int64  `json:"variant_id"`
	ChannelID   int64  `json:"channel_id"`
	Status      string `json:"status"`
	PlanAt      *int64 `json:"plan_at"`
	PublishedAt *int64 `json:"published_at"`
	URL         string `json:"url"`
	Note        string `json:"note"`
	Views       *int64 `json:"views"`
	CreatedBy   *int64 `json:"created_by"`
	UpdatedAt   int64  `json:"updated_at"`
}

type CheckItem struct {
	ID     int64  `json:"id"`
	Label  string `json:"label"`
	DoneBy *int64 `json:"done_by"`
	DoneAt *int64 `json:"done_at"`
	Sort   int    `json:"sort"`
}

type VideoDetail struct {
	*Video
	Assets       []Asset       `json:"assets"`
	Publications []Publication `json:"publications"`
	Checklist    []CheckItem   `json:"checklist"`
	Tracks       []int64       `json:"tracks"`
}

func (s *Server) handleGetVideo(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	v, err := s.getVideo(id, true)
	if err != nil {
		return err
	}
	d := VideoDetail{Video: v, Assets: []Asset{}, Publications: []Publication{}, Checklist: []CheckItem{}, Tracks: []int64{}}
	if d.Assets, err = s.queryAssets(`a.video_id = ? AND a.deleted_at IS NULL`, id); err != nil {
		return err
	}
	if err := s.eachRow(`SELECT id, video_id, variant_id, channel_id, status, plan_at, published_at, url, note, views, created_by, updated_at FROM publications WHERE video_id = ? ORDER BY id`, []any{id}, func(r *sql.Rows) error {
		var p Publication
		var plan, pub, views, by nullInt64
		if err := r.Scan(&p.ID, &p.VideoID, &p.VariantID, &p.ChannelID, &p.Status, &plan, &pub, &p.URL, &p.Note, &views, &by, &p.UpdatedAt); err != nil {
			return err
		}
		p.PlanAt, p.PublishedAt, p.Views, p.CreatedBy = plan.ptr(), pub.ptr(), views.ptr(), by.ptr()
		d.Publications = append(d.Publications, p)
		return nil
	}); err != nil {
		return err
	}
	if err := s.eachRow(`SELECT id, label, done_by, done_at, sort FROM checklist WHERE video_id = ? ORDER BY sort, id`, []any{id}, func(r *sql.Rows) error {
		var c CheckItem
		var by, at nullInt64
		if err := r.Scan(&c.ID, &c.Label, &by, &at, &c.Sort); err != nil {
			return err
		}
		c.DoneBy, c.DoneAt = by.ptr(), at.ptr()
		d.Checklist = append(d.Checklist, c)
		return nil
	}); err != nil {
		return err
	}
	if err := s.eachRow(`SELECT vt.track_id FROM video_tracks vt JOIN tracks t ON t.id = vt.track_id WHERE vt.video_id = ? AND t.deleted_at IS NULL`, []any{id}, func(r *sql.Rows) error {
		var tid int64
		if err := r.Scan(&tid); err != nil {
			return err
		}
		d.Tracks = append(d.Tracks, tid)
		return nil
	}); err != nil {
		return err
	}
	return ok(w, d)
}

func (s *Server) queryAssets(where string, args ...any) ([]Asset, error) {
	out := []Asset{}
	var shas []string
	err := s.eachRow(`SELECT a.id, a.video_id, a.variant_id, a.kind, a.filename, a.sha256, a.size, a.mime, a.version, a.note, a.uploaded_by, a.created_at, a.deleted_at
		FROM assets a WHERE `+where+` ORDER BY a.kind, a.version DESC, a.id DESC`, args, func(r *sql.Rows) error {
		var a Asset
		var variant, by, del nullInt64
		if err := r.Scan(&a.ID, &a.VideoID, &variant, &a.Kind, &a.Filename, &a.Sha, &a.Size, &a.Mime, &a.Version, &a.Note, &by, &a.CreatedAt, &del); err != nil {
			return err
		}
		a.VariantID, a.UploadedBy, a.DeletedAt = variant.ptr(), by.ptr(), del.ptr()
		out = append(out, a)
		shas = append(shas, a.Sha)
		return nil
	})
	if err != nil {
		return nil, err
	}
	blobs, err := s.blobInfos(shas)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Blob = blobs[out[i].Sha]
	}
	return out, nil
}

func (s *Server) handleCreateVideo(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Title      string  `json:"title"`
		StageID    *int64  `json:"stage_id"`
		AssigneeID *int64  `json:"assignee_id"`
		PlanDate   *string `json:"plan_date"`
		Tags       []int64 `json:"tags"`
		Script     string  `json:"script"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	title := cleanText(req.Title, 300)
	if title == "" {
		return errBad("Укажите название")
	}
	if req.PlanDate != nil && *req.PlanDate != "" && !validDate(*req.PlanDate) {
		return errBad("Некорректная дата")
	}
	if req.PlanDate != nil && *req.PlanDate == "" {
		req.PlanDate = nil
	}
	u := currentUser(r)
	now := nowMs()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stage := req.StageID
	if stage == nil {
		var sid int64
		if err := tx.QueryRow(`SELECT id FROM stages WHERE archived = 0 ORDER BY sort, id LIMIT 1`).Scan(&sid); err == nil {
			stage = &sid
		}
	}
	var num int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(num), 0) + 1 FROM videos`).Scan(&num); err != nil {
		return err
	}
	res, err := tx.Exec(`INSERT INTO videos (num, title, stage_id, assignee_id, plan_date, script, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		num, title, stage, req.AssigneeID, req.PlanDate, cleanText(req.Script, 100000), u.ID, now, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	if _, err := tx.Exec(`INSERT INTO variants (video_id, language_code, status, created_at)
		SELECT ?, code, 'wip', ? FROM languages WHERE is_primary = 1 ORDER BY sort LIMIT 1`, id, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO checklist (video_id, label, sort) SELECT ?, label, sort FROM checklist_template ORDER BY sort, id`, id); err != nil {
		return err
	}
	for _, t := range req.Tags {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO video_tags (video_id, tag_id) SELECT ?, id FROM tags WHERE id = ? AND scope = 'video'`, id, t); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.logActivity(u.ID, id, "video.created", map[string]any{"title": title})
	s.events.Publish("videos", "calendar")
	v, err := s.getVideo(id, false)
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) handlePatchVideo(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req map[string]json.RawMessage
	if err := readJSON(r, &req); err != nil {
		return err
	}
	u := currentUser(r)
	var oldStage nullInt64
	var oldPlan nullString
	var oldTitle string
	if err := s.db.QueryRow(`SELECT stage_id, plan_date, title FROM videos WHERE id = ?`, id).Scan(&oldStage, &oldPlan, &oldTitle); err != nil {
		return err
	}
	p := newPatch()
	for key, raw := range req {
		switch key {
		case "title":
			v, err := p.str(raw, 300)
			if err != nil {
				return err
			}
			if v == "" {
				return errBad("Название не может быть пустым")
			}
			p.set("title", v)
		case "script", "notes", "music_note":
			v, err := p.str(raw, 100000)
			if err != nil {
				return err
			}
			p.set(key, v)
		case "stage_id", "assignee_id", "original_id":
			v, err := p.optInt(raw)
			if err != nil {
				return err
			}
			if key == "original_id" && v != nil && *v == id {
				return errBad("Ролик не может ссылаться сам на себя")
			}
			p.set(key, v)
		case "plan_date":
			v, err := p.optDate(raw)
			if err != nil {
				return err
			}
			p.set(key, v)
		case "is_unique":
			var b bool
			if err := json.Unmarshal(raw, &b); err != nil {
				return errBad("is_unique: ожидается true/false")
			}
			p.set(key, boolInt(b))
		default:
			return errBad("Поле %s нельзя изменить", key)
		}
	}
	if p.empty() {
		return ok(w, map[string]bool{"ok": true})
	}
	p.set("updated_at", nowMs())
	if err := p.exec(s.db, "videos", "id", id); err != nil {
		return err
	}
	if raw, okk := req["stage_id"]; okk {
		var ns *int64
		json.Unmarshal(raw, &ns)
		if ns != nil && (!oldStage.Valid || oldStage.Int64 != *ns) {
			s.logActivity(u.ID, id, "video.stage", map[string]any{"from": oldStage.ptr(), "to": *ns})
		}
	}
	if raw, okk := req["plan_date"]; okk {
		var nd *string
		json.Unmarshal(raw, &nd)
		if nd != nil && *nd == "" {
			nd = nil
		}
		if (nd == nil) != !oldPlan.Valid || (nd != nil && *nd != oldPlan.String) {
			s.logActivity(u.ID, id, "video.plan", map[string]any{"from": oldPlan.ptr(), "to": nd})
		}
	}
	if raw, okk := req["title"]; okk {
		var nt string
		json.Unmarshal(raw, &nt)
		if cleanText(nt, 300) != oldTitle {
			s.logActivity(u.ID, id, "video.renamed", map[string]any{"from": oldTitle, "to": cleanText(nt, 300)})
		}
	}
	s.events.Publish("videos", "video:"+strconv.FormatInt(id, 10), "calendar")
	v, err := s.getVideo(id, true)
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) handleDeleteVideo(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE videos SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`, nowMs(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound("Ролик")
	}
	s.logActivity(currentUser(r).ID, id, "video.deleted", nil)
	s.events.Publish("videos", "video:"+strconv.FormatInt(id, 10), "calendar", "trash")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleRestoreVideo(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE videos SET deleted_at = NULL WHERE id = ?`, id); err != nil {
		return err
	}
	s.logActivity(currentUser(r).ID, id, "video.restored", nil)
	s.events.Publish("videos", "video:"+strconv.FormatInt(id, 10), "calendar", "trash")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handlePutVideoTags(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Tags []int64 `json:"tags"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM video_tags WHERE video_id = ?`, id); err != nil {
		return err
	}
	for _, t := range req.Tags {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO video_tags (video_id, tag_id) SELECT ?, id FROM tags WHERE id = ? AND scope = 'video'`, id, t); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE videos SET updated_at = ? WHERE id = ?`, nowMs(), id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.events.Publish("videos", "video:"+strconv.FormatInt(id, 10))
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handlePutVideoTracks(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Tracks []int64 `json:"tracks"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM video_tracks WHERE video_id = ?`, id); err != nil {
		return err
	}
	for _, t := range req.Tracks {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO video_tracks (video_id, track_id) SELECT ?, id FROM tracks WHERE id = ?`, id, t); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.events.Publish("video:"+strconv.FormatInt(id, 10), "music")
	return ok(w, map[string]bool{"ok": true})
}

// ---- variants -------------------------------------------------------------

func (s *Server) handleCreateVariant(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Lang string `json:"lang"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM languages WHERE code = ?`, req.Lang).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return errBad("Неизвестный язык")
	}
	res, err := s.db.Exec(`INSERT INTO variants (video_id, language_code, voice, created_at) VALUES (?, ?, 'dub', ?)`, id, req.Lang, nowMs())
	if err != nil {
		if isConstraint(err) {
			return errConflict("Эта языковая версия уже есть")
		}
		return err
	}
	vid, _ := res.LastInsertId()
	s.logActivity(currentUser(r).ID, id, "variant.created", map[string]any{"lang": req.Lang})
	s.events.Publish("videos", "video:"+strconv.FormatInt(id, 10))
	return ok(w, map[string]int64{"id": vid})
}

func (s *Server) videoOfVariant(variantID int64) (int64, error) {
	var vid int64
	err := s.db.QueryRow(`SELECT video_id FROM variants WHERE id = ?`, variantID).Scan(&vid)
	if err == sql.ErrNoRows {
		return 0, errNotFound("Языковая версия")
	}
	return vid, err
}

func (s *Server) handlePatchVariant(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	vid, err := s.videoOfVariant(id)
	if err != nil {
		return err
	}
	var req map[string]json.RawMessage
	if err := readJSON(r, &req); err != nil {
		return err
	}
	p := newPatch()
	for key, raw := range req {
		switch key {
		case "title":
			v, err := p.str(raw, 300)
			if err != nil {
				return err
			}
			p.set(key, v)
		case "caption":
			v, err := p.str(raw, 10000)
			if err != nil {
				return err
			}
			p.set(key, v)
		case "voice":
			v, err := p.str(raw, 40)
			if err != nil {
				return err
			}
			p.set(key, v)
		case "status":
			v, err := p.str(raw, 10)
			if err != nil {
				return err
			}
			if v != "todo" && v != "wip" && v != "ready" {
				return errBad("Неизвестный статус")
			}
			p.set(key, v)
		default:
			return errBad("Поле %s нельзя изменить", key)
		}
	}
	if err := p.exec(s.db, "variants", "id", id); err != nil {
		return err
	}
	s.db.Exec(`UPDATE videos SET updated_at = ? WHERE id = ?`, nowMs(), vid)
	s.events.Publish("videos", "video:"+strconv.FormatInt(vid, 10))
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteVariant(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	vid, err := s.videoOfVariant(id)
	if err != nil {
		return err
	}
	var assets, pubs int
	s.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE variant_id = ?`, id).Scan(&assets)
	s.db.QueryRow(`SELECT COUNT(*) FROM publications WHERE variant_id = ?`, id).Scan(&pubs)
	if assets > 0 || pubs > 0 {
		return errConflict("В этой языковой версии есть файлы или публикации (в т.ч. в корзине) — сначала перенесите или удалите их")
	}
	var lang string
	s.db.QueryRow(`SELECT language_code FROM variants WHERE id = ?`, id).Scan(&lang)
	if _, err := s.db.Exec(`DELETE FROM variants WHERE id = ?`, id); err != nil {
		return err
	}
	s.logActivity(currentUser(r).ID, vid, "variant.deleted", map[string]any{"lang": lang})
	s.events.Publish("videos", "video:"+strconv.FormatInt(vid, 10))
	return ok(w, map[string]bool{"ok": true})
}

// ---- assets ---------------------------------------------------------------

func (s *Server) handlePatchAsset(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var vid int64
	if err := s.db.QueryRow(`SELECT video_id FROM assets WHERE id = ?`, id).Scan(&vid); err != nil {
		return err
	}
	var req map[string]json.RawMessage
	if err := readJSON(r, &req); err != nil {
		return err
	}
	p := newPatch()
	for key, raw := range req {
		switch key {
		case "kind":
			v, err := p.str(raw, 60)
			if err != nil {
				return err
			}
			var n int
			s.db.QueryRow(`SELECT COUNT(*) FROM asset_kinds WHERE key = ?`, v).Scan(&n)
			if n == 0 {
				return errBad("Неизвестный тип файла")
			}
			p.set(key, v)
		case "variant_id":
			v, err := p.optInt(raw)
			if err != nil {
				return err
			}
			if v != nil {
				owner, err := s.videoOfVariant(*v)
				if err != nil {
					return err
				}
				if owner != vid {
					return errBad("Языковая версия принадлежит другому ролику")
				}
			}
			p.set(key, v)
		case "note":
			v, err := p.str(raw, 2000)
			if err != nil {
				return err
			}
			p.set(key, v)
		case "filename":
			v, err := p.str(raw, 250)
			if err != nil {
				return err
			}
			p.set(key, safeFilename(v))
		default:
			return errBad("Поле %s нельзя изменить", key)
		}
	}
	if err := p.exec(s.db, "assets", "id", id); err != nil {
		return err
	}
	s.events.Publish("videos", "video:"+strconv.FormatInt(vid, 10))
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteAsset(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var vid int64
	var name, kind string
	if err := s.db.QueryRow(`SELECT video_id, filename, kind FROM assets WHERE id = ?`, id).Scan(&vid, &name, &kind); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE assets SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`, nowMs(), id); err != nil {
		return err
	}
	s.logActivity(currentUser(r).ID, vid, "asset.deleted", map[string]any{"filename": name, "kind": kind})
	s.events.Publish("videos", "video:"+strconv.FormatInt(vid, 10), "trash")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleRestoreAsset(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var vid int64
	if err := s.db.QueryRow(`SELECT video_id FROM assets WHERE id = ?`, id).Scan(&vid); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE assets SET deleted_at = NULL WHERE id = ?`, id); err != nil {
		return err
	}
	s.events.Publish("videos", "video:"+strconv.FormatInt(vid, 10), "trash")
	return ok(w, map[string]bool{"ok": true})
}

// ---- publications ---------------------------------------------------------

var pubStatuses = map[string]bool{"planned": true, "scheduled": true, "published": true, "skipped": true, "removed": true}

func (s *Server) handleCreatePublication(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		VariantID   int64  `json:"variant_id"`
		ChannelID   int64  `json:"channel_id"`
		Status      string `json:"status"`
		PlanAt      *int64 `json:"plan_at"`
		PublishedAt *int64 `json:"published_at"`
		URL         string `json:"url"`
		Note        string `json:"note"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	vid, err := s.videoOfVariant(req.VariantID)
	if err != nil {
		return err
	}
	if !pubStatuses[req.Status] {
		return errBad("Неизвестный статус публикации")
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM channels WHERE id = ?`, req.ChannelID).Scan(&n)
	if n == 0 {
		return errBad("Неизвестный аккаунт")
	}
	if req.Status == "published" && req.PublishedAt == nil {
		t := nowMs()
		req.PublishedAt = &t
	}
	u := currentUser(r)
	now := nowMs()
	res, err := s.db.Exec(`INSERT INTO publications (video_id, variant_id, channel_id, status, plan_at, published_at, url, note, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, vid, req.VariantID, req.ChannelID, req.Status, req.PlanAt, req.PublishedAt,
		cleanText(req.URL, 1000), cleanText(req.Note, 2000), u.ID, now, now)
	if err != nil {
		if isConstraint(err) {
			return errConflict("Для этого аккаунта публикация уже есть")
		}
		return err
	}
	pid, _ := res.LastInsertId()
	s.afterPublicationChange(u.ID, vid, pid, "", req.Status)
	return ok(w, map[string]int64{"id": pid})
}

func (s *Server) handlePatchPublication(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var vid int64
	var oldStatus string
	var oldPub nullInt64
	if err := s.db.QueryRow(`SELECT video_id, status, published_at FROM publications WHERE id = ?`, id).Scan(&vid, &oldStatus, &oldPub); err != nil {
		return err
	}
	var req map[string]json.RawMessage
	if err := readJSON(r, &req); err != nil {
		return err
	}
	p := newPatch()
	newStatus := oldStatus
	for key, raw := range req {
		switch key {
		case "status":
			v, err := p.str(raw, 20)
			if err != nil {
				return err
			}
			if !pubStatuses[v] {
				return errBad("Неизвестный статус публикации")
			}
			newStatus = v
			p.set(key, v)
		case "plan_at", "published_at", "views":
			v, err := p.optInt(raw)
			if err != nil {
				return err
			}
			p.set(key, v)
		case "url":
			v, err := p.str(raw, 1000)
			if err != nil {
				return err
			}
			p.set(key, v)
		case "note":
			v, err := p.str(raw, 2000)
			if err != nil {
				return err
			}
			p.set(key, v)
		case "channel_id":
			v, err := p.optInt(raw)
			if err != nil || v == nil {
				return errBad("Некорректный аккаунт")
			}
			p.set(key, *v)
		default:
			return errBad("Поле %s нельзя изменить", key)
		}
	}
	if newStatus == "published" && !oldPub.Valid {
		if _, given := req["published_at"]; !given {
			p.set("published_at", nowMs())
		}
	}
	p.set("updated_at", nowMs())
	if err := p.exec(s.db, "publications", "id", id); err != nil {
		if isConstraint(err) {
			return errConflict("Для этого аккаунта публикация уже есть")
		}
		return err
	}
	s.afterPublicationChange(currentUser(r).ID, vid, id, oldStatus, newStatus)
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleDeletePublication(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var vid int64
	if err := s.db.QueryRow(`SELECT video_id FROM publications WHERE id = ?`, id).Scan(&vid); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM publications WHERE id = ?`, id); err != nil {
		return err
	}
	s.events.Publish("videos", "video:"+strconv.FormatInt(vid, 10), "calendar")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) afterPublicationChange(userID, videoID, pubID int64, oldStatus, newStatus string) {
	if newStatus != oldStatus && (newStatus == "published" || newStatus == "scheduled") {
		var platform, channel, lang string
		s.db.QueryRow(`SELECT pl.name, c.name, va.language_code FROM publications p
			JOIN channels c ON c.id = p.channel_id JOIN platforms pl ON pl.id = c.platform_id
			JOIN variants va ON va.id = p.variant_id WHERE p.id = ?`, pubID).Scan(&platform, &channel, &lang)
		s.logActivity(userID, videoID, "publication."+newStatus, map[string]any{"platform": platform, "channel": channel, "lang": lang})
	}
	if newStatus == "published" && s.setting("auto_done_stage") == "1" {
		// move the video to the first "done" stage unless it is already in one
		var kind sql.NullString
		s.db.QueryRow(`SELECT st.kind FROM videos v LEFT JOIN stages st ON st.id = v.stage_id WHERE v.id = ?`, videoID).Scan(&kind)
		if kind.String != "done" {
			var doneID int64
			if err := s.db.QueryRow(`SELECT id FROM stages WHERE kind = 'done' AND archived = 0 ORDER BY sort, id LIMIT 1`).Scan(&doneID); err == nil {
				s.db.Exec(`UPDATE videos SET stage_id = ?, updated_at = ? WHERE id = ?`, doneID, nowMs(), videoID)
			}
		}
	}
	s.db.Exec(`UPDATE videos SET updated_at = ? WHERE id = ?`, nowMs(), videoID)
	s.events.Publish("videos", "video:"+strconv.FormatInt(videoID, 10), "calendar")
}

// ---- checklist ------------------------------------------------------------

func (s *Server) handleAddCheck(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Label string `json:"label"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	label := cleanText(req.Label, 300)
	if label == "" {
		return errBad("Пустой пункт")
	}
	res, err := s.db.Exec(`INSERT INTO checklist (video_id, label, sort) VALUES (?, ?, (SELECT COALESCE(MAX(sort), 0) + 1 FROM checklist WHERE video_id = ?))`, id, label, id)
	if err != nil {
		return err
	}
	cid, _ := res.LastInsertId()
	s.events.Publish("videos", "video:"+strconv.FormatInt(id, 10))
	return ok(w, map[string]int64{"id": cid})
}

func (s *Server) handlePatchCheck(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var vid int64
	if err := s.db.QueryRow(`SELECT video_id FROM checklist WHERE id = ?`, id).Scan(&vid); err != nil {
		return err
	}
	var req struct {
		Label *string `json:"label"`
		Done  *bool   `json:"done"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Label != nil {
		l := cleanText(*req.Label, 300)
		if l == "" {
			return errBad("Пустой пункт")
		}
		if _, err := s.db.Exec(`UPDATE checklist SET label = ? WHERE id = ?`, l, id); err != nil {
			return err
		}
	}
	if req.Done != nil {
		if *req.Done {
			_, err = s.db.Exec(`UPDATE checklist SET done_at = ?, done_by = ? WHERE id = ?`, nowMs(), currentUser(r).ID, id)
		} else {
			_, err = s.db.Exec(`UPDATE checklist SET done_at = NULL, done_by = NULL WHERE id = ?`, id)
		}
		if err != nil {
			return err
		}
	}
	s.events.Publish("videos", "video:"+strconv.FormatInt(vid, 10))
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteCheck(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var vid int64
	if err := s.db.QueryRow(`SELECT video_id FROM checklist WHERE id = ?`, id).Scan(&vid); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM checklist WHERE id = ?`, id); err != nil {
		return err
	}
	s.events.Publish("videos", "video:"+strconv.FormatInt(vid, 10))
	return ok(w, map[string]bool{"ok": true})
}

// ---- comments -------------------------------------------------------------

type Comment struct {
	ID        int64  `json:"id"`
	VideoID   int64  `json:"video_id"`
	UserID    int64  `json:"user_id"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"created_at"`
	EditedAt  *int64 `json:"edited_at"`
}

func (s *Server) handleListComments(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	out := []Comment{}
	err = s.eachRow(`SELECT id, video_id, user_id, body, created_at, edited_at FROM comments WHERE video_id = ? AND deleted_at IS NULL ORDER BY id`, []any{id}, func(r *sql.Rows) error {
		var c Comment
		var ed nullInt64
		if err := r.Scan(&c.ID, &c.VideoID, &c.UserID, &c.Body, &c.CreatedAt, &ed); err != nil {
			return err
		}
		c.EditedAt = ed.ptr()
		out = append(out, c)
		return nil
	})
	if err != nil {
		return err
	}
	return ok(w, out)
}

func (s *Server) handleAddComment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Body string `json:"body"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	body := cleanText(req.Body, 10000)
	if body == "" {
		return errBad("Пустой комментарий")
	}
	u := currentUser(r)
	res, err := s.db.Exec(`INSERT INTO comments (video_id, user_id, body, created_at) VALUES (?, ?, ?, ?)`, id, u.ID, body, nowMs())
	if err != nil {
		return err
	}
	cid, _ := res.LastInsertId()
	excerpt := body
	if r := []rune(excerpt); len(r) > 140 {
		excerpt = string(r[:140]) + "…"
	}
	s.logActivity(u.ID, id, "comment.added", map[string]any{"text": excerpt})
	s.events.Publish("videos", "video:"+strconv.FormatInt(id, 10))
	return ok(w, map[string]int64{"id": cid})
}

func (s *Server) commentOwner(id int64) (videoID, userID int64, err error) {
	err = s.db.QueryRow(`SELECT video_id, user_id FROM comments WHERE id = ? AND deleted_at IS NULL`, id).Scan(&videoID, &userID)
	return
}

func (s *Server) handlePatchComment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	vid, uid, err := s.commentOwner(id)
	if err != nil {
		return err
	}
	if uid != currentUser(r).ID {
		return errForbidden()
	}
	var req struct {
		Body string `json:"body"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	body := cleanText(req.Body, 10000)
	if body == "" {
		return errBad("Пустой комментарий")
	}
	if _, err := s.db.Exec(`UPDATE comments SET body = ?, edited_at = ? WHERE id = ?`, body, nowMs(), id); err != nil {
		return err
	}
	s.events.Publish("video:" + strconv.FormatInt(vid, 10))
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteComment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	vid, uid, err := s.commentOwner(id)
	if err != nil {
		return err
	}
	u := currentUser(r)
	if uid != u.ID && !u.admin() {
		return errForbidden()
	}
	if _, err := s.db.Exec(`UPDATE comments SET deleted_at = ? WHERE id = ?`, nowMs(), id); err != nil {
		return err
	}
	s.events.Publish("videos", "video:"+strconv.FormatInt(vid, 10))
	return ok(w, map[string]bool{"ok": true})
}

// ---- patch builder --------------------------------------------------------

type patch struct {
	cols []string
	args []any
}

func newPatch() *patch { return &patch{} }

func (p *patch) set(col string, v any) {
	p.cols = append(p.cols, col+" = ?")
	p.args = append(p.args, v)
}

func (p *patch) empty() bool { return len(p.cols) == 0 }

func (p *patch) exec(d *sql.DB, table, pk string, id any) error {
	if p.empty() {
		return nil
	}
	args := append(p.args, id)
	res, err := d.Exec(`UPDATE `+table+` SET `+strings.Join(p.cols, ", ")+` WHERE `+pk+` = ?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound("Объект")
	}
	return nil
}

func (p *patch) str(raw json.RawMessage, max int) (string, error) {
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", errBad("Ожидается строка")
	}
	return cleanText(v, max), nil
}

func (p *patch) optInt(raw json.RawMessage) (*int64, error) {
	var v *int64
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, errBad("Ожидается число или null")
	}
	return v, nil
}

func (p *patch) optDate(raw json.RawMessage) (*string, error) {
	var v *string
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, errBad("Ожидается дата или null")
	}
	if v != nil && *v == "" {
		v = nil
	}
	if v != nil && !validDate(*v) {
		return nil, errBad("Некорректная дата")
	}
	return v, nil
}

// dayOf returns the project-local calendar date of a unix ms timestamp.
func (s *Server) dayOf(ms int64) string {
	return time.UnixMilli(ms).In(s.location()).Format("2006-01-02")
}
