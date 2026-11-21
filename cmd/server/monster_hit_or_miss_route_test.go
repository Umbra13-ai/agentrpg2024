package main

import (
	"fmt"
	"strings"
	"testing"
)

// A "Hit or Miss:" effect the server does not automate (the creatures around the target) is the GM's. Route: POST /api/gm/narrate
// monster_action at a character. The GM's reply carries the note, the private notices carry it, and the shared action log does not
// (invented stat-block text). Tier: route-exercised for a HIT; the miss wording is helper-tested only (TestHitOrMissNote). Control: the
// notice and the reply both exist, so an empty check cannot pass.
func TestPostgresHitOrMissNoteIsPrivate(t *testing.T) {
	testPostgres(t)
	gm, _, camp, char, _, _ := companionReactionFixture(t)
	m := compendiumFixture()
	m.Name = "Blast Brute"
	m.Type = "Giant"
	m.Actions = []srdMonsterEntry{{Name: "Blast", Text: "Ranged Attack Roll: +60, range 120 ft. Hit: 5 (1d6 + 2) Bludgeoning damage. Hit or Miss: Hidden shockwave rattles every creature within 5 feet of the target."}}
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented hit-or-miss fixture", "monsters": []srdMonster2024{m}})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	key := imported["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	cp := fmt.Sprintf("/api/campaigns/%d/combat", camp)
	pgCall(t, "POST", cp+"/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Blast Brute", "monster_key": key, "hp": 1000, "ac": 1, "initiative": -100}}})
	pgExec(t, `UPDATE characters SET hp=100000,max_hp=100000 WHERE id=$1`, char)
	reply := ""
	for n := 0; n < 30 && !strings.Contains(reply, "Hit or Miss"); n++ { // a natural 1 misses: retry until the reply shows the hit's note
		_, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": map[string]interface{}{"monster": "Blast Brute", "action": "Blast", "target": fmt.Sprint(char)}})
		reply = fmt.Sprint(j["monster_action_result"])
		if strings.Contains(reply, "misses") {
			reply = ""
		}
	}
	if !strings.Contains(reply, "Hidden shockwave") {
		t.Fatalf("the GM's reply lacks the Hit or Miss text: %s", reply)
	}
	var stored int
	if err := db.QueryRow(`SELECT COUNT(*) FROM actions WHERE lobby_id=$1 AND (COALESCE(result,'') ILIKE '%shockwave%' OR COALESCE(result,'') ILIKE '%[GM%')`, camp).Scan(&stored); err != nil || stored != 0 {
		t.Errorf("the shared action log keeps the GM's Hit or Miss text in %d rows (err %v)", stored, err)
	}
	found := false
	for _, n := range noticesOf(t, camp) {
		found = found || strings.Contains(n.Text, "Hidden shockwave")
	}
	if !found {
		t.Errorf("the private notices lack the Hit or Miss text")
	}
}
