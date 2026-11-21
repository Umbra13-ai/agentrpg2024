package main

import (
	"fmt"
	"strings"
	"testing"
)

// A hit's Hit Point maximum drain (hp_max_drain_2024.go; SRD 5.2.1 Specter and Wraith Life Drain) through every hit route (POST
// /api/gm/narrate at a character, a monster and a companion): the maximum drops by the damage the hit dealt, Hit Points follow, nothing
// of it is left for the GM; a character's reduction is recorded and the Long Rest (POST /api/characters/{id}/rest) gives it back. The
// same clause in a save effect (Wight pattern) is not automated and stays in the GM's text. The texts are invented with the published
// patterns.
func TestPostgresLifeDrainOnEveryTarget(t *testing.T) {
	testPostgres(t)
	gm, player, camp, charID, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	m := compendiumFixture()
	m.Name, m.Type, m.Traits, m.Legendary, m.LegendaryIn = "Drain Shade", "Undead", nil, nil, ""
	m.Actions = []srdMonsterEntry{
		{Name: "Drain Touch", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 9 (2d8) Necrotic damage. If the target is a creature, its Hit Point maximum decreases by an amount equal to the damage taken."},
		{Name: "Drain Gaze", Text: "Constitution Saving Throw: DC 40, one creature. Failure: 6 (1d8 + 2) Necrotic damage, and the target’s Hit Point maximum decreases by an amount equal to the damage taken."},
	}
	victim := compendiumFixture()
	victim.Name, victim.Type, victim.Traits, victim.Legendary, victim.LegendaryIn, victim.DmgImmune, victim.Resistances = "Drain Victim", "Beast", nil, nil, "", "", ""
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented life-drain fixture", "monsters": []srdMonster2024{m, victim}})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	keys := map[string]string{}
	for _, x := range imported["monsters"].([]interface{}) {
		mm := x.(map[string]interface{})
		keys[fmt.Sprint(mm["name"])] = fmt.Sprint(mm["monster_key"])
		key := fmt.Sprint(mm["monster_key"])
		t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	}
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Drain Shade", "monster_key": keys["Drain Shade"], "hp": 100, "ac": 1, "initiative": -700},
		{"name": "Drain Victim", "monster_key": keys["Drain Victim"], "hp": 500, "ac": 1, "initiative": -700}}})
	pgExec(t, `UPDATE characters SET hp=200, max_hp=200, temp_hp=0, ac=1, max_hp_reduced=0 WHERE id=$1`, charID)
	pgExec(t, `UPDATE companions SET hp=200, max_hp=200, ac=1 WHERE id=$1`, petID)
	victimID := monsterIDsByKeyOrName(camp, "Drain Victim")[0]
	vitals := func(id int) (hp, mx int) {
		switch {
		case id == charID:
			db.QueryRow(`SELECT hp, max_hp FROM characters WHERE id=$1`, charID).Scan(&hp, &mx)
		case id == pet:
			db.QueryRow(`SELECT hp, max_hp FROM companions WHERE id=$1`, petID).Scan(&hp, &mx)
		default:
			f, _ := readFight(db, camp)
			_, e := f.entry(id)
			hp, mx = asInt(e["hp"]), asInt(e["max_hp"])
		}
		return
	}
	act := func(action, target string) string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Drain Shade", "action": action, "target": target}})
		return fmt.Sprint(r["monster_action_result"])
	}
	for _, tg := range []struct {
		name, target string
		id           int
	}{{"character", fmt.Sprint(charID), charID}, {"monster", "Drain Victim", victimID}, {"companion", "React Pet", pet}} {
		for n := 0; n < 30; n++ {
			hp0, max0 := vitals(tg.id)
			res := act("Drain Touch", tg.target)
			hp1, max1 := vitals(tg.id)
			if hp1 == hp0 { // a natural 1 misses: try again
				continue
			}
			taken := hp0 - hp1
			if max1 != max0-taken || strings.Contains(res, "Hit Point maximum decreases") || !strings.Contains(res, fmt.Sprintf("Hit Point maximum -%d", taken)) {
				t.Errorf("%s: maximum %d -> %d after %d damage taken: %s", tg.name, max0, max1, taken, res)
			}
			break
		}
	}
	var reduced int
	db.QueryRow(`SELECT max_hp_reduced FROM characters WHERE id=$1`, charID).Scan(&reduced)
	if _, mx := vitals(charID); reduced <= 0 || mx != 200-reduced {
		t.Fatalf("the character's reduction is not recorded: reduced %d, maximum %d", reduced, mx)
	}
	// the save-effect form is not automated: it stays in the GM's text and the maximum holds
	_, before := vitals(charID)
	if res := act("Drain Gaze", fmt.Sprint(charID)); !strings.Contains(res, "Hit Point maximum decreases") {
		t.Errorf("the save effect's drain clause is neither applied nor given to the GM: %s", res)
	}
	if _, mx := vitals(charID); mx != before {
		t.Errorf("the save effect's drain changed the maximum %d -> %d (not automated for saves)", before, mx)
	}
	// a Long Rest gives the maximum back
	pgCall(t, "POST", cp+"/combat/end", gm, nil)
	if c, r := pgCall(t, "POST", fmt.Sprintf("/api/characters/%d/rest", charID), player, map[string]interface{}{}); c != 200 {
		t.Fatalf("long rest: %d %v", c, r)
	}
	db.QueryRow(`SELECT max_hp_reduced FROM characters WHERE id=$1`, charID).Scan(&reduced)
	if hp, mx := vitals(charID); mx != 200 || hp != 200 || reduced != 0 {
		t.Errorf("after the Long Rest: hp %d, maximum %d, still reduced %d", hp, mx, reduced)
	}
}
