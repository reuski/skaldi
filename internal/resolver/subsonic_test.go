// SPDX-License-Identifier: AGPL-3.0-or-later

package resolver

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSubsonicAuthParams(t *testing.T) {
	client := NewSubsonicClient(openSubsonicConfig{
		LibraryID: "personal",
		BaseURL:   "https://demo.example.com",
		Username:  "alice",
		Token:     "token-secret",
		TimeoutMS: 2500,
	})

	params, err := client.authParams()
	if err != nil {
		t.Fatalf("authParams failed: %v", err)
	}

	salt := params.Get("s")
	hash := params.Get("t")
	if salt == "" || hash == "" {
		t.Fatalf("salt/hash missing: s=%q t=%q", salt, hash)
	}

	expected := md5.Sum([]byte("token-secret" + salt))
	if hash != hex.EncodeToString(expected[:]) {
		t.Fatalf("hash = %q, want %q", hash, hex.EncodeToString(expected[:]))
	}

	encoded := params.Encode()
	if strings.Contains(encoded, "token-secret") {
		t.Fatalf("encoded params leaked token: %s", encoded)
	}
}

func TestSubsonicBuildStreamURL(t *testing.T) {
	client := NewSubsonicClient(openSubsonicConfig{
		LibraryID: "personal",
		BaseURL:   "https://demo.example.com",
		Username:  "alice",
		Token:     "token-secret",
		TimeoutMS: 2500,
	})

	streamURL, err := client.BuildStreamURL("track-1")
	if err != nil {
		t.Fatalf("BuildStreamURL failed: %v", err)
	}

	u, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if u.Path != "/rest/stream.view" {
		t.Fatalf("path = %q, want /rest/stream.view", u.Path)
	}
	if u.Query().Get("id") != "track-1" {
		t.Fatalf("id = %q, want track-1", u.Query().Get("id"))
	}
	if u.Query().Get("u") != "alice" {
		t.Fatalf("u = %q, want alice", u.Query().Get("u"))
	}
	if strings.Contains(streamURL, "token-secret") {
		t.Fatalf("streamURL leaked token: %s", streamURL)
	}
}

func TestSubsonicSearchReturnsAlbumsBeforeTracks(t *testing.T) {
	client := NewSubsonicClient(openSubsonicConfig{
		LibraryID: "personal",
		BaseURL:   "https://demo.example.com",
		Username:  "alice",
		Token:     "token-secret",
		TimeoutMS: 2500,
	})
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/rest/search3.view" {
				t.Fatalf("path = %q, want /rest/search3.view", req.URL.Path)
			}
			q := req.URL.Query()
			if q.Get("query") != "library" {
				t.Fatalf("query = %q, want library", q.Get("query"))
			}
			if q.Get("artistCount") != "0" || q.Get("albumCount") == "0" || q.Get("songCount") != "8" {
				t.Fatalf("search counts = artist:%q album:%q song:%q", q.Get("artistCount"), q.Get("albumCount"), q.Get("songCount"))
			}
			body := `{"subsonic-response":{"status":"ok","searchResult3":{"album":[{"id":"album-1","name":"Album One","artist":"Album Artist","duration":600,"coverArt":"cover-album","songCount":2}],"song":[{"id":"track-in-album","albumId":"album-1","title":"Album Track","artist":"Album Artist","duration":180,"coverArt":"cover-track"},{"id":"track-standalone","title":"Standalone Track","artist":"Track Artist","duration":181,"coverArt":"cover-standalone"}]}}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	hits, err := client.Search(context.Background(), "library", 8)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(hits))
	}
	if hits[0].Kind != SearchHitKindAlbum {
		t.Fatalf("first kind = %q, want album", hits[0].Kind)
	}
	if hits[0].QueueURL != "" {
		t.Fatalf("album queue_url = %q, want empty", hits[0].QueueURL)
	}
	if hits[0].WebpageURL != "skaldi+subsonic-album://personal/album-1" {
		t.Fatalf("album webpage_url = %q", hits[0].WebpageURL)
	}
	if hits[0].TrackCount != 2 {
		t.Fatalf("album track_count = %d, want 2", hits[0].TrackCount)
	}
	if hits[1].Kind != SearchHitKindTrack {
		t.Fatalf("second kind = %q, want track", hits[1].Kind)
	}
	if hits[1].QueueURL != "skaldi+subsonic://personal/track-standalone" {
		t.Fatalf("track queue_url = %q", hits[1].QueueURL)
	}
}

func TestSubsonicGetAlbumConvertsOrderedTracks(t *testing.T) {
	client := NewSubsonicClient(openSubsonicConfig{
		LibraryID: "personal",
		BaseURL:   "https://demo.example.com",
		Username:  "alice",
		Token:     "token-secret",
		TimeoutMS: 2500,
	})
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/rest/getAlbum.view" {
				t.Fatalf("path = %q, want /rest/getAlbum.view", req.URL.Path)
			}
			if req.URL.Query().Get("id") != "album-1" {
				t.Fatalf("id = %q, want album-1", req.URL.Query().Get("id"))
			}
			body := `{"subsonic-response":{"status":"ok","album":{"id":"album-1","name":"Album One","artist":"Album Artist","coverArt":"cover-album","songCount":2,"song":[{"id":"track-1","title":"First","artist":"Album Artist","duration":101,"coverArt":"cover-1"},{"id":"track-2","title":"Second","artist":"Album Artist","duration":102,"coverArt":"cover-2"}]}}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	album, err := client.GetAlbum(context.Background(), "album-1")
	if err != nil {
		t.Fatalf("GetAlbum failed: %v", err)
	}
	if album.Title != "Album One" || album.Artist != "Album Artist" {
		t.Fatalf("album = %q / %q", album.Title, album.Artist)
	}
	if album.WebpageURL != "skaldi+subsonic-album://personal/album-1" {
		t.Fatalf("album webpage_url = %q", album.WebpageURL)
	}
	if len(album.Tracks) != 2 {
		t.Fatalf("tracks = %d, want 2", len(album.Tracks))
	}
	if album.Tracks[0].Title != "First" || album.Tracks[1].Title != "Second" {
		t.Fatalf("track order = %#v", album.Tracks)
	}
	if album.Tracks[0].QueueURL != "skaldi+subsonic://personal/track-1" {
		t.Fatalf("first queue_url = %q", album.Tracks[0].QueueURL)
	}
}
