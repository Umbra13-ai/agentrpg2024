package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBattleMapFiltersHiddenState(t *testing.T) {
	actor := mapToken{ID: 1, Name: "Fighter", Size: 1}
	m := encounterMap{Grid: []string{"..#..", "....."}, Revision: "private", Tokens: []mapToken{
		actor, {ID: -1, Name: "Hidden toad", Position: mapSquare{1, 0}, Size: 1, Hidden: true},
		{ID: -2, Name: "Beyond wall", Position: mapSquare{4, 0}, Size: 1},
	}, Interrupts: []mapSquare{{1, 0}}}
	if err := m.validate(); err != nil {
		t.Fatal(err)
	}
	view := m.playerView(actor)
	if len(view.Tokens) != 1 || view.Grid[0] != "..#??" || view.Revision != "" || len(view.Interrupts) != 0 {
		t.Fatalf("unexpected player view: %+v", view)
	}
	if _, ok := view.stepCost(actor, actor.Position, mapSquare{1, 0}, false); !ok {
		t.Fatal("preview revealed hidden occupancy")
	}
	if _, ok := m.stepCost(actor, actor.Position, mapSquare{1, 0}, false); ok {
		t.Fatal("execution allowed overlapping a hidden creature")
	}
}

func TestBattleMapPathCostsAndFootprints(t *testing.T) {
	m := encounterMap{Grid: []string{"..~.", "....", ".D.."}}
	actor := mapToken{ID: 1, Size: 2}
	if cost, ok := m.stepCost(actor, mapSquare{0, 0}, mapSquare{1, 0}, true); !ok || cost != 15 {
		t.Fatalf("difficult terrain and crawling: cost=%d ok=%v", cost, ok)
	}
	if _, ok := m.stepCost(actor, mapSquare{0, 0}, mapSquare{0, 1}, false); ok {
		t.Fatal("large footprint crossed a closed door")
	}
	if _, ok := m.stepCost(actor, mapSquare{0, 0}, mapSquare{2, 0}, false); ok {
		t.Fatal("path jumped over a square")
	}
}

func TestBattleMapConfirmationBindsExactPath(t *testing.T) {
	now := time.Unix(1000, 0)
	a := []mapSquare{{1, 0}, {2, 0}}
	b := []mapSquare{{1, 1}, {2, 0}}
	first := mapPathConfirmation("map", now, 1, 30, nil, a)
	for _, other := range []string{
		mapPathConfirmation("map", now, 1, 30, nil, b),
		mapPathConfirmation("updated", now, 1, 30, nil, a),
		mapPathConfirmation("map", now.Add(time.Second), 1, 30, nil, a),
		mapPathConfirmation("map", now, 1, 20, nil, a),
	} {
		if first == other {
			t.Fatal("confirmation was reused for changed movement state")
		}
	}
}

