// SPDX-License-Identifier: AGPL-3.0-or-later

package resolver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reuski/skaldi/internal/bootstrap"
)

func TestLoadOpenSubsonicConfig_MissingOrEmpty(t *testing.T) {
	cfg, err := loadOpenSubsonicConfig(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("missing file error: %v", err)
	}
	if cfg != nil {
		t.Fatalf("missing file cfg = %#v, want nil", cfg)
	}

	emptyPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(emptyPath, []byte("\n"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	cfg, err = loadOpenSubsonicConfig(emptyPath)
	if err != nil {
		t.Fatalf("empty file error: %v", err)
	}
	if cfg != nil {
		t.Fatalf("empty file cfg = %#v, want nil", cfg)
	}
}

func TestLoadOpenSubsonicConfig_ValidEnabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{
  "opensubsonic": {
    "enabled": true,
    "library_id": "personal",
    "base_url": "https://demo.example.com/rest/",
    "username": "alice",
    "token": "secret"
  }
}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg, err := loadOpenSubsonicConfig(path)
	if err != nil {
		t.Fatalf("loadOpenSubsonicConfig failed: %v", err)
	}
	if cfg == nil {
		t.Fatal("cfg is nil")
	} else if cfg.BaseURL != "https://demo.example.com" {
		t.Fatalf("BaseURL = %q, want https://demo.example.com", cfg.BaseURL)
	} else if cfg.TimeoutMS != 2500 {
		t.Fatalf("TimeoutMS = %d, want 2500", cfg.TimeoutMS)
	}
}

func TestLoadOpenSubsonicConfig_InvalidEnabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{
  "opensubsonic": {
    "enabled": true,
    "library_id": "personal",
    "base_url": "https://demo.example.com"
  }
}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg, err := loadOpenSubsonicConfig(path)
	if err == nil {
		t.Fatalf("expected error, got cfg=%#v", cfg)
	}
}

func TestResolverNew_DisablesInvalidOpenSubsonicConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{
  "opensubsonic": {
    "enabled": true,
    "library_id": "personal",
    "base_url": "https://demo.example.com"
  }
}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	r, err := New(&bootstrap.Config{ConfigPath: path})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if r.subsonic != nil {
		t.Fatal("subsonic client should be disabled")
	}
	warnings := r.Warnings()
	if len(warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(warnings))
	}
	if got := warnings[0].Error(); got == "" || !strings.Contains(got, "opensubsonic disabled") {
		t.Fatalf("warning = %q, want opensubsonic disabled", got)
	}
}
