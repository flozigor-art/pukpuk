// Package config reads runtime settings from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Server is the configuration of crm-server (runs on the VPS).
type Server struct {
	Addr        string // listen address, e.g. ":8080"
	DataDir     string // sqlite db, upload buffer, local cache, previews
	PublicURL   string // https://crm.example.com — used for cookie flags
	NodeToken   string // shared secret the storage node authenticates with
	BufferMax   int64  // max bytes of files waiting to be synced to the node
	CacheMax    int64  // max bytes of already-synced files kept locally (LRU)
	PreviewMax  int64  // max bytes of preview proxies kept locally (LRU)
	DiskReserve int64  // never fill the disk above total-free minus this
	WarmMax     int64  // files up to this size are pulled into the cache when viewed
	ChunkSize   int64  // upload chunk size handed to clients
	MaxFile     int64  // max size of a single uploaded file
	TZ          string // project time zone (days of the publication plan)
	WebDir      string // serve the SPA from disk instead of the embedded build
}

// Node is the configuration of crm-node (runs on the storage PC).
type Node struct {
	ServerURL      string
	Token          string
	DataDir        string
	FFmpeg         string
	FFprobe        string
	PreviewHeight  int
	Downloads      int
	BackupInterval time.Duration
	BackupKeep     int
	TrashDays      int
}

func LoadServer() (Server, error) {
	c := Server{
		Addr:      env("CRM_ADDR", ":8080"),
		DataDir:   env("CRM_DATA_DIR", "./data"),
		PublicURL: strings.TrimRight(env("CRM_PUBLIC_URL", ""), "/"),
		NodeToken: env("CRM_NODE_TOKEN", ""),
		TZ:        env("CRM_TZ", "Europe/Moscow"),
		WebDir:    env("CRM_WEB_DIR", ""),
	}
	var err error
	sizes := []struct {
		dst *int64
		key string
		def string
	}{
		{&c.BufferMax, "CRM_BUFFER_MAX", "8GB"},
		{&c.CacheMax, "CRM_CACHE_MAX", "4GB"},
		{&c.PreviewMax, "CRM_PREVIEW_MAX", "2GB"},
		{&c.DiskReserve, "CRM_DISK_RESERVE", "1GB"},
		{&c.WarmMax, "CRM_WARM_MAX", "128MB"},
		{&c.ChunkSize, "CRM_CHUNK_SIZE", "8MB"},
		{&c.MaxFile, "CRM_MAX_FILE", "6GB"},
	}
	for _, s := range sizes {
		if *s.dst, err = ParseSize(env(s.key, s.def)); err != nil {
			return c, fmt.Errorf("%s: %w", s.key, err)
		}
	}
	if c.NodeToken != "" && len(c.NodeToken) < 24 {
		return c, fmt.Errorf("CRM_NODE_TOKEN is too short: use at least 24 random characters")
	}
	if c.MaxFile > c.BufferMax {
		c.MaxFile = c.BufferMax
	}
	return c, nil
}

func LoadNode() (Node, error) {
	c := Node{
		ServerURL: strings.TrimRight(env("NODE_SERVER_URL", ""), "/"),
		Token:     env("NODE_TOKEN", ""),
		DataDir:   env("NODE_DATA_DIR", "./storage"),
		FFmpeg:    env("NODE_FFMPEG", "ffmpeg"),
		FFprobe:   env("NODE_FFPROBE", "ffprobe"),
	}
	var err error
	if c.PreviewHeight, err = strconv.Atoi(env("NODE_PREVIEW_HEIGHT", "960")); err != nil {
		return c, fmt.Errorf("NODE_PREVIEW_HEIGHT: %w", err)
	}
	if c.Downloads, err = strconv.Atoi(env("NODE_DOWNLOADS", "2")); err != nil || c.Downloads < 1 {
		return c, fmt.Errorf("NODE_DOWNLOADS must be a positive number")
	}
	if c.BackupInterval, err = time.ParseDuration(env("NODE_BACKUP_INTERVAL", "6h")); err != nil {
		return c, fmt.Errorf("NODE_BACKUP_INTERVAL: %w", err)
	}
	if c.BackupKeep, err = strconv.Atoi(env("NODE_BACKUP_KEEP", "60")); err != nil {
		return c, fmt.Errorf("NODE_BACKUP_KEEP: %w", err)
	}
	if c.TrashDays, err = strconv.Atoi(env("NODE_TRASH_DAYS", "30")); err != nil {
		return c, fmt.Errorf("NODE_TRASH_DAYS: %w", err)
	}
	if c.ServerURL == "" || c.Token == "" {
		return c, fmt.Errorf("NODE_SERVER_URL and NODE_TOKEN are required")
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

// ParseSize understands plain bytes and KB/MB/GB/TB suffixes (binary multiples).
func ParseSize(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	for _, u := range []struct {
		suf string
		m   int64
	}{{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"T", 1 << 40}, {"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10}, {"B", 1}} {
		if strings.HasSuffix(s, u.suf) {
			mult = u.m
			s = strings.TrimSpace(strings.TrimSuffix(s, u.suf))
			break
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return int64(f * float64(mult)), nil
}
