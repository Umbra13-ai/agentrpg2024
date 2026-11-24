package main

// A character that can't act (an Incapacitating condition: cantActBecause) has no turn to choose (I-ALDENQA-2: a Paralyzed character was
// offered attacks that were all refused, and with the turn schedule its turn waited for the final check-in): my-turn says so and offers
// nothing to send, an action is refused with the same words, and the GM's combat/next passes the turn as it passes an unconscious one.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestPostgresIncapacitatedTurnPasses(t *testing.T) {
	testPostgres(t)
	gm, player, camp, hero, raider := earlyRulesFixture(t, "Test Stinger", []srdMonsterEntry{{Name: "Sting", Text: "Melee Attack Roll: +4, reach 5 ft. Hit: 2 (1d4) Piercing damage."}})
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	// the hero's turn: the stinger acts first (Initiative 20), then the hero
	pgExec(t, `UPDATE combat_state SET current_turn_index = (SELECT i - 1 FROM jsonb_array_elements(turn_order) WITH ORDINALITY AS e(v, i) WHERE (v->>'id')::int = $2) WHERE lobby_id = $1`, camp, hero)
	if !yourTurn(camp, hero) {
		t.Fatal("the hero's turn was not set")
	}
	for _, cond := range []string{"paralyzed", "stunned", "petrified", "incapacitated"} {
		addCharacterCondition(hero, cond)
		if n, err := turnNeeds(hero); err != nil || n.Kind != needNothing || n.Why != cond {
			t.Errorf("%s: turn needs %+v %v", cond, n, err)
		}
		_, j := pgCall(t, "GET", fmt.Sprintf("/api/my-turn?character_id=%d&view=short", hero), player, nil)
		b, _ := json.Marshal(j["now"])
		if !strings.Contains(fmt.Sprint(j["cant_act"]), "You are "+cond) || strings.Contains(string(b), `"use"`) {
			t.Errorf("%s: my-turn cant_act %v, now %s", cond, j["cant_act"], b)
		}
		c, r := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"character_id": hero, "action": "attack", "target_id": raider})
		if c != 400 || r["blocking_condition"] != cond || !strings.Contains(fmt.Sprint(r["hint"]), "turn passes on its own") {
			t.Errorf("%s: action %d %v", cond, c, r)
		}
		removeCondition(hero, cond)
	}
	if n, _ := turnNeeds(hero); n.Kind != needChoose {
		t.Errorf("free again: %+v", n)
	}
	// with the turn schedule the GM's combat/next passes a Paralyzed character's turn (a free one waits for the final check-in)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "on"})
	if c, j := pgCall(t, "POST", cp+"/combat/next", gm, map[string]interface{}{}); c != 409 || j["error"] != "stand_in_too_early" {
		t.Fatalf("a free character's turn under the schedule: %d %v", c, j)
	}
	addCharacterCondition(hero, "paralyzed")
	if c, j := pgCall(t, "POST", cp+"/combat/next", gm, map[string]interface{}{}); c != 200 || yourTurn(camp, hero) {
		t.Errorf("a Paralyzed character's turn: %d %v", c, j)
	}
	var how string
	db.QueryRow(`SELECT how FROM turn_results WHERE combatant_id = $1 ORDER BY id DESC LIMIT 1`, hero).Scan(&how)
	if how != howAutomatic {
		t.Errorf("recorded as %q", how)
	}
}
