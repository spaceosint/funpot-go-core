-- 0007_config.up.sql
-- Generic application config table.
-- Do not store real secrets here unless you intentionally encrypt/protect them.

CREATE TABLE public.config (
    key TEXT PRIMARY KEY CHECK (key <> ''),

    value_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    description TEXT NOT NULL DEFAULT '',

    updated_by TEXT NOT NULL DEFAULT '',

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER trg_config_set_updated_at
BEFORE UPDATE ON public.config
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();
