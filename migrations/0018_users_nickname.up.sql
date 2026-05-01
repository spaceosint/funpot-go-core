ALTER TABLE users ADD COLUMN IF NOT EXISTS nickname TEXT NOT NULL DEFAULT '';
UPDATE users SET nickname = username WHERE nickname = '';
CREATE INDEX IF NOT EXISTS idx_users_nickname ON users (nickname);
