// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"fmt"
	"runtime"
)

type PlatformInfo struct {
	YtDlpArtifact   string
	YtDlpExecutable string
	BunArtifact     string
}

func GetPlatformInfo() (*PlatformInfo, error) {
	goos := runtime.GOOS
	arch := runtime.GOARCH

	var ytDlpArtifact, ytDlpExecutable, bun string

	switch goos {
	case "linux":
		switch arch {
		case "amd64":
			ytDlpArtifact = "yt-dlp_linux.zip"
			ytDlpExecutable = "yt-dlp_linux"
			bun = "bun-linux-x64.zip"
		case "arm64":
			ytDlpArtifact = "yt-dlp_linux_aarch64.zip"
			ytDlpExecutable = "yt-dlp_linux_aarch64"
			bun = "bun-linux-aarch64.zip"
		default:
			return nil, fmt.Errorf("unsupported linux architecture: %s", arch)
		}
	case "darwin":
		ytDlpArtifact = "yt-dlp_macos.zip"
		ytDlpExecutable = "yt-dlp_macos"
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

	return &PlatformInfo{
		YtDlpArtifact:   ytDlpArtifact,
		YtDlpExecutable: ytDlpExecutable,
		BunArtifact:     bun,
	}, nil
}
