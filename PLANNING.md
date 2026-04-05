# PLANNING: statistics-service

## Service
- **Name:** statistics-service
- **Language:** Go 1.22+
- **Framework:** Echo

## Implementation Phase
Phase 1 (Infrastructure & Data Foundation)

## Purpose
Ingests, normalizes, caches, and serves sports statistics from external APIs. Acts as a cache + enrichment layer over external data sources (nba_api, nfl_data_py, pybaseball, CFBD). Computes derived statistics like rolling averages, efficiency ratings, and advanced metrics.

## Ordered Task List

1. Initialize Go module, set up project structure: `cmd/server/`, `internal/`, `pkg/`
2. Set up Echo HTTP server with middleware (logging, recovery, CORS, request ID)
3. Implement health check endpoint (`GET /healthz`)
4. Implement Redis client connection and caching layer with configurable TTL per data type
5. Design canonical data models: `Team`, `Player`, `GameResult`, `Schedule`, `InjuryReport` (sport-agnostic base types)
6. Implement NBA adapter: fetch team stats, player stats, game logs, schedules, injury reports, and game results from nba_api (via Python sidecar HTTP wrapper or direct NBA.com HTTP endpoints)
7. Implement stats normalization: convert NBA-specific fields into canonical format
8. Implement derived statistics computation: rolling averages (last N games), offensive/defensive ratings, pace, efficiency, per-game rates
9. Build REST API endpoints:
   - `GET /api/v1/stats/{sport}/teams` -- all team stats for a sport
   - `GET /api/v1/stats/{sport}/teams/{teamId}` -- single team stats
   - `GET /api/v1/stats/{sport}/players/{playerId}` -- single player stats
   - `GET /api/v1/stats/{sport}/games` -- game results
   - `GET /api/v1/stats/{sport}/schedule` -- upcoming games
   - `GET /api/v1/stats/{sport}/injuries` -- injury reports
10. Implement Redis pub/sub: publish `stats.updated` and `game.completed` events
11. Add OpenAPI spec generation (Echo swagger middleware or manual spec)
12. Write unit tests for normalization and derived stat computation
13. Write integration tests against real Redis (testcontainers)
14. Create Dockerfile (multi-stage build) and `.air.toml` for hot reload
15. Add `.env.example` with all service-specific environment variables

**Phase 6 additions (sport expansion):**
16. Implement NFL adapter using nfl_data_py
17. Implement MLB adapter using pybaseball
18. Implement NCAA Basketball adapter
19. Implement NCAA Football adapter using CFBD API
20. Implement NCAA Baseball adapter

## Dependencies
- **infra-ops** must provide Docker Compose with Redis running
- No other service dependencies (statistics-service has no upstream services)

## Complexity
**XL** -- Multiple external API integrations with different data formats, derived stat computation, caching strategy, and eventual 6-sport support. The Go ↔ Python bridge for sport data packages (nba_api, nfl_data_py, pybaseball) adds architectural complexity.

## Definition of Done
- [ ] `GET /api/v1/stats/nba/teams` returns current NBA team statistics
- [ ] `GET /api/v1/stats/nba/schedule` returns upcoming NBA games
- [ ] `GET /api/v1/stats/nba/games` returns completed game results with scores
- [ ] Stats responses are cached in Redis (cache hit returns in < 10ms)
- [ ] Derived statistics (rolling averages, ratings) are computed correctly
- [ ] `stats.updated` and `game.completed` events are published to Redis
- [ ] Health check endpoint responds with service status
- [ ] OpenAPI spec is generated and valid
- [ ] Unit and integration tests pass
- [ ] Service starts cleanly in Docker Compose

## Key Documentation
- [Statistics Service Component](../bookie-breaker-docs/components/statistics-service.md)
- [Statistics Data Sources (ADR-008)](../bookie-breaker-docs/decisions/008-statistics-data-sources.md)
- [Data Flow Architecture](../bookie-breaker-docs/architecture/data-flow.md)
- [Feature Inventory: PIPE-001 through PIPE-007](../bookie-breaker-docs/architecture/feature-inventory.md)
