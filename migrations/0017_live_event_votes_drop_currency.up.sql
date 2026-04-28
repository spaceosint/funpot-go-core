ALTER TABLE live_event_votes
    DROP CONSTRAINT IF EXISTS live_event_votes_currency_fpc_check;

ALTER TABLE live_event_votes
    DROP COLUMN IF EXISTS currency;
