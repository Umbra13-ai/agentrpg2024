package main

import (
	"fmt"
	"strings"
	"testing"
)

// Monster Concentration tracking (monster_concentration_2024.go). Route: POST /api/gm/narrate monster_action Spellcasting starts it for a
// Concentration spell; POST /api/gm/adjust-monster damage and hp drive the save and the end. Controls: a non-Concentration spell starts
// none; a save that succeeds keeps it and what it put on others. Public log negative control: nothing about it reaches the shared log.
// Invented stat block (Constitution save +15: a hit of 32 or less always succeeds, DC 100 always fails). Not covered: Legendary
// Resistance on the save (the GM's choice, named in the notice), a Concentration begun outside the server's spell path, effects the
// server did not record.
func TestPostgresMonsterConcentrationTracking(t *testing.T) {
	testPostgres(t)
	gm, _, camp, char, _, _ := companionReactionFixture(t)
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	build := func(name string, conSave int) (caster int) {
		m := compendiumFixture()
		m.Name, m.Type = name, "Aberration"
		a := m.Abilities["con"]
		a.Save = conSave
		m.Abilities["con"] = a
		m.Actions = []srdMonsterEntry{{Name: "Spellcasting", Text: "The caster casts one of the following spells, using Intelligence as the spellcasting ability (spell save DC 99, +60 to hit with spell attacks): At Will: Hold Person, Hold Monster, Fireball."}}
		st, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented concentration fixture", "monsters": []srdMonster2024{m}})
		if st != 201 {
			t.Fatalf("import: %d %v", st, j)
		}
		key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
		t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
		pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": name, "monster_key": key, "hp": 1000, "ac": 1, "initiative": -100}}})
		return monsterIDsByKeyOrName(camp, name)[0]
	}
	strong := build("Focus Caster", 30)       // the concentration save is DC 30 at most: +30 always passes (minimum roll 31)
	frail := build("Frail Caster", -10)       // -10 against the DC 30 that 200 damage calls for always fails
	for _, id := range []int{strong, frail} { // the fixture has Legendary Resistance: a failed save would wait for the GM (see the lifecycle test); here every use is spent
		st := loadMonsterState(camp, id)
		saveMonsterState(camp, id, st)
		pgExec(t, `UPDATE combat_monster_state SET lr_used=3 WHERE lobby_id=$1 AND monster_id=$2`, camp, id)
	}
	caster, who := strong, "Focus Caster"
	pgExec(t, `UPDATE characters SET species='Human', hp=500, max_hp=500 WHERE id=$1`, char)
	cast := func(spell string) {
		t.Helper()
		removeConditionsWhere(camp, char, func(c conditionRecord) bool { return c.SourceID == 0 })
		pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": map[string]interface{}{"monster": who, "action": "Spellcasting", "use": spell, "target_ids": []int{char}}})
	}
	held := func() (n int) {
		for _, c := range loadConditionRecords(camp, char) {
			if c.SourceID == caster {
				n++
			}
		}
		return
	}
	adjust := func(body map[string]interface{}) {
		t.Helper()
		body["campaign_id"], body["target_id"] = camp, caster
		if c, r := pgCall(t, "POST", "/api/gm/adjust-monster", gm, body); c != 200 {
			t.Fatalf("adjust %v: %d %v", body, c, r)
		}
	}
	notice := func(sub string) (n int) {
		for _, x := range noticesOf(t, camp) {
			if strings.Contains(x.Text, sub) {
				n++
			}
		}
		return
	}
	// control: a spell with no Concentration starts none
	cast("Fireball")
	if got := monsterConcentrationOn(camp, caster); got != "" {
		t.Fatalf("Fireball is not a Concentration spell, but %q is recorded", got)
	}
	// a Concentration spell starts it and holds its condition
	cast("Hold Person")
	if got := monsterConcentrationOn(camp, caster); got != "Hold Person" || held() == 0 {
		t.Fatalf("Hold Person: recorded %q, conditions from the caster %d", got, held())
	}
	// damage that the save survives keeps it
	adjust(map[string]interface{}{"damage": 12})
	if monsterConcentrationOn(camp, caster) != "Hold Person" || held() == 0 || notice("keeps Concentration on Hold Person") == 0 {
		t.Errorf("a passed save must keep the Concentration and its condition: %q, %d, notices %d", monsterConcentrationOn(camp, caster), held(), notice("keeps Concentration"))
	}
	// a second Concentration spell ends the first and what it put on others
	cast("Hold Monster")
	if got := monsterConcentrationOn(camp, caster); got != "Hold Monster" {
		t.Errorf("a second Concentration spell must replace the first: %q", got)
	}
	for _, c := range loadConditionRecords(camp, char) {
		if strings.HasSuffix(c.Source, "(Hold Person)") {
			t.Errorf("a condition of the replaced spell is still there: %+v", c)
		}
	}
	caster, who = frail, "Frail Caster" // the same steps with a caster whose Constitution save always fails
	cast("Hold Monster")
	// damage the save fails ends it and its condition
	adjust(map[string]interface{}{"damage": 200})
	if monsterConcentrationOn(camp, caster) != "" || held() != 0 || notice("Concentration on Hold Monster ended") == 0 {
		t.Errorf("a failed save must end the Concentration and its condition: %q, %d, notices %d", monsterConcentrationOn(camp, caster), held(), notice("Concentration on Hold Monster ended"))
	}
	// death ends it with no save
	cast("Hold Person")
	adjust(map[string]interface{}{"hp": 0})
	if monsterConcentrationOn(camp, caster) != "" || held() != 0 || notice("no saving throw") == 0 {
		t.Errorf("death must end the Concentration and its condition with no save: %q, %d", monsterConcentrationOn(camp, caster), held())
	}
	// the shared log never mentions it
	var pub int
	db.QueryRow(`SELECT COUNT(*) FROM actions WHERE lobby_id=$1 AND (result ILIKE '%Concentration%' OR description ILIKE '%Concentration%')`, camp).Scan(&pub)
	if pub != 0 {
		t.Errorf("Concentration tracking reached the shared action log (%d rows)", pub)
	}
	// the fight's end clears a record (monster ids are not kept between fights)
	adjust(map[string]interface{}{"hp": 1000}) // back from the dead
	cast("Hold Person")
	if monsterConcentrationOn(camp, caster) == "" {
		t.Fatalf("setup: Hold Person was not recorded")
	}
	clearMonsterConditions(camp)
	if got := monsterConcentrationOn(camp, caster); got != "" {
		t.Errorf("the fight's end left a Concentration record: %q", got)
	}
}
