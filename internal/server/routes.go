package server

import (
	"github.com/labstack/echo/v4"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/handler"
)

func registerRoutes(e *echo.Echo, deps Deps) {
	healthHandler := handler.NewHealthHandler(deps.DB, deps.Redis, deps.Cache, deps.Upstreams)
	teamsHandler := handler.NewTeamsHandler(deps.Query)
	playersHandler := handler.NewPlayersHandler(deps.Query)
	gamesHandler := handler.NewGamesHandler(deps.Query)
	injuriesHandler := handler.NewInjuriesHandler(deps.Query)

	api := e.Group("/api/v1/stats")
	api.GET("/health", healthHandler.GetHealth)
	api.GET("/teams", teamsHandler.GetTeams)
	api.GET("/teams/:team_id", teamsHandler.GetTeamByID)
	api.GET("/teams/:team_id/stats", teamsHandler.GetTeamStats)
	api.GET("/players", playersHandler.GetPlayers)
	api.GET("/players/:player_id", playersHandler.GetPlayerByID)
	api.GET("/games", gamesHandler.GetGames)
	api.GET("/games/:game_id", gamesHandler.GetGameByID)
	api.GET("/games/:game_id/result", gamesHandler.GetGameResult)
	api.GET("/schedule", gamesHandler.GetSchedule)
	api.GET("/injuries", injuriesHandler.GetInjuries)
}
