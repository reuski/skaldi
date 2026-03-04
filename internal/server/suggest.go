// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"net/http"
)

func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Query is required", http.StatusBadRequest)
		return
	}

	suggestions, err := s.resolver.Suggestions(r.Context(), query)
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
