package main

import (
	"fmt"
	"strings"
	"testing"
)

// The rider owner (rider_duration_2024.go, disadvantage_riders_2024.go) through every kind of target a monster hits: a character
// (the direct attack route), a monster (the continuation route) and a companion (companionHitRiders), each rider recorded for the
// duration its text names, read by the owner it belongs to (Speed, saveSources, attackSources) and nothing of it left for the GM;
// then the real turn route (combat/next) ends each one on the source's turn: the start of its next turn for a Speed rider and the
// "exposed" Advantage, the end of its next turn (not the one it is acting in) for the save Disadvantage.
// The texts are invented; their patterns are those of published hits (SRD 5.2.1 Worg Bite and Merfolk Skirmisher Ocean Spear; the
// 2024 riders of the CR 0-1 backlog that end on the attacker's turn).
func TestPostgresRiderDurationsOnEveryTarget(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, petID, opponent := companionReactionFixture(t)
	petCombat := companionIDBase + petID
	raider := compendiumFixture()
	raider.Name, raider.Type, raider.Traits = "Rider Raider", "Humanoid", nil
	raider.Legendary, raider.LegendaryIn = nil, ""
	raider.Actions = []srdMonsterEntry{
		{Name: "Chill Bite", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 4 (1d4 + 2) Cold damage, and the target's Speed decreases by 5 feet until the start of the raider's next turn."},
		{Name: "Mind Slam", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 4 (1d4 + 2) Acid damage, and the target has Disadvantage on Intelligence saving throws until the end of the raider's next turn."},
		{Name: "Opening Bite", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 4 (1d4 + 2) Piercing damage, and the next attack roll made against the target before the start of the raider's next turn has Advantage."},
		{Name: "Slow Strike", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 4 (1d4 + 2) Cold damage. If the target is a creature, its Speed decreases by 10 feet until the end of its next turn."},
		{Name: "Jab", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 3 (1d4 + 1) Bludgeoning damage."},
		{Name: "Mind Probe", Text: "Intelligence Saving Throw: DC 5, one creature. Failure: 3 (1d6) Psychic damage."},
		{Name: "Will Probe", Text: "Wisdom Saving Throw: DC 5, one creature. Failure: 3 (1d6) Psychic damage."},
	}
	victim := compendiumFixture()
	victim.Name, victim.Type, victim.Traits = "Rider Victim", "Beast", nil
	victim.Legendary, victim.LegendaryIn, victim.DmgImmune, victim.Resistances = nil, "", "", ""
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented rider-duration fixture", "monsters": []srdMonster2024{raider, victim}})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	keys := map[string]string{}
	for _, m := range imported["monsters"].([]interface{}) {
		mm := m.(map[string]interface{})
		keys[fmt.Sprint(mm["name"])] = fmt.Sprint(mm["monster_key"])
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM custom_monsters WHERE key IN ($1,$2)`, keys["Rider Raider"], keys["Rider Victim"])
	})
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Rider Raider", "monster_key": keys["Rider Raider"], "hp": 1000, "ac": 1, "initiative": 90},
		{"name": "Rider Victim", "monster_key": keys["Rider Victim"], "hp": 100000, "ac": 1, "initiative": -200}}})
	pgExec(t, `UPDATE companions SET hp=100000,max_hp=100000,ac=1 WHERE id=$1`, petID)
	f, _ := readFight(db, camp)
	ids, idx := map[string]int{}, map[string]int{}
	for i, e := range f.Order {
		ids[sprintAny(e["name"])], idx[sprintAny(e["name"])] = asInt(e["id"]), i
	}
	raiderID, victimID := ids["Rider Raider"], ids["Rider Victim"]
	if raiderID == 0 || victimID == 0 || idxOfID(f, charID) < 0 {
		t.Fatalf("turn order: %v", f.Order)
	}
	setTurn := func(i int) { pgExec(t, `UPDATE combat_state SET current_turn_index=$1 WHERE lobby_id=$2`, i, camp) }
	clean := func(id int) {
		removeConditionsWhere(camp, id, func(conditionRecord) bool { return true })
		db.Exec(`DELETE FROM combat_effects WHERE lobby_id=$1 AND target_id=$2`, camp, id)
	}
	act := func(action, target string) string {
		_, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "The raider acts.",
			"monster_action": map[string]interface{}{"monster": "Rider Raider", "action": action, "target": target}})
		return fmt.Sprint(j["monster_action_result"])
	}
	hit := func(action, target string, id int) string { // a natural 1 misses: retry until the hit lands
		for n := 0; n < 30; n++ {
			clean(id)
			if r := act(action, target); strings.Contains(r, "HIT") || strings.Contains(r, "CRITICAL") {
				return r
			}
		}
		t.Fatalf("%s at %s never hit", action, target)
		return ""
	}
	marker := func(id int, cond string) *conditionRecord {
		for _, r := range loadConditionRecords(camp, id) {
			if r.Condition == cond {
				r := r
				return &r
			}
		}
		return nil
	}
	effect := func(id int, kind string) *combatEffect {
		for _, e := range combatEffectsOn(camp, id) {
			if e.Kind == kind {
				e := e
				return &e
			}
		}
		return nil
	}
	noGMNote := func(what, r string) {
		if strings.Contains(r, "not applied") {
			t.Errorf("%s: the applied rider was also given to the GM as not applied: %s", what, r)
		}
	}

	setTurn(idxOfID(f, opponent)) // another creature is acting: no turn of the raider or a target is skipped
	for _, tg := range []struct {
		name, target string
		id           int
	}{{"character", fmt.Sprint(charID), charID}, {"monster", "Rider Victim", victimID}, {"companion", "React Pet", petCombat}} {
		// a Speed rider that ends at the start of the source's next turn
		r := hit("Chill Bite", tg.target, tg.id)
		if m := marker(tg.id, "speed_minus"); m == nil || m.Detail != "5" || m.SourceID != raiderID || m.Notes != notesUntilStartOfSourceTurn || m.DurationRounds != 0 ||
			!strings.Contains(r, "Speed -5 ft until the start of Rider Raider's next turn") {
			t.Errorf("%s: Chill Bite recorded %+v; reply %s", tg.name, m, r)
		}
		noGMNote(tg.name+" Chill Bite", r)
		if tg.id == charID {
			if base := characterSpeedOf(charID); base <= 0 {
				t.Errorf("character Speed not read")
			} else {
				clean(charID)
				if characterSpeedOf(charID) != base+5 {
					t.Errorf("the Speed owner did not read the rider: %d with it, %d without", base, characterSpeedOf(charID))
				}
			}
		}
		// Disadvantage on one ability's saves, ending at the end of the source's next turn; the save owner reads it for that
		// ability only
		r = hit("Mind Slam", tg.target, tg.id)
		if m := marker(tg.id, "disadv_save"); m == nil || m.Detail != "int" || m.SourceID != raiderID || m.Notes != notesUntilEndOfSourceTurn ||
			!strings.Contains(r, "Disadvantage on Intelligence saving throws until the end of Rider Raider's next turn") {
			t.Errorf("%s: Mind Slam recorded %+v; reply %s", tg.name, m, r)
		}
		noGMNote(tg.name+" Mind Slam", r)
		if r := act("Mind Probe", tg.target); !strings.Contains(r, "Disadvantage rider on its Intelligence saves") {
			t.Errorf("%s: the Intelligence save did not read the rider: %s", tg.name, r)
		}
		if r := act("Will Probe", tg.target); strings.Contains(r, "Disadvantage rider") {
			t.Errorf("%s: a Wisdom save read the Intelligence rider: %s", tg.name, r)
		}
		// the next attack roll against the target has Advantage; the roll uses it up
		r = hit("Opening Bite", tg.target, tg.id)
		if e := effect(tg.id, "exposed"); e == nil || e.SourceID != raiderID ||
			!strings.Contains(r, "The next attack roll against it has Advantage before the start of Rider Raider's next turn") {
			t.Errorf("%s: Opening Bite recorded %+v; reply %s", tg.name, e, r)
		}
		noGMNote(tg.name+" Opening Bite", r)
		if r := act("Jab", tg.target); !strings.Contains(r, "exposed") {
			t.Errorf("%s: the next attack roll against the target had no Advantage from the rider: %s", tg.name, r)
		}
		if effect(tg.id, "exposed") != nil {
			t.Errorf("%s: the Advantage was not used up by the roll", tg.name)
		}
		if r := act("Jab", tg.target); strings.Contains(r, "exposed") {
			t.Errorf("%s: a second attack roll had the used-up Advantage: %s", tg.name, r)
		}
		// "If the target is a creature, its Speed decreases ...": applied, and not repeated to the GM as unapplied
		r = hit("Slow Strike", tg.target, tg.id)
		if m := marker(tg.id, "speed_minus"); m == nil || m.Detail != "10" || m.DurationRounds != 1 {
			t.Errorf("%s: Slow Strike recorded %+v; reply %s", tg.name, m, r)
		}
		noGMNote(tg.name+" Slow Strike", r)
		clean(tg.id)
	}

	// timing through the turn route: the riders given on the raider's own turn
	clean(charID)
	setTurn(idx["Rider Raider"])
	hit("Mind Slam", fmt.Sprint(charID), charID)
	for n := 0; n < 30 && (marker(charID, "speed_minus") == nil || effect(charID, "exposed") == nil); n++ {
		act("Chill Bite", fmt.Sprint(charID))
		act("Opening Bite", fmt.Sprint(charID))
	}
	if marker(charID, "disadv_save") == nil || marker(charID, "speed_minus") == nil || effect(charID, "exposed") == nil {
		t.Fatalf("setup: riders not all given: %+v %+v", loadConditionRecords(camp, charID), combatEffectsOn(camp, charID))
	}
	next := func() int {
		if c, j := pgCall(t, "POST", cp+"/combat/next", gm, map[string]interface{}{}); c != 200 {
			t.Fatalf("combat/next: %d %v", c, j)
		}
		f, _ := readFight(db, camp)
		return asInt(f.Order[f.Index]["id"])
	}
	if next() == raiderID {
		t.Fatal("the turn did not move")
	}
	// the end of the turn the riders were given in is not "the end of the raider's next turn"; the start-anchored ones stand
	if marker(charID, "disadv_save") == nil || marker(charID, "speed_minus") == nil || effect(charID, "exposed") == nil {
		t.Fatalf("a rider ended at the end of the turn it was given in: %+v %+v", loadConditionRecords(camp, charID), combatEffectsOn(camp, charID))
	}
	for n := 0; n < len(f.Order)+2; n++ {
		if next() == raiderID {
			break
		}
		if marker(charID, "speed_minus") == nil || effect(charID, "exposed") == nil || marker(charID, "disadv_save") == nil {
			t.Fatalf("a rider ended on another creature's turn (turn %d): %+v %+v", n, loadConditionRecords(camp, charID), combatEffectsOn(camp, charID))
		}
	}
	// the start of the raider's next turn
	if marker(charID, "speed_minus") != nil || effect(charID, "exposed") != nil {
		t.Errorf("the Speed rider or the Advantage outlived the start of the raider's next turn: %+v %+v", loadConditionRecords(camp, charID), combatEffectsOn(camp, charID))
	}
	if marker(charID, "disadv_save") == nil {
		t.Fatal("the save Disadvantage ended at the start of the raider's turn, not its end")
	}
	next() // the end of the raider's next turn
	if marker(charID, "disadv_save") != nil {
		t.Errorf("the save Disadvantage outlived the end of the raider's next turn: %+v", loadConditionRecords(camp, charID))
	}
}
