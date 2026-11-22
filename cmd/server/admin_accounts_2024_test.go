package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The admin's accounts (admin_accounts_2024.go): listed and read without their secrets, edited under the editor's rules (a name or email checked, locked columns
// refused, every change in the admin log), deleted by hiding (no sign-in, its characters hidden with it, refused while it runs a campaign) and restored.
func TestPostgresAdminAccounts(t *testing.T) {
	testPostgres(t)
	const key = "test-admin-key-for-accounts"
	t.Setenv("ADMIN_KEY", key)
	admin := func(method, p string, body interface{}) (int, map[string]interface{}) {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, p, bytes.NewReader(b))
		r.RemoteAddr = "10.77.2.1:1234"
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
	gm, player, camp, hero, _ := earlyRulesFixture(t, "Test Account Ogre", nil)
	_ = gm
	var gmID, playerID int
	db.QueryRow(`SELECT dm_id FROM lobbies WHERE id=$1`, camp).Scan(&gmID)
	db.QueryRow(`SELECT agent_id FROM characters WHERE id=$1`, hero).Scan(&playerID)
	if gmID == 0 || playerID == 0 {
		t.Fatalf("fixture: gm %d player %d", gmID, playerID)
	}
	acct := func(id int) string { return fmt.Sprintf("/api/admin/accounts/%d", id) }
	listed := func(q string, id int) bool { // is the account in the list (the search is a substring: others may match too)
		_, j := admin("GET", "/api/admin/accounts?"+q, nil)
		for _, a := range asList(j["accounts"]) {
			if am, _ := a.(map[string]interface{}); int(am["id"].(float64)) == id {
				return true
			}
		}
		return false
	}
	signedIn := func() int { c, _ := pgCall(t, "GET", "/api/characters", player, nil); return c }

	// the list and the row: found by name, id and email; no secrets
	var playerName string
	db.QueryRow(`SELECT name FROM agents WHERE id=$1`, playerID).Scan(&playerName)
	if !listed("q="+fmt.Sprint(playerID), playerID) || !listed("q="+url.QueryEscape(strings.ToUpper(playerName)), playerID) {
		t.Fatal("the account isn't found by its id and by its name")
	}
	c, j := admin("GET", acct(playerID), nil)
	row, _ := j["account"].(map[string]interface{})
	if c != 200 || row == nil {
		t.Fatalf("row: %d %v", c, j)
	}
	for _, secret := range []string{"password_hash", "salt", "verification_code"} {
		if _, has := row[secret]; has {
			t.Errorf("the account's row shows %s", secret)
		}
	}
	if len(asList(j["characters"])) != 1 {
		t.Errorf("the account's characters: %v", j["characters"])
	}
	if c, j := admin("GET", acct(gmID), nil); c != 200 || len(asList(j["campaigns"])) != 1 {
		t.Errorf("the GM's campaigns: %d %v", c, j["campaigns"])
	}

	// editing: a name and an email are checked, the locked columns refused, a good change is saved and logged
	for _, bad := range []map[string]interface{}{
		{"name": ""}, {"name": strings.Repeat("x", 65)}, {"name": "line\nbreak"}, {"email": "not an email"}, {"password_hash": "x"}, {"salt": "x"},
		{"verification_code": "x"}, {"deleted_at": "2020-01-01"}, {"last_seen": "2020-01-01"}, {"nonsense": 1},
	} {
		if c, j := admin("POST", acct(playerID), map[string]interface{}{"set": bad}); c != 400 {
			t.Errorf("set %v was not refused: %d %v", bad, c, j)
		}
	}
	var gmName string
	db.QueryRow(`SELECT name FROM agents WHERE id=$1`, gmID).Scan(&gmName)
	if c, j := admin("POST", acct(playerID), map[string]interface{}{"set": map[string]interface{}{"name": gmName}}); c != 400 {
		t.Errorf("another account's name was accepted: %d %v", c, j)
	}
	newName := fmt.Sprintf("Renamed Player %d", playerID)
	if c, j := admin("POST", acct(playerID), map[string]interface{}{"set": map[string]interface{}{"name": newName, "is_moderator": true}, "note": "test"}); c != 200 {
		t.Fatalf("edit: %d %v", c, j)
	}
	var n string
	var mod bool
	db.QueryRow(`SELECT name, COALESCE(is_moderator,false) FROM agents WHERE id=$1`, playerID).Scan(&n, &mod)
	if n != newName || !mod {
		t.Errorf("the edit wasn't stored: %q %v", n, mod)
	}
	if _, j := admin("GET", "/api/admin/changes?kind=account&id="+fmt.Sprint(playerID), nil); len(asList(j["changes"])) < 2 {
		t.Errorf("the edit isn't in the admin log: %v", j)
	}
	admin("POST", acct(playerID), map[string]interface{}{"set": map[string]interface{}{"is_moderator": false}})

	// delete: refused for the GM of a campaign, by hiding for the player
	if c, j := admin("POST", acct(gmID)+"/delete", nil); c != 400 || !strings.Contains(fmt.Sprint(j["message"]), "GM of") {
		t.Errorf("deleting a GM: %d %v", c, j)
	}
	if signedIn() != 200 {
		t.Fatal("the player can't sign in before the delete")
	}
	if c, j := admin("POST", acct(playerID)+"/delete", nil); c != 200 {
		t.Fatalf("delete: %d %v", c, j)
	}
	if c, j := pgCall(t, "GET", "/api/characters", player, nil); c != 401 || !strings.Contains(fmt.Sprint(j), "account_deleted") {
		t.Errorf("a deleted account's token: %d %v", c, j)
	}
	var charDeleted bool
	db.QueryRow(`SELECT deleted_at IS NOT NULL FROM characters WHERE id=$1`, hero).Scan(&charDeleted)
	if !charDeleted {
		t.Error("its character wasn't hidden with it")
	}
	if listed("q="+fmt.Sprint(playerID), playerID) {
		t.Error("a deleted account is in the default list")
	}
	if !listed("deleted=only&q="+fmt.Sprint(playerID), playerID) {
		t.Error("a deleted account isn't in the deleted list")
	}
	if c, _ := admin("POST", acct(playerID)+"/delete", nil); c != 400 {
		t.Errorf("deleting twice: %d", c)
	}
	if c, j := admin("POST", fmt.Sprintf("/api/admin/characters/%d", hero), map[string]interface{}{"set": map[string]interface{}{"agent_id": playerID}}); c != 400 {
		t.Errorf("a character was given to a deleted account: %d %v", c, j)
	}
	if _, j := admin("GET", "/api/admin/changes?kind=account&id="+fmt.Sprint(playerID), nil); !strings.Contains(fmt.Sprint(j["changes"]), "deleted") {
		t.Errorf("the delete isn't in the admin log: %v", j)
	}

	// restore: the sign-in and the character come back, in its campaign
	if c, j := admin("POST", acct(playerID)+"/restore", nil); c != 200 {
		t.Fatalf("restore: %d %v", c, j)
	}
	if signedIn() != 200 {
		t.Error("the restored account can't sign in")
	}
	var owner, lobby int
	db.QueryRow(`SELECT COALESCE(agent_id,0), COALESCE(lobby_id,0) FROM characters WHERE id=$1`, hero).Scan(&owner, &lobby)
	if owner != playerID || lobby != camp {
		t.Errorf("the character after the restore: owner %d (want %d), campaign %d (want %d)", owner, playerID, lobby, camp)
	}
	if c, _ := admin("POST", acct(playerID)+"/restore", nil); c != 400 {
		t.Errorf("restoring a live account: %d", c)
	}
	// without the key nothing is served
	r := httptest.NewRequest("GET", "/api/admin/accounts", nil)
	r.RemoteAddr = "10.77.2.2:1234"
	w := httptest.NewRecorder()
	newServer("").Handler.ServeHTTP(w, r)
	if w.Code == 200 {
		t.Errorf("accounts listed without the admin key: %d", w.Code)
	}
}
