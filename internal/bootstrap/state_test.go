package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadState_NotExists(t *testing.T) {
	tmpDir := t.TempDir()

	state, err := LoadState(tmpDir)
	if err != nil {
		t.Fatalf("LoadState on non-existent file should return empty state: %v", err)
	}

	if state.Uv != "" || state.Bun != "" || state.YtDlp != "" {
		t.Error("Expected empty state for non-existent file")
	}
}

func TestLoadState_ValidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "versions.json")

	jsonData := `{
  "uv": "0.5.0",
  "bun": "1.1.0",
  "yt-dlp": "2024.01.01"
}`
	if err := os.WriteFile(statePath, []byte(jsonData), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	state, err := LoadState(tmpDir)
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}

	if state.Uv != "0.5.0" {
		t.Errorf("Uv = %q, want %q", state.Uv, "0.5.0")
	}

	if state.Bun != "1.1.0" {
		t.Errorf("Bun = %q, want %q", state.Bun, "1.1.0")
	}

	if state.YtDlp != "2024.01.01" {
		t.Errorf("YtDlp = %q, want %q", state.YtDlp, "2024.01.01")
	}
}

func TestLoadState_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "versions.json")

	if err := os.WriteFile(statePath, []byte("not valid json"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	_, err := LoadState(tmpDir)
	if err == nil {
		t.Error("LoadState should fail with invalid JSON")
	}
}

func TestSaveState(t *testing.T) {
	tmpDir := t.TempDir()

	state := &State{
		Uv:    "0.5.5",
		Bun:   "1.1.10",
		YtDlp: "2024.02.15",
	}

	if err := SaveState(tmpDir, state); err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}

	statePath := filepath.Join(tmpDir, "versions.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("Failed to read saved state: %v", err)
	}

	if len(data) == 0 {
		t.Error("Saved state file is empty")
	}

	loaded, err := LoadState(tmpDir)
	if err != nil {
		t.Fatalf("LoadState after save failed: %v", err)
	}

	if loaded.Uv != state.Uv {
		t.Errorf("Loaded Uv = %q, want %q", loaded.Uv, state.Uv)
	}

	if loaded.Bun != state.Bun {
		t.Errorf("Loaded Bun = %q, want %q", loaded.Bun, state.Bun)
	}

	if loaded.YtDlp != state.YtDlp {
		t.Errorf("Loaded YtDlp = %q, want %q", loaded.YtDlp, state.YtDlp)
	}
}

func TestSaveState_EmptyState(t *testing.T) {
	tmpDir := t.TempDir()

	state := &State{}

	if err := SaveState(tmpDir, state); err != nil {
		t.Fatalf("SaveState with empty state failed: %v", err)
	}

	loaded, err := LoadState(tmpDir)
	if err != nil {
		t.Fatalf("LoadState after save failed: %v", err)
	}

	if loaded.Uv != "" || loaded.Bun != "" || loaded.YtDlp != "" {
		t.Error("Empty state should load as empty values")
	}
}

func TestState_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()

	testCases := []struct {
		name  string
		state State
	}{
		{
			name: "full",
			state: State{
				Uv:    "1.0.0",
				Bun:   "1.0.0",
				YtDlp: "2024.01.01",
			},
		},
		{
			name: "partial",
			state: State{
				Uv: "0.5.0",
			},
		},
		{
			name:  "empty",
			state: State{},
		},
		{
			name: "with_special_chars",
			state: State{
				YtDlp: "2024.12.25-nightly",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := SaveState(tmpDir, &tc.state); err != nil {
				t.Fatalf("SaveState failed: %v", err)
			}

			loaded, err := LoadState(tmpDir)
			if err != nil {
				t.Fatalf("LoadState failed: %v", err)
			}

			if loaded.Uv != tc.state.Uv {
				t.Errorf("Uv mismatch: got %q, want %q", loaded.Uv, tc.state.Uv)
			}

			if loaded.Bun != tc.state.Bun {
				t.Errorf("Bun mismatch: got %q, want %q", loaded.Bun, tc.state.Bun)
			}

			if loaded.YtDlp != tc.state.YtDlp {
				t.Errorf("YtDlp mismatch: got %q, want %q", loaded.YtDlp, tc.state.YtDlp)
			}
		})
	}
}
