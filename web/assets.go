package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var embeddedStatic embed.FS

// StaticFS exposes the embedded static assets rooted at the static directory,
// suitable for serving under /static/ via http.StripPrefix("/static/", ...).
var StaticFS fs.FS = mustSub(embeddedStatic, "static")

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
