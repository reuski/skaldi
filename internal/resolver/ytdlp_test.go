// SPDX-License-Identifier: AGPL-3.0-or-later

package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
				ID:         "abc123",
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
				ID:         "xyz789",
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
				ID:         "music123",
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
				ID:         "def456",
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

			if got.ID != tc.expected.ID {
				t.Errorf("ID = %q, want %q", got.ID, tc.expected.ID)
			}
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
		ID:         "abc123",
		Title:      "Test",
		Artist:     "Test Artist",
		Duration:   180,
		Uploader:   "Uploader",
		WebpageURL: "https://example.com",
		Source:     SourceYouTube,
	}

	data, err := json.Marshal(track)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded Track
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.ID != track.ID {
		t.Fatalf("ID = %q, want %q", decoded.ID, track.ID)
	}
	if decoded.Artist != track.Artist {
		t.Fatalf("Artist = %q, want %q", decoded.Artist, track.Artist)
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
	}
	if r.cfg != cfg {
		t.Fatal("Config not set correctly")
	}
}

func TestParseSearchIntentInvalid(t *testing.T) {
	_, err := ParseSearchIntent("invalid")
	if err == nil {
		t.Fatal("expected invalid intent error")
	}
}

func TestTrackPlayableURL(t *testing.T) {
	yt := Track{Source: SourceYouTube, WebpageURL: "https://youtube.example/watch?v=1"}
	if got := yt.PlayableURL(); got != yt.WebpageURL {
		t.Fatalf("youtube PlayableURL = %q, want %q", got, yt.WebpageURL)
	}

	subsonic := Track{Source: SourceSubsonic, URL: "skaldi+subsonic://personal/track-1"}
	if got := subsonic.PlayableURL(); got != subsonic.URL {
		t.Fatalf("subsonic PlayableURL = %q, want %q", got, subsonic.URL)
	}
}

func TestSearchTypeaheadIgnoresSubsonicFailure(t *testing.T) {
	r := newTestResolver(t)
	r.suggestClient = &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			body := `["query",["test suggestion"],[],[]]`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	r.subsonic = &SubsonicClient{
		httpClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("dial tcp: connection refused")
			}),
		},
		timeout: 20 * time.Millisecond,
	}

	resultCh, err := r.Search(context.Background(), "test", SearchIntentTypeahead)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	batches := collectSearchBatches(t, resultCh)
	if len(batches) != 2 {
		t.Fatalf("batch count = %d, want 2", len(batches))
	}

	suggestionsBatch := lastBatchForBucket(batches, SearchBucketSuggestions)
	if len(suggestionsBatch.Suggestions) != 1 || suggestionsBatch.Suggestions[0] != "test suggestion" {
		t.Fatalf("suggestions = %#v, want [\"test suggestion\"]", suggestionsBatch.Suggestions)
	}

	externalBatch := lastBatchForBucket(batches, SearchBucketExternal)
	if len(externalBatch.Hits) != 0 {
		t.Fatalf("external hits = %#v, want none", externalBatch.Hits)
	}
}

func TestSearchTypeaheadSkipsRemoteSuggestionsForSingleRuneQuery(t *testing.T) {
	r := newTestResolver(t)
	var calls atomic.Int32
	r.suggestClient = &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("unexpected remote call")
		}),
	}

	resultCh, err := r.Search(context.Background(), "a", SearchIntentTypeahead)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	batches := collectSearchBatches(t, resultCh)
	if calls.Load() != 0 {
		t.Fatalf("suggestions remote calls = %d, want 0", calls.Load())
	}
	suggestionsBatch := lastBatchForBucket(batches, SearchBucketSuggestions)
	if len(suggestionsBatch.Suggestions) != 0 {
		t.Fatalf("suggestions = %#v, want none", suggestionsBatch.Suggestions)
	}
}

