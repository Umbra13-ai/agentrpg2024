package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The conditions a stat-block hit gives besides its damage are applied by the one rule a failed save uses (parseHitRider, readRider,
// giveConditions), stored as condition records, and what the server does not apply stays with the GM. Test monsters are SRD Goblin
// Warrior variants with invented attacks (+60 to hit: a hit unless the d20 is a 1, so a try repeats until it hits).
func TestPostgresMonsterHitRiders(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "hit rider gm")
	_, player := pgAgent(t, "hit rider player")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Hit riders", "max_players": 2})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Rider Hero %d", time.Now().UnixNano()%1e9)))
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
	brute.Name = "Test Brute"
	brute.Actions = []srdMonsterEntry{
		atk("Slam", ". If the target is a Large or smaller creature, it has the Prone condition."),
		atk("Huge Slam", ". If the target is a Tiny or smaller creature, it has the Prone condition."),
		atk("Coil", ", and the target has the Grappled condition (escape DC 17), and it has the Restrained condition until the grapple ends."),
		atk("Zap", ", and the target has the Stunned condition until the start of the brute's next turn."),
		atk("Whim", ", and the target has the brute's choice of (A) the Charmed condition until the start of the brute's next turn or (B) the Prone condition."),
		atk("Rot", ", and the target has the Poisoned condition until the start of the brute's next turn. While Poisoned, the target can't regain Hit Points."),
	}
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variant for hit rider tests", "monsters": []srdMonster2024{brute}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Test Brute", "monster_key": key, "initiative": 20}}})
	reset := func() {
		pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,ac=10 WHERE id=$1`, char)
		removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
	}
	play := func(action string) (string, []conditionRecord) {
		t.Helper()
		for try := 0; try < 30; try++ {
			reset()
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": "Test Brute", "action": action, "target": fmt.Sprint(char)}})
			res := fmt.Sprint(r["monster_action_result"])
			if strings.Contains(res, "damage") && !strings.Contains(res, "misses") {
				recs, _ := readConditionRecords(db, camp, char)
				return res, recs
			}
		}
		t.Fatalf("%s: no hit in 30 tries", action)
		return "", nil
	}
	has := func(recs []conditionRecord, cond string) *conditionRecord {
		for i := range recs {
			if recs[i].Condition == cond {
				return &recs[i]
			}
		}
		return nil
	}
	// a Medium character is Large or smaller: Prone is stored, from the monster
	res, recs := play("Slam")
	if r := has(recs, "prone"); r == nil || !strings.Contains(r.Source, "Test Brute") || !strings.Contains(res, "Prone") {
		t.Fatalf("Slam: Prone not stored from the hit: %s %+v", res, recs)
	}
	// a gate no Medium target meets: damage, no condition, and the reply says why
	res, recs = play("Huge Slam")
	if len(recs) != 0 || !strings.Contains(res, "not prone (larger than Tiny)") {
		t.Fatalf("Huge Slam: a Medium target got the Tiny-only Prone, or the reply does not say why not: %s %+v", res, recs)
	}
	// Grappled with the stat block's escape DC, and the Restrained that ends with it
	res, recs = play("Coil")
	g, r := has(recs, "grappled"), has(recs, "restrained")
	if g == nil || len(g.Escapes) != 2 || g.Escapes[0].DC != 17 || r == nil || r.Notes != notesEndsWithGrapple {
		t.Fatalf("Coil: Grappled (escape DC 17) with the linked Restrained not stored: %s %+v", res, recs)
	}
	// Stunned until the start of the monster's next turn
	res, recs = play("Zap")
	if s := has(recs, "stunned"); s == nil || s.Notes != "until_start_of_next_turn" {
		t.Fatalf("Zap: Stunned until the start of its next turn not stored: %s %+v", res, recs)
	}
	// "until the start of the brute's next turn": the holder's own turn start does not end it, the brute's does
	f, err := readFight(db, camp)
	if err != nil {
		t.Fatal(err)
	}
	brute0 := 0
	for _, e := range f.Order {
		if sprintAny(e["name"]) == "Test Brute" {
			brute0 = asInt(e["id"])
		}
	}
	monsterFeaturesTurnStart(camp, char, "Hero")
	if recs, _ = readConditionRecords(db, camp, char); has(recs, "stunned") == nil {
		t.Fatalf("Stunned ended at the start of the Stunned character's own turn: %+v", recs)
	}
	if log := monsterFeaturesTurnStart(camp, brute0, "Test Brute"); len(log) == 0 || !strings.Contains(strings.Join(log, " "), "stunned") {
		t.Fatalf("the brute's turn start did not end its Stunned: %v", log)
	}
	if recs, _ = readConditionRecords(db, camp, char); has(recs, "stunned") != nil {
		t.Fatalf("Stunned still stored after the brute's next turn started: %+v", recs)
	}
	// a brute no longer on its feet never starts another turn. The rules supplied do not end the effect then: it stays stored and the
	// GM is told at the holder's turn start (not shortened by a guess)
	play("Zap")
	tx, _ := db.Begin()
	locked, err := lockFight(tx, camp)
	if err != nil {
		t.Fatal(err)
	}
	_, be := locked.entry(brute0)
	be["hp"] = 0
	if err := writeFight(tx, locked); err != nil || tx.Commit() != nil {
		t.Fatalf("downing the brute: %v", err)
	}
	gmLog := strings.Join(conditionOutlastsSourceNotices(camp, char, "Hero"), " ")
	if strings.Contains(strings.Join(monsterFeaturesTurnStart(camp, char, "Hero"), " "), "Nothing was changed") {
		t.Fatal("the GM notice is in the public turn-start lines")
	}
	if recs, _ = readConditionRecords(db, camp, char); has(recs, "stunned") == nil || !strings.Contains(gmLog, "Test Brute's Zap") || !strings.Contains(gmLog, "Nothing was changed") {
		t.Fatalf("Stunned from a downed monster was shortened or the GM was not told: %v %+v", gmLog, recs)
	}
	// removed from the fight: the same
	if tx, _ = db.Begin(); true {
		if locked, err = lockFight(tx, camp); err != nil {
			t.Fatal(err)
		}
		locked.remove(brute0)
		if err := writeFight(tx, locked); err != nil || tx.Commit() != nil {
			t.Fatalf("removing the brute: %v", err)
		}
	}
	gmLog = strings.Join(conditionOutlastsSourceNotices(camp, char, "Hero"), " ")
	if recs, _ = readConditionRecords(db, camp, char); has(recs, "stunned") == nil || !strings.Contains(gmLog, "no longer in the fight") {
		t.Fatalf("Stunned from a removed monster was shortened or the GM was not told: %v %+v", gmLog, recs)
	}
	// combat teardown: what the fight's end does with it is recorded here, not assumed
	pgCall(t, "POST", cp+"/combat/end", gm, nil)
	recs, _ = readConditionRecords(db, camp, char)
	if has(recs, "stunned") == nil { // open adjudication (docs/MONSTER_SYSTEM.md): the record is kept; if this changes, the docs change with it
		t.Fatalf("combat/end now removes a character's source-timed condition: update the documented behaviour: %+v", recs)
	}
	removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Test Brute", "monster_key": key, "initiative": 20}}})
	f, _ = readFight(db, camp)
	for _, e := range f.Order {
		if sprintAny(e["name"]) == "Test Brute" {
			brute0 = asInt(e["id"])
		}
	}
	// a choice or a roll is the GM's: nothing is applied, the text is shown
	res, recs = play("Whim")
	if len(recs) != 0 || !strings.Contains(res, "[GM, not applied automatically:") || !strings.Contains(res, "choice of") {
		t.Fatalf("Whim: a choice of conditions was applied or not shown to the GM: %s %+v", res, recs)
	}
	// the condition is applied with what it does while it lasts ("While Poisoned, the target can't regain Hit Points": the server's
	// since feedback log entry 63, condition_lasting_effects_2024.go), so nothing of the sentence is left to the GM
	res, recs = play("Rot")
	if has(recs, "poisoned") == nil || !strings.Contains(res, "can't regain Hit Points while Poisoned") || strings.Contains(res, "[GM, not applied automatically:") {
		t.Fatalf("Rot: Poisoned or its lasting effect not stored, or an applied effect still shown to the GM: %s %+v", res, recs)
	}
	if cant, _ := cantRegainHP(camp, char); !cant {
		t.Fatal("Rot: the target can still regain Hit Points while Poisoned")
	}
}

// A hit whose condition is rolled for ("determined by rolling") is never applied by guess: the whole rider stays the GM's.
func TestHitRiderRolledConditionIsTheGMs(t *testing.T) {
	if _, ok := parseHitRider("Melee Attack Roll: +5, reach 5 ft. Hit: 7 (1d8 + 3) Psychic damage, and the target has a condition determined by rolling 1d4: 1, Blinded; 2, Deafened; 3, Frightened; 4, Prone."); ok {
		t.Error("a rolled condition was read as a rider to apply")
	}
}
