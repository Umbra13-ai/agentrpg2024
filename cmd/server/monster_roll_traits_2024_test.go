package main

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// The SRD 5.2.1 traits that give a creature's own rolls Advantage on a condition are read by their wording (rollTraitOf): Blood Frenzy
// (a target missing Hit Points), Bloodied Fury (while Bloodied; the Giant Boar's melee only) and Bloodied Frenzy (while Bloodied,
// attack rolls and saving throws). The whole catalog is parsed: exactly these four entries match, with these readings, and Pack
// Tactics and Aura of Authority stay with their own rows.
func TestRollTraitsOverTheCatalog(t *testing.T) {
	got := []string{}
	for _, m := range srdMonsters2024() {
		for _, tr := range m.Traits {
			if r, ok := rollTraitOf(tr.Name, tr.Text); ok {
				got = append(got, fmt.Sprintf("%s|%s|bloodied=%v wounded=%v melee=%v saves=%v", m.Name, r.Name, r.Bloodied, r.Wounded, r.MeleeOnly, r.Saves))
			}
		}
	}
	sort.Strings(got)
	want := []string{
		"Berserker|Bloodied Frenzy|bloodied=true wounded=false melee=false saves=true",
		"Boar|Bloodied Fury|bloodied=true wounded=false melee=false saves=false",
		"Giant Boar|Bloodied Fury|bloodied=true wounded=false melee=true saves=false",
		"Sahuagin Warrior|Blood Frenzy|bloodied=false wounded=true melee=false saves=false",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("roll traits in the catalog:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, e := range traitsForGM("boar") {
		if strings.HasPrefix(e["name"].(string), "Bloodied Fury") && e["server_applies"] != true {
			t.Errorf("Bloodied Fury not marked as the server's in the GM's view: %v", e)
		}
	}
}

// Through POST /api/gm/narrate (a monster attacker at a character, a monster and a companion target) and a companion's own attack
// (POST /api/action): the attacker's trait gives Advantage only when its condition holds, and the result line names the trait. A
// melee-only trait gives nothing to a ranged attack; a saving throw trait reaches the save owner. Invented wording, same pattern.
func TestPostgresRollTraitsGiveAdvantage(t *testing.T) {
	testPostgres(t)
	gm, player, camp, charID, petID, enemy := companionReactionFixture(t)
	pet := companionIDBase + petID
	hunger := compendiumFixture()
	hunger.Name, hunger.Type, hunger.Legendary, hunger.LegendaryIn = "Hunger Fixture", "Beast", nil, ""
	hunger.Traits = []srdMonsterEntry{{Name: "Wound Hunger", Text: "The fixture has Advantage on attack rolls against any creature that doesn't have all its Hit Points."}}
	hunger.Actions = []srdMonsterEntry{{Name: "Bite", Text: "Melee Attack Roll: +30, reach 5 ft. Hit: 1 Piercing damage."}}
	tusk := compendiumFixture()
	tusk.Name, tusk.Type, tusk.Legendary, tusk.LegendaryIn = "Tusk Fixture", "Beast", nil, ""
	tusk.Traits = []srdMonsterEntry{{Name: "Tusk Fury", Text: "The fixture has Advantage on melee attack rolls while it is Bloodied."},
		{Name: "Wild Frenzy", Text: "While Bloodied, the fixture has Advantage on attack rolls and saving throws."}}
	tusk.Actions = []srdMonsterEntry{{Name: "Gore", Text: "Melee Attack Roll: +30, reach 5 ft. Hit: 1 Piercing damage."},
		{Name: "Spit", Text: "Ranged Attack Roll: +30, range 30/120 ft. Hit: 1 Acid damage."}}
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented roll trait fixture", "monsters": []srdMonster2024{hunger, tusk}})
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
		{"name": "Hunger Fixture", "monster_key": keys["Hunger Fixture"], "hp": 100, "ac": 1, "initiative": -800},
		{"name": "Tusk Fixture", "monster_key": keys["Tusk Fixture"], "hp": 100, "ac": 1, "initiative": -800}}})
	hungerID := monsterIDsByKeyOrName(camp, "Hunger Fixture")[0]
	tuskID := monsterIDsByKeyOrName(camp, "Tusk Fixture")[0]
	attack := func(monster, action, target string) string {
		t.Helper()
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": monster, "action": action, "target": target}})
		return fmt.Sprint(r["monster_action_result"])
	}
	full := func() {
		pgExec(t, `UPDATE characters SET hp=5000, max_hp=5000 WHERE id=$1`, charID)
		pgExec(t, `UPDATE companions SET hp=max_hp WHERE id=$1`, petID)
		if err := setMonsterHP(camp, enemy, 1000); err != nil {
			t.Fatal(err)
		}
	}

	// Blood Frenzy: a target at full Hit Points gives nothing; one missing a point gives Advantage, at every kind of target
	for _, tg := range []struct {
		name, target string
		wound        func()
	}{
		{"character", fmt.Sprint(charID), func() { pgExec(t, `UPDATE characters SET hp=4999 WHERE id=$1`, charID) }},
		{"monster", "Opponent", func() { setMonsterHP(camp, enemy, 999) }},
		{"companion", "React Pet", func() { pgExec(t, `UPDATE companions SET hp=max_hp-1 WHERE id=$1`, petID) }},
	} {
		full()
		if res := attack("Hunger Fixture", "Bite", tg.target); strings.Contains(res, "Wound Hunger") {
			t.Errorf("%s at full Hit Points: %s", tg.name, res)
		}
		full()
		tg.wound()
		if res := attack("Hunger Fixture", "Bite", tg.target); !strings.Contains(res, "Advantage (Wound Hunger)") {
			t.Errorf("%s missing Hit Points: %s", tg.name, res)
		}
	}

	// Bloodied: nothing above half; at half, the melee attack has Advantage from the first matching trait, the ranged one from the
	// trait that covers every attack roll; and its saving throws have Advantage (Wild Frenzy)
	full()
	if res := attack("Tusk Fixture", "Gore", fmt.Sprint(charID)); strings.Contains(res, "Advantage (") {
		t.Errorf("not Bloodied: %s", res)
	}
	if s := monsterSaveRoll(camp, tuskID, "con", 10); len(s.Save.Advantage) > 0 {
		t.Errorf("a save while not Bloodied: %v", s.Save.Advantage)
	}
	setMonsterHP(camp, tuskID, 50)
	if res := attack("Tusk Fixture", "Gore", fmt.Sprint(charID)); !strings.Contains(res, "Advantage (Tusk Fury)") {
		t.Errorf("Bloodied melee: %s", res)
	}
	if res := attack("Tusk Fixture", "Spit", fmt.Sprint(charID)); !strings.Contains(res, "Advantage (Wild Frenzy)") {
		t.Errorf("Bloodied ranged (only the trait that covers every attack roll): %s", res)
	}
	if s := monsterSaveRoll(camp, tuskID, "con", 10); strings.Join(s.Save.Advantage, ",") != "Wild Frenzy" {
		t.Errorf("a save while Bloodied: %v", s.Save.Advantage)
	}
	if s := monsterSaveRoll(camp, hungerID, "con", 10); len(s.Save.Advantage) > 0 {
		t.Errorf("an attack-only trait gave a save Advantage: %v", s.Save.Advantage)
	}

	// a companion made from a stat block with the trait: its own attack (POST /api/action) at a wounded enemy
	j := map[string]interface{}{}
	_, j = pgCall(t, "POST", "/api/gm/companion", gm, map[string]interface{}{"campaign_id": camp, "character_id": charID, "monster_key": keys["Hunger Fixture"], "name": "Hunger Pet"})
	hp, _ := j["companion"].(map[string]interface{})
	if hp == nil {
		t.Fatalf("companion: %v", j)
	}
	hungerPet := int(hp["id"].(float64))
	if r := rollTraitAdvantage(camp, companionIDBase+hungerPet, false, true, enemy); r != "" {
		t.Errorf("companion at a target with all its Hit Points: %q", r)
	}
	setMonsterHP(camp, enemy, 999)
	pgExec(t, `UPDATE companions SET kind='familiar',reaction_round=0,reaction_lobby=0,last_acted_round=0,last_acted_lobby=0 WHERE id=$1`, hungerPet)
	pgExec(t, `UPDATE characters SET eldritch_invocations='["pact-of-the-chain"]',action_used=false,attacks_remaining=NULL WHERE id=$1`, charID)
	_, r := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"companion_id": hungerPet, "action": "attack", "target_id": enemy, "description": "bite"})
	if res := fmt.Sprint(r); !strings.Contains(res, "Advantage (Wound Hunger)") {
		t.Errorf("a companion's own attack at a wounded enemy: %v", r)
	}
	_ = pet
}
