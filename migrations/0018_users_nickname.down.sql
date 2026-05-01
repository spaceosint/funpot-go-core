DROP INDEX IF EXISTS idx_users_nickname;
ALTER TABLE users DROP COLUMN IF EXISTS nickname;
