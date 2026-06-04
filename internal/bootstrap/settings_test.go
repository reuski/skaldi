// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestResolvePort(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		settings Settings
		want     int
	}{
		{"default", "", Settings{}, defaultPort},
		{"file", "", settingsWithPort(9090), 9090},
		{"env wins over file", "7000", settingsWithPort(9090), 7000},
		{"invalid env falls back to file", "bogus", settingsWithPort(9090), 9090},
		{"zero file falls back to default", "", settingsWithPort(0), defaultPort},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env == "" {
				t.Setenv("SKALDI_PORT", "")
				os.Unsetenv("SKALDI_PORT")
			} else {
				t.Setenv("SKALDI_PORT", tt.env)
			}
			if got := resolvePort(tt.settings); got != tt.want {
				t.Errorf("resolvePort() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestResolveProvision(t *testing.T) {
	on, off := true, false
	tests := []struct {
		name     string
		env      string
		setEnv   bool
		settings Settings
		want     bool
	}{
		{"default true", "", false, Settings{}, true},
		{"file false", "", false, Settings{Provision: &off}, false},
		{"file true", "", false, Settings{Provision: &on}, true},
		{"env 0 wins over file true", "0", true, Settings{Provision: &on}, false},
		{"env 1 wins over file false", "1", true, Settings{Provision: &off}, true},
		{"env false keyword", "false", true, Settings{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv("SKALDI_PROVISION")
			if tt.setEnv {
				t.Setenv("SKALDI_PROVISION", tt.env)
			}
			if got := resolveProvision(tt.settings); got != tt.want {
				t.Errorf("resolveProvision() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadSettings(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "absent.json")
	if s, err := loadSettings(missing); err != nil || s.Provision != nil || s.Server.Port != 0 {
		t.Errorf("missing file: got %+v err %v, want zero settings", s, err)
	}

	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"server":{"port":1234},"provision":false,"opensubsonic":{"enabled":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := loadSettings(path)
	if err != nil {
		t.Fatalf("loadSettings failed: %v", err)
	}
	if s.Server.Port != 1234 {
		t.Errorf("port = %d, want 1234", s.Server.Port)
	}
	if s.Provision == nil || *s.Provision {
		t.Errorf("provision = %v, want explicit false", s.Provision)
	}
}

func TestUseSystemTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shim is a POSIX sh script")
	}
	pathDir := t.TempDir()
	ytDlp := filepath.Join(pathDir, "yt-dlp")
	bun := filepath.Join(pathDir, "bun")
	for _, p := range []string{ytDlp, bun} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", pathDir)

	binDir := t.TempDir()
	cfg := &Config{BinDir: binDir}

	if err := useSystemTools(cfg, discardLogger()); err != nil {
		t.Fatalf("useSystemTools failed: %v", err)
	}

	shim, err := os.ReadFile(cfg.ShimPath())
	if err != nil {
		t.Fatalf("shim not written: %v", err)
	}
	content := string(shim)
	if !strings.Contains(content, ytDlp) {
		t.Errorf("shim missing resolved yt-dlp path: %q", content)
	}
	if !strings.Contains(content, "bun:"+bun) {
		t.Errorf("shim missing resolved bun path: %q", content)
	}
}

func TestUseSystemToolsMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	cfg := &Config{BinDir: t.TempDir()}
	if err := useSystemTools(cfg, discardLogger()); err == nil {
		t.Fatal("expected error when yt-dlp/bun absent from PATH")
	}
}

func settingsWithPort(p int) Settings {
	var s Settings
	s.Server.Port = p
	return s
}
