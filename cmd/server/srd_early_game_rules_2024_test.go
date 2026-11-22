package main

import (
	"encoding/json"
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

func narrateAs(t *testing.T, gm string, camp, id int, action, target, use string) (int, string) {
	t.Helper()
	ma := map[string]interface{}{"monster": fmt.Sprintf("#%d", id), "action": action, "target": target, "confirm_targeting": true}
	if use != "" {
		ma["use"] = use
	}
	c, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": ma})
	return c, fmt.Sprint(r["monster_action_result"])
}

// A Hit that deals no damage and only gives conditions ("Hit: The target has the Grappled condition (escape DC 14) from one of six
// tentacles, and the target has the Poisoned condition until the grapple ends.": Roper Tentacle) is a valid attack: no Hit Point change,
// no Death Saving Throw failure, the grapple and its linked condition recorded from one source, the linked record ending with the grapple.
func TestPostgresNoDamageHit(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, roper := earlyRulesFixture(t, "Test Tentacle Roper", []srdMonsterEntry{
		{Name: "Tentacle", Text: "Melee Attack Roll: +60, reach 60 ft. Hit: The target has the Grappled condition (escape DC 14) from one of six tentacles, and the target has the Poisoned condition until the grapple ends."},
	})
	for _, hp := range []int{1000, 0} { // standing, and already at 0 (a damage-less hit is not a Death Saving Throw failure)
		endConditionRecords(recordsOf(camp, hero, "grappled"))
		endConditionRecords(recordsOf(camp, hero, "poisoned"))
		pgExec(t, `UPDATE characters SET hp=$1,death_save_failures=0,is_stable=false WHERE id=$2`, hp, hero)
		c, line := narrateAs(t, gm, camp, roper, "Tentacle", fmt.Sprint(hero), "")
		if c != 200 || !strings.Contains(line, "HIT") || strings.Contains(line, "damage") {
			t.Fatalf("hp %d: the damage-less hit: %d %s", hp, c, line)
		}
		var now, fails int
		db.QueryRow(`SELECT hp, COALESCE(death_save_failures,0) FROM characters WHERE id=$1`, hero).Scan(&now, &fails)
		if now != hp || fails != 0 {
			t.Errorf("hp %d: the hit changed Hit Points or the death saves: hp %d failures %d", hp, now, fails)
		}
		if len(recordsOf(camp, hero, "grappled")) != 1 || len(recordsOf(camp, hero, "poisoned")) != 1 {
			t.Fatalf("hp %d: want Grappled and the linked Poisoned: %s", hp, line)
		}
		endConditionRecords(recordsOf(camp, hero, "grappled"))
		if len(recordsOf(camp, hero, "poisoned")) != 0 {
			t.Errorf("hp %d: the linked Poisoned outlived the grapple", hp)
		}
	}
}

// "damage of the type chosen for the Draconic Origin trait" in a saving throw entry (Half-Dragon Dragon's Breath): refused before
// anything is rolled or spent until the GM names the type in use; then it is dealt as that type.
func TestPostgresChosenDamageTypeOnSave(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, drake := earlyRulesFixture(t, "Test Origin Drake", []srdMonsterEntry{
		{Name: "Dragon’s Breath (Recharge 5–6)", Text: "Dexterity Saving Throw: DC 30, each creature in a 30-foot Cone. Failure: 28 (8d6) damage of the type chosen for the Draconic Origin trait. Success: Half damage."},
	})
	pgExec(t, `UPDATE characters SET dex=1 WHERE id=$1`, hero)
	c, line := narrateAs(t, gm, camp, drake, "Dragon’s Breath", fmt.Sprint(hero), "")
	if !strings.Contains(line, "give a damage type in use") || strings.Contains(line, "saving throw (") {
		t.Fatalf("without a type: %d %s", c, line)
	}
	if st := loadMonsterState(camp, drake); len(st.RechargeSpent) != 0 {
		t.Errorf("a refused breath spent its recharge: %v", st.RechargeSpent)
	}
	var hp0 int
	db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, hero).Scan(&hp0)
	_, line = narrateAs(t, gm, camp, drake, "Dragon’s Breath", fmt.Sprint(hero), "acid")
	var hp1 int
	db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, hero).Scan(&hp1)
	if !strings.Contains(line, "acid damage") || hp1 >= hp0 {
		t.Fatalf("with a type: hp %d -> %d: %s", hp0, hp1, line)
	}
}

