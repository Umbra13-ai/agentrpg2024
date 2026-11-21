package main

import (
	"fmt"
	"strings"
	"testing"
)

// A stat-block entry that takes a standard action (monster_standard_actions_2024.go; SRD 5.2.1 Nimble Escape "The goblin takes the
// Disengage or Hide action"), through the routes: without use the choice is refused and nothing is recorded; use "disengage" records
// it, and the GM's map update moving the monster out of a character's reach then opens no Opportunity Attack chance (control: the
// same move without it does); the record ends at the start of the monster's next turn (POST combat/next); use "hide" is the GM's.
func TestPostgresMonsterNimbleEscape(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, _, _ := companionReactionFixture(t)
	m := compendiumFixture()
	m.Name, m.Type, m.Traits, m.Legendary, m.LegendaryIn = "Nimble Raider", "Humanoid", nil, nil, ""
	m.BonusActions = []srdMonsterEntry{{Name: "Nimble Escape", Text: "The raider takes the Disengage or Hide action."}}
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented standard-action fixture", "monsters": []srdMonster2024{m}})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	key := imported["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Nimble Raider", "monster_key": key, "hp": 100, "ac": 1, "initiative": -400}}})
	raider := monsterIDsByKeyOrName(camp, "Nimble Raider")[0]
	pgExec(t, `UPDATE characters SET reaction_used=false, hp=1000, max_hp=1000 WHERE id=$1`, charID)
	place := func(x int) {
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"............"}, Tokens: []mapToken{
			{ID: charID, Name: "hero", Position: mapSquare{0, 0}, Size: 1}, {ID: raider, Name: "Nimble Raider", Position: mapSquare{x, 0}, Size: 1}}}}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	use := func(choice string) string {
		body := map[string]interface{}{"monster": "Nimble Raider", "action": "Nimble Escape"}
		if choice != "" {
			body["use"] = choice
		}
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": body})
		return fmt.Sprint(r["monster_action_result"])
	}
	// control: leaving the reach without Disengage opens the chance
	place(1)
	oaNotes := func() (n int) {
		db.QueryRow(`SELECT COUNT(*) FROM actions WHERE lobby_id=$1 AND action_type='opportunity_attack_available'`, camp).Scan(&n)
		return
	}
	place(5)
	if !hasOAWindow(camp, charID, raider) || oaNotes() != 1 {
		t.Fatalf("control: moving out of reach opened no Opportunity Attack chance (window %v, notes %d)", hasOAWindow(camp, charID, raider), oaNotes())
	}
	takeOAWindow(camp, charID, raider)
	place(1)
	// the bonus action is the raider's own, on its turn (a fresh turn: the timeout worker leaves it alone)
	fr, _ := readFight(db, camp)
	pgExec(t, `UPDATE combat_state SET current_turn_index=$1, turn_started_at=NOW() WHERE lobby_id=$2`, idxOfID(fr, raider), camp)
	// the choice is the GM's to name
	if r := use(""); !strings.Contains(r, "name the action in use (Disengage, Hide). Nothing was done.") || monsterDisengaged(camp, raider) {
		t.Fatalf("an unnamed choice was not refused or recorded something: %s", r)
	}
	if r := use("hide"); !strings.Contains(r, "the Hide action, Dexterity (Stealth)") || monsterDisengaged(camp, raider) { // rolled (hide_2024.go)
		t.Errorf("Hide: %s", r)
	}
	if r := use("disengage"); !strings.Contains(r, "takes the Disengage action") || !monsterDisengaged(camp, raider) {
		t.Fatalf("Disengage was not recorded: %s", r)
	}
	place(5)
	if hasOAWindow(camp, charID, raider) || oaNotes() != 1 {
		t.Errorf("a Disengaged monster's move opened an Opportunity Attack chance (window %v, notes %d)", hasOAWindow(camp, charID, raider), oaNotes())
	}
	// it lasts for the rest of the turn: the start of the monster's next turn ends it
	f, _ := readFight(db, camp)
	idx := idxOfID(f, raider)
	pgExec(t, `UPDATE combat_state SET current_turn_index=$1 WHERE lobby_id=$2`, (idx-1+len(f.Order))%len(f.Order), camp)
	if c, r := pgCall(t, "POST", cp+"/combat/next", gm, map[string]interface{}{}); c != 200 {
		t.Fatalf("combat/next: %d %v", c, r)
	}
	if monsterDisengaged(camp, raider) {
		t.Errorf("the Disengage outlived the start of the monster's next turn")
	}
}
