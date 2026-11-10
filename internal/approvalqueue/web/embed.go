// Package web embeds the page: one HTML file, one script, one stylesheet, nothing to build.
package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html app.js app.css
var files embed.FS

// FS is the embedded page.
func FS() fs.FS { return files }
