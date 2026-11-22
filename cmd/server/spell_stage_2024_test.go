package main

import (
	"fmt"
	"strings"
	"testing"
)

// SRD 5.2.1 Sleep, cast by a monster ("casts the Sleep spell") at a character, a monster and a companion: a failed
// save is Incapacitated until the end of the target's next turn with the second stage on its repeated save (spellStages); at that turn's
// end a failed repeat turns it into Unconscious for the rest of the spell (the condition owner's repeated save, condition_turns.go), and
// damage wakes the target (wakeOnDamageSleep: a monster's cast is "<monster> (Sleep)"). The text is invented with the published pattern.
func TestPostgresSleepSecondFailure(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	m := compendiumFixture()
	m.Name, m.Type, m.Traits, m.Legendary, m.LegendaryIn, m.DmgImmune, m.Resistances = "Hush Sprite", "Fey", nil, nil, "", "", ""
	m.Actions = []srdMonsterEntry{{Name: "Hush Dust", Text: "The sprite casts the Sleep spell, requiring no spell components and using Charisma as the spellcasting ability (spell save DC 40)."}}
	victim := compendiumFixture()
	victim.Name, victim.Type, victim.Traits, victim.Legendary, victim.LegendaryIn, victim.CondImmune = "Hush Victim", "Beast", nil, nil, "", ""
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented sleep fixture", "monsters": []srdMonster2024{m, victim}})
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
		{"name": "Hush Sprite", "monster_key": keys["Hush Sprite"], "hp": 100, "ac": 1, "initiative": -900},
		{"name": "Hush Victim", "monster_key": keys["Hush Victim"], "hp": 500, "ac": 1, "initiative": -900}}})
	pgExec(t, `UPDATE characters SET hp=200, max_hp=200, temp_hp=0 WHERE id=$1`, charID)
	victimID := monsterIDsByKeyOrName(camp, "Hush Victim")[0]
	petRow, _ := loadCompanion(petID) // the companion takes its own turn (its repeated save is at the end of it)
	changeFight(camp, func(f *fight) error {
		f.place(map[string]interface{}{"id": petRow.CombatID, "name": petRow.Name, "initiative": 5, "dex_score": 12, "is_companion": true, "owner_id": petRow.OwnerID})
		return nil
	})
	index := func(id int) int {
		f, _ := readFight(db, camp)
		i, _ := f.entry(id)
		return i
	}
	sleepRecord := func(id int) *conditionRecord {
		for _, r := range loadConditionRecords(camp, id) {
			if sourceIsSpell(r.Source, "Sleep") {
				rec := r
				return &rec
			}
		}
		return nil
	}
	for _, tg := range []struct {
		name, target string
		id           int
	}{{"character", fmt.Sprint(charID), charID}, {"monster", "Hush Victim", victimID}, {"companion", "React Pet", pet}} {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Hush Sprite", "action": "Hush Dust", "target": tg.target, "confirm_targeting": true}})
		res := fmt.Sprint(r["monster_action_result"])
		rec := sleepRecord(tg.id)
		if rec == nil || rec.Condition != "incapacitated" || rec.DurationRounds != 1 || rec.EndSave == nil || rec.EndSave.Then != "unconscious" || rec.EndSave.ThenRounds != 9 {
			t.Fatalf("%s: the first failure: %+v (%s)", tg.name, rec, res)
		}
		// the end of the target's next turn: the repeated save (DC 40) fails, and the second stage takes hold
		if i := index(tg.id); i < 0 {
			t.Fatalf("%s is not in the turn order", tg.name)
		} else {
			out := endTurnEffects(camp, i)
			if rec = sleepRecord(tg.id); rec == nil || rec.Condition != "unconscious" || rec.DurationRounds != 9 || !strings.Contains(strings.Join(out.Public, " "), "becomes Unconscious") {
				t.Fatalf("%s: the second failure: %+v %v", tg.name, rec, out.Public)
			}
		}
		// damage wakes it: the spell ends on the target
		switch {
		case tg.id == charID:
			damageCharacter(charID, 1, "slashing")
		case tg.id == pet:
			c, _ := loadCompanion(petID)
			damageCompanion(c, 1, "slashing")
		default:
			hurtMonster(camp, tg.id, 1, "slashing", false)
		}
		if rec = sleepRecord(tg.id); rec != nil {
			t.Errorf("%s: damage didn't end the Sleep: %+v", tg.name, rec)
		}
	}
}

