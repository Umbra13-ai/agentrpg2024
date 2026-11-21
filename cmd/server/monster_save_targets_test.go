package main

import (
	"fmt"
	"strings"
	"testing"
)

// A monster's save action aimed at other monsters of the fight: the shared save effect (applySaveEffect) rolls the target's own save,
// deals the damage through hurtMonster (resistance, absorption), stores the conditions, and honors the Evasion trait. Test monsters are
// built from the SRD Goblin Warrior / Iron Golem entries; the DCs are 30 (always fails) and 1 (always succeeds) so the outcome is known.
func TestPostgresSaveEffectOnMonsterTargets(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "monster targets gm")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Monster targets", "max_players": 2})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	var goblin, golem srdMonster2024
	for _, m := range srdMonsters2024() {
		switch m.Name {
		case "Goblin Warrior":
			goblin = m
		case "Iron Golem":
			golem = m
		}
	}
	if goblin.Name == "" || golem.Name == "" {
		t.Fatal("SRD fixtures missing")
	}
	burner := goblin
	burner.Name = "Test Burner"
	burner.Actions = []srdMonsterEntry{
		{Name: "Flame Burst", Text: "Dexterity Saving Throw: DC 30, each creature in a 15-foot Cone. Failure: 40 (8d8) Fire damage, and the target has the Frightened condition until the end of its next turn. Success: Half damage."},
		{Name: "Soft Burst", Text: "Dexterity Saving Throw: DC 1, each creature in a 15-foot Cone. Failure: 40 (8d8) Fire damage. Success: Half damage."},
		{Name: "Lingering Burst", Text: "Dexterity Saving Throw: DC 1, each creature in a 15-foot Cone. Failure: 10 (3d6) Fire damage. Success: Half damage. Failure or Success: The burner can’t take this action again until the start of its next turn."},
		{Name: "Squeeze", Text: "Strength Saving Throw: DC 30, one creature. Failure: 10 (3d6) Bludgeoning damage, and the target has the Grappled condition (escape DC 16), and it has the Restrained condition until the grapple ends. Success: Half damage."},
		{Name: "Water Jet", Text: "Dexterity Saving Throw: DC 30, each creature in a 60-foot-long, 10-foot-wide Line. Failure: 31 (9d6) Cold damage. If the target is a Huge or smaller creature, it is pushed up to 20 feet straight away from the burner and has the Prone condition. Success: Half damage."},
		{Name: "Prism Rays", Text: "The burner shoots one of the following rays at random (roll 1d2): Frost Ray. Constitution Saving Throw: DC 30. Failure: 10 (3d6) Cold damage. Success: Half damage. Mind Ray. Wisdom Saving Throw: DC 30. Failure: 9 (2d8) Psychic damage, and the target has the Charmed condition for 1 hour. Success: Half damage."},
		{Name: "Hex", Text: "Wisdom Saving Throw: DC 30, one creature. Failure: 10 (3d6) Psychic damage, and the target has the Charmed condition for 1 minute. The target repeats the save at the end of each of its turns, ending the effect on itself on a success."},
	}
	victim := goblin
	victim.Name = "Test Victim"
	victim.HP = 500
	evader := goblin
	evader.Name = "Test Evader"
	evader.HP = 500
	evader.Traits = append(append([]srdMonsterEntry{}, evader.Traits...), srdMonsterEntry{Name: "Evasion", Text: "If the evader is subjected to an effect that allows it to make a Dexterity saving throw to take only half damage, it instead takes no damage if it succeeds on the save, and only half damage if it fails."})
	resistant := goblin
	resistant.Name = "Test Resistant"
	resistant.HP = 500
	resistant.Resistances = "Fire"
	legend := goblin
	legend.Name = "Test Legend"
	legend.HP = 500
	legend.Traits = append(append([]srdMonsterEntry{}, legend.Traits...), srdMonsterEntry{Name: "Legendary Resistance (2/Day)", Text: "If the legend fails a saving throw, it can choose to succeed instead."})
	titan := goblin
	titan.Name = "Test Titan"
	titan.Size = "Gargantuan"
	titan.HP = 900
	warded := goblin
	warded.Name = "Test Warded"
	warded.HP = 500
	warded.Traits = append(append([]srdMonsterEntry{}, warded.Traits...), srdMonsterEntry{Name: "Magic Resistance", Text: "The warded has Advantage on saving throws against spells and other magical effects."})
	absorber := golem
	absorber.Name = "Test Absorber"
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior and Iron Golem variants for save-target tests", "monsters": []srdMonster2024{burner, victim, evader, resistant, legend, absorber, warded, titan}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	keys := map[string]string{}
	for _, m := range j["monsters"].([]interface{}) {
		mm := m.(map[string]interface{})
		keys[fmt.Sprint(mm["name"])] = fmt.Sprint(mm["monster_key"])
	}
	pgExec(t, `UPDATE lobbies SET status='active',schedule_state='off' WHERE id=$1`, camp)
	pgFight(t, camp)
	var list []map[string]interface{}
	for _, n := range []string{"Test Burner", "Test Victim", "Test Evader", "Test Resistant", "Test Legend", "Test Absorber", "Test Warded", "Test Titan"} {
		list = append(list, map[string]interface{}{"name": n, "monster_key": keys[n]})
	}
	if c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": list}); c != 200 {
		t.Fatalf("combat/add: %d %v", c, j)
	}
	ids := map[string]int{}
	f, err := readFight(db, camp)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range f.Order {
		ids[sprintAny(e["name"])] = asInt(e["id"])
	}
	act := func(action, target string) string {
		t.Helper()
		pgExec(t, `DELETE FROM spell_effects WHERE lobby_id=$1`, camp)
		c, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Test Burner", "action": action, "target": target}})
		if c != 200 {
			t.Fatalf("narrate: %d %v", c, j)
		}
		return fmt.Sprint(j["monster_action_result"])
	}
	hpOf := func(name string) int {
		f, _ := readFight(db, camp)
		_, e := f.entry(ids[name])
		return asInt(e["hp"])
	}
	// a failed save: damage in 8d8 (8-64), Frightened stored for 1 round, the monster named as its source
	before := hpOf("Test Victim")
	text := act("Flame Burst", "Test Victim")
	lost := before - hpOf("Test Victim")
	if !strings.Contains(text, "fails the Dexterity saving throw") || lost < 8 || lost > 64 {
		t.Fatalf("failed save: lost %d: %s", lost, text)
	}
	recs, _ := readConditionRecords(db, camp, ids["Test Victim"])
	if len(recs) != 1 || recs[0].Condition != "frightened" || recs[0].DurationRounds != 1 || !strings.Contains(recs[0].Source, "Test Burner") {
		t.Fatalf("stored conditions after the failed save: %+v", recs)
	}
	passConditionTime(camp, 1, ids["Test Victim"])
	if recs, _ = readConditionRecords(db, camp, ids["Test Victim"]); len(recs) != 0 {
		t.Fatalf("Frightened did not end after its round: %+v", recs)
	}
	// a successful save: half damage (4-32) and no condition
	before = hpOf("Test Victim")
	text = act("Soft Burst", "Test Victim")
	lost = before - hpOf("Test Victim")
	if !strings.Contains(text, "succeeds") || lost < 4 || lost > 32 {
		t.Fatalf("successful save: lost %d: %s", lost, text)
	}
	// Evasion: a success takes nothing, a failure half
	before = hpOf("Test Evader")
	text = act("Soft Burst", "Test Evader")
	if lost = before - hpOf("Test Evader"); lost != 0 || !strings.Contains(text, "Evasion") {
		t.Fatalf("Evasion success: lost %d: %s", lost, text)
	}
	before = hpOf("Test Evader")
	text = act("Flame Burst", "Test Evader")
	if lost = before - hpOf("Test Evader"); lost < 4 || lost > 32 || !strings.Contains(text, "Evasion") {
		t.Fatalf("Evasion failure should take half (4-32): lost %d: %s", lost, text)
	}
	// resistance: a failed save of 8d8 Fire (8-64) takes half (4-32) from a monster that resists Fire
	before = hpOf("Test Resistant")
	text = act("Flame Burst", "Test Resistant")
	if lost = before - hpOf("Test Resistant"); lost < 4 || lost > 32 || !strings.Contains(text, "resists") {
		t.Fatalf("resistance: lost %d (4-32 expected): %s", lost, text)
	}
	// Legendary Resistance (2/Day): a failed save that would give a condition succeeds twice (half damage, no condition), then fails
	for use := 1; use <= 3; use++ {
		before = hpOf("Test Legend")
		text = act("Flame Burst", "Test Legend")
		lost = before - hpOf("Test Legend")
		recs, _ = readConditionRecords(db, camp, ids["Test Legend"])
		if use <= 2 && (!strings.Contains(text, "Legendary Resistance") || len(recs) != 0 || lost > 32) {
			t.Fatalf("use %d: Legendary Resistance should turn the failure into a success: lost %d, records %+v: %s", use, lost, recs, text)
		}
		if use == 3 && (strings.Contains(text, "Legendary Resistance") || len(recs) != 1 || recs[0].Condition != "frightened") {
			t.Fatalf("use 3: both uses are spent, the failure stands: records %+v: %s", recs, text)
		}
	}
	// the repeated save is stored for the stat block's ability and DC
	act("Hex", "Test Victim")
	recs, _ = readConditionRecords(db, camp, ids["Test Victim"])
	if len(recs) != 1 || recs[0].Condition != "charmed" || recs[0].DurationRounds != 10 || recs[0].EndSave == nil || recs[0].EndSave.DC != 30 {
		t.Fatalf("Hex condition record: %+v", recs)
	}
	// an absorbing target heals instead of taking the Fire damage (the one absorption owner)
	if err := setMonsterHP(camp, ids["Test Absorber"], 100); err != nil {
		t.Fatal(err)
	}
	text = act("Flame Burst", "Test Absorber")
	if hpOf("Test Absorber") <= 100 || !strings.Contains(text, "absorbs") {
		t.Fatalf("absorber: HP %d: %s", hpOf("Test Absorber"), text)
	}
	// target_ids with a monster id
	pgExec(t, `DELETE FROM spell_effects WHERE lobby_id=$1`, camp)
	c, j = pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
		"monster_action": map[string]interface{}{"monster": "Test Burner", "action": "Soft Burst", "target_ids": []int{ids["Test Victim"], ids["Test Evader"]}}})
	if got := fmt.Sprint(j["monster_action_result"]); c != 200 || strings.Count(got, "saving throw") < 2 {
		t.Fatalf("target_ids of two monsters: %d %s", c, got)
	}
	// no target, or a name that is no creature of the fight: nothing is rolled and the reply says so (the DC is shown for the GM)
	for _, target := range []string{"", "Nobody At All"} {
		if got := act("Soft Burst", target); !strings.Contains(got, "Nothing was rolled") || !strings.Contains(got, "DC 1") {
			t.Errorf("target %q: %s", target, got)
		}
	}
	// "can't take this action again until the start of its next turn": the second use is refused with nothing rolled, and the burner's
	// next turn gives the action back
	before = hpOf("Test Victim")
	if first := act("Lingering Burst", "Test Victim"); !strings.Contains(first, "saving throw") {
		t.Fatalf("first Lingering Burst: %s", first)
	}
	after := hpOf("Test Victim")
	if second := act("Lingering Burst", "Test Victim"); !strings.Contains(second, "can't take Lingering Burst again") || hpOf("Test Victim") != after || after >= before {
		t.Fatalf("second Lingering Burst must be refused (HP %d -> %d -> %d): %s", before, after, hpOf("Test Victim"), second)
	}
	monsterFeaturesTurnStart(camp, ids["Test Burner"], "Test Burner")
	if third := act("Lingering Burst", "Test Victim"); !strings.Contains(third, "saving throw") {
		t.Fatalf("Lingering Burst after the burner's turn started: %s", third)
	}
	// Grappled with the stat block's escape DC, on a monster target
	act("Squeeze", "Test Evader")
	recs, _ = readConditionRecords(db, camp, ids["Test Evader"])
	found := false
	for _, r := range recs {
		if r.Condition == "grappled" && len(r.Escapes) == 2 && r.Escapes[0].DC == 16 && r.Escapes[1].DC == 16 {
			found = true
		}
	}
	if !found {
		t.Fatalf("Grappled record with escape DC 16: %+v", recs)
	}
	restrained := false
	for _, r := range recs {
		restrained = restrained || (r.Condition == "restrained" && r.Notes == notesEndsWithGrapple)
	}
	if !restrained {
		t.Fatalf("Restrained until the grapple ends is not stored: %+v", recs)
	}
	// the grapple ends (an escape, a release, the grappler Incapacitated): the Restrained of its source goes with it
	removeConditionsWhere(camp, ids["Test Evader"], func(r conditionRecord) bool { return r.Condition == "grappled" })
	recs, _ = readConditionRecords(db, camp, ids["Test Evader"])
	for _, r := range recs {
		if r.Condition == "restrained" || r.Condition == "grappled" {
			t.Fatalf("%s still stored after the grapple ended: %+v", r.Condition, recs)
		}
	}
	// Magic Resistance (a trait of the imported stat block) is Advantage on a save against a spell, not against a monster's own action;
	// a monster without the trait gets none. peek: nothing is rolled or used up.
	for _, tc := range []struct {
		name  string
		spell bool
		want  bool
	}{{"Test Warded", true, true}, {"Test Warded", false, false}, {"Test Victim", true, false}} {
		sv := (&saveCtx{lobby: camp, saver: ids[tc.name], ability: "dex", dc: 15, spell: tc.spell, peek: true}).gather()
		has := false
		for _, a := range sv.Advantage {
			has = has || a == "Magic Resistance"
		}
		if has != tc.want {
			t.Fatalf("%s, save against a spell=%v: Magic Resistance in the Advantage sources = %v, want %v (%v)", tc.name, tc.spell, has, tc.want, sv.Advantage)
		}
	}
	// a monster's own save action used on a Magic Resistance monster does not get the Advantage
	if r := act("Flame Burst", "Test Warded"); strings.Contains(r, "Magic Resistance") {
		t.Fatalf("Magic Resistance applied to a monster action, not a spell: %s", r)
	}
	// a condition gated by size ("If the target is a Huge or smaller creature ... has the Prone condition") goes to a Medium target and
	// not to a Gargantuan one; the damage is dealt to both and the reply says why the big one stayed standing
	hasCond := func(name, cond string) bool {
		recs, _ := readConditionRecords(db, camp, ids[name])
		for _, r := range recs {
			if r.Condition == cond {
				return true
			}
		}
		return false
	}
	act("Water Jet", "Test Victim")
	if !hasCond("Test Victim", "prone") {
		t.Fatal("Prone from a Huge-or-smaller gate is not stored on a Medium target")
	}
	big := act("Water Jet", "Test Titan")
	if hasCond("Test Titan", "prone") || !strings.Contains(big, "not prone (larger than Huge)") {
		t.Fatalf("a Gargantuan target got the size-gated Prone, or the reply does not say why not: %s", big)
	}
	// an action that offers several effects is refused, nothing rolled, until the GM names the one that happened
	removeConditionsWhere(camp, ids["Test Victim"], func(conditionRecord) bool { return true }) // the Hex above charmed it
	hpBefore := hpOf("Test Victim")
	refused := act("Prism Rays", "Test Victim")
	if !strings.Contains(refused, "Nothing was rolled") || !strings.Contains(refused, "Frost Ray, Mind Ray") || hpOf("Test Victim") != hpBefore || hasCond("Test Victim", "charmed") {
		t.Fatalf("an action with two effects was not refused before rolling: %s", refused)
	}
	pgExec(t, `DELETE FROM spell_effects WHERE lobby_id=$1`, camp)
	c, j = pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
		"monster_action": map[string]interface{}{"monster": "Test Burner", "action": "Prism Rays", "use": "mind ray", "target": "Test Victim"}})
	named := fmt.Sprint(j["monster_action_result"])
	if c != 200 || !strings.Contains(named, "Mind Ray") || !strings.Contains(named, "Wisdom") || strings.Contains(named, "Frost") || !hasCond("Test Victim", "charmed") {
		t.Fatalf("the named effect was not applied alone: %d %s", c, named)
	}
}
