package main

import (
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"strings"
	"testing"
	"time"
)

// A monster's attack on another monster of the fight runs through the shared owners (attackCtx, hurtMonster, giveConditions,
// applySaveEffect) and leaves a character alone. Test monsters are SRD Goblin Warrior variants with invented attacks (+60 to hit).
func TestPostgresMonsterAttacksMonster(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "mvm gm")
	_, player := pgAgent(t, "mvm player")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Monster vs monster", "max_players": 2})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("MvM Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var goblin, golem srdMonster2024
	for _, m := range srdMonsters2024() {
		switch m.Name {
		case "Goblin Warrior":
			goblin = m
		case "Iron Golem":
			golem = m
		}
	}
	atk := func(name, tail string) srdMonsterEntry {
		return srdMonsterEntry{Name: name, Text: "Melee Attack Roll: +60, reach 10 ft. Hit: 10 (2d6 + 3) Bludgeoning damage" + tail}
	}
	brute := goblin
	brute.Name = "Mvm Brute"
	brute.Actions = []srdMonsterEntry{
		atk("Slam", ". If the target is a Large or smaller creature, it has the Prone condition."),
		atk("Zap", ", and the target has the Stunned condition until the start of the brute's next turn."),
		{Name: "Sting", Text: "Melee Attack Roll: +60, reach 10 ft. Hit: 5 (1d6 + 2) Piercing damage. Constitution Saving Throw: DC 40. Failure: The target has the Poisoned condition until the start of the brute's next turn."},
		{Name: "Gnaw", Text: "Melee Attack Roll: +60, reach 10 ft. Hit: 7 (1d6 + 4) Slashing damage, or 14 (2d6 + 7) Slashing damage if the target is Bloodied."},
		{Name: "Spark", Text: "Melee Attack Roll: +20, reach 10 ft. Hit: 9 (2d6 + 2) Lightning damage."},
		{Name: "Burst", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 5 (1d6 + 2) Bludgeoning damage. Hit or Miss: Earth explodes. Dexterity Saving Throw: DC 16, each creature in a 10-foot Emanation. Failure: 10 (3d6) Thunder damage."},
		{Name: "Jab", Text: "Melee Attack Roll: +5, reach 5 ft. Hit: 5 (1d6 + 2) Bludgeoning damage. If the target is a Large or smaller creature, it has the Prone condition."},
		{Name: "Hurl", Text: "Ranged Attack Roll: +60, range 60 ft. Hit: 20 Piercing damage."},
		{Name: "Pounce (1/Day)", Text: "Melee Attack Roll: +60, reach 10 ft. Hit: 5 (1d6 + 2) Piercing damage."},
		{Name: "Lunge (1/Day)", Text: "Melee Attack Roll: +60, reach 10 ft. Hit: 5 (1d6 + 2) Piercing damage."},
	}
	victim := goblin
	victim.Name = "Mvm Victim"
	victim.AC = 5
	victim.Resistances = "Bludgeoning"
	victim.Reactions = []srdMonsterEntry{{Name: "Redirect Attack", Text: "Trigger: A creature the victim can see targets it with an attack. Response: The victim chooses a Small or Medium ally within 5 feet of it. The victim and that ally swap places, and the ally becomes the target instead."}, {Name: "Deflect Missile", Text: "Trigger: The victim is hit by a ranged attack roll. Response: The victim reduces the damage it takes from the attack by 10 (1d10 + 5). If it reduces the damage to 0, it can redirect the force."}, {Name: "Parry", Text: "Trigger: The victim is hit by a melee attack roll while holding a weapon. Response: The victim adds 99 to its AC against that attack, possibly causing it to miss."}}
	absorber := golem
	absorber.Name = "Mvm Absorber"
	absorber.AC = 10
	absorber.Reactions = append([]srdMonsterEntry{{Name: "Warding", Text: "Trigger: The mage is hit by an attack roll. Response: The mage casts Counterspell or Shield, using the same slot."}}, victim.Reactions...)
	absorber.DmgImmune = "Lightning"
	absorber.Traits = []srdMonsterEntry{{Name: "Lightning Absorption", Text: "Whenever the golem is subjected to Lightning damage, it regains a number of Hit Points equal to the Lightning damage dealt."}}
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variants for monster-vs-monster tests", "monsters": []srdMonster2024{brute, victim, absorber}})
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
		{"name": "Mvm Brute", "monster_key": keys["Mvm Brute"], "initiative": 20}, {"name": "Mvm Victim", "monster_key": keys["Mvm Victim"], "initiative": 10}, {"name": "Mvm Absorber", "monster_key": keys["Mvm Absorber"], "initiative": 5}}})
	ids := map[string]int{}
	setHP := func(id, hp int) {
		tx, _ := db.Begin()
		locked, err := lockFight(tx, camp)
		if err != nil {
			t.Fatal(err)
		}
		_, e := locked.entry(id)
		e["hp"], e["max_hp"] = hp, 500
		if err := writeFight(tx, locked); err != nil || tx.Commit() != nil {
			t.Fatalf("setting hp: %v", err)
		}
	}
	f, _ := readFight(db, camp)
	for _, e := range f.Order {
		ids[sprintAny(e["name"])] = asInt(e["id"])
	}
	vid := ids["Mvm Victim"]
	if vid >= 0 || ids["Mvm Brute"] >= 0 {
		t.Fatalf("monsters not in the fight: %v", ids)
	}
	hpOf := func() int {
		f, _ := readFight(db, camp)
		_, e := f.entry(vid)
		return asInt(e["hp"])
	}
	play := func(action string, apply bool) (string, int) {
		t.Helper()
		for try := 0; try < 30; try++ {
			setHP(vid, 500)
			removeConditionsWhere(camp, vid, func(conditionRecord) bool { return true })
			ma := map[string]interface{}{"monster": "Mvm Brute", "action": action, "target": "Mvm Victim"}
			if !apply {
				ma["apply_damage"] = false
			}
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": ma})
			res := fmt.Sprint(r["monster_action_result"])
			if strings.Contains(res, "damage") && !strings.Contains(res, "misses") {
				return res, hpOf()
			}
		}
		t.Fatalf("%s: no hit in 30 tries", action)
		return "", 0
	}
	hasCond := func(cond string) *conditionRecord {
		recs, _ := readConditionRecords(db, camp, vid)
		for i := range recs {
			if recs[i].Condition == cond {
				return &recs[i]
			}
		}
		return nil
	}
	// damage goes through the target's resistance (Bludgeoning halved: 2d6+3 is 5 to 15, so 2 to 7 are lost; a critical hit doubles
	// the dice: 4d6+3 is 7 to 27, so 3 to 13 are lost - the cause of the one intermittent failure, a natural 20 on this roll)
	res, hp := play("Slam", true)
	maxLost := 7
	if strings.Contains(res, "CRITICAL HIT") {
		maxLost = 13
	}
	if lost := 500 - hp; lost <= 0 || lost > maxLost || !strings.Contains(res, "(it resists)") || !strings.Contains(res, "Mvm Victim takes") {
		t.Fatalf("Slam: damage not dealt through the target's resistance (lost %d): %s", lost, res)
	}
	// the hit's condition is stored on the monster target, with its source
	if r := hasCond("prone"); r == nil || !strings.Contains(r.Source, "Mvm Brute") {
		t.Fatalf("Slam: Prone not stored on the monster target: %s", res)
	}
	if recs, _ := readConditionRecords(db, camp, char); len(recs) != 0 {
		t.Fatalf("a monster-vs-monster attack left a condition on a character: %+v", recs)
	}
	// source-timed: Stunned until the start of the brute's next turn
	play("Zap", true)
	if r := hasCond("stunned"); r == nil || r.Notes != "until_start_of_next_turn" {
		t.Fatalf("Zap: source-timed Stunned not stored on the monster target: %+v", r)
	}
	// a hit's own saving throw (DC 40: it fails unless a natural 20 is rolled; legit retries below)
	for try := 0; try < 30 && hasCond("poisoned") == nil; try++ {
		play("Sting", true)
	}
	if hasCond("poisoned") == nil {
		t.Fatal("Sting: the hit's saving throw never left Poisoned on the monster target in 30 tries")
	}
	// a preview deals and stores nothing
	res, hp = play("Slam", false)
	if hp != 500 || hasCond("prone") != nil || !strings.Contains(res, "not applied") {
		t.Fatalf("preview changed the target (hp %d): %s", hp, res)
	}
	// a damage choice that depends on the target being Bloodied reads the monster target's Hit Points
	setHP(vid, 500)
	for try := 0; try < 30; try++ { // a critical hit doubles the dice and overlaps the Bloodied range: only a normal hit is judged here
		if res, hp = play("Gnaw", true); !strings.Contains(res, "CRITICAL HIT") {
			break
		}
	}
	if lost := 500 - hp; lost < 5 || lost > 10 { // 1d6+4 = 5 to 10 at full Hit Points; the Bloodied 2d6+7 is 9 to 19
		t.Fatalf("Gnaw at full HP: lost %d, want 5..10: %s", lost, res)
	}
	for try := 0; ; try++ {
		if try == 30 {
			t.Fatal("Gnaw on a Bloodied target never hit")
		}
		setHP(vid, 100) // 100 of 500: Bloodied
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Mvm Brute", "action": "Gnaw", "target": "Mvm Victim"}})
		if lost := 100 - hpOf(); lost > 0 {
			if lost < 9 {
				t.Fatalf("Gnaw on a Bloodied target dealt the unconditional damage (lost %d): %v", lost, r["monster_action_result"])
			}
			break
		}
	}
	// the attacker cannot target itself as a monster target
	_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
		"monster_action": map[string]interface{}{"monster": "Mvm Brute", "action": "Slam", "target": "Mvm Brute"}})
	if res := fmt.Sprint(r["monster_action_result"]); strings.Contains(res, "Mvm Brute attacks Mvm Brute") {
		t.Fatalf("a monster attacked itself: %s", res)
	}
	// a (1/Day) attack aimed at a monster target is counted once it was made (hit or miss), and the second try is refused before rolling
	lunge := func() string {
		setHP(vid, 500)
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Mvm Brute", "action": "Lunge (1/Day)", "target": "Mvm Victim"}})
		return fmt.Sprint(r["monster_action_result"])
	}
	if res := lunge(); strings.Contains(res, "Nothing was rolled") || !strings.Contains(res, "Mvm Victim") {
		t.Fatalf("Lunge: the first use against a monster target was refused or not made: %s", res)
	}
	if res := lunge(); !strings.Contains(res, "allows") || !strings.Contains(res, "Nothing was rolled") || hpOf() != 500 {
		t.Fatalf("Lunge: the second use of a (1/Day) attack on a monster target was not refused (hp %d): %s", hpOf(), res)
	}
	// the target's Reaction against a monster attacker's hit (monster_reactions_2024.go): the note names it, Parry turns the hit into a miss
	// and gives the damage back, a second Reaction the same round is refused
	reacted := false
	for try := 0; try < 40 && !reacted; try++ {
		clearMonsterReactionState(camp)
		monsterReactionRound.Delete(fmt.Sprintf("%d:%d", camp, vid)) // a new round for the test's purposes
		setHP(vid, 500)
		res, hp := play("Slam", true)
		if !strings.Contains(res, "Parry") || !strings.Contains(res, "Reactions are not played automatically") {
			t.Fatalf("the reply does not name the target's Reaction: %s", res)
		}
		if strings.Contains(res, "CRITICAL") && hp == 500 {
			continue
		}
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Mvm Victim", "action": "Parry"}})
		pr := fmt.Sprint(r["monster_action_result"])
		if strings.Contains(pr, "natural 20") { // a natural 20 still hits: nothing is undone
			if hpOf() != hp {
				t.Fatalf("a natural 20 changed the damage: %s", pr)
			}
			continue
		}
		if !strings.Contains(pr, "now a miss") || hpOf() != 500 {
			t.Fatalf("Parry against a monster attacker's hit did not undo it (hp %d): %s", hpOf(), pr)
		}
		_, r = pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Mvm Victim", "action": "Parry"}})
		if second := fmt.Sprint(r["monster_action_result"]); !strings.Contains(second, "already used its Reaction") {
			t.Fatalf("a second Reaction in the same round was not refused: %s", second)
		}
		reacted = true
	}
	if !reacted {
		t.Fatal("no hit that Parry could answer in 40 tries")
	}

	// Taking a hit back goes beyond Hit Points (monster_hit_undo_2024.go). Each case: the attack hits, the target's Parry (+99 AC) makes it
	// a miss, and everything the hit changed is gone while the attack's own use stays counted.
	parryAfter := func(target, action string, prepare func(), check func(res, parryRes string)) {
		t.Helper()
		for try := 0; try < 60; try++ {
			monsterReactionRound.Delete(fmt.Sprintf("%d:%d", camp, ids[target]))
			clearMonsterReactionState(camp)
			removeConditionsWhere(camp, ids[target], func(conditionRecord) bool { return true })
			setHP(ids[target], 500)
			prepare()
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": "Mvm Brute", "action": action, "target": target}})
			res := fmt.Sprint(r["monster_action_result"])
			if !strings.Contains(res, "damage") || strings.Contains(res, "misses") || strings.Contains(res, "MISS") {
				t.Logf("try %d skipped: %s", try, res)
				continue
			}
			_, r = pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": target, "action": "Parry"}})
			pr := fmt.Sprint(r["monster_action_result"])
			if strings.Contains(pr, "natural 20") {
				continue
			}
			if !strings.Contains(pr, "now a miss") {
				t.Fatalf("%s %s: Parry did not take the hit back: %s", target, action, pr)
			}
			check(res, pr)
			return
		}
		t.Fatalf("%s %s: no hit that Parry could answer in 60 tries", target, action)
	}
	vit := func(name string) game.Vitals { return monsterEntryVitals(camp, ids[name]) }
	condsOf := func(name string) string {
		var out []string
		for _, r := range loadConditionRecords(camp, ids[name]) {
			out = append(out, r.Condition)
		}
		return strings.Join(out, ",")
	}
	// the hit's own condition (Prone) is removed; Hit Points and Temporary Hit Points are what they were
	parryAfter("Mvm Victim", "Slam", func() { setMonsterTempHP(camp, ids["Mvm Victim"], 5) }, func(res, pr string) {
		if v := vit("Mvm Victim"); v.HP != 500 || v.Temp != 5 || condsOf("Mvm Victim") != "" || !strings.Contains(res, "Prone") {
			t.Fatalf("Slam taken back: %+v conditions %q (Prone must be given by the hit and gone after): %s %s", v, condsOf("Mvm Victim"), res, pr)
		}
	})
	// the hit's own saving throw (Sting's Poisoned) is removed too
	parryAfter("Mvm Victim", "Sting", func() {}, func(res, pr string) {
		if strings.Contains(res, "Poisoned") && condsOf("Mvm Victim") != "" {
			t.Fatalf("Sting taken back left %q: %s %s", condsOf("Mvm Victim"), res, pr)
		}
	})
	// a lethal hit: a creature at 0 Hit Points cannot take a Reaction itself (it is refused above), so the undo is exercised directly
	// on the recorded hit: 1 Hit Point left, the hit defeats it; taken back it is alive and not Dead
	for try := 0; try < 60; try++ {
		clearMonsterReactionState(camp)
		removeConditionsWhere(camp, ids["Mvm Victim"], func(conditionRecord) bool { return true })
		setMonsterTempHP(camp, ids["Mvm Victim"], 0)
		setHP(ids["Mvm Victim"], 1)
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Mvm Brute", "action": "Slam", "target": "Mvm Victim"}})
		res := fmt.Sprint(r["monster_action_result"])
		if !strings.Contains(res, "HIT") {
			continue
		}
		h, ok := thisTurnHitOnMonster(camp, ids["Mvm Victim"])
		if !ok || vit("Mvm Victim").HP != 0 {
			t.Fatalf("the lethal Slam did not bring it to 0: %+v %s", vit("Mvm Victim"), res)
		}
		_, _, _ = undoRecordedHit(camp, ids["Mvm Victim"], h)
		if v := vit("Mvm Victim"); v.HP != 1 || v.Dead || condsOf("Mvm Victim") != "" {
			t.Fatalf("lethal Slam taken back: %+v %q", v, condsOf("Mvm Victim"))
		}
		break
	}
	// damage that wakes a sleeper: the Sleep the damage ended is put back
	parryAfter("Mvm Victim", "Slam", func() {
		addCondition(camp, ids["Mvm Victim"], condAdd{Condition: "unconscious", Source: "Sleep", Rounds: 10})
	}, func(res, pr string) {
		if !strings.Contains(condsOf("Mvm Victim"), "unconscious") {
			t.Fatalf("Sleep ended by the hit was not put back: %q: %s %s", condsOf("Mvm Victim"), res, pr)
		}
	})
	// absorption: the hit gave Hit Points; taken back they are gone again
	parryAfter("Mvm Absorber", "Spark", func() { setHP(ids["Mvm Absorber"], 100) }, func(res, pr string) {
		if !strings.Contains(res, "absorbs") {
			t.Fatalf("the golem did not absorb Lightning: %s", res)
		}
		if v := vit("Mvm Absorber"); v.HP != 100 {
			t.Fatalf("absorbed Hit Points were not taken back: %+v: %s %s", v, res, pr)
		}
	})
	// a limited-use attack that was answered stays used
	monsterReactionRound.Delete(fmt.Sprintf("%d:%d", camp, vid))
	// each try starts with the day's use not yet spent: a try retried after a natural-20 Parry roll would otherwise be refused by the 1/Day count
	parryAfter("Mvm Victim", "Pounce (1/Day)", func() { pgExec(t, `DELETE FROM monster_day_uses WHERE campaign_id = $1`, camp) }, func(res, pr string) {})
	_, pr2 := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
		"monster_action": map[string]interface{}{"monster": "Mvm Brute", "action": "Pounce (1/Day)", "target": "Mvm Victim"}})
	if res := fmt.Sprint(pr2["monster_action_result"]); !strings.Contains(res, "allows") || !strings.Contains(res, "Nothing was rolled") {
		t.Fatalf("a (1/Day) attack that Parry answered was not counted: %s", res)
	}

	// Stale and interleaved state: the correction is tied to this hit's own changes and never guesses.
	hitOnly := func(action string, prepare func()) string {
		t.Helper()
		for try := 0; try < 60; try++ {
			monsterReactionRound.Delete(fmt.Sprintf("%d:%d", camp, ids["Mvm Victim"]))
			clearMonsterReactionState(camp)
			removeConditionsWhere(camp, ids["Mvm Victim"], func(conditionRecord) bool { return true })
			setMonsterTempHP(camp, ids["Mvm Victim"], 0)
			setHP(ids["Mvm Victim"], 500)
			prepare()
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": "Mvm Brute", "action": action, "target": "Mvm Victim"}})
			res := fmt.Sprint(r["monster_action_result"])
			if h, ok := thisTurnHitOnMonster(camp, ids["Mvm Victim"]); strings.Contains(res, "HIT") && ok && !h.Nat20 { // a natural 20 is still a hit under Parry
				return res
			}
		}
		t.Fatalf("no plain hit in 60 tries: %s", action)
		return ""
	}
	parryNow := func() string {
		t.Helper()
		monsterReactionRound.Delete(fmt.Sprintf("%d:%d", camp, ids["Mvm Victim"]))
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Mvm Victim", "action": "Parry"}})
		return fmt.Sprint(r["monster_action_result"])
	}
	// Hit Points changed after the hit (a heal): nothing is subtracted or added again, the GM is told, and the hit's Prone stays
	hitOnly("Slam", func() {})
	setHP(ids["Mvm Victim"], vit("Mvm Victim").HP+3)
	hpHealed := vit("Mvm Victim").HP
	if pr := parryNow(); !strings.Contains(pr, "RECONCILIATION NEEDED") || vit("Mvm Victim").HP != hpHealed || !strings.Contains(condsOf("Mvm Victim"), "prone") {
		t.Fatalf("a stale hit was corrected (hp %d, was %d, %q): %s", vit("Mvm Victim").HP, hpHealed, condsOf("Mvm Victim"), pr)
	}
	// the same hit cannot be answered twice: no second refund, no second removal
	if pr := parryNow(); !strings.Contains(pr, "no attack has hit") || vit("Mvm Victim").HP != hpHealed {
		t.Fatalf("a hit was answered twice (hp %d): %s", vit("Mvm Victim").HP, pr)
	}
	// duplicate Reaction after a clean correction: the first restores, the second finds no hit and changes nothing
	hitOnly("Slam", func() {})
	if pr := parryNow(); !strings.Contains(pr, "now a miss") || vit("Mvm Victim").HP != 500 {
		t.Fatalf("clean correction: %s", pr)
	}
	if pr := parryNow(); vit("Mvm Victim").HP != 500 || strings.Contains(pr, "now a miss") {
		t.Fatalf("a second Reaction changed the target again (hp %d): %s", vit("Mvm Victim").HP, pr)
	}
	// a condition that ended after the hit: the Sleep the damage ended is not put back over changed conditions
	hitOnly("Slam", func() {
		addCondition(camp, ids["Mvm Victim"], condAdd{Condition: "unconscious", Source: "Sleep", Rounds: 10})
		addCondition(camp, ids["Mvm Victim"], condAdd{Condition: "frightened", Source: "Scare", Rounds: 10})
	})
	removeConditionsWhere(camp, ids["Mvm Victim"], func(r conditionRecord) bool { return r.Condition == "frightened" })
	if pr := parryNow(); !strings.Contains(pr, "RECONCILIATION NEEDED") || !strings.Contains(pr, "unconscious (Sleep)") || strings.Contains(condsOf("Mvm Victim"), "unconscious") {
		t.Fatalf("Sleep was put back over changed conditions (%q): %s", condsOf("Mvm Victim"), pr)
	}
	// Hit or Miss: the effect stays the GM's whether the hit lands or is taken back, and the Parry reply says so
	res = hitOnly("Burst", func() {})
	if !strings.Contains(res, "Hit or Miss effect") {
		t.Fatalf("the Hit or Miss effect was dropped: %s", res)
	}
	if pr := parryNow(); !strings.Contains(pr, "now a miss") || !strings.Contains(pr, "Hit or Miss") || vit("Mvm Victim").HP != 500 {
		t.Fatalf("Hit or Miss after a taken-back hit: %s", pr)
	}

	// Shield (a Reaction that adds 5 to AC) against a monster attacker: a hit that the +5 turns into a miss is taken back like Parry's;
	// a hit that beats AC + 5 stays a hit, with nothing taken back.
	shieldTry := func(wantMiss bool) {
		t.Helper()
		ab := ids["Mvm Absorber"]
		for try := 0; try < 200; try++ {
			monsterReactionRound.Delete(fmt.Sprintf("%d:%d", camp, ab))
			clearMonsterReactionState(camp)
			removeConditionsWhere(camp, ab, func(conditionRecord) bool { return true })
			setMonsterTempHP(camp, ab, 0)
			setHP(ab, 100)
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": "Mvm Brute", "action": "Jab", "target": "Mvm Absorber"}})
			h, ok := thisTurnHitOnMonster(camp, ab)
			if !ok || h.Nat20 || (wantMiss && h.Total >= h.AC+5) || (!wantMiss && h.Total < h.AC+5) {
				continue
			}
			hpHit := vit("Mvm Absorber").HP
			_, r = pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": "Mvm Absorber", "action": "Warding"}})
			sr := fmt.Sprint(r["monster_action_result"])
			if wantMiss {
				if !strings.Contains(sr, "now a miss") || vit("Mvm Absorber").HP != 100 || condsOf("Mvm Absorber") != "" {
					t.Fatalf("Shield did not take the hit back (hp %d, %q): %s", vit("Mvm Absorber").HP, condsOf("Mvm Absorber"), sr)
				}
			} else if !strings.Contains(sr, "still hits") || vit("Mvm Absorber").HP != hpHit || !strings.Contains(condsOf("Mvm Absorber"), "prone") {
				t.Fatalf("Shield changed a hit that still lands (hp %d, was %d, %q): %s", vit("Mvm Absorber").HP, hpHit, condsOf("Mvm Absorber"), sr)
			}
			return
		}
		t.Fatalf("no Jab of the wanted kind in 200 tries (miss=%v)", wantMiss)
	}
	shieldTry(true)
	shieldTry(false)

	// Deflect Missile: the part of the damage it takes back counts Temporary Hit Points (they took the hit first), never exceeds what the
	// hit took, and leaves the recorded hit in the new state so Parry still restores the pre-hit entry
	deflect := func() string {
		monsterReactionRound.Delete(fmt.Sprintf("%d:%d", camp, ids["Mvm Victim"]))
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Mvm Victim", "action": "Deflect Missile"}})
		return fmt.Sprint(r["monster_action_result"])
	}
	hurl := func(temp int) {
		t.Helper()
		for try := 0; try < 60; try++ {
			clearMonsterReactionState(camp)
			monsterReactionRound.Delete(fmt.Sprintf("%d:%d", camp, ids["Mvm Victim"]))
			removeConditionsWhere(camp, ids["Mvm Victim"], func(conditionRecord) bool { return true })
			setMonsterTempHP(camp, ids["Mvm Victim"], temp)
			setHP(ids["Mvm Victim"], 500)
			pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": "Mvm Brute", "action": "Hurl", "target": "Mvm Victim"}})
			if h, ok := thisTurnHitOnMonster(camp, ids["Mvm Victim"]); ok && !h.Nat20 {
				return
			}
		}
		t.Fatal("no Hurl hit in 60 tries")
	}
	hurl(100) // all 20 points go to Temporary Hit Points
	if v := vit("Mvm Victim"); v.HP != 500 || v.Temp != 80 {
		t.Fatalf("Hurl against Temporary Hit Points: %+v", v)
	}
	if dr := deflect(); vit("Mvm Victim").HP != 500 || vit("Mvm Victim").Temp < 86 || vit("Mvm Victim").Temp > 95 || !strings.Contains(dr, "taken back") {
		t.Fatalf("Deflect Missile did not give back Temporary Hit Points (%+v): %s", vit("Mvm Victim"), dr)
	}
	hurl(5) // 5 points of Temporary Hit Points, then 15 Hit Points
	if v := vit("Mvm Victim"); v.HP != 485 || v.Temp != 0 {
		t.Fatalf("Hurl over Temporary Hit Points: %+v", v)
	}
	dr := deflect()
	if v := vit("Mvm Victim"); v.HP < 491 || v.HP > 500 || v.Temp != 0 && v.HP != 500 {
		t.Fatalf("Deflect Missile refunds Hit Points first (%+v): %s", v, dr)
	}
	// a Reaction answering the same hit twice does not refund beyond the damage
	if v := vit("Mvm Victim"); v.HP > 500 {
		t.Fatalf("over the maximum: %+v", v)
	}

	// Redirect Attack (the ally becomes the target of the same attack roll): the first target keeps nothing of the hit; the ally takes a
	// fresh result against ITS defenses (the first target resists Bludgeoning, the ally does not), its own AC decides hit or miss,
	// and the hit's rider (Prone) goes to the ally by the rules of a hit.
	redirect := func(allyAC int) (hitParts int, res string) {
		t.Helper()
		ally := ids["Mvm Absorber"]
		for try := 0; try < 60; try++ {
			clearMonsterReactionState(camp)
			monsterReactionRound.Delete(fmt.Sprintf("%d:%d", camp, ids["Mvm Victim"]))
			removeConditionsWhere(camp, ids["Mvm Victim"], func(conditionRecord) bool { return true })
			removeConditionsWhere(camp, ally, func(conditionRecord) bool { return true })
			setMonsterTempHP(camp, ids["Mvm Victim"], 100)
			setHP(ids["Mvm Victim"], 500)
			setMonsterTempHP(camp, ally, 0)
			setHP(ally, 300)
			changeFight(camp, func(f *fight) error { _, e := f.entry(ally); e["ac"] = allyAC; return nil })
			pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": "Mvm Brute", "action": "Slam", "target": "Mvm Victim"}})
			h, ok := thisTurnHitOnMonster(camp, ids["Mvm Victim"])
			if !ok || h.Crit || len(h.Parts) == 0 {
				continue
			}
			for _, p := range h.Parts {
				hitParts += p.Amount
			}
			_, rr := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": "Mvm Victim", "action": "Redirect Attack", "target_ids": []int{ally}}})
			return hitParts, fmt.Sprint(rr["monster_action_result"])
		}
		t.Fatal("no Slam hit in 60 tries")
		return
	}
	raw, red := redirect(10)
	if v := vit("Mvm Victim"); v.HP != 500 || v.Temp != 100 || condsOf("Mvm Victim") != "" {
		t.Fatalf("Redirect Attack left the hit on its first target (%+v, %q): %s", v, condsOf("Mvm Victim"), red)
	}
	if v := vit("Mvm Absorber"); 300-v.HP != raw || !strings.Contains(condsOf("Mvm Absorber"), "prone") {
		t.Fatalf("the ally did not take the full damage rolled (%d, the first target resists it) with the rider (%+v, %q): %s", raw, v, condsOf("Mvm Absorber"), red)
	}
	_, red = redirect(500) // the same attack roll against an AC it cannot beat: a miss for the ally, still nothing on the first target
	if v := vit("Mvm Absorber"); v.HP != 300 || condsOf("Mvm Absorber") != "" || !strings.Contains(red, "misses it") || vit("Mvm Victim").Temp != 100 {
		t.Fatalf("a redirected attack that misses the ally changed something (%+v, %q): %s", v, condsOf("Mvm Absorber"), red)
	}
}

