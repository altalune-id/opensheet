// Package genassets embeds the buf-generated OpenAPI specs so the running
// binary can serve them without touching the filesystem. buf regenerates
// openapi/**/*.openapi.yaml — this file is hand-written and stays put.
package genassets

import "embed"

//go:embed all:openapi
var OpenAPI embed.FS
