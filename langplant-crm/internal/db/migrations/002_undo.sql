-- Undo history. Every user action that changes content or settings becomes a
-- step; row triggers (generated at start-up, see server/undo.go) record the
-- before/after image of each touched row while a step is active.

CREATE TABLE undo_steps (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
  kind       TEXT NOT NULL DEFAULT 'action' CHECK (kind IN ('action', 'undo', 'redo')),
  label      TEXT NOT NULL DEFAULT '',
  video_id   INTEGER,
  admin_only INTEGER NOT NULL DEFAULT 0, -- touches settings that only an admin may change
  target_id  INTEGER,                    -- for undo/redo: the step that was reverted
  undone_by  INTEGER,                    -- the undo/redo step that reverted this one
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX undo_steps_user ON undo_steps(user_id, id);

CREATE TABLE undo_log (
  id      INTEGER PRIMARY KEY,
  step_id INTEGER NOT NULL REFERENCES undo_steps(id) ON DELETE CASCADE,
  tbl     TEXT NOT NULL,
  rid     INTEGER NOT NULL,
  op      TEXT NOT NULL CHECK (op IN ('I', 'U', 'D')),
  old     TEXT,
  new     TEXT
);
CREATE INDEX undo_log_step ON undo_log(step_id, id);
CREATE INDEX undo_log_row ON undo_log(tbl, rid, step_id);

-- The step currently being recorded (one row). NULL: changes are not recorded.
CREATE TABLE undo_ctx (
  id   INTEGER PRIMARY KEY CHECK (id = 1),
  step INTEGER
);
INSERT INTO undo_ctx (id, step) VALUES (1, NULL);