// The continuation route enforces the attach onset (and respects the cover's gates) when a monster's
// attach attack hits another monster: before 6796b13 the onset was silently dropped on this route (the
// residual already assumed enforcement). Without Advantage the Darkmantle's cover - and so its
// suffocating - stays off; the onset itself is gated by nothing.
func TestPostgresMonsterAttachesMonster(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "mvm attach gm")
	_, player := pgAgent(t, "mvm attach player")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Monster attaches monster", "max_players": 2})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("MvA Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var dark, goblin srdMonster2024
	for _, m := range srdMonsters2024() {
		switch m.Name {
		case "Darkmantle":
			dark = m
		case "Goblin Warrior":
			goblin = m
		}
	}
	dark.Name, goblin.Name = "MvA Darkmantle", "MvA Goblin"
	other := goblin
	other.Name = "MvA Other"
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Darkmantle and Goblin Warrior variants for the attach route test", "monsters": []srdMonster2024{dark, goblin, other}})
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
		{"name": "MvA Darkmantle", "monster_key": keys["MvA Darkmantle"], "initiative": 20}, {"name": "MvA Goblin", "monster_key": keys["MvA Goblin"], "initiative": 10}, {"name": "MvA Other", "monster_key": keys["MvA Other"], "initiative": 5}}})
	ids := map[string]int{}
	f, _ := readFight(db, camp)
	for _, e := range f.Order {
		ids[sprintAny(e["name"])] = asInt(e["id"])
	}
	darkID, gobID, otherID := ids["MvA Darkmantle"], ids["MvA Goblin"], ids["MvA Other"]
	if darkID >= 0 || gobID >= 0 || otherID >= 0 {
		t.Fatalf("monsters not in the fight: %v", ids)
	}
	setHP := func(id, hp int) { // a lucky crit must not kill the target mid-test: it starts each attempt whole
		tx, _ := db.Begin()
		locked, err := lockFight(tx, camp)
		if err != nil {
			t.Fatal(err)
		}
		_, e := locked.entry(id)
		e["hp"], e["max_hp"] = hp, 500
		if err := writeFight(tx, locked); err != nil || tx.Commit() != nil {
			t.Fatalf("setting hp: %v", err)
		}
	}
	crush := func(target string) string {
		t.Helper()
		res := ""
		for try := 0; try < 40; try++ { // the roll is real (+5 vs the goblin's AC): retry until the Crush hits
			setHP(gobID, 500)
			setHP(otherID, 500)
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": map[string]interface{}{"monster": "MvA Darkmantle", "action": "Crush", "target": target}})
			res = fmt.Sprint(r["monster_action_result"])
			if strings.Contains(res, "damage") && !strings.Contains(res, "misses") {
				return res
			}
			if strings.Contains(res, "Nothing was rolled") {
				return res // a refusal needs no retry
			}
		}
		t.Fatalf("no hit in 40 tries, last: %.200s", res)
		return ""
	}
	res := crush("MvA Goblin")
	var attaches int
	if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind='attached' AND source_id=$2 AND target_id=$3`, camp, darkID, gobID).Scan(&attaches); err != nil || attaches != 1 {
		t.Fatalf("the monster-vs-monster hit recorded no attach: %d %v; result: %s", attaches, err, res)
	}
	if i := strings.Index(res, "not applied automatically"); i >= 0 {
		if j := strings.Index(res[i:], "]"); j >= 0 && strings.Contains(res[i:i+j], "attaches to the target") {
			t.Fatalf("the enforced onset was also handed to the GM: %.300s", res)
		}
	}
	var suffocating int
	if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind='suffocating' AND target_id=$2`, camp, gobID).Scan(&suffocating); err != nil || suffocating != 0 {
		t.Fatalf("the cover fired without its Advantage gate holding: %d suffocating rows (err %v); result: %.200s", suffocating, err, res)
	}

	// the while-attached rider: enforced parts leave the GM residual, the unsupported Speed 0 sentence stays
	if i := strings.Index(res, "not applied automatically"); i < 0 || !strings.Contains(res[i:], "Speed becomes 0") {
		t.Fatalf("the unsupported Speed 0 sentence left the GM residual: %.300s", res)
	} else if strings.Contains(res[i:], "can attack only the target") {
		t.Fatalf("the enforced only-target rider stayed in the GM residual: %.300s", res)
	}

	// it can attack only the attached creature: another target is refused before anything is rolled
	setHP(otherID, 500)
	otherHP := monsterHPNow(camp, otherID)
	res2 := crush("MvA Other")
	if !strings.Contains(res2, "can attack only the creature it is attached to") || !strings.Contains(res2, "Nothing was rolled") {
		t.Fatalf("attacking past the attach was not refused: %.300s", res2)
	}
	if hp := monsterHPNow(camp, otherID); hp != otherHP {
		t.Fatalf("the refused attack dealt damage: %d -> %d", otherHP, hp)
	}
	if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind='attached' AND source_id=$2`, camp, darkID).Scan(&attaches); err != nil || attaches != 1 {
		t.Fatalf("the refused attack touched the attach: %d rows (err %v)", attaches, err)
	}

	// against the attached creature the roll has Advantage (its stat block's rider) - and with Advantage
	// the cover's gate now holds, so the suffocating starts on the second hit
	res3 := crush("MvA Goblin")
	if !strings.Contains(res3, "with Advantage") {
		t.Fatalf("the attached-target attack rolled without Advantage: %.300s", res3)
	}
	if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind='suffocating' AND target_id=$2`, camp, gobID).Scan(&suffocating); err != nil || suffocating != 1 {
		t.Fatalf("the covered target is not suffocating after the Advantage hit: %d rows (err %v); %.200s", suffocating, err, res3)
	}

	// the rider ends with the attach: after a GM detach, other targets are attacked freely, without Advantage
	c, _ = pgCall(t, "POST", "/api/gm/detach", gm, map[string]interface{}{"campaign_id": camp, "attacher_id": darkID, "target_id": gobID})
	if c != 200 {
		t.Fatalf("detach: %d", c)
	}
	_, r4 := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": map[string]interface{}{"monster": "MvA Darkmantle", "action": "Crush", "target": "MvA Other"}})
	if res4 := fmt.Sprint(r4["monster_action_result"]); strings.Contains(res4, "can attack only") {
		t.Fatalf("the refusal outlived the attach: %.300s", res4)
	}
}
