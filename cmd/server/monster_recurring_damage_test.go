package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Recurring turn-start damage a hit sets up ("Until the grapple ends, the target takes 4 (1d8) Bludgeoning
// damage at the start of each of its turns"): recorded when its grapple gate holds, ticked exactly once per
// affected turn start by the checkpointed turn pipeline (source- vs target-anchored timing), ended and
// removed by a real escape or the source's death, never repeated by a route replay, and never silently
// started when its gate did not hold (companion: size unknown, the GM's).
func TestPostgresRecurringDamage(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "recurring gm")
	_, p1 := pgAgent(t, "recurring p1")
	_, p2 := pgAgent(t, "recurring p2")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Recurring damage", "max_players": 2})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	mk := func(tok, label string) int {
		_, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("%s %d", label, time.Now().UnixNano()%1e9)))
		id := int(j["character_id"].(float64))
		pgCall(t, "POST", cp+"/join", tok, map[string]interface{}{"character_id": id})
		pgFinish(t, tok, id)
		return id
	}
	hero := mk(p1, "Recurring Hero")
	hero2 := mk(p2, "Second Hero")
	pgCall(t, "POST", cp+"/ready", p1, nil)
	pgCall(t, "POST", cp+"/ready", p2, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var goblin srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Goblin Warrior" {
			goblin = m
		}
	}
	con := goblin
	con.Name = "Test Constrictor"
	con.Actions = []srdMonsterEntry{
		{Name: "Constrict", Text: "Melee Attack Roll: +60, reach 10 ft. Hit: 6 (1d8 + 2) Bludgeoning damage. If the target is a Large or smaller creature, it has the Grappled condition (escape DC 12). Until the grapple ends, the target takes 4 (1d8) Bludgeoning damage at the start of each of its turns."},
		{Name: "ConstrictPlain", Text: "Melee Attack Roll: +60, reach 10 ft. Hit: 6 (1d8 + 2) Bludgeoning damage, and the target has the Grappled condition (escape DC 12). Until the grapple ends, the target takes 4 (1d8) Bludgeoning damage at the start of each of its turns."},
		{Name: "Punch", Text: "Melee Attack Roll: +60, reach 10 ft. Hit: 3 (1d4 + 1) Bludgeoning damage."},
	}
	leech := goblin
	leech.Name = "Test Leech"
	leech.Actions = []srdMonsterEntry{
		{Name: "Siphon", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 3 (1d4 + 1) Piercing damage. If the target is a Large or smaller creature, it has the Grappled condition (escape DC 12). Until the grapple ends, the target takes 3 (1d4) Necrotic damage at the start of each of the leech's turns."},
	}
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variant for recurring damage tests", "monsters": []srdMonster2024{con, leech}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	keys := map[string]string{}
	for _, m := range j["monsters"].([]interface{}) {
		mm := m.(map[string]interface{})
		keys[fmt.Sprint(mm["name"])] = fmt.Sprint(mm["monster_key"])
	}
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Test Constrictor", "monster_key": keys["Test Constrictor"], "initiative": 20},
		{"name": "Test Constrictor", "monster_key": keys["Test Constrictor"], "initiative": 19},
		{"name": "Test Constrictor", "monster_key": keys["Test Constrictor"], "initiative": 17},
		{"name": "Test Leech", "monster_key": keys["Test Leech"], "initiative": 18},
	}})
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,ac=1 WHERE id=$1`, hero)
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,ac=1 WHERE id=$1`, hero2)
	conIDs := monsterIDsByKeyOrName(camp, "Test Constrictor")
	leechIDs := monsterIDsByKeyOrName(camp, "Test Leech")
	if len(conIDs) != 3 || len(leechIDs) != 1 {
		t.Fatalf("combatants: %v %v", conIDs, leechIDs)
	}
	conA, conB, conC, leechID := conIDs[0], conIDs[1], conIDs[2], leechIDs[0]
	// the fixture companion of the first hero
	c, j = pgCall(t, "POST", "/api/gm/companion", gm, map[string]interface{}{"campaign_id": camp, "character_id": hero, "monster_key": keys["Test Constrictor"], "name": "Recurring Pet"})
	if c != 200 && c != 201 {
		t.Fatalf("companion: %d %v", c, j)
	}
	petRow := int(j["companion"].(map[string]interface{})["id"].(float64))
	pet, ok := loadCompanion(petRow)
	if !ok {
		t.Fatal("companion not loadable")
	}
	petID := pet.CombatID
	pgExec(t, `UPDATE companions SET hp=1000,max_hp=1000,ac=1 WHERE id=$1`, petRow)

	hp := func(id int) int {
		var n int
		var err error
		if id > 0 && !isCompanionCombatID(id) {
			err = db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, id).Scan(&n)
		} else if isCompanionCombatID(id) {
			err = db.QueryRow(`SELECT hp FROM companions WHERE id=$1`, petRow).Scan(&n)
		} else {
			err = db.QueryRow(`SELECT COALESCE((SELECT (e->>'hp')::int FROM combat_state cs, jsonb_array_elements(cs.turn_order) e WHERE cs.lobby_id=$1 AND (e->>'id')::int=$2), -1)`, camp, id).Scan(&n)
		}
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	rows := func(target int) int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind='recurring_damage' AND target_id=$2`, camp, target).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	grapples := func(target int) int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM condition_records WHERE lobby_id=$1 AND target_id=$2 AND condition='grappled'`, camp, target).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	narrate := func(monsterID int, action string, target int) (string, map[string]interface{}) {
		t.Helper()
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", monsterID), "action": action, "target": fmt.Sprint(target)}})
		atk, _ := r["monster_attack"].(map[string]interface{})
		return fmt.Sprint(r["monster_action_result"]), atk
	}
	hitUntil := func(monsterID int, action string, target int, want string) string {
		t.Helper()
		for try := 0; try < 30; try++ {
			res, _ := narrate(monsterID, action, target)
			if strings.Contains(res, want) {
				return res
			}
		}
		t.Fatalf("%d's %s never produced %q", monsterID, action, want)
		return ""
	}

	// A grapples the first hero: the recurring damage is recorded once, told publicly, not left to the GM
	res := hitUntil(conA, "Constrict", hero, "Grappled")
	if !strings.Contains(res, "damage at the start of each of its turns while the grapple lasts") || rows(hero) != 1 {
		t.Fatalf("the hit did not record its recurring damage once: %d %s", rows(hero), res)
	}
	if extra := unappliedHitRider(con.Actions[0].Text); extra != "" {
		t.Fatalf("an enforced recurring sentence stayed in the GM residual: %q", extra)
	}
	if extra := unappliedHitRider(leech.Actions[0].Text); extra != "" {
		t.Fatalf("an enforced source-anchored recurring sentence stayed in the GM residual: %q", extra)
	}
	// an attach-gated recurring clause (Stirge) is read with the attach owner: it leaves the GM residual
	if extra := unappliedHitRider("Melee Attack Roll: +5, reach 5 ft. Hit: 6 (1d6 + 3) Piercing damage. While attached, the stirge can't make Proboscis attacks, and the target takes 5 (2d4) Necrotic damage at the start of each of the stirge's turns."); strings.Contains(extra, "2d4") {
		t.Fatalf("an enforced attach-gated recurring clause stayed in the GM residual: %q", extra)
	}
	// the leech grapples the same hero: a second, source-anchored row; both gates live independently
	hitUntil(leechID, "Siphon", hero, "Grappled")
	if rows(hero) != 2 || grapples(hero) != 2 {
		t.Fatalf("the leech's grapple did not add its own row: rows=%d grapples=%d", rows(hero), grapples(hero))
	}
	// B grapples the second hero
	hitUntil(conB, "Constrict", hero2, "Grappled")
	if rows(hero2) != 1 {
		t.Fatalf("B's grapple did not record on the second hero: %d", rows(hero2))
	}
	// C hits the companion: its size is unknown to the server, so the grapple is the GM's call, no recurring
	// row is written, and the stripped sentence goes back to the GM note
	res = hitUntil(conC, "Constrict", petID, "HIT")
	if !strings.Contains(res, "check the companion's size") {
		t.Fatalf("the companion hit did not carry the size ruling: %s", res)
	}
	if !strings.Contains(res, "check the companion's size; Until the grapple ends, the target takes 4 (1d8) Bludgeoning damage at the start of each of its turns") {
		t.Fatalf("the companion's recurring clause was not returned to the GM with the grapple: %s", res)
	}
	if rows(petID) != 0 {
		t.Fatalf("a companion with an unresolved gate got a recurring row: %d", rows(petID))
	}

	// Per-turn timing, source vs target anchor, and same-name/different-source isolation: walk the order one
	// turn at a time and assert exactly the ticks each turn start owes. Constrictor/Leech turn starts owe
	// nothing; each hero's turn start owes its target-anchored 1d8; the leech's turn start owes the 1d4.
	step := func() string {
		t.Helper()
		_, r := pgCall(t, "POST", cp+"/combat/next", gm, map[string]interface{}{})
		if r["success"] != true {
			t.Fatalf("the turn did not advance: %v", r)
		}
		return fmt.Sprint(r["current_turn"])
	}
	hps := func() (int, int, int) { return hp(hero), hp(hero2), hp(petID) }
	seen := map[string]bool{}
	for steps := 0; steps < 12 && !(seen["hero"] && seen["hero2"] && seen["leech"]); steps++ {
		h1, h2, hp0 := hps()
		cur := step()
		d1, d2, dp := h1-hp(hero), h2-hp(hero2), hp0-hp(petID)
		switch {
		case strings.Contains(cur, "Recurring Hero"):
			if seen["hero"] {
				continue
			}
			seen["hero"] = true
			if d1 < 1 || d1 > 8 || d2 != 0 || dp != 0 {
				t.Fatalf("the hero's turn start did not tick exactly its own 1d8: %d %d %d", d1, d2, dp)
			}
		case strings.Contains(cur, "Second Hero"):
			if seen["hero2"] {
				continue
			}
			seen["hero2"] = true
			if d2 < 1 || d2 > 8 || d1 != 0 || dp != 0 {
				t.Fatalf("the second hero's turn start did not tick exactly its own 1d8: %d %d %d", d1, d2, dp)
			}
		// the pet has no turn of its own (it shares its owner's Initiative): the hero case's dp == 0 covers
		// that no pet row ticks at the owner's turn start
		case strings.Contains(cur, "Test Leech"):
			if seen["leech"] {
				continue
			}
			seen["leech"] = true
			if d1 < 1 || d1 > 4 || d2 != 0 || dp != 0 {
				t.Fatalf("the leech's turn start did not tick exactly its source-anchored 1d4: %d %d %d", d1, d2, dp)
			}
		default: // a constrictor's turn start: no row is anchored to it
			if d1 != 0 || d2 != 0 || dp != 0 {
				t.Fatalf("a constrictor's turn start dealt recurring damage: %d %d %d", d1, d2, dp)
			}
		}
	}
	if !(seen["hero"] && seen["hero2"] && seen["leech"]) {
		t.Fatalf("the walk did not reach every turn: %v", seen)
	}

	// Route replay: re-ending a turn that already ended is refused and changes nothing
	_, r0 := pgCall(t, "POST", cp+"/combat/next", gm, map[string]interface{}{})
	rnd, idx := r0["round"], r0["turn_index"]
	_, r1 := pgCall(t, "POST", cp+"/combat/next", gm, map[string]interface{}{"expected_turn": map[string]interface{}{"round": rnd, "turn_index": idx}})
	if r1["success"] != true {
		t.Fatalf("ending the named turn failed: %v", r1)
	}
	// snapshot AFTER the legitimate advance: its turn start may tick a row; the refused replay may not
	h1, h2, hp0 := hps()
	code, r2 := pgCall(t, "POST", cp+"/combat/next", gm, map[string]interface{}{"expected_turn": map[string]interface{}{"round": rnd, "turn_index": idx}})
	if code != 409 {
		t.Fatalf("re-ending the same turn was not refused: %d %v", code, r2)
	}
	if a1, a2, ap := hps(); a1 != h1 || a2 != h2 || ap != hp0 {
		t.Fatalf("the replay changed Hit Points: %d %d %d -> %d %d %d", h1, h2, hp0, a1, a2, ap)
	}

	// Real escape: the first hero escapes the constrictor's grapple through the escape route (bounded: the
	// check can fail; a failed try costs the turn's action, so walk turns between tries). The leech's
	// grapple is untouched.
	escaped := false
	for tries := 0; tries < 12 && !escaped; tries++ {
		for cur := step(); !strings.Contains(cur, "Recurring Hero"); cur = step() {
		}
		_, conds := pgCall(t, "GET", fmt.Sprintf("/api/conditions/active?character_id=%d", hero), gm, nil)
		recID := 0
		for _, cv := range conds["conditions"].([]interface{}) {
			m := cv.(map[string]interface{})
			if m["condition"] == "grappled" && strings.Contains(fmt.Sprint(m["source"]), "Constrictor") {
				recID = int(m["id"].(float64))
			}
		}
		if recID == 0 {
			t.Fatalf("the constrictor's grapple record was not listed: %v", conds)
		}
		_, er := pgCall(t, "POST", "/api/conditions/escape", p1, map[string]interface{}{"character_id": hero, "record_id": recID, "escape": 0})
		escaped, _ = er["success"].(bool)
	}
	if !escaped {
		t.Fatal("the escape never succeeded in 12 tries")
	}
	if grapples(hero) != 1 || rows(hero) != 2 {
		t.Fatalf("the escape changed the wrong gate: grapples=%d rows=%d", grapples(hero), rows(hero))
	}
	// its next turn start: the escaped constrictor's row ends without damage; the leech's row remains
	for cur := step(); !strings.Contains(cur, "Recurring Hero"); cur = step() {
	}
	// measure exactly the next hero turn start: the leech's 1d4 on the hero ticks at the LEECH's turn
	// start inside a full round, so only the step that lands on the hero may change the hero's HP here
	h1, _, _ = hps()
	for cur := step(); !strings.Contains(cur, "Recurring Hero"); cur = step() {
		h1, _, _ = hps()
	}
	if d := h1 - hp(hero); d != 0 {
		t.Fatalf("an escaped constrictor's recurring damage ticked at the hero's own turn start: %d", d)
	}
	if rows(hero) != 1 || grapples(hero) != 1 {
		t.Fatalf("the escaped row did not end exactly: rows=%d grapples=%d", rows(hero), grapples(hero))
	}
	// Death: kill B out from under its grapple; the second hero's next turn start ends the row, no damage
	if _, err := changeFight(camp, func(f *fight) error {
		for _, e := range f.Order {
			if asInt(e["id"]) == conB {
				e["hp"] = 0
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, h2, _ = hps()
	for cur := step(); !strings.Contains(cur, "Second Hero"); cur = step() {
		_, h2, _ = hps()
	}
	if d := h2 - hp(hero2); d != 0 {
		t.Fatalf("a dead constrictor's recurring damage ticked at its target's own turn start: %d", d)
	}
	if rows(hero2) != 0 {
		t.Fatalf("a dead source's row outlived it: %d", rows(hero2))
	}

	// Companion, gate held: a plain grapple applies, its recurring row records and ticks on the companion's
	// turn start, and it ends when the GM ends the grapple (DELETE /api/gm/conditions)
	res = hitUntil(conC, "ConstrictPlain", petID, "HIT")
	if rows(petID) != 1 || grapples(petID) != 1 {
		t.Fatalf("the companion's applied grapple did not record its recurring row: rows=%d grapples=%d (%s)", rows(petID), grapples(petID), res)
	}
	if !strings.Contains(res, "at the start of each of its turns while the grapple lasts") {
		t.Fatalf("the applied companion hit did not name its recurring damage: %s", res)
	}
	// the pet has no Initiative entry of its own: its turn is part of its owner's, so the row ticks at the
	// owner's turn start
	_, _, hp0 = hps()
	for cur := step(); !strings.Contains(cur, "Recurring Hero"); cur = step() {
	}
	if d := hp0 - hp(petID); d < 1 || d > 8 {
		t.Fatalf("the companion's applied recurring row did not tick its 1d8 at the owner's turn start: %d", d)
	}
	// source death: kill C out from under the companion's grapple; the owner's next turn start ends the
	// row with no damage. (The condition routes take character targets only, so a companion's grapple is
	// not ended by DELETE /api/gm/conditions today: companion_reactions_test.go documents that refusal.
	// An escape route for a companion target is likewise not wired - both stay on the unfinished list.)
	if _, err := changeFight(camp, func(f *fight) error {
		for _, e := range f.Order {
			if asInt(e["id"]) == conC {
				e["hp"] = 0
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, _, hp0 = hps()
	for cur := step(); !strings.Contains(cur, "Recurring Hero"); cur = step() {
		_, _, hp0 = hps()
	}
	if d := hp0 - hp(petID); d != 0 {
		t.Fatalf("a dead grappler's recurring damage ticked on the companion at the owner's turn start: %d", d)
	}
	if rows(petID) != 0 {
		t.Fatalf("the companion's row outlived its dead grappler: %d", rows(petID))
	}
}
