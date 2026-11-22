package main

import (
	"fmt"
	"strings"
	"testing"
)

// Curses (curse_records_2024.go): the SRD 5.2.1 wordings of the Incubus, Rakshasa, Mummy, Lamia and the lycanthropes, from the stat
// blocks' own sentences, become a `cursed` record whose flags the rules owners read.
func TestPostgresStatBlockCursesOnEveryTarget(t *testing.T) {
	testPostgres(t)
	gm, player, camp, hero, fiend := earlyRulesFixture(t, "Test Curse Fiend", []srdMonsterEntry{
		{Name: "Restless Touch", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 15 (3d6 + 5) Psychic damage, and the target is cursed for 24 hours or until the incubus dies. Until the curse ends, the target gains no benefit from finishing Short Rests."},
		{Name: "Cursed Touch", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 12 (2d6 + 5) Slashing damage plus 19 (3d12) Necrotic damage. If the target is a creature, it is cursed. While cursed, the target gains no benefit from finishing a Short or Long Rest."},
		{Name: "Rotting Fist", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 8 (1d10 + 3) Bludgeoning damage plus 10 (3d6) Necrotic damage. If the target is a creature, it is cursed. While cursed, the target can’t regain Hit Points, its Hit Point maximum doesn’t return to normal when finishing a Long Rest, and its Hit Point maximum decreases by 10 (3d6) every 24 hours that elapse. A creature dies and turns to dust if reduced to 0 Hit Points by this attack."},
		{Name: "Corrupting Touch", Text: "Wisdom Saving Throw: DC 30, one creature the lamia can see within 5 feet. Failure: 13 (3d8) Psychic damage, and the target is cursed for 1 hour. Until the curse ends, the target has the Charmed and Poisoned conditions."},
		{Name: "Bite", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 12 (2d8 + 3) Piercing damage. If the target is a Humanoid, it is subjected to the following effect. Constitution Saving Throw: DC 30. Failure: The target is cursed. If the cursed target drops to 0 Hit Points, it instead becomes a Werewolf under the GM’s control and has 10 Hit Points. Success: The target is immune to this werewolf’s curse for 24 hours."},
	})
	pgExec(t, `UPDATE characters SET hp=1000, max_hp=1000, wis=1, con=1 WHERE id=$1`, hero)
	clear := func() {
		removeConditionsWhere(camp, hero, func(r conditionRecord) bool {
			return r.Condition == condCursed || r.Condition == "charmed" || r.Condition == "poisoned"
		})
	}
	curse := func() conditionRecord {
		t.Helper()
		recs := recordsOf(camp, hero, condCursed)
		if len(recs) != 1 {
			t.Fatalf("want one cursed record, got %+v", recs)
		}
		return recs[0]
	}
	hit := func(entry string) string {
		t.Helper()
		_, line := narrateHit(t, gm, camp, fiend, entry, fmt.Sprint(hero), "")
		if !strings.Contains(line, "HIT") {
			t.Fatalf("%s never hit: %s", entry, line)
		}
		return line
	}
	rest := func(path string) string {
		c, j := pgCall(t, "POST", fmt.Sprintf("/api/characters/%d/%s", hero, path), player, map[string]interface{}{})
		if c != 200 {
			t.Fatalf("%s: %d %v", path, c, j)
		}
		return fmt.Sprint(j["message"])
	}

	// Incubus: 24 hours, no benefit from Short Rests
	line := hit("Restless Touch")
	if r := curse(); r.Detail != curseShortRest || r.DurationRounds != 14400 {
		t.Errorf("Incubus curse: %+v (%s)", r, line)
	}
	if strings.Contains(line, "GM, not applied") && strings.Contains(line, "gains no benefit") {
		t.Errorf("the rest effect is applied, not the GM's: %s", line)
	}
	if ok, _ := restBenefit(hero, false); ok {
		t.Error("a cursed creature gets no Short Rest benefit")
	}
	if m := rest("short-rest"); !strings.Contains(m, "no benefit") {
		t.Errorf("the Short Rest should pass without benefit: %s", m)
	}
	if ok, _ := restBenefit(hero, true); !ok {
		t.Error("the Incubus's curse leaves the Long Rest alone")
	}
	// the record outlives a fight and a Long Rest's marker cleanup
	dropMarkersKeepConditions(hero)
	if len(recordsOf(camp, hero, condCursed)) != 1 {
		t.Fatal("a curse survives the cleanups of a fight")
	}
	clear()

	// Rakshasa: neither rest
	hit("Cursed Touch")
	if r := curse(); r.Detail != curseShortRest+"+"+curseLongRest {
		t.Errorf("Rakshasa curse: %+v", r)
	}
	if m := rest("rest"); !strings.Contains(m, "no benefit") {
		t.Errorf("the Long Rest should pass without benefit: %s", m)
	}
	clear()

	// Mummy: no Hit Points regained; the Hit Point maximum clauses stay the GM's (not applied: said)
	line = hit("Rotting Fist")
	if r := curse(); r.Detail != curseNoRegain {
		t.Errorf("Mummy curse: %+v", r)
	}
	if no, why := cantRegainHP(camp, hero); !no || why == "" {
		t.Error("a Mummy-cursed creature can't regain Hit Points")
	}
	if !strings.Contains(line, "Hit Point maximum") {
		t.Errorf("the Hit Point maximum sentence is not applied and must be told to the GM: %s", line)
	}
	// Remove Curse ends the record
	if txt := endCursesBySpell(camp, "remove-curse", []int{hero}); !strings.Contains(txt, "end") || len(recordsOf(camp, hero, condCursed)) != 0 {
		t.Errorf("Remove Curse: %q %+v", txt, recordsOf(camp, hero, condCursed))
	}
	if no, _ := cantRegainHP(camp, hero); no {
		t.Error("the curse is gone")
	}

	// Lamia: Charmed and Poisoned for an hour, ending with the curse
	narrateAs(t, gm, camp, fiend, "Corrupting Touch", fmt.Sprint(hero), "")
	if len(recordsOf(camp, hero, condCursed)) != 1 || len(recordsOf(camp, hero, "charmed")) != 1 || len(recordsOf(camp, hero, "poisoned")) != 1 {
		t.Fatalf("Lamia: curse %d charmed %d poisoned %d", len(recordsOf(camp, hero, condCursed)), len(recordsOf(camp, hero, "charmed")), len(recordsOf(camp, hero, "poisoned")))
	}
	endCursesBySpell(camp, "remove-curse", []int{hero})
	if len(recordsOf(camp, hero, "charmed")) != 0 || len(recordsOf(camp, hero, "poisoned")) != 0 {
		t.Error("the Charmed and Poisoned conditions end with the curse")
	}

	// a lycanthrope's bite: a Humanoid (the character) rolls the save and is cursed; another type is not subjected to it
	hit("Bite")
	if r := curse(); r.Detail != curseLycanthropy {
		t.Errorf("lycanthropy curse: %+v", r)
	}
	clear()
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Curse Wolf", "monster_key": "wolf", "hp": 1000, "ac": 1, "initiative": -740}}})
	wolf := monsterIDsByKeyOrName(camp, "Curse Wolf")[0]
	_, line = narrateHit(t, gm, camp, fiend, "Bite", fmt.Sprintf("#%d", wolf), "")
	if len(recordsOf(camp, wolf, condCursed)) != 0 || !strings.Contains(line, "isn't a Humanoid") {
		t.Errorf("a Beast is not subjected to a Humanoid's curse: %v %s", recordsOf(camp, wolf, condCursed), line)
	}
}
