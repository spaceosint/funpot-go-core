ALTER TABLE users
    DROP COLUMN IF EXISTS banned_until,
    DROP COLUMN IF EXISTS banned_at,
    DROP COLUMN IF EXISTS ban_reason,
    DROP COLUMN IF EXISTS is_banned;
