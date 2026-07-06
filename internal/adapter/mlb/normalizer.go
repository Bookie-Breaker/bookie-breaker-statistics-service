package mlb

import (
	"strconv"
	"strings"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// leagueMLB is the league key for canonical id derivation.
const leagueMLB = string(model.LeagueMLB)

// Published seasonal constants for in-service sabermetrics (ADR-026). Both
// are for the 2026 season — revisit yearly when FanGraphs publishes the new
// values.
const (
	// cFIP normalizes FIP onto the league ERA scale:
	// FIP = (13*HR + 3*(BB+HBP) - 2*SO)/IP + cFIP.
	cFIP = 3.10
)

// wOBA linear weights, 2026 season — update yearly:
// wOBA = (0.69*BB + 0.72*HBP + 0.88*1B + 1.26*2B + 1.60*3B + 2.07*HR)
//
//	/ (AB + BB - IBB + SF + HBP).
const (
	wobaBB  = 0.69
	wobaHBP = 0.72
	woba1B  = 0.88
	woba2B  = 1.26
	woba3B  = 1.60
	wobaHR  = 2.07
)

// regulationInnings is a regulation MLB game; more means extra innings.
const regulationInnings = 9

// normalizeTeams builds the canonical team collections. The venue comes from
// the teams response; league/division map to conference/division.
func normalizeTeams(resp *teamsResponse) ([]model.TeamSummary, map[string]model.TeamDetail) {
	summaries := make([]model.TeamSummary, 0, len(resp.Teams))
	details := make(map[string]model.TeamDetail, len(resp.Teams))
	for _, t := range resp.Teams {
		externalID := strconv.Itoa(t.ID)
		summary := model.TeamSummary{
			ID:           ids.Team(leagueMLB, externalID),
			League:       model.LeagueMLB,
			Name:         t.Name,
			Abbreviation: t.Abbreviation,
			Location:     t.LocationName,
			Conference:   t.League.Name,
			Division:     t.Division.Name,
			Active:       t.Active,
			ExternalIDs:  map[string]string{"mlb": externalID},
		}
		detail := model.TeamDetail{TeamSummary: summary}
		if t.Venue.Name != "" {
			venueID := ids.Venue(leagueMLB, strconv.Itoa(t.Venue.ID))
			summary.VenueID = venueID
			detail.VenueID = venueID
			detail.Venue = &model.VenueSummary{ID: venueID, Name: t.Venue.Name}
		}
		summaries = append(summaries, summary)
		details[summary.ID] = detail
	}
	return summaries, details
}

// mapSeasonType converts a StatsAPI gameType to the canonical season type.
// ok is false for game types outside the betting-relevant season (all-star
// "A", exhibitions "E"); those games are skipped entirely.
func mapSeasonType(gameType string) (model.SeasonType, bool) {
	switch gameType {
	case "S":
		return model.SeasonPreseason, true
	case "R":
		return model.SeasonRegular, true
	case "F", "D", "L", "W":
		return model.SeasonPostseason, true
	default:
		return "", false
	}
}

// mapStatus converts a StatsAPI game status to the canonical status.
// detailedState is checked first: postponed games report abstractGameState
// "Final" with detailedState "Postponed" (verified live 2026-07-05).
func mapStatus(abstractState, detailedState string) model.GameStatus {
	switch {
	case strings.Contains(detailedState, "Postponed"):
		return model.GamePostponed
	case strings.Contains(detailedState, "Cancelled"), strings.Contains(detailedState, "Canceled"): //nolint:misspell // StatsAPI uses the double-l spelling
		return model.GameCancelled //nolint:misspell // spelling fixed by the OpenAPI contract enum
	case strings.Contains(detailedState, "Suspended"):
		return model.GameSuspended
	}
	switch abstractState {
	case "Live":
		return model.GameInProgress
	case "Final": // includes "Completed Early"
		return model.GameFinal
	default: // "Preview"
		return model.GameScheduled
	}
}

// buildResult builds a final game's result from its linescore. Scores come
// from the linescore totals (falling back to the schedule scores when the
// linescore is missing), period scores from the per-inning runs, and
// Overtime means extra innings. An unplayed bottom of the ninth records 0
// (the period-score sum still equals the final score). Baseball settles on
// the final score, so no regulation fields are set (ADR-027).
func buildResult(g statsapiGame, completedAt time.Time) *model.GameResult {
	homeScore, awayScore := 0, 0
	if g.Teams.Home.Score != nil {
		homeScore = *g.Teams.Home.Score
	}
	if g.Teams.Away.Score != nil {
		awayScore = *g.Teams.Away.Score
	}

	result := &model.GameResult{
		Overtime:    false,
		CompletedAt: completedAt,
	}
	if ls := g.Linescore; ls != nil && len(ls.Innings) > 0 {
		homeScore = ls.Teams.Home.Runs
		awayScore = ls.Teams.Away.Runs
		result.Overtime = len(ls.Innings) > regulationInnings
		for _, inn := range ls.Innings {
			ps := model.PeriodScore{Period: inn.Num}
			if inn.Home.Runs != nil {
				ps.Home = *inn.Home.Runs
			}
			if inn.Away.Runs != nil {
				ps.Away = *inn.Away.Runs
			}
			result.PeriodScores = append(result.PeriodScores, ps)
		}
	}
	result.HomeScore = homeScore
	result.AwayScore = awayScore
	result.TotalScore = homeScore + awayScore
	result.Margin = homeScore - awayScore
	return result
}

// normalizeGame converts one schedule game to a canonical game. ok is false
// for game types outside the season (all-star, exhibitions) and malformed
// dates. pitchers supplies embedded probable-pitcher stats keyed by StatsAPI
// person id; games not in the map still carry name and external id.
// abbrevByID supplies team abbreviations (the schedule team objects carry
// only id and name).
func normalizeGame(g statsapiGame, seasonYear int, pitchers map[int]*model.ProbablePitcher, abbrevByID map[int]string) (model.Game, bool) {
	seasonType, ok := mapSeasonType(g.GameType)
	if !ok {
		return model.Game{}, false
	}
	start, err := time.Parse(time.RFC3339, g.GameDate)
	if err != nil {
		return model.Game{}, false
	}

	externalID := strconv.Itoa(g.GamePk)
	game := model.Game{
		ID:         ids.Game(leagueMLB, externalID),
		ExternalID: externalID,
		League:     model.LeagueMLB,
		HomeTeam: model.TeamRef{
			ID:           ids.Team(leagueMLB, strconv.Itoa(g.Teams.Home.Team.ID)),
			Name:         g.Teams.Home.Team.Name,
			Abbreviation: abbrevByID[g.Teams.Home.Team.ID],
		},
		AwayTeam: model.TeamRef{
			ID:           ids.Team(leagueMLB, strconv.Itoa(g.Teams.Away.Team.ID)),
			Name:         g.Teams.Away.Team.Name,
			Abbreviation: abbrevByID[g.Teams.Away.Team.ID],
		},
		ScheduledStart: start,
		Status:         mapStatus(g.Status.AbstractGameState, g.Status.DetailedState),
		Season:         seasonYear,
		SeasonType:     seasonType,
	}
	if g.Venue.Name != "" {
		game.Venue = &model.VenueRef{
			ID:   ids.Venue(leagueMLB, strconv.Itoa(g.Venue.ID)),
			Name: g.Venue.Name,
		}
	}

	game.HomeProbablePitcher = probableFor(g.Teams.Home.ProbablePitcher, pitchers)
	game.AwayProbablePitcher = probableFor(g.Teams.Away.ProbablePitcher, pitchers)

	if game.Status == model.GameInProgress || game.Status == model.GameFinal {
		h, a := 0, 0
		if ls := g.Linescore; ls != nil && len(ls.Innings) > 0 {
			h, a = ls.Teams.Home.Runs, ls.Teams.Away.Runs
		} else {
			if g.Teams.Home.Score != nil {
				h = *g.Teams.Home.Score
			}
			if g.Teams.Away.Score != nil {
				a = *g.Teams.Away.Score
			}
		}
		game.HomeScore = &h
		game.AwayScore = &a
	}
	if game.Status == model.GameFinal {
		game.Result = buildResult(g, start)
		game.Result.ID = game.ID
	}
	return game, true
}

// probableFor builds a game's probable-pitcher field. With embedded stats
// resolved (upcoming games) it carries the full block; otherwise name and
// external id only.
func probableFor(ref *statsapiPerson, pitchers map[int]*model.ProbablePitcher) *model.ProbablePitcher {
	if ref == nil {
		return nil
	}
	if pp, ok := pitchers[ref.ID]; ok && pp != nil {
		clone := *pp
		return &clone
	}
	return &model.ProbablePitcher{
		Name:       ref.FullName,
		ExternalID: strconv.Itoa(ref.ID),
	}
}

// normalizePitcher converts a hydrated person response into an embedded
// probable-pitcher block with computed FIP and K-BB%.
func normalizePitcher(resp *peopleResponse) *model.ProbablePitcher {
	if len(resp.People) == 0 {
		return nil
	}
	p := resp.People[0]
	pp := &model.ProbablePitcher{
		Name:       p.FullName,
		ExternalID: strconv.Itoa(p.ID),
		Throws:     p.PitchHand.Code,
	}
	for _, group := range p.Stats {
		for _, split := range group.Splits {
			st := split.Stat
			pp.ERA = parseRate(st.ERA)
			pp.InningsPitched = parseInnings(st.InningsPitched)
			pp.FIP = fip(st.HomeRuns, st.BaseOnBalls, st.HitByPitch, st.StrikeOuts, pp.InningsPitched)
			if st.BattersFaced > 0 {
				pp.KBBPct = float64(st.StrikeOuts-st.BaseOnBalls) / float64(st.BattersFaced)
			}
		}
	}
	return pp
}

// fip computes Fielding Independent Pitching from counting stats using the
// 2026 cFIP constant.
func fip(hr, bb, hbp, so int, inningsPitched float64) float64 {
	if inningsPitched <= 0 {
		return 0
	}
	return (13*float64(hr)+3*float64(bb+hbp)-2*float64(so))/inningsPitched + cFIP
}

// woba computes weighted on-base average from hitting counting stats using
// the 2026 linear weights.
func woba(st statsapiStatLine) float64 {
	singles := st.Hits - st.Doubles - st.Triples - st.HomeRuns
	den := float64(st.AtBats + st.BaseOnBalls - st.IntentionalWalks + st.SacFlies + st.HitByPitch)
	if den <= 0 {
		return 0
	}
	num := wobaBB*float64(st.BaseOnBalls) +
		wobaHBP*float64(st.HitByPitch) +
		woba1B*float64(singles) +
		woba2B*float64(st.Doubles) +
		woba3B*float64(st.Triples) +
		wobaHR*float64(st.HomeRuns)
	return num / den
}

// record carries one team's standings line.
type record struct {
	Wins, Losses, GamesPlayed int
}

// normalizeTeamStats merges the hitting, pitching, bullpen, and standings
// responses into canonical team stats keyed by canonical team id.
func normalizeTeamStats(seasonYear int, hitting, pitching, bullpen *teamStatsResponse, standings *standingsResponse, abbrevByID map[int]string) map[string]model.TeamStats {
	records := make(map[int]record)
	for _, r := range standings.Records {
		for _, tr := range r.TeamRecords {
			records[tr.Team.ID] = record{Wins: tr.Wins, Losses: tr.Losses, GamesPlayed: tr.GamesPlayed}
		}
	}

	type raw struct {
		hitting, pitching, bullpen *statsapiStatLine
	}
	byTeam := make(map[int]*raw)
	collect := func(resp *teamStatsResponse, assign func(*raw, *statsapiStatLine)) {
		if resp == nil {
			return
		}
		for _, group := range resp.Stats {
			for i := range group.Splits {
				split := group.Splits[i]
				r := byTeam[split.Team.ID]
				if r == nil {
					r = &raw{}
					byTeam[split.Team.ID] = r
				}
				assign(r, &group.Splits[i].Stat)
			}
		}
	}
	collect(hitting, func(r *raw, st *statsapiStatLine) { r.hitting = st })
	collect(pitching, func(r *raw, st *statsapiStatLine) { r.pitching = st })
	collect(bullpen, func(r *raw, st *statsapiStatLine) { r.bullpen = st })

	out := make(map[string]model.TeamStats, len(byTeam))
	for teamID, r := range byTeam {
		baseball := &model.BaseballStats{}
		gamesPlayed := 0
		if st := r.hitting; st != nil {
			gamesPlayed = st.GamesPlayed
			if st.GamesPlayed > 0 {
				baseball.RunsScoredPerGame = float64(st.Runs) / float64(st.GamesPlayed)
			}
			baseball.TeamWOBA = woba(*st)
			baseball.TeamOBP = parseRate(st.OBP)
			baseball.TeamSLG = parseRate(st.SLG)
			if st.PlateAppearances > 0 {
				baseball.BattingStrikeoutPct = float64(st.StrikeOuts) / float64(st.PlateAppearances)
				baseball.BattingWalkPct = float64(st.BaseOnBalls) / float64(st.PlateAppearances)
			}
		}
		if st := r.pitching; st != nil {
			if gamesPlayed == 0 {
				gamesPlayed = st.GamesPlayed
			}
			if st.GamesPlayed > 0 {
				baseball.RunsAllowedPerGame = float64(st.Runs) / float64(st.GamesPlayed)
			}
			baseball.TeamERA = parseRate(st.ERA)
			baseball.TeamFIP = fip(st.HomeRuns, st.BaseOnBalls, st.HitByPitch, st.StrikeOuts, parseInnings(st.InningsPitched))
		}
		if st := r.bullpen; st != nil {
			baseball.BullpenERA = parseRate(st.ERA)
		} else {
			// Documented fallback: without a reliever split the bullpen is
			// approximated by the whole staff.
			baseball.BullpenERA = baseball.TeamERA
		}

		rec := records[teamID]
		if rec.GamesPlayed > 0 {
			gamesPlayed = rec.GamesPlayed
		}
		canonicalID := ids.Team(leagueMLB, strconv.Itoa(teamID))
		out[canonicalID] = model.TeamStats{
			TeamID:           canonicalID,
			TeamAbbreviation: abbrevByID[teamID],
			Season:           seasonYear,
			GamesPlayed:      gamesPlayed,
			Wins:             rec.Wins,
			Losses:           rec.Losses,
			Stats:            model.StatBlocks{Baseball: baseball},
		}
	}
	return out
}

// parseRate parses StatsAPI rate strings like ".347", "3.49", or "-.--"
// (empty-season placeholder) to a float; malformed values yield 0.
func parseRate(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseInnings converts StatsAPI innings notation to a decimal innings
// count: "802.1" means 802 innings plus 1 out-third (802 + 1/3).
func parseInnings(s string) float64 {
	whole, frac, found := strings.Cut(s, ".")
	w, err := strconv.Atoi(whole)
	if err != nil {
		return 0
	}
	if !found {
		return float64(w)
	}
	thirds, err := strconv.Atoi(frac)
	if err != nil || thirds < 0 || thirds > 2 {
		return float64(w)
	}
	return float64(w) + float64(thirds)/3
}
