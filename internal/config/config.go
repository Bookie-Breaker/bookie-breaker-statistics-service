package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

type Config struct {
	Port                 int
	DatabaseURL          string
	RedisURL             string
	OTELExporterEndpoint string
	OTELServiceName      string
	LogLevel             string

	NBAStatsBaseURL       string
	NBAUserAgent          string
	NBAMinRequestInterval time.Duration
	NBAHTTPTimeout        time.Duration

	InjurySource string // espn | none
	ESPNBaseURL  string

	MLBStatsBaseURL string

	RefreshTeamStatsInterval time.Duration
	RefreshRostersInterval   time.Duration
	RefreshScheduleInterval  time.Duration
	RefreshInjuriesInterval  time.Duration
	ScoreboardPollInterval   time.Duration

	TTLTeams     time.Duration
	TTLTeamStats time.Duration
	TTLPlayers   time.Duration
	TTLGames     time.Duration
	TTLGameFinal time.Duration
	TTLInjuries  time.Duration
	TTLStale     time.Duration

	CircuitFailureThreshold int
	CircuitOpenDuration     time.Duration

	// CurrentSeason overrides the clock-derived season when > 0
	// (e.g. 2025 means the 2025-26 NBA season).
	CurrentSeason int

	// LeaguesEnabled lists the leagues this instance serves, from
	// LEAGUES_ENABLED (comma-separated). Default: NBA.
	LeaguesEnabled []model.League
}

func Load() (*Config, error) {
	leagues, err := parseLeagues(getEnv("LEAGUES_ENABLED", "NBA"))
	if err != nil {
		return nil, err
	}

	return &Config{
		Port:                 getEnvInt("PORT", 8002),
		DatabaseURL:          getEnv("DATABASE_URL", "postgres://statistics_svc:localdev@localhost:5432/bookiebreaker?search_path=public"),
		RedisURL:             getEnv("REDIS_URL", "redis://localhost:6379"),
		OTELExporterEndpoint: getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
		OTELServiceName:      getEnv("OTEL_SERVICE_NAME", "statistics-service"),
		LogLevel:             getEnv("LOG_LEVEL", "info"),

		NBAStatsBaseURL:       getEnv("NBA_STATS_BASE_URL", "https://stats.nba.com"),
		NBAUserAgent:          getEnv("NBA_USER_AGENT", defaultUserAgent),
		NBAMinRequestInterval: getEnvDuration("NBA_MIN_REQUEST_INTERVAL", 800*time.Millisecond),
		NBAHTTPTimeout:        getEnvDuration("NBA_HTTP_TIMEOUT", 30*time.Second),

		InjurySource: getEnv("INJURY_SOURCE", "espn"),
		ESPNBaseURL:  getEnv("ESPN_BASE_URL", "https://site.api.espn.com"),

		MLBStatsBaseURL: getEnv("MLB_STATS_BASE_URL", "https://statsapi.mlb.com/api/v1"),

		RefreshTeamStatsInterval: getEnvDuration("REFRESH_TEAM_STATS_INTERVAL", 6*time.Hour),
		RefreshRostersInterval:   getEnvDuration("REFRESH_ROSTERS_INTERVAL", 12*time.Hour),
		RefreshScheduleInterval:  getEnvDuration("REFRESH_SCHEDULE_INTERVAL", 24*time.Hour),
		RefreshInjuriesInterval:  getEnvDuration("REFRESH_INJURIES_INTERVAL", 4*time.Hour),
		ScoreboardPollInterval:   getEnvDuration("SCOREBOARD_POLL_INTERVAL", 5*time.Minute),

		TTLTeams:     getEnvDuration("TTL_TEAMS", 24*time.Hour),
		TTLTeamStats: getEnvDuration("TTL_TEAM_STATS", 6*time.Hour),
		TTLPlayers:   getEnvDuration("TTL_PLAYERS", 6*time.Hour),
		TTLGames:     getEnvDuration("TTL_GAMES", time.Hour),
		TTLGameFinal: getEnvDuration("TTL_GAME_FINAL", 24*time.Hour),
		TTLInjuries:  getEnvDuration("TTL_INJURIES", time.Hour),
		TTLStale:     getEnvDuration("TTL_STALE", 168*time.Hour),

		CircuitFailureThreshold: getEnvInt("CIRCUIT_FAILURE_THRESHOLD", 5),
		CircuitOpenDuration:     getEnvDuration("CIRCUIT_OPEN_DURATION", 5*time.Minute),

		CurrentSeason: getEnvInt("CURRENT_SEASON", 0),

		LeaguesEnabled: leagues,
	}, nil
}

const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// parseLeagues validates a comma-separated league list against the known
// league enum, failing fast on unknown values.
func parseLeagues(raw string) ([]model.League, error) {
	var leagues []model.League
	seen := make(map[model.League]bool)
	for _, part := range strings.Split(raw, ",") {
		name := strings.ToUpper(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		league := model.League(name)
		if _, ok := model.SportForLeague[league]; !ok {
			return nil, fmt.Errorf("unknown league %q in LEAGUES_ENABLED", strings.TrimSpace(part))
		}
		if !seen[league] {
			seen[league] = true
			leagues = append(leagues, league)
		}
	}
	if len(leagues) == 0 {
		return nil, fmt.Errorf("LEAGUES_ENABLED must name at least one league")
	}
	return leagues, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
