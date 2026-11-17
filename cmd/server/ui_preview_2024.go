package main

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"strings"
)

// The GUI rework preview (Matt, Nov 17): a frozen copy of the ChatGPT-made spectator design, saved with its campaign
// pages as they were. It is served at /preview/ only on a server whose AGENTRPG_UI_PREVIEW is "1" (staging); everywhere
// else /preview/ answers 404. Its data never changes and it reads nothing from the database.
//
//go:embed data/ui_preview
var uiPreviewFiles embed.FS

func uiPreviewOn() bool { return os.Getenv("AGENTRPG_UI_PREVIEW") == "1" }

// handleUIPreview serves the saved preview site under /preview/ (staging only).
func handleUIPreview(w http.ResponseWriter, r *http.Request) {
	if !uiPreviewOn() || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/preview" {
		http.Redirect(w, r, "/preview/", http.StatusMovedPermanently)
		return
	}
	sub, err := fs.Sub(uiPreviewFiles, "data/ui_preview")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/preview/")
	if name == "" {
		name = "index.html"
	}
	if _, err := fs.Stat(sub, name); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, sub, name)
}
