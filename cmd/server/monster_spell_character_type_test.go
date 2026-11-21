package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// A monster-cast spell that names a creature type (Hold Person: Humanoid; Animal Friendship: Beast; SRD 5.2.1 spell text) at a CHARACTER:
// the type is the character's species' "Creature Type" in the SRD 5.2.1 species data (characterCreatureType). Route: POST /api/gm/narrate
// monster_action Spellcasting. Eligible (Human): rolled, Paralyzed stored. Ineligible (Human vs a Beast-only spell): no effect, nothing
// rolled. Unknown (a species the data does not list): nothing rolled or stored, a private GM notice, the public reply says it did not
// roll. A mixed target list keeps the valid target. The server tracks no per-day uses for a monster's spell here, so there is nothing to
// spend. SRD 5.2.1 pp. 43 and 153 require the target to retain its creature type
// during Wild Shape and Polymorph.
func TestPostgresMonsterSpellCharacterType(t *testing.T) {
	testPostgres(t)
	gm, _, camp, char, petID, _ := companionReactionFixture(t)
	m := compendiumFixture()
	m.Name, m.Type = "Type Probe Caster", "Humanoid"
	m.Actions = []srdMonsterEntry{{Name: "Spellcasting", Text: "The caster casts one of the following spells, using Intelligence as the spellcasting ability (spell save DC 99, +60 to hit with spell attacks): At Will: Hold Person, Animal Friendship."}}
	st, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented type fixture", "monsters": []srdMonster2024{m}})
	if st != 201 {
		t.Fatalf("import: %d %v", st, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Type Probe Caster", "monster_key": key, "hp": 1000, "ac": 1, "initiative": -100}}})
	pet := companionIDBase + petID
	pgExec(t, `UPDATE characters SET hp=500,max_hp=500 WHERE id=$1`, char)
	cast := func(species, spell string, ids ...int) (reply string, charConds, petConds, newNotices int, notice string) {
		pgExec(t, `UPDATE characters SET species=$1 WHERE id=$2`, species, char)
		removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
		removeConditionsWhere(camp, pet, func(conditionRecord) bool { return true })
		// Each type-eligibility scenario starts without a prior cast. Recast
		// lifecycle notices belong to the separate lifecycle regression tests.
		caster := monsterIDsByKeyOrName(camp, "Type Probe Caster")[0]
		endMonsterConcentration(camp, caster, "Type Probe Caster", "reset isolated type-eligibility scenario")
		before := len(noticesOf(t, camp))
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": map[string]interface{}{"monster": "Type Probe Caster", "action": "Spellcasting", "use": spell, "target_ids": ids}})
		all := noticesOf(t, camp)
		for _, n := range all[before:] {
			notice += n.Text + " | "
		}
		return fmt.Sprint(r["monster_action_result"]), len(loadConditionRecords(camp, char)), len(loadConditionRecords(camp, pet)), len(all) - before, notice
	}
	hp := func() (h int) { db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, char).Scan(&h); return }
	if r, c, _, n, _ := cast("Human", "Hold Person", char); c == 0 || n != 0 || !strings.Contains(r, "saving throw") {
		t.Errorf("control, Human vs Hold Person: conditions %d notices %d reply %s", c, n, r)
	}
	if r, c, _, n, _ := cast("Human", "Animal Friendship", char); c != 0 || n != 0 || !strings.Contains(r, "isn't a Beast") || strings.Contains(r, "saving throw") {
		t.Errorf("Human vs a Beast-only spell: conditions %d notices %d reply %s", c, n, r)
	}
	if r, c, _, n, note := cast("Warforged", "Hold Person", char); c != 0 || n == 0 || !strings.Contains(note, "unknown") || !strings.Contains(r, "did not roll") || strings.Contains(r, "saving throw") || hp() != 500 {
		t.Errorf("unknown species: conditions %d notices %d (%s) hp %d reply %s", c, n, note, hp(), r)
	}
	if r, c, pc, _, _ := cast("Human", "Hold Person", char, pet); c == 0 || pc != 0 || !strings.Contains(r, "isn't a Humanoid") {
		t.Errorf("mixed list: the valid character must be held and the Beast companion not: char %d pet %d reply %s", c, pc, r)
	}
	// The two forms retain the original creature type, even when the beast
	// form's stat block has a different type (SRD 5.2.1 pp. 43, 153).
	pgExec(t, `UPDATE characters SET wild_shape_form='wolf' WHERE id=$1`, char)
	if r, c, _, n, _ := cast("Human", "Hold Person", char); c == 0 || n != 0 || !strings.Contains(r, "saving throw") {
		t.Errorf("Wild Shape must retain Humanoid: conditions %d notices %d reply %s", c, n, r)
	}
	pgExec(t, `UPDATE characters SET wild_shape_form='' WHERE id=$1`, char)
	pgExec(t, `UPDATE characters SET concentrating_on='Polymorph' WHERE id=$1`, char)
	addSpellEffect(camp, char, char, "Polymorph", "polymorph", "c|wolf", 600, "", 0)
	if r, c, _, n, _ := cast("Human", "Hold Person", char); c == 0 || n != 0 || !strings.Contains(r, "saving throw") {
		t.Errorf("Polymorph must retain Humanoid: conditions %d notices %d reply %s", c, n, r)
	}
	if r, c, _, n, _ := cast("Human", "Animal Friendship", char); c != 0 || n != 0 || !strings.Contains(r, "isn't a Beast") {
		t.Errorf("Polymorph does not grant the Beast type: conditions %d notices %d reply %s", c, n, r)
	}
	clearSpellEffects(camp)
	addSpellEffect(camp, char, char, "Polymorph", "polymorph", "garbled", 600, "", 0)
	if r, c, _, n, _ := cast("Human", "Hold Person", char); c == 0 || n != 0 || !strings.Contains(r, "saving throw") {
		t.Errorf("unreadable beast form does not obscure the known species type: %d %d %s", c, n, r)
	}
	if r, c, _, n, _ := cast("Unsupported species", "Hold Person", char); c != 0 || n == 0 || !strings.Contains(r, "did not roll") {
		t.Errorf("unknown original type in Polymorph must remain unknown: %d %d %s", c, n, r)
	}
	clearSpellEffects(camp)
	pgExec(t, `UPDATE characters SET concentrating_on=NULL WHERE id=$1`, char)
	if _, c, _, _, _ := cast("Human", "Hold Person", char); c == 0 {
		t.Error("control after the form ends: Human is held again")
	}
	t.Run("imported monster target", func(t *testing.T) {
		code, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Retained Type Target", "monster_key": key, "initiative": -100}}})
		if code != 200 {
			t.Fatalf("add target: %d %v", code, j)
		}
		target := monsterIDsByKeyOrName(camp, "Retained Type Target")[0]
		code, j = pgCall(t, "POST", "/api/gm/monster-features", gm, map[string]interface{}{"campaign_id": camp, "combatant_id": target, "legendary_resistance": "manual"})
		if code != 200 {
			t.Fatalf("manual resistance: %d %v", code, j)
		}
		if have := monsterTypeOf(camp, target); have != "humanoid" {
			t.Fatalf("original monster type = %q", have)
		}
		// This component fixture supplies the form stored by the existing
		// transformation owner. The assertion exercises the real targeting route.
		var original map[string]interface{}
		if err := reshapeMonster(camp, target, func(e map[string]interface{}) {
			original = map[string]interface{}{"key": e["monster_key"], "hp": e["hp"], "ac": e["ac"], "max_hp": e["max_hp"]}
			e["monster_key"] = "wolf"
		}); err != nil {
			t.Fatal(err)
		}
		snapshot, err := json.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		pgExec(t, `UPDATE characters SET concentrating_on='Polymorph' WHERE id=$1`, char)
		addSpellEffect(camp, target, char, "Polymorph", "polymorph", fmt.Sprintf("m|%d|%s", camp, snapshot), 600, "", 0)
		if r, _, _, _, _ := cast("Human", "Hold Person", target); !strings.Contains(r, "saving throw") || !hasCondMarker(camp, target, "paralyzed") {
			t.Errorf("polymorphed imported Humanoid must be eligible for Hold Person: %s", r)
		}
		if r, _, _, _, _ := cast("Human", "Animal Friendship", target); !strings.Contains(r, "isn't a Beast") || strings.Contains(r, "saving throw") {
			t.Errorf("polymorphed imported Humanoid is ineligible for Animal Friendship: %s", r)
		}
	})
}
