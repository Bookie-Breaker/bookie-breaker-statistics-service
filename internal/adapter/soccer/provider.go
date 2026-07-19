package soccer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// sourceESPN is the raw_api_responses source label for ESPN's site API.
const sourceESPN = "espn"

// formLookbackDays bounds the ranged scoreboard fetch behind the form
// fields: soccer teams play at most twice a week, so 45 days comfortably
// covers five matches (and the whole World Cup).
const formLookbackDays = 45

var _ sportsdata.StatsProvider = (*Provider)(nil)

// Provider implements sportsdata.StatsProvider for one soccer competition
// (ADR-026). Both FIFA_WC and EPL are instances of this type with different
// Competition configs.
type Provider struct {
	client *Client
	comp   Competition
	now    func() time.Time // injectable for tests
}

// NewProvider creates a soccer provider for one competition.
func NewProvider(client *Client, comp Competition) *Provider {
	return &Provider{client: client, comp: comp, now: time.Now}
}

// League identifies the league this provider serves.
func (p *Provider) League() model.League { return p.comp.League }

// Source is the raw_api_responses source label.
func (p *Provider) Source() string { return sourceESPN }

// SeasonYear derives the current season year per the competition config.
func (p *Provider) SeasonYear(now time.Time) int { return p.comp.SeasonYear(now) }

// Teams fetches and normalizes the competition's team list. Always fetched
// dynamically — never a static seed (the 2026 World Cup has 48 teams and
// club leagues rotate by promotion/relegation).
func (p *Provider) Teams(ctx context.Context) ([]model.TeamSummary, map[string]model.TeamDetail, []*sportsdata.Fetch, error) {
	resp, fetch, err := p.client.Teams(ctx, p.comp.ESPNCode)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, nil, fetches, err
	}
	summaries, details := normalizeTeams(p.comp, resp)
	return summaries, details, fetches, nil
}

// TeamStats builds full-season team stats from the competition standings,
// plus recent-form fields from a bounded scoreboard lookback. Rolling
// windows are not supported for soccer (the form fields cover recency).
func (p *Provider) TeamStats(ctx context.Context, seasonYear, window int) (map[string]model.TeamStats, []*sportsdata.Fetch, error) {
	if window > 0 {
		return nil, nil, errors.New("rolling-window stats are unsupported for soccer")
	}

	resp, fetch, err := p.client.Standings(ctx, p.comp.ESPNCode, seasonYear)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, err
	}

	// Form failures degrade to zero form, never fatal (mirrors the NBA
	// provider's optional home/road splits).
	form, formFetches, err := p.recentForm(ctx, seasonYear)
	fetches = append(fetches, formFetches...)
	if err != nil {
		slog.Warn("recent form unavailable, continuing without it", "league", p.comp.League, "error", err)
		form = nil
	}

	return normalizeTeamStats(p.comp, seasonYear, resp, form), fetches, nil
}

// recentForm fetches the last formLookbackDays of completed games (clamped
// to the season window) and aggregates per-team form.
func (p *Provider) recentForm(ctx context.Context, seasonYear int) (map[string]formRecord, []*sportsdata.Fetch, error) {
	start, end := p.comp.SeasonWindow(seasonYear)
	now := p.now().UTC()

	from := now.AddDate(0, 0, -formLookbackDays)
	if from.Before(start) {
		from = start
	}
	to := now
	if to.After(end) {
		to = end
	}
	if to.Before(from) {
		return nil, nil, nil // season has not started; no form yet
	}

	games, fetches, err := p.fetchGames(ctx, seasonYear, from, to, false)
	if err != nil {
		return nil, fetches, err
	}
	return formFromGames(games), fetches, nil
}

// Players builds the competition's player collections from per-team ESPN
// rosters (Phase 7: soccer player props need rosters and season stats;
// amends the Phase 6 descope in ADR-026). One roster call per team on top
// of the team list.
func (p *Provider) Players(ctx context.Context, _ int) ([]model.PlayerSummary, map[string]model.PlayerDetail, []*sportsdata.Fetch, error) {
	teamsResp, fetch, err := p.client.Teams(ctx, p.comp.ESPNCode)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, nil, fetches, err
	}

	summaries := []model.PlayerSummary{}
	details := map[string]model.PlayerDetail{}
	for _, sport := range teamsResp.Sports {
		for _, l := range sport.Leagues {
			for _, entry := range l.Teams {
				team := entry.Team
				roster, fetch, err := p.client.Roster(ctx, p.comp.ESPNCode, team.ID)
				if fetch != nil {
					fetches = append(fetches, fetch)
				}
				if err != nil {
					return nil, nil, fetches, fmt.Errorf("fetch roster for team %s: %w", team.ID, err)
				}
				s, d := normalizeRoster(p.comp, team.ID, team.Abbreviation, roster)
				summaries = append(summaries, s...)
				for id, detail := range d {
					details[id] = detail
				}
			}
		}
	}
	return summaries, details, fetches, nil
}

// BoxScore fetches one match's summary and normalizes its
// boxscore.players block into per-player lines. Home/away orientation
// comes from the summary header's competitors.
func (p *Provider) BoxScore(ctx context.Context, gameExternalID string) (*model.BoxScore, []*sportsdata.Fetch, error) {
	summary, fetch, err := p.client.Summary(ctx, p.comp.ESPNCode, gameExternalID)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, err
	}

	box := normalizeBoxScore(p.comp, gameExternalID, summary)
	if box == nil {
		return nil, fetches, fmt.Errorf("no box score data for event %s", gameExternalID)
	}
	return box, fetches, nil
}

