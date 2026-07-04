// Package derivedstats computes basketball efficiency metrics. Upstream
// Advanced values (leaguedashteamstats MeasureType=Advanced) are
// authoritative when present; these standard formulas are the fallback for
// the seasonal-breakage case where the Advanced result set changes shape.
// All inputs are per-game values.
package derivedstats

// Inputs are the per-game box-score aggregates the formulas need.
type Inputs struct {
	MIN  float64 // team minutes per game (~240; 5 skaters x 48)
	PTS  float64
	FGM  float64
	FGA  float64
	FG3M float64
	FTA  float64
	OREB float64
	DREB float64
	TOV  float64

	OppPTS  float64
	OppFGA  float64
	OppFTA  float64
	OppOREB float64
	OppDREB float64
	OppTOV  float64
}

// Possessions estimates possessions per game with the standard 0.44
// free-throw factor, averaged across both teams' totals.
func Possessions(in Inputs) float64 {
	own := in.FGA + 0.44*in.FTA - in.OREB + in.TOV
	opp := in.OppFGA + 0.44*in.OppFTA - in.OppOREB + in.OppTOV
	return 0.5 * (own + opp)
}

// OffensiveRating is points scored per 100 possessions.
func OffensiveRating(in Inputs) float64 {
	poss := Possessions(in)
	if poss == 0 {
		return 0
	}
	return 100 * in.PTS / poss
}

// DefensiveRating is points allowed per 100 possessions.
func DefensiveRating(in Inputs) float64 {
	poss := Possessions(in)
	if poss == 0 {
		return 0
	}
	return 100 * in.OppPTS / poss
}

// Pace is possessions per 48 minutes.
func Pace(in Inputs) float64 {
	if in.MIN == 0 {
		return 0
	}
	return 48 * Possessions(in) / (in.MIN / 5)
}

// EffectiveFGPct weights three-pointers at 1.5 field goals.
func EffectiveFGPct(in Inputs) float64 {
	if in.FGA == 0 {
		return 0
	}
	return (in.FGM + 0.5*in.FG3M) / in.FGA
}

// TrueShootingPct accounts for free throws via the 0.44 factor.
func TrueShootingPct(in Inputs) float64 {
	denom := 2 * (in.FGA + 0.44*in.FTA)
	if denom == 0 {
		return 0
	}
	return in.PTS / denom
}

// TurnoverPct is turnovers per 100 plays.
func TurnoverPct(in Inputs) float64 {
	denom := in.FGA + 0.44*in.FTA + in.TOV
	if denom == 0 {
		return 0
	}
	return 100 * in.TOV / denom
}

// OffensiveReboundPct is the share of available offensive rebounds grabbed.
func OffensiveReboundPct(in Inputs) float64 {
	denom := in.OREB + in.OppDREB
	if denom == 0 {
		return 0
	}
	return in.OREB / denom
}

// WinPct is wins over games played.
func WinPct(wins, losses int) float64 {
	games := wins + losses
	if games == 0 {
		return 0
	}
	return float64(wins) / float64(games)
}
