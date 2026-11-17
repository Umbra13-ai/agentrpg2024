package main

// S-ALDEN-11 (feedback item 67): the pending-turn check counted every step that costs an action and warned "3 steps take your
// action" for a Fighter 11's three legal attacks. The turn's budget (turnBudget) is built from attacksPerAttackAction, the one count
// of attacks an Attack action gives, and the action table (Strike, Adds), and the check warns only beyond it.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The budget walk on its own: attacks inside an Attack action, a second action from Action Surge once the first is spent, and what
// goes beyond (an action warned, a second Bonus Action refused).
func TestTurnBudgetCheck(t *testing.T) {
	steps := func(actions ...string) []budgetStep {
		var out []budgetStep
		for i, a := range actions {
			out = append(out, budgetStep{at: fmt.Sprintf("step %d (%s)", i+1, a), action: a, kind: getActionResourceType(a)})
		}
		return out
	}
	fighter11 := turnBudget{Actions: 1, AttacksPerAction: 3, BonusActions: 1}
	surge := turnBudget{Actions: 2, AttacksPerAction: 3, BonusActions: 1, AddedBy: []string{"action_surge"}}
	for _, c := range []struct {
		name     string
		b        turnBudget
		steps    []budgetStep
		warn     string // "" none
		problem  bool
		problemW string
	}{
		{"three attacks, Extra Attack (2)", fighter11, steps("attack", "attack", "attack"), "", false, ""},
		{"a grapple is one of the attacks", fighter11, steps("attack", "grapple", "shove"), "", false, ""},
		{"four attacks without Action Surge", fighter11, steps("attack", "attack", "attack", "attack"), "step 4 (attack)", false, ""},
		{"Disengage, Action Surge, three attacks", surge, steps("disengage", "action_surge", "attack", "attack", "attack"), "", false, ""},
		{"three attacks, Action Surge, three attacks", surge, steps("attack", "attack", "attack", "action_surge", "attack", "attack", "attack"), "", false, ""},
		{"Action Surge before the first action", surge, steps("action_surge", "disengage", "attack"), "comes before your first action is spent", false, ""},
		{"Dash then an attack, one action", fighter11, steps("dash", "attack"), "step 2 (attack)", false, ""},
		{"move and free things are no action", fighter11, steps("move", "attack", "attack", "attack", "move"), "", false, ""},
		{"two Bonus Actions", fighter11, steps("attack", "second_wind", "offhand_attack"), "", true, "one Bonus Action"},
		{"Thirsting Blade: two attacks", turnBudget{Actions: 1, AttacksPerAction: 2, BonusActions: 1}, steps("attack", "attack"), "", false, ""},
		{"Thirsting Blade: three attacks", turnBudget{Actions: 1, AttacksPerAction: 2, BonusActions: 1}, steps("attack", "attack", "attack"), "step 3 (attack)", false, ""},
	} {
		problems, warnings := c.b.check(c.steps)
		w := strings.Join(warnings, " | ")
		if (c.warn == "") != (w == "") || (c.warn != "" && !strings.Contains(w, c.warn)) {
			t.Errorf("%s: warnings %q, want %q", c.name, w, c.warn)
		}
		if p := strings.Join(problems, " | "); c.problem != (p != "") || !strings.Contains(p, c.problemW) {
			t.Errorf("%s: problems %q", c.name, p)
		}
	}
}

