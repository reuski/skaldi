// SPDX-License-Identifier: AGPL-3.0-or-later

package resolver

import (
	"os"
	"path/filepath"
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
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte("secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile token failed: %v", err)
	}

	path := filepath.Join(dir, "config.json")
	data := []byte(`{
  "opensubsonic": {
    "enabled": true,
    "library_id": "personal",
    "base_url": "https://demo.example.com/rest/",
    "username": "alice",
    "token_file": "` + tokenPath + `"
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

func TestLoadOpenSubsonicConfig_TokenFile(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte("filesecret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile token failed: %v", err)
	}

	path := filepath.Join(dir, "config.json")
	data := []byte(`{
  "opensubsonic": {
    "enabled": true,
    "library_id": "personal",
    "base_url": "https://demo.example.com",
    "username": "alice",
    "token_file": "` + tokenPath + `"
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
	} else if cfg.Token != "filesecret" {
		t.Fatalf("Token = %q, want filesecret", cfg.Token)
	}
}

func TestLoadOpenSubsonicConfig_IncompleteSilentlyDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{
  "opensubsonic": {
    "enabled": true,
    "library_id": "personal",
    "base_url": "https://demo.example.com",
    "username": "alice",
    "token_file": "/nonexistent/token"
  }
}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg, err := loadOpenSubsonicConfig(path)
	if err != nil {
		t.Fatalf("loadOpenSubsonicConfig failed: %v", err)
	}
	if cfg != nil {
		t.Fatalf("cfg = %#v, want nil (silent disable)", cfg)
	}
}

func TestResolverNew_SilentlyDisablesIncompleteOpenSubsonic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{
  "opensubsonic": {
    "enabled": true,
    "library_id": "personal",
    "base_url": "https://demo.example.com",
    "username": "alice",
    "token_file": "/nonexistent/token"
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
	if warnings := r.Warnings(); len(warnings) != 0 {
		t.Fatalf("warnings = %d, want 0 (silent)", len(warnings))
	}
}
