package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// "until the end of the <creature>'s next turn" ends at the end of THAT creature's turn (the source), not at the end of the target's
// own next turn, which "until the end of its next turn" does. Invented SRD Goblin Warrior variant, +60 to hit (a try repeats on a 1).
// Route level for the hit and the stored record; the end is driven through the turn hooks (monsterFeaturesTurnStart/End), not the
// initiative-advance route.
func TestPostgresMonsterEndAnchoredToSource(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "endanchor gm")
	_, player := pgAgent(t, "endanchor player")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "End anchored", "max_players": 2})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Anchor Hero %d", time.Now().UnixNano()%1e9)))
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
	atk := func(name, tail string) srdMonsterEntry {
		return srdMonsterEntry{Name: name, Text: "Melee Attack Roll: +60, reach 10 ft. Hit: 10 (2d6 + 3) Bludgeoning damage" + tail}
	}
	brute := goblin
	brute.Name = "Anchor Brute"
	brute.Actions = []srdMonsterEntry{
		atk("Eclipse", ", and the target has the Blinded condition until the end of the brute's next turn."),
		atk("Glare", ", and the target has the Blinded condition until the start of the brute's next turn."),
		{Name: "Dimming Gaze", Text: "Dexterity Saving Throw: DC 40, one creature. Failure: 7 (2d6) Psychic damage, and the target has the Blinded condition until the end of the brute's next turn. Success: No damage."},
	}
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented variant for end-anchor tests", "monsters": []srdMonster2024{brute}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Anchor Brute", "monster_key": key, "initiative": 20}}})
	f, err := readFight(db, camp)
	if err != nil {
		t.Fatal(err)
	}
	bruteID := 0
	for _, e := range f.Order {
		if sprintAny(e["name"]) == "Anchor Brute" {
			bruteID = asInt(e["id"])
		}
	}
	if bruteID >= 0 {
		t.Fatalf("the brute is not a monster in the fight: %d", bruteID)
	}
	play := func(action string) (string, *conditionRecord) {
		t.Helper()
		for try := 0; try < 30; try++ {
			pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,ac=10 WHERE id=$1`, char)
			removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": "Anchor Brute", "action": action, "target": fmt.Sprint(char)}})
			res := fmt.Sprint(r["monster_action_result"])
			if strings.Contains(res, "damage") && !strings.Contains(res, "misses") {
				recs, _ := readConditionRecords(db, camp, char)
				for i := range recs {
					if recs[i].Condition == "blinded" {
						return res, &recs[i]
					}
				}
				t.Fatalf("%s: hit but no Blinded record: %s %+v", action, res, recs)
			}
		}
		t.Fatalf("%s: no hit in 30 tries", action)
		return "", nil
	}
	blinded := func() bool {
		recs, _ := readConditionRecords(db, camp, char)
		for _, r := range recs {
			if r.Condition == "blinded" {
				return true
			}
		}
		return false
	}
	res, r := play("Eclipse")
	skipped := strings.HasSuffix(r.Notes, ":skip_current") // the first initiative may be the brute's own (random roll): then its current turn's end is skipped
	if strings.TrimSuffix(r.Notes, ":skip_current") != "until_end_of_next_turn" || r.SourceID != bruteID || r.DurationRounds != 0 {
		t.Fatalf("the end-anchored record is wrong (want notes until_end_of_next_turn, source %d, no target-timed rounds): %+v / %s", bruteID, *r, res)
	}
	monsterFeaturesTurnStart(camp, bruteID, "Anchor Brute")
	if !blinded() {
		t.Fatal("Blinded ended at the START of the brute's turn: it lasts until the END")
	}
	// another creature's turn end does not end it
	other := bruteID - 1
	monsterFeaturesTurnEnd(camp, other, "Someone Else")
	if !blinded() {
		t.Fatal("Blinded ended at another creature's turn end")
	}
	if skipped {
		monsterFeaturesTurnEnd(camp, bruteID, "Anchor Brute")
	}
	if log := strings.Join(monsterFeaturesTurnEnd(camp, bruteID, "Anchor Brute"), " "); !strings.Contains(log, "blinded") || blinded() {
		t.Fatalf("the brute's turn end did not end Blinded (log %q)", log)
	}
	// control: the start-anchored text still ends at the brute's turn START and not at its turn end
	if _, r = play("Glare"); r.Notes != "until_start_of_next_turn" {
		t.Fatalf("Glare record: %+v", *r)
	}
	monsterFeaturesTurnEnd(camp, bruteID, "Anchor Brute")
	if !blinded() {
		t.Fatal("a start-anchored condition ended at the brute's turn end")
	}
	monsterFeaturesTurnStart(camp, bruteID, "Anchor Brute")
	if blinded() {
		t.Fatal("a start-anchored condition did not end at the brute's turn start")
	}
	// the same end-anchored text in a SAVE action (not a hit): stored the same way
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000 WHERE id=$1`, char)
	removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
	_, sr := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
		"monster_action": map[string]interface{}{"monster": "Anchor Brute", "action": "Dimming Gaze", "target": fmt.Sprint(char)}})
	recs, _ := readConditionRecords(db, camp, char)
	var saveRec *conditionRecord
	for i := range recs {
		if recs[i].Condition == "blinded" {
			saveRec = &recs[i]
		}
	}
	if saveRec == nil || strings.TrimSuffix(saveRec.Notes, ":skip_current") != "until_end_of_next_turn" || saveRec.SourceID != bruteID || saveRec.DurationRounds != 0 {
		t.Fatalf("save-action end-anchored record is wrong: %+v / %v", saveRec, sr["monster_action_result"])
	}
	monsterFeaturesTurnEnd(camp, other, "Someone Else")
	if !blinded() {
		t.Fatal("save action: Blinded ended at another creature's turn end")
	}
	if strings.HasSuffix(saveRec.Notes, ":skip_current") {
		monsterFeaturesTurnEnd(camp, bruteID, "Anchor Brute")
	}
	monsterFeaturesTurnEnd(camp, bruteID, "Anchor Brute")
	if blinded() {
		t.Fatal("save action: the brute's turn end did not end Blinded")
	}
	removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
	// through initiative advancement (POST combat/next), which calls the turn hooks itself
	lastNext := ""
	next := func() {
		t.Helper()
		c, j := pgCall(t, "POST", cp+"/combat/next", gm, map[string]interface{}{})
		if c != 200 {
			t.Fatalf("combat/next: %d %v", c, j)
		}
		lastNext = fmt.Sprint(j)
	}
	toTurn := func(id int) {
		t.Helper()
		for i := 0; i < 6 && currentTurnID(camp) != id; i++ {
			next()
		}
		if currentTurnID(camp) != id {
			t.Fatalf("could not reach the turn of %d (current %d)", id, currentTurnID(camp))
		}
	}
	// A: applied during the source's OWN turn. The end of that turn is not its next turn: Blinded stays through it, through the other
	// creature's turn, and ends at the end of the source's following turn.
	toTurn(bruteID)
	_, r = play("Eclipse")
	if !strings.HasSuffix(r.Notes, ":skip_current") {
		t.Fatalf("applied on the source's own turn but not marked to skip that turn's end: %+v", *r)
	}
	next() // the brute's current turn ends
	if !blinded() {
		t.Fatal("A: Blinded ended at the end of the source's CURRENT turn, which is not its next turn")
	}
	toTurn(char)
	// control: the source is up, so entering the holder's turn gives no "source is down" notice
	if strings.Contains(lastNext, "still has the blinded condition") {
		t.Fatalf("A: a source-is-down notice with the source up: %.300s", lastNext)
	}
	next() // the hero's turn ends
	if !blinded() {
		t.Fatal("A: Blinded ended at the end of another creature's turn")
	}
	toTurn(bruteID)
	if !blinded() {
		t.Fatal("A: Blinded ended at the start of the source's next turn")
	}
	next() // the brute's next turn ends
	if blinded() {
		t.Fatal("A: Blinded still stored after the end of the source's next turn")
	}
	// B: applied OUTSIDE the source's turn (the hero's turn): the source's first turn end ends it.
	toTurn(char)
	_, r = play("Eclipse")
	if strings.Contains(r.Notes, "skip") {
		t.Fatalf("B: applied outside the source's turn but marked to skip: %+v", *r)
	}
	next() // the hero's turn ends; the brute's turn starts
	if !blinded() {
		t.Fatal("B: Blinded ended before the source's turn ended")
	}
	if currentTurnID(camp) != bruteID {
		t.Fatalf("B: expected the brute's turn, got %d", currentTurnID(camp))
	}
	next() // the brute's turn ends: its next turn after the application
	if blinded() {
		t.Fatal("B: Blinded still stored after the source's first turn end")
	}
	// C: the source is down (0 Hit Points) while the condition stands. The text gives no early end: it stays, and the GM is told at the
	// holder's turn (the notice comes back in the combat/next reply).
	// a second monster keeps the fight going (with every enemy down the advance stops before any turn effects)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Anchor Bystander", "monster_key": key, "initiative": 3}}})
	toTurn(char)
	_, r = play("Eclipse")
	tx, _ := db.Begin()
	locked, err := lockFight(tx, camp)
	if err != nil {
		t.Fatal(err)
	}
	_, be := locked.entry(bruteID)
	be["hp"] = 0
	if err := writeFight(tx, locked); err != nil || tx.Commit() != nil {
		t.Fatalf("downing the brute: %v", err)
	}
	notice := ""
	for i := 0; i < 6; i++ {
		_, j := pgCall(t, "POST", cp+"/combat/next", gm, map[string]interface{}{})
		out := fmt.Sprint(j)
		if strings.Contains(out, "still has the blinded condition") {
			notice += out
		}
		if !blinded() {
			t.Fatalf("C: Blinded ended after its source went down, though the text gives no such end (turn %d)", i)
		}
	}
	if !strings.Contains(notice, "gm_notices") || !strings.Contains(notice, "end of that creature's next turn") {
		t.Fatalf("C: the GM was not told that the condition outlasts its downed source: %.300s", notice)
	}
	// D: audience. The log is returned only to the GM's own routes. A player gets none of it from the turn routes (refused), and the
	// notice is not in what a player can read (my-turn, the campaign log).
	for _, rt := range []struct {
		path string
		body map[string]interface{}
	}{
		{cp + "/combat/next", map[string]interface{}{}},
		{cp + "/combat/skip", map[string]interface{}{}},
		{cp + "/combat/remove", map[string]interface{}{"combatant_id": bruteID}},
		{"/api/gm/narrate", map[string]interface{}{"campaign_id": camp, "narration": "x", "advance_turn": true}},
	} {
		code, j := pgCall(t, "POST", rt.path, player, rt.body)
		out := fmt.Sprint(j)
		if code != 403 || !strings.Contains(out, "not_gm") || strings.Contains(out, "gm_notices") || strings.Contains(out, "still has the blinded") {
			t.Errorf("D: a player got %d from %s: %.200s", code, rt.path, out)
		}
	}
	for _, rd := range []struct{ method, path string }{{"GET", "/api/my-turn"}, {"GET", cp + "/log"}, {"GET", cp}} {
		_, j := pgCall(t, rd.method, rd.path, player, nil)
		if out := fmt.Sprint(j); strings.Contains(out, "still has the blinded") || strings.Contains(out, "gm_notices") {
			i := strings.Index(out, "still has the blinded")
			j2 := strings.Index(out, "gm_notices")
			t.Errorf("D: a player-readable %s shows the GM notice: at %d/%d: %.300s", rd.path, i, j2, out[max(0, max(i, j2)-150):])
		}
	}
	if !blinded() {
		t.Fatal("D: a player's refused call changed the condition")
	}
}
