// SPDX-License-Identifier: AGPL-3.0-or-later

// Package resolver extracts media metadata using yt-dlp.
package resolver

import (
	"bufio"
	"bytes"
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/reuski/skaldi/internal/bootstrap"
)

const (
	SourceSubsonic = "subsonic"
	SourceYTMusic  = "ytmusic"
	SourceYouTube  = "youtube"

	typeaheadTrackLimit           = 4
	resultsTrackLimit             = 8
	providerSearchLimit           = 12
	ytMusicAlbumSearchLimit       = 4
	ytMusicAlbumEnrichmentLimit   = 2
	ytMusicAlbumPreviewTrackLimit = 40
	ytMusicAlbumReadyGrace        = 150 * time.Millisecond
	maxSuggestionCount            = 8
	minRemoteQueryRunes           = 2
	searchCacheLimit              = 64
	suggestionCacheTTL            = 5 * time.Minute
	externalCacheTTL              = 60 * time.Second
	youtubeCacheTTL               = 2 * time.Minute
	ytMusicCacheTTL               = 2 * time.Minute
	suggestionTimeout             = 2 * time.Second
	externalSearchTimeout         = 2500 * time.Millisecond
	videoSearchTimeout            = 5 * time.Second
)

var ErrInvalidAlbumRef = errors.New("invalid album ref")

type SearchIntent string

const (
	SearchIntentTypeahead SearchIntent = "typeahead"
	SearchIntentResults   SearchIntent = "results"
)

type SearchBucket string

const (
	SearchBucketSuggestions SearchBucket = "suggestions"
	SearchBucketExternal    SearchBucket = "external"
	SearchBucketYouTube     SearchBucket = "youtube"
	SearchBucketYTMusic     SearchBucket = "ytmusic"
)

type SearchHitKind string

const (
	SearchHitKindTrack SearchHitKind = "track"
	SearchHitKindAlbum SearchHitKind = "album"
)

type Track struct {
	ID         string  `json:"id,omitempty"`
	Title      string  `json:"title"`
	Artist     string  `json:"artist"`
	Duration   float64 `json:"duration"`
	Uploader   string  `json:"uploader"`
	Thumbnail  string  `json:"thumbnail"`
	URL        string  `json:"url,omitempty"`
	WebpageURL string  `json:"webpage_url,omitempty"`
	Source     string  `json:"source,omitempty"`
}

type SearchHit struct {
	Kind       SearchHitKind `json:"kind"`
	ID         string        `json:"id"`
	Source     string        `json:"source"`
	Title      string        `json:"title"`
	Artist     string        `json:"artist"`
	Duration   float64       `json:"duration"`
	Thumbnail  string        `json:"thumbnail"`
	WebpageURL string        `json:"webpage_url"`
	QueueURL   string        `json:"queue_url"`
	TrackCount int           `json:"track_count,omitempty"`
}

type Album struct {
	Source     string      `json:"source"`
	Title      string      `json:"title"`
	Artist     string      `json:"artist"`
	Duration   float64     `json:"duration"`
	Thumbnail  string      `json:"thumbnail"`
	WebpageURL string      `json:"webpage_url"`
	TrackCount int         `json:"track_count"`
	Tracks     []SearchHit `json:"tracks"`
}

type SearchBatch struct {
	Intent      SearchIntent `json:"intent"`
	Bucket      SearchBucket `json:"bucket"`
	Complete    bool         `json:"complete"`
	Suggestions []string     `json:"suggestions,omitempty"`
	Hits        []SearchHit  `json:"hits,omitempty"`
}

type ytDlpResponse struct {
	ID               string  `json:"id"`
	Title            string  `json:"title"`
	Artist           string  `json:"artist"`
	Album            string  `json:"album"`
	AlbumArtist      string  `json:"album_artist"`
	Channel          string  `json:"channel"`
	Duration         float64 `json:"duration"`
	DurationString   string  `json:"duration_string"`
	Uploader         string  `json:"uploader"`
	Playlist         string  `json:"playlist"`
	PlaylistTitle    string  `json:"playlist_title"`
	PlaylistUploader string  `json:"playlist_uploader"`
	PlaylistCount    int     `json:"playlist_count"`
	Thumbnail        string  `json:"thumbnail"`
	Thumbnails       []struct {
		URL    string `json:"url"`
		Height int    `json:"height"`
		Width  int    `json:"width"`
	} `json:"thumbnails"`
	WebpageURL string `json:"webpage_url"`
	URL        string `json:"url"`
	IEKey      string `json:"ie_key"`
	LiveStatus string `json:"live_status"`
}

type Resolver struct {
	cfg             *bootstrap.Config
	subsonic        *SubsonicClient
	suggestClient   *http.Client
	warnings        []error
	suggestionCache *searchCache[[]string]
	externalCache   *searchCache[[]SearchHit]
	youtubeCache    *searchCache[[]Track]
	ytMusicCache    *searchCache[[]SearchHit]
}

type cacheResult[T any] struct {
	value T
	err   error
}

type cacheEntry[T any] struct {
	key       string
	value     T
	expiresAt time.Time
}

type searchCache[T any] struct {
	mu       sync.Mutex
	ttl      time.Duration
	limit    int
	order    *list.List
	entries  map[string]*list.Element
	inflight map[string][]chan cacheResult[T]
}

