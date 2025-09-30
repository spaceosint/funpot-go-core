# Repository Guidelines

This repository contains the backend modular monolith for the FunPot Telegram Mini App platform. Use Go for all backend services unless an architectural document explicitly states an alternative.

## Code Organization
- Keep the service as a single Go module (`go mod`).
- Domain packages must live under `internal/` using the following layout:
  - `internal/app` – HTTP/WSS bootstrap, routing, dependency wiring.
  - `internal/auth`, `internal/users`, `internal/wallet`, `internal/payments`, `internal/referrals`, `internal/streamers`, `internal/games`, `internal/events`, `internal/votes`, `internal/media`, `internal/prompts`, `internal/realtime`, `internal/admin`, `internal/integrations`, `internal/config`.
- Shared utilities go under `pkg/` with clear ownership.
- Configuration is provided through environment variables. Document any new variable in `docs/local_setup.md`.

## Documentation
- Keep architectural and operational documentation inside `docs/`.
- Update the relevant section if you modify public contracts (REST/WSS) or non-functional objectives.
- The canonical OpenAPI spec lives in `docs/openapi.yaml`; regenerate it when routes change.

## Testing & Quality
- Prefer table-driven tests for Go packages.
- Maintain linting via `golangci-lint`.
- Include load/performance test plans in `docs/load_testing.md` when behavior changes.

## PR Expectations
- Reference user-facing changes in the PR description.
- Attach migrations and documentation updates alongside code changes.

## Status Reporting
- Every deliverable or follow-up task must include an explicit checklist in the final
  response that marks what has been completed (`[x]`) and what remains (`[ ]`).
- Keep the checklist aligned with the implementation plan in `docs/implementation_plan.md`
  so stakeholders can track progress across iterations.

