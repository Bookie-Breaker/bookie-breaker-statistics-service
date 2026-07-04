package handler

import (
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/service"
)

// TeamsHandler serves the team endpoints.
type TeamsHandler struct {
	query *service.QueryService
}

// NewTeamsHandler creates a teams handler.
func NewTeamsHandler(query *service.QueryService) *TeamsHandler {
	return &TeamsHandler{query: query}
}

// GetTeams handles GET /api/v1/stats/teams.
func (h *TeamsHandler) GetTeams(c echo.Context) error {
	filters := service.TeamFilters{
		Leagues:    splitCSV(c.QueryParam("league")),
		Conference: c.QueryParam("conference"),
		Division:   c.QueryParam("division"),
		Limit:      parseLimit(c),
		Cursor:     c.QueryParam("cursor"),
	}
	if raw := c.QueryParam("active"); raw != "" {
		if active, err := strconv.ParseBool(raw); err == nil {
			filters.Active = &active
		}
	}

	teams, hasMore, next, err := h.query.Teams(c.Request().Context(), filters)
	if err != nil {
		return mapError(c, err)
	}
	return PaginatedResponse(c, emptyToSlice(teams), filters.Limit, hasMore, next)
}

// GetTeamByID handles GET /api/v1/stats/teams/{team_id}.
func (h *TeamsHandler) GetTeamByID(c echo.Context) error {
	team, err := h.query.TeamByID(c.Request().Context(), c.Param("team_id"))
	if err != nil {
		return mapError(c, err)
	}
	return SuccessResponse(c, team)
}

// GetTeamStats handles GET /api/v1/stats/teams/{team_id}/stats.
func (h *TeamsHandler) GetTeamStats(c echo.Context) error {
	statType := model.StatType(c.QueryParam("stat_type"))
	if statType == "" {
		statType = model.StatAll
	}

	stats, err := h.query.TeamStats(
		c.Request().Context(),
		c.Param("team_id"),
		parseIntParam(c, "season"),
		statType,
		parseIntParam(c, "rolling_window"),
	)
	if err != nil {
		return mapError(c, err)
	}
	return SuccessResponse(c, stats)
}
