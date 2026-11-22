package main

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// reactionSelfHitTrigger over the whole SRD catalog: the Reactions whose trigger is an attack roll hitting the monster itself, with the
// kind of attack the trigger names. Protection ("hits the wearer") and Redirect Attack ("makes an attack roll against it") are not.
func TestReactionSelfHitTriggersOverTheCatalog(t *testing.T) {
	got := []string{}
	for _, m := range srdMonsters2024() {
		for _, e := range m.Reactions {
			if needs, kind := reactionSelfHitTrigger(e.Text); needs {
				got = append(got, fmt.Sprintf("%s|%s|%s", m.Name, stripUsage(e.Name), kind))
			}
		}
	}
	sort.Strings(got)
	want := []string{"Bandit Captain|Parry|melee", "Erinyes|Parry|melee", "Gladiator|Parry|melee", "Knight|Parry|melee", "Marilith|Parry|melee",
		"Mummy Lord|Whirlwind of Sand|", "Noble|Parry|melee", "Pirate Captain|Riposte|melee", "Rust Monster|Reflexive Antennae|",
		"Stone Giant|Deflect Missile|ranged", "Warrior Veteran|Parry|melee"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("self-hit triggers:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Through the routes (POST /api/action for the character's hit, POST /api/gm/narrate for the Reaction): a Reaction triggered by a hit on
// the monster is refused before any hit and spends nothing (Reflexive Antennae's "uses" path, Parry's AC); after a character's melee
// hit a ranged-only trigger (Deflect Missile's) is refused, the Antennae path resolves, and a second Reaction the same round is refused.
// Invented wording with the SRD 5.2.1 patterns.
func TestPostgresMonsterReactionTriggers(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "react trigger gm")
	_, player := pgAgent(t, "react trigger player")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Reaction triggers", "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Trigger Hero %d", time.Now().UnixNano()%1e9)))
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
	m.Name, m.AC, m.HP = "Trigger Fixture", 1, 500
	m.Actions = append(m.Actions, srdMonsterEntry{Name: "Snap", Text: "Melee Attack Roll: +30, reach 5 ft. Hit: 1 Piercing damage."})
	m.Reactions = []srdMonsterEntry{
		{Name: "Duck Away", Text: "Trigger: The fixture is hit by a melee attack roll while holding a weapon. Response: The fixture adds 2 to its AC against that attack, possibly causing it to miss."},
		{Name: "Feelers", Text: "Trigger: An attack roll hits the fixture. Response: The fixture uses Snap."},
		{Name: "Arrow Ward", Text: "Trigger: The fixture is hit by a ranged attack roll. Response: The fixture adds 5 to its AC against that attack, possibly causing it to miss."},
	}
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented reaction trigger fixture", "monsters": []srdMonster2024{m}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Trigger Fixture", "monster_key": key, "initiative": 1}}})
	id := monsterIDsByKeyOrName(camp, "Trigger Fixture")[0]
	rkey := fmt.Sprintf("%d:%d", camp, id)
	pgExec(t, `UPDATE characters SET wild_shape_form='',weapon_interaction_used=false,hp=500,max_hp=500 WHERE id=$1`, char)
	var equipped string
	db.QueryRow(`SELECT COALESCE(equipped_main_hand,'') FROM characters WHERE id=$1`, char).Scan(&equipped)
	if equipped == "" {
		pgCall(t, "POST", "/api/characters/equip-weapon", player, map[string]interface{}{"character_id": char, "weapon": "dagger", "slot": "main_hand"})
		equipped = "dagger"
	}
	react := func(name string) string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Trigger Fixture", "action": name, "target": fmt.Sprint(char)}})
		return fmt.Sprint(r["monster_action_result"], " ", r["message"], " ", r["error"])
	}
	hit := func() bool {
		for try := 0; try < 40; try++ {
			clearMonsterReactionState(camp)
			monsterReactionRound.Delete(rkey)
			setMonsterTempHP(camp, id, 0)
			if err := setMonsterHP(camp, id, 500); err != nil {
				t.Fatal(err)
			}
			pgExec(t, `UPDATE characters SET action_used=false,attacks_remaining=NULL,conditions='[]' WHERE id=$1`, char)
			_, r := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"action": "attack", "target_id": id, "description": "I attack with my " + equipped})
			if am, _ := r["attack"].(map[string]interface{}); am == nil || am["hit"] != true {
				continue
			}
			if h, ok := thisTurnHitOnMonster(camp, id); ok && h.EntryBefore != nil && h.EntryAfter != nil && monsterEntryVitals(camp, id).HP < 500 {
				return true
			}
		}
		return false
	}

	// no hit this turn: both refused, the Reaction not spent
	clearMonsterReactionState(camp)
	monsterReactionRound.Delete(rkey)
	for _, name := range []string{"Duck Away", "Feelers"} {
		if res := react(name); !strings.Contains(res, "no attack has hit") {
			t.Errorf("%s without a hit: %s", name, res)
		}
	}
	if _, spent := monsterReactionRound.Load(rkey); spent {
		t.Error("a refused Reaction was spent")
	}

	// a melee hit: the ranged-only trigger is refused; the Antennae path resolves; a second Reaction the same round is refused
	if !hit() {
		t.Fatal("the character's attack never hit")
	}
	if res := react("Arrow Ward"); !strings.Contains(res, "answers a ranged hit") {
		t.Errorf("a ranged-only trigger after a melee hit: %s", res)
	}
	if res := react("Feelers"); !strings.Contains(res, "uses Feelers: Snap") {
		t.Errorf("Feelers after a hit: %s", res)
	}
	if res := react("Duck Away"); !strings.Contains(res, "already used its Reaction") {
		t.Errorf("a second Reaction the same round: %s", res)
	}
}
