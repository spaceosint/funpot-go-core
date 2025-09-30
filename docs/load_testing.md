# Load & Performance Testing Plan

## Objectives
- Validate SLO compliance: REST p95 < 150 ms, p99 < 350 ms for core APIs; WebSocket update latency < 1 s.
- Ensure system supports target concurrency: 100–200 active streamers, 1k–100k concurrent users.

## Tools
- [k6](https://k6.io/) for HTTP load testing.
- Vegeta (optional) for focused attack patterns on specific endpoints.
- Custom Go benchmark harness for WebSocket fan-out tests.

## Scenarios

### S1 – Auth & Config Fetch
- **Endpoints**: `/api/me`, `/api/config`.
- **Profile**: 5k virtual users ramping over 2 minutes, sustained 10 minutes.
- **Assertions**: p95 < 120 ms, error rate < 0.1%.

### S2 – Streamer Listing
- **Endpoint**: `/api/streamers?query=`.
- **Profile**: 2k vu, 30 rps steady.
- **Assertions**: Cache hit ratio > 80% (via metrics).

### S3 – Live Events Polling
- **Endpoint**: `/api/events/live?streamerId=<uuid>`.
- **Profile**: 10k vu, 50 rps steady (leveraging Redis cache).
- **Assertions**: p95 < 150 ms, cache TTL respected (<1 s staleness).

### S4 – Voting Hot Path
- **Endpoint**: `POST /api/votes` with idempotency.
- **Profile**: 2k vu, spike 200 rps for 60 seconds.
- **Assertions**: No >1% rate-limit errors under configured thresholds, ledger consistency checks post-run.

### S5 – Payments Lifecycle
- **Endpoints**: `POST /api/payments/stars/createInvoice` followed by webhook simulation.
- **Profile**: 200 vu, 5 rps invoice creation, webhook replay tests.
- **Assertions**: Ledger balance increases exactly once per invoice.

### S6 – WebSocket Fan-out
- **Setup**: 10k simulated connections per node; publish `EVENT_UPDATED` at 2 Hz.
- **Assertions**: 99% of clients receive updates within 1 s; dropped message rate < 0.5%.

## Metrics Collection
- Export `http_server_duration_seconds` (histograms) to Prometheus.
- Custom metrics: `votes_submitted_total`, `ws_clients_connected`, `ws_message_lag_seconds`.
- k6 outputs ingested into Grafana for dashboards.

## Capacity Planning Targets
- CPU utilization < 70% under sustained load.
- Redis < 60% memory usage; ensure eviction policy set to `volatile-lru`.
- PostgreSQL connections < 70% of pool size (use pgBouncer if necessary).

## Test Automation
- Add GitHub Actions workflow to run nightly k6 smoke (reduced load) against staging.
- Full test suite executed before major releases.

