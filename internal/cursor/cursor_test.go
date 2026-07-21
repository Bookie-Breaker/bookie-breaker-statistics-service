package cursor

import "testing"

func TestRoundTrip(t *testing.T) {
	encoded := Encode("teams", 50)
	offset, err := Decode(encoded, "teams")
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}
	if offset != 50 {
		t.Errorf("offset = %d, want 50", offset)
	}
}

func TestDecodeRejectsBadCursors(t *testing.T) {
	cases := map[string]struct {
		cursor     string
		collection string
	}{
		"garbage":           {"!!!", "teams"},
		"wrong collection":  {Encode("players", 10), "teams"},
		"negative offset":   {Encode("teams", -1), "teams"},
		"empty":             {"", "teams"},
		"non-cursor base64": {"aGVsbG8", "teams"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(tc.cursor, tc.collection); err == nil {
				t.Errorf("expected error for %q", tc.cursor)
			}
		})
	}
}

func TestPage(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}

	page, hasMore, next, err := Page(items, "nums", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0] != 1 || !hasMore || next == "" {
		t.Fatalf("first page = %v hasMore=%v next=%q", page, hasMore, next)
	}

	page, hasMore, next, err = Page(items, "nums", next, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0] != 3 || !hasMore {
		t.Fatalf("second page = %v hasMore=%v", page, hasMore)
	}

	page, hasMore, next, err = Page(items, "nums", next, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0] != 5 || hasMore || next != "" {
		t.Fatalf("last page = %v hasMore=%v next=%q", page, hasMore, next)
	}

	// Offset past the end yields an empty page, not an error.
	page, hasMore, _, err = Page(items, "nums", Encode("nums", 99), 2)
	if err != nil || len(page) != 0 || hasMore {
		t.Fatalf("past-end page = %v hasMore=%v err=%v", page, hasMore, err)
	}
}

func TestPageRejectsInvalidCursor(t *testing.T) {
	items := []int{1, 2, 3}
	page, hasMore, next, err := Page(items, "nums", "!!!not-a-cursor", 2)
	if err == nil {
		t.Fatal("expected error for invalid cursor")
	}
	if page != nil || hasMore || next != "" {
		t.Fatalf("expected zero-value results on error, got page=%v hasMore=%v next=%q", page, hasMore, next)
	}
}
