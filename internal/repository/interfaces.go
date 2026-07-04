package repository

import (
	"context"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// RawResponseRepository archives external API responses. This is the only
// Postgres access the service has (role statistics_svc owns no schema).
type RawResponseRepository interface {
	Insert(ctx context.Context, resp model.RawAPIResponse) error
}
