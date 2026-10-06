-- Profile pictures. The picture itself is small (a 256 px JPEG made in the
-- browser) and lives in the database, so it shows even when the storage PC
-- is offline. avatar_at is its version: NULL means initials on a colour.
ALTER TABLE users ADD COLUMN avatar_at INTEGER;

CREATE TABLE user_avatars (
  user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  mime    TEXT NOT NULL,
  data    BLOB NOT NULL
);
