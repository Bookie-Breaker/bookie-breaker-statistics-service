package mlb

import (
	"context"
	"log/slog"
	"maps"
	"strconv"
	"strings"
	"sync"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// rosterHydrationWorkers bounds concurrent /people season-stat calls during
// a roster refresh (~26 players x 30 teams per cycle; the refresh follows
// the standard Players cadence and is cached, so the total volume is one
// burst every cycle, spread by this bound).
const rosterHydrationWorkers = 8

// Players fetches every team's active roster and hydrates each player's
// season hitting and pitching stats (Phase 7 Wave 3; the Phase 6 descope is
// lifted). A failed roster call is fatal for the refresh; a failed per-player
// hydration degrades that player to a roster-only summary, never fatal.
func (p *Provider) Players(ctx context.Context, seasonYear int) ([]model.PlayerSummary, map[string]model.PlayerDetail, []*sportsdata.Fetch, error) {
	teamsResp, fetch, err := p.client.Teams(ctx, seasonYear)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, nil, fetches, err
	}

	summaries := []model.PlayerSummary{}
	details := map[string]model.PlayerDetail{}
	for _, team := range teamsResp.Teams {
		roster, fetch, err := p.client.Roster(ctx, team.ID, seasonYear)
		if fetch != nil {
			fetches = append(fetches, fetch)
		}
		if err != nil {
			return nil, nil, fetches, err
		}

		people, personFetches := p.hydrateRoster(ctx, roster, seasonYear)
		fetches = append(fetches, personFetches...)

		s, d := normalizeRoster(team, roster, people, seasonYear)
		summaries = append(summaries, s...)
		maps.Copy(details, d)
	}
	return summaries, details, fetches, nil
}

// hydrateRoster fetches season stats for every roster entry with bounded
// concurrency. Failures degrade to a nil entry (the player keeps a
// roster-only summary), never fatal.
func (p *Provider) hydrateRoster(ctx context.Context, roster *rosterResponse, seasonYear int) (map[int]*peopleResponse, []*sportsdata.Fetch) {
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		sem     = make(chan struct{}, rosterHydrationWorkers)
		people  = make(map[int]*peopleResponse, len(roster.Roster))
		fetches []*sportsdata.Fetch
	)
	for _, entry := range roster.Roster {
		if entry.Person.ID == 0 {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			resp, fetch, err := p.client.PersonSeason(ctx, entry.Person.ID, seasonYear)
			mu.Lock()
			defer mu.Unlock()
			if fetch != nil {
				fetches = append(fetches, fetch)
			}
			if err != nil {
				slog.Warn("player season stats unavailable, keeping roster-only summary",
					"player_id", entry.Person.ID, "player", entry.Person.FullName, "error", err)
				people[entry.Person.ID] = nil
				return
			}
			people[entry.Person.ID] = resp
		}()
	}
	wg.Wait()
	return people, fetches
}

// normalizeRoster converts one team's roster (plus its per-player
// hydrations) into canonical player collections. Names come from the
// hydrated person (the roster carries fullName only); absent hydrations
// fall back to splitting fullName and leave BaseballSeasonStats nil.
func normalizeRoster(team statsapiTeam, roster *rosterResponse, people map[int]*peopleResponse, seasonYear int) ([]model.PlayerSummary, map[string]model.PlayerDetail) {
	teamID := ids.Team(leagueMLB, strconv.Itoa(team.ID))

	var summaries []model.PlayerSummary
	details := make(map[string]model.PlayerDetail, len(roster.Roster))
	for _, entry := range roster.Roster {
		if entry.Person.ID == 0 {
			continue
		}
		externalID := strconv.Itoa(entry.Person.ID)
		first, last := personNames(people[entry.Person.ID], entry.Person.FullName)
		summary := model.PlayerSummary{
			ID:               ids.Player(leagueMLB, externalID),
			TeamID:           teamID,
			TeamAbbreviation: team.Abbreviation,
			FirstName:        first,
			LastName:         last,
			Position:         entry.Position.Abbreviation,
			Status:           model.PlayerActive,
			ExternalIDs:      map[string]string{"mlb": externalID},
			League:           model.LeagueMLB,
		}
		if n, err := strconv.Atoi(entry.JerseyNumber); err == nil {
			summary.JerseyNumber = &n
		}
		summaries = append(summaries, summary)
		details[summary.ID] = model.PlayerDetail{
			PlayerSummary:       summary,
			BaseballSeasonStats: baseballSeasonStats(people[entry.Person.ID], seasonYear),
		}
	}
	return summaries, details
}

// personNames resolves a player's structured names from the hydrated
// person, falling back to splitting the roster fullName.
func personNames(resp *peopleResponse, fullName string) (first, last string) {
	if resp != nil && len(resp.People) > 0 && (resp.People[0].FirstName != "" || resp.People[0].LastName != "") {
		return resp.People[0].FirstName, resp.People[0].LastName
	}
	parts := strings.SplitN(strings.TrimSpace(fullName), " ", 2)
	if len(parts) == 1 {
		return "", parts[0]
	}
	return parts[0], parts[1]
}

// baseballSeasonStats maps a hydrated person's season hitting and pitching
// splits (discriminated by group displayName) onto the canonical block.
// Total bases reuse the box-score formula; rates that need a denominator
// stay zero when it is zero.
func baseballSeasonStats(resp *peopleResponse, seasonYear int) *model.BaseballPlayerSeasonStats {
	if resp == nil || len(resp.People) == 0 {
		return nil
	}
	var hitting, pitching *statsapiStatLine
	for _, group := range resp.People[0].Stats {
		for i := range group.Splits {
			switch group.Group.DisplayName {
			case "hitting":
				hitting = &group.Splits[i].Stat
			case "pitching":
				pitching = &group.Splits[i].Stat
			}
		}
	}
	if hitting == nil && pitching == nil {
		return nil
	}

	stats := &model.BaseballPlayerSeasonStats{Season: seasonYear}
	if st := hitting; st != nil {
		stats.Games = st.GamesPlayed
		stats.AtBats = st.AtBats
		stats.Hits = st.Hits
		stats.TotalBases = totalBases(*st)
		stats.HomeRuns = st.HomeRuns
		stats.BattingAvg = parseRate(st.AVG)
		if st.GamesPlayed > 0 {
			stats.PlateAppearancesPerGame = float64(st.PlateAppearances) / float64(st.GamesPlayed)
		}
	}
	if st := pitching; st != nil {
		if stats.Games == 0 {
			stats.Games = st.GamesPlayed
		}
		stats.InningsPitched = parseInnings(st.InningsPitched)
		if stats.InningsPitched > 0 {
			stats.StrikeoutsPerNine = float64(st.StrikeOuts) * 9 / stats.InningsPitched
		}
	}
	return stats
}
