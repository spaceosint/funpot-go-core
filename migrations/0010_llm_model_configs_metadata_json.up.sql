ALTER TABLE llm_model_configs
    ADD COLUMN IF NOT EXISTS metadata_json TEXT NOT NULL DEFAULT '';
