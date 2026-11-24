package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Every response of an attached reaction, through the routes (POST /api/action for the character's hit, POST /api/gm/narrate for the
// Reaction): refused before a hit and spending nothing; then, after a hit, the AC bonus that turns it into a miss, the halved damage,
// Resistance with Temporary Hit Points, Disadvantage after the roll, the attacker's save that turns the hit into a miss and Charms it,
// and damage back at the attacker. Invented wording; the covered words leave nothing for the text rules.
func TestPostgresAttachedReactions(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "attached react gm")
	_, player := pgAgent(t, "attached react player")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Attached reactions", "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Reflex Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	reaction := func(name, words string, m entryMechanic) srdMonsterEntry {
		m["kind"], m["covers"] = "reaction", "Response: "+words
		return srdMonsterEntry{Name: name, Text: "Trigger: The duelist is hit by an attack roll. Response: " + words, Mechanics: []entryMechanic{m}}
	}
	mon := inventedMonster("Test Reflex Duelist", "Humanoid", nil, []srdMonsterEntry{{Name: "Jab", Text: "Melee Attack Roll: +5, reach 5 ft. Hit: 1 (1d1) Piercing damage."}})
	mon.AC, mon.HP = 1, 500
	mon.Reactions = []srdMonsterEntry{
		reaction("Steel Guard", "The duelist turns the blade aside.", entryMechanic{"response": "ac_bonus", "bonus": float64(10), "against": "melee"}),
		reaction("Glancing Twist", "The duelist rolls with the blow.", entryMechanic{"response": "halve_damage"}),
		reaction("Iron Hide", "The duelist's skin hardens.", entryMechanic{"response": "resist_damage", "damage_types": []interface{}{"piercing", "slashing"}, "temp_hp": float64(7)}),
		reaction("Foreseen", "The duelist saw it coming.", entryMechanic{"response": "disadvantage_on_attack"}),
		reaction("Warding Glare", "The duelist's gaze turns the attacker's hand.", entryMechanic{"response": "attacker_save_or_miss", "save": "wis", "dc": float64(30), "conditions": []interface{}{"charmed"}}),
		reaction("Mind Sting", "The duelist lashes the attacker's mind.", entryMechanic{"response": "damage_back", "damage": "2d4", "damage_type": "psychic", "note": "only if it can see the attacker"}),
	}
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented attached reaction fixture", "monsters": []srdMonster2024{mon}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Test Reflex Duelist", "monster_key": key, "hp": 500, "ac": 15, "initiative": 1}}})
	id := monsterIDsByKeyOrName(camp, "Test Reflex Duelist")[0]
	rkey := fmt.Sprintf("%d:%d", camp, id)
	pgExec(t, `UPDATE characters SET wild_shape_form='',weapon_interaction_used=false,hp=500,max_hp=500,wis=1 WHERE id=$1`, char)
	var equipped string
	db.QueryRow(`SELECT COALESCE(equipped_main_hand,'') FROM characters WHERE id=$1`, char).Scan(&equipped)
	if equipped == "" {
		pgCall(t, "POST", "/api/characters/equip-weapon", player, map[string]interface{}{"character_id": char, "weapon": "dagger", "slot": "main_hand"})
		equipped = "dagger"
	}
	react := func(name string) (int, string) {
		code, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Test Reflex Duelist", "action": name, "target": fmt.Sprint(char)}})
		return code, fmt.Sprint(r["monster_action_result"], " ", r["message"], " ", r["error"])
	}
	reset := func() {
		clearMonsterReactionState(camp)
		monsterReactionRound.Delete(rkey)
		setMonsterTempHP(camp, id, 0)
		if err := setMonsterHP(camp, id, 500); err != nil {
			t.Fatal(err)
		}
		pgExec(t, `UPDATE characters SET action_used=false,attacks_remaining=NULL,conditions='[]',hp=500 WHERE id=$1`, char)
		endCondition(camp, char, "charmed", "")
	}
	hit := func() int { // the hit's damage
		for try := 0; try < 40; try++ {
			reset()
			_, r := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"action": "attack", "target_id": id, "description": "I attack with my " + equipped})
			if am, _ := r["attack"].(map[string]interface{}); am == nil || am["hit"] != true {
				continue
			}
			if h, ok := thisTurnHitOnMonster(camp, id); ok && h.EntryBefore != nil && h.EntryAfter != nil && h.Natural > 0 && monsterEntryVitals(camp, id).HP < 500 {
				return 500 - monsterEntryVitals(camp, id).HP
			}
		}
		t.Fatal("the character's attack never hit")
		return 0
	}
	charHP := func() int {
		var n int
		db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, char).Scan(&n)
		return n
	}

	reset()
	if code, res := react("Glancing Twist"); code != 409 || !strings.Contains(res, "Nothing was used") {
		t.Errorf("before a hit: %d %s", code, res)
	}
	if _, spent := monsterReactionRound.Load(rkey); spent {
		t.Error("a refused reaction was spent")
	}

	for try := 0; try < 10; try++ { // AC 15 + 10: only a natural 20 still hits
		hit()
		_, res := react("Steel Guard")
		if strings.Contains(res, "natural 20") {
			continue
		}
		if !strings.Contains(res, "now a miss") || monsterEntryVitals(camp, id).HP != 500 {
			t.Errorf("ac_bonus 10: %s (HP %d)", res, monsterEntryVitals(camp, id).HP)
		}
		break
	}
	dmg := hit()
	if _, res := react("Glancing Twist"); monsterEntryVitals(camp, id).HP != 500-dmg/2 {
		t.Errorf("halve %d: HP %d: %s", dmg, monsterEntryVitals(camp, id).HP, res)
	}
	dmg = hit()
	if _, res := react("Iron Hide"); monsterEntryVitals(camp, id).HP != 500-dmg/2 || monsterEntryVitals(camp, id).Temp != 7 {
		t.Errorf("resist %d: %+v: %s", dmg, monsterEntryVitals(camp, id), res)
	}
	hit()
	if _, res := react("Foreseen"); !strings.Contains(res, "second d20") || (strings.Contains(res, "now a miss") != (monsterEntryVitals(camp, id).HP == 500)) {
		t.Errorf("disadvantage: %s (HP %d)", res, monsterEntryVitals(camp, id).HP)
	}
	hit()
	if _, res := react("Warding Glare"); !strings.Contains(res, "misses instead") || monsterEntryVitals(camp, id).HP != 500 || !hasCondMarker(camp, char, "charmed") {
		t.Errorf("attacker_save_or_miss: %s (HP %d)", res, monsterEntryVitals(camp, id).HP)
	}
	hit()
	if _, res := react("Mind Sting"); charHP() >= 500 || !strings.Contains(res, "Psychic") || !strings.Contains(res, "only if it can see the attacker") {
		t.Errorf("damage_back: %s (HP %d)", res, charHP())
	}
	if p := mechanicProblem("reaction", srdMonsterEntry{Name: "x", Text: "Trigger: x. Response: y.", Mechanics: mechanicsFromJSON(t, `[{"kind":"reaction","response":"ac_bonus"}]`)}); !strings.Contains(p, "needs bonus") {
		t.Errorf("ac_bonus without bonus: %q", p)
	}
	if p := mechanicProblem("action", srdMonsterEntry{Name: "x", Text: ashClawText, Mechanics: mechanicsFromJSON(t, `[{"kind":"reaction","response":"halve_damage"}]`)}); !strings.Contains(p, "Reaction") {
		t.Errorf("a reaction on an action: %q", p)
	}
}
