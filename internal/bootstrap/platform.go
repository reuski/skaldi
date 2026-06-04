// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"fmt"
	"runtime"
)

type PlatformInfo struct {
	YtDlpArtifact string
	BunArtifact   string
}

func GetPlatformInfo() (*PlatformInfo, error) {
	goos := runtime.GOOS
	arch := runtime.GOARCH

	var ytDlp, bun string

	switch goos {
	case "linux":
		switch arch {
		case "amd64":
			ytDlp = "yt-dlp_linux"
			bun = "bun-linux-x64.zip"
		case "arm64":
			ytDlp = "yt-dlp_linux_aarch64"
			bun = "bun-linux-aarch64.zip"
		default:
			return nil, fmt.Errorf("unsupported linux architecture: %s", arch)
		}
	case "darwin":
		ytDlp = "yt-dlp_macos"
		switch arch {
		case "amd64":
			bun = "bun-darwin-x64.zip"
		case "arm64":
			bun = "bun-darwin-aarch64.zip"
		default:
			return nil, fmt.Errorf("unsupported macos architecture: %s", arch)
		}
	default:
		return nil, fmt.Errorf("unsupported operating system: %s", goos)
	}

	return &PlatformInfo{YtDlpArtifact: ytDlp, BunArtifact: bun}, nil
}
