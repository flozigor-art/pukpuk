-- All timestamps are unix milliseconds (UTC). Calendar dates are 'YYYY-MM-DD' in the project time zone.

CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  login         TEXT NOT NULL UNIQUE COLLATE NOCASE,
  name          TEXT NOT NULL,
  role          TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
  color         TEXT NOT NULL DEFAULT '#6366f1',
  password_hash TEXT NOT NULL,
  disabled      INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL
);

CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL,
  user_agent TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE languages (
  code       TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  flag       TEXT NOT NULL DEFAULT '',
  sort       INTEGER NOT NULL DEFAULT 0,
  is_primary INTEGER NOT NULL DEFAULT 0,
  archived   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE platforms (
  id               INTEGER PRIMARY KEY,
  name             TEXT NOT NULL,
  color            TEXT NOT NULL DEFAULT '#71717a',
  icon             TEXT NOT NULL DEFAULT '',
  counts_for_quota INTEGER NOT NULL DEFAULT 1,
  sort             INTEGER NOT NULL DEFAULT 0,
  archived         INTEGER NOT NULL DEFAULT 0
);

-- A concrete account on a platform, optionally bound to one language.
CREATE TABLE channels (
  id            INTEGER PRIMARY KEY,
  platform_id   INTEGER NOT NULL REFERENCES platforms(id),
  language_code TEXT REFERENCES languages(code) ON UPDATE CASCADE,
  name          TEXT NOT NULL,
  url           TEXT NOT NULL DEFAULT '',
  sort          INTEGER NOT NULL DEFAULT 0,
  archived      INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE stages (
  id       INTEGER PRIMARY KEY,
  name     TEXT NOT NULL,
  color    TEXT NOT NULL DEFAULT '#71717a',
  kind     TEXT NOT NULL DEFAULT 'work' CHECK (kind IN ('idea', 'work', 'ready', 'done')),
  sort     INTEGER NOT NULL DEFAULT 0,
  archived INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE asset_kinds (
  key      TEXT PRIMARY KEY,
  name     TEXT NOT NULL,
  hint     TEXT NOT NULL DEFAULT '',
  scope    TEXT NOT NULL DEFAULT 'shared' CHECK (scope IN ('variant', 'shared')),
  required INTEGER NOT NULL DEFAULT 0,
  accept   TEXT NOT NULL DEFAULT '',
  sort     INTEGER NOT NULL DEFAULT 0,
  archived INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE tag_groups (
  id    INTEGER PRIMARY KEY,
  scope TEXT NOT NULL CHECK (scope IN ('video', 'music')),
  name  TEXT NOT NULL,
  sort  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE tags (
  id       INTEGER PRIMARY KEY,
  scope    TEXT NOT NULL CHECK (scope IN ('video', 'music')),
  group_id INTEGER REFERENCES tag_groups(id) ON DELETE SET NULL,
  name     TEXT NOT NULL,
  color    TEXT NOT NULL DEFAULT '',
  sort     INTEGER NOT NULL DEFAULT 0,
  UNIQUE (scope, name)
);

CREATE TABLE checklist_template (
  id    INTEGER PRIMARY KEY,
  label TEXT NOT NULL,
  sort  INTEGER NOT NULL DEFAULT 0
);

-- Content-addressed file bodies. The authoritative copy lives on the storage node.
CREATE TABLE blobs (
  sha256       TEXT PRIMARY KEY,
  size         INTEGER NOT NULL,
  mime         TEXT NOT NULL,
  state        TEXT NOT NULL CHECK (state IN ('buffered', 'stored', 'missing')),
  local        INTEGER NOT NULL DEFAULT 0, -- a copy exists in the VPS data dir
  last_access  INTEGER NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL,
  stored_at    INTEGER,
  sync_error   TEXT NOT NULL DEFAULT '',
  duration_ms  INTEGER,
  width        INTEGER,
  height       INTEGER,
  meta         TEXT NOT NULL DEFAULT '{}',
  peaks        TEXT,
  derive_state TEXT NOT NULL DEFAULT '',
  derive_at    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX blobs_state ON blobs(state, local);

-- Small derived files kept on the VPS: poster/thumb (kept), preview (LRU).
CREATE TABLE derived_files (
  sha256      TEXT NOT NULL REFERENCES blobs(sha256) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  size        INTEGER NOT NULL,
  created_at  INTEGER NOT NULL,
  last_access INTEGER NOT NULL,
  PRIMARY KEY (sha256, name)
);

CREATE TABLE videos (
  id          INTEGER PRIMARY KEY,
  num         INTEGER NOT NULL UNIQUE,
  title       TEXT NOT NULL,
  stage_id    INTEGER REFERENCES stages(id),
  assignee_id INTEGER REFERENCES users(id),
  plan_date   TEXT,
  is_unique   INTEGER NOT NULL DEFAULT 1,
  original_id INTEGER REFERENCES videos(id) ON DELETE SET NULL,
  script      TEXT NOT NULL DEFAULT '',
  notes       TEXT NOT NULL DEFAULT '',
  music_note  TEXT NOT NULL DEFAULT '',
  created_by  INTEGER REFERENCES users(id),
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  deleted_at  INTEGER
);
CREATE INDEX videos_plan ON videos(plan_date);

CREATE TABLE video_tags (
  video_id INTEGER NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
  tag_id   INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (video_id, tag_id)
);

-- A language version of a video: its own voice, subtitles, final cut and publications.
CREATE TABLE variants (
  id            INTEGER PRIMARY KEY,
  video_id      INTEGER NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
  language_code TEXT NOT NULL REFERENCES languages(code) ON UPDATE CASCADE,
  title         TEXT NOT NULL DEFAULT '',
  caption       TEXT NOT NULL DEFAULT '',
  voice         TEXT NOT NULL DEFAULT 'original',
  status        TEXT NOT NULL DEFAULT 'todo' CHECK (status IN ('todo', 'wip', 'ready')),
  created_at    INTEGER NOT NULL,
  UNIQUE (video_id, language_code)
);

CREATE TABLE assets (
  id          INTEGER PRIMARY KEY,
  video_id    INTEGER NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
  variant_id  INTEGER REFERENCES variants(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL,
  filename    TEXT NOT NULL,
  sha256      TEXT NOT NULL REFERENCES blobs(sha256),
  size        INTEGER NOT NULL,
  mime        TEXT NOT NULL,
  version     INTEGER NOT NULL DEFAULT 1,
  note        TEXT NOT NULL DEFAULT '',
  uploaded_by INTEGER REFERENCES users(id),
  created_at  INTEGER NOT NULL,
  deleted_at  INTEGER
);
CREATE INDEX assets_video ON assets(video_id);
CREATE INDEX assets_sha ON assets(sha256);

CREATE TABLE publications (
  id           INTEGER PRIMARY KEY,
  video_id     INTEGER NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
  variant_id   INTEGER NOT NULL REFERENCES variants(id) ON DELETE CASCADE,
  channel_id   INTEGER NOT NULL REFERENCES channels(id),
  status       TEXT NOT NULL CHECK (status IN ('planned', 'scheduled', 'published', 'skipped', 'removed')),
  plan_at      INTEGER,
  published_at INTEGER,
  url          TEXT NOT NULL DEFAULT '',
  note         TEXT NOT NULL DEFAULT '',
  views        INTEGER,
  created_by   INTEGER REFERENCES users(id),
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL,
  UNIQUE (variant_id, channel_id)
);
CREATE INDEX publications_video ON publications(video_id);

CREATE TABLE checklist (
  id       INTEGER PRIMARY KEY,
  video_id INTEGER NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
  label    TEXT NOT NULL,
  done_by  INTEGER REFERENCES users(id),
  done_at  INTEGER,
  sort     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX checklist_video ON checklist(video_id);

CREATE TABLE comments (
  id         INTEGER PRIMARY KEY,
  video_id   INTEGER NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id),
  body       TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  edited_at  INTEGER,
  deleted_at INTEGER
);
CREATE INDEX comments_video ON comments(video_id);

-- Per-day notes for the publication plan; excused days do not count as misses.
CREATE TABLE day_notes (
  date       TEXT PRIMARY KEY,
  excused    INTEGER NOT NULL DEFAULT 0,
  reason     TEXT NOT NULL DEFAULT '',
  note       TEXT NOT NULL DEFAULT '',
  user_id    INTEGER REFERENCES users(id),
  updated_at INTEGER NOT NULL
);

CREATE TABLE tracks (
  id          INTEGER PRIMARY KEY,
  title       TEXT NOT NULL,
  artist      TEXT NOT NULL DEFAULT '',
  album       TEXT NOT NULL DEFAULT '',
  bpm         INTEGER,
  musical_key TEXT NOT NULL DEFAULT '',
  duration_ms INTEGER,
  sha256      TEXT NOT NULL REFERENCES blobs(sha256),
  filename    TEXT NOT NULL,
  size        INTEGER NOT NULL,
  source      TEXT NOT NULL DEFAULT '',
  license     TEXT NOT NULL DEFAULT '',
  license_url TEXT NOT NULL DEFAULT '',
  platforms   TEXT NOT NULL DEFAULT '[]',
  notes       TEXT NOT NULL DEFAULT '',
  uploaded_by INTEGER REFERENCES users(id),
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  deleted_at  INTEGER
);
CREATE INDEX tracks_sha ON tracks(sha256);

CREATE TABLE track_tags (
  track_id INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
  tag_id   INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (track_id, tag_id)
);

CREATE TABLE track_favorites (
  user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  track_id INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
  PRIMARY KEY (user_id, track_id)
);

CREATE TABLE video_tracks (
  video_id INTEGER NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
  track_id INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
  PRIMARY KEY (video_id, track_id)
);

CREATE TABLE activity (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER REFERENCES users(id),
  video_id   INTEGER REFERENCES videos(id) ON DELETE CASCADE,
  action     TEXT NOT NULL,
  data       TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL
);
CREATE INDEX activity_video ON activity(video_id, id);

CREATE TABLE uploads (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id),
  filename   TEXT NOT NULL,
  size       INTEGER NOT NULL,
  mime       TEXT NOT NULL,
  received   INTEGER NOT NULL DEFAULT 0,
  hash_state BLOB,
  target     TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- Blobs purged on the server that the node still has to move to its trash.
CREATE TABLE node_deletes (
  sha256     TEXT PRIMARY KEY,
  created_at INTEGER NOT NULL
);

CREATE TABLE node_state (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
