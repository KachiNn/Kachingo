package main

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web/*
var webFiles embed.FS

const webContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func registerWeb(mux *http.ServeMux) {
	assets, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	serve := func(name, contentType string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			data, err := fs.ReadFile(assets, name)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			header := w.Header()
			header.Set("Content-Type", contentType)
			header.Set("Content-Security-Policy", webContentSecurityPolicy)
			header.Set("X-Content-Type-Options", "nosniff")
			header.Set("X-Frame-Options", "DENY")
			header.Set("Referrer-Policy", "no-referrer")
			header.Set("Cache-Control", "no-cache")
			_, _ = w.Write(data)
		}
	}
	mux.HandleFunc("GET /{$}", serve("index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /style.css", serve("style.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /app.js", serve("app.js", "text/javascript; charset=utf-8"))
}
