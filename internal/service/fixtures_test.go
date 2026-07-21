package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/espn"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/pubsub"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/tests/testutil"
)

const testSeason = 2025

// fakeProvider is a configurable in-memory StatsProvider. Every fetch
// returns one archival Fetch (also on error, matching the provider
// contract) and records the call for assertions.
type fakeProvider struct {
	league model.League

	mu    sync.Mutex
	calls map[string]int
	errs  map[string]error

	teams         []model.TeamSummary
	teamDetails   map[string]model.TeamDetail
	teamStats     map[string]model.TeamStats
	windowStats   map[string]model.TeamStats
	players       []model.PlayerSummary
	playerDetails map[string]model.PlayerDetail
	schedule      []model.Game
	scoreboard    map[string]sportsdata.ScoreboardUpdate
	gameLog       []model.PlayerGameLine
	boxScore      *model.BoxScore
}

func newFakeProvider(league model.League) *fakeProvider {
	return &fakeProvider{
		league: league,
		calls:  make(map[string]int),
		errs:   make(map[string]error),
	}
}

func (p *fakeProvider) record(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[name]++
	return p.errs[name]
}

func (p *fakeProvider) callCount(name string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[name]
}

func (p *fakeProvider) fail(name string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errs[name] = err
}

func (p *fakeProvider) fetch(endpoint string) []*sportsdata.Fetch {
	return []*sportsdata.Fetch{{
		Endpoint:   endpoint,
		Body:       []byte(`{"fake":true}`),
		HTTPStatus: 200,
		CapturedAt: time.Now().UTC(),
	}}
}

func (p *fakeProvider) League() model.League { return p.league }

func (p *fakeProvider) Source() string { return "fake_" + strings.ToLower(string(p.league)) }

func (p *fakeProvider) SeasonYear(time.Time) int { return testSeason }

func (p *fakeProvider) Teams(context.Context) ([]model.TeamSummary, map[string]model.TeamDetail, []*sportsdata.Fetch, error) {
	if err := p.record("teams"); err != nil {
		return nil, nil, p.fetch("/teams"), err
	}
	return p.teams, p.teamDetails, p.fetch("/teams"), nil
}

func (p *fakeProvider) TeamStats(_ context.Context, _, window int) (map[string]model.TeamStats, []*sportsdata.Fetch, error) {
	if window > 0 {
		if err := p.record("teamstats_window"); err != nil {
			return nil, p.fetch("/teamstats"), err
		}
		return p.windowStats, p.fetch("/teamstats"), nil
	}
	if err := p.record("teamstats"); err != nil {
		return nil, p.fetch("/teamstats"), err
	}
	return p.teamStats, p.fetch("/teamstats"), nil
}

func (p *fakeProvider) Players(context.Context, int) ([]model.PlayerSummary, map[string]model.PlayerDetail, []*sportsdata.Fetch, error) {
	if err := p.record("players"); err != nil {
		return nil, nil, p.fetch("/players"), err
	}
	// Return copies so cache-coupled mutation (injury folding, sorting)
	// cannot leak back into the fixture between refreshes.
	players := make([]model.PlayerSummary, len(p.players))
	copy(players, p.players)
	details := make(map[string]model.PlayerDetail, len(p.playerDetails))
	for k, v := range p.playerDetails {
		details[k] = v
	}
	return players, details, p.fetch("/players"), nil
}

func (p *fakeProvider) Schedule(context.Context, int) ([]model.Game, []*sportsdata.Fetch, error) {
	if err := p.record("schedule"); err != nil {
		return nil, p.fetch("/schedule"), err
	}
	games := make([]model.Game, len(p.schedule))
	copy(games, p.schedule)
	return games, p.fetch("/schedule"), nil
}

func (p *fakeProvider) Scoreboard(context.Context, time.Time) (map[string]sportsdata.ScoreboardUpdate, []*sportsdata.Fetch, error) {
	if err := p.record("scoreboard"); err != nil {
		return nil, p.fetch("/scoreboard"), err
	}
	return p.scoreboard, p.fetch("/scoreboard"), nil
}

func (p *fakeProvider) PlayerGameLog(context.Context, model.PlayerDetail, int) ([]model.PlayerGameLine, []*sportsdata.Fetch, error) {
	if err := p.record("gamelog"); err != nil {
		return nil, p.fetch("/gamelog"), err
	}
	return p.gameLog, p.fetch("/gamelog"), nil
}

func (p *fakeProvider) BoxScore(context.Context, string) (*model.BoxScore, []*sportsdata.Fetch, error) {
	if err := p.record("boxscore"); err != nil {
		return nil, p.fetch("/boxscore"), err
	}
	box := *p.boxScore
	return &box, p.fetch("/boxscore"), nil
}

// fakeRawRepo records archived raw responses.
type fakeRawRepo struct {
	mu       sync.Mutex
	inserted []model.RawAPIResponse
	err      error
}

func (r *fakeRawRepo) Insert(_ context.Context, resp model.RawAPIResponse) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.inserted = append(r.inserted, resp)
	return nil
}

func (r *fakeRawRepo) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.inserted)
}

func (r *fakeRawRepo) last() model.RawAPIResponse {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inserted[len(r.inserted)-1]
}

// fixture wires a full service stack over a real Redis: fake provider,
// recording raw repo, real cache and publisher.
type fixture struct {
	rdb       *redis.Client
	cache     *cache.StatsCache
	provider  *fakeProvider
	rawRepo   *fakeRawRepo
	publisher *pubsub.Publisher
	refresh   *RefreshService
	query     *QueryService
	watcher   *GameWatcher
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWithInjuries(t, nil)
}

