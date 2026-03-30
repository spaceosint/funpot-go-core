ALTER TABLE IF EXISTS streamer_llm_decisions DROP COLUMN IF EXISTS previous_state_json;
ALTER TABLE IF EXISTS streamer_llm_decisions DROP COLUMN IF EXISTS evidence_delta_json;
ALTER TABLE IF EXISTS streamer_llm_decisions DROP COLUMN IF EXISTS conflicts_json;
ALTER TABLE IF EXISTS streamer_llm_decisions DROP COLUMN IF EXISTS final_outcome;
