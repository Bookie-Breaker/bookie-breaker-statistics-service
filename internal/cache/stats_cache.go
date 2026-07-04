package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// TTLs configures per-data-type expirations.
type TTLs struct {
	Teams     time.Duration
	TeamStats time.Duration
	Players   time.Duration
	Games     time.Duration
	Injuries  time.Duration
	Stale     time.Duration
}

// StatsCache is the Redis-backed store for all canonical statistics data.
// The service owns no Postgres tables; this cache is its primary store,
// refreshed on a schedule and stale-served when upstreams are down.
type StatsCache struct {
	rdb  *redis.Client
	ttls TTLs
}

// NewStatsCache creates a stats cache.
func NewStatsCache(rdb *redis.Client, ttls TTLs) *StatsCache {
	return &StatsCache{rdb: rdb, ttls: ttls}
}

func (c *StatsCache) set(ctx context.Context, key string, v any, ttl time.Duration) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", key, err)
	}
	if err := c.rdb.Set(ctx, key, data, ttl).Err(); err != nil {
		return fmt.Errorf("set %s: %w", key, err)
	}
	// Stale mirror: last-known-good copy served when the upstream is down.
	if err := c.rdb.Set(ctx, stale(key), data, c.ttls.Stale).Err(); err != nil {
		return fmt.Errorf("set %s: %w", stale(key), err)
	}
	return nil
}

// get unmarshals key into v. When allowStale is true and the primary key is
// gone, the stale mirror is tried. Returns false when neither key exists.
func (c *StatsCache) get(ctx context.Context, key string, v any, allowStale bool) (bool, error) {
	data, err := c.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil && allowStale {
		data, err = c.rdb.Get(ctx, stale(key)).Bytes()
	}
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get %s: %w", key, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("unmarshal %s: %w", key, err)
	}
	return true, nil
}

func (c *StatsCache) SetTeams(ctx context.Context, league string, teams []model.TeamSummary) error {
	return c.set(ctx, keyTeams(league), teams, c.ttls.Teams)
}

func (c *StatsCache) GetTeams(ctx context.Context, league string, allowStale bool) ([]model.TeamSummary, bool, error) {
	var teams []model.TeamSummary
	ok, err := c.get(ctx, keyTeams(league), &teams, allowStale)
	return teams, ok, err
}

// SetTeamDetails stores team details (with venue) keyed by canonical id.
func (c *StatsCache) SetTeamDetails(ctx context.Context, league string, details map[string]model.TeamDetail) error {
	return c.set(ctx, keyTeamDetails(league), details, c.ttls.Teams)
}

func (c *StatsCache) GetTeamDetails(ctx context.Context, league string, allowStale bool) (map[string]model.TeamDetail, bool, error) {
	var details map[string]model.TeamDetail
	ok, err := c.get(ctx, keyTeamDetails(league), &details, allowStale)
	return details, ok, err
}

// SetTeamStats stores the whole league's stats keyed by canonical team id.
func (c *StatsCache) SetTeamStats(ctx context.Context, league string, season, window int, stats map[string]model.TeamStats) error {
	return c.set(ctx, keyTeamStats(league, season, window), stats, c.ttls.TeamStats)
}

func (c *StatsCache) GetTeamStats(ctx context.Context, league string, season, window int, allowStale bool) (map[string]model.TeamStats, bool, error) {
	var stats map[string]model.TeamStats
	ok, err := c.get(ctx, keyTeamStats(league, season, window), &stats, allowStale)
	return stats, ok, err
}

func (c *StatsCache) SetPlayers(ctx context.Context, league string, players []model.PlayerSummary) error {
	return c.set(ctx, keyPlayers(league), players, c.ttls.Players)
}

func (c *StatsCache) GetPlayers(ctx context.Context, league string, allowStale bool) ([]model.PlayerSummary, bool, error) {
	var players []model.PlayerSummary
	ok, err := c.get(ctx, keyPlayers(league), &players, allowStale)
	return players, ok, err
}

func (c *StatsCache) SetPlayerDetails(ctx context.Context, league string, details map[string]model.PlayerDetail) error {
	return c.set(ctx, keyPlayerDetails(league), details, c.ttls.Players)
}

func (c *StatsCache) GetPlayerDetails(ctx context.Context, league string, allowStale bool) (map[string]model.PlayerDetail, bool, error) {
	var details map[string]model.PlayerDetail
	ok, err := c.get(ctx, keyPlayerDetails(league), &details, allowStale)
	return details, ok, err
}

func (c *StatsCache) SetGames(ctx context.Context, league string, season int, games []model.Game) error {
	return c.set(ctx, keyGames(league, season), games, c.ttls.Games)
}

func (c *StatsCache) GetGames(ctx context.Context, league string, season int, allowStale bool) ([]model.Game, bool, error) {
	var games []model.Game
	ok, err := c.get(ctx, keyGames(league, season), &games, allowStale)
	return games, ok, err
}

func (c *StatsCache) SetInjuries(ctx context.Context, league string, injuries []model.InjuryReport) error {
	return c.set(ctx, keyInjuries(league), injuries, c.ttls.Injuries)
}

func (c *StatsCache) GetInjuries(ctx context.Context, league string, allowStale bool) ([]model.InjuryReport, bool, error) {
	var injuries []model.InjuryReport
	ok, err := c.get(ctx, keyInjuries(league), &injuries, allowStale)
	return injuries, ok, err
}

func (c *StatsCache) SetGameLog(ctx context.Context, playerID string, season int, log []model.PlayerGameLine) error {
	return c.set(ctx, keyGameLog(playerID, season), log, c.ttls.Players)
}

func (c *StatsCache) GetGameLog(ctx context.Context, playerID string, season int) ([]model.PlayerGameLine, bool, error) {
	var log []model.PlayerGameLine
	ok, err := c.get(ctx, keyGameLog(playerID, season), &log, false)
	return log, ok, err
}

// MarkGameCompleted records that a game.completed event was published so
// restarts do not re-publish. Returns true when this call set the marker
// (i.e. the event was not yet published).
func (c *StatsCache) MarkGameCompleted(ctx context.Context, gameID string) (bool, error) {
	return c.rdb.SetNX(ctx, keyGameCompleted(gameID), time.Now().UTC().Format(time.RFC3339), 48*time.Hour).Result()
}

// SetLastSuccess records a successful upstream fetch for health reporting.
func (c *StatsCache) SetLastSuccess(ctx context.Context, source string) error {
	return c.rdb.Set(ctx, keyLastSuccess(source), time.Now().UTC().Format(time.RFC3339), 0).Err()
}

// GetLastSuccess returns the time of the last successful upstream fetch.
func (c *StatsCache) GetLastSuccess(ctx context.Context, source string) (time.Time, bool, error) {
	v, err := c.rdb.Get(ctx, keyLastSuccess(source)).Result()
	if err == redis.Nil {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, false, nil //nolint:nilerr // unparseable marker means no recorded success
	}
	return t, true, nil
}
