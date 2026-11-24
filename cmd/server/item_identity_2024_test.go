package main

// An unidentified magic item (item_identity_2024.go): the GM gives it with what it looks like; every place a player reads an item names it
// by that look and tells nothing of what it is; the GM reads it as it is; the rules read it as it is.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestPlayerItemViewHidesWhatItIs(t *testing.T) {
	it := invItem{"id": "g1", "name": "Wand of Magic Missiles", "quantity": float64(1), "location": "backpack", "weight": float64(1),
		"magic_item": "Wand of Magic Missiles", "charges": float64(7), "casts": []string{"Magic Missile (1 charge)"},
		unidentifiedKey: true, appearsAsKey: "a crooked ash wand"}
	v := playerItemView(it)
	b, _ := json.Marshal(v)
	if v["name"] != "a crooked ash wand" || v["id"] != "g1" || v["location"] != "backpack" || strings.Contains(strings.ToLower(string(b)), "missile") || v[appearsAsKey] != nil {
		t.Errorf("the player's view: %s", b)
	}
	if it["name"] != "Wand of Magic Missiles" {
		t.Error("the view changed the item itself")
	}
	known := invItem{"name": "Rope", "quantity": float64(1)}
	if playerItemView(known)["name"] != "Rope" || playerItemName(known) != "Rope" {
		t.Error("an identified item is read as it is")
	}
	if shownNameIn([]invItem{it}, "wand of magic missiles") != "a crooked ash wand" || shownNameIn([]invItem{it}, "Rope") != "Rope" {
		t.Error("shownNameIn")
	}
}

