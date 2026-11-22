package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Rules the early-game set (CR 0 to 1) brought to the shared owners, with invented monsters in the SRD 5.2.1 wordings:
//   - the swallow owner reads the Giant Toad's wording as well as the Giant Frog's: "the target takes 10 (3d6) Acid damage at the end of
//     each of the toad's turns" (every turn end, from the turn of the swallow; no disgorging) and "it can't use Bite while it has a
//     swallowed target" (the lockout);
//   - the lasting-effects owner reads "While Deafened, the target also has Disadvantage on ability checks and attack rolls" (Swarm of
//     Ravens Cacophony): the attack rolls are the roll rider's marker, ended with the Deafened record; the ability checks stay the GM's.

func earlyRulesFixture(t *testing.T, name string, actions []srdMonsterEntry) (gm, player string, camp, hero, id int) {
	t.Helper()
	_, gm = pgAgent(t, "early rules gm")
	_, player = pgAgent(t, "early rules player")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Early rules", "max_players": 2})
	camp = int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Early Rules Hero %d", time.Now().UnixNano()%1e9)))
	hero = int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": hero})
	pgFinish(t, player, hero)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,ac=1,wis=1 WHERE id=$1`, hero)
	m := inventedMonster(name, "Beast", nil, actions)
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented SRD-wording fixture", "monsters": []srdMonster2024{m}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	t.Cleanup(func() { pgCall(t, "POST", cp+"/combat/end", gm, nil) })
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": name, "monster_key": key, "hp": 500, "ac": 1, "initiative": 20}}})
	id = monsterIDsByKeyOrName(camp, name)[0]
	return
}

func TestPostgresSwallowAtEachTurnEnd(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, toad := earlyRulesFixture(t, "Test Swallow Toad", []srdMonsterEntry{
		{Name: "Bite", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 5 (1d6 + 2) Piercing damage plus 5 (2d4) Poison damage. If the target is a Medium or smaller creature, it has the Grappled condition (escape DC 12)."},
		{Name: "Swallow", Text: "The toad swallows a Medium or smaller target it is grappling. While swallowed, the target isn’t Grappled but has the Blinded and Restrained conditions, and it has Total Cover against attacks and other effects outside the toad. In addition, the target takes 10 (3d6) Acid damage at the end of each of the toad’s turns. The toad can have only one target swallowed at a time, and it can’t use Bite while it has a swallowed target. If the toad dies, a swallowed creature is no longer Restrained and can escape from the corpse using 5 feet of movement, exiting with the Prone condition."},
	})
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	narrate := func(action string) string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", toad), "action": action, "target": fmt.Sprint(hero)}})
		return fmt.Sprint(r["monster_action_result"])
	}
	swallowed := func() int {
		var n int
		db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind='swallowed'`, camp).Scan(&n)
		return n
	}
	hp := func() int {
		var n int
		db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, hero).Scan(&n)
		return n
	}
	for n := 0; currentTurnID(camp) != toad; n++ { // the swallow on the toad's own turn
		if n == 15 {
			t.Fatal("the toad did not get its turn")
		}
		pgCall(t, "POST", cp+"/combat/next", gm, nil)
	}
	for try := 0; try < 30 && !hasCondMarker(camp, hero, "grappled"); try++ {
		narrate("Bite")
	}
	if !hasCondMarker(camp, hero, "grappled") {
		t.Fatal("the bite never grappled")
	}
	if line := narrate("Swallow"); swallowed() != 1 || !strings.Contains(line, "At the end of each of") {
		t.Fatalf("the swallow: %s (rows %d)", line, swallowed())
	}
	if hasCondMarker(camp, hero, "grappled") || !hasCondMarker(camp, hero, "blinded") || !hasCondMarker(camp, hero, "restrained") {
		t.Fatal("the swallowed target's conditions are wrong")
	}
	hp0 := hp()
	if line := narrate("Bite"); !strings.Contains(line, "can't use Bite") || hp() != hp0 {
		t.Fatalf("the Bite while swallowing: %s", line)
	}
	// the end of this turn (one of "each of the toad's turns") and of the next: 3d6 each time, still swallowed, never disgorged
	for turn := 0; turn < 2; turn++ {
		hp0 = hp()
		pgCall(t, "POST", cp+"/combat/next", gm, nil) // end the toad's turn
		if d := hp0 - hp(); d < 3 || d > 18 {
			t.Fatalf("turn end %d: the acid did not land 3d6: %d", turn+1, d)
		}
		if swallowed() != 1 || !hasCondMarker(camp, hero, "restrained") || hasCondMarker(camp, hero, "prone") {
			t.Fatalf("turn end %d: the target left the toad (rows %d)", turn+1, swallowed())
		}
		for n := 0; turn == 0 && currentTurnID(camp) != toad; n++ {
			if n == 15 {
				t.Fatal("the toad did not get its next turn")
			}
			pgCall(t, "POST", cp+"/combat/next", gm, nil)
		}
	}
}

func TestPostgresDeafenedAttackDisadvantage(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, swarm := earlyRulesFixture(t, "Test Cacophony Swarm", []srdMonsterEntry{
		{Name: "Cacophony", Text: "Wisdom Saving Throw: DC 10, one creature in the swarm’s space. Failure: The target has the Deafened condition until the start of the swarm’s next turn. While Deafened, the target also has Disadvantage on ability checks and attack rolls."},
	})
	var line string
	for try := 0; try < 30 && !hasCondMarker(camp, hero, "deafened"); try++ {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", swarm), "action": "Cacophony", "target": fmt.Sprint(hero)}})
		line = fmt.Sprint(r["monster_action_result"])
	}
	if !hasCondMarker(camp, hero, "deafened") {
		t.Fatalf("the save never failed: %s", line)
	}
	if !hasCondMarker(camp, hero, "disadv_attacks") || !strings.Contains(line, "Disadvantage on attack rolls while Deafened") {
		t.Fatalf("Deafened without the attack rolls' Disadvantage: %s", line)
	}
	if !strings.Contains(line, "Disadvantage on ability checks") || strings.Contains(line, "ability checks and attack rolls") {
		t.Errorf("the GM should be told the ability checks only (the attack rolls are applied): %s", line)
	}
	ctx := attackCtx{lobby: camp, attacker: hero, target: swarm}
	if a := ctx.gather(); !strings.Contains(strings.Join(a.Disadvantage, "; "), "Disadvantage rider") {
		t.Errorf("the Deafened character's attack roll lacks the rider's Disadvantage: %v", a.Disadvantage)
	}
	endConditionRecords(recordsOf(camp, hero, "deafened")) // the condition owner's end: the marker ends with it
	if hasCondMarker(camp, hero, "disadv_attacks") {
		t.Error("the attack rolls' Disadvantage outlived the Deafened record")
	}
}
