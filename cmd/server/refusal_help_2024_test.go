package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func serveRefusal(status int, contentType, body string) (*httptest.ResponseRecorder, map[string]interface{}) {
	rec := httptest.NewRecorder()
	h := &refusalHelpWriter{ResponseWriter: rec}
	h.Header().Set("Content-Type", contentType)
	h.WriteHeader(status)
	h.Write([]byte(body))
	h.finish()
	var m map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &m)
	return rec, m
}

// Plan v2.1, S5: a refusal with a known code says what failed and the next safe step, claims "nothing changed" only where its row does,
// and leaves its code and every other field as the handler wrote them.
func TestRefusalHelpIsAddedOnceInTheSharedLayer(t *testing.T) {
	rec, m := serveRefusal(http.StatusBadRequest, "application/json", `{"error":"unknown_action","message":"no such action","actions":["a"]}`)
	if rec.Code != 400 || m["error"] != "unknown_action" || m["message"] != "no such action" || m["actions"] == nil {
		t.Fatalf("the handler's own fields changed: %d %v", rec.Code, m)
	}
	if m["what_failed"] == nil || m["next_step"] == nil || m["nothing_changed"] != true {
		t.Errorf("no help on a known refusal: %v", m)
	}
	// a code the server cannot promise nothing changed under: the help says what to do, never "nothing changed"
	_, m = serveRefusal(http.StatusConflict, "application/json", `{"error":"resource_exhausted","message":"no slot"}`)
	if m["next_step"] == nil || m["nothing_changed"] != nil {
		t.Errorf("resource_exhausted: %v", m)
	}
	// an unknown code, a handler's own next_step, a 2xx, a 401, and a reply that isn't JSON are left alone
	if _, m = serveRefusal(400, "application/json", `{"error":"some_new_code"}`); m["next_step"] != nil {
		t.Errorf("a code with no row got help: %v", m)
	}
	if _, m = serveRefusal(400, "application/json", `{"error":"unknown_action","next_step":"mine"}`); m["next_step"] != "mine" || m["what_failed"] != nil {
		t.Errorf("a handler's own next_step was replaced: %v", m)
	}
	if rec, m = serveRefusal(200, "application/json", `{"error":"unknown_action"}`); rec.Code != 200 || m["next_step"] != nil {
		t.Errorf("a 200 reply was changed: %d %v", rec.Code, m)
	}
	if rec, m = serveRefusal(401, "application/json", `{"error":"unknown_action"}`); rec.Code != 401 || m["next_step"] != nil {
		t.Errorf("a 401 reply was changed: %d %v", rec.Code, m)
	}
	if rec, _ = serveRefusal(400, "text/plain", "unknown_action"); rec.Body.String() != "unknown_action" {
		t.Errorf("a text reply was changed: %q", rec.Body.String())
	}
}

// Every code in the table is an error code the server writes: a row for a code nothing writes would be advice for a refusal that never comes.
func TestEveryRefusalHelpCodeIsWrittenByTheServer(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	var src strings.Builder
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "refusal_help_2024.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		src.Write(b)
	}
	all := src.String()
	for code := range refusalHelp {
		if !strings.Contains(all, `"`+code+`"`) {
			t.Errorf("refusalHelp has a row for %q, which no handler writes", code)
		}
	}
}
