package main

// expected_turn: a turn change that names the turn it means to end is refused once that turn has ended (turn_already_moved,
// with the turn it is now), in the one turn owner (advanceTurn's check) and on every request that changes a turn by name
// (combat/next, combat/skip, narrate's advance_turn), with the turn schedule on and off. Against the test database
// (TEST_DATABASE_URL); the suite expected_turn_test.py drives the same over HTTP.

import (
	"fmt"
	"testing"
)

// pgFight sets the campaign's fight to three monsters at round 1, the first one's turn.
func pgFight(t *testing.T, camp int) {
	t.Helper()
	order := `[{"id":-1,"name":"Goblin A","initiative":15,"hp":7},{"id":-2,"name":"Goblin B","initiative":12,"hp":7},{"id":-3,"name":"Goblin C","initiative":9,"hp":7}]`
	pgExec(t, `INSERT INTO combat_state (lobby_id, round_number, current_turn_index, turn_order, turn_started_at, active) VALUES ($1, 1, 0, $2, NOW(), true)
		ON CONFLICT (lobby_id) DO UPDATE SET round_number = 1, current_turn_index = 0, turn_order = EXCLUDED.turn_order, turn_started_at = NOW(), active = true, turn_claim = ''`, camp, order)
}

// pgTurn is the fight's round and index.
func pgTurn(t *testing.T, camp int) (int, int) {
	t.Helper()
	f, err := readFight(db, camp)
	if err != nil {
		t.Fatalf("read the fight: %v", err)
	}
	return f.Round, f.Index
}

