// SPDX-License-Identifier: AGPL-3.0-or-later

// Package web embeds the frontend UI assets.
package web

import (
	_ "embed"
)

//go:embed index.html
var IndexHTML []byte
