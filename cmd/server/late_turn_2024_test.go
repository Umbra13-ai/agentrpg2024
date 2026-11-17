package main

// I-KESTREL-7 (feedback item 65): after a round's final check-in the status said the GM "takes a stand-in" while the player could
// still send its turn (check-in times are recommendations: a late turn is taken until the GM takes the stand-in); the server named a
// fight's round only by the fight's count while the GM counted campaign rounds; and an action that left nothing of the turn didn't
// say the turn was complete.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The needs_stand_in status says when the GM may take the stand-in (its next check-in) and that the player can still send its turn
// until then, from standInAllowed and the schedule's GM check-ins, at the final check-in and after it.
func TestStandInStatusSaysTheTurnCanStillBeSent(t *testing.T) {
	in := exampleInputs(at("09:55:00")) // final check-in 10:55; GM check-ins :10 :25 :40 :55
	in.Fight.Current, in.Fight.SlotSince = 5, at("10:40:40")
	for now, gm := range map[string]string{"10:55:00": "10:55", "10:56:00": "11:10", "11:26:30": "11:40"} {
		s := statusOfChar(t, checkInAt(in, at(now)), dev)
		want := fmt.Sprintf("the GM may take a stand-in turn for Dev at the GM's next check-in (%s UTC). Until the GM does, Dev can still send the turn.", gm)
		if s.Status != statusStandIn || !strings.Contains(s.Message, want) || !roundTurnOpen(s.Status) {
			t.Errorf("%s: %s %q, want %q", now, s.Status, s.Message, want)
		}
	}
	in.State = schedulePaused // paused: no stand-in at all
	if s := statusOfChar(t, checkInAt(in, at("10:56:00")), dev); s.Status == statusStandIn {
		t.Errorf("paused: %s %q", s.Status, s.Message)
	}
}

// One function names a round: both counts in a fight, the campaign round outside one; the round views and the round-complete line
// use it.
func TestRoundNamesSayBothCounts(t *testing.T) {
	fight := &roundRecord{Number: 1, CampaignRound: 3, Kind: roundCombat}
	if got := fight.name(); got != "campaign round 3, fight round 1" {
		t.Errorf("a fight's round: %q", got)
	}
	if got := (&roundRecord{Number: 4, CampaignRound: 4, Kind: roundNoncombat}).Name(); got != "Campaign round 4 (outside a fight)" {
		t.Errorf("a round outside a fight: %q", got)
	}
	next := &roundRecord{Number: 2, CampaignRound: 4, Kind: roundCombat, Deadline: nullTime(at("10:55:00"))}
	if got := roundCompleteText(1, next); !strings.HasPrefix(got, "Campaign round 3, fight round 1 is complete; campaign round 4, fight round 2 has begun") {
		t.Errorf("round complete: %q", got)
	}
	in := exampleInputs(at("09:55:00"))
	in.Round.CampaignRound = 3
	if r := checkInAt(in, at("10:00:00")).Round; r.Name != "campaign round 3, fight round 1" || r.CampaignRound != 3 || r.Number != 1 {
		t.Errorf("the round view: %+v", r)
	}
	if got := gmReminder(checkInAt(in, at("10:00:00"))); !strings.HasPrefix(got, "Campaign round 3, fight round 1:") {
		t.Errorf("the GM's reminder: %q", got)
	}
}

