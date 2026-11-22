package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Effect records (effect_records_2024.go): SRD 5.2.1 Copper Dragon Giggling Magic and Gold Dragon Weakening Breath, read from the stat
// block's own words, recorded on a character and on a creature of the fight, and read by every owner they change.
func TestPostgresD20RidersOnEveryRoute(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, drake := earlyRulesFixture(t, "Test D20 Riders Dragon", []srdMonsterEntry{
		{Name: "Giggling Magic", Text: "Charisma Saving Throw: DC 30, one creature the dragon can see within 90 feet. Failure: 24 (7d6) Psychic damage. Until the end of its next turn, the target rolls 1d6 whenever it makes an ability check or attack roll and subtracts the number rolled from the D20 Test. Failure or Success: The dragon can’t take this action again until the start of its next turn."},
		{Name: "Weakening Breath", Text: "Strength Saving Throw: DC 30, each creature that isn’t currently affected by this breath in a 30-foot Cone. Failure: The target has Disadvantage on Strength-based D20 Tests and subtracts 3 (1d6) from its damage rolls. It repeats the save at the end of each of its turns, ending the effect on itself on a success. After 1 minute, it succeeds automatically."},
	})
	pgExec(t, `UPDATE characters SET cha=1, str=1, species='Human' WHERE id=$1`, hero)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "D20 Kobold", "monster_key": "kobold-warrior", "hp": 1000, "ac": 1, "initiative": -730}}})
	kobold := monsterIDsByKeyOrName(camp, "D20 Kobold")[0]

	// Giggling Magic on the character: a record of the die, until the end of its next turn
	if c, line := narrateAs(t, gm, camp, drake, "Giggling Magic", fmt.Sprint(hero), ""); c != 200 || strings.Contains(line, "GM, not applied") && strings.Contains(line, "subtracts the number rolled") {
		t.Fatalf("Giggling Magic: %d %s", c, line)
	}
	recs := recordsOf(camp, hero, recD20Minus)
	if len(recs) != 1 || recs[0].Detail != "1d6" || recs[0].DurationRounds == 0 {
		t.Fatalf("Giggling Magic should leave a timed d20_minus:1d6 record: %+v", recs)
	}
	// every ability check and attack roll subtracts the die; a save does not
	cr := checkCtx{lobby: camp, who: hero, ability: "wis", skill: "perception"}.roll()
	if cr.Penalty < 1 || cr.Penalty > 6 || !strings.Contains(cr.Why.note(), "-") {
		t.Errorf("a check should subtract 1d6: penalty %d note %q", cr.Penalty, cr.Why.note())
	}
	atk := attackCtx{lobby: camp, attacker: hero, target: kobold, bonus: 5, melee: true, weapon: true}
	rolled, _, _ := atk.roll()
	found := false
	for _, a := range rolled.Adjust {
		found = found || (a.Amount < 0 && a.Amount >= -6)
	}
	if !found {
		t.Errorf("an attack roll should subtract the die: %+v", rolled.Adjust)
	}
	// the routes that roll their own d20 read the same sources: the penalty is on the sources, not on a roll helper
	if p := (checkCtx{lobby: camp, who: hero, ability: "str", skill: "athletics", escape: true}).sources().Penalty; p < 1 || p > 6 {
		t.Errorf("the sources of a check should carry the die: %d", p)
	}
	if c, j := pgCall(t, "POST", "/api/gm/skill-check", gm, map[string]interface{}{"campaign_id": camp, "character_id": hero, "skill": "athletics", "dc": 10}); c != 200 {
		t.Fatalf("skill-check: %d %v", c, j)
	}
	endConditionRecords(recs)
	if _, n := d20MinusRoll(camp, hero); n != "" {
		t.Error("the die outlived its record")
	}

	// Weakening Breath on the character and on a creature of the fight
	for _, target := range []int{hero, kobold} {
		ref := fmt.Sprint(target)
		if target < 0 {
			ref = fmt.Sprintf("#%d", target) // a creature of the fight, as narrate names the one that acts
		}
		c, line0 := narrateAs(t, gm, camp, drake, "Weakening Breath", ref, "")
		if c != 200 {
			t.Fatalf("Weakening Breath on %d: %d %s", target, c, line0)
		}
		recs := recordsOf(camp, target, recWeakened)
		if len(recs) != 1 || recs[0].Detail != "1d6" {
			t.Fatalf("Weakening Breath on %d should leave one weakened:1d6 record: %+v (%s)", target, recs, line0)
		}
		if recs[0].EndSave == nil || recs[0].DurationRounds != 10 {
			t.Errorf("the record keeps the repeated save and its minute: %+v", recs[0])
		}
		// "each creature that isn't currently affected by this breath": a second breath skips it
		_, line := narrateAs(t, gm, camp, drake, "Weakening Breath", ref, "")
		if !strings.Contains(line, "already affected") || len(recordsOf(camp, target, recWeakened)) != 1 {
			t.Errorf("an affected creature is not a target again: %s", line)
		}
		// Strength-based D20 Tests have Disadvantage: checks, saves, attack rolls; other abilities do not
		if !(checkCtx{lobby: camp, who: target, ability: "str", skill: "athletics"}).sources().hasDis("weakened (a Strength-based D20 Test)") {
			t.Errorf("%d: a Strength check should have Disadvantage", target)
		}
		if (checkCtx{lobby: camp, who: target, ability: "dex", skill: "stealth"}).sources().hasDis("weakened (a Strength-based D20 Test)") {
			t.Errorf("%d: a Dexterity check should not", target)
		}
		dis := func(strength bool) bool {
			a := (&attackCtx{lobby: camp, attacker: target, bonus: 5, melee: true, weapon: true, strength: strength}).gather()
			for _, d := range a.Disadvantage {
				if strings.Contains(d, "weakened") {
					return true
				}
			}
			return false
		}
		if !dis(true) || dis(false) {
			t.Errorf("%d: only a Strength-based attack roll has Disadvantage", target)
		}
		// the die comes off every damage roll, never below 0
		if dx, note := damageRollAdjust(camp, target, 20, false); dx > -1 || dx < -6 || note == "" {
			t.Errorf("%d: a damage roll loses 1d6: %d %q", target, dx, note)
		}
		if dx, _ := damageRollAdjust(camp, target, 0, false); dx != 0 {
			t.Errorf("%d: damage 0 stays 0, got %d", target, dx)
		}
		endConditionRecords(recordsOf(camp, target, recWeakened))
		if dx, _ := damageRollAdjust(camp, target, 20, false); dx != 0 {
			t.Errorf("%d: no record, no change", target)
		}
	}
}

