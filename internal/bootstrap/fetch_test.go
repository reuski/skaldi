// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadFileSucceeds(t *testing.T) {
	body := []byte("archive bytes")
	requested := stubClient(t, &downloadClient, []stubRoute{
		{pathPrefix: "/artifact.zip", status: http.StatusOK, body: body},
	}, nil)

	dest := filepath.Join(t.TempDir(), "download.tmp")
	if err := DownloadFile("https://example.test/artifact.zip", dest); err != nil {
		t.Fatalf("DownloadFile failed: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Errorf("downloaded %q, want %q", got, body)
	}
	if len(*requested) != 1 {
		t.Errorf("requests = %v, want one", *requested)
	}
}

func TestDownloadFileClassifiesFailures(t *testing.T) {
	t.Run("transport failure is recoverable", func(t *testing.T) {
		stubClient(t, &downloadClient, nil, &stubRoute{err: errors.New("network is unreachable")})
		err := DownloadFile("https://example.test/artifact.zip", filepath.Join(t.TempDir(), "a.tmp"))
		if !errors.Is(err, errUpstreamUnavailable) {
			t.Fatalf("transport failure not classified as unavailable: %v", err)
		}
	})

	t.Run("non-200 status is recoverable", func(t *testing.T) {
		stubClient(t, &downloadClient, nil, &stubRoute{status: http.StatusNotFound})
		err := DownloadFile("https://example.test/artifact.zip", filepath.Join(t.TempDir(), "a.tmp"))
		if !errors.Is(err, errUpstreamUnavailable) {
			t.Fatalf("missing artifact not classified as unavailable: %v", err)
		}
	})

	t.Run("body read failure is recoverable", func(t *testing.T) {
		stubClient(t, &downloadClient, []stubRoute{
			{pathPrefix: "/artifact.zip", status: http.StatusOK, bodyErr: errors.New("connection reset")},
		}, nil)
		err := DownloadFile("https://example.test/artifact.zip", filepath.Join(t.TempDir(), "a.tmp"))
		if !errors.Is(err, errUpstreamUnavailable) {
			t.Fatalf("truncated download not classified as unavailable: %v", err)
		}
	})

	t.Run("unwritable destination stays fatal", func(t *testing.T) {
		stubClient(t, &downloadClient, nil, &stubRoute{status: http.StatusOK})
		err := DownloadFile("https://example.test/artifact.zip", t.TempDir())
		if err == nil {
			t.Fatal("expected an error when the destination cannot be created")
		}
		if errors.Is(err, errUpstreamUnavailable) {
			t.Fatalf("local filesystem failure classified as unavailable: %v", err)
		}
	})
}

func TestIsDownloadDestinationError(t *testing.T) {
	writeErr := &fs.PathError{Op: "write", Path: "a.tmp", Err: errors.New("no space left on device")}
	if !isDownloadDestinationError(writeErr) {
		t.Error("filesystem write error not recognised as a destination error")
	}
	if isDownloadDestinationError(errors.New("unexpected EOF")) {
		t.Error("response body read error treated as a destination error")
	}
	if isDownloadDestinationError(nil) {
		t.Error("nil error treated as a destination error")
	}
}

func TestConstructArtifactURLs(t *testing.T) {
	nightly := ConstructYtDlpURL("2026.09.27.232945", "yt-dlp_linux.zip")
	if !strings.HasPrefix(nightly, "https://github.com/yt-dlp/yt-dlp-nightly-builds/releases/download/") {
		t.Errorf("nightly artifact URL = %q", nightly)
	}
	bun := ConstructBunURL("1.2.3", "bun-linux-x64.zip")
	want := "https://github.com/oven-sh/bun/releases/download/bun-v1.2.3/bun-linux-x64.zip"
	if bun != want {
		t.Errorf("ConstructBunURL = %q, want %q", bun, want)
	}
}
