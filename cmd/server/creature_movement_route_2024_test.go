package main

import (
	"fmt"
	"strings"
	"testing"
)

// A charge through POST /api/gm/narrate on every target route. With a battle map the server reads the monster's own move (the GM's
// map update without forced_movement): 20 feet straight toward the target gives the Boar's Gore its extra damage and Prone; 10 feet
// gives neither, and a forced move is no charge. Without a map, "charged": true says it; without it the GM is told. The Giant
// Seahorse's alternative amount (condCharged) is judged on the companion route, which used to refuse it.
func TestPostgresChargeOnEveryTarget(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Charge Boar", "monster_key": "boar", "hp": 1000, "ac": 1, "initiative": -500},
		{"name": "Charge Seahorse", "monster_key": "giant-seahorse", "hp": 1000, "ac": 1, "initiative": -501},
		{"name": "Charge Victim", "monster_key": "goblin-warrior", "hp": 100000, "ac": 1, "initiative": -502}}})
	pgExec(t, `UPDATE characters SET hp=100000, max_hp=100000, ac=1 WHERE id=$1`, charID)
	pgExec(t, `UPDATE companions SET hp=100000, max_hp=100000, ac=1 WHERE id=$1`, petID)
	id := func(name string) int { return monsterIDsByKeyOrName(camp, name)[0] }
	boar, seahorse, victim := id("Charge Boar"), id("Charge Seahorse"), id("Charge Victim")

	putMap := func(attacker, target, attackerX int, forced bool) {
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"........"}, Tokens: []mapToken{
			{ID: attacker, Name: "attacker", Position: mapSquare{attackerX, 0}, Size: 1}, {ID: target, Name: "target", Position: mapSquare{5, 0}, Size: 1}}},
			"forced_movement": forced}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	prone := func(target int) bool {
		for _, c := range creatureConditions(camp, target) {
			if strings.HasPrefix(strings.ToLower(c), "prone") {
				return true
			}
		}
		return false
	}
	// attack until a hit (AC 1: only a 1 misses); before each try the move is made again (a miss spends the charge)
	attack := func(monster, action, targetName string, setup func(), extra map[string]interface{}) string {
		t.Helper()
		for i := 0; i < 20; i++ {
			setup()
			ma := map[string]interface{}{"monster": monster, "action": action, "target": targetName}
			for k, v := range extra {
				ma[k] = v
			}
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": ma})
			res := fmt.Sprint(r["monster_action_result"])
			if strings.Contains(res, "HIT") {
				return res
			}
		}
		t.Fatalf("%s never hit", action)
		return ""
	}
	twoAmounts := func(res string) bool { return strings.Count(strings.ToLower(res), "piercing damage") >= 2 } // the hit and the charge's extra
	moved := func(attacker, target, startX int, forced bool) func() {
		return func() {
			putMap(attacker, target, startX, true) // placed (forced: not a move)
			putMap(attacker, target, 4, forced)    // then it moves next to the target
		}
	}
	for _, tg := range []struct {
		id   int
		name string
	}{{charID, fmt.Sprint(charID)}, {victim, "Charge Victim"}} {
		endCondition(camp, tg.id, "prone", "")
		res := attack("Charge Boar", "Gore", tg.name, moved(boar, tg.id, 0, false), nil)
		if !twoAmounts(res) || !prone(tg.id) {
			t.Errorf("%s: a 20-foot charge gave no extra damage or Prone: %s", tg.name, res)
		}
		endCondition(camp, tg.id, "prone", "")
		res = attack("Charge Boar", "Gore", tg.name, moved(boar, tg.id, 2, false), nil)
		if twoAmounts(res) || prone(tg.id) || strings.Contains(res, "moved 20+") {
			t.Errorf("%s: a 10-foot move charged: %s", tg.name, res)
		}
		res = attack("Charge Boar", "Gore", tg.name, moved(boar, tg.id, 0, true), nil)
		if twoAmounts(res) || prone(tg.id) {
			t.Errorf("%s: a forced move charged: %s", tg.name, res)
		}
	}
	// the companion route: the Seahorse's alternative amount is judged (it used to be refused there)
	res := attack("Charge Seahorse", "Ram", "React Pet", moved(seahorse, pet, 0, false), nil)
	if strings.Contains(res, "cannot judge") || strings.Contains(res, "Nothing was rolled") {
		t.Errorf("the companion route refused a charge's alternative amount: %s", res)
	}

	// without a battle map: the GM says it, or is told
	pgExec(t, `UPDATE combat_state SET battle_map = NULL WHERE lobby_id = $1`, camp)
	endCondition(camp, charID, "prone", "")
	res = attack("Charge Boar", "Gore", fmt.Sprint(charID), func() {}, map[string]interface{}{"charged": true})
	if !twoAmounts(res) || !prone(charID) {
		t.Errorf("no map, charged: true: %s", res)
	}
	endCondition(camp, charID, "prone", "")
	res = attack("Charge Boar", "Gore", fmt.Sprint(charID), func() {}, nil)
	if twoAmounts(res) || prone(charID) || !strings.Contains(res, `"charged": true`) {
		t.Errorf("no map, nothing said: %s", res)
	}
}
