package server

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Default settings; values live in the settings table and can be changed by an admin.
var defaultSettings = map[string]string{
	"code_prefix":     "LP",
	"timezone":        "",   // empty: CRM_TZ
	"quota_per_day":   "1",  // п.6.1: at least one new unique video per day
	"quota_start":     "",   // first day the daily plan is tracked; set on first start
	"archive_hours":   "48", // п.7.6: archive within 48 hours after first publication
	"window_days":     "30", // п.26.3: misses are counted over any 30 days
	"auto_done_stage": "1",  // move a video to the first "done" stage when it is published
}

type seedUser struct{ login, name, role, color string }

// The four accounts of the project: three parties of the contract and Sasha.
var seedUsers = []seedUser{
	{"sasha", "Саша", "admin", "#6366f1"},
	{"ksenia", "Ксения", "member", "#0ea5e9"},
	{"anastasia", "Анастасия", "member", "#f59e0b"},
	{"isabella", "Изабелла", "member", "#ec4899"},
}

func (s *Server) seed() error {
	now := nowMs()
	for k, v := range defaultSettings {
		if k == "quota_start" {
			v = time.Now().Format("2006-01-02")
		}
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO settings (key, value) VALUES (?, ?)`, k, v); err != nil {
			return err
		}
	}

	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		var lines []string
		for _, u := range seedUsers {
			pw := GeneratePassword()
			hash, err := HashPassword(pw)
			if err != nil {
				return err
			}
			if _, err := s.db.Exec(`INSERT INTO users (login, name, role, color, password_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
				u.login, u.name, u.role, u.color, hash, now); err != nil {
				return err
			}
			lines = append(lines, fmt.Sprintf("%-10s %s", u.login, pw))
		}
		path := filepath.Join(s.cfg.DataDir, "initial-passwords.txt")
		body := "Начальные пароли LangPlant CRM (смените после первого входа и удалите этот файл)\n\n" + strings.Join(lines, "\n") + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			return err
		}
		slog.Warn("created initial accounts; passwords are in " + path)
	}

	if err := s.db.QueryRow(`SELECT COUNT(*) FROM languages`).Scan(&n); err != nil || n > 0 {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	exec := func(q string, args ...any) {
		if err == nil {
			_, err = tx.Exec(q, args...)
		}
	}

	exec(`INSERT INTO languages (code, name, flag, sort, is_primary) VALUES ('ru', 'Русский', '🇷🇺', 0, 1), ('en', 'English', '🇬🇧', 1, 0)`)

	platforms := []struct{ name, color, icon string }{
		{"TikTok", "#111111", "tiktok"},
		{"YouTube Shorts", "#ff0033", "youtube"},
		{"Instagram Reels", "#d6249f", "instagram"},
	}
	for i, p := range platforms {
		var res sql.Result
		if err == nil {
			res, err = tx.Exec(`INSERT INTO platforms (name, color, icon, sort) VALUES (?, ?, ?, ?)`, p.name, p.color, p.icon, i)
		}
		if err == nil {
			id, _ := res.LastInsertId()
			exec(`INSERT INTO channels (platform_id, language_code, name, sort) VALUES (?, 'ru', 'LangPlant', ?)`, id, i)
		}
	}

	stages := []struct{ name, color, kind string }{
		{"Идея", "#a1a1aa", "idea"},
		{"Сценарий", "#8b5cf6", "work"},
		{"Съёмка", "#f59e0b", "work"},
		{"Монтаж", "#3b82f6", "work"},
		{"Готов", "#14b8a6", "ready"},
		{"Опубликован", "#22c55e", "done"},
	}
	for i, st := range stages {
		exec(`INSERT INTO stages (name, color, kind, sort) VALUES (?, ?, ?, ?)`, st.name, st.color, st.kind, i)
	}

	// п.7.2 — the mandatory minimum; п.7.5 — the rest when technically possible.
	kinds := []struct {
		key, name, hint, scope string
		required               bool
		accept                 string
	}{
		{"final", "Финальный ролик", "Итоговая версия, как опубликована", "variant", true, "video/*"},
		{"voice", "Чистый голос", "Без музыки, SFX и реверберации (п.7.3)", "variant", true, "audio/*,video/*"},
		{"bed", "Дорожка без голоса", "Музыка + звуковое оформление (п.7.4)", "shared", true, "audio/*,video/*"},
		{"clean", "Видео без субтитров", "Чистая картинка для локализации", "shared", false, "video/*"},
		{"subs", "Субтитры", "SRT / VTT / ASS", "variant", false, ".srt,.vtt,.ass,.txt"},
		{"cover", "Обложка", "", "variant", false, "image/*"},
		{"music", "Музыка", "Отдельные музыкальные дорожки", "shared", false, "audio/*"},
		{"sfx", "Звуки (SFX)", "", "shared", false, "audio/*"},
		{"raw", "Исходники", "Исходное видео со съёмки", "shared", false, ""},
		{"project", "Проект монтажа", "Premiere / DaVinci / CapCut / AE", "shared", false, ""},
		{"graphics", "Графика", "Плашки, титры, элементы", "shared", false, ""},
		{"script", "Сценарий (файл)", "", "variant", false, ""},
		{"other", "Другое", "", "shared", false, ""},
	}
	for i, k := range kinds {
		exec(`INSERT INTO asset_kinds (key, name, hint, scope, required, accept, sort) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			k.key, k.name, k.hint, k.scope, boolInt(k.required), k.accept, i)
	}

	for i, l := range []string{"Сценарий утверждён", "Снято", "Смонтировано", "Обложка готова", "Описание и хэштеги", "Музыка: источник и лицензия указаны"} {
		exec(`INSERT INTO checklist_template (label, sort) VALUES (?, ?)`, l, i)
	}

	groups := []struct {
		scope, name string
		tags        []string
	}{
		{"video", "Рубрика", nil},
		{"video", "Формат", []string{"Скетч", "Объяснение", "Диалог", "Тренд", "Интеграция"}},
		{"music", "Жанр", []string{"Lo-fi", "Поп", "Электроника", "Хип-хоп", "Акустика", "Классика", "Рок", "Джаз", "Эмбиент", "Кино"}},
		{"music", "Настроение", []string{"Весёлое", "Спокойное", "Энергичное", "Вдохновляющее", "Грустное", "Напряжённое", "Романтичное", "Смешное"}},
		{"music", "Темп", []string{"Медленный", "Средний", "Быстрый"}},
		{"music", "Использование", []string{"Фон под голос", "Интро", "Переход", "Тренд"}},
	}
	for gi, g := range groups {
		var res sql.Result
		if err == nil {
			res, err = tx.Exec(`INSERT INTO tag_groups (scope, name, sort) VALUES (?, ?, ?)`, g.scope, g.name, gi)
		}
		if err != nil {
			break
		}
		gid, _ := res.LastInsertId()
		for ti, t := range g.tags {
			exec(`INSERT INTO tags (scope, group_id, name, sort) VALUES (?, ?, ?, ?)`, g.scope, gid, t, ti)
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

const pwAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GeneratePassword returns a random readable password.
func GeneratePassword() string {
	out := make([]byte, 12)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(pwAlphabet))))
		if err != nil {
			panic(err)
		}
		out[i] = pwAlphabet[n.Int64()]
	}
	// grouped as xxxx-xxxx-xxxx for readability (~69 bits of entropy)
	return string(out[0:4]) + "-" + string(out[4:8]) + "-" + string(out[8:12])
}

func (s *Server) setting(key string) string {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		return defaultSettings[key]
	}
	return v
}

func (s *Server) settings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for k, v := range defaultSettings {
		out[k] = v
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	if out["timezone"] == "" {
		out["timezone"] = s.cfg.TZ
	}
	return out, rows.Err()
}
