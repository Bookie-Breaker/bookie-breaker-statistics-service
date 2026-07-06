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
			report.UpdatedAt = parseDate(inj.Date)
			reports = append(reports, report)
		}
	}
	return reports
}

// MapStatus maps ESPN injury statuses onto the contract's player statuses.
// Baseball reports injured-list stints ("10-Day-IL", "15-Day-IL",
// "60-Day-IL"; observed live 2026-07-05) — an IL player is out until
// activated.
func MapStatus(status string) model.PlayerStatus {
	normalized := strings.ToLower(strings.TrimSpace(status))
	if strings.HasSuffix(normalized, "-il") {
		return model.PlayerOut
	}
	switch normalized {
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

// parseDate parses ESPN injury timestamps: NBA reports carry full RFC 3339
// but MLB reports are minute-precision ("2026-07-05T22:50Z"; observed live).
// Unparseable dates yield the zero time.
func parseDate(date string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04Z07:00"} {
		if t, err := time.Parse(layout, date); err == nil {
			return t
		}
	}
	return time.Time{}
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
