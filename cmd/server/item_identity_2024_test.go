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

// Stage 2: every command a player sends names an unidentified item by its look and acts on the item itself; what needs knowing the item
// (attuning, working it, casting from it) waits for it to be identified; a potion is drunk all the same.
func TestPostgresUnidentifiedItemByItsLookInEveryCommand(t *testing.T) {
	testPostgres(t)
	gm, player, camp, hero, _ := earlyRulesFixture(t, "Test Look Raider", nil)
	give := func(name, look string) {
		if c, r := pgCall(t, "POST", "/api/gm/give-item", gm, map[string]interface{}{"character_id": hero, "campaign_id": camp, "item_name": name, "unidentified": true, "appears_as": look}); c != 200 {
			t.Fatalf("give %s: %d %v", name, c, r)
		}
	}
	give("Longsword +1", "a longsword with a sea-green hilt")
	give("Leather Armor +1", "a supple dark leather jerkin")
	give("Ring of Protection", "a plain silver ring")
	give("Wand of Magic Missiles", "a crooked ash wand")
	give("Potion of Healing", "a vial of cloudy red liquid")
	pgExec(t, `UPDATE characters SET equipped_main_hand = NULL, equipped_off_hand = NULL, equipped_armor = NULL, hp = 1 WHERE id = $1`, hero)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/end", camp), gm, map[string]interface{}{}) // armor changes outside a fight
	quiet := func(where string, r map[string]interface{}) {
		t.Helper()
		b, _ := json.Marshal(r)
		for _, s := range []string{"Longsword +1", "Leather Armor +1", "Ring of Protection", "Wand of Magic Missiles", "Magic Missile"} {
			if strings.Contains(string(b), s) {
				t.Errorf("%s names %q: %s", where, s, b)
			}
		}
	}
	// equip a weapon by its look: the hand holds the item itself
	c, r := pgCall(t, "POST", "/api/characters/equip-weapon", player, map[string]interface{}{"character_id": hero, "weapon": "a longsword with a sea-green hilt", "slot": "main_hand"})
	var mainHand, armor string
	db.QueryRow(`SELECT COALESCE(equipped_main_hand,''), COALESCE(equipped_armor,'') FROM characters WHERE id = $1`, hero).Scan(&mainHand, &armor)
	if c != 200 || mainHand != "Longsword +1" || r["weapon"] != "a longsword with a sea-green hilt" {
		t.Errorf("equip by its look: %d %v (hand %q)", c, r, mainHand)
	}
	quiet("the equip reply", r)
	// wear armor by its look
	c, r = pgCall(t, "POST", "/api/characters/equip-armor", player, map[string]interface{}{"character_id": hero, "armor": "supple dark leather jerkin"})
	db.QueryRow(`SELECT COALESCE(equipped_armor,'') FROM characters WHERE id = $1`, hero).Scan(&armor)
	if c != 200 || armor != "Leather Armor +1" || r["equipped_armor"] != "a supple dark leather jerkin" {
		t.Errorf("wear by its look: %d %v (armor %q)", c, r, armor)
	}
	quiet("the armor reply", r)
	// move the ring by its look; attuning waits for it to be identified
	if c, r := pgCall(t, "POST", fmt.Sprintf("/api/characters/%d/equipment/move", hero), player, map[string]interface{}{"item": "a plain silver ring", "to": "worn"}); c != 200 {
		t.Errorf("move by its look: %d %v", c, r)
	} else {
		quiet("the move reply", r)
	}
	if c, r := pgCall(t, "POST", "/api/characters/attune", player, map[string]interface{}{"character_id": hero, "action": "attune", "item_name": "a plain silver ring"}); c != 409 || r["error"] != "item_unidentified" {
		t.Errorf("attuning an unknown ring: %d %v", c, r)
	} else {
		quiet("the attune refusal", r)
	}
	// the wand can't be worked, nor cast from; the vial is drunk
	act := func(body map[string]interface{}) (int, map[string]interface{}) {
		pgExec(t, `UPDATE characters SET action_used = false, bonus_action_used = false WHERE id = $1`, hero)
		return pgCall(t, "POST", "/api/action", player, body)
	}
	c, r = act(map[string]interface{}{"action": "use_item", "item": "a crooked ash wand", "description": "I wave the crooked ash wand"})
	if res := fmt.Sprint(r["result"], r["message"]); !strings.Contains(res, "unidentified") {
		t.Errorf("working an unknown wand: %d %v", c, r)
	}
	quiet("the wand refusal", r)
	c, r = act(map[string]interface{}{"action": "cast", "spell_slug": "magic-missile", "item": "a crooked ash wand", "description": "missiles from the wand"})
	if res := fmt.Sprint(r["result"], r["message"]); !strings.Contains(res, "unidentified") && r["error"] != "spell_not_available" {
		t.Errorf("casting from an unknown wand: %d %v", c, r)
	}
	if res := fmt.Sprint(r["result"]); strings.Contains(res, "darts") || strings.Contains(res, "Force damage") {
		t.Errorf("the unknown wand cast: %v", r)
	}
	c, r = act(map[string]interface{}{"action": "use_item", "description": "I drink the vial of cloudy red liquid"})
	var hp int
	db.QueryRow(`SELECT hp FROM characters WHERE id = $1`, hero).Scan(&hp)
	if hp <= 1 {
		t.Errorf("drinking the unknown vial: %d %v (hp %d)", c, r, hp)
	}
	// drop by its look
	if _, refusal := dropToGround(hero, "a crooked ash wand", 1, ""); refusal != "" {
		t.Errorf("drop by its look: %s", refusal)
	}
}
