package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The admin's Logs, Settings and an account's activity (admin_logs_2024.go): reports listed and changed with the change logged, failed requests listed by
// account and status, the settings without a secret, and a failed sign-in recorded by recordAuthFailure with the account it was aimed at.
func TestPostgresAdminLogsSettingsActivity(t *testing.T) {
	testPostgres(t)
	const key = "test-admin-key-for-logs"
	t.Setenv("ADMIN_KEY", key)
	admin := func(method, p string, body interface{}) (int, map[string]interface{}) {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, p, bytes.NewReader(b))
		r.RemoteAddr = "10.77.3.1:1234"
		r.Header.Set("X-Admin-Key", key)
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		newServer("").Handler.ServeHTTP(w, r)
		out := map[string]interface{}{}
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	_, player, camp, hero, _ := earlyRulesFixture(t, "Test Logs Ogre", nil)
	var playerID int
	db.QueryRow(`SELECT agent_id FROM characters WHERE id=$1`, hero).Scan(&playerID)

	// reports: a player's report is listed, found, resolved and reopened, each change in the log; a wrong status is refused
	title := fmt.Sprintf("Fireball ignores cover %d", camp)
	if c, j := pgCall(t, "POST", "/api/feature-requests", player, map[string]interface{}{"title": title, "body": "type: bug\n\nthe goblin took full damage", "campaign_id": camp}); c >= 300 {
		t.Fatalf("report: %d %v", c, j)
	}
	var rid int
	db.QueryRow(`SELECT id FROM feature_requests WHERE title=$1`, title).Scan(&rid)
	found := func(status string) bool {
		_, j := admin("GET", "/api/admin/reports?status="+status+"&q="+strings.ReplaceAll(title, " ", "%20"), nil)
		for _, x := range asList(j["reports"]) {
			if m, _ := x.(map[string]interface{}); int(m["id"].(float64)) == rid {
				return m["status"] == status || status == "all"
			}
		}
		return false
	}
	if !found("open") || found("resolved") {
		t.Fatal("a new report is open")
	}
	if c, _ := admin("POST", fmt.Sprintf("/api/admin/reports/%d", rid), map[string]interface{}{"status": "wontfix"}); c != 400 {
		t.Errorf("a wrong status: %d", c)
	}
	if c, j := admin("POST", fmt.Sprintf("/api/admin/reports/%d", rid), map[string]interface{}{"status": "resolved", "note": "fixed in 1.5"}); c != 200 {
		t.Fatalf("resolve: %d %v", c, j)
	}
	if !found("resolved") || found("open") {
		t.Error("the report didn't move to resolved")
	}
	if c, _ := admin("POST", "/api/admin/reports/99999999", map[string]interface{}{"status": "open"}); c != 404 {
		t.Errorf("an unknown report: %d", c)
	}
	if _, j := admin("GET", fmt.Sprintf("/api/admin/changes?kind=report&id=%d", rid), nil); !strings.Contains(fmt.Sprint(j["changes"]), "resolved") || !strings.Contains(fmt.Sprint(j["changes"]), title) {
		t.Errorf("the status change isn't in the log with the report's title: %v", j["changes"])
	}

	// failed sign-ins: a wrong token names no account; a wrong password names the one it was aimed at, with how
	req := func(auth string) {
		r := httptest.NewRequest("GET", "/api/characters", nil)
		r.RemoteAddr = "10.77.3.9:1234"
		r.Header.Set("Authorization", auth)
		newServer("").Handler.ServeHTTP(httptest.NewRecorder(), r)
	}
	var before int
	db.QueryRow(`SELECT COUNT(*) FROM sign_in_failures`).Scan(&before)
	req("Bearer arpg_this_is_not_a_token")
	req("Basic " + base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d:not-the-password", playerID))))
	var after, aimed int
	db.QueryRow(`SELECT COUNT(*) FROM sign_in_failures`).Scan(&after)
	db.QueryRow(`SELECT COUNT(*) FROM sign_in_failures WHERE agent_id=$1 AND how='password' AND ip='10.77.3.9'`, playerID).Scan(&aimed)
	if after-before != 2 || aimed != 1 {
		t.Errorf("failed sign-ins: %d new rows, %d aimed at the account", after-before, aimed)
	}
	var nullAgent int
	db.QueryRow(`SELECT COUNT(*) FROM sign_in_failures WHERE agent_id IS NULL AND how='token' AND ip='10.77.3.9'`).Scan(&nullAgent)
	if nullAgent < 1 {
		t.Error("a wrong token wasn't recorded")
	}

	// the account's activity: its requests, a failed one, the failed sign-in and its tokens
	pgCall(t, "GET", "/api/characters", player, nil)
	pgCall(t, "GET", "/api/characters/99999999", player, nil) // a failed request, kept under the account (request_errors)
	pgCall(t, "POST", "/api/action", player, map[string]interface{}{"action": "zzz_not_an_action"})
	c, a := admin("GET", fmt.Sprintf("/api/admin/accounts/%d/activity", playerID), nil)
	if c != 200 {
		t.Fatalf("activity: %d %v", c, a)
	}
	fs, _ := a["failed_sign_ins"].(map[string]interface{})
	if fs == nil || fs["last_24h"].(float64) < 1 || len(asList(fs["recent"])) < 1 {
		t.Errorf("the failed sign-in isn't in the account's activity: %v", a["failed_sign_ins"])
	}
	if a["failed_requests_24h"].(float64) < 2 || len(asList(a["failing_routes"])) < 1 || !strings.Contains(fmt.Sprint(a["failing_routes"]), "unknown_action") {
		t.Errorf("the account's failed requests and the code of the reply: %v %v", a["failed_requests_24h"], a["failing_routes"])
	}
	if a["last_active"] == "" || a["first_seen"] == "" {
		t.Errorf("last active / first seen: %v / %v", a["last_active"], a["first_seen"])
	}
	if c, _ := admin("GET", "/api/admin/accounts/99999999/activity", nil); c != 404 {
		t.Errorf("an unknown account's activity: %d", c)
	}

	// errors: only failed requests, filtered by account and by status
	_, e := admin("GET", fmt.Sprintf("/api/admin/errors?account_id=%d", playerID), nil)
	for _, x := range asList(e["errors"]) {
		m, _ := x.(map[string]interface{})
		if m["status"].(float64) < 400 || int(m["account_id"].(float64)) != playerID {
			t.Errorf("a row that isn't a failed request of the account: %v", m)
		}
	}
	if len(asList(e["errors"])) == 0 {
		t.Error("the account's failed request isn't listed (request_errors)")
	}
	if _, e5 := admin("GET", fmt.Sprintf("/api/admin/errors?account_id=%d&min_status=500", playerID), nil); len(asList(e5["errors"])) != 0 {
		t.Errorf("a 400 or 404 listed as a server error: %v", e5["errors"])
	}
	if _, e6 := admin("GET", fmt.Sprintf("/api/admin/errors?account_id=%d&q=unknown_action", playerID), nil); len(asList(e6["errors"])) < 1 {
		t.Errorf("not found by the reply's error code: %v", e6["errors"])
	}
	// a sign-in failure (401) is not a request error, and a successful request is none
	var n401 int
	db.QueryRow(`SELECT COUNT(*) FROM request_errors WHERE status = 401 OR status < 400`).Scan(&n401)
	if n401 != 0 {
		t.Errorf("%d rows that are not failed requests", n401)
	}

	// settings: no secret, the counts and the database answer
	c, s := admin("GET", "/api/admin/settings", nil)
	text := fmt.Sprint(s)
	if c != 200 || fmt.Sprint(s["database"].(map[string]interface{})["ok"]) != "true" {
		t.Fatalf("settings: %d %v", c, s)
	}
	for _, secret := range []string{key, "AGENTMAIL_API_KEY", "DATABASE_URL", "postgres://"} {
		if strings.Contains(text, secret) {
			t.Errorf("the settings show %q", secret)
		}
	}
	if s["counts"].(map[string]interface{})["accounts"].(float64) < 2 {
		t.Errorf("counts: %v", s["counts"])
	}
	for _, p := range []string{"/api/admin/settings", "/api/admin/errors", "/api/admin/reports"} {
		r := httptest.NewRequest(http.MethodGet, p, nil)
		r.RemoteAddr = "10.77.3.2:1234"
		w := httptest.NewRecorder()
		newServer("").Handler.ServeHTTP(w, r)
		if w.Code == 200 {
			t.Errorf("%s served without the admin key", p)
		}
	}
}
