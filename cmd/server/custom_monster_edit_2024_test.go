package main

import (
	"fmt"
	"strings"
	"testing"
)

// TestPostgresCompendiumOwnership: an import is its GM's alone; share, edit, hide and restore are the owner's; every route that names a
// compendium key (the list, combat/add, the four owner routes) asks the same visibility rule.
func TestPostgresCompendiumOwnership(t *testing.T) {
	testPostgres(t)
	isolateCustomMonsters(t)
	_, gm := pgAgent(t, "owner gm")
	_, other := pgAgent(t, "second gm")
	_, player := pgAgent(t, "owner player")
	mk := func(token string) int {
		c, j := pgCall(t, "POST", "/api/campaigns", token, map[string]interface{}{"name": "Ownership test", "max_players": 4})
		if c != 200 {
			t.Fatalf("campaign %d %v", c, j)
		}
		return int(j["campaign_id"].(float64))
	}
	camp, otherCamp := mk(gm), mk(other)
	for _, id := range []int{camp, otherCamp} {
		pgExec(t, `UPDATE lobbies SET status='active',schedule_state='off' WHERE id=$1`, id)
		pgFight(t, id)
	}
	path := "/api/gm/compendium/monsters"
	m := compendiumFixture()
	listed := func(token string, query string) []map[string]interface{} {
		c, j := pgCall(t, "GET", path+query, token, nil)
		if c != 200 {
			t.Fatalf("list %d %v", c, j)
		}
		out := []map[string]interface{}{}
		for _, e := range j["monsters"].([]interface{}) {
			out = append(out, e.(map[string]interface{}))
		}
		return out
	}
	importOne := func(token string) string {
		c, j := pgCall(t, "POST", path, token, map[string]interface{}{"source_note": "ownership test", "monsters": []srdMonster2024{m}})
		if c != 201 {
			t.Fatalf("import %d %v", c, j)
		}
		return j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	}
	add := func(token string, id int, key string) int {
		c, _ := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", id), token, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Warden " + key[len(key)-4:], "monster_key": key, "initiative": 1}}})
		return c
	}
	owner := func(token, route string, body map[string]interface{}) (int, map[string]interface{}) {
		return pgCall(t, "POST", path+"/"+route, token, body)
	}
	// each route reads only its own fields (an unknown one is refused)
	bodyFor := func(route, key string, shared bool) map[string]interface{} {
		switch route {
		case "edit":
			return map[string]interface{}{"monster_key": key, "monster": m}
		case "share":
			return map[string]interface{}{"monster_key": key, "shared": shared}
		}
		return map[string]interface{}{"monster_key": key}
	}

	key := importOne(gm)
	// private: listed and usable for its owner only
	if l := listed(gm, ""); len(l) != 1 || l[0]["mine"] != true || l[0]["shared"] != false {
		t.Fatalf("owner's list %v", l)
	}
	if l := listed(other, ""); len(l) != 0 {
		t.Fatalf("private import listed for another GM %v", l)
	}
	if c := add(other, otherCamp, key); c != 404 {
		t.Fatalf("another GM used a private import: %d", c)
	}
	if c := add(gm, camp, key); c != 200 {
		t.Fatalf("owner could not use it: %d", c)
	}
	// every owner route answers a private entry of someone else as an unknown key, and refuses a player and an anonymous caller
	for _, route := range []string{"edit", "hide", "restore", "share"} {
		body := bodyFor(route, key, true)
		if c, j := owner(other, route, body); c != 404 {
			t.Fatalf("%s: another GM touched a private import %d %v", route, c, j)
		}
		if c, _ := owner(player, route, body); c != 403 {
			t.Fatalf("%s: a player got %d", route, c)
		}
		if c, _ := owner("", route, body); c != 401 {
			t.Fatalf("%s: anonymous got %d", route, c)
		}
		if c, _ := pgCall(t, "GET", path+"/"+route, gm, nil); c != 405 {
			t.Fatalf("%s: GET got %d", route, c)
		}
	}
	if c, _ := owner(gm, "share", map[string]interface{}{"monster_key": key}); c != 400 {
		t.Fatal("share without shared accepted")
	}
	if c, _ := owner(gm, "hide", map[string]interface{}{"monster_key": key, "unexpected": 1}); c != 400 {
		t.Fatal("unknown field accepted")
	}
	if c, _ := owner(gm, "hide", map[string]interface{}{"monster_key": "goblin"}); c != 400 {
		t.Fatal("an SRD slug accepted as a compendium key")
	}

	// shared: the other GM lists and uses it but changes nothing
	if c, j := owner(gm, "share", map[string]interface{}{"monster_key": key, "shared": true}); c != 200 {
		t.Fatalf("share %d %v", c, j)
	}
	if l := listed(other, ""); len(l) != 1 || l[0]["mine"] != false || l[0]["shared"] != true {
		t.Fatalf("shared list for the other GM %v", l)
	}
	if l := listed(other, "?mine=1"); len(l) != 0 {
		t.Fatalf("mine=1 listed a shared entry of someone else %v", l)
	}
	if c := add(other, otherCamp, key); c != 200 {
		t.Fatalf("another GM could not use a shared import: %d", c)
	}
	for _, route := range []string{"edit", "hide", "restore", "share"} {
		if c, j := owner(other, route, bodyFor(route, key, false)); c != 403 || j["error"] != "not_your_monster" {
			t.Fatalf("%s: a GM changed another's monster %d %v", route, c, j)
		}
	}

	// an edit is a new version: the old one leaves the list, keeps working by its key, and sharing carries over
	edited := m
	edited.HP += 50
	c, j := owner(gm, "edit", map[string]interface{}{"monster_key": key, "monster": edited})
	if c != 201 {
		t.Fatalf("edit %d %v", c, j)
	}
	key2 := j["monster_key"].(string)
	if key2 == key || j["replaces"] != key || j["shared"] != true || !strings.HasPrefix(key2, "custom:") {
		t.Fatalf("edit reply %v", j)
	}
	if sb := monsterStatBlock(key); sb == nil || sb["hp"] != m.HP {
		t.Fatalf("the old version changed: %v", sb)
	}
	if sb := monsterStatBlock(key2); sb == nil || sb["hp"] != edited.HP {
		t.Fatalf("the new version: %v", sb)
	}
	l := listed(gm, "")
	if len(l) != 1 || l[0]["monster_key"] != key2 || l[0]["replaces"] != key {
		t.Fatalf("list after the edit %v", l)
	}
	if c := add(gm, camp, key); c != 200 {
		t.Fatalf("a hidden version stopped working by its key: %d", c)
	}
	// a corrected monster is validated as an import is
	bad := m
	bad.HP = 0
	if c, _ := owner(gm, "edit", map[string]interface{}{"monster_key": key2, "monster": bad}); c != 400 {
		t.Fatal("invalid edit accepted")
	}
	if c, _ := owner(gm, "edit", map[string]interface{}{"monster": m}); c != 400 {
		t.Fatal("edit without a key accepted")
	}

	// hide and restore
	if c, j := owner(gm, "hide", map[string]interface{}{"monster_key": key2}); c != 200 {
		t.Fatalf("hide %d %v", c, j)
	}
	if len(listed(gm, "")) != 0 || len(listed(other, "")) != 0 {
		t.Fatal("a hidden monster is still listed")
	}
	if c := add(gm, camp, key2); c != 200 {
		t.Fatalf("a hidden monster stopped working by its key: %d", c)
	}
	if c, j := owner(gm, "restore", map[string]interface{}{"monster_key": key2}); c != 200 {
		t.Fatalf("restore %d %v", c, j)
	}
	if len(listed(gm, "")) != 1 {
		t.Fatal("a restored monster is not listed")
	}
	// private again: gone for the other GM, kept by the fight that has it
	if c, _ := owner(gm, "share", map[string]interface{}{"monster_key": key2, "shared": false}); c != 200 {
		t.Fatal("unshare failed")
	}
	if len(listed(other, "")) != 0 || add(other, otherCamp, key2) != 404 {
		t.Fatal("an unshared monster is still reachable")
	}

	// an entry from before ownership has no owner: shared as it was, and nobody's to change
	pgExec(t, `INSERT INTO custom_monsters(key,definition,stats,source_note,owner_id,shared)
		SELECT 'custom:legacy-' || $1::text,definition,stats,source_note,NULL,true FROM custom_monsters WHERE key=$2`, camp, key2)
	legacy := "custom:legacy-" + fmt.Sprint(camp)
	if len(listed(other, "")) != 1 || add(other, otherCamp, legacy) != 200 {
		t.Fatal("an entry from before ownership stopped being shared")
	}
	if c, j := owner(gm, "hide", map[string]interface{}{"monster_key": legacy}); c != 403 || j["error"] != "not_your_monster" {
		t.Fatalf("an ownerless entry was changed %d %v", c, j)
	}
}
