package model

type League string

const (
	LeagueNBA League = "NBA"
)

type GameStatus string

const (
	GameScheduled  GameStatus = "SCHEDULED"
	GameInProgress GameStatus = "IN_PROGRESS"
	GameFinal      GameStatus = "FINAL"
	GamePostponed  GameStatus = "POSTPONED"
	GameCancelled  GameStatus = "CANCELLED" //nolint:misspell // spelling fixed by the OpenAPI contract enum
	GameSuspended  GameStatus = "SUSPENDED"
)

type SeasonType string

const (
	SeasonPreseason  SeasonType = "PRESEASON"
	SeasonRegular    SeasonType = "REGULAR"
	SeasonPostseason SeasonType = "POSTSEASON"
	SeasonOffseason  SeasonType = "OFFSEASON"
)

type PlayerStatus string

const (
	PlayerActive    PlayerStatus = "ACTIVE"
	PlayerInjured   PlayerStatus = "INJURED"
	PlayerOut       PlayerStatus = "OUT"
	PlayerSuspended PlayerStatus = "SUSPENDED"
	PlayerInactive  PlayerStatus = "INACTIVE"
)

type StatType string

const (
	StatOffensive StatType = "offensive"
	StatDefensive StatType = "defensive"
	StatOverall   StatType = "overall"
	StatAdvanced  StatType = "advanced"
	StatAll       StatType = "all"
)
