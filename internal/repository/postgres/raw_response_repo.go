package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

type RawResponseRepo struct {
	db *pgxpool.Pool
}

func NewRawResponseRepo(db *pgxpool.Pool) *RawResponseRepo {
	return &RawResponseRepo{db: db}
}

func (r *RawResponseRepo) Insert(ctx context.Context, resp model.RawAPIResponse) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO public.raw_api_responses (service, source, endpoint, http_status, request_body, response_body, captured_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		resp.Service, resp.Source, resp.Endpoint, resp.HTTPStatus, resp.RequestBody, resp.ResponseBody, resp.CapturedAt,
	)
	if err != nil {
		return fmt.Errorf("insert raw api response: %w", err)
	}
	return nil
}
