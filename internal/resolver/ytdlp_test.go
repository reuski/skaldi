package resolver

import (
	"encoding/json"
	"testing"

	"skaldi/internal/bootstrap"
)

func TestParseYtDlpResponse(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		expected Track
	}{
		{
			name: "complete_youtube_response",
			json: `{
				"id": "dQw4w9WgXcQ",
				"title": "Test Video",
				"duration": 212.0,
				"uploader": "Test Channel",
				"thumbnail": "https://example.com/thumb.jpg",
				"webpage_url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
				"url": "dQw4w9WgXcQ",
				"ie_key": "Youtube"
			}`,
			expected: Track{
				Title:      "Test Video",
				Duration:   212.0,
				Uploader:   "Test Channel",
				Thumbnail:  "https://example.com/thumb.jpg",
				WebpageURL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			},
		},
		{
			name: "response_without_webpage_url",
			json: `{
				"id": "abc123",
				"title": "Another Video",
				"duration": 300.0,
				"uploader": "Another Channel",
				"url": "https://example.com/video.mp4"
			}`,
			expected: Track{
				Title:      "Another Video",
				Duration:   300.0,
				Uploader:   "Another Channel",
				WebpageURL: "https://example.com/video.mp4",
			},
		},
		{
			name: "youtube_with_video_id_only",
			json: `{
				"id": "xyz789",
				"title": "Short Video",
				"duration": 60.0,
				"ie_key": "Youtube"
			}`,
			expected: Track{
				Title:      "Short Video",
				Duration:   60.0,
				WebpageURL: "https://www.youtube.com/watch?v=xyz789",
			},
		},
		{
			name: "minimal_response",
			json: `{
				"title": "Minimal"
			}`,
			expected: Track{
				Title: "Minimal",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var resp ytDlpResponse
			if err := json.Unmarshal([]byte(tc.json), &resp); err != nil {
				t.Fatalf("Failed to unmarshal JSON: %v", err)
			}

			track := Track{
				Title:      resp.Title,
				Duration:   resp.Duration,
				Uploader:   resp.Uploader,
				Thumbnail:  resp.Thumbnail,
				WebpageURL: resp.WebpageURL,
			}

			if track.WebpageURL == "" {
				if resp.URL != "" {
					if resp.IEKey == "Youtube" && len(resp.URL) == 11 {
						track.WebpageURL = "https://www.youtube.com/watch?v=" + resp.URL
					} else {
						track.WebpageURL = resp.URL
					}
				} else if resp.ID != "" {
					if resp.IEKey == "Youtube" {
						track.WebpageURL = "https://www.youtube.com/watch?v=" + resp.ID
					} else {
						track.WebpageURL = resp.ID
					}
				}
			}

			if track.Title != tc.expected.Title {
				t.Errorf("Title = %q, want %q", track.Title, tc.expected.Title)
			}

			if track.Duration != tc.expected.Duration {
				t.Errorf("Duration = %f, want %f", track.Duration, tc.expected.Duration)
			}

			if track.Uploader != tc.expected.Uploader {
				t.Errorf("Uploader = %q, want %q", track.Uploader, tc.expected.Uploader)
			}

			if track.WebpageURL != tc.expected.WebpageURL {
				t.Errorf("WebpageURL = %q, want %q", track.WebpageURL, tc.expected.WebpageURL)
			}
		})
	}
}

func TestTrack_Struct(t *testing.T) {
	track := Track{
		Title:      "Test Title",
		Duration:   180.5,
		Uploader:   "Test Uploader",
		Thumbnail:  "https://example.com/thumb.jpg",
		URL:        "https://example.com/video.mp4",
		WebpageURL: "https://example.com/watch",
		IsLive:     true,
	}

	data, err := json.Marshal(track)
	if err != nil {
		t.Fatalf("Failed to marshal Track: %v", err)
	}

	var unmarshaled Track
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal Track: %v", err)
	}

	if unmarshaled.Title != track.Title {
		t.Errorf("Title mismatch: got %q, want %q", unmarshaled.Title, track.Title)
	}

	if unmarshaled.Duration != track.Duration {
		t.Errorf("Duration mismatch: got %f, want %f", unmarshaled.Duration, track.Duration)
	}

	if unmarshaled.IsLive != track.IsLive {
		t.Errorf("IsLive mismatch: got %v, want %v", unmarshaled.IsLive, track.IsLive)
	}
}

func TestResolver_New(t *testing.T) {
	cfg := &bootstrap.Config{
		CacheDir: "/tmp/test",
	}

	r := New(cfg)
	if r == nil {
		t.Error("New() returned nil")
	}

	if r == nil {
		t.Fatal("New() returned nil")
	}
	if r.cfg != cfg {
		t.Error("Resolver config mismatch")
	}
}
