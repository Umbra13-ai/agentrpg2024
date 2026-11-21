package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// I-CALLA-22: a record with no character keeps its description in the players' line ("GM gave Robe to Calla → x2"), not only
// its result ("x2").
func TestEventLineKeepsAGMRecordsDescription(t *testing.T) {
	for _, a := range []actionRecord{
		{Actor: "RookDM", Type: "item_given", Description: "GM gave Robe to Calla", Result: "x2"},
		{Actor: "Calla", CharacterID: 3, Type: "search", Description: "looks under the cot", Result: "finds a key"},
	} {
		if got := a.eventLine(); !strings.Contains(got, a.Description) || !strings.Contains(got, a.Result) {
			t.Errorf("eventLine dropped a part: %q", got)
		}
	}
	// a result that already holds the description is said once
	a := actionRecord{Actor: "RookDM", Type: "note", Description: "the gate", Result: "the gate opens"}
	if got := a.eventLine(); strings.Count(got, "the gate") != 1 {
		t.Errorf("said twice: %q", got)
	}
}

// I-CALLA-23: a rest a player takes is written to the campaign's log, Long and Short, so the GM sees it.
func TestPostgresRestsAreLogged(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "pg rest gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Rest", "max_players": 2})
	lobby := int(j["campaign_id"].(float64))
	pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/schedule", lobby), gm, map[string]interface{}{"state": "off"})
	_, tok := pgAgent(t, "pg rest player")
	_, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Rest %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", lobby), tok, map[string]interface{}{"character_id": char})
	pgFinish(t, tok, char)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), tok, nil)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), gm, nil)
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", lobby), gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	count := func(typ string) int {
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM actions WHERE lobby_id = $1 AND character_id = $2 AND action_type = $3`, lobby, char, typ).Scan(&n)
		return n
	}
	pgExec(t, `UPDATE characters SET hp = GREATEST(hp - 3, 1) WHERE id = $1`, char)
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/characters/%d/short-rest", char), tok, map[string]interface{}{"hit_dice": 0}); c != 200 {
		t.Fatalf("short rest: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/characters/%d/rest", char), tok, nil); c != 200 {
		t.Fatalf("long rest: %d %v", c, j)
	}
	if count("short_rest") != 1 || count("long_rest") != 1 {
		t.Errorf("rests in the log: short %d, long %d", count("short_rest"), count("long_rest"))
	}
}
