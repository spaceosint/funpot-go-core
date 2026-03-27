DROP INDEX IF EXISTS idx_llm_rule_set_versions_order;
DROP INDEX IF EXISTS idx_llm_rule_set_versions_active_game;
DROP TABLE IF EXISTS llm_rule_set_versions;

DROP INDEX IF EXISTS idx_llm_state_schema_versions_order;
DROP INDEX IF EXISTS idx_llm_state_schema_versions_active_game;
DROP TABLE IF EXISTS llm_state_schema_versions;

DROP INDEX IF EXISTS idx_llm_prompt_versions_order;
DROP INDEX IF EXISTS idx_llm_prompt_versions_active_stage;
DROP TABLE IF EXISTS llm_prompt_versions;
