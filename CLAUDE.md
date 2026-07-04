# bookie-breaker-statistics-service

## Service Purpose

Go REST API providing centralized team/player statistics, schedules, game results, and injury data with Redis caching. Publishes stat update and game completion events.

## Language & Conventions

- **Language:** Go 1.25
- **Framework:** Echo
- **Project layout:** `cmd/server/main.go` entry point, `internal/` for private code, `pkg/` for public libraries
- **Naming:** `snake_case.go` files, `camelCase` variables, `PascalCase` exports
- **Testing:** `*_test.go` co-located, `tests/integration/` for testcontainers

## Key Files

- `cmd/server/main.go` — HTTP server entry point
- `internal/handler/` — HTTP route handlers
- `internal/service/` — Business logic
- `internal/adapter/` — External API adapters (nba_api, nfl_data_py, etc.)
- `.config/mise.toml` — Tool versions
- `.config/lefthook.yml` — Git hooks

## Service-Specific Commands

```bash
task dev          # Run with air hot reload
task lint         # golangci-lint
task test         # go test -race ./...
task build        # Build to bin/server
```

## Dependencies

- **Redis** — Primary store: canonical collections cached with per-type TTLs plus 7-day stale
  mirrors; pub/sub (`events:stats.updated`, `events:game.completed`)
- **PostgreSQL** — Archival only: raw API responses into `public.raw_api_responses`
  (role `statistics_svc` owns no schema and no tables)
- **External APIs** — stats.nba.com direct from Go (ADR-020), ESPN for injuries (ADR-008
  fallback; NBA.com has no injury endpoint); nfl_data_py/pybaseball via sidecar (Phase 6)
- No upstream service dependencies

## Environment Variables

See `.env.example`. Key: `REDIS_URL`, `DATABASE_URL`, `PORT=8002`, `INJURY_SOURCE`.
