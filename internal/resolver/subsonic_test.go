// SPDX-License-Identifier: AGPL-3.0-or-later

package resolver

import (
	"crypto/md5"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
)

func TestSubsonicAuthParams(t *testing.T) {
	client := NewSubsonicClient(openSubsonicConfig{
		LibraryID: "personal",
		BaseURL:   "https://demo.example.com",
		Username:  "alice",
		Token:     "token-secret",
		TimeoutMS: 2500,
	})

	params, err := client.authParams()
	if err != nil {
		t.Fatalf("authParams failed: %v", err)
	}

	salt := params.Get("s")
	hash := params.Get("t")
	if salt == "" || hash == "" {
		t.Fatalf("salt/hash missing: s=%q t=%q", salt, hash)
	}

	expected := md5.Sum([]byte("token-secret" + salt))
	if hash != hex.EncodeToString(expected[:]) {
		t.Fatalf("hash = %q, want %q", hash, hex.EncodeToString(expected[:]))
	}

	encoded := params.Encode()
	if strings.Contains(encoded, "token-secret") {
		t.Fatalf("encoded params leaked token: %s", encoded)
	}
}

func TestSubsonicBuildStreamURL(t *testing.T) {
	client := NewSubsonicClient(openSubsonicConfig{
		LibraryID: "personal",
		BaseURL:   "https://demo.example.com",
		Username:  "alice",
		Token:     "token-secret",
		TimeoutMS: 2500,
	})

	streamURL, err := client.BuildStreamURL("track-1")
	if err != nil {
		t.Fatalf("BuildStreamURL failed: %v", err)
	}

	u, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if u.Path != "/rest/stream.view" {
		t.Fatalf("path = %q, want /rest/stream.view", u.Path)
	}
	if u.Query().Get("id") != "track-1" {
		t.Fatalf("id = %q, want track-1", u.Query().Get("id"))
	}
	if u.Query().Get("u") != "alice" {
		t.Fatalf("u = %q, want alice", u.Query().Get("u"))
	}
	if strings.Contains(streamURL, "token-secret") {
		t.Fatalf("streamURL leaked token: %s", streamURL)
	}
}