func New(cfg *bootstrap.Config) (*Resolver, error) {
	r := &Resolver{
		cfg:             cfg,
		suggestClient:   &http.Client{Timeout: suggestionTimeout},
		suggestionCache: newSearchCache[[]string](suggestionCacheTTL, searchCacheLimit),
		externalCache:   newSearchCache[[]SearchHit](externalCacheTTL, searchCacheLimit),
		youtubeCache:    newSearchCache[[]Track](youtubeCacheTTL, searchCacheLimit),
		ytMusicCache:    newSearchCache[[]SearchHit](ytMusicCacheTTL, searchCacheLimit),
	}

	if cfg == nil {
		return r, nil
	}

	extCfg, err := loadOpenSubsonicConfig(cfg.ConfigPath)
	if err != nil {
		r.warnings = append(r.warnings, fmt.Errorf("opensubsonic disabled: %w", err))
		return r, nil
	}
	if extCfg != nil {
		r.subsonic = NewSubsonicClient(*extCfg)
	}

	return r, nil
}

func newSearchCache[T any](ttl time.Duration, limit int) *searchCache[T] {
	return &searchCache[T]{
		ttl:      ttl,
		limit:    limit,
		order:    list.New(),
		entries:  make(map[string]*list.Element),
		inflight: make(map[string][]chan cacheResult[T]),
	}
}

func (c *searchCache[T]) GetOrLoad(ctx context.Context, key string, loader func(context.Context) (T, error)) (T, error) {
	var zero T

	c.mu.Lock()
	if elem, ok := c.entries[key]; ok {
		entry := elem.Value.(*cacheEntry[T])
		if time.Now().Before(entry.expiresAt) {
			c.order.MoveToFront(elem)
			value := entry.value
			c.mu.Unlock()
			return value, nil
		}
		c.order.Remove(elem)
		delete(c.entries, key)
	}

	if waiters, ok := c.inflight[key]; ok {
		waitCh := make(chan cacheResult[T], 1)
		c.inflight[key] = append(waiters, waitCh)
		c.mu.Unlock()

		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case result := <-waitCh:
			return result.value, result.err
		}
	}

	c.inflight[key] = nil
	c.mu.Unlock()

	value, err := loader(ctx)

	c.mu.Lock()
	waiters := c.inflight[key]
	delete(c.inflight, key)
	if err == nil {
		c.storeLocked(key, value)
	}
	c.mu.Unlock()

	result := cacheResult[T]{value: value, err: err}
	for _, waitCh := range waiters {
		waitCh <- result
		close(waitCh)
	}

	return value, err
}

func (c *searchCache[T]) storeLocked(key string, value T) {
	if elem, ok := c.entries[key]; ok {
		c.order.Remove(elem)
		delete(c.entries, key)
	}

	entry := &cacheEntry[T]{
		key:       key,
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.entries[key] = c.order.PushFront(entry)

	for len(c.entries) > c.limit {
		back := c.order.Back()
		if back == nil {
			break
		}
		stale := back.Value.(*cacheEntry[T])
		delete(c.entries, stale.key)
		c.order.Remove(back)
	}
}

func (r *Resolver) Warnings() []error {
	if len(r.warnings) == 0 {
		return nil
	}
	out := make([]error, len(r.warnings))
	copy(out, r.warnings)
	return out
}

func (r *Resolver) FetchSubsonicCoverArt(ctx context.Context, libraryID, coverArtID string) ([]byte, string, error) {
	if r.subsonic == nil {
		return nil, "", fmt.Errorf("opensubsonic source is not configured")
	}
	if libraryID != r.subsonic.LibraryID() {
		return nil, "", fmt.Errorf("unknown opensubsonic library: %s", libraryID)
	}
	return r.subsonic.FetchCoverArt(ctx, coverArtID)
}

func ParseSearchIntent(raw string) (SearchIntent, error) {
	switch SearchIntent(raw) {
	case SearchIntentTypeahead, SearchIntentResults:
		return SearchIntent(raw), nil
	default:
		return "", fmt.Errorf("invalid search intent: %s", raw)
	}
}

func (r *Resolver) Search(ctx context.Context, query string, intent SearchIntent) (<-chan SearchBatch, error) {
	intent, err := ParseSearchIntent(string(intent))
	if err != nil {
		return nil, err
	}

	normalized := normalizeSearchQuery(query)
	if normalized == "" {
		return nil, fmt.Errorf("query is required")
	}

	resultCh := make(chan SearchBatch, 6)
	go func() {
		defer close(resultCh)
		switch intent {
		case SearchIntentTypeahead:
			r.streamTypeahead(ctx, normalized, resultCh)
		case SearchIntentResults:
			r.streamResults(ctx, normalized, resultCh)
		}
	}()

	return resultCh, nil
}

func (r *Resolver) streamTypeahead(ctx context.Context, query string, resultCh chan<- SearchBatch) {
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		suggestions := []string{}
		if shouldSearchRemote(query) {
			items, err := r.loadSuggestions(ctx, query)
			if err == nil {
				suggestions = items
			}
		}
		emitSearchBatch(ctx, resultCh, SearchBatch{
			Intent:      SearchIntentTypeahead,
			Bucket:      SearchBucketSuggestions,
			Complete:    true,
			Suggestions: suggestions,
		})
	}()

	if r.subsonic != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hits := []SearchHit{}
			externalHits, err := r.loadExternalHits(ctx, query)
			if err == nil {
				hits = trimSearchHits(trackHitsOnly(externalHits), typeaheadTrackLimit)
			}
			emitSearchBatch(ctx, resultCh, SearchBatch{
				Intent:   SearchIntentTypeahead,
				Bucket:   SearchBucketExternal,
				Complete: true,
				Hits:     hits,
			})
		}()
	}

	wg.Wait()
}