func TestExpectedTurnInTheTurnOwner(t *testing.T) {
	testPostgres(t)
	gm, gmTok := pgAgent(t, "pg expected gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Expected %d", gm), "max_players": 4})
	camp := int(j["campaign_id"].(float64))
	pgExec(t, `UPDATE lobbies SET status = 'active' WHERE id = $1`, camp)

	// the owner itself: the named turn ends once; the same name again changes nothing and says whose turn it is now
	pgFight(t, camp)
	if tc, code := advanceTurn(camp, turnHow{by: "a test", expect: &turnRef{Round: 1, Index: 0, Named: true}}); code != "" || tc.Index != 1 {
		t.Fatalf("the named turn: %q %+v", code, tc)
	}
	tc, code := advanceTurn(camp, turnHow{by: "a test", expect: &turnRef{Round: 1, Index: 0, Named: true}})
	if code != "turn_already_moved" || tc.Now["turn_index"] != 1 || tc.Now["current_turn_id"] != -2 || tc.Now["current_turn"] != "Goblin B" {
		t.Fatalf("the same named turn again: %q %+v", code, tc.Now)
	}
	if r, i := pgTurn(t, camp); r != 1 || i != 1 {
		t.Fatalf("a refused change moved the turn to %d:%d", r, i)
	}

	base := fmt.Sprintf("/api/campaigns/%d/combat", camp)
	turn := func(r, i int) map[string]interface{} {
		return map[string]interface{}{"round": r, "turn_index": i}
	}
	for _, state := range []string{"on", "off"} {
		pgExec(t, `UPDATE lobbies SET schedule_state = $1 WHERE id = $2`, state, camp)
		pgFight(t, camp)
		// combat/next: the named turn ends; a retry of it after it finished is refused and changes nothing
		if c, j := pgCall(t, "POST", base+"/next", gmTok, map[string]interface{}{"expected_turn": turn(1, 0)}); c != 200 || j["turn_index"] != 1.0 {
			t.Fatalf("schedule %s: next with the current turn: %d %v", state, c, j)
		}
		c, j := pgCall(t, "POST", base+"/next", gmTok, map[string]interface{}{"expected_turn": turn(1, 0)})
		if c != 409 || j["error"] != "turn_already_moved" || j["turn_index"] != 1.0 || j["round"] != 1.0 || j["current_turn_id"] != -2.0 {
			t.Fatalf("schedule %s: a retried next: %d %v", state, c, j)
		}
		if r, i := pgTurn(t, camp); r != 1 || i != 1 {
			t.Fatalf("schedule %s: a refused next moved the turn to %d:%d", state, r, i)
		}
		// combat/skip with a turn that has ended: refused before its own checks (a monster's turn, the timeout)
		if c, j := pgCall(t, "POST", base+"/skip", gmTok, map[string]interface{}{"expected_turn": turn(1, 0)}); c != 409 || j["error"] != "turn_already_moved" {
			t.Fatalf("schedule %s: a retried skip: %d %v", state, c, j)
		}
		// narrate's advance_turn with a turn that has ended: refused before the narration is recorded
		text := fmt.Sprintf("The goblins regroup (%s).", state)
		c, j = pgCall(t, "POST", "/api/gm/narrate", gmTok, map[string]interface{}{"campaign_id": camp, "narration": text, "advance_turn": true, "expected_turn": turn(1, 0)})
		if c != 409 || j["error"] != "turn_already_moved" || j["narration_recorded"] != false {
			t.Fatalf("schedule %s: a retried narrate: %d %v", state, c, j)
		}
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM actions WHERE lobby_id = $1 AND description = $2`, camp, text).Scan(&n)
		if r, i := pgTurn(t, camp); r != 1 || i != 1 || n != 0 {
			t.Fatalf("schedule %s: a refused narrate changed something: turn %d:%d, narration recorded %d times", state, r, i, n)
		}
		// narrate with the turn it is now ends it
		if c, j := pgCall(t, "POST", "/api/gm/narrate", gmTok, map[string]interface{}{"campaign_id": camp, "narration": text, "advance_turn": true, "expected_turn": turn(1, 1)}); c != 200 || j["turn_index"] != 2.0 {
			t.Fatalf("schedule %s: narrate with the current turn: %d %v", state, c, j)
		}
		// without expected_turn: as before (the turn moves, into round 2)
		if c, j := pgCall(t, "POST", base+"/next", gmTok, nil); c != 200 || j["round"] != 2.0 || j["turn_index"] != 0.0 {
			t.Fatalf("schedule %s: next without expected_turn: %d %v", state, c, j)
		}
		// an incomplete expected_turn, and one without advance_turn, are refused with nothing changed
		for _, body := range []map[string]interface{}{{"expected_turn": map[string]interface{}{"round": 2}}, {"expected_turn": turn(0, 0)}, {"expected_turn": turn(2, -1)}} {
			if c, j := pgCall(t, "POST", base+"/next", gmTok, body); c != 400 || j["error"] != "invalid_expected_turn" {
				t.Fatalf("schedule %s: next with %v: %d %v", state, body, c, j)
			}
		}
		if c, j := pgCall(t, "POST", "/api/gm/narrate", gmTok, map[string]interface{}{"campaign_id": camp, "narration": text, "expected_turn": turn(2, 0)}); c != 400 || j["error"] != "invalid_expected_turn" {
			t.Fatalf("schedule %s: expected_turn without advance_turn: %d %v", state, c, j)
		}
		if r, i := pgTurn(t, camp); r != 2 || i != 0 {
			t.Fatalf("schedule %s: a refused request moved the turn to %d:%d", state, r, i)
		}
	}
	// narrate's advance_turn without a fight: refused before the narration is recorded (the turn owner's no_active_combat)
	pgExec(t, `UPDATE combat_state SET active = false WHERE lobby_id = $1`, camp)
	if c, j := pgCall(t, "POST", "/api/gm/narrate", gmTok, map[string]interface{}{"campaign_id": camp, "narration": "Silence.", "advance_turn": true}); c != 409 || j["error"] != "no_active_combat" || j["narration_recorded"] != false {
		t.Fatalf("narrate's advance_turn without a fight: %d %v", c, j)
	}
}
