// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

const (
	nightlyReleasePath = "/repos/yt-dlp/yt-dlp-nightly-builds/releases/latest"
	bunReleasePath     = "/repos/oven-sh/bun/releases/latest"
)

const (
	ytDlpArtifactPrefix = "/yt-dlp/yt-dlp-nightly-builds/releases/download/"
	bunArtifactPrefix   = "/oven-sh/bun/releases/download/"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type stubRoute struct {
	pathPrefix string
	status     int
	body       []byte
	bodyErr    error
	err        error
}

func (r stubRoute) response() *http.Response {
	var body io.ReadCloser
	if r.bodyErr != nil {
		body = io.NopCloser(errReader{err: r.bodyErr})
	} else {
		body = io.NopCloser(bytes.NewReader(r.body))
	}
	return &http.Response{
		StatusCode: r.status,
		Status:     fmt.Sprintf("%d %s", r.status, http.StatusText(r.status)),
		Header:     http.Header{},
		Body:       body,
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func stubClient(t *testing.T, target **http.Client, routes []stubRoute, fallback *stubRoute) *[]string {
	t.Helper()
	requested := &[]string{}
	previous := *target
	*target = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			*requested = append(*requested, req.URL.Path)
			for _, route := range routes {
				if strings.HasPrefix(req.URL.Path, route.pathPrefix) {
					if route.err != nil {
						return nil, route.err
					}
					return route.response(), nil
				}
			}
			if fallback == nil {
				return nil, fmt.Errorf("unexpected upstream request: %s", req.URL.Path)
			}
			if fallback.err != nil {
				return nil, fallback.err
			}
			return fallback.response(), nil
		}),
	}
	t.Cleanup(func() { *target = previous })
	return requested
}

func stubGitHub(t *testing.T, tags map[string]string, status int) *[]string {
	t.Helper()
	var (
		routes   []stubRoute
		fallback *stubRoute
	)
	if status == http.StatusOK {
		for path, tag := range tags {
			routes = append(routes, stubRoute{
				pathPrefix: path,
				status:     http.StatusOK,
				body:       []byte(fmt.Sprintf(`{"tag_name":%q}`, tag)),
			})
		}
	} else {
		fallback = &stubRoute{status: status}
	}
	return stubClient(t, &httpClient, routes, fallback)
}

func stubGitHubRoutes(t *testing.T, routes []stubRoute) *[]string {
	t.Helper()
	return stubClient(t, &httpClient, routes, nil)
}

func zipArchive(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range entries {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func ytDlpArchive(t *testing.T) []byte {
	t.Helper()
	info, err := GetPlatformInfo()
	if err != nil {
		t.Fatal(err)
	}
	return zipArchive(t, map[string]string{
		"_internal/runtime.txt": "runtime",
		info.YtDlpExecutable:    shellScript("exit 0"),
	})
}

func bunArchive(t *testing.T, script string) []byte {
	t.Helper()
	info, err := GetPlatformInfo()
	if err != nil {
		t.Fatal(err)
	}
	return zipArchive(t, map[string]string{
		strings.TrimSuffix(info.BunArtifact, ".zip") + "/bun": shellScript(script),
	})
}

func shellScript(body string) string {
	return "#!/bin/sh\n" + body + "\n"
}
