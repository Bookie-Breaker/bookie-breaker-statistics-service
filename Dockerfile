FROM golang:1.25-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /statistics-service ./cmd/server

FROM alpine:3.22

RUN adduser -D -g '' appuser
USER appuser

COPY --from=builder /statistics-service /statistics-service

EXPOSE 8002

HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
  CMD wget -qO- http://localhost:8002/api/v1/stats/health || exit 1

ENTRYPOINT ["/statistics-service"]
