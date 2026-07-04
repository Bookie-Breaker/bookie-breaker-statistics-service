package espn

// injuriesResponse is the shape of /apis/site/v2/sports/basketball/nba/injuries:
// one entry per team, each with its current injury list.
type injuriesResponse struct {
	Injuries []teamInjuries `json:"injuries"`
}

type teamInjuries struct {
	ID          string         `json:"id"`
	DisplayName string         `json:"displayName"`
	Injuries    []injuryDetail `json:"injuries"`
}

type injuryDetail struct {
	Status       string `json:"status"`
	Date         string `json:"date"`
	ShortComment string `json:"shortComment"`
	LongComment  string `json:"longComment"`
	Athlete      struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
		Position    struct {
			Abbreviation string `json:"abbreviation"`
		} `json:"position"`
	} `json:"athlete"`
	Details struct {
		Type       string `json:"type"`
		ReturnDate string `json:"returnDate"`
	} `json:"details"`
}
