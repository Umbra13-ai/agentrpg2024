package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// "Failure or Success:" riders through the narrate route, on a character target and on a monster target, with the save passed and failed
// (DC 1 passes, DC 40 fails): stored state, source-timed expiry, and Speed marker expiry. The damage of the Failure line must follow the save.
func TestPostgresFailureOrSuccessRiders(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "always gm")
	_, player := pgAgent(t, "always player")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Always effects", "max_players": 2})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Always Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var goblin srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Goblin Warrior" {
			goblin = m
		}
	}
	both := " Failure or Success: The target has the Frightened condition until the start of the pulser's next turn. The target's Speed is 0 until the end of its next turn."
	pulser := goblin
	pulser.Name = "Pulse Caster"
	pulser.Actions = []srdMonsterEntry{
		{Name: "Soft Pulse", Text: "Dexterity Saving Throw: DC 1, one creature. Failure: 10 (3d6) Force damage. Success: No damage." + both},
		{Name: "Hard Pulse", Text: "Dexterity Saving Throw: DC 40, one creature. Failure: 10 (3d6) Force damage. Success: No damage." + both},
	}
	victim := goblin
	victim.Name = "Pulse Victim"
	victim.HP = 500
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variants for Failure or Success tests", "monsters": []srdMonster2024{pulser, victim}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	keys := map[string]string{}
	for _, m := range j["monsters"].([]interface{}) {
		mm := m.(map[string]interface{})
		keys[fmt.Sprint(mm["name"])] = fmt.Sprint(mm["monster_key"])
	}
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Pulse Caster", "monster_key": keys["Pulse Caster"], "initiative": 20}, {"name": "Pulse Victim", "monster_key": keys["Pulse Victim"], "initiative": 1}}})
	f, _ := readFight(db, camp)
	ids := map[string]int{}
	idx := map[string]int{}
	for i, e := range f.Order {
		ids[sprintAny(e["name"])] = asInt(e["id"])
		idx[sprintAny(e["name"])] = i
	}
	casterID := ids["Pulse Caster"]
	if casterID == 0 || ids["Pulse Victim"] == 0 {
		t.Fatalf("fight order: %v", ids)
	}
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000 WHERE id=$1`, char)
	pgExec(t, `UPDATE combat_state SET current_turn_index=$1 WHERE lobby_id=$2`, idx["Pulse Caster"], camp) // the caster acts: the target's next turn is a coming one
	targets := []struct {
		name string
		id   int
		hp   func() int
		turn int // its place in the turn order
	}{
		{fmt.Sprint(char), char, func() int { var h int; db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, char).Scan(&h); return h }, idxOfID(f, char)},
		{"Pulse Victim", ids["Pulse Victim"], func() int {
			f, _ := readFight(db, camp)
			_, e := f.entry(ids["Pulse Victim"])
			return asInt(e["hp"])
		}, idx["Pulse Victim"]},
	}
	for _, tg := range targets {
		who := tg.name
		if tg.id == char {
			who = "the character"
		}
		clear := func() {
			removeConditionsWhere(camp, tg.id, func(conditionRecord) bool { return true })
			pgExec(t, `UPDATE characters SET hp=1000 WHERE id=$1`, char)
			if tg.id < 0 {
				setMonsterHP(camp, tg.id, 500) // a monster's Hit Points (setHP is a character's and did nothing here)
			}
		}
		for _, action := range []string{"Soft Pulse", "Hard Pulse"} {
			clear()
			before := tg.hp()
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": "Pulse Caster", "action": action, "target": tg.name}})
			res := fmt.Sprint(r["monster_action_result"])
			passed := action == "Soft Pulse"
			lost := before - tg.hp()
			if passed && lost != 0 || !passed && (lost < 3 || lost > 18) {
				t.Fatalf("%s, %s: lost %d, the Failure damage must follow the save: %s", who, action, lost, res)
			}
			recs, _ := readConditionRecords(db, camp, tg.id)
			var fright, zero *conditionRecord
			for i := range recs {
				switch recs[i].Condition {
				case "frightened":
					fright = &recs[i]
				case "speed_zero":
					zero = &recs[i]
				}
			}
			if fright == nil || fright.Notes != "until_start_of_next_turn" || fright.SourceID != casterID || !strings.Contains(fright.Source, "Pulse Caster") || zero == nil || zero.DurationRounds != 1 {
				t.Fatalf("%s, %s (save %v): stored state %+v, want Frightened (source-timed, from the caster) and Speed 0 for 1 round: %s", who, action, map[bool]string{true: "passed", false: "failed"}[passed], recs, res)
			}
			if strings.Contains(res, "Frightened condition until") || strings.Contains(res, "Speed is 0 until") {
				t.Fatalf("%s, %s: an applied rider was also left for the GM: %s", who, action, res)
			}
			// expiry: Speed 0 with the end of the target's next turn, Frightened with the start of the caster's next turn
			turnEffects(camp, tg.turn, "end_of_turn")
			if hasCondMarker(camp, tg.id, "speed_zero") {
				t.Fatalf("%s, %s: Speed 0 outlived the end of the target's next turn", who, action)
			}
			if !hasCondMarker(camp, tg.id, "frightened") {
				t.Fatalf("%s, %s: Frightened ended before the start of the caster's next turn", who, action)
			}
			monsterFeaturesTurnStart(camp, casterID, "Pulse Caster")
			if hasCondMarker(camp, tg.id, "frightened") {
				recs, _ = readConditionRecords(db, camp, tg.id)
				t.Fatalf("%s, %s: Frightened outlived the start of the caster's next turn: %+v", who, action, recs)
			}
		}
	}
	// ownership is the creature's identity, not the display text: a record whose Source text names nothing the hook knows still ends
	// with the caster's turn when it carries the caster's id; a record without an id (written before it was stored) ends by the old text forms
	for _, c := range []struct {
		name string
		add  condAdd
	}{
		{"identity", condAdd{Condition: "frightened", Source: "something renamed", SourceID: casterID, Notes: "until_start_of_next_turn"}},
		{"legacy \"Caster's Action\"", condAdd{Condition: "frightened", Source: "Pulse Caster's Soft Pulse", Notes: "until_start_of_next_turn"}},
		{"legacy \"Caster (Action)\"", condAdd{Condition: "frightened", Source: "Pulse Caster (Soft Pulse)", Notes: "until_start_of_next_turn"}},
	} {
		removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
		addCondition(camp, char, c.add)
		if !hasCondMarker(camp, char, "frightened") {
			t.Fatalf("%s: not stored", c.name)
		}
		monsterFeaturesTurnStart(camp, casterID, "Pulse Caster")
		if hasCondMarker(camp, char, "frightened") {
			t.Fatalf("%s: outlived the start of the caster's next turn", c.name)
		}
	}
	// another creature's turn does not end it
	removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
	addCondition(camp, char, condAdd{Condition: "frightened", Source: "Pulse Caster (Soft Pulse)", SourceID: ids["Pulse Victim"], Notes: "until_start_of_next_turn"})
	monsterFeaturesTurnStart(camp, casterID, "Pulse Caster")
	if !hasCondMarker(camp, char, "frightened") {
		t.Fatal("a condition caused by another creature ended at the caster's turn start (matched by display text instead of identity)")
	}
	// reused encounter ids: when the fight's monsters are cleared, a character's condition caused by monster -N keeps its text but no longer
	// names anyone, so the next fight's monster with the same number neither ends it nor is taken for its source
	removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
	addCondition(camp, char, condAdd{Condition: "frightened", Source: "Old Monster (Stare)", SourceID: casterID, Notes: "until_start_of_next_turn"})
	clearMonsterConditionRecords(camp)
	recs, _ := readConditionRecords(db, camp, char)
	if len(recs) != 1 || recs[0].SourceID != goneSourceID {
		t.Fatalf("a condition caused by an earlier fight's monster keeps its id: %+v", recs)
	}
	monsterFeaturesTurnStart(camp, casterID, "Pulse Caster") // the new fight's monster with the same number
	if !hasCondMarker(camp, char, "frightened") {
		t.Fatal("a reused monster number ended the earlier fight's condition")
	}
	if log := strings.Join(conditionOutlastsSourceNotices(camp, char, "the character"), " "); !strings.Contains(log, "still has the") {
		t.Fatalf("the holder's turn did not tell the GM the source is gone: %s", log)
	}
}

func idxOfID(f *fight, id int) int {
	for i, e := range f.Order {
		if asInt(e["id"]) == id {
			return i
		}
	}
	return -1
}
