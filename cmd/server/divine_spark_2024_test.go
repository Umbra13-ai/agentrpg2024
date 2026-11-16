package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Feedback log 44: Divine Spark aimed at an ally heals that ally, named with target_character_id or with target_id (the ally's
// character id); with no ally named, or an ally at full Hit Points, it is refused and spends nothing (not the action, not Channel
// Divinity); it never heals the caster.
func TestPostgresDivineSparkHealsTheNamedAlly(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "pg spark gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Spark", "max_players": 3})
	lobby := int(j["campaign_id"].(float64))
	pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/schedule", lobby), gm, map[string]interface{}{"state": "off"})
	join := func(who string) (int, string) {
		_, tok := pgAgent(t, "pg spark "+who)
		c, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Spark %s %d", who, time.Now().UnixNano()%1e9)))
		if c != 200 {
			t.Fatalf("character: %d %v", c, j)
		}
		id := int(j["character_id"].(float64))
		if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", lobby), tok, map[string]interface{}{"character_id": id}); c != 200 {
			t.Fatalf("join: %d %v", c, j)
		}
		pgFinish(t, tok, id)
		pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), tok, nil)
		return id, tok
	}
	cleric, tok := join("cleric")
	ally, _ := join("ally")
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), gm, nil)
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", lobby), gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	pgExec(t, `UPDATE characters SET class = 'cleric', level = 2, wis = 16 WHERE id = $1`, cleric)
	hp := func(id int) (int, int) {
		var h, m int
		db.QueryRow(`SELECT hp, max_hp FROM characters WHERE id = $1`, id).Scan(&h, &m)
		return h, m
	}
	spent := func() string {
		var s string
		db.QueryRow(`SELECT COALESCE(class_resources_used::text, '') FROM characters WHERE id = $1`, cleric).Scan(&s)
		return s
	}
	_, allyMax := hp(ally)
	clericHP, _ := hp(cleric)
	before := spent()

	// nothing named, and an ally at full Hit Points: refused, nothing spent
	for _, body := range []map[string]interface{}{
		{"action": "divine_spark", "description": "heal"},
		{"action": "divine_spark", "target_character_id": ally, "description": "heal"},
	} {
		c, j := pgCall(t, "POST", "/api/action", tok, body)
		if c != 409 || spent() != before {
			t.Errorf("%v: %d %v (spent %s, was %s)", body, c, j, spent(), before)
		}
	}
	// the ally hurt: target_character_id heals the ally, then target_id (the ally's character id) does too; the caster is untouched
	for i, body := range []map[string]interface{}{
		{"action": "divine_spark", "target_character_id": ally, "description": "heal"},
		{"action": "divine_spark", "target_id": ally},
	} {
		pgExec(t, `UPDATE characters SET hp = 1, action_used = false WHERE id = $1`, ally)
		pgExec(t, `UPDATE characters SET action_used = false WHERE id = $1`, cleric)
		pgExec(t, `UPDATE characters SET class_resources_used = '{}' WHERE id = $1`, cleric)
		pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": lobby, "narration": "The scene moves on."}) // a new beat
		c, j := pgCall(t, "POST", "/api/action", tok, body)
		if h, _ := hp(ally); c != 200 || h <= 1 || h > allyMax || !strings.Contains(fmt.Sprint(j["result"]), "Pg Spark ally") {
			t.Errorf("heal %d: %d %v (ally %d)", i, c, j, h)
		}
		if h, _ := hp(cleric); h != clericHP {
			t.Errorf("heal %d changed the caster's Hit Points: %d → %d", i, clericHP, h)
		}
	}
}
