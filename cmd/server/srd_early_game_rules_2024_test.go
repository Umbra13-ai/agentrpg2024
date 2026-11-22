package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Rules the early-game set (CR 0 to 3) brought to the shared owners, with invented monsters in the SRD 5.2.1 wordings:
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

// A hit that reduces its target to 0 Hit Points ("If this damage reduces the target to 0 Hit Points, the target becomes Stable, and it has
// the Poisoned condition for 1 hour. While Poisoned, the target also has the Paralyzed condition.": Phase Spider Bite): nothing on a hit
// that leaves the target standing; Stable, Poisoned for 1 hour and Paralyzed while Poisoned when the hit drops it.
func TestPostgresHitThatDropsToZero(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, spider := earlyRulesFixture(t, "Test Drop Spider", []srdMonsterEntry{
		{Name: "Bite", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 7 (1d10 + 2) Piercing damage plus 9 (2d8) Poison damage. If this damage reduces the target to 0 Hit Points, the target becomes Stable, and it has the Poisoned condition for 1 hour. While Poisoned, the target also has the Paralyzed condition."},
		{Name: "Gnaw", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 7 (2d6) Piercing damage. The target dies if it is reduced to 0 Hit Points by this attack."},
	})
	bite := func() string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", spider), "action": "Bite", "target": fmt.Sprint(hero)}})
		return fmt.Sprint(r["monster_action_result"])
	}
	line := bite() // 1000 Hit Points: standing
	if !strings.Contains(line, "HIT") || hasCondMarker(camp, hero, "poisoned") || hasCondMarker(camp, hero, "paralyzed") {
		t.Fatalf("a hit that leaves the target standing gave the 0 Hit Points conditions: %s", line)
	}
	pgExec(t, `UPDATE characters SET hp=1 WHERE id=$1`, hero)
	line = bite()
	var hp int
	var stable bool
	db.QueryRow(`SELECT hp, COALESCE(is_stable,false) FROM characters WHERE id=$1`, hero).Scan(&hp, &stable)
	if hp != 0 || !stable || !hasCondMarker(camp, hero, "poisoned") || !hasCondMarker(camp, hero, "paralyzed") {
		t.Fatalf("the hit that dropped it: hp %d stable %v poisoned %v paralyzed %v: %s", hp, stable, hasCondMarker(camp, hero, "poisoned"), hasCondMarker(camp, hero, "paralyzed"), line)
	}
	if rs := recordsOf(camp, hero, "poisoned"); len(rs) != 1 || rs[0].DurationRounds != 600 {
		t.Errorf("Poisoned for 1 hour (600 rounds): %+v", rs)
	}
	endConditionRecords(recordsOf(camp, hero, "poisoned"))
	if hasCondMarker(camp, hero, "paralyzed") {
		t.Error("Paralyzed outlived the Poisoned record")
	}
	// a hit on a character already at 0 (a Death Saving Throw failure) is not a hit that reduced it to 0
	endConditionRecords(recordsOf(camp, hero, "paralyzed"))
	pgExec(t, `UPDATE characters SET is_stable=false WHERE id=$1`, hero)
	bite()
	if hasCondMarker(camp, hero, "poisoned") {
		t.Error("a hit on a character already at 0 Hit Points gave the conditions again")
	}
	// "The target dies if it is reduced to 0 Hit Points by this attack" (Gibbering Mouther): standing, nothing; dropped, dead
	gnaw := func() string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", spider), "action": "Gnaw", "target": fmt.Sprint(hero)}})
		return fmt.Sprint(r["monster_action_result"])
	}
	dead := func() bool {
		var d bool
		db.QueryRow(`SELECT COALESCE(is_dead,false) FROM characters WHERE id=$1`, hero).Scan(&d)
		return d
	}
	pgExec(t, `UPDATE characters SET hp=1000,death_save_failures=0,death_save_successes=0,is_stable=false WHERE id=$1`, hero)
	if line := gnaw(); dead() {
		t.Fatalf("a standing target died: %s", line)
	}
	pgExec(t, `UPDATE characters SET hp=1 WHERE id=$1`, hero)
	if line := gnaw(); !dead() || !strings.Contains(line, "dies") {
		t.Fatalf("the hit that dropped it should kill it: %s", line)
	}
}