func (r *Resolver) streamResults(ctx context.Context, query string, resultCh chan<- SearchBatch) {
	var wg sync.WaitGroup

	if r.subsonic != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hits := []SearchHit{}
			externalHits, err := r.loadExternalHits(ctx, query)
			if err == nil {
				hits = trimSearchHits(externalHits, resultsTrackLimit)
			}
			emitSearchBatch(ctx, resultCh, SearchBatch{
				Intent:   SearchIntentResults,
				Bucket:   SearchBucketExternal,
				Complete: true,
				Hits:     hits,
			})
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		r.streamVideoResults(ctx, query, resultCh)
	}()

	wg.Wait()
}

func (r *Resolver) streamVideoResults(ctx context.Context, query string, resultCh chan<- SearchBatch) {
	if !shouldSearchRemote(query) {
		emitSearchBatch(ctx, resultCh, SearchBatch{
			Intent:   SearchIntentResults,
			Bucket:   SearchBucketYouTube,
			Complete: true,
			Hits:     []SearchHit{},
		})
		emitSearchBatch(ctx, resultCh, SearchBatch{
			Intent:   SearchIntentResults,
			Bucket:   SearchBucketYTMusic,
			Complete: true,
			Hits:     []SearchHit{},
		})
		return
	}

	type providerResult struct {
		tracks []Track
		hits   []SearchHit
	}

	youtubeCh := make(chan providerResult, 1)
	ytMusicCh := make(chan providerResult, 1)

	go func() {
		tracks, _ := r.loadYouTubeTracks(ctx, query)
		youtubeCh <- providerResult{tracks: tracks}
	}()

	go func() {
		hits, _ := r.loadYTMusicHits(ctx, query)
		ytMusicCh <- providerResult{hits: hits}
	}()

	var (
		youtubeTracks []Track
		ytMusicHits   []SearchHit
		youtubeReady  bool
		ytMusicReady  bool
	)

	for !youtubeReady || !ytMusicReady {
		select {
		case <-ctx.Done():
			return
		case result := <-youtubeCh:
			youtubeReady = true
			if len(result.tracks) > 0 {
				youtubeTracks = trimTracks(result.tracks, resultsTrackLimit)
			}
			if len(youtubeTracks) > 0 && !ytMusicReady {
				emitSearchBatch(ctx, resultCh, SearchBatch{
					Intent:   SearchIntentResults,
					Bucket:   SearchBucketYouTube,
					Complete: false,
					Hits:     searchHitsFromTracks(youtubeTracks),
				})
			}
		case result := <-ytMusicCh:
			ytMusicReady = true
			if len(result.hits) > 0 {
				ytMusicHits = trimSearchHits(result.hits, resultsTrackLimit)
			}
		}
	}

	ytMusicTracks := tracksFromSearchHits(ytMusicHits)
	mergedYouTube, uniqueYTMusic := mergeTrackSources(youtubeTracks, ytMusicTracks)
	ytMusicFinal := mergeYTMusicHitsWithTracks(ytMusicHits, uniqueYTMusic)
	emitSearchBatch(ctx, resultCh, SearchBatch{
		Intent:   SearchIntentResults,
		Bucket:   SearchBucketYouTube,
		Complete: true,
		Hits:     searchHitsFromTracks(trimTracks(mergedYouTube, resultsTrackLimit)),
	})
	emitSearchBatch(ctx, resultCh, SearchBatch{
		Intent:   SearchIntentResults,
		Bucket:   SearchBucketYTMusic,
		Complete: true,
		Hits:     trimSearchHits(ytMusicFinal, resultsTrackLimit),
	})
}

func (r *Resolver) loadSuggestions(ctx context.Context, query string) ([]string, error) {
	return r.suggestionCache.GetOrLoad(ctx, query, func(ctx context.Context) ([]string, error) {
		tCtx, cancel := context.WithTimeout(ctx, suggestionTimeout)
		defer cancel()
		items, err := r.fetchSuggestions(tCtx, query)
		if err != nil {
			return nil, err
		}
		return dedupeSuggestions(items), nil
	})
}

func (r *Resolver) fetchSuggestions(ctx context.Context, query string) ([]string, error) {
	suggestURL := "https://suggestqueries.google.com/complete/search?client=firefox&ds=yt&oe=utf8&q=" + url.QueryEscape(query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, suggestURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := r.suggestClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}

	var raw []json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	if len(raw) < 2 {
		return []string{}, nil
	}

	var suggestions []string
	if err := json.Unmarshal(raw[1], &suggestions); err != nil {
		return nil, err
	}

	return suggestions, nil
}

