package main

import (
	"fmt"
	"strings"
	"testing"
)

// Request-level tests of the shared attack-data rules: an imported monster's attacks, made through the GM's narrate route against a
// character, are dealt in full, or refused before anything is rolled (no Hit Points, no resource spent).
func TestPostgresImportedAttackRulesThroughTheGMRoute(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "attack rules gm")
	_, player := pgAgent(t, "attack rules player")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Attack rules", "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	m := compendiumFixture()
	m.Name = "Rules Beast"
	m.Actions = []srdMonsterEntry{
		{Name: "Multiattack", Text: "The beast makes two Flat Rider attacks."},
		{Name: "Flat Rider", Text: "Melee Attack Roll: +30, reach 5 ft. Hit: 12 (2d6 + 5) Slashing damage plus 7 Fire damage."},
		{Name: "Bite", Text: "Melee Attack Roll: +30, reach 5 ft. Hit: 12 (2d6 + 5) Piercing damage."},
		{Name: "Claw", Text: "Melee Attack Roll: +30, reach 5 ft. Hit: 12 (2d6 + 5) Slashing damage."},
		{Name: "Prone Rider", Text: "Melee Attack Roll: +30, reach 5 ft. Hit: 12 (2d6 + 5) Slashing damage plus 7 (2d6) Fire damage if the target is Prone."},
		{Name: "Chosen Rider", Text: "Melee Attack Roll: +30, reach 5 ft. Hit: 6 (1d4 + 4) Slashing damage plus 7 (2d6) damage of the type chosen for the Draconic Origin trait."},
	}
	choice := compendiumFixture()
	choice.Name = "Choice Beast"
	choice.Actions = []srdMonsterEntry{
		{Name: "Multiattack", Text: "The beast makes one Bite attack or one Claw attack."},
		m.Actions[2], m.Actions[3],
	}
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "original test", "monsters": []srdMonster2024{m, choice}})
	if c != 201 {
		t.Fatalf("import %d %v", c, j)
	}
	imported := j["monsters"].([]interface{})
	key := imported[0].(map[string]interface{})["monster_key"].(string)
	choiceKey := imported[1].(map[string]interface{})["monster_key"].(string)
	pgExec(t, `UPDATE lobbies SET status='active',schedule_state='off' WHERE id=$1`, camp)
	pgFight(t, camp)
	status, j := pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Rules target %d", camp)))
	if status != 200 {
		t.Fatalf("fixture character: %d %v", status, j)
	}
	char := int(j["character_id"].(float64))
	pgExec(t, `UPDATE characters SET lobby_id=$1,hp=1000,max_hp=1000,ac=1 WHERE id=$2`, camp, char)
	c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Rules Beast", "monster_key": key, "initiative": 1}, {"name": "Choice Beast", "monster_key": choiceKey, "initiative": 0}}})
	if c != 200 {
		t.Fatalf("add %d %v", c, j)
	}
	hp := func() int { var n int; db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, char).Scan(&n); return n }
	attackBy := func(monster, action, use string) string {
		t.Helper()
		ma := map[string]interface{}{"monster": monster, "action": action, "target": fmt.Sprint(char)}
		if use != "" {
			ma["use"] = use
		}
		c, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "The beast attacks.", "monster_action": ma})
		if !narrateAnswered(c, j) {
			t.Fatalf("attack %s: %d %v", action, c, j)
		}
		return fmt.Sprint(j["monster_action_result"])
	}
	attack := func(action, use string) string { return attackBy("Rules Beast", action, use) }
	refused := func(action, use, why string) {
		t.Helper()
		pgExec(t, `UPDATE characters SET hp=1000 WHERE id=$1`, char)
		text := attack(action, use)
		if hp() != 1000 || !strings.Contains(text, why) {
			t.Errorf("%s was not refused before it rolled (HP %d): %s", action, hp(), text)
		}
	}
	// flat secondary damage is dealt (7 Fire on every hit, at least 2d6+5 = 7 Slashing): two hits deal at least 28
	pgExec(t, `UPDATE characters SET hp=1000 WHERE id=$1`, char)
	text := attack("Multiattack", "")
	hits := strings.Count(text, " HIT") // a critical hit reads "- CRITICAL HIT"
	if got := 1000 - hp(); got < 14*hits || strings.Count(text, "7 Fire damage") != hits {
		t.Errorf("Multiattack of flat riders dealt %d for %d hits, every hit adds 7 Fire damage: %s", got, hits, text)
	}
	pgExec(t, `UPDATE characters SET hp=1000 WHERE id=$1`, char)
	if text := attackBy("Choice Beast", "Multiattack", ""); hp() != 1000 || !strings.Contains(text, "choice between attacks") || !strings.Contains(text, "Nothing was rolled") {
		t.Errorf("an either/or Multiattack was not refused (HP %d): %s", hp(), text)
	}
	refused("Prone Rider", "", "Nothing was rolled")
	refused("Chosen Rider", "", "Nothing was rolled")
	// a natural 1 misses whatever the bonus: try until a hit
	for try := 0; try < 20; try++ {
		pgExec(t, `UPDATE characters SET hp=1000 WHERE id=$1`, char)
		text := attack("Chosen Rider", "cold")
		if !strings.Contains(text, "- HIT") {
			continue
		}
		if hp() == 1000 || !strings.Contains(strings.ToLower(text), "cold damage") {
			t.Errorf("Chosen Rider with a type did not deal Cold damage: %s", text)
		}
		return
	}
	t.Error("no hit in 20 attacks at +30")
}