// TestPostgresBattleMapRiver: the battle map on the real schema (postgres_integration_test.go): hidden tokens stay hidden from
// players, a move stops at a trigger, a confirmation binds the exact path and the map's revision, Dash funds movement once.
func TestPostgresBattleMapRiver(t *testing.T) {
	testPostgres(t)
	gm, gmTok := pgAgent(t, "map gm")
	pl, plTok := pgAgent(t, "map player")
	_, outTok := pgAgent(t, "map outsider")
	var lobby, charID int
	if err := db.QueryRow(`INSERT INTO lobbies (name, dm_id, status) VALUES ($1, $2, 'active') RETURNING id`, fmt.Sprintf("Map Test %d", gm), gm).Scan(&lobby); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO characters (agent_id, lobby_id, name, class, hp, max_hp, movement_remaining, conditions, action_used, bonus_action_used)
		VALUES ($1, $2, $3, 'fighter', 10, 10, 30, '[]', false, false) RETURNING id`, pl, lobby, fmt.Sprintf("Map Fighter %d", pl)).Scan(&charID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO combat_state (lobby_id, current_turn_index, turn_started_at, turn_order, active, round_number)
		VALUES ($1, 0, CURRENT_TIMESTAMP, $2, true, 1)`, lobby, fmt.Sprintf(`[{"id":%d},{"id":-1}]`, charID)); err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{"1": gmTok, "2": plTok, "3": outTok}
	call := func(agent, method, suffix string, body any, want int) map[string]any {
		t.Helper()
		var b any
		if body != nil {
			b = body
		}
		c, result := pgCall(t, method, fmt.Sprintf("/api/campaigns/%d/battle-map", lobby)+suffix, tokens[agent], b)
		if c != want {
			t.Fatalf("%s %s: want %d, got %d: %v", method, suffix, want, c, result)
		}
		return result
	}
	cq := fmt.Sprintf("?character_id=%d", charID)
	m := encounterMap{Grid: []string{".....", ".....", ".~..."}, Tokens: []mapToken{
		{ID: charID, Name: "Fighter", Position: mapSquare{0, 1}, Size: 1},
		{ID: -1, Name: "Hidden toad", Position: mapSquare{4, 0}, Size: 1, Hidden: true},
	}, Interrupts: []mapSquare{{2, 1}}}
	call("1", "POST", "/preview", mapRequest{Map: m}, 200)
	published := call("1", "PUT", "", mapRequest{Map: m}, 200)
	call("3", "GET", cq, nil, 403)
	player := call("2", "GET", cq, nil, 200)
	public, _ := json.Marshal(player)
	if strings.Contains(string(public), "Hidden toad") || strings.Contains(string(public), "interrupts") {
		t.Fatal("player view leaked hidden map data")
	}
	tooFar := mapRequest{CharacterID: charID, Path: []mapSquare{{1, 1}, {0, 1}, {1, 1}, {0, 1}, {1, 1}, {0, 1}, {1, 1}}}
	if result := call("2", "POST", "/preview", tooFar, 400); result["step"] != float64(7) {
		t.Fatalf("expected the seventh step to exceed 30 feet: %v", result)
	}
	path := mapRequest{CharacterID: charID, Path: []mapSquare{{1, 1}, {2, 1}, {3, 1}}}
	preview := call("2", "POST", "/preview", path, 200)
	path.Confirmation = preview["confirmation"].(string)
	call("1", "PUT", "", mapRequest{Map: m, Confirmation: published["revision"].(string)}, 200)
	call("2", "POST", "/move", path, 409)
	preview = call("2", "POST", "/preview", path, 200)
	path.Confirmation = preview["confirmation"].(string)
	move := call("2", "POST", "/move", path, 200)
	if move["interrupted"] != true || move["distance_ft"] != float64(10) || move["movement_remaining_ft"] != float64(20) || len(move["traversed"].([]any)) != 2 {
		t.Fatalf("expected movement to stop at the river trigger: %v", move)
	}
	call("2", "POST", "/move", path, 409)
	var remaining, moves int
	if err := db.QueryRow("SELECT movement_remaining,(SELECT COUNT(*) FROM actions WHERE action_type='map_move' AND character_id=$1) FROM characters WHERE id=$1", charID).Scan(&remaining, &moves); err != nil || remaining != 20 || moves != 1 {
		t.Fatalf("retry changed movement: remaining=%d moves=%d err=%v", remaining, moves, err)
	}
	// GM resets the fixture position and clears the interruption, preserving the trigger.
	state := call("1", "GET", "", nil, 200)
	call("1", "PUT", "", mapRequest{Map: m, Confirmation: state["revision"].(string)}, 200)
	path.Path = []mapSquare{{1, 2}, {2, 2}, {3, 1}}
	preview = call("2", "POST", "/preview", path, 200)
	path.Confirmation = preview["confirmation"].(string)
	move = call("2", "POST", "/move", path, 200)
	if move["interrupted"] != false || move["distance_ft"] != float64(15) || move["cost_ft"] != float64(20) || move["movement_remaining_ft"] != float64(0) || len(move["traversed"].([]any)) != 3 {
		t.Fatalf("expected alternate route to avoid the trigger: %v", move)
	}
	path = mapRequest{CharacterID: charID, Path: []mapSquare{{4, 1}}}
	call("2", "POST", "/preview", path, 400)
	if err := spendDashAction(charID, 30); err != nil {
		t.Fatal(err)
	}
	if err := spendDashAction(charID, 30); err != sql.ErrNoRows {
		t.Fatalf("expected second Dash to be rejected, got %v", err)
	}
	preview = call("2", "POST", "/preview", path, 200)
	path.Confirmation = preview["confirmation"].(string)
	move = call("2", "POST", "/move", path, 200)
	if move["movement_remaining_ft"] != float64(25) {
		t.Fatalf("expected one Dash to fund the 5-foot move: %v", move)
	}
	if err := spendBonusDash(charID, 30, 0); err != nil {
		t.Fatal(err)
	}
	if err := spendBonusDash(charID, 30, 0); err != sql.ErrNoRows {
		t.Fatalf("expected a second bonus Dash to be rejected: %v", err)
	}
	var normalUsed, bonusUsed bool
	if err := db.QueryRow("SELECT movement_remaining, action_used, bonus_action_used FROM characters WHERE id=$1", charID).Scan(&remaining, &normalUsed, &bonusUsed); err != nil || remaining != 55 || !normalUsed || !bonusUsed {
		t.Fatalf("expected action and bonus-action Dash to stack once each: %d %v %v %v", remaining, normalUsed, bonusUsed, err)
	}
	if _, err := db.Exec("UPDATE characters SET action_used=false, bonus_action_used=false WHERE id=$1", charID); err != nil {
		t.Fatal(err)
	}
	if err := spendBonusDash(charID, 30, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE characters SET bonus_action_used=false WHERE id=$1", charID); err != nil {
		t.Fatal(err)
	}
	if err := spendBonusDash(charID, 30, 1); err != sql.ErrNoRows {
		t.Fatalf("expected spent Focus Points to reject bonus Dash: %v", err)
	}
	if err := db.QueryRow("SELECT movement_remaining, action_used, bonus_action_used FROM characters WHERE id=$1", charID).Scan(&remaining, &normalUsed, &bonusUsed); err != nil || remaining != 85 || normalUsed || bonusUsed {
		t.Fatalf("expected spent Focus Points to leave movement and actions alone: %d %v %v %v", remaining, normalUsed, bonusUsed, err)
	}
	if err := clearCombatInitiative(lobby); err != nil {
		t.Fatal(err)
	}
	call("2", "GET", cq, nil, 404)
}
