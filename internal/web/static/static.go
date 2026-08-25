// Package static embeds the compiled web cockpit SPA build output (dist/) so
// the loopback server can serve the frontend offline without a filesystem
// dependency. The embed directive lives here (package static), not in the
// server package, per the architect decision: internal/web/server accepts an
// fs.FS so tests can inject synthetic filesystems.
package static

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var FS embed.FS

// DistFS is FS rooted at the dist/ directory, ready to be served at "/".
// It panics at init if the embed is malformed (dist/ absent), which can only
// happen when the SPA build output has been deleted.
var DistFS fs.FS = sub(FS, "dist")

func sub(fsys fs.FS, dir string) fs.FS {
	s, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("static: embed dist/ missing: " + err.Error())
	}
	return s
}
