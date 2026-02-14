package resolver

import (
	"encoding/json"
	"testing"

	"skaldi/internal/bootstrap"
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
		})
	}
}

func TestExtractVideoID(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://www.youtube.com/watch?v=abc123", "abc123"},
		{"https://youtu.be/xyz789", ""},
		{"", ""},
		{"invalid-url", ""},
	}

	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			got := extractVideoID(tc.url)
			if got != tc.expected {
				t.Errorf("extractVideoID(%q) = %q, want %q", tc.url, got, tc.expected)
			}
		})
	}
}

func TestDedup(t *testing.T) {
	primary := []Track{
		{Title: "First", WebpageURL: "https://youtube.com/watch?v=1"},
		{Title: "Second", WebpageURL: "https://youtube.com/watch?v=2"},
	}
	secondary := []Track{
		{Title: "Duplicate", WebpageURL: "https://youtube.com/watch?v=1"},
		{Title: "Third", WebpageURL: "https://youtube.com/watch?v=3"},
	}

	result := dedup(primary, secondary)

	if len(result) != 3 {
		t.Errorf("dedup returned %d tracks, want 3", len(result))
	}

	expected := []string{"First", "Second", "Third"}
	for i, title := range expected {
		if result[i].Title != title {
			t.Errorf("result[%d].Title = %q, want %q", i, result[i].Title, title)
		}
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
	r := New(cfg)
	if r == nil {
		t.Fatal("New() returned nil")
	}
	if r.cfg != cfg {
		t.Error("Config not set correctly")
	}
}