// Schedule fetches the season's games by walking the competition's season
// window in month-sized ranged scoreboard calls.
func (p *Provider) Schedule(ctx context.Context, seasonYear int) ([]model.Game, []*sportsdata.Fetch, error) {
	start, end := p.comp.SeasonWindow(seasonYear)
	return p.fetchGames(ctx, seasonYear, start, end, true)
}

// fetchGames fetches and normalizes every event in [from, to]. When
// withRegulation is true, extra-time finals get a summary fetch for the
// per-period linescores behind the ADR-027 regulation scores.
func (p *Provider) fetchGames(ctx context.Context, seasonYear int, from, to time.Time, withRegulation bool) ([]model.Game, []*sportsdata.Fetch, error) {
	var fetches []*sportsdata.Fetch
	var games []model.Game
	seen := make(map[string]bool)

	for _, chunk := range monthChunks(from, to) {
		resp, fetch, err := p.client.Scoreboard(ctx, p.comp.ESPNCode, chunk[0], chunk[1])
		if fetch != nil {
			fetches = append(fetches, fetch)
		}
		if err != nil {
			return nil, fetches, fmt.Errorf("fetch scoreboard %s: %w", chunk[0].Format("2006-01"), err)
		}

		for _, ev := range resp.Events {
			if seen[ev.ID] {
				continue
			}
			seen[ev.ID] = true

			var homeLines, awayLines []int
			if withRegulation && mapStatus(ev.Status.Type) == model.GameFinal && wentToExtraTime(ev) {
				var lineFetches []*sportsdata.Fetch
				homeLines, awayLines, lineFetches = p.extraTimeLinescores(ctx, ev.ID)
				fetches = append(fetches, lineFetches...)
			}

			if game, ok := normalizeEvent(p.comp, ev, seasonYear, homeLines, awayLines); ok {
				games = append(games, game)
			}
		}
	}
	return games, fetches, nil
}

// Scoreboard fetches one date's live statuses as neutral updates keyed by
// ESPN event id. FINAL results are built with an empty ID; the service sets
// it after matching the update to a canonical game. Extra-time finals cost
// one extra summary call each for the regulation-score linescores.
func (p *Provider) Scoreboard(ctx context.Context, date time.Time) (map[string]sportsdata.ScoreboardUpdate, []*sportsdata.Fetch, error) {
	resp, fetch, err := p.client.Scoreboard(ctx, p.comp.ESPNCode, date, date)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, err
	}

	updates := make(map[string]sportsdata.ScoreboardUpdate, len(resp.Events))
	for _, ev := range resp.Events {
		home, away, ok := homeAwayCompetitors(ev)
		if !ok {
			continue
		}

		update := sportsdata.ScoreboardUpdate{
			Status:    mapStatus(ev.Status.Type),
			HomeScore: parseScore(home.Score),
			AwayScore: parseScore(away.Score),
		}
		if update.Status == model.GameFinal {
			var homeLines, awayLines []int
			if wentToExtraTime(ev) {
				var lineFetches []*sportsdata.Fetch
				homeLines, awayLines, lineFetches = p.extraTimeLinescores(ctx, ev.ID)
				fetches = append(fetches, lineFetches...)
			}
			update.Result = buildResult(home, away, homeLines, awayLines, wentToExtraTime(ev), time.Now().UTC())
		}
		updates[ev.ID] = update
	}
	return updates, fetches, nil
}

// extraTimeLinescores fetches an extra-time final's summary for its
// per-period linescores. Failures degrade to nil lines — the result then
// carries no regulation scores and the emulator falls back to the final
// score (ADR-027's documented fallback) — so a summary outage never blocks
// the scoreboard.
func (p *Provider) extraTimeLinescores(ctx context.Context, eventID string) (homeLines, awayLines []int, fetches []*sportsdata.Fetch) {
	summary, fetch, err := p.client.Summary(ctx, p.comp.ESPNCode, eventID)
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		slog.Warn("summary linescores unavailable for extra-time match; result will carry no regulation scores",
			"league", p.comp.League, "event_id", eventID, "error", err)
		return nil, nil, fetches
	}

	for _, comp := range summary.Header.Competitions {
		for _, c := range comp.Competitors {
			switch c.HomeAway {
			case "home":
				homeLines = parseLinescores(c)
			case "away":
				awayLines = parseLinescores(c)
			}
		}
	}
	return homeLines, awayLines, fetches
}

// PlayerGameLog returns an empty log: soccer player modeling is a
// documented Phase 6 descope (ADR-026).
func (p *Provider) PlayerGameLog(context.Context, model.PlayerDetail, int) ([]model.PlayerGameLine, []*sportsdata.Fetch, error) {
	return []model.PlayerGameLine{}, nil, nil
}

// monthChunks splits [from, to] (inclusive) into calendar-month ranges for
// ESPN's dates=YYYYMMDD-YYYYMMDD parameter.
func monthChunks(from, to time.Time) [][2]time.Time {
	var chunks [][2]time.Time
	cur := from.UTC()
	to = to.UTC()
	for !cur.After(to) {
		nextMonth := time.Date(cur.Year(), cur.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
		chunkEnd := nextMonth.AddDate(0, 0, -1)
		if chunkEnd.After(to) {
			chunkEnd = to
		}
		chunks = append(chunks, [2]time.Time{cur, chunkEnd})
		cur = nextMonth
	}
	return chunks
}
