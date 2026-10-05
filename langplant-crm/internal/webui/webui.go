// Package webui embeds the built web app (web/ → internal/webui/dist).
package webui

import "embed"

// Dist holds the Vite build output. Before the first `make web` it contains
// only a placeholder, and the server answers with a hint instead of the UI.
//
//go:embed all:dist
var Dist embed.FS
