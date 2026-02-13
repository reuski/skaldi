// Package web embeds the frontend UI assets.
package web

import (
	_ "embed"
)

//go:embed index.html
var IndexHTML []byte