func TestPostgresUnidentifiedItemEverywhereAPlayerReads(t *testing.T) {
	testPostgres(t)
	gm, player, camp, hero, _ := earlyRulesFixture(t, "Test Identity Raider", nil)
	give := func(body map[string]interface{}) (int, map[string]interface{}) {
		body["character_id"], body["campaign_id"] = hero, camp
		return pgCall(t, "POST", "/api/gm/give-item", gm, body)
	}
	if c, r := give(map[string]interface{}{"item_name": "Ring of Protection", "unidentified": true}); c != 400 || r["error"] != "appears_as_required" {
		t.Errorf("unidentified without appears_as: %d %v", c, r)
	}
	if c, r := give(map[string]interface{}{"item_name": "Ring of Protection", "appears_as": "a ring"}); c != 400 || r["error"] != "appears_as_without_unidentified" {
		t.Errorf("appears_as alone: %d %v", c, r)
	}
	if c, r := give(map[string]interface{}{"item_name": "Ring of Protection", "unidentified": true, "appears_as": "a plain silver ring"}); c != 200 || r["appears_as"] != "a plain silver ring" {
		t.Fatalf("give: %d %v", c, r)
	}
	give(map[string]interface{}{"item_name": "Longsword +1", "unidentified": true, "appears_as": "a longsword with a sea-green hilt"})
	give(map[string]interface{}{"item_name": "Potion of Healing", "unidentified": true, "appears_as": "a vial of cloudy red liquid"})
	pgExec(t, `UPDATE characters SET equipped_main_hand = 'Longsword +1', hp = 1 WHERE id = $1`, hero)

	secrets := []string{"protection", "+1", "potion of healing"}
	leaks := func(where string, v interface{}) {
		t.Helper()
		b, _ := json.Marshal(v)
		low := strings.ToLower(string(b))
		for _, s := range secrets {
			if strings.Contains(low, s) {
				t.Errorf("%s tells what an unidentified item is (%q): %s", where, s, b)
			}
		}
	}
	// the sheet: the player and a spectator read the look; the GM reads the item
	_, sheet := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d", hero), player, nil)
	inv := fmt.Sprint(sheet["inventory"])
	if !strings.Contains(inv, "a plain silver ring") || !strings.Contains(inv, "a vial of cloudy red liquid") {
		t.Errorf("the player's sheet inventory: %s", inv)
	}
	var unknownCount int
	for _, x := range sheet["inventory"].([]interface{}) {
		m := x.(map[string]interface{})
		if m["unidentified"] == true {
			unknownCount++
			leaks("the sheet's unidentified item", m)
		}
	}
	if unknownCount != 3 {
		t.Errorf("unidentified items on the sheet: %d", unknownCount)
	}
	if eq := fmt.Sprint(sheet["equipment"]); !strings.Contains(eq, "sea-green hilt") || strings.Contains(eq, "+1") {
		t.Errorf("the sheet's main hand: %s", eq)
	}
	_, gmSheet := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d", hero), gm, nil)
	if inv := fmt.Sprint(gmSheet["inventory"]); !strings.Contains(inv, "Longsword +1") || !strings.Contains(inv, "a plain silver ring") {
		t.Errorf("the GM reads the items as they are, with their look: %s", inv)
	}
	give(map[string]interface{}{"item_name": "Ring of Protection"}) // a known one doesn't stack with the unknown one
	known := 0
	for _, it := range inventoryOf(hero) {
		if strings.EqualFold(itemStr(it, "name"), "Ring of Protection") {
			known++
		}
	}
	if known != 2 {
		t.Errorf("the known ring stacked with the unknown one: %d rings", known)
	}
	pgExec(t, `UPDATE characters SET inventory = (SELECT jsonb_agg(x) FROM jsonb_array_elements(inventory) x WHERE NOT (x->>'name' = 'Ring of Protection' AND x->'unidentified' IS NULL)) WHERE id = $1`, hero)
	// the equipment page, my-turn (full and short), the weight list, the log
	_, layout := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/equipment", hero), player, nil)
	leaks("the equipment layout", layout)
	_, turn := pgCall(t, "GET", fmt.Sprintf("/api/my-turn?character_id=%d", hero), player, nil)
	if c, _ := turn["character"].(map[string]interface{}); c == nil || !strings.Contains(fmt.Sprint(c["inventory"]), "a plain silver ring") {
		t.Errorf("my-turn's carried list: %v", turn["character"])
	}
	leaks("my-turn", map[string]interface{}{"character": turn["character"], "reminders": turn["reminders"], "gear_reminders": turn["gear_reminders"], "turn_left": turn["turn_left"]}) // "now"'s examples are the action table's own
	_, short := pgCall(t, "GET", fmt.Sprintf("/api/my-turn?character_id=%d&view=short", hero), player, nil)
	leaks("my-turn's short view", short["you"])
	_, enc := pgCall(t, "GET", fmt.Sprintf("/api/characters/encumbrance?character_id=%d", hero), player, nil)
	leaks("the weight list", enc["item_weights"])
	var given string
	db.QueryRow(`SELECT string_agg(description, ' | ') FROM actions WHERE lobby_id = $1 AND action_type = 'item_given'`, camp).Scan(&given)
	if !strings.Contains(given, "a plain silver ring") || strings.Contains(given, "Longsword +1") {
		t.Errorf("the log of the gifts: %s", given)
	}
	// a shop can't price it; the ground names it by its look and keeps it whole
	var ringID string
	for _, it := range inventoryOf(hero) {
		if itemUnidentified(it) && strings.HasPrefix(itemStr(it, "name"), "Ring of") {
			ringID = itemStr(it, "id")
			if why := sellBlocked(db, hero, inventoryOf(hero), it, 1); !strings.Contains(why, "unidentified") {
				t.Errorf("selling an unknown item: %q", why)
			}
		}
	}
	if _, refusal := dropToGround(hero, ringID, 1, ""); refusal != "" {
		t.Fatalf("drop: %s", refusal)
	}
	var groundName, bundle string
	db.QueryRow(`SELECT name, bundle::text FROM dropped_items WHERE dropped_by = $1 ORDER BY id DESC LIMIT 1`, hero).Scan(&groundName, &bundle)
	if groundName != "a plain silver ring" || !strings.Contains(bundle, `"unidentified": true`) && !strings.Contains(bundle, `"unidentified":true`) {
		t.Errorf("the ground: %q, bundle %s", groundName, bundle)
	}
}
