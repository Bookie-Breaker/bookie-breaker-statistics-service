package soccer

import (
	"strconv"
	"strings"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// ESPN stat key/label synonyms, lowercased. ESPN's soccer stat vocabulary
// varies across competitions and endpoint versions (keys like
// "goalAssists" vs labels like "A"), so every lookup walks a synonym list
// and defaults to zero when nothing matches.
var (
	synMinutes       = []string{"minutesplayed", "minutes", "min", "timeplayed"}
	synGoals         = []string{"totalgoals", "goals", "g"}
	synAssists       = []string{"goalassists", "assists", "a"}
	synShots         = []string{"totalshots", "shotstotal", "shots", "sh"}
	synShotsOnTarget = []string{"shotsontarget", "shotsontargettotal", "st"}
	synYellowCards   = []string{"yellowcards", "yc"}
	synRedCards      = []string{"redcards", "rc"}
	synAppearances   = []string{"appearances", "gamesplayed", "gp"}
)

// normalizeBoxScore converts an event summary's boxscore.players block into
// the canonical box score. Home/away orientation comes from the header
// competitors; player lines attach to their team by ESPN team id. Returns
// nil when the summary carries no usable competitors.
func normalizeBoxScore(comp Competition, eventID string, resp *summaryResponse) *model.BoxScore {
	league := string(comp.League)

	if len(resp.Header.Competitions) == 0 {
		return nil
	}
	var home, away *espnCompetitor
	for i := range resp.Header.Competitions[0].Competitors {
		c := &resp.Header.Competitions[0].Competitors[i]
		switch c.HomeAway {
		case "home":
			home = c
		case "away":
			away = c
		}
	}
	if home == nil || away == nil {
		return nil
	}

	teamBox := func(c *espnCompetitor) model.TeamBoxScore {
		return model.TeamBoxScore{
			ID:            ids.Team(league, c.Team.ID),
			Abbreviation:  c.Team.Abbreviation,
			Score:         parseScore(c.Score),
			SoccerPlayers: playerLines(league, c.Team.ID, resp),
		}
	}

	return &model.BoxScore{
		GameID:   ids.Game(league, eventID),
		Sport:    string(model.SportSoccer),
		HomeTeam: teamBox(home),
		AwayTeam: teamBox(away),
	}
}

// playerLines builds one team's player lines from every statistic group
// under boxscore.players (outfield players and goalkeepers arrive in
// separate groups), deduplicating athletes across groups.
func playerLines(league, espnTeamID string, resp *summaryResponse) []model.SoccerPlayerBoxScore {
	var lines []model.SoccerPlayerBoxScore
	seen := make(map[string]bool)
	for _, teamPlayers := range resp.Boxscore.Players {
		if teamPlayers.Team.ID != espnTeamID {
			continue
		}
		for _, block := range teamPlayers.Statistics {
			idx := statIndex(block)
			for _, line := range block.Athletes {
				if line.Athlete.ID == "" || seen[line.Athlete.ID] {
					continue
				}
				seen[line.Athlete.ID] = true
				lines = append(lines, model.SoccerPlayerBoxScore{
					PlayerID:      ids.Player(league, line.Athlete.ID),
					PlayerName:    line.Athlete.DisplayName,
					Position:      line.Athlete.Position.Abbreviation,
					Minutes:       statFloat(line.Stats, idx, synMinutes),
					Goals:         statInt(line.Stats, idx, synGoals),
					Assists:       statInt(line.Stats, idx, synAssists),
					Shots:         statInt(line.Stats, idx, synShots),
					ShotsOnTarget: statInt(line.Stats, idx, synShotsOnTarget),
					YellowCards:   statInt(line.Stats, idx, synYellowCards),
					RedCards:      statInt(line.Stats, idx, synRedCards),
				})
			}
		}
	}
	return lines
}

// statIndex maps a block's keys and labels (lowercased) to their stat
// positions. Keys win over labels on collision.
func statIndex(block summaryStatBlock) map[string]int {
	idx := make(map[string]int, len(block.Keys)+len(block.Labels))
	for i, l := range block.Labels {
		if l != "" {
			idx[strings.ToLower(l)] = i
		}
	}
	for i, k := range block.Keys {
		if k != "" {
			idx[strings.ToLower(k)] = i
		}
	}
	return idx
}

// statFloat resolves one stat through the synonym list, defaulting to 0 on
// missing keys or unparseable values (ESPN drift must never fail a whole
// box score).
func statFloat(stats []string, idx map[string]int, synonyms []string) float64 {
	for _, syn := range synonyms {
		i, ok := idx[syn]
		if !ok || i >= len(stats) {
			continue
		}
		if v, err := strconv.ParseFloat(strings.TrimSpace(stats[i]), 64); err == nil {
			return v
		}
	}
	return 0
}

func statInt(stats []string, idx map[string]int, synonyms []string) int {
	return int(statFloat(stats, idx, synonyms))
}

// normalizeRoster converts one team's roster into canonical player
// collections. Season stats are attached when ESPN carries a statistics
// block for the athlete (mapped through the same synonym lists as the box
// score); absent stats leave SoccerSeasonStats nil.
func normalizeRoster(comp Competition, espnTeamID, teamAbbrev string, resp *rosterResponse) ([]model.PlayerSummary, map[string]model.PlayerDetail) {
	league := string(comp.League)
	teamID := ids.Team(league, espnTeamID)

	var summaries []model.PlayerSummary
	details := make(map[string]model.PlayerDetail, len(resp.Athletes))
	for _, a := range resp.Athletes {
		if a.ID == "" {
			continue
		}
		first, last := a.FirstName, a.LastName
		if first == "" && last == "" {
			first, last = splitDisplayName(a.DisplayName)
		}
		summary := model.PlayerSummary{
			ID:               ids.Player(league, a.ID),
			TeamID:           teamID,
			TeamAbbreviation: teamAbbrev,
			FirstName:        first,
			LastName:         last,
			Position:         a.Position.Abbreviation,
			Status:           model.PlayerActive,
			ExternalIDs:      map[string]string{"espn": a.ID},
			League:           comp.League,
		}
		if n, err := strconv.Atoi(a.Jersey); err == nil {
			summary.JerseyNumber = &n
		}
		summaries = append(summaries, summary)
		details[summary.ID] = model.PlayerDetail{
			PlayerSummary:     summary,
			SoccerSeasonStats: seasonStatsFromNamed(a.Statistics),
		}
	}
	return summaries, details
}

// seasonStatsFromNamed maps a roster athlete's named season statistics.
func seasonStatsFromNamed(stats []espnNamedStat) *model.SoccerSeasonStats {
	if len(stats) == 0 {
		return nil
	}
	byName := make(map[string]float64, len(stats)*2)
	for _, s := range stats {
		if s.Name != "" {
			byName[strings.ToLower(s.Name)] = s.Value
		}
		if s.Abbreviation != "" {
			byName[strings.ToLower(s.Abbreviation)] = s.Value
		}
	}
	named := func(synonyms []string) int {
		for _, syn := range synonyms {
			if v, ok := byName[syn]; ok {
				return int(v)
			}
		}
		return 0
	}
	return &model.SoccerSeasonStats{
		Appearances:   named(synAppearances),
		Minutes:       named(synMinutes),
		Goals:         named(synGoals),
		Assists:       named(synAssists),
		Shots:         named(synShots),
		ShotsOnTarget: named(synShotsOnTarget),
		YellowCards:   named(synYellowCards),
		RedCards:      named(synRedCards),
	}
}

// splitDisplayName splits "Jude Bellingham" into first and last names when
// ESPN omits the structured name fields.
func splitDisplayName(full string) (first, last string) {
	parts := strings.SplitN(strings.TrimSpace(full), " ", 2)
	if len(parts) == 1 {
		return "", parts[0]
	}
	return parts[0], parts[1]
}
