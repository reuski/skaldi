// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func discoveryRoute(path, tag string) stubRoute {
	return stubRoute{
		pathPrefix: path,
		status:     http.StatusOK,
		body:       []byte(fmt.Sprintf(`{"tag_name":%q}`, tag)),
	}
}

func TestFetchLatestVersionsChecksUpstreamDespiteFreshCache(t *testing.T) {
	cacheDir := t.TempDir()
	if err := SaveCachedVersions(cacheDir, &CachedVersions{
		Versions:  LatestVersions{YtDlp: "2099.01.01.000000", Bun: "9.9.9"},
		Channel:   versionChannel,
		CheckedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	requested := stubGitHub(t, map[string]string{
		nightlyReleasePath: "2026.09.27.232945",
		bunReleasePath:     "bun-v1.2.3",
	}, http.StatusOK)

	ytDlp, bun := fetchLatestVersions(cacheDir, nil)
	if !ytDlp.Latest || ytDlp.Version != "2026.09.27.232945" {
		t.Errorf("yt-dlp = %+v, want upstream 2026.09.27.232945", ytDlp)
	}
	if !bun.Latest || bun.Version != "1.2.3" {
		t.Errorf("bun = %+v, want trimmed 1.2.3", bun)
	}
	if len(*requested) != 2 {
		t.Fatalf("upstream requests = %v, want one per repo despite a fresh cache", *requested)
	}

	saved, err := LoadCachedVersions(cacheDir)
	if err != nil || saved == nil {
		t.Fatalf("cache not readable after fetch: %v", err)
	}
	if saved.Versions.YtDlp != "2026.09.27.232945" || saved.Channel != versionChannel {
		t.Errorf("cache not refreshed: %+v", saved)
	}
}

func TestFetchLatestVersionsUsesNightlyRepository(t *testing.T) {
	requested := stubGitHub(t, map[string]string{
		nightlyReleasePath: "2026.09.27.232945",
		bunReleasePath:     "bun-v1.2.3",
	}, http.StatusOK)

	ytDlp, _ := fetchLatestVersions(t.TempDir(), nil)
	if (*requested)[0] != nightlyReleasePath {
		t.Errorf("version discovery hit %q, want %q", (*requested)[0], nightlyReleasePath)
	}

	wantURL := "https://github.com/yt-dlp/yt-dlp-nightly-builds/releases/download/2026.09.27.232945/yt-dlp_linux.zip"
	if url := ConstructYtDlpURL(ytDlp.Version, "yt-dlp_linux.zip"); url != wantURL {
		t.Errorf("ConstructYtDlpURL = %q, want %q", url, wantURL)
	}
}

func TestFetchLatestVersionsOfflineFallback(t *testing.T) {
	t.Run("matching channel reuses cache", func(t *testing.T) {
		cacheDir := t.TempDir()
		if err := SaveCachedVersions(cacheDir, &CachedVersions{
			Versions:  LatestVersions{YtDlp: "2026.09.26.232945", Bun: "1.1.0"},
			Channel:   versionChannel,
			CheckedAt: time.Now().Add(-48 * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		stubGitHub(t, nil, http.StatusServiceUnavailable)

		ytDlp, bun := fetchLatestVersions(cacheDir, nil)
		if ytDlp.Latest || bun.Latest {
			t.Errorf("cached fallback reported as checked upstream: %+v %+v", ytDlp, bun)
		}
		if ytDlp.Version != "2026.09.26.232945" || bun.Version != "1.1.0" {
			t.Errorf("fallback = %+v %+v, want cached versions", ytDlp, bun)
		}
		if !errors.Is(ytDlp.Err, errUpstreamUnavailable) || !errors.Is(bun.Err, errUpstreamUnavailable) {
			t.Errorf("cached fallback lost the availability error: %v %v", ytDlp.Err, bun.Err)
		}
	})

	t.Run("stable channel cache is not mistaken for nightly", func(t *testing.T) {
		cacheDir := t.TempDir()
		if err := SaveCachedVersions(cacheDir, &CachedVersions{
			Versions:  LatestVersions{YtDlp: "2026.09.27", Bun: "1.1.0"},
			CheckedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
		stubGitHub(t, nil, http.StatusServiceUnavailable)

		ytDlp, _ := fetchLatestVersions(cacheDir, nil)
		if ytDlp.Version != "" {
			t.Fatalf("stable-channel cache used as nightly version: %+v", ytDlp)
		}
		if !errors.Is(ytDlp.Err, errUpstreamUnavailable) {
			t.Fatalf("expected an availability error, got %v", ytDlp.Err)
		}
	})

	t.Run("no cache leaves the source unresolved", func(t *testing.T) {
		stubGitHub(t, nil, http.StatusServiceUnavailable)

		ytDlp, bun := fetchLatestVersions(t.TempDir(), nil)
		if ytDlp.Version != "" || ytDlp.Latest || ytDlp.Err == nil {
			t.Errorf("yt-dlp = %+v, want an unresolved source", ytDlp)
		}
		if bun.Version != "" || bun.Latest || bun.Err == nil {
			t.Errorf("bun = %+v, want an unresolved source", bun)
		}
	})
}

func TestFetchLatestVersionsKeepsFreshSourceWhenOtherFails(t *testing.T) {
	unavailableStatus := http.StatusServiceUnavailable

	t.Run("yt-dlp unavailable, bun fresh", func(t *testing.T) {
		cacheDir := t.TempDir()
		if err := SaveCachedVersions(cacheDir, &CachedVersions{
			Versions:  LatestVersions{YtDlp: "2026.09.26.232945", Bun: "1.1.0"},
			Channel:   versionChannel,
			CheckedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
		requested := stubGitHubRoutes(t, []stubRoute{
			{pathPrefix: nightlyReleasePath, status: unavailableStatus},
			discoveryRoute(bunReleasePath, "bun-v1.2.3"),
		})

		ytDlp, bun := fetchLatestVersions(cacheDir, nil)
		if ytDlp.Version != "2026.09.26.232945" || ytDlp.Latest {
			t.Errorf("yt-dlp = %+v, want the channel cache", ytDlp)
		}
		if !bun.Latest || bun.Version != "1.2.3" {
			t.Errorf("bun = %+v, want a fresh 1.2.3", bun)
		}
		if len(*requested) != 2 {
			t.Fatalf("upstream requests = %v, want both repos checked", *requested)
		}

		saved, err := LoadCachedVersions(cacheDir)
		if err != nil || saved == nil {
			t.Fatalf("cache not readable: %v", err)
		}
		if saved.Versions.Bun == "1.2.3" {
			t.Errorf("partial check recorded as a complete one: %+v", saved)
		}
	})

	t.Run("bun unavailable, yt-dlp fresh", func(t *testing.T) {
		cacheDir := t.TempDir()
		if err := SaveCachedVersions(cacheDir, &CachedVersions{
			Versions:  LatestVersions{YtDlp: "2026.09.26.232945", Bun: "1.1.0"},
			Channel:   versionChannel,
			CheckedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
		requested := stubGitHubRoutes(t, []stubRoute{
			{pathPrefix: bunReleasePath, status: unavailableStatus},
			discoveryRoute(nightlyReleasePath, "2026.09.27.232945"),
		})

		ytDlp, bun := fetchLatestVersions(cacheDir, nil)
		if !ytDlp.Latest || ytDlp.Version != "2026.09.27.232945" {
			t.Errorf("yt-dlp = %+v, want a fresh 2026.09.27.232945", ytDlp)
		}
		if bun.Version != "1.1.0" || bun.Latest {
			t.Errorf("bun = %+v, want the channel cache", bun)
		}
		if len(*requested) != 2 {
			t.Fatalf("upstream requests = %v, want both repos checked", *requested)
		}
	})

	t.Run("wrong-channel cache is not reused", func(t *testing.T) {
		cacheDir := t.TempDir()
		if err := SaveCachedVersions(cacheDir, &CachedVersions{
			Versions:  LatestVersions{YtDlp: "2026.09.26", Bun: "1.1.0"},
			CheckedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
		stubGitHub(t, nil, http.StatusServiceUnavailable)

		ytDlp, bun := fetchLatestVersions(cacheDir, nil)
		if ytDlp.Version != "" {
			t.Errorf("stable-channel yt-dlp cache reused: %+v", ytDlp)
		}
		if bun.Version != "" {
			t.Errorf("untagged bun cache reused: %+v", bun)
		}
	})
}

func TestFetchLatestVersionsRejectsMalformedReleaseMetadata(t *testing.T) {
	requested := stubGitHubRoutes(t, []stubRoute{
		{pathPrefix: nightlyReleasePath, status: http.StatusOK, body: []byte("not json")},
		discoveryRoute(bunReleasePath, "bun-v1.2.3"),
	})

	ytDlp, bun := fetchLatestVersions(t.TempDir(), nil)
	if ytDlp.Err == nil {
		t.Fatal("malformed release metadata accepted")
	}
	if errors.Is(ytDlp.Err, errUpstreamUnavailable) {
		t.Errorf("malformed metadata classified as an availability failure: %v", ytDlp.Err)
	}
	if ytDlp.Version != "" || ytDlp.Latest {
		t.Errorf("yt-dlp = %+v, want no version", ytDlp)
	}
	if !bun.Latest {
		t.Errorf("bun = %+v, want a fresh version despite the other source failing", bun)
	}
	if len(*requested) != 2 {
		t.Fatalf("upstream requests = %v, want both repos checked", *requested)
	}
}

func TestFetchLatestVersionsTreatsBodyReadFailureAsUnavailable(t *testing.T) {
	stubGitHubRoutes(t, []stubRoute{
		{pathPrefix: nightlyReleasePath, status: http.StatusOK, bodyErr: errors.New("connection reset")},
		discoveryRoute(bunReleasePath, "bun-v1.2.3"),
	})

	ytDlp, bun := fetchLatestVersions(t.TempDir(), nil)
	if !errors.Is(ytDlp.Err, errUpstreamUnavailable) {
		t.Fatalf("body read failure not classified as unavailable: %v", ytDlp.Err)
	}
	if ytDlp.Version != "" {
		t.Errorf("yt-dlp = %+v, want no version without a cache", ytDlp)
	}
	if !bun.Latest {
		t.Errorf("bun = %+v, want a fresh version", bun)
	}
}

func TestSelectSourceVersion(t *testing.T) {
	t.Run("fresh check wins over cache", func(t *testing.T) {
		got := selectSourceVersion("2026.09.27.232945", nil, "2026.09.26.232945")
		if !got.Latest || got.Version != "2026.09.27.232945" {
			t.Errorf("selectSourceVersion = %+v, want the fresh tag", got)
		}
	})

	t.Run("availability failure reuses the cache", func(t *testing.T) {
		err := fmt.Errorf("%w: request failed", errUpstreamUnavailable)
		got := selectSourceVersion("", err, "2026.09.26.232945")
		if got.Latest || got.Version != "2026.09.26.232945" {
			t.Errorf("selectSourceVersion = %+v, want the cached version", got)
		}
		if !errors.Is(got.Err, errUpstreamUnavailable) {
			t.Errorf("selectSourceVersion dropped the availability error: %v", got.Err)
		}
	})

	t.Run("other failures are reported as-is", func(t *testing.T) {
		err := errors.New("malformed release metadata")
		got := selectSourceVersion("", err, "2026.09.26.232945")
		if got.Version != "" || got.Latest {
			t.Errorf("selectSourceVersion = %+v, want no version", got)
		}
		if !errors.Is(got.Err, err) {
			t.Errorf("selectSourceVersion = %v, want the original error", got.Err)
		}
	})
}
