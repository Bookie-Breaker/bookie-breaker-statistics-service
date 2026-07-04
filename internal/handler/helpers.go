package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/service"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

func parseLimit(c echo.Context) int {
	raw := c.QueryParam("limit")
	if raw == "" {
		return defaultLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

func parseIntParam(c echo.Context, name string) int {
	n, err := strconv.Atoi(c.QueryParam(name))
	if err != nil {
		return 0
	}
	return n
}

func splitCSV(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// emptyToSlice keeps empty results serializing as [] instead of null.
func emptyToSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func mapError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrNotFound):
		return ErrorResponse(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, service.ErrBadRequest):
		return ErrorResponse(c, http.StatusBadRequest, "INVALID_PARAMETER", err.Error())
	default:
		c.Logger().Error(err)
		return ErrorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}
