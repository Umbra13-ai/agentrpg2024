package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Feedback log 71 (I-ALDRIC-3, I-KESTREL-7): the round names itself apart from a fight's count; a seat's window says when to send;
// a stand-in status says the turn can still be sent until the GM takes the stand-in.
func TestCheckInWindowAndRoundName(t *testing.T) {
	if roundName(roundCombat, 1) != "fight round 1" || roundName(roundNoncombat, 3) != "campaign round 3" || roundTitle(roundNoncombat, 3) != "Campaign round 3" {
		t.Errorf("round names: %q %q", roundName(roundCombat, 1), roundName(roundNoncombat, 3))
	}
	in := exampleInputs(at("09:55:00")) // Dev's seat :45; window 10:45-10:55
	submit(&in, ann, at("10:00:20"))
	c := checkInAt(in, at("10:12:00"))
	if c.Round.Name != "fight round 1" {
		t.Errorf("round view name %q", c.Round.Name)
	}
	w := statusOfChar(t, c, dev).Window
	if w == nil || !w.OpensAt.Equal(at("10:45:00")) || !w.ClosesAt.Equal(at("10:55:00")) || w.Open || w.SendNow {
		t.Errorf("10:12 Dev's window: %+v", w)
	}
	if w := statusOfChar(t, c, ann).Window; w == nil || w.SendNow {
		t.Errorf("a submitted turn's window says send now: %+v", w)
	}
	c = checkInAt(in, at("10:47:00"))
	if w := statusOfChar(t, c, dev).Window; w == nil || !w.Open || !w.SendNow {
		t.Errorf("10:47 Dev's window: %+v", w)
	}
	c = checkInAt(in, at("10:56:00"))
	expect(t, "10:56", c, dev, statusStandIn, "you can still send your turn until the GM takes a stand-in turn for Dev at its next check-in (11:10 UTC)")
	if w := statusOfChar(t, c, dev).Window; w == nil || w.Open || !w.SendNow {
		t.Errorf("10:56 Dev's window: %+v", w)
	}
	c = checkInAt(in, at("10:55:00"))
	expect(t, "10:55", c, dev, statusStandIn, "at its next check-in (10:55 UTC)")
	r := &roundRecord{Number: 1, Kind: roundCombat, OpenedAt: at("10:00:00"), Deadline: nullTime(at("10:55:00"))}
	if l := roundOpenedLine(r); !strings.HasPrefix(l, "Fight round 1 began at") {
		t.Errorf("%q", l)
	}
}