// Through the server, in a fight with the turn schedule: the round opened in the fight is named with both counts (the party chat,
// my-turn, GET .../round); after the final check-in my-turn says the GM may take the stand-in at its next check-in and the player can
// still send its turn, and a turn sent then is accepted.
func TestPostgresLateTurnBeforeTheStandIn(t *testing.T) {
	t.Setenv(testClockEnv, "1")
	testPostgres(t)
	_, gm := pgAgent(t, "pg late gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Late", "max_players": 2}) // seats :00 :30, GM :25 :55
	lobby := int(j["campaign_id"].(float64))
	base := fmt.Sprintf("/api/campaigns/%d", lobby)
	_, tok := pgAgent(t, "pg late player")
	_, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Late %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	if c, j := pgCall(t, "POST", base+"/join", tok, map[string]interface{}{"character_id": char}); c != 200 {
		t.Fatalf("join: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", base+"/test-clock", gm, map[string]interface{}{"at": "2026-11-16T10:00:00Z"}); c != 200 {
		t.Fatalf("test clock: %d %v", c, j)
	}
	pgFinish(t, tok, char)
	pgCall(t, "POST", base+"/ready", tok, nil)
	pgCall(t, "POST", base+"/ready", gm, nil)
	if c, j := pgCall(t, "POST", base+"/start", gm, nil); c != 200 { // campaign round 1, outside a fight
		t.Fatalf("start: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", base+"/combat/start", gm, map[string]interface{}{}); c != 200 { // campaign round 2, fight round 1
		t.Fatalf("combat start: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", base+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Late Goblin", "monster_key": "goblin"}}}); c != 200 {
		t.Fatalf("add: %d %v", c, j)
	}
	pgExec(t, `UPDATE combat_state SET current_turn_index = 0 WHERE lobby_id = $1`, lobby)

	_, j = pgCall(t, "GET", fmt.Sprintf("/api/campaigns/messages?campaign_id=%d", lobby), tok, nil)
	if !strings.Contains(fmt.Sprint(j), "Campaign round 2, fight round 1 began") {
		t.Errorf("the party chat doesn't name the fight's round with both counts: %.600v", j)
	}
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/my-turn?campaign_id=%d", lobby), tok, nil)
	r, _ := j["round"].(map[string]interface{})
	if r == nil || r["name"] != "campaign round 2, fight round 1" || asInt(r["campaign_round"]) != 2 || asInt(r["round"]) != 1 {
		t.Errorf("my-turn's round: %v", r)
	}
	if _, j := pgCall(t, "GET", base+"/round", tok, nil); !strings.Contains(fmt.Sprint(j["check_in"]), "campaign round 2, fight round 1") {
		t.Errorf("GET .../round doesn't name the round: %.400v", j["check_in"])
	}

	if c, j := pgCall(t, "POST", base+"/test-clock", gm, map[string]interface{}{"advance_minutes": 56}); c != 200 { // 10:56: past the final check-in
		t.Fatalf("advance: %d %v", c, j)
	}
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/my-turn?campaign_id=%d", lobby), tok, nil)
	r, _ = j["round"].(map[string]interface{})
	msg := fmt.Sprint(r["message"])
	if r["status"] != statusStandIn || !strings.Contains(msg, "may take a stand-in turn") || !strings.Contains(msg, "(11:25 UTC)") || !strings.Contains(msg, "can still send the turn") {
		t.Errorf("my-turn after the final check-in: %v", r)
	}
	if c, j := pgCall(t, "POST", "/api/pending-turn", tok, map[string]interface{}{"character_id": char, "steps": []map[string]interface{}{{"action": "dodge"}}}); c != 200 {
		t.Errorf("a late turn before the stand-in is refused: %d %v", c, j)
	}
	if _, j := pgCall(t, "GET", fmt.Sprintf("/api/my-turn?campaign_id=%d", lobby), tok, nil); fmt.Sprint(j["round"].(map[string]interface{})["status"]) == statusStandIn {
		t.Errorf("the late turn wasn't counted: %v", j["round"])
	}
}

// An action that leaves nothing of the turn (no Action, no Bonus Action anything uses: read from turn_left) says the turn is
// complete and names who is next in the turn order; while the Action is left it says nothing.
func TestPostgresTurnCompleteNamesWhoIsNext(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "pg done gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Done", "max_players": 2})
	lobby := int(j["campaign_id"].(float64))
	base := fmt.Sprintf("/api/campaigns/%d", lobby)
	pgCall(t, "PATCH", base+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, tok := pgAgent(t, "pg done player")
	_, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Done %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", base+"/join", tok, map[string]interface{}{"character_id": char})
	pgFinish(t, tok, char)
	pgCall(t, "POST", base+"/ready", tok, nil)
	pgCall(t, "POST", base+"/ready", gm, nil)
	if c, j := pgCall(t, "POST", base+"/start", gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	order := fmt.Sprintf(`[{"id":%d,"name":"Pg Done","initiative":20},{"id":-1,"name":"Done Goblin A","initiative":15,"hp":0,"is_monster":true},{"id":-2,"name":"Done Goblin B","initiative":10,"hp":7,"is_monster":true}]`, char)
	pgExec(t, `INSERT INTO combat_state (lobby_id, round_number, current_turn_index, turn_order, turn_started_at, active) VALUES ($1, 1, 0, $2, NOW(), true)
		ON CONFLICT (lobby_id) DO UPDATE SET round_number = 1, current_turn_index = 0, turn_order = EXCLUDED.turn_order, turn_started_at = NOW(), active = true, turn_claim = ''`, lobby, order)
	pgExec(t, `UPDATE characters SET action_used = false, bonus_action_used = true, attacks_remaining = NULL, movement_remaining = 30 WHERE id = $1`, char)

	if c, j := pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "move", "movement_cost": 5, "description": "I step back"}); c != 200 || j["turn_complete"] != nil {
		t.Errorf("the Action is still left: %d turn_complete %v", c, j["turn_complete"])
	}
	c, j := pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "dodge"})
	done := fmt.Sprint(j["turn_complete"])
	if c != 200 || !strings.Contains(done, "Your turn is complete") || !strings.Contains(done, "Done Goblin B") || !strings.Contains(done, "25 ft") {
		t.Errorf("after the Action with the Bonus Action spent: %d %q (turn_left %v)", c, done, j["turn_left"])
	}
}