// "Disadvantage on the next attack roll it makes before the end of its next turn" (Ettin Morningstar): given by the hit, used up by
// the target's next attack roll, never the one after.
func TestPostgresNextAttackDisadvantage(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, ettin := earlyRulesFixture(t, "Test Dazing Ettin", []srdMonsterEntry{
		{Name: "Morningstar", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 13 (2d8 + 4) Piercing damage, and the target has Disadvantage on the next attack roll it makes before the end of its next turn."},
	})
	if _, line := narrateAs(t, gm, camp, ettin, "Morningstar", fmt.Sprint(hero), ""); !hasCondMarker(camp, hero, "disadv_next_attack") {
		t.Fatalf("the hit gave no marker: %s", line)
	}
	roll := func() []string { return (&attackCtx{lobby: camp, attacker: hero}).gather().Disadvantage }
	if d := roll(); !strings.Contains(strings.Join(d, ";"), "next attack roll") {
		t.Fatalf("the next attack roll has no Disadvantage: %v", d)
	}
	if hasCondMarker(camp, hero, "disadv_next_attack") {
		t.Error("the marker was not used up by the roll")
	}
	if d := roll(); strings.Contains(strings.Join(d, ";"), "next attack roll") {
		t.Errorf("the roll after it still has the Disadvantage: %v", d)
	}
}

// A monster's cast asks castDealsNoDamage as a character's does: Dream's damage comes later (when the target wakes), never at the cast.
func TestPostgresDreamDealsNoDamageOnCast(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, dreamer := earlyRulesFixture(t, "Test Dreamer", []srdMonsterEntry{
		{Name: "Nightmare Haunting", Text: "The dreamer casts Dream, using Wisdom as the spellcasting ability (spell save DC 30)."},
	})
	var hp0 int
	db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, hero).Scan(&hp0)
	_, line := narrateAs(t, gm, camp, dreamer, "Nightmare Haunting", fmt.Sprint(hero), "")
	var hp1 int
	db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, hero).Scan(&hp1)
	if hp1 != hp0 || strings.Contains(line, "psychic damage") {
		t.Fatalf("the cast of Dream dealt damage: hp %d -> %d: %s", hp0, hp1, line)
	}
}

// "casts X or Y on it" is a choice of spells the GM names in use (Unicorn's Blessing).
func TestCastChoiceOnIt(t *testing.T) {
	testPostgres(t)
	got := castChoiceList("The unicorn casts Cure Wounds or Lesser Restoration on it, using the same spellcasting ability as Spellcasting.")
	if len(got) != 2 || got[0] != "Cure Wounds" || got[1] != "Lesser Restoration" {
		t.Fatalf("castChoiceList: %v", got)
	}
}

