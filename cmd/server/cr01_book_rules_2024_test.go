package main

import (
	"fmt"
	"strings"
	"testing"
)

// CR 0-1 Monster Manual entries outside the SRD data (Jackalwere Sleep Gaze, Flumph Stench Spray, Gas Spore Death Burst, Scarecrow
// Terrifying Glare, Manes Vaporspawn Sickening Vapors, Salamander Fire Snake Fire Aura) enter through the GM compendium import. The
// texts here are invented with the same patterns (no book text is committed); each rule is checked at a character, a monster and a
// companion target.

// importBookFixtures imports invented monsters into the compendium and adds them to the fight; it answers each one's combat id.
func importBookFixtures(t *testing.T, gm string, camp int, ms ...srdMonster2024) map[string]int {
	t.Helper()
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"campaign_id": camp, "source_note": "invented CR 0-1 book-pattern fixture", "monsters": ms})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	var add []map[string]interface{}
	for _, x := range imported["monsters"].([]interface{}) {
		mm := x.(map[string]interface{})
		key := fmt.Sprint(mm["monster_key"])
		t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
		add = append(add, map[string]interface{}{"name": fmt.Sprint(mm["name"]), "monster_key": key, "hp": 1000, "ac": 1, "initiative": -400})
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": add}); c != 200 {
		t.Fatalf("add: %d %v", c, j)
	}
	ids := map[string]int{}
	for _, m := range ms {
		ids[m.Name] = monsterIDsByKeyOrName(camp, m.Name)[0]
	}
	return ids
}

func bookFixture(name, typ string, traits, actions []srdMonsterEntry) srdMonster2024 {
	m := compendiumFixture()
	m.Name, m.Type, m.Traits, m.Actions, m.Legendary, m.LegendaryIn, m.DmgImmune, m.Resistances = name, typ, traits, actions, nil, "", "", ""
	return m
}

func recordsOf(camp, id int, cond string) []conditionRecord {
	var out []conditionRecord
	for _, r := range loadConditionRecords(camp, id) {
		if r.Condition == cond {
			out = append(out, r)
		}
	}
	return out
}

