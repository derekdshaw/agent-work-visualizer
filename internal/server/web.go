package server

import (
	"embed"
	"io/fs"
	"net/http"
)

// webFiles holds the Vite build of web/ in web/dist. The all: prefix keeps
// bundle files whose names start with "_".
//
//go:embed all:web
var webFiles embed.FS

// builtWeb returns the files of the Vite build.
func builtWeb() fs.FS {
	dist, err := fs.Sub(webFiles, "web/dist")
	if err != nil {
		// fs.Sub fails only for an invalid path, and this one is constant.
		panic(err)
	}
	return dist
}

const unbuiltPage = `<!doctype html><meta charset="utf-8"><title>Agent Work Visualizer</title>
<body style="background:#04060b;color:#e6f1ff;font:14px/1.6 ui-monospace,monospace;padding:40px">
<p>The web UI is not built into this binary.</p>
<p>Run <code>npm --prefix web ci &amp;&amp; npm --prefix web run build</code>, then <code>go build</code> again.
For development, run <code>npm --prefix web run dev</code> beside this server.</p>`

// webHandler serves the page and its assets from files. Without an
// index.html, it answers every request with a page that explains the build.
func webHandler(files fs.FS) http.Handler {
	if _, err := fs.Stat(files, "index.html"); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(unbuiltPage))
		})
	}
	server := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Vite names assets by content hash, so only the page must not be cached.
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Cache-Control", "no-store")
		}
		server.ServeHTTP(w, r)
	})
}
