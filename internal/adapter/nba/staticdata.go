package nba

// StaticTeam is franchise metadata that changes rarely enough to embed,
// mirroring nba_api's static teams module. Records and stats come from
// leaguedashteamstats; this seed only supplies identity and venue info.
type StaticTeam struct {
	NBAID      string
	FullName   string
	City       string
	Mascot     string
	Tricode    string
	Conference string
	Division   string
	Arena      string
	ArenaCity  string
	ArenaState string
}

// StaticTeams lists the 30 NBA franchises keyed by NBA team id.
var StaticTeams = []StaticTeam{
	{"1610612737", "Atlanta Hawks", "Atlanta", "Hawks", "ATL", "East", "Southeast", "State Farm Arena", "Atlanta", "GA"},
	{"1610612738", "Boston Celtics", "Boston", "Celtics", "BOS", "East", "Atlantic", "TD Garden", "Boston", "MA"},
	{"1610612739", "Cleveland Cavaliers", "Cleveland", "Cavaliers", "CLE", "East", "Central", "Rocket Arena", "Cleveland", "OH"},
	{"1610612740", "New Orleans Pelicans", "New Orleans", "Pelicans", "NOP", "West", "Southwest", "Smoothie King Center", "New Orleans", "LA"},
	{"1610612741", "Chicago Bulls", "Chicago", "Bulls", "CHI", "East", "Central", "United Center", "Chicago", "IL"},
	{"1610612742", "Dallas Mavericks", "Dallas", "Mavericks", "DAL", "West", "Southwest", "American Airlines Center", "Dallas", "TX"},
	{"1610612743", "Denver Nuggets", "Denver", "Nuggets", "DEN", "West", "Northwest", "Ball Arena", "Denver", "CO"},
	{"1610612744", "Golden State Warriors", "Golden State", "Warriors", "GSW", "West", "Pacific", "Chase Center", "San Francisco", "CA"},
	{"1610612745", "Houston Rockets", "Houston", "Rockets", "HOU", "West", "Southwest", "Toyota Center", "Houston", "TX"},
	{"1610612746", "LA Clippers", "Los Angeles", "Clippers", "LAC", "West", "Pacific", "Intuit Dome", "Inglewood", "CA"},
	{"1610612747", "Los Angeles Lakers", "Los Angeles", "Lakers", "LAL", "West", "Pacific", "Crypto.com Arena", "Los Angeles", "CA"},
	{"1610612748", "Miami Heat", "Miami", "Heat", "MIA", "East", "Southeast", "Kaseya Center", "Miami", "FL"},
	{"1610612749", "Milwaukee Bucks", "Milwaukee", "Bucks", "MIL", "East", "Central", "Fiserv Forum", "Milwaukee", "WI"},
	{"1610612750", "Minnesota Timberwolves", "Minnesota", "Timberwolves", "MIN", "West", "Northwest", "Target Center", "Minneapolis", "MN"},
	{"1610612751", "Brooklyn Nets", "Brooklyn", "Nets", "BKN", "East", "Atlantic", "Barclays Center", "Brooklyn", "NY"},
	{"1610612752", "New York Knicks", "New York", "Knicks", "NYK", "East", "Atlantic", "Madison Square Garden", "New York", "NY"},
	{"1610612753", "Orlando Magic", "Orlando", "Magic", "ORL", "East", "Southeast", "Kia Center", "Orlando", "FL"},
	{"1610612754", "Indiana Pacers", "Indiana", "Pacers", "IND", "East", "Central", "Gainbridge Fieldhouse", "Indianapolis", "IN"},
	{"1610612755", "Philadelphia 76ers", "Philadelphia", "76ers", "PHI", "East", "Atlantic", "Xfinity Mobile Arena", "Philadelphia", "PA"},
	{"1610612756", "Phoenix Suns", "Phoenix", "Suns", "PHX", "West", "Pacific", "Mortgage Matchup Center", "Phoenix", "AZ"},
	{"1610612757", "Portland Trail Blazers", "Portland", "Trail Blazers", "POR", "West", "Northwest", "Moda Center", "Portland", "OR"},
	{"1610612758", "Sacramento Kings", "Sacramento", "Kings", "SAC", "West", "Pacific", "Golden 1 Center", "Sacramento", "CA"},
	{"1610612759", "San Antonio Spurs", "San Antonio", "Spurs", "SAS", "West", "Southwest", "Frost Bank Center", "San Antonio", "TX"},
	{"1610612760", "Oklahoma City Thunder", "Oklahoma City", "Thunder", "OKC", "West", "Northwest", "Paycom Center", "Oklahoma City", "OK"},
	{"1610612761", "Toronto Raptors", "Toronto", "Raptors", "TOR", "East", "Atlantic", "Scotiabank Arena", "Toronto", "ON"},
	{"1610612762", "Utah Jazz", "Utah", "Jazz", "UTA", "West", "Northwest", "Delta Center", "Salt Lake City", "UT"},
	{"1610612763", "Memphis Grizzlies", "Memphis", "Grizzlies", "MEM", "West", "Southwest", "FedExForum", "Memphis", "TN"},
	{"1610612764", "Washington Wizards", "Washington", "Wizards", "WAS", "East", "Southeast", "Capital One Arena", "Washington", "DC"},
	{"1610612765", "Detroit Pistons", "Detroit", "Pistons", "DET", "East", "Central", "Little Caesars Arena", "Detroit", "MI"},
	{"1610612766", "Charlotte Hornets", "Charlotte", "Hornets", "CHA", "East", "Southeast", "Spectrum Center", "Charlotte", "NC"},
}

// StaticTeamByNBAID indexes the seed by NBA team id.
var StaticTeamByNBAID = func() map[string]StaticTeam {
	m := make(map[string]StaticTeam, len(StaticTeams))
	for _, t := range StaticTeams {
		m[t.NBAID] = t
	}
	return m
}()
