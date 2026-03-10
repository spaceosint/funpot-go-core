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
| `DEV_DATABASE_HOST` | Development PostgreSQL host. | Used to compose CI migration DSN on `dev` branch when mode is `apply`. |
| `DEV_DATABASE_PORT` | Development PostgreSQL port. | Used to compose CI migration DSN on `dev` branch when mode is `apply`. |
| `DEV_DATABASE_NAME` | Development PostgreSQL database name. | Used to compose CI migration DSN on `dev` branch when mode is `apply`. |
| `DEV_DATABASE_USER` | Development PostgreSQL user. | Used to compose CI migration DSN on `dev` branch when mode is `apply`. |
| `DEV_DATABASE_PASSWORD` | Development PostgreSQL password. | Used to compose CI migration DSN on `dev` branch when mode is `apply`. |
| `DEV_DATABASE_SSLMODE` | Development PostgreSQL sslmode (`disable`, `require`, etc.). | Optional, defaults to `require` in CI migration stage when mode is `apply`. |
| `PROD_DATABASE_HOST` | Production PostgreSQL host. | Used to compose CI migration DSN on `main` branch when mode is `apply`. |
| `PROD_DATABASE_PORT` | Production PostgreSQL port. | Used to compose CI migration DSN on `main` branch when mode is `apply`. |
| `PROD_DATABASE_NAME` | Production PostgreSQL database name. | Used to compose CI migration DSN on `main` branch when mode is `apply`. |
| `PROD_DATABASE_USER` | Production PostgreSQL user. | Used to compose CI migration DSN on `main` branch when mode is `apply`. |
| `PROD_DATABASE_PASSWORD` | Production PostgreSQL password. | Used to compose CI migration DSN on `main` branch when mode is `apply`. |
| `PROD_DATABASE_SSLMODE` | Production PostgreSQL sslmode (`disable`, `require`, etc.). | Optional, defaults to `require` in CI migration stage when mode is `apply`. |
| `DEV_MIGRATIONS_MODE` | `check` or `apply`. | Controls dev migration behavior in CI (`check` default). |
| `PROD_MIGRATIONS_MODE` | `check` or `apply`. | Controls production migration behavior in CI (`check` default, recommended). |

## Migration Stage in CI
Migration execution was moved from CD to the CI workflow:

- On `dev` pushes CI uses `DEV_DATABASE_HOST/PORT/NAME/USER/PASSWORD(/SSLMODE)` + `DEV_MIGRATIONS_MODE`.
- On `main` pushes CI uses `PROD_DATABASE_HOST/PORT/NAME/USER/PASSWORD(/SSLMODE)` + `PROD_MIGRATIONS_MODE`.
- `check` (default): validates migration file set only (naming + up/down pair consistency) and exits without DB connection.
- `apply`: runs DB preflight (`migrate version`) and then executes `migrate up`.

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
