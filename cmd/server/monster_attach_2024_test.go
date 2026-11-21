package main

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The attach owner (Stirge Proboscis): the hit records the attach as a combat effect, the recurring damage
// it gates ticks on the ATTACKER's turn starts while the attach lasts, the attack it locks is refused with
// nothing rolled, the GM ends the attach with /api/gm/detach (the stat block gives no way), and a death on
// either side ends it at the next turn start (attachSweep - a dead creature has no turn of its own).
func TestPostgresAttach(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "attach gm")
	_, p1 := pgAgent(t, "attach p1")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Attach", "max_players": 1})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", p1, pgRogue(fmt.Sprintf("Attach Hero %d", time.Now().UnixNano()%1e9)))
	hero := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", p1, map[string]interface{}{"character_id": hero})
	pgFinish(t, p1, hero)
	pgCall(t, "POST", cp+"/ready", p1, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var goblin srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Goblin Warrior" {
			goblin = m
		}
	}
	stirge := goblin
	stirge.Name = "Test Stirge"
	proboscis := "Melee Attack Roll: +60, reach 5 ft. Hit: 6 (1d6 + 3) Piercing damage, and the stirge attaches to the target. While attached, the stirge can't make Proboscis attacks, and the target takes 5 (2d4) Necrotic damage at the start of each of the stirge's turns."
	stirge.Actions = []srdMonsterEntry{
		{Name: "Proboscis", Text: proboscis},
		{Name: "Slam", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 3 (1d4 + 1) Bludgeoning damage."},
	}
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variant for attach tests", "monsters": []srdMonster2024{stirge}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Test Stirge", "monster_key": key, "initiative": 20},
	}})
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,ac=1 WHERE id=$1`, hero)
	ids := monsterIDsByKeyOrName(camp, "Test Stirge")
	if len(ids) != 1 {
		t.Fatalf("combatants: %v", ids)
	}
	stirgeID := ids[0]

	hp := func() int {
		var n int
		if err := db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, hero).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	rows := func(kind string) int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind=$2`, camp, kind).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	narrate := func(action string) (string, map[string]interface{}) {
		t.Helper()
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", stirgeID), "action": action, "target": fmt.Sprint(hero)}})
		atk, _ := r["monster_attack"].(map[string]interface{})
		return fmt.Sprint(r["monster_action_result"]), atk
	}
	step := func() string {
		t.Helper()
		_, r := pgCall(t, "POST", cp+"/combat/next", gm, map[string]interface{}{})
		if r["success"] != true {
			t.Fatalf("the turn did not advance: %v", r)
		}
		return fmt.Sprint(r["current_turn"])
	}
	hitUntil := func(action, want string) string {
		t.Helper()
		for try := 0; try < 30; try++ {
			res, _ := narrate(action)
			if strings.Contains(res, want) {
				return res
			}
		}
		t.Fatalf("%s never produced %q", action, want)
		return ""
	}

	// the hit attaches: the row is written, the recurring damage it gates is recorded with its source
	// anchor, both are told publicly, and nothing of the two sentences is left for the GM
	res := hitUntil("Proboscis", "attaches to the target")
	if !strings.Contains(res, "attaches to the target") || rows("attached") != 1 {
		t.Fatalf("the hit did not record its attach: rows=%d %s", rows("attached"), res)
	}
	if !strings.Contains(res, "5 (2d4) necrotic damage at the start of each of the Test Stirge's turns while the attach lasts") || rows("recurring_damage") != 1 {
		t.Fatalf("the attach-gated recurring damage was not recorded with its source anchor: rows=%d %s", rows("recurring_damage"), res)
	}
	if strings.Contains(res, "not applied automatically") {
		t.Fatalf("an enforced attach sentence was left to the GM: %s", res)
	}
	if extra := unappliedHitRider(proboscis); extra != "" {
		t.Fatalf("an enforced attach sentence stayed in the GM residual: %q", extra)
	}

	// the lockout the attach carries: Proboscis is refused with its reason, nothing rolled, HP unchanged;
	// another attack is free
	if hpBefore := hp(); true {
		res, atk := narrate("Proboscis")
		if atk["refused"] != true || !strings.Contains(res, "can't make Proboscis attacks") {
			t.Fatalf("the attach lockout did not refuse Proboscis: %v %s", atk, res)
		}
		if hp() != hpBefore {
			t.Fatalf("a refused attach-locked attack changed HP %d -> %d", hpBefore, hp())
		}
	}
	if res := hitUntil("Slam", "HIT"); strings.Contains(res, "refused") {
		t.Fatalf("the attach locked an attack its text does not name: %s", res)
	}

	// per-turn timing: the tick is anchored to the ATTACKER's turns. The stirge's turn start deals 2d4 to
	// the hero; the hero's own turn start deals nothing.
	seen := map[string]bool{}
	for steps := 0; steps < 8 && !(seen["stirge"] && seen["hero"]); steps++ {
		before := hp()
		cur := step()
		d := before - hp()
		switch {
		case strings.Contains(cur, "Test Stirge"):
			if seen["stirge"] {
				continue
			}
			seen["stirge"] = true
			if d < 2 || d > 8 {
				t.Fatalf("the stirge's turn start did not tick the source-anchored 2d4: %d", d)
			}
		case strings.Contains(cur, "Attach Hero"):
			if seen["hero"] {
				continue
			}
			seen["hero"] = true
			if d != 0 {
				t.Fatalf("the hero's own turn start ticked an attacker-anchored row: %d", d)
			}
		}
	}
	if !(seen["stirge"] && seen["hero"]) {
		t.Fatalf("the walk did not reach both turns: %v", seen)
	}

	// the GM ends the attach (the stat block gives no way): the attach and the recurring damage it gated
	// end together, a second detach is a 404, and the lockout is gone (the next Proboscis rolls and, on
	// its hit, attaches again)
	c, j = pgCall(t, "POST", "/api/gm/detach", gm, map[string]interface{}{"campaign_id": camp, "attacher_id": stirgeID, "target_id": hero})
	if c != 200 || rows("attached") != 0 || rows("recurring_damage") != 0 {
		t.Fatalf("the detach did not end the attach and its recurring damage: %d %v rows=%d/%d", c, j, rows("attached"), rows("recurring_damage"))
	}
	c, _ = pgCall(t, "POST", "/api/gm/detach", gm, map[string]interface{}{"campaign_id": camp, "attacher_id": stirgeID, "target_id": hero})
	if c != 404 {
		t.Fatalf("detaching a missing attach was not a 404: %d", c)
	}
	if res := hitUntil("Proboscis", "attaches to the target"); strings.Contains(res, "refused") {
		t.Fatalf("the attack stayed locked after the detach: %s", res)
	}
	if rows("attached") != 1 || rows("recurring_damage") != 1 {
		t.Fatalf("the re-hit did not record its attach and recurring damage again: %d/%d", rows("attached"), rows("recurring_damage"))
	}

	// death: kill the stirge out from under its attach; the next turn start sweeps the attach and the
	// recurring damage it gated (a dead creature has no turn of its own to end them on)
	if _, err := changeFight(camp, func(f *fight) error {
		for _, e := range f.Order {
			if asInt(e["id"]) == stirgeID {
				e["hp"] = 0
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	step()
	if rows("attached") != 0 || rows("recurring_damage") != 0 {
		t.Fatalf("the attach and its recurring damage outlived the dead attacher: %d/%d", rows("attached"), rows("recurring_damage"))
	}
}

// The published detach action (SRD 5.2.1): a stirge's attach ends as an action without a check, taken by
// the target or a creature within 5 feet of it; a darkmantle's by the attached creature's own DC 13
// Strength (Athletics) check. The attempt costs the action; a failed check spends it and the attach holds;
// an actor who is not the target needs the rule to allow it and the map to place them (else the GM rules).
func TestPostgresAttachDetachAction(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "detach gm")
	_, p1 := pgAgent(t, "detach p1")
	_, p2 := pgAgent(t, "detach p2")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Detach action", "max_players": 2})
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
	hero := mk(p1, "Detach Hero")
	hero2 := mk(p2, "Detach Second")
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
	stirge := goblin
	stirge.Name = "Test Detach Stirge"
	stirge.Actions = []srdMonsterEntry{{Name: "Proboscis", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 6 (1d6 + 3) Piercing damage, and the stirge attaches to the target. While attached, the stirge can't make Proboscis attacks, and the target takes 5 (2d4) Necrotic damage at the start of each of the stirge's turns.\nThe stirge can detach itself by spending 5 feet of its movement. The target or a creature within 5 feet of it can detach the stirge as an action."}}
	dark := goblin
	dark.Name = "Test Detach Darkmantle"
	dark.Actions = []srdMonsterEntry{{Name: "Crush", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 6 (1d6 + 3) Bludgeoning damage, and the darkmantle attaches to the target. If the target is a Medium or smaller creature and the darkmantle had Advantage on the attack roll, it covers the target, which has the Blinded condition and is suffocating while the darkmantle is attached in this way.\nWhile attached to a target, the darkmantle can attack only the target but has Advantage on its attack rolls. Its Speed becomes 0, it can't benefit from any bonus to its Speed, and it moves with the target.\nA creature can take an action to try to detach the darkmantle from itself, doing so with a successful DC 13 Strength (Athletics) check. On its turn, the darkmantle can detach itself by using 5 feet of movement."}}
	plain := goblin
	plain.Name = "Test Detach Plain"
	plain.Actions = []srdMonsterEntry{{Name: "Latch", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 2 (1d4) Piercing damage, and the creature attaches to the target."}}
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variants for detach action tests", "monsters": []srdMonster2024{stirge, dark, plain}})
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
		{"name": "Test Detach Stirge", "monster_key": keys["Test Detach Stirge"], "initiative": 20},
		{"name": "Test Detach Darkmantle", "monster_key": keys["Test Detach Darkmantle"], "initiative": 19},
		{"name": "Test Detach Plain", "monster_key": keys["Test Detach Plain"], "initiative": 18},
	}})
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,ac=1 WHERE id=$1`, hero)
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,ac=1 WHERE id=$1`, hero2)
	stirgeIDs := monsterIDsByKeyOrName(camp, "Test Detach Stirge")
	darkIDs := monsterIDsByKeyOrName(camp, "Test Detach Darkmantle")
	plainIDs := monsterIDsByKeyOrName(camp, "Test Detach Plain")
	if len(stirgeIDs) != 1 || len(darkIDs) != 1 || len(plainIDs) != 1 {
		t.Fatalf("combatants: %v %v %v", stirgeIDs, darkIDs, plainIDs)
	}
	stirgeID, darkID, plainID := stirgeIDs[0], darkIDs[0], plainIDs[0]

	rows := func(kind string) int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind=$2`, camp, kind).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	narrate := func(monsterID int, action string, target int) string {
		t.Helper()
		for try := 0; try < 30; try++ {
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", monsterID), "action": action, "target": fmt.Sprint(target)}})
			if res := fmt.Sprint(r["monster_action_result"]); strings.Contains(res, "attaches to the target") {
				return res
			}
		}
		t.Fatalf("%d's %s never attached", monsterID, action)
		return ""
	}
	detach := func(tok string, actor, attacher, target int) (int, map[string]interface{}) {
		t.Helper()
		return pgCall(t, "POST", "/api/attach/detach", tok, map[string]interface{}{"character_id": actor, "attacher_id": attacher, "target_id": target})
	}
	rowText := func() string {
		var s string
		if err := db.QueryRow(`SELECT text FROM combat_effects WHERE lobby_id=$1 AND kind='attached' LIMIT 1`, camp).Scan(&s); err != nil {
			return ""
		}
		return s
	}

	// stirge, automatic, the target's own action: the row carries the published rule; the detach ends the
	// attach and its gated recurring damage, and costs the action
	narrate(stirgeID, "Proboscis", hero)
	if !strings.Contains(rowText(), "detach: an action, no check") {
		t.Fatalf("the attach row did not carry the published detach rule: %q", rowText())
	}
	c, j = detach(p1, hero, stirgeID, hero)
	if c != 200 || j["success"] != true || rows("attached") != 0 || rows("recurring_damage") != 0 {
		t.Fatalf("the automatic detach failed: %d %v rows=%d/%d", c, j, rows("attached"), rows("recurring_damage"))
	}
	// the action is spent: a re-attach and a second attempt are refused before any state changes
	narrate(stirgeID, "Proboscis", hero)
	c, _ = detach(p1, hero, stirgeID, hero)
	if c != 409 {
		t.Fatalf("a second detach did not meet the spent action: %d", c)
	}
	if rows("attached") != 1 || rows("recurring_damage") != 1 {
		t.Fatalf("the refused attempt changed the attach state: %d/%d", rows("attached"), rows("recurring_damage"))
	}
	// an adjacent creature may detach a stirge - but only when the map places it within 5 feet; without a
	// map placing both, the GM rules it
	c, j = detach(p2, hero2, stirgeID, hero)
	if c != 409 || j["error"] != "distance_unknown" {
		t.Fatalf("an adjacent detach without map positions was not left to the GM: %d %v", c, j)
	}
	if rows("attached") != 1 {
		t.Fatalf("the unresolved adjacent attempt changed the attach state: %d", rows("attached"))
	}
	pgCall(t, "POST", "/api/gm/detach", gm, map[string]interface{}{"campaign_id": camp, "attacher_id": stirgeID, "target_id": hero})

	// darkmantle: only the attached creature, with a DC 13 Strength (Athletics) check
	narrate(darkID, "Crush", hero2)
	if !strings.Contains(rowText(), "detach: an action, DC 13 Strength (Athletics)") {
		t.Fatalf("the darkmantle row did not carry its check rule: %q", rowText())
	}
	c, j = detach(p1, hero, darkID, hero2)
	if c != 400 || j["error"] != "self_only" {
		t.Fatalf("a third creature tried the darkmantle's self-only detach: %d %v", c, j)
	}
	c, j = detach(p2, hero2, darkID, hero2)
	if c != 200 {
		t.Fatalf("the attached creature's check was not run: %d %v", c, j)
	}
	total, okTot := j["total"].(float64)
	success, okS := j["success"].(bool)
	if !okTot || !okS || success != (int(total) >= 13) {
		t.Fatalf("the check's success did not match its roll: %v", j)
	}
	if success && rows("attached") != 0 {
		t.Fatalf("the successful check left the attach: %d", rows("attached"))
	}
	if !success && rows("attached") != 1 {
		t.Fatalf("the failed check ended the attach: %d", rows("attached"))
	}
	if !success {
		c, _ = detach(p2, hero2, darkID, hero2)
		if c != 409 {
			t.Fatalf("a failed check did not spend the action: %d", c)
		}
	}

	// no published detach action: the action route refuses and changes nothing; the GM route still ends it
	pgCall(t, "POST", "/api/gm/detach", gm, map[string]interface{}{"campaign_id": camp, "attacher_id": darkID, "target_id": hero2}) // clear a surviving darkmantle attach (a failed check)
	pgExec(t, `UPDATE characters SET action_used=false WHERE id=$1`, hero)
	narrate(plainID, "Latch", hero)
	if rows("attached") != 1 {
		t.Fatalf("the plain attach was not recorded: %d", rows("attached"))
	}
	c, j = detach(p1, hero, plainID, hero)
	if c != 400 || j["error"] != "no_detach_action" {
		t.Fatalf("a rule-less attach met the action route: %d %v", c, j)
	}
	if rows("attached") != 1 {
		t.Fatalf("the refused action changed the attach state: %d", rows("attached"))
	}
	c, _ = pgCall(t, "POST", "/api/gm/detach", gm, map[string]interface{}{"campaign_id": camp, "attacher_id": plainID, "target_id": hero})
	if c != 200 || rows("attached") != 0 {
		t.Fatalf("the GM detach did not end the rule-less attach: %d rows=%d", c, rows("attached"))
	}
}

// The cover clause's Advantage gate (Darkmantle Crush): "... and the darkmantle had Advantage on the
// attack roll" - its conditions apply only when the roll had Advantage. Without it the clause triggers
// nothing; the suffocating part stays with the GM either way (the suffocation rules are the GM's).
func TestPostgresAttachAdvantageGate(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "gate gm")
	_, p1 := pgAgent(t, "gate p1")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Advantage gate", "max_players": 1})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", p1, pgRogue(fmt.Sprintf("Gate Hero %d", time.Now().UnixNano()%1e9)))
	hero := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", p1, map[string]interface{}{"character_id": hero})
	pgFinish(t, p1, hero)
	pgCall(t, "POST", cp+"/ready", p1, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var goblin srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Goblin Warrior" {
			goblin = m
		}
	}
	dark := goblin
	dark.Name = "Test Gate Darkmantle"
	crush := "Melee Attack Roll: +60, reach 5 ft. Hit: 6 (1d6 + 3) Bludgeoning damage, and the darkmantle attaches to the target. If the target is a Medium or smaller creature and the darkmantle had Advantage on the attack roll, it covers the target, which has the Blinded condition and is suffocating while the darkmantle is attached in this way."
	dark.Actions = []srdMonsterEntry{{Name: "Crush", Text: crush}}
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variant for the Advantage gate", "monsters": []srdMonster2024{dark}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Test Gate Darkmantle", "monster_key": key, "initiative": 20},
	}})
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,ac=1 WHERE id=$1`, hero)
	ids := monsterIDsByKeyOrName(camp, "Test Gate Darkmantle")
	if len(ids) != 1 {
		t.Fatalf("combatants: %v", ids)
	}
	darkID := ids[0]
	if e, ok := parseHitRider(crush); !ok || !e.CondIfAdvantage || e.CondMaxSize != "medium" {
		t.Fatalf("the cover clause's gates were not read: %+v", e)
	}
	blinded := func() int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM condition_records WHERE lobby_id=$1 AND target_id=$2 AND condition='blinded'`, camp, hero).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	grappled := func() int { // an attach never Grapples its target: the text owns no such condition
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM condition_records WHERE lobby_id=$1 AND target_id=$2 AND condition='grappled'`, camp, hero).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	hit := func() string {
		t.Helper()
		for try := 0; try < 30; try++ {
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", darkID), "action": "Crush", "target": fmt.Sprint(hero)}})
			if res := fmt.Sprint(r["monster_action_result"]); strings.Contains(res, "HIT") {
				return res
			}
		}
		t.Fatal("Crush never hit")
		return ""
	}

	// no Advantage: the attach lands, the cover clause triggers nothing, and the suffocating sentence
	// still reaches the GM
	res := hit()
	if !strings.Contains(res, "attaches to the target") {
		t.Fatalf("the hit did not attach: %s", res)
	}
	if blinded() != 0 || strings.Contains(res, "[Blinded") {
		t.Fatalf("the cover's Blinded applied without Advantage on the roll: %d %s", blinded(), res)
	}
	if !strings.Contains(res, "suffocating") {
		t.Fatalf("the suffocating sentence did not reach the GM: %s", res)
	}

	// Advantage (the target is Prone and the attack is in reach): the cover's condition applies
	pgCall(t, "POST", "/api/gm/conditions", gm, map[string]interface{}{"campaign_id": camp, "target_id": hero, "condition": "prone", "source": "the test"})
	res = hit()
	if blinded() != 1 || !strings.Contains(res, "Blinded") {
		t.Fatalf("the cover's Blinded did not apply with Advantage on the roll: %d %s", blinded(), res)
	}
	if grappled() != 0 {
		t.Fatalf("the attach invented a Grappled condition the text does not own: %d", grappled())
	}
}

// The attach owns the conditions its text ties to it ("the Blinded condition ... while the darkmantle is
// attached in this way"): ending the attach - the GM route or the death sweep - ends exactly those records
// (the attacher's, under the hit's source label), and an independent source of the same condition survives.
func TestPostgresAttachOwnedConditions(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "owns gm")
	_, p1 := pgAgent(t, "owns p1")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Attach owns", "max_players": 1})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", p1, pgRogue(fmt.Sprintf("Owns Hero %d", time.Now().UnixNano()%1e9)))
	hero := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", p1, map[string]interface{}{"character_id": hero})
	pgFinish(t, p1, hero)
	pgCall(t, "POST", cp+"/ready", p1, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var goblin srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Goblin Warrior" {
			goblin = m
		}
	}
	dark := goblin
	dark.Name = "Test Owns Darkmantle"
	crush := "Melee Attack Roll: +60, reach 5 ft. Hit: 6 (1d6 + 3) Bludgeoning damage, and the darkmantle attaches to the target. If the target is a Medium or smaller creature and the darkmantle had Advantage on the attack roll, it covers the target, which has the Blinded condition and is suffocating while the darkmantle is attached in this way.\nA creature can take an action to try to detach the darkmantle from itself, doing so with a successful DC 13 Strength (Athletics) check."
	dark.Actions = []srdMonsterEntry{{Name: "Crush", Text: crush}}
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variant for attach-owned conditions", "monsters": []srdMonster2024{dark}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Test Owns Darkmantle", "monster_key": key, "initiative": 20},
	}})
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,ac=1 WHERE id=$1`, hero)
	ids := monsterIDsByKeyOrName(camp, "Test Owns Darkmantle")
	if len(ids) != 1 {
		t.Fatalf("combatants: %v", ids)
	}
	darkID := ids[0]
	blinded := func() int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM condition_records WHERE lobby_id=$1 AND target_id=$2 AND condition='blinded'`, camp, hero).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	attachRows := func() int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind='attached'`, camp).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	rowText := func() string {
		var s string
		if err := db.QueryRow(`SELECT text FROM combat_effects WHERE lobby_id=$1 AND kind='attached' LIMIT 1`, camp).Scan(&s); err != nil {
			return ""
		}
		return s
	}
	hit := func() string {
		t.Helper()
		for try := 0; try < 30; try++ {
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", darkID), "action": "Crush", "target": fmt.Sprint(hero)}})
			if res := fmt.Sprint(r["monster_action_result"]); strings.Contains(res, "HIT") {
				return res
			}
		}
		t.Fatal("Crush never hit")
		return ""
	}

	// an independent Blinded (a spell's) is on the hero before the attach; Prone gives the attack Advantage
	pgCall(t, "POST", "/api/gm/conditions", gm, map[string]interface{}{"campaign_id": camp, "target_id": hero, "condition": "blinded", "source": "a spell"})
	pgCall(t, "POST", "/api/gm/conditions", gm, map[string]interface{}{"campaign_id": camp, "target_id": hero, "condition": "prone", "source": "the test"})
	if blinded() != 1 {
		t.Fatalf("the independent Blinded was not recorded: %d", blinded())
	}

	// the hit with Advantage: the attach row names the condition it owns and its source label
	res := hit()
	if !strings.Contains(res, "attaches to the target") || attachRows() != 1 {
		t.Fatalf("the hit did not attach: %s", res)
	}
	if !strings.Contains(rowText(), "owns: blinded @ Test Owns Darkmantle's Crush") {
		t.Fatalf("the attach row did not record the condition it owns with its source: %q", rowText())
	}
	if blinded() != 2 {
		t.Fatalf("the cover's Blinded did not join the independent one: %d", blinded())
	}

	// the detach action ends it too (Strength 40: the DC 13 check cannot fail): again exactly the attach's
	// own Blinded ends
	pgExec(t, `UPDATE characters SET str=40 WHERE id=$1`, hero)
	c, j = pgCall(t, "POST", "/api/attach/detach", p1, map[string]interface{}{"character_id": hero, "attacher_id": darkID, "target_id": hero})
	if c != 200 || j["success"] != true || attachRows() != 0 {
		t.Fatalf("the detach action did not end the attach: %d %v rows=%d", c, j, attachRows())
	}
	if !strings.Contains(fmt.Sprint(j["message"]), "Blinded condition ends") {
		t.Fatalf("the detach action did not tell the owned condition's end: %v", j)
	}
	if blinded() != 1 {
		t.Fatalf("the detach action did not end exactly the attach's own Blinded: %d", blinded())
	}

	// the GM route ends the attach: its own Blinded ends with it, the independent one survives
	hit()
	if attachRows() != 1 || blinded() != 2 {
		t.Fatalf("the re-hit did not re-attach with its Blinded: rows=%d blinded=%d", attachRows(), blinded())
	}
	c, j = pgCall(t, "POST", "/api/gm/detach", gm, map[string]interface{}{"campaign_id": camp, "attacher_id": darkID, "target_id": hero})
	if c != 200 || attachRows() != 0 {
		t.Fatalf("the detach did not end the attach: %d %v rows=%d", c, j, attachRows())
	}
	if !strings.Contains(fmt.Sprint(j["result"]), "Blinded condition ends") {
		t.Fatalf("the detach did not tell the owned condition's end: %v", j)
	}
	if blinded() != 1 {
		t.Fatalf("the detach did not end exactly the attach's own Blinded: %d", blinded())
	}

	// the death sweep: a re-attach, the darkmantle dies, the next turn start ends the attach and again
	// exactly its own Blinded
	hit()
	if attachRows() != 1 || blinded() != 2 {
		t.Fatalf("the re-hit did not re-attach with its Blinded: rows=%d blinded=%d", attachRows(), blinded())
	}
	if _, err := changeFight(camp, func(f *fight) error {
		for _, e := range f.Order {
			if asInt(e["id"]) == darkID {
				e["hp"] = 0
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, r := pgCall(t, "POST", cp+"/combat/next", gm, map[string]interface{}{}); r["success"] != true {
		t.Fatalf("the turn did not advance: %v", r)
	}
	if attachRows() != 0 || blinded() != 1 {
		t.Fatalf("the sweep did not end the attach and exactly its own Blinded: rows=%d blinded=%d", attachRows(), blinded())
	}
}

// Attach-tied suffocating (Darkmantle Crush, SRD 5.2.1 Suffocation): when the cover clause applies the
// target is suffocating - 1 Exhaustion level at the end of each of its turns while it lasts - and ending
// the attach lets it breathe again and removes exactly the levels this suffocation gave. A failed cover
// gate (no Advantage) starts nothing and the sentence stays the GM's. A monster or companion target has
// no Exhaustion store: its tick is a GM note, its cleanup says how many levels the GM removes.
func TestPostgresAttachSuffocation(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "suff gm")
	_, p1 := pgAgent(t, "suff p1")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Suffocation", "max_players": 1})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", p1, pgRogue(fmt.Sprintf("Suff Hero %d", time.Now().UnixNano()%1e9)))
	hero := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", p1, map[string]interface{}{"character_id": hero})
	pgFinish(t, p1, hero)
	pgCall(t, "POST", cp+"/ready", p1, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var goblin srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Goblin Warrior" {
			goblin = m
		}
	}
	dark := goblin
	dark.Name = "Test Suff Darkmantle"
	dark.Actions = []srdMonsterEntry{{Name: "Crush", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 6 (1d6 + 3) Bludgeoning damage, and the darkmantle attaches to the target. If the target is a Medium or smaller creature and the darkmantle had Advantage on the attack roll, it covers the target, which has the Blinded condition and is suffocating while the darkmantle is attached in this way.\nA creature can take an action to try to detach the darkmantle from itself, doing so with a successful DC 13 Strength (Athletics) check."}}
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variant for suffocation tests", "monsters": []srdMonster2024{dark}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Test Suff Darkmantle", "monster_key": key, "initiative": 20},
	}})
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,ac=1 WHERE id=$1`, hero)
	ids := monsterIDsByKeyOrName(camp, "Test Suff Darkmantle")
	if len(ids) != 1 {
		t.Fatalf("combatants: %v", ids)
	}
	darkID := ids[0]
	suffRows := func() int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind='suffocating' AND target_id=$2`, camp, hero).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	gained := func() int {
		var s string
		if err := db.QueryRow(`SELECT text FROM combat_effects WHERE lobby_id=$1 AND kind='suffocating' AND target_id=$2`, camp, hero).Scan(&s); err != nil {
			return -1
		}
		m := suffocationRowRe.FindStringSubmatch(s)
		if m == nil {
			return -1
		}
		n, _ := strconv.Atoi(m[2])
		return n
	}
	hit := func() string {
		t.Helper()
		for try := 0; try < 30; try++ {
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", darkID), "action": "Crush", "target": fmt.Sprint(hero)}})
			if res := fmt.Sprint(r["monster_action_result"]); strings.Contains(res, "HIT") {
				return res
			}
		}
		t.Fatal("Crush never hit")
		return ""
	}
	step := func() string {
		t.Helper()
		_, r := pgCall(t, "POST", cp+"/combat/next", gm, map[string]interface{}{})
		if r["success"] != true {
			t.Fatalf("the turn did not advance: %v", r)
		}
		return fmt.Sprint(r["current_turn"])
	}

	// no Advantage: the attach lands, the cover does not fire - no suffocation state, and the suffocating
	// sentence still reaches the GM
	res := hit()
	if !strings.Contains(res, "attaches to the target") {
		t.Fatalf("the hit did not attach: %s", res)
	}
	if suffRows() != 0 || !strings.Contains(res, "suffocating") {
		t.Fatalf("a cover that did not fire started suffocation or hid the sentence: rows=%d %s", suffRows(), res)
	}
	// ending that attach mentions no suffocation (none began)
	c, j = pgCall(t, "POST", "/api/gm/detach", gm, map[string]interface{}{"campaign_id": camp, "attacher_id": darkID, "target_id": hero})
	if c != 200 || strings.Contains(fmt.Sprint(j["result"]), "breathe") {
		t.Fatalf("the detach of an unfired cover spoke of breathing: %d %v", c, j)
	}

	// Advantage (the target is Prone and the attack is in reach): the cover fires - Blinded, the
	// suffocation state - and the enforced sentence leaves the GM residual
	pgCall(t, "POST", "/api/gm/conditions", gm, map[string]interface{}{"campaign_id": camp, "target_id": hero, "condition": "prone", "source": "the test"})
	res = hit()
	if suffRows() != 1 || gained() != 0 {
		t.Fatalf("the cover did not start the suffocation: rows=%d gained=%d %s", suffRows(), gained(), res)
	}
	if strings.Contains(res, "suffocating") {
		t.Fatalf("the enforced suffocating sentence stayed in the residual: %s", res)
	}

	// the suffocating creature's own turn ends tick it: two of the hero's turn ends, two levels, counted
	for want := 1; want <= 2; want++ {
		for steps := 0; steps < 8; steps++ {
			before := exhaustionLevelOf(hero)
			cur := step()
			if strings.Contains(cur, "Test Suff Darkmantle") && exhaustionLevelOf(hero) == before+1 {
				goto ticked
			}
		}
		t.Fatalf("level %d never ticked (exhaustion %d)", want, exhaustionLevelOf(hero))
	ticked:
		if exhaustionLevelOf(hero) != want || gained() != want {
			t.Fatalf("the tick did not land once: exhaustion=%d gained=%d want=%d", exhaustionLevelOf(hero), gained(), want)
		}
	}

	// the detach: the attach, its Blinded and its suffocation end; exactly the two levels this suffocation
	// gave come off
	c, j = pgCall(t, "POST", "/api/gm/detach", gm, map[string]interface{}{"campaign_id": camp, "attacher_id": darkID, "target_id": hero})
	if c != 200 || suffRows() != 0 {
		t.Fatalf("the detach did not end the suffocation: %d %v rows=%d", c, j, suffRows())
	}
	if r := fmt.Sprint(j["result"]); !strings.Contains(r, "can breathe again") || !strings.Contains(r, "removes the 2 Exhaustion level(s)") {
		t.Fatalf("the detach did not say the breathing and the levels: %v", j)
	}
	if exhaustionLevelOf(hero) != 0 {
		t.Fatalf("the levels this suffocation gave were not removed: %d", exhaustionLevelOf(hero))
	}

	// a monster target: the same mechanical lifecycle - the tick lands the level in the cross-target
	// store, the d20 penalty shows on its own attack roll, the cleanup removes the level, immunity is
	// honored, and level 6 kills (white-box where the route cannot reach)
	if _, err := db.Exec(`INSERT INTO combat_effects (lobby_id, target_id, source_id, kind, text) VALUES ($1,$2,$3,'suffocating',$4)`,
		camp, darkID, darkID-500000, "Test Suff Darkmantle's Crush | gained: 0"); err != nil {
		t.Fatal(err)
	}
	if pub := suffocationTurnEnd(camp, darkID, "Test Suff Darkmantle"); len(pub) != 1 || exhaustionLevelOfAny(camp, darkID) != 1 {
		t.Fatalf("a monster's tick did not land its level: %v level=%d", pub, exhaustionLevelOfAny(camp, darkID))
	}
	if res := hit(); !strings.Contains(res, "Exhaustion -2") { // its own attack roll carries the penalty
		t.Fatalf("the exhausted monster's attack roll carried no Exhaustion penalty: %s", res)
	}
	c, _ = pgCall(t, "POST", "/api/gm/detach", gm, map[string]interface{}{"campaign_id": camp, "attacher_id": darkID, "target_id": hero}) // the re-attach from that hit
	if c != 200 {
		t.Fatalf("the cleanup detach failed: %d", c)
	}
	note, err := endAttachSuffocation(camp, darkID-500000, darkID, "the darkmantle attaches to the target | suffocates")
	if err != nil {
		t.Fatalf("the monster's cleanup: %v", err)
	}
	if !strings.Contains(note, "removes the 1 Exhaustion level(s)") || exhaustionLevelOfAny(camp, darkID) != 0 {
		t.Fatalf("a monster's cleanup did not remove the level mechanically: %q level=%d", note, exhaustionLevelOfAny(camp, darkID))
	}
	var left int
	if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind='suffocating' AND target_id=$2`, camp, darkID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("the monster suffocation row outlived its cleanup: %d %v", left, err)
	}

	// its saving throw carries the same penalty (white-box: no fixture action forces a monster's save)
	if note := addExhaustionAny(camp, darkID, 1, "the test"); !strings.HasPrefix(note, "Exhaustion level 1") {
		t.Fatalf("the save-setup gain was refused: %q", note)
	}
	sv := (&saveCtx{lobby: camp, saver: darkID, ability: "STR", dc: 13}).resist(saveStakes{})
	sawPenalty := false
	for _, a := range sv.Save.Adjust {
		if a.Why == "Exhaustion -2" {
			sawPenalty = true
		}
	}
	if !sawPenalty {
		t.Fatalf("the exhausted monster's save carried no Exhaustion adjustment: %+v", sv.Save.Adjust)
	}
	if note := addExhaustionAny(camp, darkID, -1, "the test"); exhaustionLevelOfAny(camp, darkID) != 0 {
		t.Fatalf("the save-setup removal did not land: %q", note)
	}

	// immunity (its stat block): no level, and level 6 kills through the HP owner
	if note := addExhaustionAny(camp, darkID, 1, "the test"); !strings.HasPrefix(note, "Exhaustion level 1") {
		t.Fatalf("the plain gain was refused: %q", note)
	}
	if note := addExhaustionAny(camp, darkID, -1, "the test"); exhaustionLevelOfAny(camp, darkID) != 0 {
		t.Fatalf("the removal did not land: %q level=%d", note, exhaustionLevelOfAny(camp, darkID))
	}
	if note := addExhaustionAny(camp, darkID, 6, "the test"); !strings.Contains(note, "dies") || monsterAlive(camp, darkID) {
		t.Fatalf("level 6 did not kill the monster: %q", note)
	}
}

