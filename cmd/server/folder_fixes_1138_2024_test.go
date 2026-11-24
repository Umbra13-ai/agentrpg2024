package main

// I-ALDENQA-5: an attached attacked_with_disadvantage mechanic may end while the creature's Speed is 0 (unless_speed_zero, read through the
// speed owner), and covers can't claim a Speed-0 exception the mechanic doesn't apply. I-CALLA-33: the short view's spells_ready is the spell
// owner's list (spellsReady): cantrips, prepared spells and the always-prepared ones.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/agentrpg/agentrpg/game"
)

func TestBlurredSpeedZeroCoversNeedsTheFlag(t *testing.T) {
	sentence := "Attack rolls against the quickling have Disadvantage unless the quickling has the Incapacitated condition or its Speed is 0."
	m := inventedMonster("Test Blur Sprite", "Fey", []srdMonsterEntry{{Name: "Blurred Steps", Text: sentence,
		Mechanics: []entryMechanic{{"kind": "attacked_with_disadvantage", "covers": sentence}}}}, nil)
	if msg := validateCustomMonster(m); !strings.Contains(msg, "unless_speed_zero") {
		t.Errorf("covers claims the Speed-0 exception without the flag: %q", msg)
	}
	m.Traits[0].Mechanics[0]["unless_speed_zero"] = true
	if msg := validateCustomMonster(m); msg != "" {
		t.Errorf("with the flag: %q", msg)
	}
	k, _ := mechanicKindOf("attacked_with_disadvantage")
	if d := k.describe(m.Traits[0].Mechanics[0]); d != "attack rolls against it have Disadvantage unless it is Incapacitated or its Speed is 0" {
		t.Errorf("describe: %q", d)
	}
}

func TestPostgresBlurredMechanicEndsAtSpeedZero(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, _ := earlyRulesFixture(t, "Test Blur Host", nil)
	plain := []srdMonsterEntry{{Name: "Claw", Text: "Melee Attack Roll: +1, reach 5 ft. Hit: 1 (1d1) Slashing damage."}}
	flagged := inventedMonster("Test Speed Blur", "Fey", []srdMonsterEntry{{Name: "Blurred Steps", Text: "The sprite darts about.",
		Mechanics: []entryMechanic{{"kind": "attacked_with_disadvantage", "unless_speed_zero": true}}}}, plain)
	unflagged := inventedMonster("Test Still Blur", "Fey", []srdMonsterEntry{{Name: "Blurred Steps", Text: "The sprite darts about.",
		Mechanics: []entryMechanic{{"kind": "attacked_with_disadvantage"}}}}, plain)
	ids := importInventedMonsters(t, gm, camp, flagged, unflagged)
	disadv := func(target int) bool {
		atk := attackCtx{lobby: camp, attacker: hero, target: target, name: "Claw", bonus: 1, melee: true, weapon: true}
		ar, _, _ := atk.roll()
		return strings.Contains(strings.Join(ar.Disadvantage, ","), "Blurred Steps")
	}
	if !disadv(ids["Test Speed Blur"]) || !disadv(ids["Test Still Blur"]) {
		t.Fatal("free to move: both blurred")
	}
	for _, id := range []int{ids["Test Speed Blur"], ids["Test Still Blur"]} {
		addCondition(camp, id, condAdd{Condition: "restrained", Source: "test"})
	}
	if disadv(ids["Test Speed Blur"]) {
		t.Error("Restrained (Speed 0) with unless_speed_zero: still Disadvantage")
	}
	if !disadv(ids["Test Still Blur"]) {
		t.Error("Restrained without the flag: the Disadvantage should hold")
	}
}

func TestPostgresShortViewSpellsReady(t *testing.T) {
	testPostgres(t)
	_, player, _, hero, _ := earlyRulesFixture(t, "Test Spell Host", nil)
	life := ""
	for s, sc := range game.AvailableSubclasses {
		if sc.Name == "Life Domain" {
			life = s
		}
	}
	pgExec(t, `UPDATE characters SET class='Cleric', level=3, subclass=$2, known_spells='["sacred-flame"]', prepared_spells='["healing-word"]' WHERE id=$1`, hero, life)
	got := strings.Join(spellsReady(hero), ", ")
	for _, want := range []string{"Sacred Flame", "Healing Word", "Bless (always prepared)", "Cure Wounds (always prepared)", "Aid (always prepared)", "Lesser Restoration (always prepared)"} {
		if !strings.Contains(got, want) {
			t.Errorf("spells ready %q: missing %s", got, want)
		}
	}
	_, j := pgCall(t, "GET", fmt.Sprintf("/api/my-turn?character_id=%d&view=short", hero), player, nil)
	if you, _ := j["you"].(map[string]interface{}); !strings.Contains(fmt.Sprint(you["spells_ready"]), "Bless (always prepared)") {
		t.Errorf("the short view's spells_ready: %v", you["spells_ready"])
	}
}
