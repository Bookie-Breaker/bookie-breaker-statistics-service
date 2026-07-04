package handler

import (
	"github.com/labstack/echo/v4"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/service"
)

// GamesHandler serves the game and schedule endpoints.
type GamesHandler struct {
	query *service.QueryService
}

// NewGamesHandler creates a games handler.
func NewGamesHandler(query *service.QueryService) *GamesHandler {
	return &GamesHandler{query: query}
}

// GetGames handles GET /api/v1/stats/games.
func (h *GamesHandler) GetGames(c echo.Context) error {
	filters := service.GameFilters{
		League:   c.QueryParam("league"),
		DateFrom: c.QueryParam("date_from"),
		DateTo:   c.QueryParam("date_to"),
		Team:     c.QueryParam("team"),
		Statuses: splitCSV(c.QueryParam("status")),
		Season:   parseIntParam(c, "season"),
		Limit:    parseLimit(c),
		Cursor:   c.QueryParam("cursor"),
	}

	games, hasMore, next, err := h.query.Games(c.Request().Context(), filters)
	if err != nil {
		return mapError(c, err)
	}
	return PaginatedResponse(c, emptyToSlice(games), filters.Limit, hasMore, next)
}

// GetGameByID handles GET /api/v1/stats/games/{game_id}.
func (h *GamesHandler) GetGameByID(c echo.Context) error {
	game, err := h.query.GameByID(c.Request().Context(), c.Param("game_id"))
	if err != nil {
		return mapError(c, err)
	}
	return SuccessResponse(c, game)
}

// GetGameResult handles GET /api/v1/stats/games/{game_id}/result.
func (h *GamesHandler) GetGameResult(c echo.Context) error {
	result, err := h.query.GameResult(c.Request().Context(), c.Param("game_id"))
	if err != nil {
		return mapError(c, err)
	}
	return SuccessResponse(c, result)
}

// GetSchedule handles GET /api/v1/stats/schedule.
func (h *GamesHandler) GetSchedule(c echo.Context) error {
	filters := service.GameFilters{
		League:   c.QueryParam("league"),
		DateFrom: c.QueryParam("date_from"),
		DateTo:   c.QueryParam("date_to"),
		Team:     c.QueryParam("team_id"),
		Limit:    parseLimit(c),
		Cursor:   c.QueryParam("cursor"),
	}

	games, hasMore, next, err := h.query.Games(c.Request().Context(), filters)
	if err != nil {
		return mapError(c, err)
	}
	return PaginatedResponse(c, emptyToSlice(games), filters.Limit, hasMore, next)
}
