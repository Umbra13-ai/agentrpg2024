package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestValidEmail(t *testing.T) {
	for _, ok := range []string{"agent@example.com", "player.two@example.com", " spaced@example.org "} {
		if _, good := validEmail(ok); !good {
			t.Errorf("%q should be accepted", ok)
		}
	}
	for _, bad := range []string{"", "no-at-sign", "Agent <agent@example.com>", "a@b.c, d@e.f", "x@" + string(make([]byte, 260))} {
		if _, good := validEmail(bad); good {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestRecoveryCode(t *testing.T) {
	re := regexp.MustCompile(`^[a-z]+-[a-z]+-[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}$`)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c := generateRecoveryCode()
		if !re.MatchString(c) {
			t.Fatalf("code %q has the wrong shape", c)
		}
		if seen[c] {
			t.Fatalf("code %q repeated in 200 tries", c)
		}
		seen[c] = true
	}
	if hashCode("ancient-blade-AB23-CD45") != hashCode(" ANCIENT-BLADE-ab23-cd45 ") {
		t.Error("codes compare without case and spaces")
	}
	if hashCode("ancient-blade-AB23-CD45") == hashCode("ancient-blade-AB23-CD46") {
		t.Error("different codes must hash differently")
	}
}

func TestAdminKey(t *testing.T) {
	t.Setenv("ADMIN_KEY", "secret-key")
	r := httptest.NewRequest("POST", "/api/admin/issue-token", nil)
	if adminKeyOK(r) {
		t.Error("no header: refused")
	}
	r.Header.Set("X-Admin-Key", "secret-kez")
	if adminKeyOK(r) {
		t.Error("wrong key: refused")
	}
	r.Header.Set("X-Admin-Key", "secret-key")
	if !adminKeyOK(r) {
		t.Error("right key: accepted")
	}
	t.Setenv("ADMIN_KEY", "")
	r.Header.Set("X-Admin-Key", "")
	if adminKeyOK(r) {
		t.Error("no ADMIN_KEY on the server: always refused")
	}
}

// The operator's new token for a player who lost theirs (the admin page's button and POST /api/admin/issue-token): the key or the
// page's session lets it through, the new token works, the old ones end (in the same step), and the history never holds the token.
func TestPostgresAdminIssuesToken(t *testing.T) {
	testPostgres(t)
	t.Setenv("ADMIN_KEY", "test-admin-key-for-issue-token")
	id, old := pgAgent(t, "pg lost token")
	call := func(headers map[string]string, cookie string, body interface{}) (int, map[string]interface{}) {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/api/admin/issue-token", bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: adminCookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		newServer("").Handler.ServeHTTP(w, r)
		out := map[string]interface{}{}
		json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code == 200 && w.Header().Get("Cache-Control") != "no-store" {
			t.Error("the reply with a token may be cached")
		}
		return w.Code, out
	}
	if c, _ := call(nil, "", map[string]interface{}{"agent_id": id}); c != 401 {
		t.Fatalf("no key: %d", c)
	}
	exp := fmt.Sprint(time.Now().Add(time.Hour).Unix())
	session := exp + "." + adminCookieSig(exp)
	if c, _ := call(nil, session, map[string]interface{}{"agent_id": id}); c != 401 {
		t.Errorf("the session without the page's header (another site's form) was let through: %d", c)
	}
	c, j := call(map[string]string{"X-Admin-Page": "1"}, session, map[string]interface{}{"agent_id": id})
	tok, _ := j["token"].(string)
	if c != 200 || tok == "" || j["revoked_tokens"] != float64(1) {
		t.Fatalf("the page's button: %d %v", c, j)
	}
	if c, _ := pgCall(t, "GET", "/api/my-turn", tok, nil); c == 401 {
		t.Error("the new token doesn't sign in")
	}
	if c, _ := pgCall(t, "GET", "/api/my-turn", old, nil); c != 401 {
		t.Errorf("the old token still signs in: %d", c)
	}
	var logged string
	db.QueryRow(`SELECT COALESCE(string_agg(COALESCE(old_value,'') || ' ' || COALESCE(new_value,'') || ' ' || COALESCE(note,''), ' | '), '') FROM admin_changes WHERE kind = 'account' AND row_id = $1`, id).Scan(&logged)
	if !strings.Contains(logged, "new token issued") || strings.Contains(logged, tok) {
		t.Errorf("the admin history: %q", logged)
	}
	// with the key, keeping the old tokens
	c, j = call(map[string]string{"X-Admin-Key": "test-admin-key-for-issue-token"}, "", map[string]interface{}{"agent_id": id, "revoke_old": false})
	if c != 200 || j["revoked_tokens"] != float64(0) {
		t.Fatalf("with the key: %d %v", c, j)
	}
	if c, _ := pgCall(t, "GET", "/api/my-turn", tok, nil); c == 401 {
		t.Error("revoke_old false ended the earlier token")
	}
}
