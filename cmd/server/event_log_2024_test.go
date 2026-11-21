package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// What players read of the shared log (GET /api/my-turn's recent_events, the feed): a GM's monster adjustment says how the monster
// looks, never its Hit Points, maximum or AC (I-CALLA-14); a player's own words stay beside a Dodge's rule (I-CALLA-13); a refused
// request is not written at all (I-CALLA-17); the order is stated and since_your_last_action lists what came after the character's
// own last action (I-CALLA-6, I-CALLA-16).
func TestPostgresPlayersReadTheLogWithoutHiddenNumbers(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "pg log gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Log", "max_players": 2})
	lobby := int(j["campaign_id"].(float64))
	pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/schedule", lobby), gm, map[string]interface{}{"state": "off"})
	_, tok := pgAgent(t, "pg log player")
	_, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Log %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", lobby), tok, map[string]interface{}{"character_id": char})
	pgFinish(t, tok, char)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), tok, nil)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), gm, nil)
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", lobby), gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/start", lobby), gm, map[string]interface{}{})
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", lobby), gm, map[string]interface{}{
		"combatants": []map[string]interface{}{{"name": "Log Drake", "monster_key": "goblin-warrior", "hp": 212, "ac": 17}}}); c != 200 {
		t.Fatalf("add: %d %v", c, j)
	}
	// the player dodges with words of its own, then the GM adjusts the monster
	if c, j := pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "dodge", "description": "Crouches behind the parapet stonework"}); c != 200 {
		t.Fatalf("dodge: %d %v", c, j)
	}
	c, adj := pgCall(t, "POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": lobby, "target": "Log Drake", "damage": 3, "damage_type": "fire", "ac": 22})
	if c != 200 || !strings.Contains(fmt.Sprint(adj["message"]), "209/212") {
		t.Fatalf("adjust: the GM's reply keeps the numbers: %d %v", c, adj)
	}
	// a refused request: answered, not logged
	pgExec(t, `UPDATE characters SET action_used = false WHERE id = $1`, char)
	var before int
	db.QueryRow(`SELECT COUNT(*) FROM actions WHERE lobby_id = $1`, lobby).Scan(&before)
	if c, j := pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "study", "skill": "acrobatics", "description": "I think"}); c < 400 {
		t.Fatalf("a study with a skill not its own was accepted: %d %v", c, j)
	}
	var after int
	db.QueryRow(`SELECT COUNT(*) FROM actions WHERE lobby_id = $1`, lobby).Scan(&after)
	if after != before {
		t.Errorf("a refused request was written to the log (%d → %d rows)", before, after)
	}
	_, mt := pgCall(t, "GET", "/api/my-turn", tok, nil)
	raw, _ := json.Marshal(mt["recent_events"])
	events := string(raw)
	if strings.Contains(events, "209/212") || strings.Contains(events, "AC 22") || strings.Contains(events, "Hit Points.") {
		t.Errorf("the players' log shows the monster's numbers: %s", events)
	}
	if !strings.Contains(events, "Log Drake looks healthy") {
		t.Errorf("the adjustment doesn't say how the monster looks: %s", events)
	}
	if !strings.Contains(events, "Crouches behind the parapet stonework") || !strings.Contains(events, "Dodging") {
		t.Errorf("the Dodge lost the player's words or its rule: %s", events)
	}
	if mt["recent_events_order"] == nil {
		t.Error("my-turn doesn't say the order of recent_events")
	}
	since, _ := json.Marshal(mt["since_your_last_action"])
	if !strings.Contains(string(since), "Log Drake looks healthy") || strings.Contains(string(since), "Crouches") {
		t.Errorf("since_your_last_action: %s", since)
	}
	if !strings.Contains(events, "Dexterity saving throws") {
		t.Errorf("the Dodge leaves out its Dexterity saving throw half: %s", events)
	}
	// the reaction hint names an order the character can set (a level 1 Rogue: not Shield; I-CALLA-8)
	if rem, _ := json.Marshal(mt["reminders"]); strings.Contains(string(rem), `\"order\": \"shield\"`) || !strings.Contains(string(rem), "opportunity_attack") {
		t.Errorf("the reaction hint: %s", rem)
	}
	// Hit Point Dice: the sheet shows what is left (I-CALLA-9)
	pgExec(t, `UPDATE characters SET hit_dice_spent = 1 WHERE id = $1`, char)
	_, mt = pgCall(t, "GET", "/api/my-turn", tok, nil)
	if hd, _ := json.Marshal(mt["character"].(map[string]interface{})["hit_dice_by_type"]); string(hd) != `{"d8":0}` {
		t.Errorf("hit_dice_by_type after spending the only die: %s", hd)
	}
	// the fight ends: combat is null and the players are told it is over until the GM narrates (I-CALLA-7, I-CALLA-18)
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/end", lobby), gm, map[string]interface{}{}); c != 200 {
		t.Fatalf("combat end: %d %v", c, j)
	}
	_, mt = pgCall(t, "GET", "/api/my-turn", tok, nil)
	if v, ok := mt["combat"]; !ok || v != nil || mt["fight_over"] == nil {
		t.Errorf("after the fight: combat %v (present %v), fight_over %v", v, ok, mt["fight_over"])
	}
	pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": lobby, "narration": "The last goblin flees into the dark."})
	if _, mt = pgCall(t, "GET", "/api/my-turn", tok, nil); mt["fight_over"] != nil {
		t.Errorf("fight_over stays after the GM narrated: %v", mt["fight_over"])
	}
	// the feed shows the same
	_, feed := pgCall(t, "GET", fmt.Sprintf("/api/campaigns/%d/feed", lobby), tok, nil)
	if f, _ := json.Marshal(feed); strings.Contains(string(f), "209/212") || strings.Contains(string(f), "AC 22") {
		t.Errorf("the feed shows the monster's numbers: %.400s", f)
	}
}
