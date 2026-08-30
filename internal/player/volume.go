// SPDX-License-Identifier: AGPL-3.0-or-later

package player

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

const defaultStartupVolume = 35

type playbackState struct {
	Volume float64 `json:"volume"`
}

func loadStartupVolume(path string, logger *slog.Logger) float64 {
	if path == "" {
		return defaultStartupVolume
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warn("Failed to read playback state", "error", err)
		}
		return defaultStartupVolume
	}

	var state playbackState
	if err := json.Unmarshal(data, &state); err != nil {
		logger.Warn("Failed to parse playback state", "error", err)
		return defaultStartupVolume
	}

	return clampVolume(state.Volume)
}

func saveStartupVolume(path string, volume float64, logger *slog.Logger) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logger.Warn("Failed to create playback state directory", "error", err)
		return
	}

	data, err := json.MarshalIndent(playbackState{Volume: clampVolume(volume)}, "", "  ")
	if err != nil {
		logger.Warn("Failed to encode playback state", "error", err)
		return
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		logger.Warn("Failed to save playback state", "error", err)
	}
}

func clampVolume(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func volumeArg(v float64) string {
	return fmt.Sprintf("--volume=%.0f", clampVolume(v))
}
