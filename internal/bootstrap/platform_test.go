// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"runtime"
	"strings"
	"testing"
)

func TestGetPlatformInfo(t *testing.T) {
	info, err := GetPlatformInfo()
	if err != nil {
		t.Fatalf("GetPlatformInfo failed: %v", err)
	}

	if info.YtDlpArtifact == "" {
		t.Error("YtDlpArtifact should not be empty")
	}

	if info.BunArtifact == "" {
		t.Error("BunArtifact should not be empty")
	}
}

func TestGetPlatformInfo_ArtifactShape(t *testing.T) {
	info, err := GetPlatformInfo()
	if err != nil {
		t.Fatalf("GetPlatformInfo failed: %v", err)
	}

	goos := runtime.GOOS

	if goos == "linux" || goos == "darwin" {
		if !strings.HasPrefix(info.YtDlpArtifact, "yt-dlp") {
			t.Errorf("YtDlpArtifact should start with yt-dlp, got %s", info.YtDlpArtifact)
		}
		if strings.Contains(info.YtDlpArtifact, ".") {
			t.Errorf("YtDlpArtifact should be a raw binary (no extension), got %s", info.YtDlpArtifact)
		}
		if !strings.HasSuffix(info.BunArtifact, ".zip") {
			t.Errorf("BunArtifact should end with .zip, got %s", info.BunArtifact)
		}
	}
}

func TestGetPlatformInfo_OSToken(t *testing.T) {
	info, err := GetPlatformInfo()
	if err != nil {
		t.Fatalf("GetPlatformInfo failed: %v", err)
	}

	goos := runtime.GOOS

	switch goos {
	case "linux":
		if !strings.Contains(info.YtDlpArtifact, "linux") {
			t.Errorf("YtDlpArtifact should contain 'linux', got %s", info.YtDlpArtifact)
		}
		if !strings.Contains(info.BunArtifact, "linux") {
			t.Errorf("BunArtifact should contain 'linux', got %s", info.BunArtifact)
		}
	case "darwin":
		if !strings.Contains(info.YtDlpArtifact, "macos") {
			t.Errorf("YtDlpArtifact should contain 'macos', got %s", info.YtDlpArtifact)
		}
		if !strings.Contains(info.BunArtifact, "darwin") {
			t.Errorf("BunArtifact should contain 'darwin', got %s", info.BunArtifact)
		}
	}
}

func TestGetPlatformInfo_ArchSpecifics(t *testing.T) {
	info, err := GetPlatformInfo()
	if err != nil {
		t.Fatalf("GetPlatformInfo failed: %v", err)
	}

	goos := runtime.GOOS
	goarch := runtime.GOARCH

	if goos == "linux" {
		switch goarch {
		case "amd64":
			if !strings.Contains(info.BunArtifact, "x64") {
				t.Errorf("BunArtifact should contain x64 on amd64, got %s", info.BunArtifact)
			}
			if info.YtDlpArtifact != "yt-dlp_linux" {
				t.Errorf("YtDlpArtifact should be yt-dlp_linux on amd64, got %s", info.YtDlpArtifact)
			}
		case "arm64":
			if !strings.Contains(info.BunArtifact, "aarch64") {
				t.Errorf("BunArtifact should contain aarch64 on arm64, got %s", info.BunArtifact)
			}
			if !strings.Contains(info.YtDlpArtifact, "aarch64") {
				t.Errorf("YtDlpArtifact should contain aarch64 on arm64, got %s", info.YtDlpArtifact)
			}
		}
	}
}

func TestPlatformInfo_Struct(t *testing.T) {
	tests := []struct {
		name string
		info PlatformInfo
	}{
		{
			name: "linux_amd64",
			info: PlatformInfo{
				YtDlpArtifact: "yt-dlp_linux",
				BunArtifact:   "bun-linux-x64.zip",
			},
		},
		{
			name: "linux_arm64",
			info: PlatformInfo{
				YtDlpArtifact: "yt-dlp_linux_aarch64",
				BunArtifact:   "bun-linux-aarch64.zip",
			},
		},
		{
			name: "darwin_amd64",
			info: PlatformInfo{
				YtDlpArtifact: "yt-dlp_macos",
				BunArtifact:   "bun-darwin-x64.zip",
			},
		},
		{
			name: "darwin_arm64",
			info: PlatformInfo{
				YtDlpArtifact: "yt-dlp_macos",
				BunArtifact:   "bun-darwin-aarch64.zip",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.info.YtDlpArtifact == "" {
				t.Error("YtDlpArtifact should not be empty")
			}
			if tc.info.BunArtifact == "" {
				t.Error("BunArtifact should not be empty")
			}
		})
	}
}