// The serialization boundary (beginAttachWrite): an attach-row write - the onset, a detach - blocks while
// a battle-map transaction holds the encounter's combat_state lock, and lands after it commits, so a
// committed map always reflects a complete attach set. Simulated with a raw FOR UPDATE lock standing in
// for the map transaction; the goroutine's completion channel makes the interleaving deterministic.
func TestPostgresAttachWriteSerialization(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "serial gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Attach write serialization", "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	_, player := pgAgent(t, "serial player")
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Serial Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var goblin srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Goblin Warrior" {
			goblin = m
		}
	}
	m := goblin
	m.Name = "Serial Lurker"
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variant for the serialization test", "monsters": []srdMonster2024{m}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Serial Lurker", "monster_key": key, "initiative": 1}}})
	ids := monsterIDsByKeyOrName(camp, "Serial Lurker")
	if len(ids) != 1 {
		t.Fatalf("monster id: %v", ids)
	}
	id := ids[0]

	lockMap := func(t *testing.T) *sql.Tx {
		t.Helper()
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		var one int
		if err := tx.QueryRow(`SELECT 1 FROM combat_state WHERE lobby_id=$1 AND active FOR UPDATE`, camp).Scan(&one); err != nil {
			t.Fatalf("map lock: %v", err)
		}
		return tx
	}

	// Onset during an in-flight map transaction: addAttach blocks on the shared lock, lands after.
	mapTx := lockMap(t)
	done := make(chan string, 1)
	go func() {
		done <- addAttach(camp, id, char, "Hit: 1 (1d1) Bludgeoning damage, and the lurker attaches to the target.", "Serial test", false)
	}()
	select {
	case r := <-done:
		t.Fatalf("the onset committed while the map transaction held the lock: %q", r)
	case <-time.After(400 * time.Millisecond):
	}
	if err := mapTx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if strings.Contains(r, "could not be recorded") {
			t.Fatalf("the onset failed after the lock released: %q", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the onset never landed after the map transaction committed")
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind='attached' AND source_id=$2`, camp, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("attach row after the serialized onset: %d %v", n, err)
	}

	// A detach during an in-flight map transaction blocks the same way, then lands.
	mapTx = lockMap(t)
	detached := make(chan error, 1)
	go func() {
		tx, err := beginAttachWrite(camp)
		if err == nil {
			_, err = tx.Exec(`DELETE FROM combat_effects WHERE lobby_id=$1 AND kind='attached' AND source_id=$2`, camp, id)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		detached <- err
	}()
	select {
	case err := <-detached:
		t.Fatalf("the detach committed while the map transaction held the lock: %v", err)
	case <-time.After(400 * time.Millisecond):
	}
	if err := mapTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-detached; err != nil {
		t.Fatalf("the detach failed after the lock released: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind='attached' AND source_id=$2`, camp, id).Scan(&n); err != nil || n != 0 {
		t.Fatalf("attach row after the serialized detach: %d %v", n, err)
	}
}

