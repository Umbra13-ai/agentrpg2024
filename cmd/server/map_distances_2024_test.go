package main

// my-turn gives how far each creature is on the battle map (distancesFrom): with each enemy in the situation (the line and the details) and
// each party member, in the full and the short view; nothing without a map.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestPostgresMyTurnDistancesOnTheMap(t *testing.T) {
	testPostgres(t)
	gm, player, camp, hero, raider := earlyRulesFixture(t, "Test Far Raider", []srdMonsterEntry{{Name: "Club", Text: "Melee Attack Roll: +4, reach 5 ft. Hit: 2 (1d4) Bludgeoning damage."}})
	short := func() map[string]interface{} {
		_, j := pgCall(t, "GET", fmt.Sprintf("/api/my-turn?character_id=%d&view=short", hero), player, nil)
		return j
	}
	if b, _ := json.Marshal(short()["situation"]); strings.Contains(string(b), "distance_ft") || strings.Contains(string(b), "ft away") {
		t.Errorf("no map, yet distances: %s", b)
	}
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
	req := map[string]interface{}{"map": encounterMap{Grid: []string{"........", "........", "........"}, Tokens: []mapToken{
		{ID: hero, Name: "hero", Position: mapSquare{0, 0}, Size: 1},
		{ID: raider, Name: "raider", Position: mapSquare{3, 2}, Size: 1}}}, "forced_movement": true}
	if rev, ok := state["revision"].(string); ok {
		req["confirmation"] = rev
	}
	if c, j := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
		t.Fatalf("battle map: %d %v", c, j)
	}
	s, _ := short()["situation"].(map[string]interface{})
	b, _ := json.Marshal(s)
	if !strings.Contains(fmt.Sprint(s["enemies"]), "15 ft away") || !strings.Contains(string(b), `"distance_ft":15`) {
		t.Errorf("3 squares across, 2 down (a diagonal is one square): 15 ft; got %s", b)
	}
}
