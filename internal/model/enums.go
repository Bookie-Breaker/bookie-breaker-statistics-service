package model

type League string

// League values match the league_enum in the shared contracts (ADR-026).
const (
	LeagueNFL     League = "NFL"
	LeagueNBA     League = "NBA"
	LeagueMLB     League = "MLB"
	LeagueNCAAFB  League = "NCAA_FB"
	LeagueNCAABB  League = "NCAA_BB"
	LeagueNCAABSB League = "NCAA_BSB"
	LeagueFIFAWC  League = "FIFA_WC"
	LeagueEPL     League = "EPL"
	LeagueNHL     League = "NHL"
	LeagueNCAAHKY League = "NCAA_HKY"
)

type Sport string

// Sport values match the sport_enum in the shared contracts (ADR-026).
const (
	SportFootball   Sport = "FOOTBALL"
	SportBasketball Sport = "BASKETBALL"
	SportBaseball   Sport = "BASEBALL"
	SportSoccer     Sport = "SOCCER"
	SportHockey     Sport = "HOCKEY"
)

// SportForLeague maps every known league to its sport; it doubles as the
// registry of valid league values.
var SportForLeague = map[League]Sport{
	LeagueNFL:     SportFootball,
	LeagueNCAAFB:  SportFootball,
	LeagueNBA:     SportBasketball,
	LeagueNCAABB:  SportBasketball,
	LeagueMLB:     SportBaseball,
	LeagueNCAABSB: SportBaseball,
	LeagueFIFAWC:  SportSoccer,
	LeagueEPL:     SportSoccer,
	LeagueNHL:     SportHockey,
	LeagueNCAAHKY: SportHockey,
}

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
