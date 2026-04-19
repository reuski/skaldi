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

	typeaheadTrackLimit   = 4
	resultsTrackLimit     = 8
	providerSearchLimit   = 12
	maxSuggestionCount    = 8
	minRemoteQueryRunes   = 2
	searchCacheLimit      = 64
	suggestionCacheTTL    = 5 * time.Minute
	externalCacheTTL      = 60 * time.Second
	youtubeCacheTTL       = 2 * time.Minute
	ytMusicCacheTTL       = 2 * time.Minute
	suggestionTimeout     = 2 * time.Second
	externalSearchTimeout = 2500 * time.Millisecond
	youtubeSearchTimeout  = 5 * time.Second
	ytMusicSearchTimeout  = 10 * time.Second
)

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
	ID         string  `json:"id"`
	Source     string  `json:"source"`
	Title      string  `json:"title"`
	Artist     string  `json:"artist"`
	Duration   float64 `json:"duration"`
	Thumbnail  string  `json:"thumbnail"`
	WebpageURL string  `json:"webpage_url"`
	QueueURL   string  `json:"queue_url"`
}

type SearchBatch struct {
	Intent      SearchIntent `json:"intent"`
	Bucket      SearchBucket `json:"bucket"`
	Complete    bool         `json:"complete"`
	Suggestions []string     `json:"suggestions,omitempty"`
	Hits        []SearchHit  `json:"hits,omitempty"`
}