func (r *Resolver) loadExternalHits(ctx context.Context, query string) ([]SearchHit, error) {
	if r.subsonic == nil {
		return []SearchHit{}, nil
	}

	return r.externalCache.GetOrLoad(ctx, query, func(ctx context.Context) ([]SearchHit, error) {
		tCtx, cancel := context.WithTimeout(ctx, externalSearchTimeout)
		defer cancel()
		hits, err := r.subsonic.Search(tCtx, query, providerSearchLimit)
		if err != nil {
			return nil, err
		}
		return dedupeSearchHits(hits, providerSearchLimit), nil
	})
}

func (r *Resolver) loadYouTubeTracks(ctx context.Context, query string) ([]Track, error) {
	return r.youtubeCache.GetOrLoad(ctx, query, func(ctx context.Context) ([]Track, error) {
		tCtx, cancel := context.WithTimeout(ctx, videoSearchTimeout)
		defer cancel()
		tracks, err := r.searchYouTube(tCtx, query)
		if err != nil {
			return nil, err
		}
		return rankTracks(query, dedupeTracks(tracks, providerSearchLimit), providerSearchLimit), nil
	})
}

func (r *Resolver) loadYTMusicHits(ctx context.Context, query string) ([]SearchHit, error) {
	return r.ytMusicCache.GetOrLoad(ctx, query, func(ctx context.Context) ([]SearchHit, error) {
		tCtx, cancel := context.WithTimeout(ctx, videoSearchTimeout)
		defer cancel()

		albumCtx, cancelAlbums := context.WithCancel(tCtx)
		defer cancelAlbums()

		type albumResult struct {
			hits []SearchHit
		}
		albumCh := make(chan albumResult, 1)
		go func() {
			hits, err := r.searchMusicAlbumHits(albumCtx, query)
			if err != nil {
				albumCh <- albumResult{}
				return
			}
			albumCh <- albumResult{hits: hits}
		}()

		tracks, err := r.searchMusic(tCtx, query)
		if err != nil {
			return nil, err
		}
		tracks = rankTracks(query, dedupeTracks(tracks, providerSearchLimit), resultsTrackLimit)
		r.hydrateVideoTracks(tCtx, tracks)
		complete := completeVideoTracks(tracks)
		trackErr := error(nil)
		if len(complete) < len(tracks) {
			trackErr = fmt.Errorf("yt-dlp music metadata incomplete")
		}

		albums := []SearchHit{}
		select {
		case result := <-albumCh:
			albums = result.hits
		case <-time.After(ytMusicAlbumReadyGrace):
			cancelAlbums()
		case <-tCtx.Done():
			cancelAlbums()
		}

		hits := append([]SearchHit{}, albums...)
		hits = append(hits, searchHitsFromTracks(complete)...)
		hits = trimSearchHits(dedupeSearchHits(hits, providerSearchLimit), resultsTrackLimit)
		return hits, trackErr
	})
}

func (r *Resolver) searchYouTube(ctx context.Context, query string) ([]Track, error) {
	searchKey := fmt.Sprintf("ytsearch%d:%s", providerSearchLimit, query)
	args := []string{"--dump-json", "--flat-playlist", "--no-download", "--no-warnings", searchKey}

	cmd := exec.CommandContext(ctx, r.cfg.ShimPath(), args...)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("yt-dlp failed: %w", err)
	}

	tracks, err := parseLines(out)
	if err != nil {
		if isNoTracksError(err) {
			return []Track{}, nil
		}
		return nil, err
	}
	for i := range tracks {
		tracks[i].Source = SourceYouTube
	}
	return tracks, nil
}

func (r *Resolver) searchMusic(ctx context.Context, query string) ([]Track, error) {
	musicURL := "https://music.youtube.com/search?q=" + url.QueryEscape(query) + "#songs"
	args := []string{"--dump-json", "--flat-playlist", "--no-download", "--no-warnings"}
	args = append(args, "--playlist-end", fmt.Sprintf("%d", resultsTrackLimit))
	args = append(args, musicURL)

	cmd := exec.CommandContext(ctx, r.cfg.ShimPath(), args...)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("yt-dlp music search failed: %w", err)
	}

	tracks, err := parseLines(out)
	if err != nil {
		if isNoTracksError(err) {
			return []Track{}, nil
		}
		return nil, err
	}
	for i := range tracks {
		tracks[i].Source = SourceYTMusic
	}
	return tracks, nil
}

func (r *Resolver) searchMusicAlbumHits(ctx context.Context, query string) ([]SearchHit, error) {
	musicURL := "https://music.youtube.com/search?q=" + url.QueryEscape(query) + "#albums"
	args := []string{"--dump-json", "--flat-playlist", "--no-download", "--no-warnings"}
	args = append(args, "--playlist-end", fmt.Sprintf("%d", ytMusicAlbumSearchLimit))
	args = append(args, musicURL)

	cmd := exec.CommandContext(ctx, r.cfg.ShimPath(), args...)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("yt-dlp music album search failed: %w", err)
	}

	seeds, err := parseAlbumSearchLines(out)
	if err != nil {
		if isNoTracksError(err) {
			return []SearchHit{}, nil
		}
		return nil, err
	}

	hits := make([]SearchHit, 0, min(ytMusicAlbumEnrichmentLimit, len(seeds)))
	for _, seed := range seeds {
		if len(hits) >= ytMusicAlbumEnrichmentLimit {
			break
		}
		hit, err := r.fetchVideoAlbumSummary(ctx, seed.WebpageURL, seed)
		if err != nil {
			continue
		}
		if completeAlbumHit(hit) {
			hits = append(hits, hit)
		}
	}
	return hits, nil
}