// A timed condition: "for 10 minutes or until it takes damage" (ended by damage), "(Constructs and Undead succeed automatically)", a
// rolled duration "for 1d4 hours"; and "While Frightened, the target has the Paralyzed condition": a record that ends with the first.
func TestPostgresBookTimedAndLinkedConditions(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000 WHERE id=$1`, charID)
	pgExec(t, `UPDATE companions SET hp=1000,max_hp=1000 WHERE id=$1`, petID)
	watcher := bookFixture("Dusk Watcher", "Fiend", nil, []srdMonsterEntry{
		{Name: "Drowse Stare", Text: "Wisdom Saving Throw: DC 40, one creature the watcher can see within 30 feet (Constructs and Undead succeed automatically). Failure: The target has the Unconscious condition for 10 minutes or until it takes damage or a creature within 5 feet of it takes an action to wake it."},
		{Name: "Reek Spit", Text: "Dexterity Saving Throw: DC 40, one creature the watcher can see within 15 feet. Failure: The target has the Poisoned condition for 1d4 hours."},
		{Name: "Dread Stare", Text: "Wisdom Saving Throw: DC 40, one creature the watcher can see within 30 feet. Failure: The target has the Frightened condition until the end of the watcher's next turn. While Frightened, the target has the Paralyzed condition."}})
	thrall := bookFixture("Bone Thrall", "Undead", nil, []srdMonsterEntry{{Name: "Claw", Text: "Melee Attack Roll: +1, reach 5 ft. Hit: 1 (1d1) Slashing damage."}})
	beast := bookFixture("Plain Boar", "Beast", nil, []srdMonsterEntry{{Name: "Tusk", Text: "Melee Attack Roll: +1, reach 5 ft. Hit: 1 (1d1) Piercing damage."}})
	ids := importBookFixtures(t, gm, camp, watcher, thrall, beast)
	names := map[int]string{ids["Plain Boar"]: "Plain Boar", ids["Bone Thrall"]: "Bone Thrall", pet: "React Pet", charID: fmt.Sprint(charID)}
	act := func(action string, target int) string {
		t.Helper()
		c, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Dusk Watcher", "action": action, "target": names[target], "confirm_targeting": true}})
		if !narrateAnswered(c, j) {
			t.Fatalf("%s at %v: %d %v", action, target, c, j)
		}
		return fmt.Sprint(j["monster_action_result"])
	}

	// Drowse Stare: 10 minutes (100 rounds), ended by damage; an Undead succeeds without a roll
	for _, tg := range []struct {
		name string
		id   int
	}{{"character", charID}, {"monster", ids["Plain Boar"]}, {"companion", pet}} {
		res := act("Drowse Stare", tg.id)
		rs := recordsOf(camp, tg.id, "unconscious")
		if len(rs) != 1 || rs[0].DurationRounds != 100 || rs[0].Notes != notesEndsOnDamage {
			t.Fatalf("%s: Drowse Stare's record %+v (%s)", tg.name, rs, res)
		}
		if strings.Contains(res, "within 5 feet of it takes an action to wake it") && !strings.Contains(res, "the GM handles") {
			t.Errorf("%s: the wake-by-action clause isn't the GM's note: %s", tg.name, res)
		}
	}
	damageCharacter(charID, 1, "fire")
	if len(recordsOf(camp, charID, "unconscious")) != 0 {
		t.Error("damage didn't end the character's Unconscious")
	}
	if res := act("Drowse Stare", ids["Bone Thrall"]); len(recordsOf(camp, ids["Bone Thrall"], "unconscious")) != 0 || !strings.Contains(strings.ToLower(res), "succeed automatically") {
		t.Errorf("an Undead didn't succeed automatically: %s", res)
	}

	// Reek Spit: Poisoned for 1d4 hours (600 to 2400 rounds, whole hours)
	for _, tg := range []int{charID, ids["Plain Boar"], pet} {
		act("Reek Spit", tg)
		rs := recordsOf(camp, tg, "poisoned")
		if len(rs) != 1 || rs[0].DurationRounds < 600 || rs[0].DurationRounds > 2400 || rs[0].DurationRounds%600 != 0 {
			t.Errorf("Reek Spit at %d: %+v", tg, rs)
		}
	}

	// Dread Stare: Frightened and, while Frightened, Paralyzed; the Paralyzed record ends with the Frightened one
	for _, tg := range []int{charID, ids["Plain Boar"], pet} {
		res := act("Dread Stare", tg)
		fr, pa := recordsOf(camp, tg, "frightened"), recordsOf(camp, tg, "paralyzed")
		if len(fr) != 1 || len(pa) != 1 || pa[0].Notes != notesEndsWith+"frightened" || strings.Contains(res, "While Frightened, the target has the Paralyzed condition") {
			t.Fatalf("Dread Stare at %d: frightened %+v, paralyzed %+v (%s)", tg, fr, pa, res)
		}
		endCondition(camp, tg, "frightened", fr[0].Source)
		if len(recordsOf(camp, tg, "paralyzed")) != 0 {
			t.Errorf("Paralyzed outlived the Frightened record at %d", tg)
		}
	}
}

// An aura at the end of the monster's turn: a saving throw aura ("Constitution Saving Throw: DC N, each creature in a 5-foot Emanation
// originating from the X at the end of the X's turn", with a success's immunity) and a damage aura "of its choice" (its enemies, never
// the monsters of its side).
func TestPostgresBookEndOfTurnAuras(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000 WHERE id=$1`, charID)
	pgExec(t, `UPDATE companions SET hp=1000,max_hp=1000 WHERE id=$1`, petID)
	vapor := bookFixture("Fume Wretch", "Fiend", []srdMonsterEntry{{Name: "Choking Fumes", Text: "Constitution Saving Throw: DC 40, each creature in a 5-foot Emanation originating from the wretch at the end of the wretch's turn. Failure: The target has the Incapacitated condition until the end of its next turn. Success: The target is immune to this wretch's Choking Fumes for 24 hours."}},
		[]srdMonsterEntry{{Name: "Claw", Text: "Melee Attack Roll: +1, reach 5 ft. Hit: 1 (1d1) Slashing damage."}})
	snake := bookFixture("Ember Coil", "Elemental", []srdMonsterEntry{{Name: "Heat Aura", Text: "At the end of each of the coil's turns, each creature of the coil's choice in a 5-foot Emanation originating from the coil takes 3 (1d6) Cold damage."}},
		[]srdMonsterEntry{{Name: "Bite", Text: "Melee Attack Roll: +1, reach 5 ft. Hit: 1 (1d1) Piercing damage."}})
	ally := bookFixture("Coil Kin", "Beast", nil, []srdMonsterEntry{{Name: "Tusk", Text: "Melee Attack Roll: +1, reach 5 ft. Hit: 1 (1d1) Piercing damage."}})
	ids := importBookFixtures(t, gm, camp, vapor, snake, ally)
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	place := func(source int) {
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"........", "........"}, Tokens: []mapToken{
			{ID: source, Name: "source", Position: mapSquare{1, 0}, Size: 1},
			{ID: charID, Name: "hero", Position: mapSquare{0, 0}, Size: 1},
			{ID: pet, Name: "React Pet", Position: mapSquare{2, 0}, Size: 1},
			{ID: ids["Coil Kin"], Name: "Coil Kin", Position: mapSquare{1, 1}, Size: 1},
			{ID: map[bool]int{true: ids["Ember Coil"], false: ids["Fume Wretch"]}[source == ids["Fume Wretch"]], Name: "other", Position: mapSquare{7, 1}, Size: 1}}}, "forced_movement": true}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}

	place(ids["Fume Wretch"])
	lines := monsterAuraTurnEnd(camp, ids["Fume Wretch"], "Fume Wretch")
	for _, tg := range []int{charID, pet, ids["Coil Kin"]} {
		if len(recordsOf(camp, tg, "incapacitated")) != 1 {
			t.Errorf("the saving throw aura missed %d: %v", tg, lines)
		}
	}
	// a success's immunity: an immune creature is left out of the next turn's aura
	endCondition(camp, charID, "incapacitated", "")
	addSpellEffect(camp, charID, ids["Fume Wretch"], "Fume Wretch (Choking Fumes)", "effect_immune", "Choking Fumes", 14400, "", 0) // as a success records it
	monsterAuraTurnEnd(camp, ids["Fume Wretch"], "Fume Wretch")
	if len(recordsOf(camp, charID, "incapacitated")) != 0 {
		t.Error("a creature immune to the aura rolled it again")
	}

	place(ids["Ember Coil"])
	hp := func(id int) int {
		if id == charID {
			var h int
			db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, id).Scan(&h)
			return h
		}
		if id == pet {
			var h int
			db.QueryRow(`SELECT hp FROM companions WHERE id=$1`, petID).Scan(&h)
			return h
		}
		return monsterHPNow(camp, id)
	}
	c0, p0, k0 := hp(charID), hp(pet), hp(ids["Coil Kin"])
	lines = monsterAuraTurnEnd(camp, ids["Ember Coil"], "Ember Coil")
	if hp(charID) >= c0 || hp(pet) >= p0 {
		t.Errorf("the aura of its choice spared its enemies: hero %d->%d, companion %d->%d (%v)", c0, hp(charID), p0, hp(pet), lines)
	}
	if hp(ids["Coil Kin"]) != k0 {
		t.Errorf("the aura of its choice hurt a monster of its side: %d->%d", k0, hp(ids["Coil Kin"]))
	}
	if strings.Contains(strings.Join(lines, " "), "resolve the targeting") {
		t.Errorf("the aura of its choice still went to the GM: %v", lines)
	}
}
