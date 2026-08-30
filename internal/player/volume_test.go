// SPDX-License-Identifier: AGPL-3.0-or-later

package player

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadStartupVolumeDefault(t *testing.T) {
	got := loadStartupVolume(filepath.Join(t.TempDir(), "missing.json"), slog.Default())
	if got != defaultStartupVolume {
		t.Fatalf("volume = %v, want %v", got, defaultStartupVolume)
	}
}

func TestSaveAndLoadStartupVolume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "playback.json")
	saveStartupVolume(path, 42, slog.Default())

	got := loadStartupVolume(path, slog.Default())
	if got != 42 {
		t.Fatalf("volume = %v, want 42", got)
	}
}

func TestLoadStartupVolumeClamps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "playback.json")
	if err := os.WriteFile(path, []byte(`{"volume":140}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got := loadStartupVolume(path, slog.Default())
	if got != 100 {
		t.Fatalf("volume = %v, want 100", got)
	}
}
