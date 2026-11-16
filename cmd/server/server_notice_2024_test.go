package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// The operator's notice (feedback log 42): the operator sets it, every agent reads it without a token (GET /api/notice, GET /api/,
// GET /api/version, /llms.txt, the home page), in a 401 reply to a token a reset made invalid and in the heartbeat; a new notice
// replaces the old; DELETE clears it everywhere. Only the operator sets or clears it.
func TestPostgresServerNotice(t *testing.T) {
	testPostgres(t)
	t.Setenv("ADMIN_KEY", "test-admin-key-for-notice")
	send := func(method, path, key string, body interface{}) (int, string) {
		var rd io.Reader = bytes.NewReader(nil)
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		r := httptest.NewRequest(method, path, rd)
		if key != "" {
			r.Header.Set("X-Admin-Key", key)
		}
		if strings.HasPrefix(key, "Bearer ") {
			r.Header.Del("X-Admin-Key")
			r.Header.Set("Authorization", key)
		}
		w := httptest.NewRecorder()
		newServer("").Handler.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	if c, _ := send("POST", "/api/admin/notice", "", map[string]interface{}{"notice": "x"}); c != 401 {
		t.Fatalf("a notice without the operator's key: %d", c)
	}
	first, notice := "Old notice.", "The test server was reset at 19:20 UTC: register again; the campaign continues as 220."
	for _, n := range []string{first, notice} {
		if c, b := send("POST", "/api/admin/notice", "test-admin-key-for-notice", map[string]interface{}{"notice": n}); c != 200 {
			t.Fatalf("set: %d %s", c, b)
		}
	}
	_, tok := pgAgent(t, "pg notice reader")
	for _, x := range []struct{ path, key string }{
		{"/api/notice", ""}, {"/api/", ""}, {"/api/version", ""}, {"/llms.txt", ""}, {"/", ""},
		{"/api/heartbeat", "Bearer arpg_not_a_token"}, {"/api/heartbeat", "Bearer " + tok},
	} {
		c, b := send("GET", x.path, x.key, nil)
		if !strings.Contains(b, "register again; the campaign continues as 220") || strings.Contains(b, first) {
			t.Errorf("GET %s (%d) doesn't carry the notice: %.300s", x.path, c, b)
		}
	}
	if c, b := send("DELETE", "/api/admin/notice", "test-admin-key-for-notice", nil); c != 200 {
		t.Fatalf("clear: %d %s", c, b)
	}
	if _, b := send("GET", "/api/version", "", nil); strings.Contains(b, "server_notice") {
		t.Errorf("a cleared notice still shows: %s", b)
	}
	if _, b := send("GET", "/api/notice", "", nil); !strings.Contains(b, `"server_notice":null`) {
		t.Errorf("GET /api/notice after clearing: %s", b)
	}
}
