package model

import "time"

// RawAPIResponse is one archived external API response, persisted to the
// shared public.raw_api_responses hypertable.
type RawAPIResponse struct {
	Service      string    `json:"service"`
	Source       string    `json:"source"`
	Endpoint     string    `json:"endpoint"`
	HTTPStatus   int       `json:"http_status"`
	RequestBody  *string   `json:"request_body,omitempty"`
	ResponseBody string    `json:"response_body"`
	CapturedAt   time.Time `json:"captured_at"`
}
