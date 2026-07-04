package handler

import (
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/service"
)

// PlayersHandler serves the player endpoints.
type PlayersHandler struct {
	query *service.QueryService
}

// NewPlayersHandler creates a players handler.
func NewPlayersHandler(query *service.QueryService) *PlayersHandler {
	return &PlayersHandler{query: query}
}

// GetPlayers handles GET /api/v1/stats/players.
func (h *PlayersHandler) GetPlayers(c echo.Context) error {
	filters := service.PlayerFilters{
		TeamID:   c.QueryParam("team_id"),
		League:   c.QueryParam("league"),
		Position: c.QueryParam("position"),
		Status:   c.QueryParam("status"),
		Limit:    parseLimit(c),
		Cursor:   c.QueryParam("cursor"),
	}

	players, hasMore, next, err := h.query.Players(c.Request().Context(), filters)
	if err != nil {
		return mapError(c, err)
	}
	return PaginatedResponse(c, emptyToSlice(players), filters.Limit, hasMore, next)
}

// GetPlayerByID handles GET /api/v1/stats/players/{player_id}.
func (h *PlayersHandler) GetPlayerByID(c echo.Context) error {
	gameLog, _ := strconv.ParseBool(c.QueryParam("game_log"))

	player, err := h.query.PlayerByID(c.Request().Context(), c.Param("player_id"), gameLog)
	if err != nil {
		return mapError(c, err)
	}
	return SuccessResponse(c, player)
}
