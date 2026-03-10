# CI Registry, Migrations, and Watchtower Deployment Setup

This guide documents the values used by the CI workflow to build/publish images and
validate database migrations. Deployment is **not** handled by repository CD jobs:
rollout is performed externally by Watchtower.

## Required Secrets (CI)
The `FunPot Core CI` workflow expects these secrets:

| Secret | Source | Usage |
| --- | --- | --- |
| `REGISTRY_URL` | Fully qualified address of your container registry (e.g. `git.funwan.live`). | Used by CI to build canonical image references. |
| `REGISTRY_REPOSITORY` | Repository path inside the registry (e.g. `stream/funpot-core`). | Combined with Git SHA / rolling tags (`dev`, `prod`). |
| `REGISTRY_USERNAME` | Service account or robot user with push/pull permissions. | Authenticates Docker login in CI. |
| `REGISTRY_PASSWORD` | Token or password generated for the above user. | Authenticates Docker login in CI. |
| `FUNPOT_AUTH_TELEGRAM_BOT_TOKEN` | Token issued by BotFather for the Telegram Mini App bot. | Enables smoke tests to authenticate against the container. |
| `DEV_DATABASE_URL` | PostgreSQL DSN for development (URL-encoded, includes `sslmode`). | Used by CI migration preflight on `dev` branch. |
| `PROD_DATABASE_URL` | PostgreSQL DSN for production (URL-encoded, includes `sslmode`). | Used by CI migration preflight on `main` branch. |
| `DEV_MIGRATIONS_MODE` | `check` or `apply`. | Controls dev migration behavior in CI (`check` default). |
| `PROD_MIGRATIONS_MODE` | `check` or `apply`. | Controls production migration behavior in CI (`check` default, recommended). |

## Migration Stage in CI
Migration execution was moved from CD to the CI workflow:

- On `dev` pushes CI uses `DEV_DATABASE_URL` + `DEV_MIGRATIONS_MODE`.
- On `main` pushes CI uses `PROD_DATABASE_URL` + `PROD_MIGRATIONS_MODE`.
- `check` (default): validates migration metadata (`migrate version`) and exits.
- `apply`: runs preflight and then executes `migrate up`.

This keeps schema validation in the pipeline while leaving deployment orchestration
to external infrastructure.

## Deployment Model (Watchtower)
Image rollout is performed by your Watchtower-managed environment:

1. CI publishes immutable image tags (`:<sha>`) and rolling tags (`:dev`/`:prod`).
2. Watchtower monitors the target tag and updates running containers when a new
   image is available.
3. Environment health is verified on the infrastructure side (e.g. readiness
   probes and monitoring dashboards).

> This repository no longer ships a `.gitea/workflows/cd.yml` deployment workflow.

## Validating the Configuration
- Trigger CI from a feature branch push and confirm `Build & Publish` succeeds.
- Verify the image exists in the registry with expected `<sha>` and branch tags.
- For `dev`/`main`, confirm migration preflight passes with configured mode.
- Confirm Watchtower pulls the new tag and restarts the target service.

If registry or migration secrets are missing/incorrect, CI will fail during Docker
login, image operations, or migration checks.
