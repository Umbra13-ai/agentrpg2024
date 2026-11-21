package main

import (
	"fmt"
	"strings"
	"testing"
)

// A hit's Hit Point maximum drain (hp_max_drain_2024.go; SRD 5.2.1 Specter and Wraith Life Drain) through every hit route (POST
// /api/gm/narrate at a character, a monster and a companion): the maximum drops by the damage the hit dealt, Hit Points follow, nothing
// of it is left for the GM; a character's reduction is recorded and the Long Rest (POST /api/characters/{id}/rest) gives it back. The
// same clause in a save effect drains each target by the damage it took (Wight pattern, every target kind); a typed clause counts that
// type only and the source regains that much (Vampire pattern); under "Failure or Success" a success's half damage drains too (Succubus
// pattern); a typed hit counts its type only (Clay Golem pattern). The texts are invented with the published patterns.
func TestPostgresLifeDrainOnEveryTarget(t *testing.T) {
	testPostgres(t)
	gm, player, camp, charID, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	m := compendiumFixture()
	m.Name, m.Type, m.Traits, m.Legendary, m.LegendaryIn = "Drain Shade", "Undead", nil, nil, ""
	m.Actions = []srdMonsterEntry{
		{Name: "Drain Touch", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 9 (2d8) Necrotic damage. If the target is a creature, its Hit Point maximum decreases by an amount equal to the damage taken."},
		{Name: "Drain Gaze", Text: "Constitution Saving Throw: DC 40, one creature. Failure: 6 (1d8 + 2) Necrotic damage, and the target’s Hit Point maximum decreases by an amount equal to the damage taken."},
		{Name: "Drain Nip", Text: "Constitution Saving Throw: DC 40, one creature within 5 feet. Failure: 5 (1d4 + 3) Piercing damage plus 10 (3d6) Necrotic damage. The target’s Hit Point maximum decreases by an amount equal to the Necrotic damage taken, and the shade regains Hit Points equal to that amount."},
		{Name: "Drain Kiss", Text: "Constitution Saving Throw: DC 12, one creature within 5 feet. Failure: 13 (3d8) Psychic damage. Success: Half damage. Failure or Success: The target’s Hit Point maximum decreases by an amount equal to the damage taken."},
		{Name: "Drain Slam", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 10 (1d10 + 5) Bludgeoning damage plus 6 (1d12) Acid damage, and the target’s Hit Point maximum decreases by an amount equal to the Acid damage taken."},
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
			"monster_action": map[string]interface{}{"monster": "Drain Shade", "action": action, "target": target, "confirm_targeting": true}})
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
	// the save-effect form: each target loses as much maximum as the damage it took (the save fails at DC 40); the GM isn't asked
	targets := []struct {
		name, target string
		id           int
	}{{"character", fmt.Sprint(charID), charID}, {"monster", "Drain Victim", victimID}, {"companion", "React Pet", pet}}
	for _, tg := range targets {
		hp0, max0 := vitals(tg.id)
		res := act("Drain Gaze", tg.target)
		hp1, max1 := vitals(tg.id)
		if taken := hp0 - hp1; taken <= 0 || max1 != max0-taken || strings.Contains(res, "Hit Point maximum decreases") || !strings.Contains(res, fmt.Sprintf("Hit Point maximum -%d", taken)) {
			t.Errorf("save drain, %s: hp %d -> %d, maximum %d -> %d: %s", tg.name, hp0, hp1, max0, max1, res)
		}
	}
	// the typed form: only the Necrotic damage drains, and the shade regains that much (the monster Hit Point owner)
	shade := monsterIDsByKeyOrName(camp, "Drain Shade")[0]
	f, _ := readFight(db, camp)
	_, se := f.entry(shade)
	shadeMax := asInt(se["max_hp"])
	for _, tg := range targets {
		if err := setMonsterHP(camp, shade, 1); err != nil {
			t.Fatal(err)
		}
		hp0, max0 := vitals(tg.id)
		res := act("Drain Nip", tg.target)
		hp1, max1 := vitals(tg.id)
		cut := max0 - max1
		if taken := hp0 - hp1; cut <= 0 || cut >= taken || strings.Contains(res, "Hit Point maximum decreases") {
			t.Errorf("typed save drain, %s: took %d, maximum cut %d (the Necrotic part only): %s", tg.name, taken, cut, res)
		}
		if got := monsterHPNow(camp, shade) - 1; got != min(cut, shadeMax-1) || !strings.Contains(res, "regains") {
			t.Errorf("typed save drain, %s: the shade regained %d for a drain of %d: %s", tg.name, got, cut, res)
		}
	}
	// "Failure or Success": whatever the save, the maximum drops by the damage taken (half on a success)
	for _, tg := range targets {
		hp0, max0 := vitals(tg.id)
		res := act("Drain Kiss", tg.target)
		hp1, max1 := vitals(tg.id)
		if taken := hp0 - hp1; max0-max1 != taken || (taken > 0 && !strings.Contains(res, fmt.Sprintf("Hit Point maximum -%d", taken))) {
			t.Errorf("failure-or-success drain, %s: took %d, maximum %d -> %d: %s", tg.name, taken, max0, max1, res)
		}
	}
	// the typed hit: only the Acid damage drains
	for _, tg := range targets {
		for n := 0; n < 30; n++ {
			hp0, max0 := vitals(tg.id)
			res := act("Drain Slam", tg.target)
			hp1, max1 := vitals(tg.id)
			if hp1 == hp0 {
				continue
			}
			if cut, taken := max0-max1, hp0-hp1; cut <= 0 || cut >= taken || strings.Contains(res, "Hit Point maximum decreases") {
				t.Errorf("typed hit drain, %s: took %d, maximum cut %d (the Acid part only): %s", tg.name, taken, cut, res)
			}
			break
		}
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