// A crash between the detach commit and the ending effects must not lose them: the pending row the
// transaction recorded is drained (turn start or the handler's fast path) and the endings complete;
// a second drain is a no-op.
func TestPostgresAttachEndingDurability(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "durability gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Attach ending durability", "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	_, player := pgAgent(t, "durability player")
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Durability Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var darkmantle srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Darkmantle" {
			darkmantle = m
		}
	}
	m := darkmantle
	m.Name = "Durability Lurker"
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Darkmantle variant for the durability test", "monsters": []srdMonster2024{m}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Durability Lurker", "monster_key": key, "initiative": 1}}})
	ids := monsterIDsByKeyOrName(camp, "Durability Lurker")
	if len(ids) != 1 {
		t.Fatalf("monster id: %v", ids)
	}
	mid := ids[0]

	attachText := "Hit: 5 (1d6 + 3) Bludgeoning damage. If the target is a Medium or smaller creature, it has the Grappled condition (escape DC 13). The Durability Lurker attaches to the target. While the Durability Lurker is attached, the target has the Blinded condition, is suffocating, and the Durability Lurker has Advantage on attack rolls against it. The Durability Lurker can attack only the target while attached."
	attachSource := "Durability Lurker's Crush"
	if r := addAttach(camp, mid, char, attachText, attachSource, true); strings.Contains(r, "could not be recorded") {
		t.Fatalf("the onset: %q", r)
	}
	// The record the attach's onset wrote, as the attack path writes it.
	addCondition(camp, char, condAdd{Condition: "blinded", SourceID: mid, Source: attachSource})
	var rowID int
	var rowText string
	if err := db.QueryRow(`SELECT id, text FROM combat_effects WHERE lobby_id=$1 AND kind='attached' AND source_id=$2`, camp, mid).Scan(&rowID, &rowText); err != nil {
		t.Fatalf("the attach row: %v", err)
	}
	var blinded int
	if err := db.QueryRow(`SELECT count(*) FROM condition_records WHERE lobby_id=$1 AND target_id=$2 AND condition='blinded'`, camp, char).Scan(&blinded); err != nil || blinded != 1 {
		t.Fatalf("the owned Blinded before the crash: %d %v", blinded, err)
	}

	// The simulated crash: the detach's transaction committed (the attach row went and the pending
	// ending was recorded with it) and the process died before the ending effects ran.
	if _, err := db.Exec(`DELETE FROM combat_effects WHERE id=$1`, rowID); err != nil {
		t.Fatalf("delete the attach row (the committed detach): %v", err)
	}
	if _, err := db.Exec(`INSERT INTO pending_attach_endings (lobby_id, attacher_id, target_id, row_text) VALUES ($1,$2,$3,$4)`, camp, mid, char, rowText); err != nil {
		t.Fatalf("the recorded pending ending: %v", err)
	}
	// Before the drain the effects the crash left are still on.
	if err := db.QueryRow(`SELECT count(*) FROM condition_records WHERE lobby_id=$1 AND target_id=$2 AND condition='blinded'`, camp, char).Scan(&blinded); err != nil || blinded != 1 {
		t.Fatalf("the Blinded the crash left should still be present: %d %v", blinded, err)
	}
	var suffocating int
	if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind='suffocating' AND target_id=$2`, camp, char).Scan(&suffocating); err != nil || suffocating != 1 {
		t.Fatalf("the suffocating row the crash left should still be present: %d %v", suffocating, err)
	}

	// The drain completes the recorded endings.
	notes := drainAttachEndings(camp)
	if len(notes) == 0 {
		t.Fatal("the drain should report the caught-up ending")
	}
	if err := db.QueryRow(`SELECT count(*) FROM condition_records WHERE lobby_id=$1 AND target_id=$2 AND condition='blinded'`, camp, char).Scan(&blinded); err != nil || blinded != 0 {
		t.Errorf("the drain should end the attach-owned Blinded: %d %v", blinded, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind='suffocating' AND target_id=$2`, camp, char).Scan(&suffocating); err != nil || suffocating != 0 {
		t.Errorf("the drain should end the suffocating row: %d %v", suffocating, err)
	}
	var pending int
	if err := db.QueryRow(`SELECT count(*) FROM pending_attach_endings WHERE lobby_id=$1`, camp).Scan(&pending); err != nil || pending != 0 {
		t.Errorf("the drain should clear its pending row: %d %v", pending, err)
	}

	// A second drain is a no-op: nothing to do, nothing changes.
	if notes = drainAttachEndings(camp); len(notes) != 0 {
		t.Errorf("a repeat drain should have nothing left, got %v", notes)
	}

	// The deeper crash: the suffocating row's DELETE committed but the Exhaustion removal never ran.
	// The gained count the detach recorded in its pending row still says what to shed.
	if r := addAttach(camp, mid, char, attachText, attachSource, true); strings.Contains(r, "could not be recorded") {
		t.Fatalf("the re-onset: %q", r)
	}
	addCondition(camp, char, condAdd{Condition: "blinded", SourceID: mid, Source: attachSource})
	addExhaustionAny(camp, char, 2, attachSource)            // two suffocation ticks landed
	addExhaustionAny(camp, char, 1, "a night without sleep") // one level the suffocation never gave
	if _, err := db.Exec(`UPDATE combat_effects SET text=$1 WHERE lobby_id=$2 AND kind='suffocating' AND source_id=$3 AND target_id=$4`, attachSource+" | gained: 2", camp, mid, char); err != nil {
		t.Fatalf("the two ticks on the row: %v", err)
	}
	if lvl := exhaustionLevelOfAny(camp, char); lvl != 3 {
		t.Fatalf("two ticks plus one independent level = 3, found %d", lvl)
	}
	// The detach committed (attach row + pending ending with the recorded count) and the crash took
	// the suffocating row's delete but not the Exhaustion removal.
	if _, err := db.Exec(`DELETE FROM combat_effects WHERE lobby_id=$1 AND kind='attached' AND source_id=$2`, camp, mid); err != nil {
		t.Fatalf("the committed detach: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM combat_effects WHERE lobby_id=$1 AND kind='suffocating' AND source_id=$2`, camp, mid); err != nil {
		t.Fatalf("the crash's half-finished ending: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO pending_attach_endings (lobby_id, attacher_id, target_id, row_text, suffocation_gained) VALUES ($1,$2,$3,$4,$5)`, camp, mid, char, rowText, 2); err != nil {
		t.Fatalf("the pending row with its recorded count: %v", err)
	}
	notes = drainAttachEndings(camp)
	if lvl := exhaustionLevelOfAny(camp, char); lvl != 1 {
		t.Errorf("the drain should shed exactly the recorded 2 and keep the independent level, level=%d (notes %v)", lvl, notes)
	}
	if err := db.QueryRow(`SELECT count(*) FROM pending_attach_endings WHERE lobby_id=$1`, camp).Scan(&pending); err != nil || pending != 0 {
		t.Errorf("the acknowledgment should commit WITH the removal, pending rows left: %d %v", pending, err)
	}
	// The complementary window, closed structurally: the acknowledgment shares the removal's
	// transaction, so a retry after a crash finds no pending row and no count - and removes nothing.
	notes = drainAttachEndings(camp)
	if lvl := exhaustionLevelOfAny(camp, char); lvl != 1 || len(notes) != 0 {
		t.Errorf("a repeat drain must not remove levels twice, level=%d notes=%v", lvl, notes)
	}
	// Direct evidence: one ending call with a pending row removes the levels AND the row together.
	startSuffocation(camp, mid, char, attachSource)
	addExhaustionAny(camp, char, 1, attachSource)
	if _, err := db.Exec(`UPDATE combat_effects SET text=$1 WHERE lobby_id=$2 AND kind='suffocating' AND source_id=$3 AND target_id=$4`, attachSource+" | gained: 1", camp, mid, char); err != nil {
		t.Fatalf("the tick on the row: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO pending_attach_endings (lobby_id, attacher_id, target_id, row_text, suffocation_gained) VALUES ($1,$2,$3,$4,$5)`, camp, mid, char, rowText, 1); err != nil {
		t.Fatalf("the pending row: %v", err)
	}
	if _, err := endAttachSuffocation(camp, mid, char, rowText); err != nil {
		t.Fatalf("the atomic ending: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM pending_attach_endings WHERE lobby_id=$1`, camp).Scan(&pending); err != nil || pending != 0 {
		t.Errorf("the ending's transaction should carry its acknowledgment: %d %v", pending, err)
	}
	if lvl := exhaustionLevelOfAny(camp, char); lvl != 1 {
		t.Errorf("exactly the one recorded level goes, the independent one stays: level=%d", lvl)
	}
}
