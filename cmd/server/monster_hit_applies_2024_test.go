package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Feedback log 62 (I-GM-19): a monster's attack the GM sends with narrate deals its damage to the character by default, and a hit's
// own saving throw (the Pit Fiend's Bite: Constitution DC 21 or Poisoned) is rolled; "apply_damage": false only rolls and reports.
func TestPostgresMonsterHitAppliesByDefault(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "pg fiend gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Fiend", "max_players": 2})
	lobby := int(j["campaign_id"].(float64))
	pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/schedule", lobby), gm, map[string]interface{}{"state": "off"})
	_, tok := pgAgent(t, "pg fiend player")
	_, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Bitten %d", time.Now().UnixNano()%1e9)))
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
		"combatants": []map[string]interface{}{{"name": "Test Fiend", "monster_key": "pit-fiend"}}}); c != 200 {
		t.Fatalf("add: %d %v", c, j)
	}
	pgExec(t, `UPDATE characters SET max_hp = 500, hp = 500 WHERE id = $1`, char)
	hp := func() int {
		var h int
		db.QueryRow(`SELECT hp FROM characters WHERE id = $1`, char).Scan(&h)
		return h
	}
	bite := func(apply interface{}) (string, map[string]interface{}) {
		ma := map[string]interface{}{"monster": "Test Fiend", "action": "Bite", "target": fmt.Sprint(char)}
		if apply != nil {
			ma["apply_damage"] = apply
		}
		c, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": lobby, "narration": "The fiend bites.", "monster_action": ma})
		if c != 200 {
			t.Fatalf("narrate: %d %v", c, j)
		}
		return fmt.Sprint(j["monster_action_result"]), j
	}
	// "apply_damage": false rolls and reports only
	before := hp()
	if res, _ := bite(false); hp() != before {
		t.Errorf("apply_damage false changed the Hit Points: %d → %d (%s)", before, hp(), res)
	}
	// the default deals a hit's damage and rolls the Bite's saving throw (a natural 1 misses: try a few times)
	for i := 0; i < 10; i++ {
		before = hp()
		res, j := bite(nil)
		if !strings.Contains(res, "HIT") {
			continue
		}
		if hp() >= before {
			t.Errorf("a hit didn't change the Hit Points (%d → %d): %s %v", before, hp(), res, j["monster_attack"])
		}
		if !strings.Contains(res, "Constitution") {
			t.Errorf("the Bite's saving throw wasn't rolled: %s", res)
		}
		return
	}
	t.Fatal("ten Bites without a hit")
}
