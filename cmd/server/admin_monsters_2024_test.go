package main

// The admin page's Monsters tab (admin_monsters_2024.go): every GM-built monster from every GM, private and hidden ones included, one
// monster's facts and versions, its stat block page (framed by the admin page only), and its token by the GM route's rule, logged.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPostgresAdminMonsters(t *testing.T) {
	testPostgres(t)
	t.Setenv("ADMIN_KEY", "test-admin-key-for-monsters")
	gm, _, camp, _, _ := earlyRulesFixture(t, "Test Admin Host", []srdMonsterEntry{{Name: "Tap", Text: "Melee Attack Roll: +4, reach 5 ft. Hit: 1 Bludgeoning damage."}})
	m := inventedMonster("Test Bog Lantern", "Fey", nil, []srdMonsterEntry{{Name: "Flicker", Text: "Melee Attack Roll: +3, reach 5 ft. Hit: 2 (1d4) Radiant damage."}})
	_, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"campaign_id": camp, "source_note": "invented admin fixture", "monsters": []srdMonster2024{m}})
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	m.HP += 3
	_, j = pgCall(t, "POST", "/api/gm/compendium/monsters/edit", gm, map[string]interface{}{"campaign_id": camp, "monster_key": key, "monster": m})
	newKey := fmt.Sprint(j["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key IN ($1, $2)`, key, newKey) })

	admin := func(method, p string, body interface{}, signedIn bool) (int, []byte, map[string]string) {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, p, bytes.NewReader(b))
		r.RemoteAddr = "10.77.2.1:1234"
		r.Header.Set("Content-Type", "application/json")
		if signedIn {
			r.Header.Set("X-Admin-Key", "test-admin-key-for-monsters")
		}
		w := httptest.NewRecorder()
		newServer("").Handler.ServeHTTP(w, r)
		return w.Code, w.Body.Bytes(), map[string]string{"frame": w.Header().Get("X-Frame-Options"), "type": w.Header().Get("Content-Type")}
	}
	asJSON := func(b []byte) map[string]interface{} {
		out := map[string]interface{}{}
		json.Unmarshal(b, &out)
		return out
	}
	if code, _, _ := admin("GET", "/api/admin/monsters", nil, false); code != 401 {
		t.Errorf("signed out: %d", code)
	}
	// the list: the current version by default, the replaced one under only
	keysOf := func(b []byte) string {
		var ks []string
		for _, x := range asJSON(b)["monsters"].([]interface{}) {
			ks = append(ks, fmt.Sprint(x.(map[string]interface{})["key"]))
		}
		return strings.Join(ks, ",")
	}
	_, b, _ := admin("GET", "/api/admin/monsters?q=Bog+Lantern", nil, true)
	if keysOf(b) != newKey || !strings.Contains(string(b), `"shared":false`) || !strings.Contains(string(b), `"replaces":"`+key+`"`) {
		t.Errorf("the list: %s", b)
	}
	if _, b, _ = admin("GET", "/api/admin/monsters?q=Bog+Lantern&deleted=only", nil, true); keysOf(b) != key {
		t.Errorf("the hidden list: %s", b)
	}
	if _, b, _ = admin("GET", "/api/admin/monsters?q=Bog+Lantern&deleted=include", nil, true); keysOf(b) != newKey+","+key {
		t.Errorf("every version, newest first: %s", b)
	}
	// one monster, its versions and its page
	esc := url.PathEscape(newKey)
	_, b, _ = admin("GET", "/api/admin/monsters/"+esc, nil, true)
	v := asJSON(b)
	if !strings.Contains(fmt.Sprint(v["versions"]), key) || !strings.Contains(fmt.Sprint(v["token_rule"]), "square") || v["page"] != "/api/admin/monsters/"+esc+"/page" {
		t.Errorf("one monster: %s", b)
	}
	code, page, h := admin("GET", "/api/admin/monsters/"+esc+"/page", nil, true)
	if code != 200 || !strings.Contains(string(page), "Test Bog Lantern") || !strings.Contains(string(page), "abilities-3x2") || h["frame"] != "SAMEORIGIN" || strings.Contains(string(page), `<header class="topbar"`) {
		t.Errorf("the page: %d %v", code, h)
	}
	if code, _, _ := admin("GET", "/api/admin/monsters/custom:nothing", nil, true); code != 404 {
		t.Errorf("an unknown key: %d", code)
	}
	// the token: the rule, the write, the log
	img := base64.StdEncoding.EncodeToString(pngOf(t, 300, 200))
	if code, b, _ := admin("POST", "/api/admin/monsters/"+esc+"/token", map[string]interface{}{"image_base64": img}, true); code != 400 || asJSON(b)["error"] != "not_square" {
		t.Errorf("a 300x200 token: %d %s", code, b)
	}
	img = base64.StdEncoding.EncodeToString(pngOf(t, 256, 256))
	if code, b, _ := admin("POST", "/api/admin/monsters/"+esc+"/token", map[string]interface{}{"image_base64": img, "note": "a token for the GM"}, true); code != 200 || monsterTokenURL(newKey) == "" {
		t.Errorf("a square token: %d %s", code, b)
	}
	var logged int
	db.QueryRow(`SELECT COUNT(*) FROM admin_changes WHERE kind = 'monster' AND field LIKE $1 AND new_value = 'a token' AND note = 'a token for the GM'`, "%"+newKey+"%").Scan(&logged)
	if logged != 1 {
		t.Errorf("the change log: %d rows", logged)
	}
	if code, _, _ := admin("DELETE", "/api/admin/monsters/"+esc+"/token", nil, true); code != 200 || monsterTokenURL(newKey) != "" {
		t.Errorf("remove: %d", code)
	}
}

// The admin page's own size check says the rule's size (the page's script is a string, so the test holds it to monsterTokenRule).
func TestAdminPageTokenLimitIsTheRules(t *testing.T) {
	want := fmt.Sprintf("file.size>%d*1024*1024", monsterTokenRule.MaxBytes/(1024*1024))
	if !strings.Contains(adminPageHTML, want) {
		t.Errorf("the admin page doesn't check %q", want)
	}
}
