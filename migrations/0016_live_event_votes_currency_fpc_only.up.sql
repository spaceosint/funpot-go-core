ALTER TABLE live_event_votes
    ADD COLUMN IF NOT EXISTS currency TEXT NOT NULL DEFAULT 'FPC';

UPDATE live_event_votes
SET currency = 'FPC'
WHERE currency IS DISTINCT FROM 'FPC';

ALTER TABLE live_event_votes
    DROP CONSTRAINT IF EXISTS live_event_votes_currency_fpc_check;

ALTER TABLE live_event_votes
    ADD CONSTRAINT live_event_votes_currency_fpc_check
    CHECK (currency = 'FPC');
