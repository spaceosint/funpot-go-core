# Secret Configuration Reference

This document enumerates all secrets that must be provisioned for the FunPot Go
Core service and its CI/CD workflows. Each entry describes the expected format
and where the secret is required.

## Runtime secrets

| Key | Required environments | Format | Notes |
| --- | --- | --- | --- |
| `FUNPOT_SENTRY_DSN` | staging, production | Full Sentry DSN string: `https://<public_key>@o<org_id>.ingest.sentry.io/<project_id>` | Mandatory wherever Sentry error reporting is enabled. Leave blank in local development to disable Sentry. |
| `FUNPOT_AUTH_TELEGRAM_BOT_TOKEN` | all (dev, staging, production, CI smoke tests) | Raw token issued by BotFather: `123456789:AA...` | Used to validate Telegram Mini App `initData`. CI smoke tests pull the same token to sign requests. |
| `FUNPOT_AUTH_JWT_SECRET` | staging, production | Strong symmetric key (>=32 random bytes). Example: base64-encoded string `c2VjdXJlLXNpZ25pbmcta2V5LWF0LWxlYXN0LTMyaHR=` | Overrides the insecure dev default (`dev-secret`). Rotate via secret storage; never commit to VCS. |
| `FUNPOT_DATABASE_DSN` | staging, production | PostgreSQL DSN: `postgres://<user>:<password>@<host>:<port>/<db>?sslmode=require` | Points to managed PostgreSQL instance. Local development can reuse dockerised DSN from `docs/local_setup.md`. |

## CI/CD secrets

| Key | Required contexts | Format | Notes |
| --- | --- | --- | --- |
| `REGISTRY_URL` | CI | Fully qualified registry host, e.g. `registry.example.com` | Used by CI to tag and push container images. |
| `REGISTRY_REPOSITORY` | CI | Registry namespace/repository, e.g. `funpot/core` | Combined with `REGISTRY_URL` to build image reference. |
| `REGISTRY_USERNAME` | CI | Registry login/user name | Provide a robot/service account with push permissions. |
| `REGISTRY_PASSWORD` | CI | Registry password or access token | Stored as secret text. Avoid expiring passwords; prefer scoped tokens. |
| `DEV_DEPLOY_WEBHOOK_URL` | CD | HTTPS endpoint, e.g. `https://deploy.example.com/dev` | Called after CI succeeds to trigger dev rollout. Must accept POST with JSON payload. |
| `PROD_DEPLOY_WEBHOOK_URL` | CD | HTTPS endpoint, e.g. `https://deploy.example.com/prod` | Same contract as dev webhook. Required before enabling production CD. |
| `DEV_DEPLOY_HEALTHCHECK_URL` | CD | HTTPS URL to `/readyz`, e.g. `https://dev.funpot.live/readyz` | Polled until environment reports ready. |
| `PROD_DEPLOY_HEALTHCHECK_URL` | CD (optional but recommended) | HTTPS URL to `/readyz` | Leave undefined to skip post-deploy health poll. |

## Management checklist

1. Store every secret in the infrastructure secret manager or CI/CD vault with
   least-privilege access.
2. Use separate values per environment (dev, staging, production) to avoid
   accidental cross-environment access.
3. Rotate secrets periodically and update this reference whenever the surface
   area changes.
