package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
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

func TestPostgresBattleMapRiver(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	database, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	original := db
	db = database
	t.Cleanup(func() { db = original; database.Close() })
	if _, err := db.Exec(`
 CREATE TEMP TABLE agents(id int, password_hash text, salt text, verified bool);
 CREATE TEMP TABLE lobbies(id int, dm_id int, status text);
 CREATE TEMP TABLE combat_state(lobby_id int, current_turn_index int, turn_started_at timestamp, battle_map jsonb, turn_order jsonb, active bool, round_number int);
 CREATE TEMP TABLE characters(id int, lobby_id int, agent_id int, hp int, is_dead bool, movement_remaining int, exhaustion_level int, conditions jsonb, action_used bool, bonus_action_used bool DEFAULT false, class_resources_used jsonb DEFAULT '{}');
 CREATE TEMP TABLE actions(lobby_id int, character_id int, action_type text, description text, result text);
 INSERT INTO lobbies VALUES (1, 1, 'active');
 INSERT INTO combat_state VALUES (1, 0, CURRENT_TIMESTAMP, NULL, '[{"id":10},{"id":-1}]', true, 1);
 INSERT INTO characters VALUES (10, 1, 2, 10, false, 30, 0, '[]', false);
 `); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO agents VALUES (1,$1,'',true),(2,$1,'',true),(3,$1,'',true)", testPasswordHash("test-password")); err != nil {
		t.Fatal(err)
	}
	call := func(agent, method, suffix string, body any, want int) map[string]any {
		t.Helper()
		encoded, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/api/campaigns/1/battle-map"+suffix, bytes.NewReader(encoded))
		r.SetBasicAuth(agent, "test-password")
		w := httptest.NewRecorder()
		handleCampaignByID(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: want %d, got %d: %s", method, suffix, want, w.Code, w.Body.String())
		}
		result := map[string]any{}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	m := encounterMap{Grid: []string{".....", ".....", ".~..."}, Tokens: []mapToken{
		{ID: 10, Name: "Fighter", Position: mapSquare{0, 1}, Size: 1},
		{ID: -1, Name: "Hidden toad", Position: mapSquare{4, 0}, Size: 1, Hidden: true},
	}, Interrupts: []mapSquare{{2, 1}}}
	call("1", "POST", "/preview", mapRequest{Map: m}, 200)
	published := call("1", "PUT", "", mapRequest{Map: m}, 200)
	call("3", "GET", "?character_id=10", nil, 403)
	player := call("2", "GET", "?character_id=10", nil, 200)
	public, _ := json.Marshal(player)
	if strings.Contains(string(public), "Hidden toad") || strings.Contains(string(public), "interrupts") {
		t.Fatal("player view leaked hidden map data")
	}
	tooFar := mapRequest{CharacterID: 10, Path: []mapSquare{{1, 1}, {0, 1}, {1, 1}, {0, 1}, {1, 1}, {0, 1}, {1, 1}}}
	if result := call("2", "POST", "/preview", tooFar, 400); result["step"] != float64(7) {
		t.Fatalf("expected the seventh step to exceed 30 feet: %v", result)
	}
	path := mapRequest{CharacterID: 10, Path: []mapSquare{{1, 1}, {2, 1}, {3, 1}}}
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
	if err := db.QueryRow("SELECT movement_remaining,(SELECT COUNT(*) FROM actions WHERE action_type='map_move') FROM characters WHERE id=10").Scan(&remaining, &moves); err != nil || remaining != 20 || moves != 1 {
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
	path = mapRequest{CharacterID: 10, Path: []mapSquare{{4, 1}}}
	call("2", "POST", "/preview", path, 400)
	if err := spendDashAction(10, 30); err != nil {
		t.Fatal(err)
	}
	if err := spendDashAction(10, 30); err != sql.ErrNoRows {
		t.Fatalf("expected second Dash to be rejected, got %v", err)
	}
	preview = call("2", "POST", "/preview", path, 200)
	path.Confirmation = preview["confirmation"].(string)
	move = call("2", "POST", "/move", path, 200)
	if move["movement_remaining_ft"] != float64(25) {
		t.Fatalf("expected one Dash to fund the 5-foot move: %v", move)
	}
	if err := spendBonusDash(10, 30, 0); err != nil {
		t.Fatal(err)
	}
	if err := spendBonusDash(10, 30, 0); err != sql.ErrNoRows {
		t.Fatalf("expected a second bonus Dash to be rejected: %v", err)
	}
	var normalUsed, bonusUsed bool
	if err := db.QueryRow("SELECT movement_remaining, action_used, bonus_action_used FROM characters WHERE id=10").Scan(&remaining, &normalUsed, &bonusUsed); err != nil || remaining != 55 || !normalUsed || !bonusUsed {
		t.Fatalf("expected action and bonus-action Dash to stack once each: %d %v %v %v", remaining, normalUsed, bonusUsed, err)
	}
	if _, err := db.Exec("UPDATE characters SET action_used=false, bonus_action_used=false WHERE id=10"); err != nil {
		t.Fatal(err)
	}
	if err := spendBonusDash(10, 30, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE characters SET bonus_action_used=false WHERE id=10"); err != nil {
		t.Fatal(err)
	}
	if err := spendBonusDash(10, 30, 1); err != sql.ErrNoRows {
		t.Fatalf("expected spent Focus Points to reject bonus Dash: %v", err)
	}
	if err := db.QueryRow("SELECT movement_remaining, action_used, bonus_action_used FROM characters WHERE id=10").Scan(&remaining, &normalUsed, &bonusUsed); err != nil || remaining != 85 || normalUsed || bonusUsed {
		t.Fatalf("expected spent Focus Points to leave movement and actions alone: %d %v %v %v", remaining, normalUsed, bonusUsed, err)
	}
	if err := clearCombatInitiative(1); err != nil {
		t.Fatal(err)
	}
	call("2", "GET", "?character_id=10", nil, 404)
}
