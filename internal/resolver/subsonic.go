// SPDX-License-Identifier: AGPL-3.0-or-later

package resolver

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type SubsonicClient struct {
	cfg        openSubsonicConfig
	httpClient *http.Client
	timeout    time.Duration
}

func NewSubsonicClient(cfg openSubsonicConfig) *SubsonicClient {
	return &SubsonicClient{
		cfg:        cfg,
		httpClient: &http.Client{},
		timeout:    time.Duration(cfg.TimeoutMS) * time.Millisecond,
	}
}

func (c *SubsonicClient) LibraryID() string {
	return c.cfg.LibraryID
}

type subsonicSearchResponse struct {
	SubsonicResponse struct {
		Status string `json:"status"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
		SearchResult3 struct {
			Song []subsonicSong `json:"song"`
		} `json:"searchResult3"`
	} `json:"subsonic-response"`
}

type subsonicGetSongResponse struct {
	SubsonicResponse struct {
		Status string        `json:"status"`
		Error  *subsonicErr  `json:"error,omitempty"`
		Song   *subsonicSong `json:"song,omitempty"`
	} `json:"subsonic-response"`
}

type subsonicErr struct {
	Message string `json:"message"`
}

type subsonicSong struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Duration int    `json:"duration"`
}

func (c *SubsonicClient) Search(ctx context.Context, query string, limit int) ([]Track, error) {
	if limit <= 0 {
		limit = 5
	}

	params, err := c.authParams()
	if err != nil {
		return nil, err
	}
	params.Set("query", query)
	params.Set("artistCount", "0")
	params.Set("albumCount", "0")
	params.Set("songCount", strconv.Itoa(limit))
	params.Set("songOffset", "0")

	var resp subsonicSearchResponse
	if err := c.getJSON(ctx, "search3.view", params, &resp); err != nil {
		return nil, err
	}
	if resp.SubsonicResponse.Status != "ok" {
		msg := "search failed"
		if resp.SubsonicResponse.Error != nil && resp.SubsonicResponse.Error.Message != "" {
			msg = resp.SubsonicResponse.Error.Message
		}
		return nil, fmt.Errorf("opensubsonic: %s", msg)
	}

	tracks := make([]Track, 0, len(resp.SubsonicResponse.SearchResult3.Song))
	for _, song := range resp.SubsonicResponse.SearchResult3.Song {
		tracks = append(tracks, c.songToTrack(song))
	}
	return tracks, nil
}

func (c *SubsonicClient) GetTrack(ctx context.Context, trackID string) (Track, error) {
	params, err := c.authParams()
	if err != nil {
		return Track{}, err
	}
	params.Set("id", trackID)

	var resp subsonicGetSongResponse
	if err := c.getJSON(ctx, "getSong.view", params, &resp); err != nil {
		return Track{}, err
	}
	if resp.SubsonicResponse.Status != "ok" {
		msg := "getSong failed"
		if resp.SubsonicResponse.Error != nil && resp.SubsonicResponse.Error.Message != "" {
			msg = resp.SubsonicResponse.Error.Message
		}
		return Track{}, fmt.Errorf("opensubsonic: %s", msg)
	}
	if resp.SubsonicResponse.Song == nil || resp.SubsonicResponse.Song.ID == "" {
		return Track{}, fmt.Errorf("opensubsonic: track not found")
	}

	return c.songToTrack(*resp.SubsonicResponse.Song), nil
}

func (c *SubsonicClient) BuildStreamURL(trackID string) (string, error) {
	params, err := c.authParams()
	if err != nil {
		return "", err
	}
	params.Del("f")
	params.Set("id", trackID)
	return c.endpointURL("stream.view", params), nil
}

func (c *SubsonicClient) songToTrack(song subsonicSong) Track {
	artist := song.Artist
	if artist == "" {
		artist = "OpenSubsonic"
	}
	opaque := BuildSubsonicURI(c.cfg.LibraryID, song.ID)
	return Track{
		ID:         song.ID,
		Title:      song.Title,
		Artist:     artist,
		Duration:   float64(song.Duration),
		Uploader:   artist,
		URL:        opaque,
		WebpageURL: opaque,
		Source:     SourceSubsonic,
	}
}

func (c *SubsonicClient) getJSON(ctx context.Context, endpoint string, params url.Values, out any) error {
	tCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(tCtx, http.MethodGet, c.endpointURL(endpoint, params), nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		return fmt.Errorf("opensubsonic: %s: %s", resp.Status, string(body))
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(out); err != nil {
		return fmt.Errorf("opensubsonic decode failed: %w", err)
	}
	return nil
}

func (c *SubsonicClient) endpointURL(endpoint string, params url.Values) string {
	u := c.cfg.BaseURL + "/rest/" + endpoint
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	return u
}

func (c *SubsonicClient) authParams() (url.Values, error) {
	salt, err := randomSalt(8)
	if err != nil {
		return nil, err
	}

	hash := md5.Sum([]byte(c.cfg.Token + salt))
	tokenHash := hex.EncodeToString(hash[:])

	params := url.Values{}
	params.Set("u", c.cfg.Username)
	params.Set("t", tokenHash)
	params.Set("s", salt)
	params.Set("v", "1.16.1")
	params.Set("c", "skaldi")
	params.Set("f", "json")
	return params, nil
}

func randomSalt(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