// What a single save target must be, read from the entry's header (monsterSaveTargetIssue): "one Frightened creature" (Sea Hag Death
// Glare) and "one living creature ... that has 0 Hit Points" (Will-o'-Wisp Consume Life); a target that isn't is refused before
// anything is rolled or spent. Death Glare's Hit Point threshold (dropsToZero) and Consume Life's death and regain (killOnFailure).
// "ends early immediately after the wisp ... uses Consume Life" (Vanish) never calls Consume Life (usesAnotherEntry).
func TestPostgresSaveTargetGatesThresholdAndDeath(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, hag := earlyRulesFixture(t, "Test Gate Hag", []srdMonsterEntry{
		{Name: "Death Glare", Text: "Wisdom Saving Throw: DC 30, one Frightened creature the hag can see within 30 feet. Failure: If the target has 20 Hit Points or fewer, it drops to 0 Hit Points. Otherwise, the target takes 13 (3d8) Psychic damage."},
		{Name: "Consume Life", Text: "Constitution Saving Throw: DC 30, one living creature the wisp can see within 5 feet that has 0 Hit Points. Failure: The target dies, and the wisp regains 10 (3d6) Hit Points."},
		{Name: "Vanish", Text: "The wisp and its light have the Invisible condition until the wisp’s Concentration ends on this effect, which ends early immediately after the wisp makes an attack roll or uses Consume Life."},
	})
	act := func(action string) (int, string) {
		c, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", hag), "action": action, "target": fmt.Sprint(hero), "confirm_targeting": true}})
		return c, fmt.Sprint(r["monster_action_result"])
	}
	hp := func() int {
		var n int
		db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, hero).Scan(&n)
		return n
	}
	if c, line := act("Death Glare"); !strings.Contains(line, "doesn't have the Frightened condition") || strings.Contains(line, "saving throw (") || hp() != 1000 {
		t.Fatalf("Death Glare at a target that isn't Frightened: %d %s", c, line)
	}
	addCondition(camp, hero, condAdd{Condition: "frightened", Source: "test"})
	if _, line := act("Death Glare"); hp() >= 1000 || hp() < 1000-24 || strings.Contains(line, "drops to 0") {
		t.Fatalf("Death Glare at 1000 Hit Points should deal 3d8: hp %d: %s", hp(), line)
	}
	pgExec(t, `UPDATE characters SET hp=20 WHERE id=$1`, hero)
	if _, line := act("Death Glare"); hp() != 0 || !strings.Contains(line, "drops to 0 Hit Points") {
		t.Fatalf("Death Glare at 20 Hit Points should drop it to 0: hp %d: %s", hp(), line)
	}
	if _, line := act("Vanish"); strings.Contains(line, "saving throw") || strings.Contains(line, "dies") {
		t.Fatalf("Vanish called Consume Life: %s", line)
	}
	pgExec(t, `UPDATE characters SET hp=5 WHERE id=$1`, hero)
	if c, line := act("Consume Life"); !strings.Contains(line, "targets only a living creature that has 0 Hit Points") || strings.Contains(line, "saving throw (") {
		t.Fatalf("Consume Life at a target with Hit Points: %d %s", c, line)
	}
	pgExec(t, `UPDATE characters SET hp=0,is_dead=false WHERE id=$1`, hero)
	setMonsterHP(camp, hag, 100)
	_, line := act("Consume Life")
	var dead bool
	db.QueryRow(`SELECT COALESCE(is_dead,false) FROM characters WHERE id=$1`, hero).Scan(&dead)
	if !dead || !strings.Contains(line, "dies") || monsterEntryVitals(camp, hag).HP <= 100 {
		t.Fatalf("Consume Life at a dying target: dead %v, hag at %d: %s", dead, monsterEntryVitals(camp, hag).HP, line)
	}
	if c, line := act("Consume Life"); !strings.Contains(line, "isn't living") {
		t.Errorf("Consume Life at the dead: %d %s", c, line)
	}
}
