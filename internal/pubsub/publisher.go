package pubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	channelStatsUpdated  = "events:stats.updated"
	channelGameCompleted = "events:game.completed"
)

// StatsChange records one field transition inside a stats.updated event.
type StatsChange struct {
	PlayerID string `json:"player_id,omitempty"`
	Field    string `json:"field"`
	Old      string `json:"old"`
	New      string `json:"new"`
}

// StatsUpdatedEvent matches the payload schema in redis-schemas.md.
type StatsUpdatedEvent struct {
	Event      string        `json:"event"`
	Timestamp  string        `json:"timestamp"`
	League     string        `json:"league"`
	UpdateType string        `json:"update_type"`
	TeamIDs    []string      `json:"team_ids,omitempty"`
	PlayerIDs  []string      `json:"player_ids,omitempty"`
	Changes    []StatsChange `json:"changes,omitempty"`
}

// GameCompletedEvent matches the payload schema in redis-schemas.md.
// Regulation scores are optional (ADR-027): they are set only by leagues
// whose markets settle on regulation time, so payloads for all other
// leagues are byte-identical to the pre-ADR-027 contract.
type GameCompletedEvent struct {
	Event               string `json:"event"`
	Timestamp           string `json:"timestamp"`
	GameID              string `json:"game_id"`
	GameExternalID      string `json:"game_external_id"`
	League              string `json:"league"`
	HomeTeam            string `json:"home_team"`
	AwayTeam            string `json:"away_team"`
	HomeScore           int    `json:"home_score"`
	AwayScore           int    `json:"away_score"`
	Total               int    `json:"total"`
	Margin              int    `json:"margin"`
	Overtime            bool   `json:"overtime"`
	RegulationHomeScore *int   `json:"regulation_home_score,omitempty"`
	RegulationAwayScore *int   `json:"regulation_away_score,omitempty"`
}

// Publisher publishes events to Redis pub/sub channels.
type Publisher struct {
	rdb *redis.Client
}

// NewPublisher creates a new Redis pub/sub publisher.
func NewPublisher(rdb *redis.Client) *Publisher {
	return &Publisher{rdb: rdb}
}

// PublishStatsUpdated publishes a stats.updated event.
func (p *Publisher) PublishStatsUpdated(ctx context.Context, event StatsUpdatedEvent) error {
	event.Event = "stats.updated"
	event.Timestamp = time.Now().UTC().Format(time.RFC3339)

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal stats.updated event: %w", err)
	}
	if err := p.rdb.Publish(ctx, channelStatsUpdated, payload).Err(); err != nil {
		return fmt.Errorf("publish to %s: %w", channelStatsUpdated, err)
	}

	slog.Info("published stats.updated event", "league", event.League, "update_type", event.UpdateType)
	return nil
}

// PublishGameCompleted publishes a game.completed event.
func (p *Publisher) PublishGameCompleted(ctx context.Context, event GameCompletedEvent) error {
	event.Event = "game.completed"
	event.Timestamp = time.Now().UTC().Format(time.RFC3339)

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal game.completed event: %w", err)
	}
	if err := p.rdb.Publish(ctx, channelGameCompleted, payload).Err(); err != nil {
		return fmt.Errorf("publish to %s: %w", channelGameCompleted, err)
	}

	slog.Info("published game.completed event",
		"game_id", event.GameID,
		"home", event.HomeTeam, "away", event.AwayTeam,
		"score", fmt.Sprintf("%d-%d", event.HomeScore, event.AwayScore),
	)
	return nil
}