func (r *Resolver) Resolve(ctx context.Context, rawURL string) ([]Track, error) {
	if subsonicRef, ok := ParseSubsonicURI(rawURL); ok {
		return r.resolveSubsonicTrack(ctx, subsonicRef)
	}

	args := []string{"--dump-json", "--flat-playlist", "--no-download", "--no-warnings", rawURL}
	cmd := exec.CommandContext(ctx, r.cfg.ShimPath(), args...)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("yt-dlp failed: %w", err)
	}
	return parseLines(out)
}

func (r *Resolver) Album(ctx context.Context, rawURL string) (Album, error) {
	if subsonicRef, ok := ParseSubsonicAlbumURI(rawURL); ok {
		return r.resolveSubsonicAlbum(ctx, subsonicRef)
	}
	if strings.HasPrefix(rawURL, SubsonicURIScheme+"://") || strings.HasPrefix(rawURL, SubsonicAlbumURIScheme+"://") {
		return Album{}, ErrInvalidAlbumRef
	}
	if !isYTMusicAlbumURL(rawURL) {
		return Album{}, ErrInvalidAlbumRef
	}
	return r.fetchVideoAlbum(ctx, rawURL, 0, SearchHit{Source: SourceYTMusic, WebpageURL: rawURL})
}

func (r *Resolver) resolveSubsonicTrack(ctx context.Context, ref SubsonicRef) ([]Track, error) {
	if r.subsonic == nil {
		return nil, fmt.Errorf("opensubsonic source is not configured")
	}
	if ref.LibraryID != r.subsonic.LibraryID() {
		return nil, fmt.Errorf("unknown opensubsonic library: %s", ref.LibraryID)
	}

	streamURL, err := r.subsonic.BuildStreamURL(ref.TrackID)
	if err != nil {
		return nil, err
	}

	track, err := r.subsonic.GetTrack(ctx, ref.TrackID)
	if err != nil {
		track = Track{
			ID:         ref.TrackID,
			Title:      ref.TrackID,
			Artist:     "OpenSubsonic",
			Uploader:   "OpenSubsonic",
			WebpageURL: BuildSubsonicURI(ref.LibraryID, ref.TrackID),
			Source:     SourceSubsonic,
		}
	}

	track.URL = streamURL
	track.WebpageURL = BuildSubsonicURI(ref.LibraryID, ref.TrackID)
	track.Source = SourceSubsonic

	return []Track{track}, nil
}

func (r *Resolver) resolveSubsonicAlbum(ctx context.Context, ref SubsonicAlbumRef) (Album, error) {
	if r.subsonic == nil {
		return Album{}, fmt.Errorf("opensubsonic source is not configured")
	}
	if ref.LibraryID != r.subsonic.LibraryID() {
		return Album{}, fmt.Errorf("unknown opensubsonic library: %s", ref.LibraryID)
	}

	return r.subsonic.GetAlbum(ctx, ref.AlbumID)
}

func (r *Resolver) fetchVideoAlbum(ctx context.Context, rawURL string, limit int, seed SearchHit) (Album, error) {
	meta, _ := r.fetchVideoAlbumMetadata(ctx, rawURL)
	tracks, err := r.fetchVideoAlbumTracks(ctx, rawURL, limit)
	if err != nil {
		return Album{}, err
	}
	r.hydrateVideoTracks(ctx, tracks)
	completeTracks := completeVideoTracks(tracks)
	if len(completeTracks) == 0 {
		return Album{}, fmt.Errorf("yt-dlp album tracks incomplete")
	}

	album := albumFromVideoMetadata(rawURL, seed, meta, completeTracks)
	if album.Title == "" || album.Artist == "" || album.Thumbnail == "" {
		return Album{}, fmt.Errorf("yt-dlp album metadata incomplete")
	}
	return album, nil
}

func (r *Resolver) fetchVideoAlbumSummary(ctx context.Context, rawURL string, seed SearchHit) (SearchHit, error) {
	meta, err := r.fetchVideoAlbumMetadata(ctx, rawURL)
	if err != nil {
		return SearchHit{}, err
	}
	tracks, err := r.fetchVideoAlbumTracks(ctx, rawURL, ytMusicAlbumPreviewTrackLimit)
	if err != nil {
		return SearchHit{}, err
	}
	if len(tracks) == 0 {
		return SearchHit{}, fmt.Errorf("yt-dlp album has no tracks")
	}

	return albumHitFromAlbum(albumFromVideoMetadata(rawURL, seed, meta, tracks)), nil
}

func (r *Resolver) fetchVideoAlbumMetadata(ctx context.Context, rawURL string) (ytDlpResponse, error) {
	args := []string{"--dump-json", "--playlist-end", "1", "--no-download", "--no-warnings", rawURL}
	cmd := exec.CommandContext(ctx, r.cfg.ShimPath(), args...)
	out, err := cmd.Output()
	if err != nil {
		return ytDlpResponse{}, err
	}

	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		var resp ytDlpResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err == nil {
			return resp, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return ytDlpResponse{}, err
	}
	return ytDlpResponse{}, fmt.Errorf("no album metadata found")
}

