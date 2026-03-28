DROP INDEX IF EXISTS idx_llm_scenario_packages_model_config;
ALTER TABLE llm_scenario_packages DROP COLUMN IF EXISTS llm_model_config_id;

DROP INDEX IF EXISTS idx_llm_model_configs_single_active;
DROP INDEX IF EXISTS idx_llm_model_configs_order;
DROP TABLE IF EXISTS llm_model_configs;