func newFixtureWithInjuries(t *testing.T, espnInjuries map[model.League]*espn.Client) *fixture {
	t.Helper()

	rdb := testutil.RedisClient(t)
	statsCache := cache.NewStatsCache(rdb, cache.TTLs{
		Teams:     time.Hour,
		TeamStats: time.Hour,
		Players:   time.Hour,
		Games:     time.Hour,
		Injuries:  time.Hour,
		BoxScore:  time.Hour,
		Stale:     24 * time.Hour,
	})
	provider := newFakeProvider(model.LeagueNBA)
	rawRepo := &fakeRawRepo{}
	publisher := pubsub.NewPublisher(rdb)

	refresh := NewRefreshService(
		map[model.League]sportsdata.StatsProvider{model.LeagueNBA: provider},
		espnInjuries,
		statsCache,
		rawRepo,
		publisher,
	)
	query := NewQueryService(statsCache, refresh, []model.League{model.LeagueNBA}, func() int { return testSeason })
	watcher := NewGameWatcher(refresh, statsCache, publisher)

	return &fixture{
		rdb:       rdb,
		cache:     statsCache,
		provider:  provider,
		rawRepo:   rawRepo,
		publisher: publisher,
		refresh:   refresh,
		query:     query,
		watcher:   watcher,
	}
}

// subscribe opens a confirmed subscription so publishes cannot race it.
func (f *fixture) subscribe(t *testing.T, channel string) <-chan string {
	t.Helper()
	sub := f.rdb.Subscribe(context.Background(), channel)
	t.Cleanup(func() { _ = sub.Close() })
	if _, err := sub.Receive(context.Background()); err != nil {
		t.Fatalf("confirm subscription: %v", err)
	}

	out := make(chan string, 8)
	go func() {
		ch := sub.Channel()
		for msg := range ch {
			out <- msg.Payload
		}
	}()
	return out
}

func waitForEvent(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case payload := <-ch:
		return payload
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event")
		return ""
	}
}

func assertNoEvent(t *testing.T, ch <-chan string) {
	t.Helper()
	select {
	case payload := <-ch:
		t.Fatalf("unexpected event published: %s", payload)
	case <-time.After(300 * time.Millisecond):
	}
}

// Shared fixture data used across query and refresh tests.

func sampleTeams() []model.TeamSummary {
	return []model.TeamSummary{
		{ID: "team-lal", League: model.LeagueNBA, Name: "Los Angeles Lakers", Abbreviation: "LAL", Conference: "Western", Division: "Pacific", Active: true},
		{ID: "team-bos", League: model.LeagueNBA, Name: "Boston Celtics", Abbreviation: "BOS", Conference: "Eastern", Division: "Atlantic", Active: true},
		{ID: "team-old", League: model.LeagueNBA, Name: "Defunct Franchise", Abbreviation: "DEF", Conference: "Eastern", Division: "Atlantic", Active: false},
	}
}

func sampleTeamDetails() map[string]model.TeamDetail {
	details := make(map[string]model.TeamDetail)
	for _, t := range sampleTeams() {
		details[t.ID] = model.TeamDetail{TeamSummary: t}
	}
	return details
}

func sampleTeamStats() map[string]model.TeamStats {
	return map[string]model.TeamStats{
		"team-lal": {
			TeamID: "team-lal", TeamAbbreviation: "LAL", Season: testSeason,
			Wins: 30, Losses: 10,
			Stats: model.StatBlocks{
				Offensive: &model.OffensiveStats{PointsPerGame: 115, OffensiveRating: 118},
				Defensive: &model.DefensiveStats{PointsAllowedPerGame: 108, DefensiveRating: 110},
				Advanced:  &model.AdvancedStats{NetRating: 8},
			},
			HomeAwaySplits: &model.HomeAwaySplits{Home: &model.SplitRecord{Wins: 18}},
		},
	}
}

func samplePlayers() []model.PlayerSummary {
	return []model.PlayerSummary{
		{ID: "player-lbj", TeamID: "team-lal", TeamAbbreviation: "LAL", FirstName: "LeBron", LastName: "James", Position: "F", Status: model.PlayerActive, League: model.LeagueNBA},
		{ID: "player-jt", TeamID: "team-bos", TeamAbbreviation: "BOS", FirstName: "Jayson", LastName: "Tatum", Position: "F", Status: model.PlayerActive, League: model.LeagueNBA},
	}
}

func samplePlayerDetails() map[string]model.PlayerDetail {
	details := make(map[string]model.PlayerDetail)
	for _, p := range samplePlayers() {
		details[p.ID] = model.PlayerDetail{PlayerSummary: p}
	}
	return details
}

func sampleGames(start time.Time) []model.Game {
	return []model.Game{
		{
			ID: "game-1", League: model.LeagueNBA, ExternalID: "ext-1",
			HomeTeam:       model.TeamRef{ID: "team-lal", Abbreviation: "LAL"},
			AwayTeam:       model.TeamRef{ID: "team-bos", Abbreviation: "BOS"},
			ScheduledStart: start, Status: model.GameScheduled, Season: testSeason,
		},
		{
			ID: "game-2", League: model.LeagueNBA, ExternalID: "ext-2",
			HomeTeam:       model.TeamRef{ID: "team-bos", Abbreviation: "BOS"},
			AwayTeam:       model.TeamRef{ID: "team-lal", Abbreviation: "LAL"},
			ScheduledStart: start.Add(48 * time.Hour), Status: model.GameScheduled, Season: testSeason,
		},
	}
}