func (r *Resolver) fetchVideoAlbumTracks(ctx context.Context, rawURL string, limit int) ([]Track, error) {
	args := []string{"--dump-json", "--flat-playlist", "--no-download", "--no-warnings"}
	if limit > 0 {
		args = append(args, "--playlist-end", fmt.Sprintf("%d", limit))
	}
	args = append(args, rawURL)

	cmd := exec.CommandContext(ctx, r.cfg.ShimPath(), args...)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("yt-dlp album tracks failed: %w", err)
	}

	tracks, err := parseLines(out)
	if err != nil {
		if isNoTracksError(err) {
			return []Track{}, nil
		}
		return nil, err
	}
	for i := range tracks {
		tracks[i].Source = SourceYTMusic
	}
	return tracks, nil
}

func (r *Resolver) hydrateVideoTracks(ctx context.Context, tracks []Track) {
	var wg sync.WaitGroup
	for index := range tracks {
		if hasVideoDisplayMetadata(tracks[index]) {
			continue
		}
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			meta, err := r.fetchVideoMetadata(ctx, tracks[index].WebpageURL)
			if err != nil {
				return
			}
			meta.Source = tracks[index].Source
			tracks[index] = meta
		}(index)
	}
	wg.Wait()
}

func (r *Resolver) fetchVideoMetadata(ctx context.Context, rawURL string) (Track, error) {
	args := []string{"--dump-json", "--no-playlist", "--no-download", "--no-warnings", rawURL}
	cmd := exec.CommandContext(ctx, r.cfg.ShimPath(), args...)
	out, err := cmd.Output()
	if err != nil {
		return Track{}, err
	}

	var resp ytDlpResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return Track{}, err
	}
	return trackFromResponse(resp), nil
}

func emitSearchBatch(ctx context.Context, resultCh chan<- SearchBatch, batch SearchBatch) {
	select {
	case <-ctx.Done():
	case resultCh <- batch:
	}
}

func searchHitsFromTracks(tracks []Track) []SearchHit {
	hits := make([]SearchHit, 0, len(tracks))
	for _, track := range tracks {
		hit := searchHitFromTrack(track)
		if hit.Source == "" {
			continue
		}
		hits = append(hits, hit)
	}
	return hits
}

func searchHitFromTrack(track Track) SearchHit {
	if (track.Source == SourceYouTube || track.Source == SourceYTMusic) && !hasVideoDisplayMetadata(track) {
		return SearchHit{}
	}
	queueURL := track.PlayableURL()
	if queueURL == "" || track.WebpageURL == "" || track.Source == "" {
		return SearchHit{}
	}
	return SearchHit{
		Kind:       SearchHitKindTrack,
		ID:         track.ID,
		Source:     track.Source,
		Title:      track.Title,
		Artist:     track.Artist,
		Duration:   track.Duration,
		Thumbnail:  track.Thumbnail,
		WebpageURL: track.WebpageURL,
		QueueURL:   queueURL,
	}
}

func albumHitFromAlbum(album Album) SearchHit {
	return SearchHit{
		Kind:       SearchHitKindAlbum,
		Source:     album.Source,
		Title:      album.Title,
		Artist:     album.Artist,
		Duration:   album.Duration,
		Thumbnail:  album.Thumbnail,
		WebpageURL: album.WebpageURL,
		TrackCount: album.TrackCount,
	}
}

func completeAlbumHit(hit SearchHit) bool {
	return hit.Kind == SearchHitKindAlbum && hit.Source != "" && hit.Title != "" && hit.Artist != "" && hit.Thumbnail != "" && hit.WebpageURL != ""
}

func trackHitsOnly(hits []SearchHit) []SearchHit {
	out := make([]SearchHit, 0, len(hits))
	for _, hit := range hits {
		if hit.Kind == "" || hit.Kind == SearchHitKindTrack {
			out = append(out, hit)
		}
	}
	return out
}

func tracksFromSearchHits(hits []SearchHit) []Track {
	tracks := make([]Track, 0, len(hits))
	for _, hit := range hits {
		if hit.Kind != SearchHitKindTrack {
			continue
		}
		tracks = append(tracks, Track{
			ID:         hit.ID,
			Title:      hit.Title,
			Artist:     hit.Artist,
			Duration:   hit.Duration,
			Uploader:   hit.Artist,
			Thumbnail:  hit.Thumbnail,
			WebpageURL: hit.WebpageURL,
			Source:     hit.Source,
		})
	}
	return tracks
}

func mergeYTMusicHitsWithTracks(original []SearchHit, tracks []Track) []SearchHit {
	hits := make([]SearchHit, 0, len(original)+len(tracks))
	for _, hit := range original {
		if hit.Kind == SearchHitKindAlbum {
			hits = append(hits, hit)
		}
	}
	hits = append(hits, searchHitsFromTracks(tracks)...)
	return hits
}

func hasVideoDisplayMetadata(track Track) bool {
	return track.Title != "" && track.Artist != "" && track.Duration > 0 && track.Thumbnail != ""
}

func completeVideoTracks(tracks []Track) []Track {
	complete := make([]Track, 0, len(tracks))
	for _, track := range tracks {
		if hasVideoDisplayMetadata(track) {
			complete = append(complete, track)
		}
	}
	return complete
}

