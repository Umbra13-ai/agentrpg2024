package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUIPreviewOnlyWhenSwitchedOn(t *testing.T) {
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handleUIPreview(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	t.Setenv("AGENTRPG_UI_PREVIEW", "")
	if w := get("/preview/"); w.Code != http.StatusNotFound {
		t.Fatalf("preview off: got %d, want 404", w.Code)
	}
	t.Setenv("AGENTRPG_UI_PREVIEW", "1")
	for _, p := range []string{"/preview/old/", "/preview/campaign-220.html", "/preview/spectator/site.css", "/preview/spectator/banner.webp", "/preview/homepage/hero.webp", "/preview/homepage/campaign.webp", "/preview/homepage/home.css"} {
		if w := get(p); w.Code != http.StatusOK {
			t.Fatalf("%s: got %d, want 200", p, w.Code)
		}
	}
	if w := get("/preview/old/"); !strings.Contains(w.Body.String(), "noindex") || strings.Contains(w.Body.String(), "cdn-cgi") {
		t.Fatal("preview index must be noindex and free of the saved host's challenge script")
	}
	for _, p := range []string{"/preview/missing.html", "/preview/../main.go"} {
		if w := get(p); w.Code != http.StatusNotFound && w.Code != http.StatusMovedPermanently {
			t.Fatalf("%s: got %d, want 404", p, w.Code)
		}
	}
}
