// Package cursor implements opaque offset cursors over cached collections.
// Collections here are small (30 teams, ~600 players, ~1300 games) and
// fully cached with a deterministic order, so an offset into the filtered
// list is sufficient; drift across a refresh between pages is tolerated.
package cursor

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalid is returned for malformed or mismatched cursors.
var ErrInvalid = errors.New("invalid pagination cursor")

// Encode builds a cursor for the next page of a collection.
func Encode(collection string, offset int) string {
	return base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, "v1|%s|%d", collection, offset))
}

// Decode parses a cursor, validating it belongs to the given collection.
func Decode(cursor, collection string) (int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, ErrInvalid
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 || parts[0] != "v1" || parts[1] != collection {
		return 0, ErrInvalid
	}
	offset, err := strconv.Atoi(parts[2])
	if err != nil || offset < 0 {
		return 0, ErrInvalid
	}
	return offset, nil
}

// Page slices items at the cursor's offset. It returns the page, whether
// more items remain, and the cursor for the next page.
func Page[T any](items []T, collection, cur string, limit int) ([]T, bool, string, error) {
	offset := 0
	if cur != "" {
		var err error
		offset, err = Decode(cur, collection)
		if err != nil {
			return nil, false, "", err
		}
	}
	if offset >= len(items) {
		return []T{}, false, "", nil
	}

	end := offset + limit
	if end >= len(items) {
		return items[offset:], false, "", nil
	}
	return items[offset:end], true, Encode(collection, end), nil
}
