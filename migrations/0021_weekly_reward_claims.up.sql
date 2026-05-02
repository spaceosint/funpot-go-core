CREATE TABLE IF NOT EXISTS weekly_reward_claims (
    user_id TEXT PRIMARY KEY,
    last_claim_at TIMESTAMPTZ,
    streak_day INT NOT NULL DEFAULT 0 CHECK (streak_day >= 0 AND streak_day <= 7),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
