ALTER TABLE streamer_llm_decisions ADD COLUMN IF NOT EXISTS previous_state_json TEXT;
ALTER TABLE streamer_llm_decisions ADD COLUMN IF NOT EXISTS updated_state_json TEXT;
ALTER TABLE streamer_llm_decisions ADD COLUMN IF NOT EXISTS evidence_delta_json TEXT;
ALTER TABLE streamer_llm_decisions ADD COLUMN IF NOT EXISTS conflicts_json TEXT;
ALTER TABLE streamer_llm_decisions ADD COLUMN IF NOT EXISTS final_outcome TEXT;
