package main

// Who in a fight is the party's enemy (I-CALLA-29): a creature is hostile unless the GM recorded it Friendly or Indifferent
// (attitudes_2024.go); my-turn lists the others apart and its hints follow; a combatant's attitude ends with its fight. And what
// happened since a character's own last action leaves out the log's bookkeeping (I-CALLA-28).

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestPartyHintsFollowTheFightsSides(t *testing.T) {
	if h := strings.Join(partyHints("healthy", nil, fightSides{InFight: true, Hostile: 1}), " | "); !strings.Contains(h, "Press the attack") {
		t.Errorf("a fight with an enemy: %s", h)
	}
	if h := strings.Join(partyHints("healthy", nil, fightSides{InFight: true}), " | "); strings.Contains(h, "Press the attack") || !strings.Contains(h, "no creature in the fight is hostile") {
		t.Errorf("a fight with no enemy: %s", h)
	}
	if h := strings.Join(partyHints("healthy", nil, fightSides{}), " | "); strings.Contains(h, "attack") {
		t.Errorf("no fight: %s", h)
	}
	guard := map[string]interface{}{"name": "Wagon Guard", "attitude": "friendly", "status": "bloodied"}
	if h := strings.Join(partyHints("healthy", nil, fightSides{InFight: true, Hostile: 1, Others: []map[string]interface{}{guard}}), " | "); !strings.Contains(h, "Wagon Guard (friendly) is bloodied. It is not one of your enemies.") || strings.Contains(h, "good shape") {
		t.Errorf("a wounded friendly creature: %s", h)
	}
	if isPlayRecord("following") || isPlayRecord("joined") || !isPlayRecord("attack") {
		t.Error("isPlayRecord does not follow notPlayTypes")
	}
}

func TestPostgresFightSidesInMyTurn(t *testing.T) {
	testPostgres(t)
	gm, player, camp, hero, raider := earlyRulesFixture(t, "Test Raider", []srdMonsterEntry{{Name: "Club", Text: "Melee Attack Roll: +4, reach 5 ft. Hit: 2 (1d4) Bludgeoning damage."}})
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	if c, j := pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Wagon Guard", "monster_key": "guard", "hp": 11, "attitude": "Friendly", "initiative": -10},
		{"name": "Merchant", "monster_key": "commoner", "hp": 4, "initiative": -11}}}); c != 200 || !strings.Contains(fmt.Sprint(j), "attitude:friendly") {
		t.Fatalf("add: %d %v", c, j)
	}
	if c, _ := pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Odd", "monster_key": "commoner", "attitude": "smug"}}}); c != 400 {
		t.Errorf("an attitude that is no attitude: %d", c)
	}
	guard := monsterIDsByKeyOrName(camp, "Wagon Guard")[0]
	merchant := monsterIDsByKeyOrName(camp, "Merchant")[0]
	if c, j := pgCall(t, "POST", "/api/gm/attitude", gm, map[string]interface{}{"campaign_id": camp, "target_id": merchant, "attitude": "indifferent"}); c != 200 {
		t.Fatalf("attitude: %d %v", c, j)
	}
	setMonsterHP(camp, guard, 2) // a bolt through his arm: a quarter of 11 or fewer
	situation := func() (map[string]interface{}, string) {
		t.Helper()
		_, j := pgCall(t, "GET", fmt.Sprintf("/api/my-turn?view=short&character_id=%d", hero), player, nil)
		b, _ := json.Marshal(j["tactical_suggestions"])
		s, _ := j["situation"].(map[string]interface{})
		return s, string(b)
	}
	s, hints := situation()
	enemies, others := fmt.Sprint(s["enemies"]), fmt.Sprint(s["others"])
	if !strings.Contains(enemies, "Test Raider") || strings.Contains(enemies, "Wagon Guard") || strings.Contains(enemies, "Merchant") {
		t.Errorf("enemies: %s", enemies)
	}
	if !strings.Contains(others, "Wagon Guard") || !strings.Contains(others, "friendly, critical") || !strings.Contains(others, "Merchant") || !strings.Contains(others, "indifferent") {
		t.Errorf("others: %s", others)
	}
	if !strings.Contains(hints, "Wagon Guard (friendly) is critical. It is not one of your enemies.") || strings.Contains(hints, "good shape") {
		t.Errorf("hints: %s", hints)
	}
	setMonsterHP(camp, raider, 0)
	setMonsterHP(camp, guard, 11)
	if _, hints = situation(); !strings.Contains(hints, "no creature in the fight is hostile") {
		t.Errorf("no enemy left: %s", hints)
	}
	// a new fight gives its ids again from -1: the old attitudes end with the old fight
	pgCall(t, "POST", cp+"/combat/end", gm, nil)
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Ambusher A", "monster_key": "bandit", "initiative": -10}, {"name": "Ambusher B", "monster_key": "bandit", "initiative": -11},
		{"name": "Ambusher C", "monster_key": "bandit", "initiative": -12}}})
	if len(fightAttitudes(camp)) != 0 {
		t.Errorf("attitudes outlived their fight: %v", fightAttitudes(camp))
	}
	if s, _ = situation(); strings.Count(fmt.Sprint(s["enemies"]), "Ambusher") != 3 {
		t.Errorf("the new fight's bandits: %v / others %v", s["enemies"], s["others"])
	}
	// bookkeeping since the character's own last action: an automatic "following" mark is not play (I-CALLA-28)
	writeAction(db, camp, hero, "following", "Automatically marked as following the party (12h+ inactive)", "Inactive for 13 hours. Auto-marked by system.")
	_, j := pgCall(t, "GET", fmt.Sprintf("/api/my-turn?character_id=%d", hero), player, nil)
	if b, _ := json.Marshal(j["since_your_last_action"]); strings.Contains(string(b), "Automatically marked as following") {
		t.Errorf("since_your_last_action shows bookkeeping: %s", b)
	}
}
