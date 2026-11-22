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

	// Mummy: no Hit Points regained, and the Hit Point maximum that falls 3d6 every 24 hours (TestPostgresMummyCurseHitPointMaximum)
	line = hit("Rotting Fist")
	if r := curse(); !strings.HasPrefix(r.Detail, curseNoRegain+"+"+curseHPMax+"=3d6@") {
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

// The Mummy's curse takes Hit Point maximum: 3d6 for each full 24 story hours, kept through a Long Rest (which still returns Life Drain's part), and
// given back as an ordinary reduction when Remove Curse ends the curse; a maximum reduced to 0 is a death.
func TestPostgresMummyCurseHitPointMaximum(t *testing.T) {
	testPostgres(t)
	_, _, camp, char, _, _ := companionReactionFixture(t)
	text := srdEntryText(t, "Mummy", "Rotting Fist")
	var e saveEffect
	e.readCurse(text)
	if e.Curse == nil || !hasPrefixFlag(e.Curse.Flags, curseHPMax+"=3d6") || !containsStr(e.Curse.Flags, curseNoRegain) {
		t.Fatalf("the Mummy's curse flags: %+v", e.Curse)
	}
	var lord saveEffect
	lord.readCurse(srdEntryText(t, "Mummy Lord", "Rotting Fist"))
	if lord.Curse == nil || !containsStr(lord.Curse.Flags, curseLongRest) || containsStr(lord.Curse.Flags, curseShortRest) || !hasPrefixFlag(lord.Curse.Flags, curseHPMax+"=3d6") {
		t.Fatalf("the Mummy Lord's curse flags: %+v", lord.Curse)
	}
	pgExec(t, `UPDATE characters SET hp=1000, max_hp=1000, max_hp_reduced=0, max_hp_cursed=0, conditions='[]', is_dead=false WHERE id=$1`, char)
	pgExec(t, `UPDATE lobbies SET story_rounds=0 WHERE id=$1`, camp)
	e.SourceID = -1
	e.giveCurse(camp, char, "the Mummy's Rotting Fist")
	maxHP := func() (m, reduced, cursed int) {
		db.QueryRow(`SELECT max_hp, COALESCE(max_hp_reduced,0), COALESCE(max_hp_cursed,0) FROM characters WHERE id=$1`, char).Scan(&m, &reduced, &cursed)
		return
	}
	passSpellTimeOn(camp, storyDayRounds-1, 0) // a day less a round: nothing yet
	if m, _, c := maxHP(); m != 1000 || c != 0 {
		t.Fatalf("before 24 hours: max %d cursed %d", m, c)
	}
	passSpellTimeOn(camp, 1, 0) // the day is complete: 3d6
	m, _, c := maxHP()
	if c < 3 || c > 18 || m != 1000-c {
		t.Fatalf("after 24 hours: max %d, cursed part %d (3d6 is 3 to 18)", m, c)
	}
	passSpellTimeOn(camp, 1, 0)
	if _, _, c2 := maxHP(); c2 != c {
		t.Errorf("the same day was counted twice: %d then %d", c, c2)
	}
	// Life Drain's part returns with a Long Rest, the curse's does not
	if _, err := reduceMaxHP(char, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreAllHP(char); err != nil {
		t.Fatal(err)
	}
	if m, red, cur := maxHP(); red != 0 || cur != c || m != 1000-c {
		t.Errorf("after a Long Rest: max %d, reduced %d, cursed %d (the curse's %d stays)", m, red, cur, c)
	}
	passSpellTimeOn(camp, 2*storyDayRounds, 0) // two more days
	_, _, c3 := maxHP()
	if c3 < c+6 || c3 > c+36 {
		t.Errorf("two more days: cursed %d, was %d", c3, c)
	}
	// Remove Curse ends it: the part returns as an ordinary reduction, at the next Long Rest
	if endCursesBySpell(camp, "remove-curse", []int{char}) == "" {
		t.Fatal("Remove Curse ended nothing")
	}
	if m, red, cur := maxHP(); cur != 0 || red != c3 || m != 1000-c3 {
		t.Errorf("after Remove Curse: max %d, reduced %d, cursed %d", m, red, cur)
	}
	if _, err := restoreAllHP(char); err != nil {
		t.Fatal(err)
	}
	if m, red, cur := maxHP(); m != 1000 || red != 0 || cur != 0 {
		t.Errorf("after the next Long Rest: max %d reduced %d cursed %d", m, red, cur)
	}
	// a maximum reduced to 0 is a death
	pgExec(t, `UPDATE characters SET hp=2, max_hp=2, max_hp_reduced=0, max_hp_cursed=0, is_dead=false WHERE id=$1`, char)
	e.giveCurse(camp, char, "the Mummy's Rotting Fist")
	passSpellTimeOn(camp, storyDayRounds, 0)
	var dead bool
	db.QueryRow(`SELECT COALESCE(is_dead,false) FROM characters WHERE id=$1`, char).Scan(&dead)
	if !dead {
		t.Errorf("a cursed character whose Hit Point maximum fell to 0 is not dead")
	}
}

func hasPrefixFlag(flags []string, prefix string) bool {
	for _, f := range flags {
		if strings.HasPrefix(f, prefix) {
			return true
		}
	}
	return false
}

// Edges of the Mummy curse's Hit Point maximum (Fable review of Nov 22): a second hit while cursed doesn't double the days, a cursed creature regains no
// Hit Points at a Long Rest, a maximum already at 1 still dies, and another campaign's pass-time or a campaign the creature has left never ticks it.
func TestPostgresMummyCurseEdges(t *testing.T) {
	testPostgres(t)
	gm, _, camp, char, _, _ := companionReactionFixture(t)
	text := srdEntryText(t, "Mummy", "Rotting Fist")
	var e saveEffect
	e.readCurse(text)
	e.SourceID = -1
	reset := func() {
		pgExec(t, `UPDATE characters SET hp=500, max_hp=1000, max_hp_reduced=0, max_hp_cursed=0, conditions='[]', is_dead=false WHERE id=$1`, char)
		pgExec(t, `DELETE FROM condition_records WHERE target_id=$1`, char)
		pgExec(t, `UPDATE lobbies SET story_rounds=0 WHERE id=$1`, camp)
	}
	cursedRecords := func() (n int) {
		for _, r := range loadConditionRecords(camp, char) {
			if r.Condition == condCursed {
				n++
			}
		}
		return
	}
	cursedPart := func() (c int) {
		db.QueryRow(`SELECT COALESCE(max_hp_cursed,0) FROM characters WHERE id=$1`, char).Scan(&c)
		return
	}

	// 1. a second Rotting Fist while cursed: one curse, one 3d6 a day
	reset()
	e.giveCurse(camp, char, "Mummy 1's Rotting Fist")
	passSpellTimeOn(camp, 100, 0)
	e.giveCurse(camp, char, "Mummy 2's Rotting Fist")
	if n := cursedRecords(); n != 1 {
		t.Errorf("two hits left %d curse records, want 1", n)
	}
	passSpellTimeOn(camp, storyDayRounds, 0)
	if c := cursedPart(); c < 3 || c > 18 {
		t.Errorf("one day after two hits: the cursed part is %d (one 3d6 is 3 to 18)", c)
	}

	// 3. a cursed creature regains no Hit Points at a Long Rest, but Life Drain's part of the maximum returns
	reset()
	e.giveCurse(camp, char, "Mummy 1's Rotting Fist")
	if _, err := reduceMaxHP(char, 30); err != nil {
		t.Fatal(err)
	}
	pgExec(t, `UPDATE characters SET hp=100 WHERE id=$1`, char)
	if _, err := restoreAllHP(char); err != nil {
		t.Fatal(err)
	}
	var hp, maxHP int
	db.QueryRow(`SELECT hp, max_hp FROM characters WHERE id=$1`, char).Scan(&hp, &maxHP)
	if hp != 100 || maxHP != 1000 {
		t.Errorf("a cursed creature's Long Rest: hp %d (it can't regain Hit Points: want 100), max %d (Life Drain's part returns: want 1000)", hp, maxHP)
	}

	// 9. a maximum already at 1 dies when the curse takes more
	reset()
	pgExec(t, `UPDATE characters SET hp=1, max_hp=1 WHERE id=$1`, char)
	e.giveCurse(camp, char, "Mummy 1's Rotting Fist")
	passSpellTimeOn(camp, storyDayRounds, 0)
	var dead bool
	db.QueryRow(`SELECT COALESCE(is_dead,false) FROM characters WHERE id=$1`, char).Scan(&dead)
	if !dead {
		t.Errorf("a cursed character at Hit Point maximum 1 did not die when the curse took more")
	}

	// 4. a character that left the campaign is not ticked by its pass-time; joining another campaign starts the count again there
	reset()
	e.giveCurse(camp, char, "Mummy 1's Rotting Fist")
	pgExec(t, `UPDATE characters SET lobby_id=NULL WHERE id=$1`, char)
	passSpellTimeOn(camp, 3*storyDayRounds, 0)
	if c := cursedPart(); c != 0 {
		t.Errorf("a character that left the campaign lost %d Hit Point maximum to its pass-time", c)
	}
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Curse edges B", "max_players": 2})
	campB := int(j["campaign_id"].(float64))
	pgExec(t, `UPDATE lobbies SET story_rounds=$2 WHERE id=$1`, campB, 50*storyDayRounds) // another campaign, its clock far ahead
	pgExec(t, `UPDATE characters SET lobby_id=$1 WHERE id=$2`, campB, char)
	passSpellTimeOn(campB, 1, 0)
	if c := cursedPart(); c != 0 {
		t.Errorf("joining a campaign whose clock is far ahead counted %d Hit Point maximum at once; the count starts again there", c)
	}
	passSpellTimeOn(campB, storyDayRounds, 0)
	if c := cursedPart(); c < 3 || c > 18 {
		t.Errorf("one day on the new campaign's clock: the cursed part is %d (3d6 is 3 to 18)", c)
	}
	pgExec(t, `UPDATE characters SET lobby_id=$1 WHERE id=$2`, camp, char)
}
