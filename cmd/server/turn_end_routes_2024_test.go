package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

// TestPostgresStagedSaveOnEveryTurnEndRoute (I-ALDENQA-37, I-ALDENQA-28): a staged repeated save ("repeats the save at the end of its next
// turn ... Second Failure: Petrified") and a condition's duration run at the end of the target's turn whatever ends it: the GM's next and
// skip, narrate's advance_turn, a submitted pending turn the GM's resolve plays, and a turn that passes on its own because its creature is
// Incapacitated (resolve and next).
func TestPostgresStagedSaveOnEveryTurnEndRoute(t *testing.T) {
	testPostgres(t)
	gm, toks, camp, chars, _ := pgFightWith(t, "turnend", 1)
	tok, charID := toks[0], chars[0]
	m := compendiumFixture()
	m.Name, m.Type, m.Traits, m.Legendary, m.LegendaryIn = "Turn End Drake", "Dragon", nil, nil, ""
	m.Actions = []srdMonsterEntry{
		{Name: "Stone Nip", Text: "Constitution Saving Throw: DC 40, one creature within 5 feet. First Failure: The target has the Restrained condition. The target repeats the save at the end of its next turn if it is still Restrained, ending the effect on itself on a success. Second Failure: The target has the Petrified condition, instead of the Restrained condition, for 24 hours."},
		{Name: "Drowse Breath", Text: "Constitution Saving Throw: DC 40, each creature in a 15-foot Cone. Failure: The target has the Incapacitated condition until the end of its next turn, at which point it repeats the save. Second Failure: The target has the Unconscious condition for 1 minute. This effect ends for the target if it takes damage or a creature within 5 feet of it takes an action to wake it."},
	}
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented turn-end fixture", "monsters": []srdMonster2024{m}})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	key := fmt.Sprint(imported["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Turn End Drake", "monster_key": key, "hp": 1000, "ac": 1, "initiative": -900}}})
	pgExec(t, `UPDATE characters SET hp=300, max_hp=300, temp_hp=0 WHERE id=$1`, charID)

	has := func(cond string) bool {
		for _, r := range loadConditionRecords(camp, charID) {
			if r.Condition == cond {
				return true
			}
		}
		return false
	}
	// its turn: the character's slot is the current one, its turn's flags fresh, no pending turn left over
	itsTurn := func() {
		order := combatEntries(camp)
		for i, e := range order {
			if asInt(e["id"]) == charID {
				raw, _ := json.Marshal(order)
				pgExec(t, `UPDATE combat_state SET turn_order = $1, current_turn_index = $2 WHERE lobby_id = $3 AND active = true`, raw, i, camp)
			}
		}
		pgExec(t, `UPDATE characters SET action_used = false, bonus_action_used = false, reaction_used = false WHERE id = $1`, charID)
		pgExec(t, `DELETE FROM pending_turns WHERE lobby_id = $1`, camp)
	}
	act := func(action string) {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Turn End Drake", "action": action, "target": fmt.Sprint(charID), "confirm_targeting": true}})
		if r["error"] != nil {
			t.Fatalf("%s: %v", action, r)
		}
	}
	routes := []struct {
		name string
		end  func()
	}{
		{"combat/next", func() {
			pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/next", camp), gm, map[string]interface{}{})
		}},
		{"combat/skip", func() {
			pgExec(t, `UPDATE combat_state SET turn_started_at = NOW() - interval '5 hours' WHERE lobby_id = $1 AND active = true`, camp) // timed out
			if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/skip", camp), gm, map[string]interface{}{}); c != 200 {
				t.Fatalf("skip: %d %v", c, j)
			}
		}},
		{"narrate advance_turn", func() {
			pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "The turn ends.", "advance_turn": true})
		}},
		{"pending turn + combat/resolve", func() {
			if c, j := pgCall(t, "POST", "/api/pending-turn", tok, map[string]interface{}{"steps": []map[string]interface{}{{"action": "dodge"}}}); c != 200 {
				t.Fatalf("pending turn: %d %v", c, j)
			}
			pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/resolve", camp), gm, nil)
		}},
	}
	sched := func(state string) {
		if c, j := pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/schedule", camp), gm, map[string]interface{}{"state": state}); c != 200 {
			t.Fatalf("schedule %s: %d %v", state, c, j)
		}
	}
	routes = append(routes, struct {
		name string
		end  func()
	}{"schedule on: pending turn + combat/resolve", func() {
		sched("on")
		if c, j := pgCall(t, "POST", "/api/pending-turn", tok, map[string]interface{}{"steps": []map[string]interface{}{{"action": "dodge"}}}); c != 200 {
			t.Fatalf("pending turn (schedule): %d %v", c, j)
		}
		pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/resolve", camp), gm, nil)
		sched("off")
	}})
	for _, rt := range routes {
		removeConditionsWhere(camp, charID, func(conditionRecord) bool { return true })
		itsTurn()
		act("Stone Nip")
		if !has("restrained") {
			t.Fatalf("%s: Stone Nip gave no Restrained", rt.name)
		}
		rt.end()
		if !has("petrified") || has("restrained") {
			t.Errorf("%s: the repeated save at the end of the turn: %+v", rt.name, loadConditionRecords(camp, charID))
		}
	}
	// a turn that passes on its own (Incapacitated): its end runs too, without the turn schedule and with it
	for _, rt := range []struct {
		name string
		end  func()
	}{
		{"automatic pass + combat/resolve", func() {
			// an automatic turn is the server's: no stand-in, with the schedule or without it
			if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/stand-in", camp), gm, map[string]interface{}{"steps": []map[string]interface{}{{"action": "dodge"}}}); c != 409 || j["error"] != "automatic_turn" {
				t.Errorf("a stand-in for an Incapacitated character: %d %v", c, j)
			}
			pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/resolve", camp), gm, nil)
		}},
		{"automatic pass + combat/next", routes[0].end},
		{"schedule on: automatic pass + combat/resolve", func() {
			sched("on")
			pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/resolve", camp), gm, nil)
			sched("off")
		}},
	} {
		removeConditionsWhere(camp, charID, func(conditionRecord) bool { return true })
		itsTurn()
		act("Drowse Breath")
		if !has("incapacitated") {
			t.Fatalf("%s: Drowse Breath gave no Incapacitated", rt.name)
		}
		rt.end()
		if !has("unconscious") || has("incapacitated") {
			t.Errorf("%s: the end of an Incapacitated turn: %+v", rt.name, loadConditionRecords(camp, charID))
		}
	}
}
