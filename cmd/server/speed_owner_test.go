package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSpeedFactorsCombine(t *testing.T) {
	ten := 10
	cases := []struct {
		name    string
		f       speedFactors
		speed   int
		want    int
		settled bool
	}{
		{"nothing", speedFactors{}, 30, 30, true},
		{"0 can't increase: wins over Haste", speedFactors{Zero: true, Doubles: []string{"haste"}}, 30, 0, true},
		{"0 wins over a reduction and a halving", speedFactors{Zero: true, Halves: []string{"a"}, Minuses: []int{10}}, 30, 0, true},
		{"one halving", speedFactors{Halves: []string{"slow"}}, 30, 15, true},
		{"one halving of a Speed that already carries bonuses and Exhaustion (35)", speedFactors{Halves: []string{"slow"}}, 35, 17, true},
		{"one flat reduction", speedFactors{Minuses: []int{10}}, 30, 20, true},
		{"one doubling", speedFactors{Doubles: []string{"haste"}}, 30, 60, true},
		{"two flat reductions from different effects add", speedFactors{Minuses: []int{10, 15}}, 30, 5, true},
		{"Slow with Haste on an even Speed: every order gives 30", speedFactors{Halves: []string{"slow"}, Doubles: []string{"haste"}}, 30, 30, true},
		{"Slow with Haste on 35: the orders differ (34, 35)", speedFactors{Halves: []string{"slow"}, Doubles: []string{"haste"}}, 35, 35, false},
		{"flat reduction with a halving: the orders differ (10, 5)", speedFactors{Minuses: []int{10}, Halves: []string{"slow"}}, 30, 30, false},
		{"flat reduction with a doubling: the orders differ (40, 50)", speedFactors{Minuses: []int{10}, Doubles: []string{"haste"}}, 30, 30, false},
		{"two halvings: stacking not said", speedFactors{Halves: []string{"slow", "speed_halved"}}, 30, 30, false},
		{"the GM's ruling decides", speedFactors{Halves: []string{"a"}, Minuses: []int{10}, Set: &ten}, 30, 10, true},
	}
	for _, c := range cases {
		got, ok := c.f.apply(c.speed)
		if got != c.want || ok != c.settled {
			t.Errorf("%s: %d settled=%v, want %d settled=%v", c.name, got, ok, c.want, c.settled)
		}
	}
}

func TestSpeedRiderParsing(t *testing.T) {
	for _, c := range []struct {
		text string
		mode string
		end  riderEnd
	}{
		{"Failure: 27 (6d8) Fire damage, and the target's Speed is halved until the end of its next turn.", "halved", riderEndTargetTurnEnd},
		{"Failure: 7 (2d6) Cold damage, and the target's Speed is 0 until the end of the target's next turn.", "zero", riderEndTargetTurnEnd},
		{"Failure or Success: The target's Speed is reduced to 0 until the end of its next turn.", "zero", riderEndTargetTurnEnd},
		{"Hit: 18 (3d8 + 5) Piercing damage, and the target's Speed decreases by 10 feet until the end of the target's next turn.", "minus", riderEndTargetTurnEnd},
		// the source's turn: ended by expireSourceTurnConditions on that creature's turn (rider_duration_2024.go)
		{"Failure: The target's Speed is halved until the end of the dragon's next turn.", "halved", riderEndSourceTurnEnd},
		{"Hit: 6 (1d8 + 2) Cold damage, and the target's Speed decreases by 5 feet until the start of the raider's next turn.", "minus", riderEndSourceTurnStart},
		{"Failure: 5 (2d4) Fire damage, and the target’s Speed decreases by 10 feet until the end of the mephit’s next turn.", "minus", riderEndSourceTurnEnd},
		// the duration in its own sentence of the effect (Slowing Breath)
		{"Failure: The target can’t take Reactions; its Speed is halved; and it can take either an action or a Bonus Action on its turn, not both. This effect lasts until the end of its next turn.", "halved", riderEndTargetTurnEnd},
		{"Failure: The target's Speed is halved until the start of its next turn.", "", riderEndNone}, // whose turn is not said: the GM's
		{"Failure: The target's Speed is halved.", "", riderEndNone},                                  // no duration given: the GM's
	} {
		var e saveEffect
		e.readSpeed(c.text)
		if e.SpeedMode != c.mode || e.SpeedEnd != c.end {
			t.Errorf("%q: mode %q end %d, want %q end %d", c.text, e.SpeedMode, e.SpeedEnd, c.mode, c.end)
		}
	}
	e, ok := parseHitRider("Melee Attack Roll: +5, reach 5 ft. Hit: 18 (3d8 + 5) Piercing damage, and the target's Speed decreases by 10 feet until the end of the target's next turn.")
	if !ok || e.SpeedMode != "minus" || e.SpeedMinus != 10 || e.Extra != "" {
		t.Errorf("hit rider: %+v ok=%v (the whole text is applied, nothing is left for the GM)", e, ok)
	}
}