// A damage roll is adjusted by what is on its creature (Enlarge, Reduce, a weakened die): every route that rolls attack damage asks
// damageRollAdjust right after the roll, and nothing calls enlargeHit but it.
func TestDamageRollsAskTheAdjust(t *testing.T) {
	routes := map[string]string{ // file -> function whose body must call damageRollAdjust
		"attack_target.go":                    "resolveAttackOnMonster",
		"monster_attacks.go":                  "resolveMonsterAttack",
		"monster_attack_continuation_2024.go": "runMonsterAttackSequence",
		"companions_2024.go":                  "companionAttack",
		"gm_reactions.go":                     "handleGMOpportunityAttack",
		"spell_resolution.go":                 "resolveSpellOnMonster",
		"monster_effects_2024.go":             "applySaveEffect",
	}
	for file, fn := range routes {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		i := strings.Index(src, "func "+fn+"(")
		if i < 0 {
			t.Errorf("%s: no function %s (renamed? update this list)", file, fn)
			continue
		}
		body := src[i:]
		if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
			body = body[:j+1]
		}
		if !strings.Contains(body, "damageRollAdjust(") {
			t.Errorf("%s %s rolls attack damage and must ask damageRollAdjust (effect_records_2024.go)", file, fn)
		}
	}
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "effect_records_2024.go" || f == "spells_more_2024.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), "enlargeHit(") {
			t.Errorf("%s calls enlargeHit: ask damageRollAdjust instead", f)
		}
	}
}
