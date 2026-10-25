// Package web holds the dashboard's static files.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

// FS serves the dashboard from its root.
var FS, _ = fs.Sub(files, "static")