// A monster's cast of a spell that ends conditions (Lesser Restoration; a Heal) reads the same table as a character's
// cast (spellEndsConditions): at a character, a monster and a companion, the condition the GM's use names ends (else the first the
// target has), a creature without one is told so, and a Heal ends the conditions before it heals. The text is invented.
func TestPostgresMonsterCastEndsConditions(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	m := compendiumFixture()
	m.Name, m.Type, m.Traits, m.Legendary, m.LegendaryIn = "Mend Mote", "Celestial", nil, nil, ""
	m.Actions = []srdMonsterEntry{
		{Name: "Soothe", Text: "The mote casts the Lesser Restoration spell, requiring no spell components and using Wisdom as the spellcasting ability."},
		{Name: "Renew", Text: "The mote casts the Heal spell, requiring no spell components and using Wisdom as the spellcasting ability."},
	}
	victim := compendiumFixture()
	victim.Name, victim.Type, victim.Traits, victim.Legendary, victim.LegendaryIn, victim.CondImmune = "Mend Victim", "Beast", nil, nil, "", ""
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented restoration fixture", "monsters": []srdMonster2024{m, victim}})
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
		{"name": "Mend Mote", "monster_key": keys["Mend Mote"], "hp": 100, "ac": 1, "initiative": -900},
		{"name": "Mend Victim", "monster_key": keys["Mend Victim"], "hp": 50, "ac": 1, "initiative": -900}}})
	victimID := monsterIDsByKeyOrName(camp, "Mend Victim")[0]
	has := func(id int, cond string) bool {
		for _, r := range loadConditionRecords(camp, id) {
			if r.Condition == cond {
				return true
			}
		}
		return false
	}
	cast := func(action, target, use string) string {
		ma := map[string]interface{}{"monster": "Mend Mote", "action": action, "target": target, "confirm_targeting": true}
		if use != "" {
			ma["use"] = use
		}
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": ma})
		return fmt.Sprint(r["monster_action_result"])
	}
	for _, tg := range []struct {
		name, target string
		id           int
	}{{"character", fmt.Sprint(charID), charID}, {"monster", "Mend Victim", victimID}, {"companion", "React Pet", pet}} {
		for _, c := range []string{"poisoned", "blinded"} {
			if r := addCondition(camp, tg.id, condAdd{Condition: c, Source: "the test"}); r.Err != nil {
				t.Fatal(r.Err)
			}
		}
		// the named condition ends, the other stays
		if res := cast("Soothe", tg.target, "blinded"); !strings.Contains(res, "Blinded condition") || has(tg.id, "blinded") || !has(tg.id, "poisoned") {
			t.Errorf("%s: Lesser Restoration naming blinded: blinded %v, poisoned %v: %s", tg.name, has(tg.id, "blinded"), has(tg.id, "poisoned"), res)
		}
		// none named: the first it has
		if res := cast("Soothe", tg.target, ""); has(tg.id, "poisoned") || !strings.Contains(res, "Poisoned") {
			t.Errorf("%s: Lesser Restoration with none named: poisoned %v: %s", tg.name, has(tg.id, "poisoned"), res)
		}
		if res := cast("Soothe", tg.target, ""); !strings.Contains(res, "nothing ends") {
			t.Errorf("%s: Lesser Restoration on a creature with none: %s", tg.name, res)
		}
		// Heal: the conditions end, then it heals
		addCondition(camp, tg.id, condAdd{Condition: "poisoned", Source: "the test"})
		if res := cast("Renew", tg.target, ""); has(tg.id, "poisoned") || !strings.Contains(res, "Poisoned condition") || !strings.Contains(res, "regains") {
			t.Errorf("%s: Heal: poisoned %v: %s", tg.name, has(tg.id, "poisoned"), res)
		}
	}
}
