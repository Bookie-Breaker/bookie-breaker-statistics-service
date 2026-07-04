-- Minimal replica of the shared raw_api_responses archive (see
-- bookie-breaker-infra-ops/init-db/05-create-raw-api-responses.sql). The
-- service only INSERTs, so the TimescaleDB hypertable conversion is omitted.
CREATE TABLE public.raw_api_responses (
    id            UUID NOT NULL DEFAULT gen_random_uuid(),
    service       TEXT NOT NULL,
    source        TEXT NOT NULL,
    endpoint      TEXT NOT NULL,
    http_status   INTEGER NOT NULL,
    request_body  TEXT,
    response_body TEXT NOT NULL,
    captured_at   TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (id, captured_at)
);
