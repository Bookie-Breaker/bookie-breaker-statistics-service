// Package ids derives deterministic UUIDs for external entities. The
// contract requires UUID identifiers while sources use their own id spaces
// (NBA numeric team ids, 10-character game ids). UUIDv5 over a fixed
// namespace makes the mapping stable across restarts and services with no
// stored state; payloads also carry the raw ids under external_ids.
package ids

import "github.com/google/uuid"

// namespace is a project-wide constant; changing it changes every derived id.
var namespace = uuid.MustParse("f6b1a3e8-9d4c-4b6f-8a2e-5c7d0e1f2a3b")

func derive(kind, league, externalID string) string {
	return uuid.NewSHA1(namespace, []byte(league+":"+kind+":"+externalID)).String()
}

func Team(league, externalID string) string   { return derive("team", league, externalID) }
func Player(league, externalID string) string { return derive("player", league, externalID) }
func Game(league, externalID string) string   { return derive("game", league, externalID) }
func Venue(league, name string) string        { return derive("venue", league, name) }
