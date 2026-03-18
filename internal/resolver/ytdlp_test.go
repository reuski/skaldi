// SPDX-License-Identifier: AGPL-3.0-or-later

package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/reuski/skaldi/internal/bootstrap"
)

func TestTrackFromResponse(t *testing.T) {
	tests := []struct {
		name     string
		resp     ytDlpResponse
		expected Track
	}{
		{
			name: "ytmusic_result",
			resp: ytDlpResponse{
				ID:         "abc123",
				Title:      "Maniac",
				Artist:     "Michael Sembello",
				Duration:   256,
				Uploader:   "Michael Sembello - Topic",
				WebpageURL: "https://music.youtube.com/watch?v=abc123",
				IEKey:      "Youtube",
			},
			expected: Track{
				Title:      "Maniac",
				Artist:     "Michael Sembello",
				Duration:   256,
				Uploader:   "Michael Sembello - Topic",
				WebpageURL: "https://music.youtube.com/watch?v=abc123",
				Source:     SourceYouTube,
			},
		},
		{
			name: "regular_youtube",
			resp: ytDlpResponse{
				ID:         "xyz789",
				Title:      "Some Video",
				Duration:   300,
				Uploader:   "Channel Name",
				WebpageURL: "https://www.youtube.com/watch?v=xyz789",
				IEKey:      "Youtube",
			},
			expected: Track{
				Title:      "Some Video",
				Artist:     "Channel Name",
				Duration:   300,
				Uploader:   "Channel Name",
				WebpageURL: "https://www.youtube.com/watch?v=xyz789",
				Source:     SourceYouTube,
			},
		},
		{
			name: "duration_string_fallback",
			resp: ytDlpResponse{
				ID:             "music123",
				Title:          "Song Title",
				DurationString: "3:45",
				Uploader:       "Artist Name - Topic",
				WebpageURL:     "https://music.youtube.com/watch?v=music123",
				IEKey:          "Youtube",
			},
			expected: Track{
				Title:      "Song Title",
				Artist:     "Artist Name - Topic",
				Duration:   225,
				Uploader:   "Artist Name - Topic",
				WebpageURL: "https://music.youtube.com/watch?v=music123",
				Source:     SourceYouTube,
			},
		},
		{
			name: "no_webpage_url_uses_id",
			resp: ytDlpResponse{
				ID:       "def456",
				Title:    "Video Title",
				Duration: 180,
				IEKey:    "Youtube",
			},
			expected: Track{
				Title:      "Video Title",
				Duration:   180,
				WebpageURL: "https://www.youtube.com/watch?v=def456",
				Source:     SourceYouTube,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := trackFromResponse(tc.resp)

			if got.Title != tc.expected.Title {
				t.Errorf("Title = %q, want %q", got.Title, tc.expected.Title)
			}
			if got.Artist != tc.expected.Artist {
				t.Errorf("Artist = %q, want %q", got.Artist, tc.expected.Artist)
			}
			if got.Duration != tc.expected.Duration {
				t.Errorf("Duration = %f, want %f", got.Duration, tc.expected.Duration)
			}
			if got.WebpageURL != tc.expected.WebpageURL {
				t.Errorf("WebpageURL = %q, want %q", got.WebpageURL, tc.expected.WebpageURL)
			}
			if got.Source != tc.expected.Source {
				t.Errorf("Source = %q, want %q", got.Source, tc.expected.Source)
			}
		})
	}
}

func TestTrackStruct(t *testing.T) {
	track := Track{
		Title:      "Test",
		Artist:     "Test Artist",
		Duration:   180,
		Uploader:   "Uploader",
		WebpageURL: "https://example.com",
	}

	data, err := json.Marshal(track)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded Track
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Artist != track.Artist {
		t.Errorf("Artist mismatch: got %q, want %q", decoded.Artist, track.Artist)
	}
}

func TestResolverNew(t *testing.T) {
	cfg := &bootstrap.Config{CacheDir: "/tmp/test"}
	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	if r == nil {
		t.Fatal("New() returned nil")
	} else if r.cfg != cfg {
		t.Error("Config not set correctly")
	}
}

func TestResolverSearch_InvalidMode(t *testing.T) {
	r, err := New(&bootstrap.Config{})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	_, err = r.Search(t.Context(), "test", 5, "invalid")
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

func TestSearchTypeahead_IgnoresSubsonicFailure(t *testing.T) {
	r := &Resolver{
		cfg: &bootstrap.Config{},
		suggestClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				body := `["query",["test suggestion"],[],[]]`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
				}, nil
			}),
		},
		subsonic: &SubsonicClient{
			httpClient: &http.Client{
				Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, errors.New("dial tcp: connection refused")
				}),
			},
			timeout: 20 * time.Millisecond,
		},
	}

	resultCh := r.searchTypeahead(context.Background(), "test", 5)

	var results []SearchResult
	for result := range resultCh {
		results = append(results, result)
	}

	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if len(results[0].Suggestions) != 1 || results[0].Suggestions[0] != "test suggestion" {
		t.Fatalf("suggestions = %#v, want [\"test suggestion\"]", results[0].Suggestions)
	}
	if len(results[0].Tracks) != 0 {
		t.Fatalf("tracks = %#v, want none", results[0].Tracks)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
