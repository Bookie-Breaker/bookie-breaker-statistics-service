package mlb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// sourceMLB is the raw_api_responses source label for the MLB StatsAPI.
const sourceMLB = "mlb_statsapi"

var _ sportsdata.StatsProvider = (*Provider)(nil)

// Provider implements sportsdata.StatsProvider for MLB on the official MLB
// StatsAPI (ADR-026).
type Provider struct {
	client *Client
}

// NewProvider creates the MLB provider.
func NewProvider(client *Client) *Provider {
	return &Provider{client: client}
}

// League identifies the league this provider serves.
func (p *Provider) League() model.League { return model.LeagueMLB }

// Source is the raw_api_responses source label.
func (p *Provider) Source() string { return sourceMLB }

// SeasonYear derives the current season year. An MLB season (spring training
// through the World Series, February–November) sits wholly inside one
// calendar year, so the season year is simply the calendar year.
func (p *Provider) SeasonYear(now time.Time) int { return now.UTC().Year() }

// Teams fetches and normalizes the season's team list with home venues.
func (p *Provider) Teams(ctx context.Context) ([]model.TeamSummary, map[string]model.TeamDetail, []*sportsdata.Fetch, error) {
	resp, fetch, err := p.client.Teams(ctx, p.SeasonYear(time.Now()))
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, nil, fetches, err
	}
	summaries, details := normalizeTeams(resp)
	return summaries, details, fetches, nil
}

// TeamStats builds full-season team stats from four league-wide calls:
// hitting, pitching, the reliever-only split behind bullpen_era, and the
// standings for W/L records. Rolling windows are not supported for MLB in
// this wave. The bullpen split degrades to the team ERA (the normalizer's
// documented fallback), never fatal.
func (p *Provider) TeamStats(ctx context.Context, seasonYear, window int) (map[string]model.TeamStats, []*sportsdata.Fetch, error) {
	if window > 0 {
		return nil, nil, errors.New("rolling-window stats are unsupported for MLB")
	}

	var fetches []*sportsdata.Fetch
	appendFetch := func(f *sportsdata.Fetch) {
		if f != nil {
			fetches = append(fetches, f)
		}
	}

	hitting, fetch, err := p.client.TeamStats(ctx, seasonYear, "hitting")
	appendFetch(fetch)
	if err != nil {
		return nil, fetches, fmt.Errorf("fetch hitting stats: %w", err)
	}
	pitching, fetch, err := p.client.TeamStats(ctx, seasonYear, "pitching")
	appendFetch(fetch)
	if err != nil {
		return nil, fetches, fmt.Errorf("fetch pitching stats: %w", err)
	}
	standings, fetch, err := p.client.Standings(ctx, seasonYear)
	appendFetch(fetch)
	if err != nil {
		return nil, fetches, fmt.Errorf("fetch standings: %w", err)
	}

	bullpen, fetch, err := p.client.BullpenStats(ctx, seasonYear)
	appendFetch(fetch)
	if err != nil {
		slog.Warn("bullpen split unavailable, bullpen_era falls back to team_era", "error", err)
		bullpen = nil
	}

	abbrevByID, teamFetches := p.teamAbbreviations(ctx, seasonYear)
	fetches = append(fetches, teamFetches...)

	return normalizeTeamStats(seasonYear, hitting, pitching, bullpen, standings, abbrevByID), fetches, nil
}

// teamAbbreviations fetches the team list for the abbreviation lookup the
// stats endpoints lack. Failures degrade to empty abbreviations, never fatal.
func (p *Provider) teamAbbreviations(ctx context.Context, seasonYear int) (map[int]string, []*sportsdata.Fetch) {
	resp, fetch, err := p.client.Teams(ctx, seasonYear)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		slog.Warn("team list unavailable for abbreviations, continuing without them", "error", err)
		return map[int]string{}, fetches
	}
	abbrevs := make(map[int]string, len(resp.Teams))
	for _, t := range resp.Teams {
		abbrevs[t.ID] = t.Abbreviation
	}
	return abbrevs, fetches
}

