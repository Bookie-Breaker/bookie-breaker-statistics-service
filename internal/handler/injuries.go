package handler

import (
	"github.com/labstack/echo/v4"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/service"
)

// InjuriesHandler serves the injuries endpoint.
type InjuriesHandler struct {
	query *service.QueryService
}

// NewInjuriesHandler creates an injuries handler.
func NewInjuriesHandler(query *service.QueryService) *InjuriesHandler {
	return &InjuriesHandler{query: query}
}

// GetInjuries handles GET /api/v1/stats/injuries.
func (h *InjuriesHandler) GetInjuries(c echo.Context) error {
	injuries, err := h.query.Injuries(
		c.Request().Context(),
		c.QueryParam("league"),
		c.QueryParam("team_id"),
		c.QueryParam("status"),
	)
	if err != nil {
		return mapError(c, err)
	}
	return SuccessResponse(c, emptyToSlice(injuries))
}
