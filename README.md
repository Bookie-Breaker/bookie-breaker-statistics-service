# bookie-breaker-statistics-service

[![CI](https://img.shields.io/github/actions/workflow/status/Bookie-Breaker/bookie-breaker-statistics-service/ci.yml?branch=main&label=CI&logo=githubactions&logoColor=white)](https://github.com/Bookie-Breaker/bookie-breaker-statistics-service/actions/workflows/ci.yml)
[![coverage](https://img.shields.io/codecov/c/github/Bookie-Breaker/bookie-breaker-statistics-service?logo=codecov&logoColor=white)](https://app.codecov.io/gh/Bookie-Breaker/bookie-breaker-statistics-service)
![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)
![Echo](https://img.shields.io/badge/Echo-v4-1D9BF0)
![Redis](https://img.shields.io/badge/Redis-7-DC382D?logo=redis&logoColor=white)
![Docker](https://img.shields.io/badge/Docker-ready-2496ED?logo=docker&logoColor=white)

Serves team/player statistics, schedules, game results, and injuries with Redis as the primary store — Postgres is
used for archival only (raw API responses into `public.raw_api_responses`). Sources stats from stats.nba.com and
injuries from ESPN, and publishes `events:stats.updated` when stat or injury data changes. Which leagues are active
is gated by `LEAGUES_ENABLED`.

Operational runbooks live in the
[daily operations playbook](https://github.com/Bookie-Breaker/bookie-breaker-docs/blob/main/playbooks/02-daily-operations.md).

## Quickstart

### With Docker Compose (recommended)

```bash
task up # from BookieBreaker/ root
```

### Standalone

```bash
cp .env.example .env # fill in values
task bootstrap
task dev
```

## API

The service listens on port 8002 with base path `/api/v1/stats`; health is at `/api/v1/stats/health`.
Interactive docs are not served — see the
[statistics-service API contract](https://github.com/Bookie-Breaker/bookie-breaker-docs/blob/main/api-contracts/statistics-service-api.md)
for the full endpoint reference.

## Architecture Decisions

- [Statistics Data Sources (ADR-008)](https://github.com/Bookie-Breaker/bookie-breaker-docs/blob/main/decisions/008-statistics-data-sources.md)
- [Tech Stack Selection (ADR-010)](https://github.com/Bookie-Breaker/bookie-breaker-docs/blob/main/decisions/010-tech-stack-selection.md)
- [Statistics Data Bridge (ADR-020)](https://github.com/Bookie-Breaker/bookie-breaker-docs/blob/main/decisions/020-statistics-data-bridge.md)
- [Sport Expansion Scope and Data Sources (ADR-026)](https://github.com/Bookie-Breaker/bookie-breaker-docs/blob/main/decisions/026-sport-expansion-scope-and-data-sources.md)

## Environment Variables

See `.env.example` for all variables with descriptions. Notes: `CFBD_API_KEY` and `CBBD_API_KEY` are only needed
when NCAA leagues are enabled, and `INJURY_SOURCE` selects the injury provider (`espn` or `none`).