// A Speed rider is a marker on the target that the Speed owner reads; the movement budget follows it in the middle of a turn; it ends
// at the end of the target's next turn (twice around when the target is acting now); the other sources of a Speed change are the
// same owner's.
func TestPostgresSpeedOwner(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "speed gm")
	_, player := pgAgent(t, "speed player")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Speed owner", "max_players": 2})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Speed Hero %d", time.Now().UnixNano()%1e9)))
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
	brute := goblin
	brute.Name = "Speed Brute"
	brute.Actions = []srdMonsterEntry{
		{Name: "Scorch", Text: "Dexterity Saving Throw: DC 40, each creature in a 15-foot Cone. Failure: 7 (2d6) Fire damage, and the target's Speed is halved until the end of its next turn. Success: Half damage."},
		{Name: "Freeze", Text: "Dexterity Saving Throw: DC 40, each creature in a 15-foot Cone. Failure: 7 (2d6) Cold damage, and the target's Speed is 0 until the end of the target's next turn. Success: Half damage."},
		{Name: "Shot", Text: "Ranged Attack Roll: +60, range 150/600 ft. Hit: 18 (3d8 + 5) Piercing damage, and the target's Speed decreases by 10 feet until the end of the target's next turn."},
		{Name: "Drag", Text: "Dexterity Saving Throw: DC 40, each creature in a 15-foot Cone. Failure: 7 (2d6) Cold damage, and the target's Speed is halved until the end of the dragon's next turn. Success: Half damage."},
	}
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variant for Speed tests", "monsters": []srdMonster2024{brute}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Speed Brute", "monster_key": key, "initiative": 20}}})
	base := characterSpeedOf(char)
	if base < 20 {
		t.Fatalf("base Speed %d", base)
	}
	reset := func() {
		pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,ac=10 WHERE id=$1`, char)
		removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
		db.Exec(`DELETE FROM spell_effects WHERE lobby_id=$1 AND target_id=$2`, camp, char)
		db.Exec(`DELETE FROM combat_effects WHERE lobby_id=$1 AND target_id=$2`, camp, char)
		setMovementBudget(char, base)
	}
	marker := func(cond string) *conditionRecord {
		recs, _ := readConditionRecords(db, camp, char)
		for i := range recs {
			if recs[i].Condition == cond {
				return &recs[i]
			}
		}
		return nil
	}
	remaining := func() int {
		var n int
		db.QueryRow(`SELECT movement_remaining FROM characters WHERE id=$1`, char).Scan(&n)
		return n
	}
	play := func(action, cond string) (string, *conditionRecord) {
		t.Helper()
		for try := 0; try < 30; try++ {
			reset()
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": "Speed Brute", "action": action, "target": fmt.Sprint(char)}})
			res := fmt.Sprint(r["monster_action_result"])
			if m := marker(cond); m != nil {
				return res, m
			}
			if !strings.Contains(res, "damage") && !strings.Contains(res, "Speed") {
				t.Logf("%s: %s", action, res)
			}
		}
		return "", nil
	}
	// halved: stored from the stat block's text, counted down at the end of the target's turns, read by the owner
	res, m := play("Scorch", "speed_halved")
	wantRounds := 1
	if currentTurnID(camp) == char { // the target is acting now: this turn is not its next turn
		wantRounds = 2
	}
	if m == nil || m.DurationRounds != wantRounds || !strings.Contains(m.Source, "Speed Brute") || !strings.Contains(res, "Speed halved until the end of its next turn") {
		t.Fatalf("Scorch: halved marker not stored with one round: %+v %s", m, res)
	}
	if strings.Contains(res, "GM handles the rest") {
		t.Fatalf("Scorch: the Speed rider was handed to the GM although it is applied: %s", res)
	}
	if got := characterSpeedOf(char); got != base/2 {
		t.Fatalf("Speed with halved %d, want %d", got, base/2)
	}
	// in the middle of a turn: 10 ft already moved, then the Speed is halved: the budget is the new Speed minus what was spent
	reset()
	pgExec(t, `UPDATE characters SET movement_remaining=$1 WHERE id=$2`, base-10, char)
	addCondition(camp, char, condAdd{Condition: "speed_halved", Source: "x", Rounds: 1})
	settleMovement(char)
	if got, want := remaining(), max(base/2-10, 0); got != want {
		t.Fatalf("budget after a mid-turn halving: %d, want %d", got, want)
	}
	settleMovement(char) // settling twice changes nothing
	if got, want := remaining(), max(base/2-10, 0); got != want {
		t.Fatalf("budget settled twice: %d, want %d", got, want)
	}
	removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
	settleMovement(char) // the effect ends mid-turn: the Speed comes back
	if got := remaining(); got != base-10 {
		t.Fatalf("budget after the effect ended: %d, want %d", got, base-10)
	}
	// Speed 0, and the budget at the next turn start
	res, m = play("Freeze", "speed_zero")
	if m == nil || characterSpeedOf(char) != 0 {
		t.Fatalf("Freeze: Speed 0 not stored or not read: %+v %s", m, res)
	}
	resetTurnFlags(char, characterSpeedOf(char))
	if remaining() != 0 {
		t.Fatalf("the turn start gave %d ft of movement at Speed 0", remaining())
	}
	// a hit's flat reduction
	res, m = play("Shot", "speed_minus")
	if m == nil || m.Detail != "10" || characterSpeedOf(char) != base-10 {
		t.Fatalf("Shot: -10 not stored or not read: %+v %s", m, res)
	}
	// a rider that ends on the attacker's turn is the attacker's marker: it ends at the end of the attacker's next turn, not on the
	// target's turns (expireSourceTurnConditions), and nothing of it is left for the GM
	reset()
	f, _ := readFight(db, camp)
	bruteID, charIdx := 0, -1
	for i, e := range f.Order {
		if sprintAny(e["name"]) == "Speed Brute" {
			bruteID = asInt(e["id"])
		}
		if asInt(e["id"]) == char {
			charIdx = i
		}
	}
	pgExec(t, `UPDATE combat_state SET current_turn_index=$1 WHERE lobby_id=$2`, charIdx, camp) // another creature is acting
	_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
		"monster_action": map[string]interface{}{"monster": "Speed Brute", "action": "Drag", "target": fmt.Sprint(char)}})
	res = fmt.Sprint(r["monster_action_result"])
	if m := marker("speed_halved"); m == nil || m.SourceID != bruteID || m.Notes != notesUntilEndOfSourceTurn || m.DurationRounds != 0 ||
		!strings.Contains(res, "Speed halved until the end of Speed Brute's next turn") || strings.Contains(res, "not applied") {
		t.Fatalf("Drag: the attacker-anchored Speed rider was not recorded for the attacker's turn end: %+v %s", m, res)
	}
	turnEffects(camp, charIdx, "end_of_turn") // the target's own turn end: not its end
	expireSourceTurnConditions(camp, bruteID, "Speed Brute", "start")
	if marker("speed_halved") == nil {
		t.Fatal("Drag: the marker ended on the target's turn end or the attacker's turn start")
	}
	expireSourceTurnConditions(camp, bruteID, "Speed Brute", "end")
	if marker("speed_halved") != nil {
		t.Fatal("Drag: the marker outlived the end of the attacker's next turn")
	}
	// timing: the marker runs out at the end of the target's next turn, not at the end of the turn it is acting now
	idx := -1
	for i, e := range f.Order {
		if asInt(e["id"]) == char {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("character not in the turn order")
	}
	other := 1 - idx
	setTurn := func(i int) { pgExec(t, `UPDATE combat_state SET current_turn_index=$1 WHERE lobby_id=$2`, i, camp) }
	reset()
	setTurn(other) // another creature is acting: the target's next turn is its coming one
	e := saveEffect{SpeedMode: "halved", SpeedEnd: riderEndTargetTurnEnd}
	e.giveSpeed(camp, char, "t")
	if m := marker("speed_halved"); m == nil || m.DurationRounds != 1 {
		t.Fatalf("applied on another creature's turn: %+v, want 1 round", m)
	}
	turnEffects(camp, idx, "end_of_turn")
	if marker("speed_halved") != nil {
		t.Fatal("halved Speed outlived the end of the target's next turn")
	}
	reset()
	setTurn(idx) // the target is acting now: this turn is not its next turn
	e.giveSpeed(camp, char, "t")
	if m := marker("speed_halved"); m == nil || m.DurationRounds != 2 {
		t.Fatalf("applied on its own turn: %+v, want 2 rounds", m)
	}
	turnEffects(camp, idx, "end_of_turn")
	if marker("speed_halved") == nil {
		t.Fatal("halved Speed ended at the end of the turn it was applied in")
	}
	turnEffects(camp, idx, "end_of_turn")
	if marker("speed_halved") != nil {
		t.Fatal("halved Speed outlived the end of its next turn")
	}
	// Disadvantage riders: stored state, read by the attack and save owners, ended with the target's next turn
	reset()
	setTurn(other)
	d := saveEffect{DisadvMode: []string{"attacks", "concentration"}, RollRiderEnd: map[string]riderEnd{"attacks": riderEndTargetTurnEnd, "concentration": riderEndTargetTurnEnd}}
	d.giveConditions(camp, char, "t")
	if !hasCondMarker(camp, char, "disadv_attacks") || !hasCondMarker(camp, char, "disadv_concentration") {
		t.Fatal("the Disadvantage markers were not stored")
	}
	if r := (&attackCtx{attacker: char, lobby: camp, target: -1}).gather(); !strings.Contains(strings.Join(r.Disadvantage, ","), "Disadvantage rider") {
		t.Fatalf("an attack roll did not read the marker: %v", r.Disadvantage)
	}
	if sv := (&saveCtx{lobby: camp, saver: char, ability: "con", dc: 10, concentration: true, peek: true}).gather(); !strings.Contains(strings.Join(sv.Disadvantage, ","), "Concentration saves") {
		t.Fatalf("a Concentration save did not read the marker: %v", sv.Disadvantage)
	}
	if sv := (&saveCtx{lobby: camp, saver: char, ability: "con", dc: 10, peek: true}).gather(); strings.Contains(strings.Join(sv.Disadvantage, ","), "Concentration saves") {
		t.Fatal("an ordinary Constitution save read the Concentration marker")
	}
	turnEffects(camp, idx, "end_of_turn")
	if hasCondMarker(camp, char, "disadv_attacks") || hasCondMarker(camp, char, "disadv_concentration") {
		t.Fatal("a Disadvantage marker outlived the end of the target's next turn")
	}
	if r := (&attackCtx{attacker: char, lobby: camp, target: -1}).gather(); strings.Contains(strings.Join(r.Disadvantage, ","), "Disadvantage rider") {
		t.Fatal("an attack roll read an ended marker")
	}
	// "Failure or Success:": conditions and Speed of that section are given whatever the save's result; the rest stays with the GM
	reset()
	fs, _ := parseSaveEffect("Wisdom Saving Throw: DC 19, each creature in a 30-foot Emanation. Failure: 10 (3d6) Psychic damage. Success: Half damage. Failure or Success: The target has the Frightened condition until the start of the lich's next turn. The target's Speed is 0 until the end of its next turn. The lich can't take this action again until the start of its next turn.")
	if fs.Always == nil || len(fs.Always.Conditions) != 1 || fs.Always.SpeedMode != "zero" || len(fs.Conditions) != 0 {
		t.Fatalf("Failure or Success not read as an always-effect: %+v", fs)
	}
	if !strings.Contains(fs.Extra, "can't take this action again") || strings.Contains(fs.Extra, "Frightened") {
		t.Fatalf("what is left for the GM: %q", fs.Extra)
	}
	fs.SourceID = -7
	parts := fs.giveAlways(camp, char, "t")
	if r := marker("frightened"); r == nil || r.Notes != "until_start_of_next_turn" || len(parts) < 2 {
		t.Fatalf("source-timed Frightened not stored: %+v %v", r, parts)
	}
	if marker("speed_zero") == nil {
		t.Fatal("Speed 0 of Failure or Success not stored")
	}
	if (saveEffect{}).giveAlways(camp, char, "t") != nil {
		t.Fatal("an effect without a Failure or Success part gave something")
	}
	// the other sources are the owner's too
	reset()
	addCondition(camp, char, condAdd{Condition: "grappled", Source: "x"})
	if characterSpeedOf(char) != 0 {
		t.Fatal("Grappled did not give Speed 0")
	}
	reset()
	addSpellEffect(camp, char, 0, "Slow", "slowed", "", 10, "", 0)
	if got := characterSpeedOf(char); got != base/2 {
		t.Fatalf("Slow: Speed %d, want %d", got, base/2)
	}
	// a halving that meets a flat reduction is not settled by the rules supplied: the Speed is not computed, movement waits for the GM
	addCombatEffect(camp, char, 0, "hamstring")
	if got := characterSpeedOf(char); got != base {
		t.Fatalf("Slow with Hamstring Blow: Speed %d, want it left at %d until the GM rules", got, base)
	}
	if why := speedRulingNeeded(char); !strings.Contains(why, "not settle") || !strings.Contains(why, "Hamstring Blow") {
		t.Fatalf("the ambiguity was not named: %q", why)
	}
	if why := turnLimitProblem(char, "move"); why == "" {
		t.Fatal("movement was not refused while the effects wait for a ruling")
	}
	if why := turnLimitProblem(char, "attack"); strings.Contains(why, "not settle") {
		t.Fatal("an attack was refused for a Speed ruling")
	}
	// the GM's ruling, through the route, decides the Speed; deleting it returns to the waiting state
	c, j = pgCall(t, "POST", "/api/gm/conditions", gm, map[string]interface{}{"target_id": char, "condition": "speed_ruling", "speed_ft": 10, "duration_rounds": 2})
	if c != 200 {
		t.Fatalf("ruling: %d %v", c, j)
	}
	if got := characterSpeedOf(char); got != 10 || speedRulingNeeded(char) != "" {
		t.Fatalf("GM ruling: Speed %d", got)
	}
	c, j = pgCall(t, "DELETE", "/api/gm/conditions", gm, map[string]interface{}{"target_id": char, "condition": "speed_ruling"})
	if c != 200 || characterSpeedOf(char) != base || speedRulingNeeded(char) == "" {
		t.Fatalf("ruling removed: %d %v", c, j)
	}
	// a Speed of 0 needs no ruling even with other effects: "Speed 0 ... can't increase"
	addCondition(camp, char, condAdd{Condition: "grappled", Source: "x"})
	if characterSpeedOf(char) != 0 || speedRulingNeeded(char) != "" {
		t.Fatal("Grappled with other effects: Speed 0 should need no ruling")
	}
	// movement already spent stays spent when effects come and go: 25 of 30 spent, Slow arrives (halved to 15, 10 over), Slow ends
	reset()
	pgExec(t, `UPDATE characters SET movement_remaining=$1 WHERE id=$2`, base-25, char)
	addSpellEffect(camp, char, 0, "Slow", "slowed", "", 10, "", 0)
	settleMovement(char)
	if got := remaining(); got != base/2-25 {
		t.Fatalf("budget with more spent than the lowered Speed allows: %d, want %d", got, base/2-25)
	}
	db.Exec(`DELETE FROM spell_effects WHERE lobby_id=$1 AND target_id=$2`, camp, char)
	settleMovement(char)
	if got := remaining(); got != base-25 {
		t.Fatalf("effect toggled: budget %d, want %d (spent movement came back or was lost)", got, base-25)
	}
	reset()
	addSpellEffect(camp, char, 0, "Haste", "hasted", "", 10, "", 0)
	if got := characterSpeedOf(char); got != base*2 {
		t.Fatalf("Haste: %d, want %d", got, base*2)
	}
	// baseline: the ordinary base Speed calculation never needs a ruling. Exhaustion lowers the base, then Slow halves it (the order the
	// server used before the owner existed), and Haste with Exhaustion doubles it
	reset()
	pgExec(t, `UPDATE characters SET exhaustion_level=1 WHERE id=$1`, char)
	exBase := characterBaseSpeed(char, "")
	if exBase >= base {
		t.Fatalf("Exhaustion did not lower the base Speed: %d vs %d", exBase, base)
	}
	addSpellEffect(camp, char, 0, "Slow", "slowed", "", 10, "", 0)
	if got := characterSpeedOf(char); got != exBase/2 || speedRulingNeeded(char) != "" || turnLimitProblem(char, "move") != "" {
		t.Fatalf("Slow with Exhaustion: Speed %d (want %d), ruling %q, move refused %q", got, exBase/2, speedRulingNeeded(char), turnLimitProblem(char, "move"))
	}
	db.Exec(`DELETE FROM spell_effects WHERE lobby_id=$1 AND target_id=$2`, camp, char)
	addSpellEffect(camp, char, 0, "Haste", "hasted", "", 10, "", 0)
	if got := characterSpeedOf(char); got != exBase*2 || speedRulingNeeded(char) != "" {
		t.Fatalf("Haste with Exhaustion: Speed %d, want %d", got, exBase*2)
	}
	pgExec(t, `UPDATE characters SET exhaustion_level=0 WHERE id=$1`, char)
	// Dash under an effect, through the route: it adds the Speed after the effect (glossary, Dash), on top of the budget the effect left
	reset()
	f2, _ := readFight(db, camp)
	for i, e := range f2.Order {
		if asInt(e["id"]) == char {
			pgExec(t, `UPDATE combat_state SET current_turn_index=$1 WHERE lobby_id=$2`, i, camp)
		}
	}
	pgExec(t, `UPDATE characters SET action_used=false, bonus_action_used=false WHERE id=$1`, char)
	addCondition(camp, char, condAdd{Condition: "speed_halved", Source: "x", Rounds: 2})
	c, j = pgCall(t, "POST", "/api/action", player, map[string]interface{}{"action": "dash", "description": "I dash"})
	if c != 200 {
		t.Fatalf("dash: %d %v", c, j)
	}
	settleMovement(char)
	if got := remaining(); got != base/2+base/2 {
		t.Fatalf("Dash while the Speed is halved: budget %d, want %d (the halved budget plus the halved Speed)", got, base)
	}
	reset()
}

func TestDisadvantageRiderParsing(t *testing.T) {
	for _, c := range []struct {
		text    string
		modes   string
		extra   bool // something is left for the GM
		extraIn string
	}{
		{"Failure: 22 (4d10) Poison damage, and the target has Disadvantage on saving throws to maintain Concentration until the end of its next turn.", "concentration", false, ""},
		{"Hit: 17 (2d10) Radiant damage, and the target has Disadvantage on attack rolls until the end of its next turn.", "attacks", false, ""},
		{"Failure: The target has Disadvantage on attack rolls and ability checks until the end of its next turn.", "attacks", true, "ability checks"},
		{"Hit: 9 (2d6) Piercing damage. The target has Disadvantage on its next attack roll before the start of the warrior\u2019s next turn.", "", true, "next attack roll"}, // no SRD wording: the GM's
		{"Hit: 17 (2d10) Radiant damage, and the target has Disadvantage on attack rolls until the end of the cyclops\u2019s next turn.", "attacks", false, ""},               // the source's turn: its marker
		{"Failure: The target has Disadvantage on Strength-based D20 Tests and loses 1d6 damage.", "", true, "Strength-based"},
		// Disadvantage on one ability's saving throws: no SRD stat block has it, so it is the GM's
		{"Hit: 8 (2d6 + 1) Acid damage, and the target has Disadvantage on Intelligence saving throws until the end of the raider's next turn.", "", true, "Intelligence saving throws"},
		{"Failure: The target has Disadvantage on Intelligence saving throws.", "", true, "Intelligence saving throws"}, // no duration: the GM's
		// the next attack roll against the target (the Worg's Bite; the effect Guiding Bolt gives)
		{"Hit: 7 (1d8 + 3) Piercing damage, and the next attack roll made against the target before the start of the worg\u2019s next turn has Advantage.", "exposed", false, ""},
		// a rider used up by one roll is kept only until the start of the source's turn: any other end is the GM's
		{"Hit: 7 (1d8 + 3) Piercing damage, and the next attack roll made against the target before the end of its next turn has Advantage.", "", true, "Advantage"},
		{"Hit: 9 (2d6) Piercing damage, and the target has Disadvantage on the next attack roll it makes before the end of its next turn.", "", true, "next attack roll"},
	} {
		var e saveEffect
		extra := strings.Join(e.readRider(c.text), ". ")
		if got := strings.Join(e.DisadvMode, ","); got != c.modes {
			t.Errorf("%q: modes %q, want %q", c.text, got, c.modes)
		}
		if (extra != "") != c.extra || !strings.Contains(extra, c.extraIn) {
			t.Errorf("%q: left for the GM %q (want something: %v, containing %q)", c.text, extra, c.extra, c.extraIn)
		}
	}
}

// An attach the monster is the source of sets its Speed to 0 ("Its Speed becomes 0, it can't benefit from
// any bonus to its Speed, and it moves with the target", Darkmantle Crush): speedFactorsOf carries the
// attach as a Zero factor, the map move check flags the move as Speed 0, and ending the attach frees it.
func TestPostgresAttachSpeedZero(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "attach speed gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Attach speed zero", "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	_, player := pgAgent(t, "attach speed player")
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Attach Hero %d", time.Now().UnixNano()%1e9)))
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
	m.Name = "Attach Lurker"
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variant for the attach Speed test", "monsters": []srdMonster2024{m}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Attach Lurker", "monster_key": key, "initiative": 1}}})
	ids := monsterIDsByKeyOrName(camp, "Attach Lurker")
	if len(ids) != 1 {
		t.Fatalf("monster id: %v", ids)
	}
	id := ids[0]
	base, known := monsterWalkSpeed(id, camp)
	if !known || base <= 0 {
		t.Fatalf("walk speed: %d %v", base, known)
	}
	if eff, why, _ := effectiveSpeed(camp, id, base); eff != base {
		t.Fatalf("before attach: speed %d %v", eff, why)
	}
	if _, err := db.Exec(`INSERT INTO combat_effects (lobby_id, target_id, source_id, kind, text) VALUES ($1,$2,$3,'attached','the lurker attaches to the target')`, camp, char, id); err != nil {
		t.Fatalf("attach row: %v", err)
	}
	eff, why, ruling := effectiveSpeed(camp, id, base)
	if eff != 0 || ruling {
		t.Fatalf("attached: speed %d ruling %v %v", eff, ruling, why)
	}
	found := false
	for _, w := range why {
		if strings.Contains(w, "attached to") {
			found = true
		}
	}
	if !found {
		t.Fatalf("attach not named in %v", why)
	}
	put := func(x int) {
		t.Helper()
		_, st := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"............"}, Tokens: []mapToken{{ID: id, Name: "Attach Lurker", Position: mapSquare{x, 0}, Size: 1}}}}
		if rev, ok := st["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	notes := func() string {
		var out []string
		rows, err := db.Query(`SELECT result FROM actions WHERE lobby_id=$1 AND action_type='monster_move' ORDER BY id`, camp)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			rows.Scan(&s)
			out = append(out, s)
		}
		return strings.Join(out, " | ")
	}
	put(0)
	put(4)
	if n := notes(); !strings.Contains(n, "Speed is 0") || !strings.Contains(n, "attached to") {
		t.Fatalf("move while attached not flagged: %q", n)
	}
	// The target moves and the attached monster stays: the update is refused - the attach carries the
	// monster with the target. Explicit overrides: move the pair together, detach first, or forced_movement.
	putPair := func(mx, cx int, forced bool) (int, map[string]interface{}) {
		t.Helper()
		_, st := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"............"}, Tokens: []mapToken{
			{ID: id, Name: "Attach Lurker", Position: mapSquare{mx, 0}, Size: 1},
			{ID: char, Name: "Attach Hero", Position: mapSquare{cx, 0}, Size: 1},
		}}, "forced_movement": forced}
		if rev, ok := st["revision"].(string); ok {
			req["confirmation"] = rev
		}
		return pgCall(t, "PUT", cp+"/battle-map", gm, req)
	}
	if c, r := putPair(4, 5, false); c != 200 {
		t.Fatalf("first pair publish: %d %v", c, r)
	}
	rc, rr := putPair(4, 7, false)
	if rc != 409 || fmt.Sprint(rr["error"]) != "attach_carry_required" {
		t.Fatalf("target moved without the attached monster was not refused: %d %v", rc, rr)
	}
	if !strings.Contains(fmt.Sprint(rr["message"]), "forced_movement") {
		t.Fatalf("the refusal does not name the overrides: %v", rr)
	}
	// forced_movement is the explicit override (a push, a teleport, a ruling); the note path records it.
	if c, r := putPair(4, 7, true); c != 200 {
		t.Fatalf("forced override refused: %d %v", c, r)
	}
	if n := notes(); !strings.Contains(n, "did not move with it") {
		t.Fatalf("a forced separation was not noted: %q", n)
	}
	// A pair moved together is fine: the monster is carried, so no note at all (its Speed-0 is its own
	// movement's limit, and being carried is not its own movement).
	after := notes()
	if c, r := putPair(5, 8, false); c != 200 {
		t.Fatalf("pair moved together refused: %d %v", c, r)
	}
	if n := notes(); n != after {
		t.Fatalf("a pair moved together was flagged: %q (before %q)", n, after)
	}
	// The player move route carries the attached monster's token with the character's move.
	var orderRaw []byte
	if err := db.QueryRow(`SELECT turn_order FROM combat_state WHERE lobby_id=$1`, camp).Scan(&orderRaw); err != nil {
		t.Fatal(err)
	}
	var order []struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(orderRaw, &order); err != nil {
		t.Fatal(err)
	}
	turnIdx := -1
	for i, e := range order {
		if e.ID == char {
			turnIdx = i
		}
	}
	if turnIdx < 0 {
		t.Fatalf("character not in the turn order: %v", order)
	}
	pgExec(t, `UPDATE combat_state SET current_turn_index=$1 WHERE lobby_id=$2`, turnIdx, camp)
	path := map[string]interface{}{"character_id": char, "path": []mapSquare{{9, 0}}}
	_, pv := pgCall(t, "POST", cp+"/battle-map/preview", player, path)
	conf, _ := pv["confirmation"].(string)
	if conf == "" {
		t.Fatalf("preview: %v", pv)
	}
	path["confirmation"] = conf
	c, mv := pgCall(t, "POST", cp+"/battle-map/move", player, path)
	if c != 200 {
		t.Fatalf("player move: %d %v", c, mv)
	}
	if !strings.Contains(fmt.Sprint(mv["carried"]), "moves with") {
		t.Fatalf("the attached monster was not carried: %v", mv)
	}
	_, st := pgCall(t, "GET", cp+"/battle-map", gm, nil)
	carriedTokenFound := false
	for _, tok := range st["tokens"].([]interface{}) {
		tm := tok.(map[string]interface{})
		if int(tm["id"].(float64)) == id {
			pos := tm["position"].(map[string]interface{})
			if int(pos["x"].(float64)) != 6 {
				t.Fatalf("carried token at %v, want x=6", pos)
			}
			carriedTokenFound = true
		}
	}
	if !carriedTokenFound {
		t.Fatalf("monster token missing after the carry: %v", st)
	}
	// The voluntary detach is its own costed action: the attacher's turn, 5 ft of movement; not its turn
	// is refused and changes nothing.
	c, r := pgCall(t, "POST", "/api/gm/detach/voluntary", gm, map[string]interface{}{"attacher_id": id, "target_id": char})
	if c != 409 || fmt.Sprint(r["error"]) != "not_its_turn" {
		t.Fatalf("voluntary detach off its turn: %d %v", c, r)
	}
	monsterTurn := -1
	for i, e := range order {
		if e.ID == id {
			monsterTurn = i
		}
	}
	if monsterTurn < 0 {
		t.Fatalf("monster not in the turn order: %v", order)
	}
	pgExec(t, `UPDATE combat_state SET current_turn_index=$1 WHERE lobby_id=$2`, monsterTurn, camp)
	c, r = pgCall(t, "POST", "/api/gm/detach/voluntary", gm, map[string]interface{}{"attacher_id": id, "target_id": char})
	if c != 200 || !strings.Contains(fmt.Sprint(r["result"]), "5 feet of movement") || !strings.Contains(fmt.Sprint(r["result"]), "(5 ft this turn)") {
		t.Fatalf("voluntary detach: %d %v", c, r)
	}
	var attachGone bool
	if err := db.QueryRow(`SELECT NOT EXISTS(SELECT 1 FROM combat_effects WHERE lobby_id=$1 AND kind='attached' AND source_id=$2)`, camp, id).Scan(&attachGone); err != nil || !attachGone {
		t.Fatalf("attach row after the voluntary detach: %v %v", attachGone, err)
	}
	if c, r = pgCall(t, "POST", "/api/gm/detach/voluntary", gm, map[string]interface{}{"attacher_id": id, "target_id": char}); c != 404 {
		t.Fatalf("a second voluntary detach: %d %v", c, r)
	}
	if eff, _, _ := effectiveSpeed(camp, id, base); eff != base {
		t.Fatalf("after detach: speed %d, want %d", eff, base)
	}
}
