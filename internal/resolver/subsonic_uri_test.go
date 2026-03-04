// SPDX-License-Identifier: AGPL-3.0-or-later

package resolver

import "testing"

func TestSubsonicURIRoundTrip(t *testing.T) {
	raw := BuildSubsonicURI("personal", "track/123")
	ref, ok := ParseSubsonicURI(raw)
	if !ok {
		t.Fatalf("ParseSubsonicURI(%q) = not ok", raw)
	}
	if ref.LibraryID != "personal" {
		t.Fatalf("LibraryID = %q, want personal", ref.LibraryID)
	}
	if ref.TrackID != "track/123" {
		t.Fatalf("TrackID = %q, want track/123", ref.TrackID)
	}
}

func TestParseSubsonicURIInvalid(t *testing.T) {
	cases := []string{
		"",
		"https://example.com/x",
		"skaldi+subsonic:///track",
		"skaldi+subsonic://lib/",
	}
	for _, raw := range cases {
		if _, ok := ParseSubsonicURI(raw); ok {
			t.Fatalf("ParseSubsonicURI(%q) = ok, want not ok", raw)
		}
	}
}
