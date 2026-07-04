package derivedstats

import (
	"math"
	"testing"
)

// sample approximates a real per-game team line (2023-24 Celtics-ish); the
// expected values below follow from the standard formulas.
var sample = Inputs{
	MIN: 241.0, PTS: 120.6, FGM: 43.7, FGA: 90.0, FG3M: 16.5,
	FTA: 21.0, OREB: 10.5, DREB: 36.0, TOV: 12.0,
	OppPTS: 109.2, OppFGA: 88.0, OppFTA: 20.0, OppOREB: 9.5,
	OppDREB: 33.0, OppTOV: 13.5,
}

func within(t *testing.T, name string, got, want, tolerance float64) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Errorf("%s = %.4f, want %.4f (±%.4f)", name, got, want, tolerance)
	}
}

func TestFormulas(t *testing.T) {
	// possessions = 0.5*((90+0.44*21-10.5+12)+(88+0.44*20-9.5+13.5)) = 100.77
	within(t, "Possessions", Possessions(sample), 100.77, 0.01)
	within(t, "OffensiveRating", OffensiveRating(sample), 119.68, 0.01)
	within(t, "DefensiveRating", DefensiveRating(sample), 108.37, 0.01)
	// pace = 48*poss/(241/5)
	within(t, "Pace", Pace(sample), 100.35, 0.01)
	// eFG% = (43.7+8.25)/90
	within(t, "EffectiveFGPct", EffectiveFGPct(sample), 0.5772, 0.001)
	// TS% = 120.6/(2*(90+9.24))
	within(t, "TrueShootingPct", TrueShootingPct(sample), 0.6076, 0.001)
	// TOV% = 100*12/(90+9.24+12)
	within(t, "TurnoverPct", TurnoverPct(sample), 10.79, 0.01)
	// ORB% = 10.5/(10.5+33)
	within(t, "OffensiveReboundPct", OffensiveReboundPct(sample), 0.2414, 0.001)
}

func TestZeroInputsDoNotDivideByZero(t *testing.T) {
	var zero Inputs
	for name, fn := range map[string]func(Inputs) float64{
		"OffensiveRating":     OffensiveRating,
		"DefensiveRating":     DefensiveRating,
		"Pace":                Pace,
		"EffectiveFGPct":      EffectiveFGPct,
		"TrueShootingPct":     TrueShootingPct,
		"TurnoverPct":         TurnoverPct,
		"OffensiveReboundPct": OffensiveReboundPct,
	} {
		if got := fn(zero); got != 0 {
			t.Errorf("%s(zero) = %f, want 0", name, got)
		}
	}
}

func TestWinPct(t *testing.T) {
	if got := WinPct(41, 41); got != 0.5 {
		t.Errorf("WinPct(41,41) = %f, want 0.5", got)
	}
	if got := WinPct(0, 0); got != 0 {
		t.Errorf("WinPct(0,0) = %f, want 0", got)
	}
}
