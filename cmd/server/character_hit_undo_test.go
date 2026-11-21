package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The character-attacker route (attack_target.go) records the same snapshot as the monster attacker, so a monster target's Parry
// takes a character's hit back by the same rules (monster_hit_undo_2024.go): Temporary Hit Points and Hit Points come back, a target
// whose Hit Points changed after the hit is left alone with a reconciliation note, and a second Reaction finds no hit.
func TestPostgresCharacterHitTakenBack(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "char undo gm")
	_, player := pgAgent(t, "char undo player")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Character hit undo", "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Undo Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var goblin srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Goblin Warrior" {
			goblin = m
		}
	}
	m := goblin
	m.Name, m.AC, m.HP = "Undo Parrier", 1, 500
	m.Reactions = []srdMonsterEntry{{Name: "Parry", Text: "Trigger: The parrier is hit by a melee attack roll while holding a weapon. Response: The parrier adds 99 to its AC against that attack, possibly causing it to miss."}}
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variant for character hit undo test", "monsters": []srdMonster2024{m}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Undo Parrier", "monster_key": key, "initiative": 1}}})
	ids := monsterIDsByKeyOrName(camp, "Undo Parrier")
	if len(ids) != 1 {
		t.Fatalf("monster id: %v", ids)
	}
	id := ids[0]
	hp := func() int { return monsterEntryVitals(camp, id).HP }
	pgExec(t, `UPDATE characters SET wild_shape_form='',weapon_interaction_used=false WHERE id=$1`, char)
	var equipped string
	db.QueryRow(`SELECT COALESCE(equipped_main_hand,'') FROM characters WHERE id=$1`, char).Scan(&equipped)
	if equipped == "" {
		pgCall(t, "POST", "/api/characters/equip-weapon", player, map[string]interface{}{"character_id": char, "weapon": "dagger", "slot": "main_hand"})
		equipped = "dagger"
	}
	hit := func() bool {
		for try := 0; try < 40; try++ {
			clearMonsterReactionState(camp)
			monsterReactionRound.Delete(fmt.Sprintf("%d:%d", camp, id))
			setMonsterTempHP(camp, id, 5)
			if err := setMonsterHP(camp, id, 500); err != nil { // the monster's Hit Points (setHP is a character's: with a monster id it did nothing, so a try after a critical hit started below 500)
				t.Fatal(err)
			}
			pgExec(t, `UPDATE characters SET action_used=false,attacks_remaining=NULL,conditions='[]' WHERE id=$1`, char)
			status, r := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"action": "attack", "target_id": id, "description": "I attack with my " + equipped})
			if status != 200 {
				t.Fatalf("attack: %d %v", status, r)
			}
			// this reply's own hit flag first: thisTurnHitOnMonster keeps an earlier try's record, so a miss on a later try could be read as a hit
			// (and the Parry would restore that older record's Hit Points)
			if am, _ := r["attack"].(map[string]interface{}); am == nil || am["hit"] != true {
				continue
			}
			if h, ok := thisTurnHitOnMonster(camp, id); ok && !h.Nat20 && h.EntryBefore != nil && h.EntryAfter != nil {
				return true
			}
		}
		return false
	}
	parry := func() string {
		monsterReactionRound.Delete(fmt.Sprintf("%d:%d", camp, id))
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Undo Parrier", "action": "Parry"}})
		return fmt.Sprint(r["monster_action_result"])
	}
	if !hit() {
		t.Fatal("the character's attack never hit")
	}
	if v := monsterEntryVitals(camp, id); v.HP == 500 && v.Temp == 5 {
		t.Fatalf("the hit changed nothing: %+v", v)
	}
	if pr := parry(); !strings.Contains(pr, "now a miss") || hp() != 500 || monsterEntryVitals(camp, id).Temp != 5 {
		t.Fatalf("character hit not taken back (%+v): %s", monsterEntryVitals(camp, id), pr)
	}
	if pr := parry(); strings.Contains(pr, "now a miss") || hp() != 500 {
		t.Fatalf("a second Reaction changed the target again: %s", pr)
	}
	if !hit() {
		t.Fatal("the character's attack never hit (stale case)")
	}
	setMonsterTempHP(camp, id, 9) // another source gave Temporary Hit Points after the hit
	if pr := parry(); !strings.Contains(pr, "RECONCILIATION NEEDED") || monsterEntryVitals(camp, id).Temp != 9 {
		t.Fatalf("a stale character hit was corrected (%+v): %s", monsterEntryVitals(camp, id), pr)
	}
}
