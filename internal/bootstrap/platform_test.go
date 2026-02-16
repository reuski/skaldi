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

	if info.UvArtifact == "" {
		t.Error("UvArtifact should not be empty")
	}

	if info.BunArtifact == "" {
		t.Error("BunArtifact should not be empty")
	}
}

func TestGetPlatformInfo_ValidSuffixes(t *testing.T) {
	info, err := GetPlatformInfo()
	if err != nil {
		t.Fatalf("GetPlatformInfo failed: %v", err)
	}

	goos := runtime.GOOS

	if goos == "linux" || goos == "darwin" {
		if !strings.HasSuffix(info.UvArtifact, ".tar.gz") {
			t.Errorf("UvArtifact should end with .tar.gz, got %s", info.UvArtifact)
		}

		if !strings.HasSuffix(info.BunArtifact, ".zip") {
			t.Errorf("BunArtifact should end with .zip, got %s", info.BunArtifact)
		}
	}
}

func TestGetPlatformInfo_ContainsArch(t *testing.T) {
	info, err := GetPlatformInfo()
	if err != nil {
		t.Fatalf("GetPlatformInfo failed: %v", err)
	}

	goarch := runtime.GOARCH
	goos := runtime.GOOS

	var expectedArch string
	switch goarch {
	case "amd64":
		expectedArch = "x86_64"
	case "arm64":
		expectedArch = "aarch64"
	}

	if expectedArch != "" {
		if !strings.Contains(info.UvArtifact, expectedArch) {
			t.Errorf("UvArtifact should contain %s, got %s", expectedArch, info.UvArtifact)
		}
	}

	if goos == "linux" {
		if !strings.Contains(info.BunArtifact, "linux") {
			t.Errorf("BunArtifact should contain 'linux' on Linux, got %s", info.BunArtifact)
		}
	} else if goos == "darwin" {
		if !strings.Contains(info.BunArtifact, "darwin") {
			t.Errorf("BunArtifact should contain 'darwin' on macOS, got %s", info.BunArtifact)
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
		case "arm64":
			if !strings.Contains(info.BunArtifact, "aarch64") {
				t.Errorf("BunArtifact should contain aarch64 on arm64, got %s", info.BunArtifact)
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
				UvArtifact:  "uv-x86_64-unknown-linux-gnu.tar.gz",
				BunArtifact: "bun-linux-x64.zip",
			},
		},
		{
			name: "linux_arm64",
			info: PlatformInfo{
				UvArtifact:  "uv-aarch64-unknown-linux-gnu.tar.gz",
				BunArtifact: "bun-linux-aarch64.zip",
			},
		},
		{
			name: "darwin_amd64",
			info: PlatformInfo{
				UvArtifact:  "uv-x86_64-apple-darwin.tar.gz",
				BunArtifact: "bun-darwin-x64.zip",
			},
		},
		{
			name: "darwin_arm64",
			info: PlatformInfo{
				UvArtifact:  "uv-aarch64-apple-darwin.tar.gz",
				BunArtifact: "bun-darwin-aarch64.zip",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.info.UvArtifact == "" {
				t.Error("UvArtifact should not be empty")
			}
			if tc.info.BunArtifact == "" {
				t.Error("BunArtifact should not be empty")
			}
		})
	}
}
