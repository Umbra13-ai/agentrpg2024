package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func anyContains(list []string, w string) bool {
	for _, s := range list {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// Obscured areas, senses and light (obscured_areas_2024.go, creature_senses_2024.go, hide_2024.go), through the server:
//   - an Ice Mephit's Fog Cloud without a battle map: the creatures named are inside it, an attack roll into it has the unseen-target
//     Disadvantage and the unseen-attacker Advantage; the GM names who is inside or ends it; on the battle map the area is centered
//     on the GM's point, blocks a line through it and the creatures in it, and Blindsight perceives through it; the mephit's
//     Concentration ending ends it; a character's Fog Cloud ends with the character's Concentration;
//   - an Octopus's Ink Cloud: a Cube on its own square for 1 minute;
//   - the Shadow's Shadow Stealth: refused in bright light, rolled in dim light, and the hidden condition ends with its attack roll;
//   - Sunlight Sensitivity in sunlight: Disadvantage on a Kobold's attack roll, and a Shadow's save (D20 Tests).
func TestPostgresObscuredAreasAndSenses(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "fog gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Fog", "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, player := pgAgent(t, "fog player")
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Fog Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	if c, j := pgCall(t, "POST", cp+"/start", gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	if c, j := pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Mist Mephit", "monster_key": "ice-mephit", "hp": 300, "initiative": 5},
		{"name": "Ink Octopus", "monster_key": "octopus", "hp": 300, "initiative": 4},
		{"name": "Gloom Shadow", "monster_key": "shadow", "hp": 300, "initiative": 3},
		{"name": "Sun Kobold", "monster_key": "kobold-warrior", "hp": 300, "initiative": 2},
		{"name": "Grove Dryad", "monster_key": "dryad", "hp": 300, "initiative": 1}}}); c != 200 {
		t.Fatalf("add: %d %v", c, j)
	}
	id := func(name string) int {
		t.Helper()
		ids := monsterIDsByKeyOrName(camp, name)
		if len(ids) != 1 {
			t.Fatalf("%s: %v", name, ids)
		}
		return ids[0]
	}
	mephit, octopus, shadow, kobold := id("Mist Mephit"), id("Ink Octopus"), id("Gloom Shadow"), id("Sun Kobold")
	pgExec(t, `UPDATE characters SET hp=500, max_hp=500, blindsight_range=0 WHERE id=$1`, char)
	act := func(body map[string]interface{}) (int, string) {
		t.Helper()
		c, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": body})
		return c, fmt.Sprint(r["monster_action_result"])
	}
	gather := func(attacker, target int) (adv, dis []string) {
		c := attackCtx{lobby: camp, attacker: attacker, target: target, name: "test", melee: true, weapon: true}
		a := c.gather()
		return a.Advantage, a.Disadvantage
	}

	// ---- no battle map: the creatures named are inside the fog
	c, res := act(map[string]interface{}{"monster": "Mist Mephit", "action": "Fog Cloud", "target_ids": []int{char}})
	if c != 200 || !strings.Contains(res, "Heavily Obscured") || !strings.Contains(res, "around") {
		t.Fatalf("Fog Cloud without a map: %d %s", c, res)
	}
	areas := obscuredAreas(camp)
	if len(areas) != 1 || areas[0].Placed || len(areas[0].Inside) != 1 || areas[0].Inside[0] != char || !areas[0].Concentration {
		t.Fatalf("the area: %+v", areas)
	}
	if !areaBlocksSight(camp, kobold, char) || !areaBlocksSight(camp, char, kobold) || areaBlocksSight(camp, kobold, octopus) {
		t.Error("without a map: sight into, out of and beside the fog")
	}
	if adv, dis := gather(kobold, char); !anyContains(dis, "can't see") || !anyContains(adv, "can't see") {
		t.Errorf("an attack into the fog: Advantage %v, Disadvantage %v", adv, dis)
	}
	if adv, dis := gather(kobold, octopus); anyContains(dis, "can't see") || anyContains(adv, "can't see") {
		t.Errorf("an attack beside the fog: Advantage %v, Disadvantage %v", adv, dis)
	}
	// the GM names who is inside, then ends it (a strong wind)
	area := areas[0].ID
	if c, r := pgCall(t, "POST", "/api/gm/obscured-area", gm, map[string]interface{}{"campaign_id": camp, "area_id": area, "inside": []int{octopus}}); c != 200 {
		t.Fatalf("inside: %d %v", c, r)
	}
	if areaBlocksSight(camp, kobold, char) || !areaBlocksSight(camp, kobold, octopus) {
		t.Error("the GM's list of who is inside")
	}
	cj := map[string]interface{}{}
	if c, r := pgCall(t, "POST", "/api/gm/companion", gm, map[string]interface{}{"campaign_id": camp, "character_id": char, "monster_key": "wolf", "name": "Fog Wolf"}); c != 200 {
		t.Fatalf("companion: %d %v", c, r)
	} else {
		cj = r["companion"].(map[string]interface{})
	}
	pet := companionIDBase + int(cj["id"].(float64)) // a companion inside
	if c, _ := pgCall(t, "POST", "/api/gm/obscured-area", gm, map[string]interface{}{"campaign_id": camp, "area_id": area, "inside": []int{companionIDBase + 999999}}); c != 404 {
		t.Errorf("a creature of no campaign was accepted: %d", c)
	}
	if c, r := pgCall(t, "POST", "/api/gm/obscured-area", gm, map[string]interface{}{"campaign_id": camp, "area_id": area, "inside": []int{pet, octopus}}); c != 200 {
		t.Fatalf("a companion inside: %d %v", c, r)
	}
	if !areaBlocksSight(camp, kobold, pet) || !areaBlocksSight(camp, pet, kobold) {
		t.Error("a companion in the fog")
	}
	if adv, dis := gather(kobold, pet); !anyContains(dis, "can't see") || !anyContains(adv, "can't see") {
		t.Errorf("an attack into the fog at a companion: Advantage %v, Disadvantage %v", adv, dis)
	}
	if c, r := pgCall(t, "POST", "/api/gm/obscured-area", gm, map[string]interface{}{"campaign_id": camp, "area_id": area, "end": true}); c != 200 || len(r["areas"].([]interface{})) != 0 {
		t.Fatalf("end: %d %v", c, r)
	}
	if c, r := pgCall(t, "POST", "/api/gm/obscured-area", gm, map[string]interface{}{"campaign_id": camp, "area_id": area, "end": true}); c != 404 {
		t.Errorf("an ended area: %d %v", c, r)
	}

	// ---- the battle map: the fog centered on the GM's point (10, 1), 20 feet
	put := func(tokens ...mapToken) {
		t.Helper()
		row := strings.Repeat(".", 30)
		_, st := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{row, row, row, row, row, row, row, row, row, row}, Tokens: tokens}}
		if rev, ok := st["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("map: %d %v", c, r)
		}
	}
	put(mapToken{ID: char, Name: "hero", Position: mapSquare{1, 1}, Size: 1}, mapToken{ID: kobold, Name: "kobold", Position: mapSquare{20, 1}, Size: 1},
		mapToken{ID: octopus, Name: "octopus", Position: mapSquare{11, 1}, Size: 1}, mapToken{ID: mephit, Name: "mephit", Position: mapSquare{12, 8}, Size: 1},
		mapToken{ID: shadow, Name: "shadow", Position: mapSquare{20, 8}, Size: 1})
	if c, r := pgCall(t, "POST", "/api/gm/monster-features", gm, map[string]interface{}{"campaign_id": camp, "new_day": true}); c != 200 { // the 1/Day comes back
		t.Fatalf("new day: %d %v", c, r)
	}
	c, res = act(map[string]interface{}{"monster": "Mist Mephit", "action": "Fog Cloud", "point": []int{10, 1}})
	if c != 200 || !strings.Contains(res, "(10, 1)") {
		t.Fatalf("Fog Cloud on the map: %d %s", c, res)
	}
	if !areaBlocksSight(camp, kobold, octopus) { // the target stands in the fog
		t.Error("a target in the fog is seen")
	}
	if !areaBlocksSight(camp, kobold, char) { // the line from (20,1) to (1,1) crosses the fog
		t.Error("a line through the fog is not blocked")
	}
	if areaBlocksSight(camp, shadow, mephit) { // (20,8) to (12,8): the fog is 7 squares away
		t.Error("a line beside the fog is blocked")
	}
	pgExec(t, `UPDATE characters SET blindsight_range=60 WHERE id=$1`, char)
	if areaBlocksSight(camp, char, octopus) {
		t.Error("Blindsight 60 ft. doesn't perceive through the fog at 50 feet")
	}
	pgExec(t, `UPDATE characters SET blindsight_range=0 WHERE id=$1`, char)
	// the mephit's Concentration ends: the fog with it
	endMonsterConcentration(camp, mephit, "Mist Mephit", "the test")
	if len(obscuredAreas(camp)) != 0 {
		t.Errorf("the fog outlived the mephit's Concentration: %+v", obscuredAreas(camp))
	}

	// ---- a character's Fog Cloud, centered on its target; it ends with the character's Concentration
	pgExec(t, `UPDATE characters SET class='wizard', intl=16, known_spells='["fog-cloud"]', prepared_spells='["fog-cloud"]', action_used=false, concentrating_on=NULL WHERE id=$1`, char)
	if c, r := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"action": "cast", "spell_slug": "fog-cloud", "target_id": kobold}); c != 200 || !strings.Contains(fmt.Sprint(r["result"]), "Heavily Obscured") {
		t.Fatalf("a character's Fog Cloud: %d %v", c, r)
	}
	if a := obscuredAreas(camp); len(a) != 1 || !a[0].Placed || a[0].Center != (mapSquare{20, 1}) {
		t.Fatalf("the character's fog: %+v", a)
	}
	pgExec(t, `UPDATE characters SET concentrating_on=NULL WHERE id=$1`, char)
	if a := obscuredAreas(camp); len(a) != 0 {
		t.Errorf("the character's fog outlived its Concentration: %+v", a)
	}

	// ---- the Octopus's Ink Cloud: a 5-foot Cube on its own square
	c, res = act(map[string]interface{}{"monster": "Ink Octopus", "action": "Ink Cloud"})
	if c != 200 || !strings.Contains(res, "Heavily Obscured for 1 minute") {
		t.Fatalf("Ink Cloud: %d %s", c, res)
	}
	if !heavilyObscuredAt(camp, octopus) || heavilyObscuredAt(camp, char) {
		t.Error("the ink's square")
	}
	if a := obscuredAreas(camp); len(a) != 1 || a[0].Rounds != 10 || a[0].Shape != "cube" {
		t.Errorf("the ink: %+v", a)
	}

	// ---- Shadow Stealth: not in bright light; in dim light the Hide is rolled; an attack roll ends hiding
	c, res = act(map[string]interface{}{"monster": "Gloom Shadow", "action": "Shadow Stealth"})
	if !strings.Contains(res, "can't use Shadow Stealth") {
		t.Fatalf("Shadow Stealth in bright light: %d %s", c, res)
	}
	pgCall(t, "POST", "/api/gm/set-lighting", gm, map[string]interface{}{"campaign_id": camp, "lighting": "dim"})
	hidden := false
	for n := 0; n < 30 && !hidden; n++ {
		_, res = act(map[string]interface{}{"monster": "Gloom Shadow", "action": "Shadow Stealth"})
		if !strings.Contains(res, "Dexterity (Stealth)") {
			t.Fatalf("Shadow Stealth in dim light: %s", res)
		}
		hidden = hasCond(creatureConditions(camp, shadow), "hidden")
	}
	if !hidden {
		t.Fatal("never hidden in 30 tries")
	}
	if adv, _ := gather(shadow, kobold); !anyContains(adv, "can't see the attacker") {
		t.Errorf("a hidden attacker: %v", adv)
	}
	sc := attackCtx{lobby: camp, attacker: shadow, target: char, name: "test", melee: true, weapon: true}
	sc.roll()
	if hasCond(creatureConditions(camp, shadow), "hidden") {
		t.Error("the shadow is still hidden after its attack roll")
	}

	// ---- Sunlight Sensitivity in sunlight
	pgCall(t, "POST", "/api/gm/set-lighting", gm, map[string]interface{}{"campaign_id": camp, "lighting": "sunlight"})
	if getCampaignLighting(camp) != "bright" || !inSunlight(camp, kobold) {
		t.Fatal("sunlight is Bright Light that is sunlight")
	}
	if _, dis := gather(kobold, char); !anyContains(dis, "Sunlight Sensitivity") {
		t.Errorf("the kobold's attack in sunlight: %v", dis)
	}
	if _, dis := gather(octopus, char); anyContains(dis, "Sunlight Sensitivity") {
		t.Errorf("an octopus has no Sunlight Sensitivity: %v", dis)
	}
	s := saveCtx{lobby: camp, saver: shadow, ability: "dex", dc: 10}
	if st := s.gather(); !anyContains(st.Disadvantage, "Sunlight Weakness") { // the reason is the creature's own trait (lightPenaltyNow)
		t.Errorf("the shadow's save in sunlight (D20 Tests): %v", st.Disadvantage)
	}
	k := saveCtx{lobby: camp, saver: kobold, ability: "dex", dc: 10}
	if st := k.gather(); anyContains(st.Disadvantage, "Sunlight Sensitivity") {
		t.Errorf("the kobold's trait covers attack rolls and checks, not saves: %v", st.Disadvantage)
	}
	pgCall(t, "POST", "/api/gm/set-lighting", gm, map[string]interface{}{"campaign_id": camp, "lighting": "bright"})
	if _, dis := gather(kobold, char); anyContains(dis, "Sunlight Sensitivity") {
		t.Errorf("bright light that isn't sunlight: %v", dis)
	}

	// ---- Pass without Trace cast by a monster: its +10 Stealth serves any creature in the aura (a monster's Hide reads it)
	dryad := id("Grove Dryad")
	if passWithoutTraceOn(camp, shadow) {
		t.Fatal("Pass without Trace before the cast")
	}
	c, res = act(map[string]interface{}{"monster": "Grove Dryad", "action": "Spellcasting", "use": "Pass without Trace"})
	if c != 200 || strings.Contains(res, "GM resolves") {
		t.Fatalf("the dryad's Pass without Trace: %d %s", c, res)
	}
	if !passWithoutTraceOn(camp, dryad) {
		t.Errorf("the caster isn't in its own aura: %s", res)
	}
	if !passWithoutTraceOn(camp, shadow) { // the dryad has no token: without a distance the aura counts (as for a character's cast)
		t.Error("an ally of the caster doesn't get Pass without Trace")
	}

	// ---- the SRD Imp's Senses, "Darkvision 120 ft. (unimpeded by magical Darkness)": its Darkvision sees into Magical Darkness
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Gloom Imp", "monster_key": "imp", "hp": 50, "initiative": 0}}})
	impID := id("Gloom Imp")
	if !piercesMagicalDarknessWithin(camp, impID, 60) || piercesMagicalDarknessWithin(camp, impID, 130) {
		t.Error("unimpeded Darkvision: its 120-foot Darkvision sees into Magical Darkness, not beyond it")
	}
	if piercesMagicalDarknessWithin(camp, kobold, 30) {
		t.Error("a kobold's Darkvision sees into Magical Darkness")
	}
}

