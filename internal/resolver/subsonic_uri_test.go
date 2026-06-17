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

func TestSubsonicAlbumURIRoundTrip(t *testing.T) {
	raw := BuildSubsonicAlbumURI("personal", "album/123")
	ref, ok := ParseSubsonicAlbumURI(raw)
	if !ok {
		t.Fatalf("ParseSubsonicAlbumURI(%q) = not ok", raw)
	}
	if ref.LibraryID != "personal" {
		t.Fatalf("LibraryID = %q, want personal", ref.LibraryID)
	}
	if ref.AlbumID != "album/123" {
		t.Fatalf("AlbumID = %q, want album/123", ref.AlbumID)
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

func TestParseSubsonicAlbumURIInvalid(t *testing.T) {
	cases := []string{
		"",
		"https://example.com/x",
		"skaldi+subsonic-album:///album",
		"skaldi+subsonic-album://lib/",
	}
	for _, raw := range cases {
		if _, ok := ParseSubsonicAlbumURI(raw); ok {
			t.Fatalf("ParseSubsonicAlbumURI(%q) = ok, want not ok", raw)
		}
	}
}
