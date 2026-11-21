package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Playtest I-CALLA-10, -11, -12: a readied action keeps the held request (the spell and its target), fires through POST /api/action
// itself (a readied Fire Bolt rolls its attack against the target's Armor Class, as a cast on the turn does), takes the Reaction and
// not the action, whoever fires it (the GM or the player); and it ends at the start of the character's next turn and with the fight.
func TestPostgresReadiedActionRunsAsTheAction(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "pg ready gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Ready", "max_players": 2})
	lobby := int(j["campaign_id"].(float64))
	pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/schedule", lobby), gm, map[string]interface{}{"state": "off"})
	_, tok := pgAgent(t, "pg ready player")
	_, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Ready %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", lobby), tok, map[string]interface{}{"character_id": char})
	pgFinish(t, tok, char)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), tok, nil)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), gm, nil)
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", lobby), gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/start", lobby), gm, map[string]interface{}{}); c != 200 {
		t.Fatalf("combat start: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", lobby), gm, map[string]interface{}{
		"combatants": []map[string]interface{}{{"name": "Ready Target", "monster_key": "goblin-warrior", "hp": 400, "ac": 12}}}); c != 200 {
		t.Fatalf("add: %d %v", c, j)
	}
	var raw []byte
	db.QueryRow(`SELECT turn_order FROM combat_state WHERE lobby_id = $1`, lobby).Scan(&raw)
	var order []map[string]interface{}
	json.Unmarshal(raw, &order)
	monster := 0
	for _, e := range order {
		if id := asInt(e["id"]); id < 0 {
			monster = id
		}
	}
	if monster == 0 {
		t.Fatalf("no monster in %s", raw)
	}
	pgExec(t, `UPDATE characters SET class = 'wizard', intl = 16, known_spells = '["fire-bolt"]' WHERE id = $1`, char)
	state := func() (action, reaction bool, readied string) {
		db.QueryRow(`SELECT COALESCE(action_used,false), COALESCE(reaction_used,false), COALESCE(readied_action::text,'') FROM characters WHERE id = $1`, char).Scan(&action, &reaction, &readied)
		return
	}
	ready := func() {
		t.Helper()
		pgExec(t, `UPDATE characters SET action_used = false, reaction_used = false WHERE id = $1`, char)
		c, j := pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "ready", "spell_slug": "fire-bolt", "target_id": monster,
			"description": "trigger: the goblin steps out of cover; action: cast Fire Bolt at it"})
		if c != 200 {
			t.Fatalf("ready: %d %v", c, j)
		}
		if a, _, r := state(); !a || !strings.Contains(r, `"spell_slug": "fire-bolt"`) || !strings.Contains(r, fmt.Sprintf(`"target_id": %d`, monster)) {
			t.Fatalf("the Ready didn't take the action or keep the held request: action used %v, %s", a, r)
		}
		if strings.Contains(fmt.Sprint(j["result"]), "POST /api/trigger-readied), or it will be lost") {
			t.Errorf("the old Ready words: %v", j["result"])
		}
	}

	// fired by the GM and then by the player: the attack roll is made, the Reaction is spent, the Ready ends
	for _, fire := range []func() (int, map[string]interface{}){
		func() (int, map[string]interface{}) {
			return pgCall(t, "POST", "/api/gm/trigger-readied", gm, map[string]interface{}{"campaign_id": lobby, "character_id": char})
		},
		func() (int, map[string]interface{}) { return pgCall(t, "POST", "/api/trigger-readied", tok, nil) },
	} {
		ready()
		pgExec(t, `UPDATE characters SET action_used = true WHERE id = $1`, char) // the action is spent: the Reaction still fires it
		c, j := fire()
		res := fmt.Sprint(j["result"])
		if c != 200 || !strings.Contains(res, "Fire Bolt") || !(strings.Contains(res, "HIT") || strings.Contains(res, "MISS") || strings.Contains(strings.ToLower(res), "d20")) {
			t.Errorf("fire: %d %v", c, j)
		}
		if _, reaction, readied := state(); !reaction || readied != "" {
			t.Errorf("after firing: reaction used %v, readied %q", reaction, readied)
		}
		var typ string
		db.QueryRow(`SELECT action_type FROM actions WHERE character_id = $1 ORDER BY id DESC LIMIT 1`, char).Scan(&typ)
		if typ != "readied_cast" {
			t.Errorf("the log records %q", typ)
		}
	}

	// no Reaction left: refused, the Ready still holds
	ready()
	pgExec(t, `UPDATE characters SET reaction_used = true WHERE id = $1`, char)
	if c, j := pgCall(t, "POST", "/api/trigger-readied", tok, nil); c != 409 {
		t.Errorf("no Reaction: %d %v", c, j)
	}
	if _, _, r := state(); r == "" {
		t.Error("a refused firing ended the Ready")
	}
	// the start of the character's next turn ends it
	resetTurnFlags(char, 30)
	if _, _, r := state(); r != "" {
		t.Errorf("the next turn's start left the Ready: %s", r)
	}
	// so does the end of the fight
	ready()
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/end", lobby), gm, map[string]interface{}{}); c != 200 {
		t.Fatalf("combat end: %d %v", c, j)
	}
	if _, _, r := state(); r != "" {
		t.Errorf("the fight's end left the Ready: %s", r)
	}
	if c, _ := pgCall(t, "GET", "/api/my-turn", tok, nil); c == 200 {
		_, j := pgCall(t, "GET", "/api/my-turn", tok, nil)
		if j["readied_action"] != nil {
			t.Errorf("my-turn still shows a readied action: %v", j["readied_action"])
		}
	}
}