func (t Track) PlayableURL() string {
	switch t.Source {
	case SourceSubsonic:
		return t.URL
	case SourceYouTube, SourceYTMusic:
		return t.WebpageURL
	default:
		return ""
	}
}

func dedupeSuggestions(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, min(maxSuggestionCount, len(items)))
	for _, item := range items {
		normalized := normalizeSearchQuery(item)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, item)
		if len(out) >= maxSuggestionCount {
			break
		}
	}
	return out
}

func dedupeTracks(tracks []Track, limit int) []Track {
	seen := make(map[string]struct{}, len(tracks))
	out := make([]Track, 0, min(limit, len(tracks)))
	for _, track := range tracks {
		key := trackDedupKey(track)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, track)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func dedupeSearchHits(hits []SearchHit, limit int) []SearchHit {
	seen := make(map[string]struct{}, len(hits))
	out := make([]SearchHit, 0, min(limit, len(hits)))
	for _, hit := range hits {
		key := searchHitDedupKey(hit)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, hit)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func searchHitDedupKey(hit SearchHit) string {
	if hit.Kind == SearchHitKindAlbum {
		if hit.WebpageURL != "" {
			return string(hit.Kind) + "|" + hit.Source + "|url|" + hit.WebpageURL
		}
		if hit.ID != "" {
			return string(hit.Kind) + "|" + hit.Source + "|id|" + hit.ID
		}
		return ""
	}
	if hit.ID != "" {
		return string(SearchHitKindTrack) + "|" + hit.Source + "|id|" + hit.ID
	}
	if hit.WebpageURL != "" {
		return string(SearchHitKindTrack) + "|" + hit.Source + "|url|" + hit.WebpageURL
	}
	return ""
}

func trackDedupKey(track Track) string {
	if track.ID != "" {
		return track.Source + "|id|" + track.ID
	}
	if track.WebpageURL != "" {
		return track.Source + "|url|" + track.WebpageURL
	}
	return ""
}

func trimSearchHits(hits []SearchHit, limit int) []SearchHit {
	if len(hits) <= limit {
		return append([]SearchHit(nil), hits...)
	}
	return append([]SearchHit(nil), hits[:limit]...)
}

func rankTracks(query string, tracks []Track, limit int) []Track {
	type rankedTrack struct {
		track Track
		score int
		index int
	}

	tokens := strings.Fields(query)
	ranked := make([]rankedTrack, 0, len(tracks))
	for idx, track := range tracks {
		ranked = append(ranked, rankedTrack{
			track: track,
			score: trackRelevanceScore(query, tokens, track),
			index: idx,
		})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].index < ranked[j].index
		}
		return ranked[i].score > ranked[j].score
	})

	out := make([]Track, 0, min(limit, len(ranked)))
	for _, item := range ranked {
		out = append(out, item.track)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func trackRelevanceScore(query string, tokens []string, track Track) int {
	title := normalizeSearchQuery(track.Title)
	artist := normalizeSearchQuery(track.Artist)
	uploader := normalizeSearchQuery(track.Uploader)
	combined := strings.TrimSpace(title + " " + artist)

	score := 0
	switch {
	case title == query:
		score += 1200
	case combined == query:
		score += 1100
	}
	if strings.HasPrefix(title, query) {
		score += 500
	}
	if strings.HasPrefix(combined, query) {
		score += 350
	}
	if strings.Contains(title, query) {
		score += 220
	}
	if strings.Contains(artist, query) {
		score += 110
	}
	if strings.Contains(uploader, query) {
		score += 40
	}
	for _, token := range tokens {
		if strings.Contains(title, token) {
			score += 120
		}
		if strings.Contains(artist, token) {
			score += 60
		}
	}
	if track.Duration > 0 {
		score += 5
	}
	return score
}

func mergeTrackSources(youtubeTracks []Track, ytMusicTracks []Track) ([]Track, []Track) {
	if len(youtubeTracks) == 0 {
		return []Track{}, trimTracks(ytMusicTracks, resultsTrackLimit)
	}

	mergedYouTube := append([]Track(nil), youtubeTracks...)
	youtubeIndex := make(map[string]int, len(mergedYouTube))
	for idx, track := range mergedYouTube {
		if track.ID != "" {
			youtubeIndex[track.ID] = idx
		}
	}

	uniqueYTMusic := make([]Track, 0, len(ytMusicTracks))
	for _, track := range ytMusicTracks {
		if idx, ok := youtubeIndex[track.ID]; ok && track.ID != "" {
			mergedYouTube[idx] = mergeTrackMetadata(mergedYouTube[idx], track)
			continue
		}
		uniqueYTMusic = append(uniqueYTMusic, track)
	}

	return mergedYouTube, uniqueYTMusic
}

func mergeTrackMetadata(primary Track, secondary Track) Track {
	if primary.Title == "" && secondary.Title != "" {
		primary.Title = secondary.Title
	}
	if secondary.Artist != "" && (primary.Artist == "" || primary.Artist == primary.Uploader) {
		primary.Artist = secondary.Artist
	}
	if primary.Duration == 0 && secondary.Duration > 0 {
		primary.Duration = secondary.Duration
	}
	if secondary.Thumbnail != "" {
		primary.Thumbnail = secondary.Thumbnail
	}
	if primary.Uploader == "" && secondary.Uploader != "" {
		primary.Uploader = secondary.Uploader
	}
	return primary
}

func trimTracks(tracks []Track, limit int) []Track {
	if len(tracks) <= limit {
		return append([]Track(nil), tracks...)
	}
	return append([]Track(nil), tracks[:limit]...)
}

func normalizeSearchQuery(raw string) string {
	return strings.ToLower(strings.Join(strings.Fields(raw), " "))
}

func shouldSearchRemote(query string) bool {
	return utf8.RuneCountInString(query) >= minRemoteQueryRunes
}

func parseLines(data []byte) ([]Track, error) {
	var tracks []Track
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		var resp ytDlpResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			continue
		}

		if resp.IEKey == "YoutubeTab" || resp.LiveStatus == "is_live" || resp.LiveStatus == "was_live" {
			continue
		}

		if track := trackFromResponse(resp); track.WebpageURL != "" {
			tracks = append(tracks, track)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan output: %w", err)
	}
	if len(tracks) == 0 {
		return nil, fmt.Errorf("no tracks found")
	}
	return tracks, nil
}

