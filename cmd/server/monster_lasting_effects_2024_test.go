package main

// What lasts after a monster's effect (feedback log 66, S-ALDEN-10: the Pit Fiend's Bite applied Poisoned only). parseSaveEffect
// reads a Failure's lasting parts (damage at the start or end of each of the target's turns, no healing, the repeated save, an end
// after 1 minute); applySaveEffect records them on the first condition's record (the condition owner); a turn hook deals the damage
// through the Hit Point owner; regainBlocked (the Hit Point owner) refuses every healing while a record, the Sword of Wounding or
// Chill Touch says so; conditions_detail shows it all.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestParseSaveEffectLastingPartsInEverySRDStatBlock goes through every saving throw effect of the SRD 5.2.1 stat blocks: each lasting
// part the Failure text has is read (recorded with the first condition), or left to the GM in Extra when there is no condition to carry
// it, and damage that comes each turn is never dealt at once.
func TestParseSaveEffectLastingPartsInEverySRDStatBlock(t *testing.T) {
	turnDmg := regexp.MustCompile(`\((\d+d\d+(?:\s*[+−-]\s*\d+)?)\) [A-Za-z]+ damage at the (?:start|end) of each of its turns|At the (?:start|end) of each of its turns, the [^.,]*?takes \d+ \((\d+d\d+)\)`)
	repeat := regexp.MustCompile(`repeats the save at the end of each of its turns|At the end of each of its turns, the target repeats the save`)
	withTurnDamage := map[string]bool{}
	for _, m := range srdMonsters2024() {
		for _, list := range [][]srdMonsterEntry{m.Traits, m.Actions, m.BonusActions, m.Reactions, m.Legendary} {
			for _, entry := range list {
				e, ok := parseSaveEffect(entry.Text)
				if !ok {
					continue
				}
				name := m.Name + "'s " + entry.Name
				fail := mfxSection(entry.Text, "Failure:", "Success:", "Failure or Success:", "Second Failure:")
				if fail == "" {
					fail = mfxSection(entry.Text, "First Failure:", "Second Failure:", "Success:")
				}
				for _, x := range turnDmg.FindAllStringSubmatch(fail, -1) {
					dice := normDice(x[1] + x[2])
					for _, d := range e.Damage {
						if d.Dice == dice {
							t.Errorf("%s: the %s that comes each turn is dealt at once", name, dice)
						}
					}
					found := false
					for _, d := range e.TurnDamage {
						found = found || d.Dice == dice
					}
					switch {
					case len(e.Conditions) > 0 && !found:
						t.Errorf("%s: the %s each turn isn't recorded with the %v condition: %+v", name, dice, e.Conditions, e.TurnDamage)
					case len(e.Conditions) == 0 && !strings.Contains(e.Extra, x[0][:10]):
						t.Errorf("%s: the %s each turn has no condition to carry it and isn't left to the GM: %q", name, dice, e.Extra)
					}
				}
				if len(e.TurnDamage) > 0 {
					withTurnDamage[m.Name] = true
				}
				if strings.Contains(fail, "regain Hit Points") {
					unless := strings.Contains(fail, "regain Hit Points unless")
					switch {
					case len(e.Conditions) > 0 && !unless && !e.NoHealing:
						t.Errorf("%s: \"can't regain Hit Points\" isn't recorded: %+v", name, e)
					case (len(e.Conditions) == 0 || unless) && (e.NoHealing || !strings.Contains(e.Extra, "regain Hit Points")):
						t.Errorf("%s: a no-healing the server can't keep is the GM's: NoHealing %v, Extra %q", name, e.NoHealing, e.Extra)
					}
				}
				if repeat.MatchString(fail) != e.EndSave {
					t.Errorf("%s: the repeated save read as %v", name, e.EndSave)
				}
				if strings.Contains(fail, "After 1 minute, it succeeds automatically") && e.Rounds != 10 {
					t.Errorf("%s: \"After 1 minute, it succeeds automatically\" is 10 rounds, not %d", name, e.Rounds)
				}
				for _, gone := range []string{"After 1 minute, it succeeds automatically", "repeats the save at the end of each of its turns"} {
					if strings.Contains(e.Extra, gone) && !strings.Contains(e.Extra, ". ") {
						t.Errorf("%s: what is applied (%q) is still left to the GM alone: %q", name, gone, e.Extra)
					}
				}
			}
		}
	}
	// the stat blocks whose saving throw effects deal damage each turn; the Animated Rug of Smothering's Smother has no saving throw
	// (its grapple is the GM's choice instead of the damage), and the devils' infernal wounds and the Behir's swallow (each of the
	// behir's turns) have no condition of their own to carry the damage: the GM's
	var got []string
	for n := range withTurnDamage {
		got = append(got, n)
	}
	sort.Strings(got)
	if want := "Djinni, Kraken, Pit Fiend, Shambling Mound, Vrock"; strings.Join(got, ", ") != want {
		t.Errorf("monsters whose effects deal damage each turn: %s, want %s", strings.Join(got, ", "), want)
	}

	bite := "Melee Attack Roll: +14, reach 10 ft. Hit: 18 (3d6 + 8) Piercing damage. If the target is a creature, it must make the following saving throw. Constitution Saving Throw: DC 21. Failure: The target has the Poisoned condition. While Poisoned, the target can’t regain Hit Points and takes 21 (6d6) Poison damage at the start of each of its turns, and it repeats the save at the end of each of its turns, ending the effect on itself on a success. After 1 minute, it succeeds automatically."
	e, _ := parseSaveEffect(bite)
	if len(e.Damage) != 0 || len(e.Conditions) != 1 || e.Conditions[0] != "poisoned" || !e.NoHealing || !e.EndSave || e.Rounds != 10 || e.Extra != "" ||
		len(e.TurnDamage) != 1 || e.TurnDamage[0] != (conditionTurnDamage{Dice: "6d6", Type: "poison", When: "start_of_turn"}) {
		t.Errorf("Pit Fiend's Bite: %+v", e)
	}
	engulf := "Strength Saving Throw: DC 15, one Medium or smaller creature within 5 feet. Failure: The target is pulled into the shambling mound’s space and has the Grappled condition (escape DC 14). Until the grapple ends, the target has the Blinded and Restrained conditions, and it takes 10 (3d6) Lightning damage at the start of each of its turns. When the shambling mound moves, the Grappled target moves with it, costing it no extra movement. The shambling mound can have only one creature Grappled by this action at a time."
	e, _ = parseSaveEffect(engulf)
	if len(e.Conditions) != 1 || e.Conditions[0] != "grappled" || e.EscapeDC != 14 || len(e.TurnDamage) != 1 || e.TurnDamage[0].Type != "lightning" || len(e.Damage) != 0 ||
		!strings.Contains(e.Extra, "Blinded and Restrained") {
		t.Errorf("Shambling Mound's Engulf (the Blinded and Restrained that last with the grapple are the GM's): %+v", e)
	}
	scare := "Wisdom Saving Throw: DC 10, one creature within 20 feet. Failure: The target has the Frightened condition. At the end of each of its turns, the target repeats the save, ending the effect on itself on a success. After 1 minute, it succeeds automatically."
	if e, _ = parseSaveEffect(scare); !e.EndSave || e.Rounds != 10 || e.Extra != "" {
		t.Errorf("Quasit's Scare: %+v", e)
	}
}

