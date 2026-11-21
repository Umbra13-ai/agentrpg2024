package main

import (
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"strings"
	"testing"
)

// Monster Concentration lifecycle boundaries (monster_concentration_2024.go). Routes: POST /api/gm/narrate (cast), POST
// /api/gm/adjust-monster (damage), POST /api/gm/monster-concentration (the GM's decision on a failed save that Legendary Resistance
// could turn). Invented stat blocks. Rules: SRD 5.2.1 Concentration (a Constitution save when damaged; casting another Concentration
// spell ends it; Incapacitated or dead ends it) and Legendary Resistance (the monster CHOOSES to succeed). Covers: a failed save that
// Legendary Resistance could turn keeps the record and its conditions until the GM decides (both decisions); ending one cast removes
// only that caster's conditions, not another caster's condition of the same name; a non-Concentration cast and a cast of an
// unknown spell keep a valid Concentration; campaign isolation of the record; the record and the pending decision are read back from
// the database. Not covered: a cast blocked by Antimagic Field, Concentration begun outside the server's spell path.
func TestPostgresMonsterConcentrationLifecycle(t *testing.T) {
	testPostgres(t)
	gm, _, camp, char, _, _ := companionReactionFixture(t)
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	build := func(name string, conSave int, _ bool) int {
		m := compendiumFixture()
		m.Name, m.Type = name, "Aberration"
		a := m.Abilities["con"]
		a.Save = conSave
		m.Abilities["con"] = a
		m.Actions = []srdMonsterEntry{{Name: "Spellcasting", Text: "The caster casts one of the following spells, using Intelligence as the spellcasting ability (spell save DC 99, +60 to hit with spell attacks): At Will: Hold Person, Hold Monster, Fireball, Zzyzx Invented Spell."}}
		st, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented lifecycle fixture", "monsters": []srdMonster2024{m}})
		if st != 201 {
			t.Fatalf("import: %d %v", st, j)
		}
		key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
		t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
		pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": name, "monster_key": key, "hp": 1000, "ac": 1, "initiative": -100}}})
		return monsterIDsByKeyOrName(camp, name)[0]
	}
	resist := build("Resist Caster", -10, true) // the save always fails; the fixture's 3/Day Legendary Resistance
	other := build("Other Caster", 30, false)
	pgExec(t, `UPDATE characters SET species='Human', hp=500, max_hp=500 WHERE id=$1`, char)
	s := loadMonsterState(camp, resist)
	s.LRPolicy = "manual"
	saveMonsterState(camp, resist, s)
	cast := func(who, spell string) {
		t.Helper()
		pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": map[string]interface{}{"monster": who, "action": "Spellcasting", "use": spell, "target_ids": []int{char}}})
	}
	held := func(src int, suffix string) (n int) {
		for _, c := range loadConditionRecords(camp, char) {
			if c.SourceID == src && strings.HasSuffix(c.Source, suffix) {
				n++
			}
		}
		return
	}
	damage := func(id, dmg int) {
		t.Helper()
		if c, r := pgCall(t, "POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target_id": id, "damage": dmg}); c != 200 {
			t.Fatalf("damage: %d %v", c, r)
		}
	}
	decide := func(id int, decision string) (int, map[string]interface{}) {
		var token string
		db.QueryRow(`SELECT decision_token FROM monster_concentration WHERE lobby_id=$1 AND monster_id=$2`, camp, id).Scan(&token)
		return pgCall(t, "POST", "/api/gm/monster-concentration", gm, map[string]interface{}{"campaign_id": camp, "combatant_id": id, "decision": decision, "decision_token": token})
	}
	notice := func(sub string) (n int) {
		for _, x := range noticesOf(t, camp) {
			if strings.Contains(x.Text, sub) {
				n++
			}
		}
		return
	}
	// two casters hold the same spell on the same character
	cast("Resist Caster", "Hold Person")
	cast("Other Caster", "Hold Person")
	if held(resist, "(Hold Person)") == 0 || held(other, "(Hold Person)") == 0 {
		t.Fatalf("setup: both casters must have a Hold Person condition on the character: %d, %d", held(resist, "(Hold Person)"), held(other, "(Hold Person)"))
	}
	// a non-Concentration cast and a cast of a spell the server does not know keep the valid Concentration
	cast("Resist Caster", "Fireball")
	cast("Resist Caster", "Zzyzx Invented Spell")
	if monsterConcentrationOn(camp, resist) != "Hold Person" || held(resist, "(Hold Person)") == 0 {
		t.Errorf("a non-Concentration or unknown cast cleared a valid Concentration: %q", monsterConcentrationOn(camp, resist))
	}
	// the failed save is held for the GM: nothing is removed yet
	damage(resist, 100)
	if monsterConcentrationOn(camp, resist) != "Hold Person" || held(resist, "(Hold Person)") == 0 || monsterConcentrationPending(camp, resist) == "" {
		t.Fatalf("a failed save Legendary Resistance could turn must wait for the GM: %q, %d, pending %q", monsterConcentrationOn(camp, resist), held(resist, "(Hold Person)"), monsterConcentrationPending(camp, resist))
	}
	if notice("KEPT until you decide") == 0 {
		t.Errorf("the GM was not told a decision is pending")
	}
	// two more damage packets while pending: each owes its own save, queued in the table in order, none rolled yet
	queue := func() (q string) {
		db.QueryRow(`SELECT queue FROM monster_concentration WHERE lobby_id=$1 AND monster_id=$2`, camp, resist).Scan(&q)
		if q == "" {
			return ""
		}
		var rolls []game.SaveResult
		if err := json.Unmarshal([]byte(q), &rolls); err != nil {
			t.Fatalf("invalid queued saves: %v", err)
		}
		var dcs []string
		for _, roll := range rolls {
			if roll.Success || roll.Save.Bonus != -10 {
				t.Fatalf("wrong damage-time save: %+v", roll)
			}
			dcs = append(dcs, fmt.Sprint(roll.Save.DC))
		}
		return strings.Join(dcs, ",")
	}
	damage(resist, 100)
	damage(resist, 100)
	if queue() != "30,30" || notice("was rolled now") != 2 || monsterConcentrationPending(camp, resist) == "" {
		t.Errorf("each damage packet while pending must queue its own save: queue %q, notices %d", queue(), notice("was rolled now"))
	}
	// a bad decision changes nothing
	if c, _ := decide(resist, "maybe"); c != 400 || monsterConcentrationPending(camp, resist) == "" || queue() != "30,30" {
		t.Errorf("a bad decision must be refused and keep the pending state: %d", c)
	}
	// Legendary Resistance #1 spends one use; the first queued save then fails with uses left: pending again, one packet still queued
	left := legendaryResistanceLeft(camp, resist)
	if c, j := decide(resist, "legendary_resistance"); c != 200 {
		t.Fatalf("decision: %d %v", c, j)
	}
	if monsterConcentrationOn(camp, resist) != "Hold Person" || held(resist, "(Hold Person)") == 0 || monsterConcentrationPending(camp, resist) == "" || queue() != "30" || legendaryResistanceLeft(camp, resist) != left-1 {
		t.Errorf("after use 1: kept, pending again for the first queued failure, one save still queued, one use spent: %q pending %q queue %q left %d (was %d)", monsterConcentrationOn(camp, resist), monsterConcentrationPending(camp, resist), queue(), legendaryResistanceLeft(camp, resist), left)
	}
	// Legendary Resistance #2: the last queued save fails with a use left: pending again, nothing queued
	decide(resist, "legendary_resistance")
	if monsterConcentrationPending(camp, resist) == "" || queue() != "" || legendaryResistanceLeft(camp, resist) != left-2 {
		t.Errorf("after use 2: pending, queue empty, two uses spent: pending %q queue %q left %d", monsterConcentrationPending(camp, resist), queue(), legendaryResistanceLeft(camp, resist))
	}
	// Legendary Resistance #3 (the last use) with nothing queued: kept, nothing pending, all uses spent
	decide(resist, "legendary_resistance")
	if monsterConcentrationOn(camp, resist) != "Hold Person" || monsterConcentrationPending(camp, resist) != "" || legendaryResistanceLeft(camp, resist) != 0 {
		t.Errorf("after use 3: kept and settled, no use left: %q pending %q left %d", monsterConcentrationOn(camp, resist), monsterConcentrationPending(camp, resist), legendaryResistanceLeft(camp, resist))
	}
	// a retry spends nothing more
	if c, _ := decide(resist, "legendary_resistance"); c != 400 || legendaryResistanceLeft(camp, resist) != 0 {
		t.Errorf("a retry with nothing pending must be refused and spend nothing: %d", c)
	}
	// exhausted: the next failed save ends it at once with its conditions (the last Legendary Resistance use was spent above)
	damage(resist, 100)
	if monsterConcentrationOn(camp, resist) != "" || held(resist, "(Hold Person)") != 0 {
		t.Errorf("a failed save with no use left must end it at once: %q", monsterConcentrationOn(camp, resist))
	}
	if monsterConcentrationOn(camp, other) != "Hold Person" || held(other, "(Hold Person)") == 0 {
		t.Errorf("ending one caster's spell removed another caster's same-named condition")
	}
	pgCall(t, "POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target_id": resist, "hp": 1000}) // damage totals stay below the death line
	// queued saves with the last use: a failed first packet waits, a second is queued, the GM spends the last use, the queued save fails
	// with no use left: the Concentration ends in that same decision
	cast("Resist Caster", "Hold Person")
	s = loadMonsterState(camp, resist)
	saveMonsterState(camp, resist, s)
	pgExec(t, `UPDATE combat_monster_state SET lr_used=$3 WHERE lobby_id=$1 AND monster_id=$2`, camp, resist, 2)
	damage(resist, 100)
	damage(resist, 100)
	decide(resist, "legendary_resistance")
	if monsterConcentrationOn(camp, resist) != "" || held(resist, "(Hold Person)") != 0 || queue() != "" {
		t.Errorf("a queued failure with no use left must end it within the decision: %q", monsterConcentrationOn(camp, resist))
	}
	cast("Resist Caster", "Hold Person")
	s = loadMonsterState(camp, resist)
	saveMonsterState(camp, resist, s)
	pgExec(t, `UPDATE combat_monster_state SET lr_used=$3 WHERE lobby_id=$1 AND monster_id=$2`, camp, resist, 0)
	damage(resist, 100)
	damage(resist, 100)
	// the pending decision and the queue are stored in the table (read back, not a cache); "end" lets the failure stand and drops the queue
	var stored, q2 string
	db.QueryRow(`SELECT pending, queue FROM monster_concentration WHERE lobby_id=$1 AND monster_id=$2`, camp, resist).Scan(&stored, &q2)
	if stored == "" || queue() != "30" {
		t.Fatalf("the pending decision and one queued save must be stored: %q, %q", stored, q2)
	}
	if c, j := decide(resist, "end"); c != 200 {
		t.Fatalf("end: %d %v", c, j)
	}
	// ending one cast removes only that caster's conditions
	if monsterConcentrationOn(camp, resist) != "" || held(resist, "(Hold Person)") != 0 {
		t.Errorf("the GM let the failed save stand: nothing may remain from the caster: %q, %d", monsterConcentrationOn(camp, resist), held(resist, "(Hold Person)"))
	}
	if monsterConcentrationOn(camp, other) != "Hold Person" || held(other, "(Hold Person)") == 0 {
		t.Errorf("ending one caster's spell removed another caster's same-named condition: %q, %d", monsterConcentrationOn(camp, other), held(other, "(Hold Person)"))
	}
	var rows int
	db.QueryRow(`SELECT COUNT(*) FROM monster_concentration WHERE lobby_id=$1 AND monster_id=$2`, camp, resist).Scan(&rows)
	if rows != 0 {
		t.Errorf("ending left a record with its queue")
	}
	// campaign isolation: another campaign's decision route and record are untouched
	pgExec(t, `INSERT INTO monster_concentration (lobby_id, monster_id, spell, pending) VALUES ($1, $2, 'Hold Person', 'x') ON CONFLICT DO NOTHING`, camp+1000000, resist)
	t.Cleanup(func() { db.Exec(`DELETE FROM monster_concentration WHERE lobby_id=$1`, camp+1000000) })
	cast("Resist Caster", "Hold Person")
	damage(resist, 100)
	decide(resist, "end")
	if monsterConcentrationOn(camp+1000000, resist) != "Hold Person" || monsterConcentrationPending(camp+1000000, resist) != "x" {
		t.Errorf("a decision in one campaign changed another campaign's record")
	}
	pgCall(t, "POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target_id": resist, "hp": 1000}) // damage totals stay below the death line
	// a recast of the same spell while pending clears the pending state (a new cast is a new Concentration)
	cast("Resist Caster", "Hold Person")
	s = loadMonsterState(camp, resist)
	saveMonsterState(camp, resist, s)
	pgExec(t, `UPDATE combat_monster_state SET lr_used=$3 WHERE lobby_id=$1 AND monster_id=$2`, camp, resist, 0)
	damage(resist, 100)
	if monsterConcentrationPending(camp, resist) == "" {
		t.Fatalf("setup: expected a pending decision")
	}
	cast("Resist Caster", "Hold Person")
	if monsterConcentrationPending(camp, resist) != "" {
		t.Errorf("a new cast must not inherit the old pending decision")
	}
	// death with a pending decision ends it with no decision
	damage(resist, 100)
	pgCall(t, "POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target_id": resist, "hp": 0})
	if monsterConcentrationOn(camp, resist) != "" || held(resist, "(Hold Person)") != 0 {
		t.Errorf("death must end a pending Concentration: %q", monsterConcentrationOn(camp, resist))
	}
}