type ytDlpResponse struct {
	ID             string  `json:"id"`
	Title          string  `json:"title"`
	Artist         string  `json:"artist"`
	Channel        string  `json:"channel"`
	Duration       float64 `json:"duration"`
	DurationString string  `json:"duration_string"`
	Uploader       string  `json:"uploader"`
	Thumbnail      string  `json:"thumbnail"`
	Thumbnails     []struct {
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
	externalCache   *searchCache[[]Track]
	youtubeCache    *searchCache[[]Track]
	ytMusicCache    *searchCache[[]Track]
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
		externalCache:   newSearchCache[[]Track](externalCacheTTL, searchCacheLimit),
		youtubeCache:    newSearchCache[[]Track](youtubeCacheTTL, searchCacheLimit),
		ytMusicCache:    newSearchCache[[]Track](ytMusicCacheTTL, searchCacheLimit),
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
			tracks, err := r.loadExternalTracks(ctx, query)
			if err == nil {
				hits = searchHitsFromTracks(trimTracks(tracks, typeaheadTrackLimit))
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
			tracks, err := r.loadExternalTracks(ctx, query)
			if err == nil {
				hits = searchHitsFromTracks(trimTracks(tracks, resultsTrackLimit))
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
		err    error
	}

	youtubeCh := make(chan providerResult, 1)
	ytMusicCh := make(chan providerResult, 1)

	go func() {
		tracks, err := r.loadYouTubeTracks(ctx, query)
		youtubeCh <- providerResult{tracks: tracks, err: err}
	}()

	go func() {
		tracks, err := r.loadYTMusicTracks(ctx, query)
		ytMusicCh <- providerResult{tracks: tracks, err: err}
	}()

	var (
		youtubeTracks []Track
		ytMusicTracks []Track
		youtubeReady  bool
		ytMusicReady  bool
	)

	for !youtubeReady || !ytMusicReady {
		select {
		case <-ctx.Done():
			return
		case result := <-youtubeCh:
			youtubeReady = true
			if result.err == nil {
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
			if result.err == nil {
				ytMusicTracks = trimTracks(result.tracks, resultsTrackLimit)
			}
		}
	}

	mergedYouTube, uniqueYTMusic := mergeTrackSources(youtubeTracks, ytMusicTracks)
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
		Hits:     searchHitsFromTracks(trimTracks(uniqueYTMusic, resultsTrackLimit)),
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

func (r *Resolver) loadExternalTracks(ctx context.Context, query string) ([]Track, error) {
	if r.subsonic == nil {
		return []Track{}, nil
	}

	return r.externalCache.GetOrLoad(ctx, query, func(ctx context.Context) ([]Track, error) {
		tCtx, cancel := context.WithTimeout(ctx, externalSearchTimeout)
		defer cancel()
		tracks, err := r.subsonic.Search(tCtx, query, providerSearchLimit)
		if err != nil {
			return nil, err
		}
		return dedupeTracks(tracks, providerSearchLimit), nil
	})
}

func (r *Resolver) loadYouTubeTracks(ctx context.Context, query string) ([]Track, error) {
	return r.youtubeCache.GetOrLoad(ctx, query, func(ctx context.Context) ([]Track, error) {
		tCtx, cancel := context.WithTimeout(ctx, youtubeSearchTimeout)
		defer cancel()
		tracks, err := r.searchYouTube(tCtx, query)
		if err != nil {
			return nil, err
		}
		return rankTracks(query, dedupeTracks(tracks, providerSearchLimit), providerSearchLimit), nil
	})
}

func (r *Resolver) loadYTMusicTracks(ctx context.Context, query string) ([]Track, error) {
	return r.ytMusicCache.GetOrLoad(ctx, query, func(ctx context.Context) ([]Track, error) {
		tCtx, cancel := context.WithTimeout(ctx, ytMusicSearchTimeout)
		defer cancel()
		tracks, err := r.searchMusic(tCtx, query)
		if err != nil {
			return nil, err
		}
		return rankTracks(query, dedupeTracks(tracks, providerSearchLimit), providerSearchLimit), nil
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
	args := []string{"--dump-json", "--no-download", "--no-warnings"}
	args = append(args, "--playlist-end", fmt.Sprintf("%d", providerSearchLimit*2))
	args = append(args, musicURL)

	cmdCtx, cmdCancel := context.WithCancel(ctx)
	defer cmdCancel()

	cmd := exec.CommandContext(cmdCtx, r.cfg.ShimPath(), args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var tracks []Track
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		var resp ytDlpResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			continue
		}
		if resp.IEKey == "YoutubeTab" || resp.LiveStatus == "is_live" || resp.LiveStatus == "was_live" {
			continue
		}
		track := trackFromResponse(resp)
		if track.WebpageURL == "" {
			continue
		}
		track.Source = SourceYTMusic
		tracks = append(tracks, track)
		if len(tracks) >= providerSearchLimit {
			break
		}
	}

	cmdCancel()
	_ = cmd.Wait()

	if len(tracks) == 0 {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("no tracks found")
	}
	return tracks, nil
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

func emitSearchBatch(ctx context.Context, resultCh chan<- SearchBatch, batch SearchBatch) {
	select {
	case <-ctx.Done():
	case resultCh <- batch:
	}
}

func searchHitsFromTracks(tracks []Track) []SearchHit {
	hits := make([]SearchHit, 0, len(tracks))
	for _, track := range tracks {
		queueURL := track.PlayableURL()
		if queueURL == "" || track.WebpageURL == "" || track.Source == "" {
			continue
		}
		hits = append(hits, SearchHit{
			ID:         track.ID,
			Source:     track.Source,
			Title:      track.Title,
			Artist:     track.Artist,
			Duration:   track.Duration,
			Thumbnail:  track.Thumbnail,
			WebpageURL: track.WebpageURL,
			QueueURL:   queueURL,
		})
	}
	return hits
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

func trackDedupKey(track Track) string {
	if track.ID != "" {
		return track.Source + "|id|" + track.ID
	}
	if track.WebpageURL != "" {
		return track.Source + "|url|" + track.WebpageURL
	}
	return ""
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

func isNoTracksError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no tracks found") || errors.Is(err, context.DeadlineExceeded)
}

func trackFromResponse(resp ytDlpResponse) Track {
	artist := coalesce(resp.Artist, resp.Channel, resp.Uploader)

	webpageURL := resp.WebpageURL
	if webpageURL == "" && resp.ID != "" && resp.IEKey == "Youtube" {
		webpageURL = "https://www.youtube.com/watch?v=" + resp.ID
	}

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