// "and the pirate has Advantage on the next attack roll it makes before the end of this turn" (Pirate Captain Rapier): the hit's own attacker
// gets it (not the target), used up by its next attack roll, and gone when its turn ends.
func TestPostgresAttackerNextAttackAdvantage(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, pirate := earlyRulesFixture(t, "Test Duelist Pirate", []srdMonsterEntry{
		{Name: "Rapier", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 13 (2d8 + 4) Piercing damage, and the pirate has Advantage on the next attack roll it makes before the end of this turn."},
	})
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	for n := 0; currentTurnID(camp) != pirate; n++ { // its own turn
		if n == 15 {
			t.Fatal("the pirate did not get its turn")
		}
		pgCall(t, "POST", cp+"/combat/next", gm, nil)
	}
	if _, line := narrateAs(t, gm, camp, pirate, "Rapier", fmt.Sprint(hero), ""); !hasCondMarker(camp, pirate, "adv_next_attack") || hasCondMarker(camp, hero, "adv_next_attack") {
		t.Fatalf("the marker belongs on the attacker only: %s", line)
	}
	if a := (&attackCtx{lobby: camp, attacker: pirate, target: hero}).gather().Advantage; !strings.Contains(strings.Join(a, ";"), "next attack roll") {
		t.Fatalf("the next attack roll has no Advantage: %v", a)
	}
	if hasCondMarker(camp, pirate, "adv_next_attack") {
		t.Error("the marker was not used up by the roll")
	}
	narrateAs(t, gm, camp, pirate, "Rapier", fmt.Sprint(hero), "")
	pgCall(t, "POST", cp+"/combat/next", gm, nil) // the end of this turn
	if hasCondMarker(camp, pirate, "adv_next_attack") {
		t.Error("the marker outlived the turn it was given in")
	}
}

// "While Grappled, the target has the Restrained condition and can't be targeted by the crocodile's Tail." (Giant Crocodile Bite): the
// Tail is refused at the creature the crocodile has Grappled, in a Multiattack too, and free again when the grapple ends; the GM is not
// told a sentence the server applies.
func TestPostgresGrappledTargetBarsAnAttack(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, croc := earlyRulesFixture(t, "Test Lock Crocodile", []srdMonsterEntry{
		{Name: "Bite", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 21 (3d10 + 5) Piercing damage. If the target is a Large or smaller creature, it has the Grappled condition (escape DC 15). While Grappled, the target has the Restrained condition and can’t be targeted by the crocodile’s Tail."},
		{Name: "Tail", Text: "Melee Attack Roll: +60, reach 10 ft. Hit: 18 (3d8 + 5) Bludgeoning damage."},
		{Name: "Multiattack", Text: "The crocodile makes one Bite attack and one Tail attack."},
	})
	target := fmt.Sprint(hero)
	if _, line := narrateAs(t, gm, camp, croc, "Tail", target, ""); !strings.Contains(line, "HIT") {
		t.Fatalf("the Tail before any grapple: %s", line)
	}
	_, line := narrateAs(t, gm, camp, croc, "Bite", target, "")
	if len(recordsOf(camp, hero, "grappled")) != 1 || strings.Contains(line, "can’t be targeted") || strings.Contains(line, "can't be targeted") {
		t.Fatalf("the Bite: grappled %d; the GM was told the applied sentence: %s", len(recordsOf(camp, hero, "grappled")), line)
	}
	if _, line := narrateAs(t, gm, camp, croc, "Tail", target, ""); strings.Contains(line, "HIT") || !strings.Contains(line, "can't make Tail attacks") {
		t.Fatalf("the Tail at a grappled creature: %s", line)
	}
	endConditionRecords(recordsOf(camp, hero, "grappled"))
	endConditionRecords(recordsOf(camp, hero, "restrained"))
	if _, line := narrateAs(t, gm, camp, croc, "Tail", target, ""); !strings.Contains(line, "HIT") {
		t.Fatalf("the Tail after the grapple ended: %s", line)
	}
}

// An entry that pulls the creatures the monster holds (Roper Reel: "pulls each creature Grappled by it up to 30 feet straight toward it";
// Ettercap Reel: "pulls one creature within 30 feet of itself that is Restrained by its Web Strand up to 25 feet straight toward itself"):
// only held creatures move, through the forced movement owner (a pull stops next to the monster); without a map the GM places them.
func TestPostgresReelPullsHeldCreatures(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, roper := earlyRulesFixture(t, "Test Reel Roper", []srdMonsterEntry{
		{Name: "Tentacle", Text: "Melee Attack Roll: +60, reach 60 ft. Hit: The target has the Grappled condition (escape DC 14) from one of six tentacles, and the target has the Poisoned condition until the grapple ends."},
		{Name: "Reel", Text: "The roper pulls each creature Grappled by it up to 30 feet straight toward it."},
		{Name: "Lash", Text: "The roper pulls one creature within 30 feet of itself that is Restrained by its Web Strand up to 25 feet straight toward itself."},
	})
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	target := fmt.Sprint(hero)
	if _, line := narrateAs(t, gm, camp, roper, "Reel", target, ""); !strings.Contains(line, "holds no creature to pull") {
		t.Fatalf("Reel with nothing held: %s", line)
	}
	narrateAs(t, gm, camp, roper, "Tentacle", target, "")
	if len(recordsOf(camp, hero, "grappled")) != 1 {
		t.Fatal("the Tentacle did not grapple")
	}
	if _, line := narrateAs(t, gm, camp, roper, "Reel", target, ""); !strings.Contains(line, "no battle map: the GM places it") {
		t.Fatalf("Reel without a map: %s", line)
	}
	if _, line := narrateAs(t, gm, camp, roper, "Lash", target, ""); !strings.Contains(line, "isn't Restrained") {
		t.Fatalf("the Ettercap wording at a creature that is not Restrained by that effect: %s", line)
	}
	// with a map: the hero stands 7 squares (35 feet) away; the pull stops next to the roper
	pgCall(t, "PUT", cp+"/battle-map", gm, map[string]interface{}{"map": encounterMap{Grid: []string{"........"}, Tokens: []mapToken{
		{ID: roper, Name: "roper", Position: mapSquare{0, 0}, Size: 1}, {ID: hero, Name: "hero", Position: mapSquare{7, 0}, Size: 1}}}, "forced_movement": true})
	_, line := narrateAs(t, gm, camp, roper, "Reel", target, "")
	var raw []byte
	db.QueryRow(`SELECT battle_map FROM combat_state WHERE lobby_id=$1`, camp).Scan(&raw)
	var m encounterMap
	json.Unmarshal(raw, &m)
	x := -1
	for _, tk := range m.Tokens {
		if tk.ID == hero {
			x = tk.Position.X
		}
	}
	if x != 1 || !strings.Contains(line, "is pulled") {
		t.Fatalf("the pull should bring the hero from 7 to 1 (next to the roper): x=%d: %s", x, line)
	}
	// the Ettercap wording: Restrained by its Web Strand (a record this monster caused, its source naming the effect)
	addCondition(camp, hero, condAdd{Condition: "restrained", Source: "Test Reel Roper's Web Strand", SourceID: roper})
	_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
	req := map[string]interface{}{"map": encounterMap{Grid: []string{"........"}, Tokens: []mapToken{
		{ID: roper, Name: "roper", Position: mapSquare{0, 0}, Size: 1}, {ID: hero, Name: "hero", Position: mapSquare{6, 0}, Size: 1}}}, "forced_movement": true}
	if rev, ok := state["revision"].(string); ok {
		req["confirmation"] = rev
	}
	if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
		t.Fatalf("put map: %d %v", c, r)
	}
	if _, line := narrateAs(t, gm, camp, roper, "Lash", target, ""); !strings.Contains(line, "is pulled") {
		t.Fatalf("Lash at a creature Restrained by its Web Strand: %s", line)
	}
}

// A saving throw that waits for a rest ("Whenever the Poisoned target finishes a Long Rest, it is subjected to the following effect. ...":
// Otyugh Bite) is not rolled with the hit: the hit's Poisoned is applied, no save is rolled, the rest-triggered effect is the GM's text.
func TestPostgresRestSaveIsNotRolledWithTheHit(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, otyugh := earlyRulesFixture(t, "Test Filth Otyugh", []srdMonsterEntry{
		{Name: "Bite", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 12 (2d8 + 3) Piercing damage, and the target has the Poisoned condition. Whenever the Poisoned target finishes a Long Rest, it is subjected to the following effect. Constitution Saving Throw: DC 15. Failure: The target’s Hit Point maximum decreases by 5 (1d10) and doesn’t return to normal until the Poisoned condition ends on the target. Success: The Poisoned condition ends."},
	})
	_, line := narrateAs(t, gm, camp, otyugh, "Bite", fmt.Sprint(hero), "")
	if !strings.Contains(line, "HIT") || len(recordsOf(camp, hero, "poisoned")) != 1 {
		t.Fatalf("the hit should poison: %s", line)
	}
	if strings.Contains(line, "saving throw (") || !strings.Contains(line, "finishes a Long Rest") {
		t.Fatalf("the Long Rest save was rolled at the hit, or the GM was not told it: %s", line)
	}
}
