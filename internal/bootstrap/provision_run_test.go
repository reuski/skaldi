// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var offlineDiscovery = []stubRoute{{pathPrefix: "/repos/", status: http.StatusServiceUnavailable}}

var downloadsUnavailable = &stubRoute{err: errors.New("network is unreachable")}

type provisionHarness struct {
	cfg           *Config
	toolsDir      string
	logs          *bytes.Buffer
	logger        *slog.Logger
	discoveryReqs *[]string
	downloadReqs  *[]string
}

func newProvisionHarness(t *testing.T) *provisionHarness {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("SKALDI_CONFIG", filepath.Join(home, "config", "skaldi", "config.json"))
	t.Setenv("SKALDI_PORT", "")
	t.Setenv("SKALDI_PROVISION", "")

	toolsDir := t.TempDir()
	for _, tool := range []string{"mpv", "ffmpeg", "yt-dlp", "bun"} {
		writeExecutable(t, filepath.Join(toolsDir, tool), shellScript("exit 0"))
	}
	t.Setenv("PATH", toolsDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if err := createDirectories(cfg); err != nil {
		t.Fatalf("createDirectories failed: %v", err)
	}

	logs := &bytes.Buffer{}
	return &provisionHarness{
		cfg:      cfg,
		toolsDir: toolsDir,
		logs:     logs,
		logger:   slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

func (h *provisionHarness) run(t *testing.T, discovery, downloads []stubRoute, downloadFallback *stubRoute) error {
	t.Helper()
	h.discoveryReqs = stubClient(t, &httpClient, discovery, nil)
	h.downloadReqs = stubClient(t, &downloadClient, downloads, downloadFallback)
	return Run(h.logger)
}

func (h *provisionHarness) seedInstalled(t *testing.T, ytDlpVersion, bunVersion string) {
	t.Helper()
	if err := SaveState(h.cfg.CacheDir, &State{YtDlp: ytDlpVersion, Bun: bunVersion}); err != nil {
		t.Fatal(err)
	}
	h.installYtDlp(t, "echo "+ytDlpVersion)
	h.installBun(t, "echo "+bunVersion)
}

func (h *provisionHarness) seedMatchingCache(t *testing.T, ytDlpVersion, bunVersion string) {
	t.Helper()
	if err := SaveCachedVersions(h.cfg.CacheDir, &CachedVersions{
		Versions:  LatestVersions{YtDlp: ytDlpVersion, Bun: bunVersion},
		Channel:   versionChannel,
		CheckedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

func (h *provisionHarness) installYtDlp(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(h.cfg.YtDlpDir(), "_internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, h.cfg.YtDlpPath(), shellScript(body))
}

func (h *provisionHarness) installBun(t *testing.T, body string) {
	t.Helper()
	writeExecutable(t, h.cfg.BunPath(), shellScript(body))
}

func (h *provisionHarness) state(t *testing.T) State {
	t.Helper()
	state, err := LoadState(h.cfg.CacheDir)
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}
	return *state
}

func (h *provisionHarness) readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

func (h *provisionHarness) assertShim(t *testing.T) {
	t.Helper()
	want := shimContent(h.cfg.YtDlpPath(), h.cfg.BunPath())
	if got := h.readFile(t, h.cfg.ShimPath()); got != want {
		t.Errorf("shim = %q, want %q", got, want)
	}
}

func (h *provisionHarness) assertNoStagedCandidates(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(h.cfg.BinDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".bun-candidate-") {
			t.Errorf("staged bun candidate left behind: %s", entry.Name())
		}
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestRunProvisionDisabledSkipsUpstreamAndGeneratesShim(t *testing.T) {
	h := newProvisionHarness(t)
	t.Setenv("SKALDI_PROVISION", "0")

	if err := h.run(t, nil, nil, nil); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(*h.discoveryReqs) != 0 || len(*h.downloadReqs) != 0 {
		t.Fatalf("provisioning bypass made upstream requests: %v %v", *h.discoveryReqs, *h.downloadReqs)
	}

	ytDlpPath, err := exec.LookPath("yt-dlp")
	if err != nil {
		t.Fatal(err)
	}
	bunPath, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := h.readFile(t, h.cfg.ShimPath()), shimContent(ytDlpPath, h.cfg.BunPath()); got != want {
		t.Errorf("shim = %q, want %q", got, want)
	}
	if got, want := h.readFile(t, h.cfg.BunPath()), toolShimContent(bunPath); got != want {
		t.Errorf("bun shim = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(h.cfg.BinDir, "bun")); err != nil {
		t.Errorf("bun shim missing: %v", err)
	}
}

func TestRunRecordsInstalledVersionsAfterInstallingFreshVersions(t *testing.T) {
	h := newProvisionHarness(t)

	discovery := []stubRoute{
		discoveryRoute(nightlyReleasePath, "2026.09.28.000000"),
		discoveryRoute(bunReleasePath, "bun-v1.2.3"),
	}
	downloads := []stubRoute{
		{pathPrefix: ytDlpArtifactPrefix, status: http.StatusOK, body: ytDlpArchive(t)},
		{pathPrefix: bunArtifactPrefix, status: http.StatusOK, body: bunArchive(t, "exit 0")},
	}
	if err := h.run(t, discovery, downloads, nil); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if got := h.state(t); got.YtDlp != "2026.09.28.000000" || got.Bun != "1.2.3" {
		t.Errorf("recorded state = %+v, want the installed versions", got)
	}
	if _, err := os.Stat(filepath.Join(h.cfg.YtDlpDir(), "_internal")); err != nil {
		t.Errorf("yt-dlp runtime layout missing: %v", err)
	}
	h.assertShim(t)
	h.assertNoStagedCandidates(t)
}

func TestRunReusesVerifiedRuntimesWhenDownloadsUnavailable(t *testing.T) {
	h := newProvisionHarness(t)
	h.seedInstalled(t, "2026.09.26.232945", "1.1.0")
	if err := SaveCachedVersions(h.cfg.CacheDir, &CachedVersions{
		Versions:  LatestVersions{YtDlp: "2099.01.01.000000", Bun: "9.9.9"},
		Channel:   versionChannel,
		CheckedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	if err := h.run(t, offlineDiscovery, nil, downloadsUnavailable); err != nil {
		t.Fatalf("Run failed instead of reusing verified runtimes: %v", err)
	}

	if got := h.state(t); got.YtDlp != "2026.09.26.232945" || got.Bun != "1.1.0" {
		t.Errorf("recorded state advanced past what was installed: %+v", got)
	}
	if got := h.readFile(t, h.cfg.YtDlpPath()); got != shellScript("echo 2026.09.26.232945") {
		t.Errorf("installed yt-dlp replaced during fallback: %q", got)
	}
	if got := h.readFile(t, h.cfg.BunPath()); got != shellScript("echo 1.1.0") {
		t.Errorf("installed bun replaced during fallback: %q", got)
	}
	h.assertShim(t)

	logs := h.logs.String()
	for _, runtime := range []string{"yt-dlp", "bun"} {
		if !strings.Contains(logs, runtime+" download unavailable, reusing installed version") {
			t.Errorf("fallback for %s not visible in logs:\n%s", runtime, logs)
		}
	}
	if strings.Contains(logs, "level=ERROR") {
		t.Errorf("fallback reported as a failure:\n%s", logs)
	}
}

func TestRunFallsBackToInstalledYtDlpWhenOnlyBunUpdates(t *testing.T) {
	h := newProvisionHarness(t)
	h.seedInstalled(t, "2026.09.26.232945", "1.1.0")

	discovery := []stubRoute{
		discoveryRoute(nightlyReleasePath, "2026.09.28.000000"),
		discoveryRoute(bunReleasePath, "bun-v1.2.3"),
	}
	downloads := []stubRoute{
		{pathPrefix: bunArtifactPrefix, status: http.StatusOK, body: bunArchive(t, "exit 0")},
	}
	if err := h.run(t, discovery, downloads, downloadsUnavailable); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	got := h.state(t)
	if got.YtDlp != "2026.09.26.232945" {
		t.Errorf("yt-dlp recorded as %q, want the still-installed version", got.YtDlp)
	}
	if got.Bun != "1.2.3" {
		t.Errorf("bun recorded as %q, want the installed 1.2.3", got.Bun)
	}
	if content := h.readFile(t, h.cfg.YtDlpPath()); content != shellScript("echo 2026.09.26.232945") {
		t.Errorf("installed yt-dlp replaced during fallback: %q", content)
	}
	h.assertShim(t)
	h.assertNoStagedCandidates(t)
}

func TestRunChecksBothUpstreamsOnEveryStartup(t *testing.T) {
	h := newProvisionHarness(t)
	h.seedInstalled(t, "2026.09.26.232945", "1.1.0")
	if err := SaveCachedVersions(h.cfg.CacheDir, &CachedVersions{
		Versions:  LatestVersions{YtDlp: "2026.09.26.232945", Bun: "1.1.0"},
		Channel:   versionChannel,
		CheckedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	discovery := []stubRoute{
		{pathPrefix: nightlyReleasePath, status: http.StatusServiceUnavailable},
		discoveryRoute(bunReleasePath, "bun-v1.1.0"),
	}
	if err := h.run(t, discovery, nil, nil); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(*h.discoveryReqs) != 2 {
		t.Fatalf("discovery requests = %v, want both repositories", *h.discoveryReqs)
	}
	if len(*h.downloadReqs) != 0 {
		t.Errorf("downloads requested for unchanged versions: %v", *h.downloadReqs)
	}
	if got := h.state(t); got.YtDlp != "2026.09.26.232945" || got.Bun != "1.1.0" {
		t.Errorf("recorded state = %+v, want it unchanged", got)
	}
	if logs := h.logs.String(); !strings.Contains(logs, "cannot be guaranteed") {
		t.Errorf("unconfirmed latest version not warned about:\n%s", logs)
	}
}

func TestRunWarnsWhenCachedVersionsAreInstalled(t *testing.T) {
	h := newProvisionHarness(t)
	h.seedInstalled(t, "2026.09.26.232945", "1.1.0")
	if err := SaveCachedVersions(h.cfg.CacheDir, &CachedVersions{
		Versions:  LatestVersions{YtDlp: "2026.09.27.232945", Bun: "1.2.3"},
		Channel:   versionChannel,
		CheckedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	downloads := []stubRoute{
		{pathPrefix: ytDlpArtifactPrefix, status: http.StatusOK, body: ytDlpArchive(t)},
		{pathPrefix: bunArtifactPrefix, status: http.StatusOK, body: bunArchive(t, "exit 0")},
	}
	if err := h.run(t, offlineDiscovery, downloads, nil); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if got := h.state(t); got.YtDlp != "2026.09.27.232945" || got.Bun != "1.2.3" {
		t.Errorf("recorded state = %+v, want the cached versions", got)
	}
	logs := h.logs.String()
	for _, runtime := range []string{"yt-dlp", "bun"} {
		if !strings.Contains(logs, "latest "+runtime+" cannot be guaranteed") {
			t.Errorf("unconfirmed latest %s not warned about:\n%s", runtime, logs)
		}
	}
}

func TestRunFailsWithoutUsableRecordedRuntimes(t *testing.T) {
	t.Run("no recorded state", func(t *testing.T) {
		h := newProvisionHarness(t)
		err := h.run(t, offlineDiscovery, nil, downloadsUnavailable)
		if err == nil {
			t.Fatal("expected failure without a recorded runtime and no upstream")
		}
		if !strings.Contains(err.Error(), "no previously installed yt-dlp is recorded") {
			t.Errorf("error = %v", err)
		}
		if len(*h.downloadReqs) != 0 {
			t.Errorf("downloads attempted without a resolved version: %v", *h.downloadReqs)
		}
	})

	t.Run("recorded yt-dlp runtime missing", func(t *testing.T) {
		h := newProvisionHarness(t)
		h.seedInstalled(t, "2026.09.26.232945", "1.1.0")
		if err := os.RemoveAll(h.cfg.YtDlpDir()); err != nil {
			t.Fatal(err)
		}
		err := h.run(t, offlineDiscovery, nil, downloadsUnavailable)
		if err == nil {
			t.Fatal("expected failure with the recorded yt-dlp runtime missing")
		}
		if !strings.Contains(err.Error(), "is missing") {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("recorded yt-dlp does not run", func(t *testing.T) {
		h := newProvisionHarness(t)
		h.seedInstalled(t, "2026.09.26.232945", "1.1.0")
		h.installYtDlp(t, "exit 1")
		err := h.run(t, offlineDiscovery, nil, downloadsUnavailable)
		if err == nil {
			t.Fatal("expected failure when the recorded yt-dlp fails validation")
		}
		if !strings.Contains(err.Error(), "failed validation") {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("recorded bun does not run", func(t *testing.T) {
		h := newProvisionHarness(t)
		h.seedInstalled(t, "2026.09.26.232945", "1.1.0")
		h.installBun(t, "exit 1")
		err := h.run(t, offlineDiscovery, nil, downloadsUnavailable)
		if err == nil {
			t.Fatal("expected failure when the recorded bun fails its probe")
		}
		if !strings.Contains(err.Error(), "bun --version failed") {
			t.Errorf("error = %v", err)
		}
	})
}

func TestRunVerifiesCachedMatchingRuntimesOffline(t *testing.T) {
	t.Run("working matching runtimes are reused without downloading", func(t *testing.T) {
		h := newProvisionHarness(t)
		h.seedInstalled(t, "2026.09.26.232945", "1.1.0")
		h.seedMatchingCache(t, "2026.09.26.232945", "1.1.0")

		if err := h.run(t, offlineDiscovery, nil, downloadsUnavailable); err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		if len(*h.downloadReqs) != 0 {
			t.Errorf("downloads requested for matching versions: %v", *h.downloadReqs)
		}
		if got := h.state(t); got.YtDlp != "2026.09.26.232945" || got.Bun != "1.1.0" {
			t.Errorf("recorded state = %+v, want it unchanged", got)
		}
		h.assertShim(t)
	})

	t.Run("matching yt-dlp that fails to run is fatal", func(t *testing.T) {
		h := newProvisionHarness(t)
		h.seedInstalled(t, "2026.09.26.232945", "1.1.0")
		h.seedMatchingCache(t, "2026.09.26.232945", "1.1.0")
		h.installYtDlp(t, "exit 1")

		err := h.run(t, offlineDiscovery, nil, downloadsUnavailable)
		if err == nil {
			t.Fatal("expected failure for a recorded yt-dlp that does not run")
		}
		if !strings.Contains(err.Error(), "failed validation") {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("matching yt-dlp that is not executable is fatal", func(t *testing.T) {
		h := newProvisionHarness(t)
		h.seedInstalled(t, "2026.09.26.232945", "1.1.0")
		h.seedMatchingCache(t, "2026.09.26.232945", "1.1.0")
		h.installYtDlp(t, "exit 0")
		if err := os.Chmod(h.cfg.YtDlpPath(), 0o644); err != nil {
			t.Fatal(err)
		}

		err := h.run(t, offlineDiscovery, nil, downloadsUnavailable)
		if err == nil {
			t.Fatal("expected failure for a non-executable recorded yt-dlp")
		}
		if !strings.Contains(err.Error(), "failed validation") {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("matching yt-dlp exceeding the startup budget is fatal", func(t *testing.T) {
		h := newProvisionHarness(t)
		h.seedInstalled(t, "2026.09.26.232945", "1.1.0")
		h.seedMatchingCache(t, "2026.09.26.232945", "1.1.0")
		// Warm-up passes; the next probe exceeds the startup budget.
		marker := filepath.Join(t.TempDir(), "warmed")
		h.installYtDlp(t, fmt.Sprintf("if [ -f '%s' ]; then sleep 4; fi\ntouch '%s'\nexit 0", marker, marker))

		err := h.run(t, offlineDiscovery, nil, downloadsUnavailable)
		if err == nil {
			t.Fatal("expected failure for a recorded yt-dlp exceeding the startup budget")
		}
		if !strings.Contains(err.Error(), "performance requirement") {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("matching bun that fails its probe is fatal", func(t *testing.T) {
		h := newProvisionHarness(t)
		h.seedInstalled(t, "2026.09.26.232945", "1.1.0")
		h.seedMatchingCache(t, "2026.09.26.232945", "1.1.0")
		h.installBun(t, "exit 1")

		err := h.run(t, offlineDiscovery, nil, downloadsUnavailable)
		if err == nil {
			t.Fatal("expected failure for a recorded bun that does not run")
		}
		if !strings.Contains(err.Error(), "bun --version failed") {
			t.Errorf("error = %v", err)
		}
	})
}

func TestRunFailsOnBrokenUpstreamInsteadOfFallingBack(t *testing.T) {
	t.Run("corrupt yt-dlp archive", func(t *testing.T) {
		h := newProvisionHarness(t)
		h.seedInstalled(t, "2026.09.26.232945", "1.1.0")

		discovery := []stubRoute{discoveryRoute(nightlyReleasePath, "2026.09.28.000000")}
		downloads := []stubRoute{
			{pathPrefix: ytDlpArtifactPrefix, status: http.StatusOK, body: []byte("not a zip archive")},
		}
		err := h.run(t, discovery, downloads, downloadsUnavailable)
		if err == nil {
			t.Fatal("expected failure for a corrupt archive")
		}
		if !strings.Contains(err.Error(), "zip archive") {
			t.Errorf("error = %v", err)
		}
		if got := h.state(t); got.YtDlp != "2026.09.26.232945" {
			t.Errorf("recorded version advanced after a failed extraction: %+v", got)
		}
		if content := h.readFile(t, h.cfg.YtDlpPath()); content != shellScript("echo 2026.09.26.232945") {
			t.Errorf("installed yt-dlp replaced: %q", content)
		}
	})

	t.Run("malformed release metadata", func(t *testing.T) {
		h := newProvisionHarness(t)
		discovery := []stubRoute{
			{pathPrefix: nightlyReleasePath, status: http.StatusOK, body: []byte("not json")},
			discoveryRoute(bunReleasePath, "bun-v1.2.3"),
		}
		err := h.run(t, discovery, nil, downloadsUnavailable)
		if err == nil {
			t.Fatal("expected failure for malformed release metadata")
		}
		if !strings.Contains(err.Error(), "malformed release metadata") {
			t.Errorf("error = %v", err)
		}
		if len(*h.discoveryReqs) != 2 {
			t.Errorf("discovery requests = %v, want both repositories", *h.discoveryReqs)
		}
		if len(*h.downloadReqs) != 0 {
			t.Errorf("downloads attempted after a fatal discovery failure: %v", *h.downloadReqs)
		}
	})

	t.Run("staged bun candidate fails its probe", func(t *testing.T) {
		h := newProvisionHarness(t)
		h.seedInstalled(t, "2026.09.26.232945", "1.1.0")

		discovery := []stubRoute{
			discoveryRoute(nightlyReleasePath, "2026.09.26.232945"),
			discoveryRoute(bunReleasePath, "bun-v1.2.3"),
		}
		downloads := []stubRoute{
			{pathPrefix: bunArtifactPrefix, status: http.StatusOK, body: bunArchive(t, "exit 1")},
		}
		err := h.run(t, discovery, downloads, downloadsUnavailable)
		if err == nil {
			t.Fatal("expected failure for a broken bun candidate")
		}
		if !strings.Contains(err.Error(), "bun --version failed") {
			t.Errorf("error = %v", err)
		}
		if content := h.readFile(t, h.cfg.BunPath()); content != shellScript("echo 1.1.0") {
			t.Errorf("installed bun replaced by a broken candidate: %q", content)
		}
		if got := h.state(t); got.Bun != "1.1.0" {
			t.Errorf("recorded bun advanced after a failed install: %+v", got)
		}
		h.assertNoStagedCandidates(t)
	})

	t.Run("installed versions cannot be recorded", func(t *testing.T) {
		h := newProvisionHarness(t)
		if err := os.MkdirAll(filepath.Join(h.cfg.CacheDir, "versions.json"), 0o755); err != nil {
			t.Fatal(err)
		}

		discovery := []stubRoute{
			discoveryRoute(nightlyReleasePath, "2026.09.28.000000"),
			discoveryRoute(bunReleasePath, "bun-v1.2.3"),
		}
		downloads := []stubRoute{
			{pathPrefix: ytDlpArtifactPrefix, status: http.StatusOK, body: ytDlpArchive(t)},
			{pathPrefix: bunArtifactPrefix, status: http.StatusOK, body: bunArchive(t, "exit 0")},
		}
		err := h.run(t, discovery, downloads, nil)
		if err == nil {
			t.Fatal("expected failure when the installed version cannot be recorded")
		}
		if !strings.Contains(err.Error(), "failed to record installed versions") {
			t.Errorf("error = %v", err)
		}
	})
}