func TestSearchResultsEmitFixedBucketsAndMergeYTMusic(t *testing.T) {
	r := newResolverWithVideoFixture(t)
	r.subsonic = newFakeSubsonicClient([]subsonicSong{
		{ID: "lib-1", Title: "Library Song", Artist: "Library Artist", Duration: 180},
	})

	resultCh, err := r.Search(context.Background(), "test song", SearchIntentResults)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	batches := collectSearchBatches(t, resultCh)

	externalBatch := lastBatchForBucket(batches, SearchBucketExternal)
	if !externalBatch.Complete {
		t.Fatal("external batch should be complete")
	}
	if len(externalBatch.Hits) != 1 {
		t.Fatalf("external hits = %d, want 1", len(externalBatch.Hits))
	}
	if externalBatch.Hits[0].QueueURL != "skaldi+subsonic://personal/lib-1" {
		t.Fatalf("external queue_url = %q", externalBatch.Hits[0].QueueURL)
	}

	youtubeFinal := lastBatchForBucket(batches, SearchBucketYouTube)
	if !youtubeFinal.Complete {
		t.Fatal("youtube final batch should be complete")
	}
	if len(youtubeFinal.Hits) != 1 {
		t.Fatalf("youtube hits = %d, want 1", len(youtubeFinal.Hits))
	}
	if youtubeFinal.Hits[0].ID != "shared-1" {
		t.Fatalf("youtube id = %q, want shared-1", youtubeFinal.Hits[0].ID)
	}
	if youtubeFinal.Hits[0].Artist != "Precise Artist" {
		t.Fatalf("youtube artist = %q, want Precise Artist", youtubeFinal.Hits[0].Artist)
	}
	if youtubeFinal.Hits[0].Duration != 201 {
		t.Fatalf("youtube duration = %f, want 201", youtubeFinal.Hits[0].Duration)
	}
	if youtubeFinal.Hits[0].QueueURL != "https://www.youtube.com/watch?v=shared-1" {
		t.Fatalf("youtube queue_url = %q", youtubeFinal.Hits[0].QueueURL)
	}

	ytMusicFinal := lastBatchForBucket(batches, SearchBucketYTMusic)
	if !ytMusicFinal.Complete {
		t.Fatal("ytmusic final batch should be complete")
	}
	if len(ytMusicFinal.Hits) != 1 {
		t.Fatalf("ytmusic hits = %d, want 1", len(ytMusicFinal.Hits))
	}
	if ytMusicFinal.Hits[0].ID != "music-only-2" {
		t.Fatalf("ytmusic id = %q, want music-only-2", ytMusicFinal.Hits[0].ID)
	}
}

func TestSearchCacheCoalescesConcurrentLoads(t *testing.T) {
	cache := newSearchCache[string](time.Minute, 4)
	var calls atomic.Int32

	loader := func(ctx context.Context) (string, error) {
		calls.Add(1)
		time.Sleep(40 * time.Millisecond)
		return "value", nil
	}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := cache.GetOrLoad(context.Background(), "same-key", loader)
			if err != nil {
				t.Errorf("GetOrLoad failed: %v", err)
				return
			}
			if value != "value" {
				t.Errorf("value = %q, want value", value)
			}
		}()
	}
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("loader calls = %d, want 1", calls.Load())
	}
}

func collectSearchBatches(t *testing.T, ch <-chan SearchBatch) []SearchBatch {
	t.Helper()
	var batches []SearchBatch
	for batch := range ch {
		batches = append(batches, batch)
	}
	return batches
}

func lastBatchForBucket(batches []SearchBatch, bucket SearchBucket) SearchBatch {
	for idx := len(batches) - 1; idx >= 0; idx-- {
		if batches[idx].Bucket == bucket {
			return batches[idx]
		}
	}
	return SearchBatch{}
}

func newTestResolver(t *testing.T) *Resolver {
	t.Helper()
	cfg := &bootstrap.Config{
		CacheDir:   t.TempDir(),
		BinDir:     t.TempDir(),
		UvBinDir:   t.TempDir(),
		MpvSocket:  filepath.Join(t.TempDir(), "mpv.sock"),
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return r
}

func newResolverWithVideoFixture(t *testing.T) *Resolver {
	t.Helper()
	r := newTestResolver(t)
	writeExecutable(t, r.cfg.ShimPath(), `#!/bin/sh
last=""
for arg in "$@"; do
  last="$arg"
done
case "$last" in
  ytsearch8:*)
    printf '%s\n' '{"id":"shared-1","title":"Test Song","uploader":"Loose Channel","webpage_url":"https://www.youtube.com/watch?v=shared-1","ie_key":"Youtube"}'
    ;;
  https://music.youtube.com/search\?q=*)
    sleep 0.05
    printf '%s\n' '{"id":"shared-1","title":"Test Song","artist":"Precise Artist","duration":201,"thumbnail":"https://img.example/shared-1.jpg","webpage_url":"https://music.youtube.com/watch?v=shared-1","ie_key":"Youtube"}'
    printf '%s\n' '{"id":"music-only-2","title":"Other Song","artist":"Other Artist","duration":202,"thumbnail":"https://img.example/music-only-2.jpg","webpage_url":"https://music.youtube.com/watch?v=music-only-2","ie_key":"Youtube"}'
    ;;
esac
`)
	return r
}

func newFakeSubsonicClient(songs []subsonicSong) *SubsonicClient {
	responseBody, err := json.Marshal(map[string]any{
		"subsonic-response": map[string]any{
			"status": "ok",
			"searchResult3": map[string]any{
				"song": songs,
			},
		},
	})
	if err != nil {
		panic(err)
	}

	return &SubsonicClient{
		cfg: openSubsonicConfig{
			LibraryID: "personal",
			BaseURL:   "https://demo.example.com",
			Username:  "alice",
			Token:     "secret",
			TimeoutMS: 2500,
		},
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(responseBody))),
					Header:     make(http.Header),
				}, nil
			}),
		},
		timeout: time.Second,
	}
}

func writeExecutable(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
