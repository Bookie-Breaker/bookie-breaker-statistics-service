package espn

import (
	"strings"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// Normalize converts the ESPN injury payload into canonical reports for one
// league. teamIDByName maps normalized full team names to canonical team
// UUIDs and abbrevByName to their abbreviations (built from the cached team
// list). playerIDByKey maps "name|teamAbbrev" to canonical player UUIDs;
// ESPN-to-source player matching is best-effort by normalized name.
func Normalize(resp *injuriesResponse, league model.League, teamIDByName, abbrevByName, playerIDByKey map[string]string) []model.InjuryReport {
	var reports []model.InjuryReport
	for _, team := range resp.Injuries {
		nameKey := normalizeName(team.DisplayName)
		teamID := teamIDByName[nameKey]
		abbrev := abbrevByName[nameKey]

		for _, inj := range team.Injuries {
			report := model.InjuryReport{
				PlayerName:       inj.Athlete.DisplayName,
				TeamID:           teamID,
				TeamAbbreviation: abbrev,
				League:           league,
				Position:         inj.Athlete.Position.Abbreviation,
				Status:           string(MapStatus(inj.Status)),
				Description:      firstNonEmpty(inj.ShortComment, inj.Details.Type),
			}
			if id, ok := playerIDByKey[normalizeName(inj.Athlete.DisplayName)+"|"+abbrev]; ok {
				report.PlayerID = id
			}
			if t, err := time.Parse(time.RFC3339, inj.Date); err == nil {
				report.UpdatedAt = t
			}
			reports = append(reports, report)
		}
	}
	return reports
}

// MapStatus maps ESPN injury statuses onto the contract's player statuses.
func MapStatus(status string) model.PlayerStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "out":
		return model.PlayerOut
	case "suspension", "suspended":
		return model.PlayerSuspended
	case "day-to-day", "questionable", "doubtful", "probable", "game time decision":
		return model.PlayerInjured
	default:
		return model.PlayerInjured
	}
}

func normalizeName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
