package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// "The target can't take Reactions" with the end of the target's own next turn (disadvantage_riders_2024.go): applied as the marker
// no_reactions through the narrate route on a monster and a character target, with the save failed and passed, read by the monster
// Reaction resolver and by the character's reactionReady, and gone at the end of the target's next turn. An end anchored to the
// attacker ("until the start of the brute's next turn") is not applied and stays with the GM in the reply.
func TestPostgresCantTakeReactionsRider(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "noreact gm")
	_, player := pgAgent(t, "noreact player")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "No reactions", "max_players": 2})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("NoReact Hero %d", time.Now().UnixNano()%1e9)))
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
	tail := " Until the end of the target's next turn, the target's Speed is halved; the target can't take Reactions; and it can take either an action or a Bonus Action on its turn, not both."
	ray := goblin
	ray.Name = "Slow Ray Caster"
	ray.Actions = []srdMonsterEntry{
		{Name: "Hard Ray", Text: "Constitution Saving Throw: DC 40, one creature. Failure: 10 (3d6) Necrotic damage." + tail + " Success: Half damage only."},
		{Name: "Soft Ray", Text: "Constitution Saving Throw: DC 1, one creature. Failure: 10 (3d6) Necrotic damage." + tail + " Success: Half damage only."},
		{Name: "Brute Blade", Text: "Constitution Saving Throw: DC 40, one creature. Failure: 10 (3d6) Necrotic damage, and the target can't take Reactions until the start of the caster's next turn."},
	}
	victim := goblin
	victim.Name = "Slow Ray Victim"
	victim.HP = 500
	victim.Reactions = []srdMonsterEntry{{Name: "Parry", Text: "Trigger: The victim is hit by a melee attack roll while holding a weapon. Response: The victim adds 99 to its AC against that attack, possibly causing it to miss."}}
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variants for the no-reactions test", "monsters": []srdMonster2024{ray, victim}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	keys := map[string]string{}
	for _, m := range j["monsters"].([]interface{}) {
		mm := m.(map[string]interface{})
		keys[fmt.Sprint(mm["name"])] = fmt.Sprint(mm["monster_key"])
	}
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Slow Ray Caster", "monster_key": keys["Slow Ray Caster"], "initiative": 20}, {"name": "Slow Ray Victim", "monster_key": keys["Slow Ray Victim"], "initiative": 1}}})
	f, _ := readFight(db, camp)
	ids := map[string]int{}
	idx := map[string]int{}
	for i, e := range f.Order {
		ids[sprintAny(e["name"])] = asInt(e["id"])
		idx[sprintAny(e["name"])] = i
	}
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,reaction_used=false WHERE id=$1`, char)
	pgExec(t, `UPDATE combat_state SET current_turn_index=$1 WHERE lobby_id=$2`, idx["Slow Ray Caster"], camp)
	cast := func(action string, target string) string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Slow Ray Caster", "action": action, "target": target}})
		return fmt.Sprint(r["monster_action_result"])
	}
	vid := ids["Slow Ray Victim"]
	clear := func(id int) { removeConditionsWhere(camp, id, func(conditionRecord) bool { return true }) }
	// a failed save: marker given, listed once, not left for the GM; a passed save: none
	for _, tg := range []struct {
		name string
		id   int
		turn int
	}{{fmt.Sprint(char), char, idxOfID(f, char)}, {"Slow Ray Victim", vid, idx["Slow Ray Victim"]}} {
		clear(tg.id)
		if res := cast("Soft Ray", tg.name); hasCondMarker(camp, tg.id, "no_reactions") {
			t.Fatalf("%s: the marker was given on a passed save: %s", tg.name, res)
		}
		clear(tg.id)
		res := cast("Hard Ray", tg.name)
		if !hasCondMarker(camp, tg.id, "no_reactions") || !hasCondMarker(camp, tg.id, "speed_halved") {
			t.Fatalf("%s: failed save left no marker: %s", tg.name, res)
		}
		if !strings.Contains(res, "can't take Reactions until the end of its next turn") || !strings.Contains(res, "either an action or a Bonus Action") {
			t.Fatalf("%s: the reply does not list the applied rider and the action limit left for the GM: %s", tg.name, res)
		}
		if tg.id == char && reactionReady(char) {
			t.Fatal("the character can still take a Reaction")
		}
		turnEffects(camp, tg.turn, "end_of_turn")
		if hasCondMarker(camp, tg.id, "no_reactions") {
			t.Fatalf("%s: the marker outlived the end of the target's next turn", tg.name)
		}
		if tg.id == char && !reactionReady(char) {
			t.Fatal("the character's Reaction did not come back with the end of the turn")
		}
	}
	// entry points: the shared checks refuse a character's Reaction under the marker and spend nothing
	clear(char)
	cast("Hard Ray", fmt.Sprint(char))
	pgExec(t, `UPDATE characters SET reaction_used=false WHERE id=$1`, char)
	// what the character is shown: turn_left and my-turn say the Reaction is not available while the marker stands (and are back after)
	pgExec(t, `UPDATE combat_state SET current_turn_index=$1 WHERE lobby_id=$2`, idxOfID(f, char), camp)
	if tl := turnLeft(char); tl == nil || tl["reaction"] != "used" {
		t.Fatalf("turn_left shows the Reaction as usable under the marker: %v", tl)
	}
	_, mt := pgCall(t, "GET", "/api/my-turn", player, nil)
	opts, _ := mt["your_options"].(map[string]interface{})
	econ, _ := opts["action_economy"].(map[string]interface{})
	if econ == nil || econ["reaction"] != false {
		t.Fatalf("my-turn shows the Reaction as usable under the marker: action_economy %v reaction %v", econ, opts["reaction"])
	}
	if ok, _, why := checkActionEconomy(char, "opportunity_attack", 0); ok || !strings.Contains(why, "can't take Reactions") {
		t.Fatalf("the action economy check let a Reaction through under the marker: %v %q", ok, why)
	}
	if _, ref := featureTurnCost(char, "opportunity_attack"); ref == nil || ref.Code != "cant_take_reactions" {
		t.Fatalf("featureTurnCost let a Reaction through under the marker: %v", ref)
	}
	if err := spendTurnResource(char, "reaction"); err == nil {
		t.Fatal("spendTurnResource spent a Reaction under the marker")
	}
	var spent bool
	db.QueryRow(`SELECT COALESCE(reaction_used,false) FROM characters WHERE id=$1`, char).Scan(&spent)
	if spent {
		t.Fatal("a refused Reaction was marked used")
	}
	clear(char)
	if err := spendTurnResource(char, "reaction"); err != nil {
		t.Fatalf("a Reaction without the marker was refused: %v", err)
	}
	pgExec(t, `UPDATE characters SET reaction_used=false WHERE id=$1`, char)
	// the monster's Reaction is refused while the marker stands and nothing is used up
	clear(vid)
	cast("Hard Ray", "Slow Ray Victim")
	monsterReactionRound.Delete(fmt.Sprintf("%d:%d", camp, vid))
	_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
		"monster_action": map[string]interface{}{"monster": "Slow Ray Victim", "action": "Parry"}})
	if pr := fmt.Sprint(r["monster_action_result"]); !strings.Contains(pr, "can't take Reactions") {
		t.Fatalf("a Reaction was taken under the marker: %s", pr)
	}
	if _, used := monsterReactionRound.Load(fmt.Sprintf("%d:%d", camp, vid)); used {
		t.Fatal("a refused Reaction was counted as used")
	}
	// an end anchored to the attacker is not applied, and the GM is told
	clear(vid)
	if res := cast("Brute Blade", "Slow Ray Victim"); hasCondMarker(camp, vid, "no_reactions") || !strings.Contains(res, "can't take Reactions") {
		t.Fatalf("an attacker-anchored end was applied or dropped: %s", res)
	}
}
