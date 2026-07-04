package season

import (
	"testing"
	"time"
)

func TestCurrent(t *testing.T) {
	tests := []struct {
		date string
		want int
	}{
		{"2026-07-03", 2025}, // offseason belongs to the season that ended
		{"2025-10-01", 2025}, // rollover day
		{"2025-09-30", 2024},
		{"2026-01-15", 2025}, // mid-season
		{"2026-06-20", 2025}, // finals
	}
	for _, tt := range tests {
		now, err := time.Parse("2006-01-02", tt.date)
		if err != nil {
			t.Fatal(err)
		}
		if got := Current(now); got != tt.want {
			t.Errorf("Current(%s) = %d, want %d", tt.date, got, tt.want)
		}
	}
}

func TestString(t *testing.T) {
	if got := String(2025); got != "2025-26" {
		t.Errorf("String(2025) = %q, want 2025-26", got)
	}
	if got := String(2099); got != "2099-00" {
		t.Errorf("String(2099) = %q, want 2099-00", got)
	}
}
