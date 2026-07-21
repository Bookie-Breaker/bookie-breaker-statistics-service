package config

import (
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

func TestGetEnv(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    string // "" means leave unset
		set      bool
		fallback string
		want     string
	}{
		{"unset returns fallback", "BB_TEST_GETENV_UNSET", "", false, "fallback", "fallback"},
		{"empty value returns fallback", "BB_TEST_GETENV_EMPTY", "", true, "fallback", "fallback"},
		{"set value overrides fallback", "BB_TEST_GETENV_SET", "override", true, "fallback", "override"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(tt.key, tt.value)
			}
			if got := getEnv(tt.key, tt.fallback); got != tt.want {
				t.Errorf("getEnv(%q, %q) = %q, want %q", tt.key, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestGetEnvInt(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		set      bool
		fallback int
		want     int
	}{
		{"unset returns fallback", "", false, 42, 42},
		{"valid int overrides fallback", "7", true, 42, 7},
		{"invalid int falls back", "not-a-number", true, 42, 42},
		{"empty string falls back", "", true, 42, 42},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const key = "BB_TEST_GETENVINT"
			if tt.set {
				t.Setenv(key, tt.value)
			}
			if got := getEnvInt(key, tt.fallback); got != tt.want {
				t.Errorf("getEnvInt = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGetEnvDuration(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		set      bool
		fallback time.Duration
		want     time.Duration
	}{
		{"unset returns fallback", "", false, 5 * time.Second, 5 * time.Second},
		{"valid duration overrides fallback", "250ms", true, 5 * time.Second, 250 * time.Millisecond},
		{"invalid duration falls back", "not-a-duration", true, 5 * time.Second, 5 * time.Second},
		{"empty string falls back", "", true, 5 * time.Second, 5 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const key = "BB_TEST_GETENVDURATION"
			if tt.set {
				t.Setenv(key, tt.value)
			}
			if got := getEnvDuration(key, tt.fallback); got != tt.want {
				t.Errorf("getEnvDuration = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseLeagues(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []model.League
		wantErr bool
	}{
		{"single league", "NBA", []model.League{model.LeagueNBA}, false},
		{"mixed case", "nba", []model.League{model.LeagueNBA}, false},
		{"whitespace trimmed", "  NBA  ,  MLB  ", []model.League{model.LeagueNBA, model.LeagueMLB}, false},
		{"dedupes repeats", "NBA,NBA,MLB", []model.League{model.LeagueNBA, model.LeagueMLB}, false},
		{"lower and upper mixed with dupes", "nba, NBA, mlb", []model.League{model.LeagueNBA, model.LeagueMLB}, false},
		{"unknown league errors", "NBA,FOOBALL", nil, true},
		{"empty string errors", "", nil, true},
		{"only separators errors", " , , ", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLeagues(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseLeagues(%q) expected error, got leagues %v", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLeagues(%q) unexpected error: %v", tt.raw, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseLeagues(%q) = %v, want %v", tt.raw, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parseLeagues(%q)[%d] = %v, want %v", tt.raw, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Port != 8002 {
		t.Errorf("Port = %d, want default 8002", cfg.Port)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want default %q", cfg.LogLevel, "info")
	}
	if cfg.NBAMinRequestInterval != 800*time.Millisecond {
		t.Errorf("NBAMinRequestInterval = %v, want default 800ms", cfg.NBAMinRequestInterval)
	}
	if len(cfg.LeaguesEnabled) != 1 || cfg.LeaguesEnabled[0] != model.LeagueNBA {
		t.Errorf("LeaguesEnabled = %v, want [NBA]", cfg.LeaguesEnabled)
	}
	if cfg.CircuitFailureThreshold != 5 {
		t.Errorf("CircuitFailureThreshold = %d, want default 5", cfg.CircuitFailureThreshold)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	t.Setenv("PORT", "9099")
	t.Setenv("DATABASE_URL", "postgres://example/db")
	t.Setenv("NBA_MIN_REQUEST_INTERVAL", "5s")
	t.Setenv("LEAGUES_ENABLED", "nfl, mlb")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Port != 9099 {
		t.Errorf("Port = %d, want 9099", cfg.Port)
	}
	if cfg.DatabaseURL != "postgres://example/db" {
		t.Errorf("DatabaseURL = %q, want override", cfg.DatabaseURL)
	}
	if cfg.NBAMinRequestInterval != 5*time.Second {
		t.Errorf("NBAMinRequestInterval = %v, want 5s", cfg.NBAMinRequestInterval)
	}
	want := []model.League{model.LeagueNFL, model.LeagueMLB}
	if len(cfg.LeaguesEnabled) != len(want) {
		t.Fatalf("LeaguesEnabled = %v, want %v", cfg.LeaguesEnabled, want)
	}
	for i := range want {
		if cfg.LeaguesEnabled[i] != want[i] {
			t.Errorf("LeaguesEnabled[%d] = %v, want %v", i, cfg.LeaguesEnabled[i], want[i])
		}
	}
}

func TestLoadInvalidIntAndDurationFallBack(t *testing.T) {
	t.Setenv("PORT", "not-an-int")
	t.Setenv("NBA_MIN_REQUEST_INTERVAL", "not-a-duration")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Port != 8002 {
		t.Errorf("Port = %d, want fallback default 8002 for invalid int", cfg.Port)
	}
	if cfg.NBAMinRequestInterval != 800*time.Millisecond {
		t.Errorf("NBAMinRequestInterval = %v, want fallback default 800ms for invalid duration", cfg.NBAMinRequestInterval)
	}
}

func TestLoadRejectsUnknownLeague(t *testing.T) {
	t.Setenv("LEAGUES_ENABLED", "NBA,ATLANTIS")

	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for unknown league, got nil")
	}
}

func TestLoadRejectsEmptyLeagueList(t *testing.T) {
	t.Setenv("LEAGUES_ENABLED", "  ")

	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for empty league list, got nil")
	}
}
