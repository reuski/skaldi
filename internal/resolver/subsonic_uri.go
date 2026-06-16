// SPDX-License-Identifier: AGPL-3.0-or-later

package resolver

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	SubsonicURIScheme    = "skaldi+subsonic"
	SubsonicCoverArtPath = "/art/subsonic"
)

type SubsonicRef struct {
	LibraryID string
	TrackID   string
}

func BuildSubsonicURI(libraryID, trackID string) string {
	return fmt.Sprintf("%s://%s/%s", SubsonicURIScheme, libraryID, url.PathEscape(trackID))
}

func BuildSubsonicCoverArtPath(libraryID, coverArtID string) string {
	if libraryID == "" || coverArtID == "" {
		return ""
	}
	params := url.Values{}
	params.Set("id", coverArtID)
	params.Set("library", libraryID)
	return SubsonicCoverArtPath + "?" + params.Encode()
}

func ParseSubsonicURI(raw string) (SubsonicRef, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != SubsonicURIScheme {
		return SubsonicRef{}, false
	}

	libraryID := strings.TrimSpace(u.Host)
	if libraryID == "" {
		return SubsonicRef{}, false
	}

	trackID := strings.TrimPrefix(u.Path, "/")
	trackID, err = url.PathUnescape(trackID)
	if err != nil || trackID == "" {
		return SubsonicRef{}, false
	}

	return SubsonicRef{LibraryID: libraryID, TrackID: trackID}, true
}
