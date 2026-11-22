package main

import (
	"fmt"
	"strings"
	"testing"
)

// The range of a monster's attack (monsterAttackRange, map_rules.go), the one rule of every target route, through POST /api/gm/narrate:
// at its normal range no Disadvantage; beyond it, up to its long range, Disadvantage ("long range"); beyond its long range (or the single
// range of an attack that has no long range) no attack: nothing rolled, Hit Points unchanged. The SRD 5.2.1 ranged attacks of the CR 0-1
// roster (the published ranges are asserted against the catalog's own text), plus an invented single-range attack ("range 320 ft.", the
// single-range form of the SRD Solar's Flying Sword "range 120 ft."), against a character; one of them against a monster and a companion. The
// battle map holds at most 40 squares (200 ft): the distances are reached through the target's altitude, which tokenDistanceFeet counts.
func TestPostgresMonsterAttackRangeOnEveryTarget(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	type ranged struct {
		monster, attack string
		normal, long    int // long 0: a single range
	}
	published := []ranged{
		{"Gnoll Warrior", "Bone Bow", 150, 600}, {"Hobgoblin Warrior", "Longbow", 150, 600}, {"Scout", "Longbow", 150, 600},
		{"Tough", "Heavy Crossbow", 100, 400}, {"Goblin Warrior", "Shortbow", 80, 320}, {"Skeleton", "Shortbow", 80, 320},
		{"Bandit", "Light Crossbow", 80, 320}, {"Goblin Boss", "Shortbow", 80, 320},
	}
	catalog := map[string]srdMonster2024{}
	for _, m := range srdMonsters2024() {
		catalog[m.Name] = m
	}
	var add []map[string]interface{}
	for _, r := range published {
		text := ""
		for _, a := range catalog[r.monster].Actions {
			if a.Name == r.attack {
				text = a.Text
			}
		}
		if !strings.Contains(text, fmt.Sprintf("range %d/%d ft.", r.normal, r.long)) {
			t.Fatalf("%s %s: the catalog text %q does not give range %d/%d", r.monster, r.attack, text, r.normal, r.long)
		}
		add = append(add, map[string]interface{}{"name": "Range " + r.monster, "monster_key": monsterSlug(r.monster), "hp": 1000, "ac": 1, "initiative": -500})
	}
	gear := compendiumFixture()
	gear.Name, gear.Type, gear.Traits, gear.Legendary, gear.LegendaryIn = "Range Gearbot", "Construct", nil, nil, ""
	gear.Actions = []srdMonsterEntry{{Name: "Gear Toss", Text: "Ranged Attack Roll: +4, range 320 ft. Hit: 3 (1d4 + 1) Force damage."}}
	victim := compendiumFixture()
	victim.Name, victim.Type, victim.Traits, victim.Legendary, victim.LegendaryIn, victim.DmgImmune, victim.Resistances = "Range Victim", "Beast", nil, nil, "", "", ""
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented range fixture", "monsters": []srdMonster2024{gear, victim}})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	for _, m := range imported["monsters"].([]interface{}) {
		mm := m.(map[string]interface{})
		key := fmt.Sprint(mm["monster_key"])
		t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
		add = append(add, map[string]interface{}{"name": fmt.Sprint(mm["name"]), "monster_key": key, "hp": 100000, "ac": 1, "initiative": -500})
	}
	published = append(published, ranged{"Gearbot", "Gear Toss", 320, 0})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": add})
	pgExec(t, `UPDATE characters SET hp=100000, max_hp=100000, ac=1 WHERE id=$1`, charID)
	pgExec(t, `UPDATE companions SET hp=100000, max_hp=100000, ac=1 WHERE id=$1`, petID)
	id := func(name string) int { return monsterIDsByKeyOrName(camp, name)[0] }
	victimID := id("Range Victim")

	place := func(attacker, target, altitude int) {
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"...."}, Tokens: []mapToken{
			{ID: attacker, Name: "attacker", Position: mapSquare{0, 0}, Size: 1}, {ID: target, Name: "target", Position: mapSquare{1, 0}, Size: 1, Altitude: altitude}}},
			"forced_movement": true}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map (altitude %d): %d %v", altitude, c, r)
		}
	}
	hpOf := func(target int) (h int) {
		switch {
		case target == charID:
			db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, charID).Scan(&h)
		case target == pet:
			db.QueryRow(`SELECT hp FROM companions WHERE id=$1`, petID).Scan(&h)
		default:
			f, _ := readFight(db, camp)
			_, e := f.entry(target)
			h = asInt(e["hp"])
		}
		return
	}
	shoot := func(monster, attack, target string) string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": monster, "action": attack, "target": target}})
		return fmt.Sprint(r["monster_action_result"])
	}
	check := func(r ranged, target int, targetName string) {
		t.Helper()
		monster := "Range " + r.monster
		attacker := id(monster)
		far := r.long
		if far == 0 {
			far = r.normal
		}
		bands := []struct {
			alt           int
			long, refused bool
		}{{r.normal, false, false}, {far, r.long > 0, false}, {far + 5, false, true}}
		if r.long > 0 {
			bands = append(bands, struct {
				alt           int
				long, refused bool
			}{r.normal + 5, true, false})
		}
		for _, b := range bands {
			place(attacker, target, b.alt)
			before := hpOf(target)
			res := shoot(monster, r.attack, targetName)
			refused := strings.Contains(res, fmt.Sprintf("is %d ft away, beyond its %d ft range - no attack", b.alt, far))
			switch {
			case refused != b.refused:
				t.Errorf("%s %s at %d ft (%s): refused %v, want %v: %s", monster, r.attack, b.alt, targetName, refused, b.refused, res)
			case b.refused && hpOf(target) != before:
				t.Errorf("%s %s at %d ft (%s): a refused attack changed the Hit Points %d -> %d", monster, r.attack, b.alt, targetName, before, hpOf(target))
			case !b.refused && strings.Contains(res, "long range") != b.long:
				t.Errorf("%s %s at %d ft (%s): long-range Disadvantage %v, want %v: %s", monster, r.attack, b.alt, targetName, strings.Contains(res, "long range"), b.long, res)
			}
		}
	}
	for _, r := range published {
		check(r, charID, fmt.Sprint(charID))
	}
	// the other target routes: a monster (the continuation route) and a companion
	check(published[4], victimID, "Range Victim")
	check(published[4], pet, "React Pet")
	check(published[8], victimID, "Range Victim")
}