// Schedule fetches the season schedule in one season-long call (accepted by
// the live API; verified 2026-07-05), then embeds season pitching stats for
// each upcoming game's announced probable starters. Pitcher lookups are
// cached per refresh cycle, and only games that have not started need them —
// past finals keep name and external id without a stats call. Postponed
// originals that already have a makeup game (same gamePk under a new date,
// flagged by rescheduleDate) are skipped so each game normalizes once.
func (p *Provider) Schedule(ctx context.Context, seasonYear int) ([]model.Game, []*sportsdata.Fetch, error) {
	resp, fetch, err := p.client.Schedule(ctx, seasonYear)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, err
	}

	// The schedule's team objects carry only id and name; abbreviations for
	// the game team refs come from the team list.
	abbrevByID, teamFetches := p.teamAbbreviations(ctx, seasonYear)
	fetches = append(fetches, teamFetches...)

	pitchers := make(map[int]*model.ProbablePitcher)
	var games []model.Game
	seen := make(map[int]bool)
	for _, date := range resp.Dates {
		for _, g := range date.Games {
			if g.RescheduleDate != "" || seen[g.GamePk] {
				continue
			}
			if _, relevant := mapSeasonType(g.GameType); !relevant {
				continue
			}
			seen[g.GamePk] = true

			if mapStatus(g.Status.AbstractGameState, g.Status.DetailedState) == model.GameScheduled {
				pitcherFetches := p.resolvePitchers(ctx, seasonYear, pitchers, g.Teams.Home.ProbablePitcher, g.Teams.Away.ProbablePitcher)
				fetches = append(fetches, pitcherFetches...)
			}
			if game, ok := normalizeGame(g, seasonYear, pitchers, abbrevByID); ok {
				games = append(games, game)
			}
		}
	}
	return games, fetches, nil
}

// resolvePitchers fetches season pitching stats for announced probables not
// yet in the per-cycle cache. Failures degrade to a name-and-id-only block
// (cached as nil so one bad pitcher costs one call per cycle), never fatal.
func (p *Provider) resolvePitchers(ctx context.Context, seasonYear int, cache map[int]*model.ProbablePitcher, refs ...*statsapiPerson) []*sportsdata.Fetch {
	var fetches []*sportsdata.Fetch
	for _, ref := range refs {
		if ref == nil {
			continue
		}
		if _, ok := cache[ref.ID]; ok {
			continue
		}
		resp, fetch, err := p.client.Person(ctx, ref.ID, seasonYear)
		if fetch != nil {
			fetches = append(fetches, fetch)
		}
		if err != nil {
			slog.Warn("probable pitcher stats unavailable, embedding name only",
				"pitcher_id", ref.ID, "pitcher", ref.FullName, "error", err)
			cache[ref.ID] = nil
			continue
		}
		cache[ref.ID] = normalizePitcher(resp)
	}
	return fetches
}

// Scoreboard fetches one date's live statuses as neutral updates keyed by
// StatsAPI gamePk. FINAL results carry per-inning period scores and
// Overtime for extra innings; baseball settles on the final score, so no
// regulation fields are set (ADR-027).
func (p *Provider) Scoreboard(ctx context.Context, date time.Time) (map[string]sportsdata.ScoreboardUpdate, []*sportsdata.Fetch, error) {
	resp, fetch, err := p.client.Scoreboard(ctx, date)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, err
	}

	updates := make(map[string]sportsdata.ScoreboardUpdate)
	for _, d := range resp.Dates {
		for _, g := range d.Games {
			update := sportsdata.ScoreboardUpdate{
				Status: mapStatus(g.Status.AbstractGameState, g.Status.DetailedState),
			}
			if ls := g.Linescore; ls != nil && len(ls.Innings) > 0 {
				update.HomeScore = ls.Teams.Home.Runs
				update.AwayScore = ls.Teams.Away.Runs
			} else {
				if g.Teams.Home.Score != nil {
					update.HomeScore = *g.Teams.Home.Score
				}
				if g.Teams.Away.Score != nil {
					update.AwayScore = *g.Teams.Away.Score
				}
			}
			if update.Status == model.GameFinal {
				update.Result = buildResult(g, time.Now().UTC())
			}
			updates[strconv.Itoa(g.GamePk)] = update
		}
	}
	return updates, fetches, nil
}

// PlayerGameLog returns an empty log: per-game MLB player logs remain
// descoped (rosters, season rates, and box scores landed in Phase 7
// Wave 3; prop grading reads box scores, not game logs).
func (p *Provider) PlayerGameLog(context.Context, model.PlayerDetail, int) ([]model.PlayerGameLine, []*sportsdata.Fetch, error) {
	return []model.PlayerGameLine{}, nil, nil
}

// BoxScore fetches one game's box score by StatsAPI gamePk and normalizes
// its per-player batting and pitching lines (Phase 7 Wave 3; the tracked
// deferral is closed). The response labels home/away directly, so the
// blocks pass through orientBoxScore already oriented.
func (p *Provider) BoxScore(ctx context.Context, gameExternalID string) (*model.BoxScore, []*sportsdata.Fetch, error) {
	resp, fetch, err := p.client.BoxScore(ctx, gameExternalID)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, err
	}

	box := normalizeBoxScore(gameExternalID, resp)
	if box == nil {
		return nil, fetches, fmt.Errorf("no box score data for game %s", gameExternalID)
	}
	return box, fetches, nil
}