// The wording readers over the SRD catalog: Ink Cloud (Octopus, Giant Octopus) fills an area; Shadow Stealth is light-gated;
// Sunlight Sensitivity has its D20 Tests variant; seven devils' Darkvision is unimpeded by magical Darkness.
func TestObscuringAndSenseWordings(t *testing.T) {
	ink, gate, sun, d20, unimpeded := 0, 0, 0, 0, 0
	for _, m := range srdMonsters2024() {
		if unimpededDarkvisionRe.MatchString(m.Senses) {
			unimpeded++
		}
		for _, list := range [][]srdMonsterEntry{m.Traits, m.Actions, m.BonusActions, m.Reactions} {
			for _, e := range list {
				if obscuringTextRe.MatchString(e.Text) {
					ink++
				}
				if hideLightGateRe.MatchString(e.Text) && strings.Contains(e.Text, "Hide") {
					gate++
				}
				if mm := sunlightRe.FindStringSubmatch(e.Text); mm != nil {
					sun++
					if mm[1] != "" {
						d20++
					}
				}
			}
		}
	}
	if ink != 2 || gate != 1 || sun != 6 || d20 != 1 {
		t.Errorf("catalog: ink %d (want 2), light-gated Hide %d (want 1), Sunlight %d (want 6), of them D20 Tests %d (want 1)", ink, gate, sun, d20)
	}
	if unimpeded != 7 {
		t.Errorf("catalog: Darkvision unimpeded by magical Darkness %d, want 7 (the devils and the Imp)", unimpeded)
	}
	if durationRounds(1, "minute") != 10 || durationRounds(1, "hour") != 600 {
		t.Error("durations")
	}
	cube := obscuredArea{Shape: "cube", Radius: 5, CenterSize: 2} // a 10-foot Cube on a Large creature: its own space
	if cube.reachFeet() != 0 {
		t.Errorf("a 10-foot Cube on a Large creature reaches %d", cube.reachFeet())
	}
	if (obscuredArea{Shape: "sphere", Radius: 20}).reachFeet() != 20 {
		t.Error("a Sphere's reach")
	}
}
