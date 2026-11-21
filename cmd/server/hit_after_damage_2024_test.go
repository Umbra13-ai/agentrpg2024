package main

import (
	"fmt"
	"strings"
	"testing"
)

// "It starts burning" on a hit (hit_after_damage_2024.go; SRD 5.2.1 Magmin Touch) through every hit route (POST /api/gm/narrate at a
// character, a monster and a companion): the target is set burning by the Burning owner and nothing of the clause is left for the GM;
// at a burning companion's turn start the 1d4 Fire damage goes through the companion's own Hit Point owner (it used to fall into the
// character branch and deal nothing). The text is invented with the published pattern.
func TestPostgresBurningOnHitEveryTarget(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	m := compendiumFixture()
	m.Name, m.Type, m.Traits, m.Legendary, m.LegendaryIn = "Ember Imp", "Elemental", nil, nil, ""
	m.Actions = []srdMonsterEntry{{Name: "Touch", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 3 (1d4 + 1) Fire damage. If the target is a creature or a flammable object that isn’t being worn or carried, it starts burning."}}
	victim := compendiumFixture()
	victim.Name, victim.Type, victim.Traits, victim.Legendary, victim.LegendaryIn, victim.DmgImmune, victim.Resistances = "Ember Victim", "Beast", nil, nil, "", "", ""
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented burning fixture", "monsters": []srdMonster2024{m, victim}})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	keys := map[string]string{}
	for _, x := range imported["monsters"].([]interface{}) {
		mm := x.(map[string]interface{})
		key := fmt.Sprint(mm["monster_key"])
		keys[fmt.Sprint(mm["name"])] = key
		t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	}
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Ember Imp", "monster_key": keys["Ember Imp"], "hp": 100, "ac": 1, "initiative": -800},
		{"name": "Ember Victim", "monster_key": keys["Ember Victim"], "hp": 500, "ac": 1, "initiative": -800}}})
	pgExec(t, `UPDATE characters SET hp=500, max_hp=500, ac=1 WHERE id=$1`, charID)
	pgExec(t, `UPDATE companions SET hp=500, max_hp=500, ac=1 WHERE id=$1`, petID)
	victimID := monsterIDsByKeyOrName(camp, "Ember Victim")[0]
	for _, tg := range []struct {
		name, target string
		id           int
	}{{"character", fmt.Sprint(charID), charID}, {"monster", "Ember Victim", victimID}, {"companion", "React Pet", pet}} {
		var res string
		for n := 0; n < 30 && !isBurning(camp, tg.id); n++ {
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": "Ember Imp", "action": "Touch", "target": tg.target}})
			res = fmt.Sprint(r["monster_action_result"])
		}
		if !isBurning(camp, tg.id) || !strings.Contains(res, "starts burning: 1d4 Fire damage") || strings.Contains(res, "not applied") {
			t.Errorf("%s: burning %v: %s", tg.name, isBurning(camp, tg.id), res)
		}
	}
	// the pet resists Fire: a 1 on the 1d4 halves to 0, so a few turn starts are rolled (it keeps burning) until one deals damage
	before, _ := loadCompanion(petID)
	var line string
	after := before
	for n := 0; n < 12 && after.HP >= before.HP; n++ {
		line = burningTurnStart(camp, pet, "React Pet")
		after, _ = loadCompanion(petID)
	}
	if after.HP >= before.HP || !strings.Contains(line, "is burning") {
		t.Errorf("a burning companion's turn start dealt no fire damage: %d -> %d (%s)", before.HP, after.HP, line)
	}
}

// A burning sentence sets a creature on fire only without a gate or with a gate that names a creature (SRD 5.2.1 Magmin Touch, Fire
// Elemental Burn); an object-only gate (Barbed and Horned Devil Hurl Flame) never sets a creature burning and stays in the GM's text.
func TestBurnSentenceForCreatures(t *testing.T) {
	for _, c := range []struct {
		text string
		want bool
	}{
		{"Hit: 7 (2d4 + 2) Fire damage. If the target is a creature or a flammable object that isn’t being worn or carried, it starts burning.", true},
		{"Hit: 10 (2d6 + 3) Fire damage. If the target is a creature or a flammable object, it starts burning.", true},
		{"Hit: 17 (5d6) Fire damage. If the target is a flammable object that isn’t being worn or carried, it starts burning.", false},
		{"Hit: 5 (1d6 + 2) Fire damage, and the target starts burning.", true},
	} {
		if got := burnSentenceForCreatures(c.text) != ""; got != c.want {
			t.Errorf("%q: sets a creature burning %v, want %v", c.text, got, c.want)
		}
		if r := unappliedHitRider("Melee Attack Roll: +5, reach 5 ft. " + c.text); strings.Contains(r, "starts burning") == c.want {
			t.Errorf("%q: residual %q", c.text, r)
		}
	}
}
