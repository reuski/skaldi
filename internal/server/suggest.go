// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"
)

var suggestClient = &http.Client{Timeout: 2 * time.Second}

func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Query is required", http.StatusBadRequest)
		return
	}

	suggestions, err := fetchSuggestions(r.Context(), query)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		s.logger.Error("Failed to fetch suggestions", "query", query, "error", err)
		http.Error(w, "Suggestion fetch failed", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(suggestions)
}

func fetchSuggestions(ctx context.Context, query string) ([]string, error) {
	u := "https://suggestqueries.google.com/complete/search?client=firefox&ds=yt&oe=utf8&q=" + url.QueryEscape(query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := suggestClient.Do(req)
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