func parseAlbumSearchLines(data []byte) ([]SearchHit, error) {
	var hits []SearchHit
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		var resp ytDlpResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			continue
		}

		rawURL := albumURLFromResponse(resp)
		if rawURL == "" {
			continue
		}

		artist := firstArtist(coalesce(resp.Artist, resp.Channel, resp.Uploader))
		hits = append(hits, SearchHit{
			Kind:       SearchHitKindAlbum,
			ID:         resp.ID,
			Source:     SourceYTMusic,
			Title:      resp.Title,
			Artist:     artist,
			Thumbnail:  thumbnailFromResponse(resp),
			WebpageURL: rawURL,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan album output: %w", err)
	}
	if len(hits) == 0 {
		return nil, fmt.Errorf("no tracks found")
	}
	return hits, nil
}

func isNoTracksError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no tracks found") || errors.Is(err, context.DeadlineExceeded)
}

func trackFromResponse(resp ytDlpResponse) Track {
	artist := firstArtist(coalesce(resp.Artist, resp.Channel, resp.Uploader))

	webpageURL := resp.WebpageURL
	if webpageURL == "" && resp.ID != "" && resp.IEKey == "Youtube" {
		webpageURL = "https://www.youtube.com/watch?v=" + resp.ID
	}

	thumbnail := thumbnailFromResponse(resp)

	return Track{
		ID:         resp.ID,
		Title:      resp.Title,
		Artist:     artist,
		Duration:   durationFromResponse(resp),
		Uploader:   resp.Uploader,
		Thumbnail:  thumbnail,
		WebpageURL: webpageURL,
		Source:     SourceYouTube,
	}
}

func thumbnailFromResponse(resp ytDlpResponse) string {
	thumbnail := resp.Thumbnail
	if len(resp.Thumbnails) > 0 {
		last := resp.Thumbnails[len(resp.Thumbnails)-1]
		if last.URL != "" {
			thumbnail = last.URL
		}
	}
	if thumbnail == "" && resp.ID != "" {
		thumbnail = fmt.Sprintf("https://i.ytimg.com/vi/%s/hqdefault.jpg", resp.ID)
	}
	return thumbnail
}

func albumURLFromResponse(resp ytDlpResponse) string {
	for _, rawURL := range []string{resp.WebpageURL, resp.URL} {
		if isYTMusicAlbumURL(rawURL) {
			return rawURL
		}
	}
	return ""
}

func isYTMusicAlbumURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	if host != "music.youtube.com" {
		return false
	}
	return strings.HasPrefix(u.Path, "/browse/") || strings.HasPrefix(u.Path, "/playlist")
}

func albumFromVideoMetadata(rawURL string, seed SearchHit, meta ytDlpResponse, tracks []Track) Album {
	firstTrack := tracks[0]
	title := coalesce(meta.Album, meta.PlaylistTitle, meta.Playlist, seed.Title)
	artist := firstArtist(coalesce(meta.AlbumArtist, seed.Artist, meta.Artist, meta.Channel, meta.PlaylistUploader, meta.Uploader, firstTrack.Artist))
	thumbnail := coalesce(seed.Thumbnail, thumbnailFromResponse(meta), firstTrack.Thumbnail)
	trackCount := meta.PlaylistCount
	if trackCount == 0 {
		trackCount = seed.TrackCount
	}
	if trackCount == 0 {
		trackCount = len(tracks)
	}

	return Album{
		Source:     SourceYTMusic,
		Title:      title,
		Artist:     artist,
		Thumbnail:  thumbnail,
		WebpageURL: rawURL,
		TrackCount: trackCount,
		Tracks:     searchHitsFromTracks(tracks),
	}
}

func durationFromResponse(resp ytDlpResponse) float64 {
	if resp.Duration > 0 {
		return resp.Duration
	}
	if parsed, ok := parseDurationString(resp.DurationString); ok {
		return parsed
	}
	return 0
}

func coalesce(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstArtist(value string) string {
	first, _, _ := strings.Cut(value, ",")
	return strings.TrimSpace(first)
}

func parseDurationString(raw string) (float64, bool) {
	if raw == "" {
		return 0, false
	}

	parts := strings.Split(raw, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}

	total := 0
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			return 0, false
		}
		total = total*60 + value
	}

	return float64(total), true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
