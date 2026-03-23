ALTER TABLE streamer_llm_decisions DROP COLUMN IF EXISTS final_outcome;
ALTER TABLE streamer_llm_decisions DROP COLUMN IF EXISTS conflicts_json;
ALTER TABLE streamer_llm_decisions DROP COLUMN IF EXISTS evidence_delta_json;
ALTER TABLE streamer_llm_decisions DROP COLUMN IF EXISTS updated_state_json;
ALTER TABLE streamer_llm_decisions DROP COLUMN IF EXISTS previous_state_json;