// TestPostgresPitFiendBiteLasts: the Bite through the GM's narrate on a character: the record, my-turn's conditions_detail, the
// damage at the start of the character's turn, the repeated save at its end, healing refused while poisoned on every healing path, and
// healing back once the record ends. Then the regain rule on a monster (Chill Touch stops Regeneration and the GM's heal; a Parry's
// give-back is no healing) and a condition's damage each turn on a monster.
func TestPostgresPitFiendBiteLasts(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "pg bite gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Bite", "max_players": 2})
	lobby := int(j["campaign_id"].(float64))
	pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/schedule", lobby), gm, map[string]interface{}{"state": "off"})
	_, tok := pgAgent(t, "pg bite player")
	_, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Doran %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", lobby), tok, map[string]interface{}{"character_id": char})
	pgFinish(t, tok, char)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), tok, nil)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), gm, nil)
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", lobby), gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/start", lobby), gm, map[string]interface{}{}); c != 200 {
		t.Fatalf("combat start: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", lobby), gm, map[string]interface{}{
		"combatants": []map[string]interface{}{{"name": "Test Fiend", "monster_key": "pit-fiend"}}}); c != 200 {
		t.Fatalf("add: %d %v", c, j)
	}
	pgExec(t, `UPDATE characters SET max_hp = 500, hp = 300 WHERE id = $1`, char)
	hp := func() int {
		var h int
		db.QueryRow(`SELECT hp FROM characters WHERE id = $1`, char).Scan(&h)
		return h
	}
	poison := func() (conditionRecord, bool) {
		for _, r := range loadConditionRecords(lobby, char) {
			if r.Condition == "poisoned" {
				return r, true
			}
		}
		return conditionRecord{}, false
	}

	// bite until it hits and the save fails (a Dwarf has Advantage on the save against Poisoned)
	for i := 0; i < 40; i++ {
		if _, ok := poison(); ok {
			break
		}
		c, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": lobby, "narration": "The fiend bites.",
			"monster_action": map[string]interface{}{"monster": "Test Fiend", "action": "Bite", "target": fmt.Sprint(char)}})
		if c != 200 {
			t.Fatalf("narrate: %d %v", c, j)
		}
		if res := fmt.Sprint(j["monster_action_result"]); strings.Contains(res, "Poisoned") && !strings.Contains(res, "Poisoned (can't regain Hit Points; 6d6 Poison damage at the start of each of its turns") {
			t.Errorf("the Bite's line doesn't say what lasts: %s", res)
		}
	}
	rec, ok := poison()
	if !ok {
		t.Fatal("forty Bites and no failed save")
	}
	if !rec.NoHealing || rec.DurationRounds != 10 || rec.EndSave == nil || rec.EndSave.DC != 21 || rec.EndSave.Ability != "con" ||
		len(rec.TurnDamage) != 1 || rec.TurnDamage[0] != (conditionTurnDamage{Dice: "6d6", Type: "poison", When: "start_of_turn"}) || rec.Source != "Test Fiend's Bite" {
		t.Fatalf("the Bite's record: %+v", rec)
	}

	// my-turn's conditions_detail says what lasts (the player isn't told a DC)
	c, mt := pgCall(t, "GET", fmt.Sprintf("/api/my-turn?character_id=%d", char), tok, nil)
	if c != 200 {
		t.Fatalf("my-turn: %d %v", c, mt)
	}
	var shown map[string]interface{}
	if ch, _ := mt["character"].(map[string]interface{}); ch != nil {
		list, _ := ch["conditions_detail"].([]interface{})
		for _, x := range list {
			if v, _ := x.(map[string]interface{}); v != nil && v["condition"] == "poisoned" {
				shown = v
			}
		}
	}
	save, _ := shown["ends_on_save"].(map[string]interface{})
	if shown == nil || shown["no_healing"] != true || shown["source"] != "Test Fiend's Bite" || shown["duration_rounds"] != float64(10) || save == nil ||
		save["ability"] != "Constitution" || save["when"] != "end_of_turn" || save["dc"] != nil || shown["turn_damage"] == nil ||
		shown["effect"] != "can't regain Hit Points; 6d6 Poison damage at the start of each of your turns; ends on a successful Constitution saving throw at the end of each of your turns; 10 round(s) left at most" {
		t.Errorf("conditions_detail: %v", shown)
	}

	// no healing while poisoned: the GM's heal, the healing owner itself, a Long Rest's Hit Points
	before := hp()
	c, hj := pgCall(t, "POST", fmt.Sprintf("/api/characters/%d/heal", char), gm, map[string]interface{}{"healing": 20})
	if c != 200 || hp() != before || hj["healing_applied"] != float64(0) || hj["cannot_regain_hp"] != "the Poisoned condition (Test Fiend's Bite)" {
		t.Errorf("a heal while poisoned: %d %v (HP %d → %d)", c, hj, before, hp())
	}
	if h, err := healHP(char, 5); err != nil || h.Healed != 0 || h.Blocked == "" || hp() != before {
		t.Errorf("healHP while poisoned: %+v %v", h, err)
	}
	if v, err := restoreAllHP(char); err != nil || v.HP != before {
		t.Errorf("a Long Rest's Hit Points while poisoned: %+v %v", v, err)
	}
	if acts, err := readActions(actionFilter{lobby: lobby, limit: 20, newestFirst: true}); err != nil || !strings.Contains(fmt.Sprint(acts), "can't regain Hit Points (the Poisoned condition (Test Fiend's Bite)): the healing has no effect") {
		t.Errorf("the refused healing isn't in the log: %v", err)
	}

	// the character's turn starts: the 6d6 Poison damage (halved by Dwarven Resilience); its turn ends: the repeated save
	f, err := readFight(db, lobby)
	if err != nil {
		t.Fatalf("read the fight: %v", err)
	}
	fiendAt := -1
	for i, e := range f.Order {
		if asInt(e["id"]) < 0 {
			fiendAt = i
		}
	}
	pgExec(t, `UPDATE combat_state SET current_turn_index = $2, turn_claim = '' WHERE lobby_id = $1`, lobby, fiendAt)
	before = hp()
	tc, code := advanceTurn(lobby, turnHow{by: "a test"})
	if code != "" || tc.NextID != char {
		t.Fatalf("to the character's turn: %q %+v", code, tc)
	}
	if took := before - hp(); took < 3 || took > 18 || !strings.Contains(strings.Join(tc.Log, " "), "At the start of its turn, the Poisoned condition (Test Fiend's Bite) deals 6d6 Poison damage") {
		t.Errorf("the start of the turn: %d damage (3-18 after Dwarven Resilience), log %v", took, tc.Log)
	}
	tc, code = advanceTurn(lobby, turnHow{by: "a test"})
	if code != "" || tc.EndedID != char {
		t.Fatalf("the character's turn ends: %q %+v", code, tc)
	}
	log := strings.Join(tc.Log, " ")
	rec, still := poison()
	switch {
	case !strings.Contains(log, "repeats the con saving throw at the end of its turn"):
		t.Errorf("no repeated save at the end of the turn: %v", tc.Log)
	case still && (rec.DurationRounds != 9 || !strings.Contains(log, "fails")):
		t.Errorf("a failed save counts the minute down: %+v %v", rec, tc.Log)
	case !still && !strings.Contains(log, "succeeds"):
		t.Errorf("the record ended without a success: %v", tc.Log)
	}

	// the record ends: healing works again
	removeConditionsWhere(lobby, char, func(r conditionRecord) bool { return r.Condition == "poisoned" })
	before = hp()
	if c, hj := pgCall(t, "POST", fmt.Sprintf("/api/characters/%d/heal", char), gm, map[string]interface{}{"healing": 20}); c != 200 || hp() != before+20 || hj["cannot_regain_hp"] != nil {
		t.Errorf("a heal after the poison ended: %d %v (HP %d → %d)", c, hj, before, hp())
	}

	// the other sources of the one rule: the Sword of Wounding on a character
	addSpellEffect(lobby, char, char, "Sword of Wounding", "no_regain", "", 600, "con", 15)
	if h, _ := healHP(char, 5); h.Blocked != "Sword of Wounding" || h.Healed != 0 {
		t.Errorf("the Sword of Wounding: %+v", h)
	}
	pgExec(t, `DELETE FROM spell_effects WHERE lobby_id = $1 AND kind = 'no_regain'`, lobby)

	// a monster: Chill Touch stops the GM's heal (and Regeneration, through healMonster); a Parry's give-back isn't healing
	fiend := asInt(f.Order[fiendAt]["id"])
	if err := setMonsterHP(lobby, fiend, 100); err != nil {
		t.Fatal(err)
	}
	addCombatEffect(lobby, fiend, char, "no_healing")
	if _, after, blocked, err := healMonster(lobby, fiend, 10); err != nil || blocked != "Chill Touch" || after.HP != 100 {
		t.Errorf("Chill Touch on a monster: %q %+v %v", blocked, after, err)
	}
	if _, after, err := undoMonsterDamage(lobby, fiend, 10); err != nil || after.HP != 110 {
		t.Errorf("a hit given back isn't healing: %+v %v", after, err)
	}
	pgExec(t, `DELETE FROM combat_effects WHERE lobby_id = $1`, lobby)
	if _, after, blocked, _ := healMonster(lobby, fiend, 10); blocked != "" || after.HP != 120 {
		t.Errorf("a monster's heal without Chill Touch: %q %+v", blocked, after)
	}

	// a condition's damage each turn on a monster, through the monster Hit Point owner (the Pit Fiend resists nothing of Acid)
	addCondition(lobby, fiend, condAdd{Condition: "restrained", Source: "a test's swallow", TurnDamage: []conditionTurnDamage{{Dice: "2d6", Type: "acid", When: "end_of_turn"}}})
	pgExec(t, `UPDATE combat_state SET current_turn_index = $2, turn_claim = '' WHERE lobby_id = $1`, lobby, fiendAt)
	tc, code = advanceTurn(lobby, turnHow{by: "a test"})
	f, _ = readFight(db, lobby)
	_, e := f.entry(fiend)
	if took := 120 - asInt(e["hp"]); code != "" || took < 2 || took > 12 || !strings.Contains(strings.Join(tc.Log, " "), "the Restrained condition (a test's swallow) deals 2d6 Acid damage") {
		t.Errorf("a monster's damage at the end of its turn: %q, %d damage, log %v", code, took, tc.Log)
	}
}
