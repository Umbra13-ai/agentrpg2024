package main

// Success-immunity auras: "immune to the <entry> of all <kind>s" (the 2024
// Troglodyte's Stench) grants one immunity that covers EVERY source of the
// entry, unlike "immune to this ...", which covers only the grantor. The grant
// keeps the granting monster as caster so its expiry ticks on the same owner
// schedule as a single-source grant (spell_effects_2024.go).

import (
	"fmt"
	"testing"
)

func TestPostgresAllSourcesSaveImmunity(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
		m.Traits = []srdMonsterEntry{
			{Name: "Review Stench", Text: "Constitution Saving Throw: DC 1, any creature (other than a troglodyte) that starts its turn in a 5-foot Emanation originating from the troglodyte. Failure: The target has the Poisoned condition until the start of its next turn. Success: The target is immune to the Stench of all troglodytes for 1 hour."},
			{Name: "Review Spores", Text: "Constitution Saving Throw: DC 99, any creature that starts its turn in a 5-foot Emanation originating from the troglodyte. Failure: The target has the Poisoned condition until the start of its next turn. Success: The target is immune to this aura for 1 hour."},
		}
	})
	// A second combatant from the same stat block: the all-sources clause is about sources.
	code, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", f.camp), f.gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Review Focus Caster", "monster_key": f.key, "initiative": -101}}})
	if code != 200 {
		t.Fatalf("second combatant: %d %v", code, j)
	}
	ids := monsterIDsByKeyOrName(f.camp, "Review Focus Caster")
	if len(ids) != 2 {
		t.Fatalf("ids: %v", ids)
	}
	other := ids[0]
	if other == f.caster {
		other = ids[1]
	}
	if modifier, _, _ := saveModifier(f.camp, f.char, "con"); modifier < 0 {
		t.Fatalf("fixture must pass a DC 1 Constitution save, has %+d", modifier)
	}
	ironGolemMap(t, f, map[int]int{f.char: 1, other: 2})
	heroTurn := func() map[string]interface{} {
		t.Helper()
		_, err := changeFight(f.camp, func(combat *fight) error {
			i, _ := combat.entry(f.char)
			if i < 0 || len(combat.Order) < 2 {
				return fmt.Errorf("fixture needs the hero and at least one other turn")
			}
			combat.Index = (i - 1 + len(combat.Order)) % len(combat.Order)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		code, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/next", f.camp), f.gm, map[string]interface{}{})
		if code != 200 || currentTurnID(f.camp) != f.char {
			t.Fatalf("advance to hero: %d %v", code, j)
		}
		return j
	}
	allGrants := func() []struct {
		caster, rounds int
	} {
		var out []struct {
			caster, rounds int
		}
		for _, e := range spellEffectsOn(f.camp, f.char) {
			if e.Kind == "effect_immune_all" && e.Detail == "Review Stench" {
				out = append(out, struct {
					caster, rounds int
				}{e.CasterID, e.RoundsLeft})
			}
		}
		return out
	}
	heroTurn()
	if grants := allGrants(); len(grants) != 1 || grants[0].rounds != 600 {
		t.Fatalf("the first hero turn stores one 1-hour (600-round) all-sources grant: %+v", grants)
	}
	grants := allGrants()
	if len(grants) != 1 || grants[0].rounds != 600 {
		t.Fatalf("one all-sources grant for 1 hour (600 rounds): %+v", grants)
	}
	if !effectImmune(f.camp, f.char, grants[0].caster, "Review Stench") {
		t.Fatal("the grantor's own Stench must be covered")
	}
	if !effectImmune(f.camp, f.char, other, "Review Stench") {
		t.Fatal("the all-sources clause must cover the OTHER troglodyte's Stench")
	}
	if !hasCondMarker(f.camp, f.char, "poisoned") {
		t.Fatal("the failed DC 99 Review Spores save must apply Poisoned")
	}
	passSpellTime(f.camp, 1)
	heroTurn()
	// A re-roll for either source would DELETE/INSERT (resetting to 600) or add
	// a second row; the covered grant may only tick down with time.
	if grants := allGrants(); len(grants) != 1 || grants[0].rounds != 599 {
		t.Fatalf("the grant persists, covers both sources, and only ticks down: %+v", grants)
	}
}
