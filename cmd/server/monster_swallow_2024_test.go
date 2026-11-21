package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The published Swallow lifecycle (SRD 5.2.1 Giant Frog): the action swallows a Small-or-smaller
// target the frog is grappling; the grapple ends and the swallowed target has the Blinded and
// Restrained conditions; while swallowing, the frog can't use Bite; at the end of the frog's next
// turn the swallowed target takes the listed Acid damage, and if it survives the frog disgorges it
// and it exits Prone.
func TestPostgresSwallowLifecycle(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "swallow gm")
	_, p1 := pgAgent(t, "swallow p1")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Swallow lifecycle", "max_players": 2})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", p1, pgRogue(fmt.Sprintf("Swallow Hero %d", time.Now().UnixNano()%1e9)))
	hero := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", p1, map[string]interface{}{"character_id": hero})
	pgFinish(t, p1, hero)
	pgCall(t, "POST", cp+"/ready", p1, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,ac=1,species='Halfling' WHERE id=$1`, hero)

	var goblin srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Goblin Warrior" {
			goblin = m
		}
	}
	frog := goblin
	frog.Name = "Test Swallow Frog"
	frog.Actions = []srdMonsterEntry{
		{Name: "Bite", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 5 (1d6 + 2) Piercing damage. If the target is a Medium or smaller creature, it has the Grappled condition (escape DC 11)."},
		{Name: "Swallow", Text: "The frog swallows a Small or smaller target it is grappling. While swallowed, the target isn't Grappled but has the Blinded and Restrained conditions, and it has Total Cover against attacks and other effects outside the frog. While swallowing the target, the frog can't use Bite, and if the frog dies, the swallowed target is no longer Restrained and can escape from the corpse using 5 feet of movement, exiting with the Prone condition.\nAt the end of the frog's next turn, the swallowed target takes 5 (2d4) Acid damage. If that damage doesn't kill it, the frog disgorges it, causing it to exit Prone."},
	}
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Giant Frog variant for the swallow lifecycle tests", "monsters": []srdMonster2024{frog}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Test Swallow Frog", "monster_key": key, "initiative": 20},
	}})
	ids := monsterIDsByKeyOrName(camp, "Test Swallow Frog")
	if len(ids) != 1 {
		t.Fatalf("combatants: %v", ids)
	}
	frogID := ids[0]

	swallowed := func() int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind='swallowed'`, camp).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	grappled := func() bool { return hasCondMarker(camp, hero, "grappled") }
	blinded := func() bool { return hasCondMarker(camp, hero, "blinded") }
	restrained := func() bool { return hasCondMarker(camp, hero, "restrained") }
	prone := func() bool { return hasCondMarker(camp, hero, "prone") }
	hp := func() int {
		var n int
		if err := db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, hero).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	narrate := func(action string, target int) (int, map[string]interface{}, string) {
		c, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", frogID), "action": action, "target": fmt.Sprint(target)}})
		return c, r, fmt.Sprint(r["monster_action_result"])
	}

	// Leg 1: no grapple - the swallow refuses atomically.
	hp0 := hp()
	c, j, line := narrate("Swallow", hero)
	if c == 200 && swallowed() != 0 {
		t.Fatalf("swallowed without a grapple: %s", line)
	}
	if swallowed() != 0 || hp() != hp0 {
		t.Fatalf("the refused swallow changed state: rows=%d hp %d->%d", swallowed(), hp0, hp())
	}

	// Leg 2: the Bite's grapple lands (Small hero, Medium-or-smaller gate holds).
	for try := 0; try < 30 && !grappled(); try++ {
		narrate("Bite", hero)
	}
	if !grappled() {
		t.Fatal("the bite never grappled")
	}

	// Leg 3: the swallow ends the grapple and owns Blinded + Restrained.
	c, j, line = narrate("Swallow", hero)
	if swallowed() != 1 {
		t.Fatalf("the swallow recorded no row: %d %s", c, line)
	}
	if grappled() {
		t.Fatal("the swallowed target is still grappled")
	}
	if !blinded() || !restrained() {
		t.Fatalf("the swallowed target lacks its conditions: blinded=%v restrained=%v", blinded(), restrained())
	}

	// Leg 4: the Bite lockout refuses while swallowing; nothing rolls and nothing changes.
	hp0 = hp()
	c, j, line = narrate("Bite", hero)
	if !strings.Contains(line, "can't use Bite") || swallowed() != 1 || hp() != hp0 {
		t.Fatalf("the locked Bite was not refused atomically: %s (rows=%d)", line, swallowed())
	}

	// Leg 5: at the end of the frog's NEXT turn the acid lands; the survivor is disgorged Prone.
	hp0 = hp()
	for n := 0; currentTurnID(camp) != frogID; n++ { // reach the frog's current turn, then pass it
		if n == 15 {
			t.Fatal("the frog did not get its turn")
		}
		pgCall(t, "POST", cp+"/combat/next", gm, nil)
	}
	for n := 0; currentTurnID(camp) != frogID; n++ { // the frog's NEXT turn
		if n == 15 {
			t.Fatal("the frog did not get its next turn")
		}
		pgCall(t, "POST", cp+"/combat/next", gm, nil)
	}
	pgCall(t, "POST", cp+"/combat/next", gm, nil) // end the frog's next turn
	if d := hp0 - hp(); d < 2 || d > 8 {
		t.Fatalf("the end-of-next-turn acid did not land 2d4: %d", d)
	}
	if swallowed() != 0 || blinded() || restrained() {
		t.Fatalf("the survivor was not disgorged: rows=%d blinded=%v restrained=%v", swallowed(), blinded(), restrained())
	}
	if !prone() {
		t.Fatal("the disgorged target did not exit Prone")
	}
}