// Through POST /api/pending-turn: a Fighter 11's three attacks and Disengage + Action Surge + three attacks are no warning, four
// attacks without Action Surge are; a Warlock with Pact of the Blade and Thirsting Blade has two attacks. The reply shows the budget.
// After Action Surge a new Attack action has every attack again.
func TestPostgresPendingTurnBudget(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "pg budget gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Budget", "max_players": 2})
	lobby := int(j["campaign_id"].(float64))
	base := fmt.Sprintf("/api/campaigns/%d", lobby)
	pgCall(t, "PATCH", base+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, tok := pgAgent(t, "pg budget player")
	_, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Budget %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", base+"/join", tok, map[string]interface{}{"character_id": char})
	pgFinish(t, tok, char)
	pgCall(t, "POST", base+"/ready", tok, nil)
	pgCall(t, "POST", base+"/ready", gm, nil)
	if c, j := pgCall(t, "POST", base+"/start", gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	order := fmt.Sprintf(`[{"id":-1,"name":"Budget Goblin","initiative":20,"hp":7,"is_monster":true},{"id":%d,"name":"Pg Budget","initiative":10}]`, char)
	pgExec(t, `INSERT INTO combat_state (lobby_id, round_number, current_turn_index, turn_order, turn_started_at, active) VALUES ($1, 1, 0, $2, NOW(), true)
		ON CONFLICT (lobby_id) DO UPDATE SET round_number = 1, current_turn_index = 0, turn_order = EXCLUDED.turn_order, turn_started_at = NOW(), active = true, turn_claim = ''`, lobby, order)
	pgExec(t, `UPDATE characters SET class = 'fighter', subclass = '', level = 11 WHERE id = $1`, char)

	send := func(actions ...string) map[string]interface{} {
		t.Helper()
		var steps []map[string]interface{}
		for _, a := range actions {
			steps = append(steps, map[string]interface{}{"action": a})
		}
		c, j := pgCall(t, "POST", "/api/pending-turn", tok, map[string]interface{}{"character_id": char, "steps": steps})
		if c != 200 {
			t.Fatalf("%v: %d %v", actions, c, j)
		}
		return j
	}
	budget := func(j map[string]interface{}) (int, int) {
		b, _ := j["budget"].(map[string]interface{})
		return asInt(b["actions"]), asInt(b["attacks_per_action"])
	}
	if j := send("attack", "attack", "attack"); j["warnings"] != nil {
		t.Errorf("a Fighter 11's three attacks are warned: %v", j["warnings"])
	} else if a, n := budget(j); a != 1 || n != 3 {
		t.Errorf("budget %d actions, %d attacks: %v", a, n, j["budget"])
	}
	if j := send("disengage", "action_surge", "attack", "attack", "attack"); j["warnings"] != nil {
		t.Errorf("Disengage, Action Surge and three attacks are warned: %v", j["warnings"])
	} else if a, _ := budget(j); a != 2 {
		t.Errorf("Action Surge isn't a second action: %v", j["budget"])
	}
	if j := send("attack", "attack", "attack", "attack"); !strings.Contains(fmt.Sprint(j["warnings"]), "step 4 (attack)") {
		t.Errorf("a fourth attack without Action Surge isn't warned: %v", j["warnings"])
	}

	// Thirsting Blade (Pact of the Blade): two attacks per Attack action
	pgExec(t, `UPDATE characters SET class = 'warlock', level = 5, eldritch_invocations = '["pact-of-the-blade","thirsting-blade"]' WHERE id = $1`, char)
	if j := send("attack", "attack"); j["warnings"] != nil {
		t.Errorf("a Thirsting Blade Warlock's two attacks are warned: %v", j["warnings"])
	} else if _, n := budget(j); n != 2 {
		t.Errorf("Thirsting Blade: %v", j["budget"])
	}
	if j := send("attack", "attack", "attack"); !strings.Contains(fmt.Sprint(j["warnings"]), "step 3 (attack)") {
		t.Errorf("a third attack isn't warned: %v", j["warnings"])
	}
	if j := send("attack", "action_surge"); fmt.Sprint(j["warnings"]) != "<nil>" || asInt(j["budget"].(map[string]interface{})["actions"]) != 1 {
		t.Errorf("Action Surge counted for a Warlock: %v %v", j["budget"], j["warnings"])
	}

	// resolution: once the first Attack action is spent, Action Surge gives a new one with every attack
	pgExec(t, `UPDATE characters SET class = 'fighter', level = 11, eldritch_invocations = '[]', class_resources_used = '{}', action_surge_this_turn = false,
		action_used = true, attacks_remaining = 0 WHERE id = $1`, char)
	pgExec(t, `UPDATE combat_state SET current_turn_index = 1 WHERE lobby_id = $1`, lobby)
	if c, j := pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "action_surge"}); c != 200 || !strings.Contains(fmt.Sprint(j), "Action Surge") {
		t.Fatalf("action surge: %d %v", c, j)
	}
	used := func() bool {
		var u bool
		db.QueryRow(`SELECT COALESCE(action_used, false) FROM characters WHERE id = $1`, char).Scan(&u)
		return u
	}
	for i := 1; i <= 3; i++ {
		if used() {
			t.Fatalf("after %d attack(s) of the Action Surge's Attack action the action is spent", i-1)
		}
		consumeAttackAction(char)
	}
	if !used() {
		t.Error("three attacks didn't spend the Action Surge's action")
	}
}
