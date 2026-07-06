package soccer

import (
	"fmt"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// Competition configures one soccer competition served by the shared ESPN
// adapter (ADR-026). Each competition is a config entry — league_enum value,
// ESPN league code, and season shape — so adding a competition never adds
// code paths.
type Competition struct {
	// League is the league_enum value this competition maps to.
	League model.League
	// ESPNCode is the ESPN site API league code, e.g. "fifa.world".
	ESPNCode string
	// SeasonYear derives the current season year from a moment in time.
	SeasonYear func(now time.Time) int
	// SeasonWindow bounds the competition's schedule for one season; the
	// schedule fetch walks it in month chunks.
	SeasonWindow func(seasonYear int) (start, end time.Time)
	// SeasonType maps an ESPN event's season slug to a canonical season
	// type. Verified live (2026-07-05): World Cup group-stage events carry
	// season.slug "group-stage" and knockout rounds carry the round name
	// ("round-of-32", "round-of-16", ...); EPL events carry the season name.
	SeasonType func(seasonSlug string) model.SeasonType
}

// FIFAWorldCup is the FIFA World Cup competition config. The season is the
// tournament year and the group stage maps to the regular season; knockout
// rounds are the postseason.
func FIFAWorldCup() Competition {
	return Competition{
		League:   model.LeagueFIFAWC,
		ESPNCode: "fifa.world",
		SeasonYear: func(now time.Time) int {
			// Most recent World Cup year: tournaments run every four years
			// on the 1930 cycle (..., 2022, 2026, 2030).
			y := now.UTC().Year()
			return y - ((y-1930)%4+4)%4
		},
		SeasonWindow: func(seasonYear int) (time.Time, time.Time) {
			// The tournament runs mid-June to mid-July; the window pads both
			// ends (2026: June 11 – July 19).
			return time.Date(seasonYear, time.June, 1, 0, 0, 0, 0, time.UTC),
				time.Date(seasonYear, time.August, 1, 0, 0, 0, 0, time.UTC)
		},
		SeasonType: func(seasonSlug string) model.SeasonType {
			if seasonSlug == "" || seasonSlug == "group-stage" {
				return model.SeasonRegular
			}
			return model.SeasonPostseason
		},
	}
}

// PremierLeague is the English Premier League competition config. Seasons
// roll over August–May and are named by their starting year.
func PremierLeague() Competition {
	return Competition{
		League:   model.LeagueEPL,
		ESPNCode: "eng.1",
		SeasonYear: func(now time.Time) int {
			t := now.UTC()
			if t.Month() >= time.July {
				return t.Year()
			}
			return t.Year() - 1
		},
		SeasonWindow: func(seasonYear int) (time.Time, time.Time) {
			// August 1 through June 15 of the following year covers early
			// starts and any rescheduled late fixtures.
			return time.Date(seasonYear, time.August, 1, 0, 0, 0, 0, time.UTC),
				time.Date(seasonYear+1, time.June, 15, 0, 0, 0, 0, time.UTC)
		},
		SeasonType: func(string) model.SeasonType {
			// The EPL has no postseason; every fixture is regular season.
			return model.SeasonRegular
		},
	}
}

// CompetitionForLeague resolves the competition config for a soccer league.
func CompetitionForLeague(league model.League) (Competition, error) {
	switch league {
	case model.LeagueFIFAWC:
		return FIFAWorldCup(), nil
	case model.LeagueEPL:
		return PremierLeague(), nil
	default:
		return Competition{}, fmt.Errorf("no soccer competition config for league %s", league)
	}
}
