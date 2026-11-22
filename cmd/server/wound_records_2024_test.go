package main

import (
	"fmt"
	"strings"
	"testing"
)

// Wounds (wound_records_2024.go): the SRD 5.2.1 Bearded Devil's and Horned Devil's infernal wound, from the stat blocks' own sentences.
func TestPostgresInfernalWoundOnEveryTarget(t *testing.T) {
	testPostgres(t)
	gm, player, camp, hero, devil := earlyRulesFixture(t, "Test Wound Devil", []srdMonsterEntry{
		{Name: "Infernal Glaive", Text: "Melee Attack Roll: +60, reach 10 ft. Hit: 8 (1d10 + 3) Slashing damage. If the target is a creature and doesn’t already have an infernal wound, it is subjected to the following effect. Constitution Saving Throw: DC 30. Failure: The target receives an infernal wound. While wounded, the target loses 5 (1d10) Hit Points at the start of each of its turns. The wound closes after 1 minute, after a spell restores Hit Points to the target, or after the target or a creature within 5 feet of it takes an action to stanch the wound, doing so by succeeding on a DC 12 Wisdom (Medicine) check."},
		{Name: "Infernal Tail", Text: "Dexterity Saving Throw: DC 30, one creature the devil can see within 10 feet. Failure: 10 (1d8 + 6) Necrotic damage, and the target receives an infernal wound if it doesn’t have one. While wounded, the target loses 10 (3d6) Hit Points at the start of each of its turns. The wound closes after 1 minute, after a spell restores Hit Points to the target, or after the target or a creature within 5 feet of it takes an action to stanch the wound, doing so by succeeding on a DC 17 Wisdom (Medicine) check."},
	})
	pgExec(t, `UPDATE characters SET hp=1000, max_hp=1000, con=1, dex=1 WHERE id=$1`, hero)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Wound Kobold", "monster_key": "kobold-warrior", "hp": 1000, "ac": 1, "initiative": -750}}})
	kobold := monsterIDsByKeyOrName(camp, "Wound Kobold")[0]
	hpOf := func(id int) int {
		if id < 0 {
			return monsterHPNow(camp, id)
		}
		var hp int
		db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, id).Scan(&hp)
		return hp
	}

	// the hit's save: the wound, one minute, the Medicine check that stanches it; the loss reads the record
	_, line := narrateHit(t, gm, camp, devil, "Infernal Glaive", fmt.Sprint(hero), "")
	recs := recordsOf(camp, hero, woundedConditions)
	if len(recs) != 1 || recs[0].Detail != "1d10" || recs[0].DurationRounds != 10 || len(recs[0].Escapes) != 1 || recs[0].Escapes[0].DC != 12 || !recs[0].Escapes[0].Adjacent {
		t.Fatalf("Infernal Glaive should leave a timed wound with its Medicine check: %+v (%s)", recs, line)
	}
	if strings.Contains(line, "For the GM to apply") && strings.Contains(line, "loses 5") {
		t.Errorf("the wound is applied, not the GM's: %s", line)
	}
	// "doesn't already have an infernal wound": a second hit rolls no save and gives no second wound
	_, line = narrateHit(t, gm, camp, devil, "Infernal Glaive", fmt.Sprint(hero), "")
	if len(recordsOf(camp, hero, woundedConditions)) != 1 || !strings.Contains(line, "already has a wound") {
		t.Errorf("a wounded creature is not given another: %s", line)
	}
	// it loses Hit Points at the start of its turn
	before := hpOf(hero)
	if out := woundTurnStart(camp, hero, "Hero"); len(out) != 1 || hpOf(hero) >= before {
		t.Errorf("the wound should cost Hit Points: %v %d -> %d", out, before, hpOf(hero))
	}
	// a spell that restores Hit Points closes it; one that does not, doesn't
	if len(endWoundsBySpell(camp, hero, "bless")) != 0 || len(recordsOf(camp, hero, woundedConditions)) != 1 {
		t.Error("a spell that restores no Hit Points leaves the wound")
	}
	if len(endWoundsBySpell(camp, hero, "cure-wounds")) != 1 || len(recordsOf(camp, hero, woundedConditions)) != 0 {
		t.Error("a spell that restores Hit Points closes the wound")
	}

	// the entry with its own save gives it the same way, to a creature of the fight too
	for _, target := range []int{hero, kobold} {
		ref := fmt.Sprint(target)
		if target < 0 {
			ref = fmt.Sprintf("#%d", target)
		}
		narrateAs(t, gm, camp, devil, "Infernal Tail", ref, "")
		recs := recordsOf(camp, target, woundedConditions)
		if len(recs) != 1 || recs[0].Detail != "3d6" || recs[0].Escapes[0].DC != 17 {
			t.Fatalf("Infernal Tail on %d: %+v", target, recs)
		}
		before := hpOf(target)
		woundTurnStart(camp, target, "target")
		if hpOf(target) >= before {
			t.Errorf("%d: the wound should cost Hit Points (%d -> %d)", target, before, hpOf(target))
		}
		if target > 0 { // stanch it: the Wisdom (Medicine) check, made by the wounded character itself (the DC lowered below any roll)
			pgExec(t, `UPDATE condition_records SET escapes = jsonb_set(escapes, '{0,dc}', '-20') WHERE id=$1`, recs[0].ID)
			c, j := pgCall(t, "POST", "/api/conditions/escape", player, map[string]interface{}{"character_id": hero, "record_id": recs[0].ID, "escape": 0})
			if c != 200 || j["success"] != true || len(recordsOf(camp, hero, woundedConditions)) != 0 {
				t.Errorf("stanching the wound: %d %v", c, j)
			}
		} else {
			endConditionRecords(recs)
		}
	}
}

// Infernal Tail (Horned Devil) conditions only the wound on there being none: a creature already wounded still takes the save and the damage.
func TestPostgresInfernalTailHurtsTheWounded(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, devil := earlyRulesFixture(t, "Test Horned Tail Devil", []srdMonsterEntry{
		{Name: "Infernal Tail", Text: "Dexterity Saving Throw: DC 30, one creature the devil can see within 10 feet. Failure: 10 (1d8 + 6) Necrotic damage, and the target receives an infernal wound if it doesn’t have one. While wounded, the target loses 10 (3d6) Hit Points at the start of each of its turns. The wound closes after 1 minute, after a spell restores Hit Points to the target, or after the target or a creature within 5 feet of it takes an action to stanch the wound, doing so by succeeding on a DC 17 Wisdom (Medicine) check."},
	})
	pgExec(t, `UPDATE characters SET hp=1000, max_hp=1000, dex=1 WHERE id=$1`, hero)
	addCondition(camp, hero, condAdd{Condition: woundedConditions + ":1d10", Source: "an earlier devil"})
	hp := func() (h int) { db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, hero).Scan(&h); return }
	before := hp()
	_, line := narrateAs(t, gm, camp, devil, "Infernal Tail", fmt.Sprint(hero), "")
	if lost := before - hp(); lost < 7 || strings.Contains(fmt.Sprint(line), "no saving throw") {
		t.Errorf("a wounded target took %d from Infernal Tail (1d8 + 6 is 7 to 14): %v", lost, line)
	}
	if n := len(recordsOf(camp, hero, woundedConditions)); n != 1 {
		t.Errorf("a wounded target holds %d wounds after Infernal Tail, want the one it had", n)
	}
}
