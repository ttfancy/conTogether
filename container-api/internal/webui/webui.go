// Package webui embeds the built frontend (source at repo-root /web,
// built output copied here by `make frontend` or the Dockerfile — see
// package doc in dist/index.html for why it can't be embedded directly
// from /web: go:embed can only reach files inside the embedding Go
// file's own module subtree, and /web sits alongside container-api, not
// inside it) and serves it with SPA fallback: any path that isn't a
// real static file serves index.html instead of 404ing, so React
// Router's client-side routes (e.g. /containers/{id}/logs) work on a
// direct navigation or hard refresh, not just in-app link clicks.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dist
var distFS embed.FS

// Handler returns an http.Handler serving the embedded frontend build.
// Mount it as Gin's NoRoute handler (see cmd/server/main.go) — anything
// not matched by a registered API route falls through to this, and this
// itself falls through to index.html for anything not a real file.
func Handler() (http.Handler, error) {
	dist, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	fileServer := http.FileServer(http.FS(dist))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqPath := r.URL.Path
		if len(reqPath) > 0 && reqPath[0] == '/' {
			reqPath = reqPath[1:]
		}
		if reqPath == "" {
			reqPath = "."
		}

		if _, err := fs.Stat(dist, reqPath); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	}), nil
}
