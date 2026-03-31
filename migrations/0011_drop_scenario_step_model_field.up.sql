UPDATE llm_scenario_packages
SET steps_json = COALESCE((
    SELECT jsonb_agg(step - 'model')
    FROM jsonb_array_elements(steps_json) AS step
), '[]'::jsonb)
WHERE EXISTS (
    SELECT 1
    FROM jsonb_array_elements(steps_json) AS step
    WHERE step ? 'model'
);