// Feedback log 66 (I-ALDRIC-1, I-ALDRIC-5) through the server: the party list, the player's own status and hints, the spectator view
// and the GM's party view all read the one health label; 73 (I-ALDRIC-2): my-turn gives the map's URL only when the player's view
// works, else why; S-ALDEN-11: a pending turn's action warning comes from the real budget.
func TestPostgresHealthLabelsMapAndActionBudget(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "pg health gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Health", "max_players": 2})
	lobby := int(j["campaign_id"].(float64))
	pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/schedule", lobby), gm, map[string]interface{}{"state": "off"})
	var toks []string
	var chars []int
	for i := 0; i < 2; i++ {
		_, tok := pgAgent(t, fmt.Sprintf("pg health player %d", i))
		_, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Health %d %d", i, time.Now().UnixNano()%1e9)))
		id := int(j["character_id"].(float64))
		pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", lobby), tok, map[string]interface{}{"character_id": id})
		pgFinish(t, tok, id)
		pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), tok, nil)
		toks, chars = append(toks, tok), append(chars, id)
	}
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), gm, nil)
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", lobby), gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	a, b := chars[0], chars[1]
	pgExec(t, `UPDATE characters SET hp = 0, is_dead = true WHERE id = $1`, b)
	pgExec(t, `UPDATE characters SET hp = 0, is_stable = true, is_dead = false WHERE id = $1`, a)
	myTurn := func() map[string]interface{} {
		t.Helper()
		c, j := pgCall(t, "GET", fmt.Sprintf("/api/my-turn?campaign_id=%d", lobby), toks[0], nil)
		if c != 200 {
			t.Fatalf("my-turn: %d %v", c, j)
		}
		return j
	}
	partyLabel := func(j map[string]interface{}, key string) string {
		for _, p := range j[key].([]interface{}) {
			m := p.(map[string]interface{})
			if strings.HasPrefix(fmt.Sprint(m["name"]), "Pg Health 1") {
				return fmt.Sprint(m["status"])
			}
		}
		return ""
	}
	j = myTurn()
	hints := fmt.Sprint(j["tactical_suggestions"])
	if st := j["character"].(map[string]interface{})["status"]; st != "stable" || strings.Contains(hints, "death save with") || !strings.Contains(hints, "no Death Saving Throws") {
		t.Errorf("stable: status %v, hints %s", st, hints)
	}
	if l := partyLabel(j, "party_status"); l != "dead" || strings.Contains(hints, "Pg Health 1") {
		t.Errorf("dead ally: %q, hints %s", l, hints)
	}
	pgExec(t, `UPDATE characters SET is_stable = false WHERE id = $1`, a)
	if j = myTurn(); !strings.Contains(fmt.Sprint(j["tactical_suggestions"]), `"death_save"`) || j["character"].(map[string]interface{})["status"] != "dying" {
		t.Errorf("dying outside a fight: %v %v", j["character"].(map[string]interface{})["status"], j["tactical_suggestions"])
	}
	_, sp := pgCall(t, "GET", fmt.Sprintf("/api/campaigns/%d/spectate", lobby), "", nil)
	raw, _ := json.Marshal(sp)
	if !strings.Contains(string(raw), `"hp_status":"dead"`) || !strings.Contains(string(raw), `"hp_status":"dying"`) {
		t.Errorf("spectator party: %s", raw)
	}
	_, gs := pgCall(t, "GET", fmt.Sprintf("/api/gm/status?campaign_id=%d", lobby), gm, nil)
	if l := partyLabel(gs, "party_status"); l != "dead" {
		t.Errorf("GM party view: %q", l)
	}

	// the map: none published, then a map without this character's token, then with it
	pgExec(t, `UPDATE characters SET hp = max_hp, is_dead = false, is_stable = false WHERE id IN ($1, $2)`, a, b)
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/start", lobby), gm, map[string]interface{}{}); c != 200 {
		t.Fatalf("combat start: %d %v", c, j)
	}
	if j = myTurn(); j["battle_map_url"] != nil || !strings.Contains(fmt.Sprint(j["battle_map_unavailable"]), "hasn't published") {
		t.Errorf("no map: %v / %v", j["battle_map_url"], j["battle_map_unavailable"])
	}
	pgExec(t, `UPDATE combat_state SET battle_map = $1 WHERE lobby_id = $2`, fmt.Sprintf(`{"grid":["....","...."],"tokens":[{"id":%d,"name":"B","position":{"x":1,"y":1},"size":1}],"revision":"r1"}`, b), lobby)
	if j = myTurn(); j["battle_map_url"] != nil || !strings.Contains(fmt.Sprint(j["battle_map_unavailable"]), "placed your token") {
		t.Errorf("no token: %v / %v", j["battle_map_url"], j["battle_map_unavailable"])
	}
	if c, j := pgCall(t, "GET", fmt.Sprintf("/api/campaigns/%d/battle-map?character_id=%d", lobby, a), toks[0], nil); c != 404 || j["error"] != "no_character_token" || !strings.Contains(fmt.Sprint(j["message"]), "placed your token") {
		t.Errorf("the map's own refusal: %d %v", c, j)
	}
	pgExec(t, `UPDATE combat_state SET battle_map = $1 WHERE lobby_id = $2`, fmt.Sprintf(`{"grid":["....","...."],"tokens":[{"id":%d,"name":"A","position":{"x":0,"y":0},"size":1}],"revision":"r2"}`, a), lobby)
	if j = myTurn(); !strings.Contains(fmt.Sprint(j["battle_map_url"]), "/battle-map?character_id=") || j["battle_map_unavailable"] != nil {
		t.Errorf("token placed: %v / %v", j["battle_map_url"], j["battle_map_unavailable"])
	}

	// the action budget: a Fighter 5 holds two attacks in one Attack action; a third needs Action Surge
	pgExec(t, `UPDATE characters SET class = 'fighter', level = 5, class_resources_used = '{}' WHERE id = $1`, a)
	if w := actionBudgetWarning(a, []string{"attack", "attack"}, false); w != "" {
		t.Errorf("two attacks with Extra Attack: %s", w)
	}
	if w := actionBudgetWarning(a, []string{"attack", "attack", "attack"}, false); !strings.Contains(w, "need 2 actions") || !strings.Contains(w, "you have 1") {
		t.Errorf("three attacks: %q", w)
	}
	if w := actionBudgetWarning(a, []string{"attack", "attack", "attack", "dash"}, true); !strings.Contains(w, "need 3 actions") {
		t.Errorf("three attacks and a Dash with Action Surge: %q", w)
	}
	if w := actionBudgetWarning(a, []string{"attack", "attack", "attack"}, true); w != "" {
		t.Errorf("three attacks with Action Surge: %s", w)
	}
	pgExec(t, `UPDATE characters SET class_resources_used = '{"action_surge": 1}' WHERE id = $1`, a)
	if w := actionBudgetWarning(a, []string{"attack", "attack", "attack"}, true); w == "" {
		t.Error("Action Surge already used: no warning")
	}
	pgExec(t, `UPDATE characters SET class = 'rogue', level = 1 WHERE id = $1`, a)
	if w := actionBudgetWarning(a, []string{"attack", "attack"}, false); !strings.Contains(w, "holds 1 attack") {
		t.Errorf("a Rogue 1's two attacks: %q", w)
	}
}
