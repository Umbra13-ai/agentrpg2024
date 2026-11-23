package main

import (
	"strings"
	"testing"
)

// A spell's "at the end of its next turn" damage is dealt then, once: Vitriolic Sphere on a failed save and Acid Arrow on a hit, at a
// monster in a fight (spell_later_damage_2024.go). The caster's DC is high and the monster's AC 1, so the save fails and the attack hits.
func TestPostgresSpellLaterDamageAtTheTargetsTurnEnd(t *testing.T) {
	testPostgres(t)
	_, char, camp, target, cast := spellFightFixture(t, "laterdamage")
	pgExec(t, `UPDATE characters SET class = 'wizard', subclass = NULL, level = 9, intl = 30, known_spells = '["vitriolic-sphere","acid-arrow"]'::jsonb,
		prepared_spells = '["vitriolic-sphere","acid-arrow"]'::jsonb, inventory = $2::jsonb WHERE id = $1`, char, focusInventory)
	pending := func() int {
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM combat_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = 'later_damage'`, camp, target).Scan(&n)
		return n
	}
	endTargetsTurn := func() {
		t.Helper()
		for i := 0; i < 12 && currentTurnID(camp) != target; i++ {
			advanceTurn(camp, turnHow{by: "a test"})
		}
		if currentTurnID(camp) != target {
			t.Fatalf("the target's turn never came")
		}
		advanceTurn(camp, turnHow{by: "a test"}) // its turn ends
	}
	for _, c := range []struct {
		slug      string
		body      map[string]interface{}
		landed    string
		low, high int // the later damage's range
	}{
		{"vitriolic-sphere", map[string]interface{}{"slot_level": 4, "target_ids": []int{target}}, "fails", 5, 20},
		{"acid-arrow", map[string]interface{}{"slot_level": 2, "target_id": target}, "HIT", 2, 8},
	} {
		landed := false
		for try := 0; try < 10 && !landed; try++ {
			pgExec(t, `DELETE FROM combat_effects WHERE lobby_id = $1 AND kind = 'later_damage'`, camp)
			res := cast(c.slug, c.body)
			landed = strings.Contains(res, c.landed) && pending() == 1
			if landed && !strings.Contains(res, "at the end of its next turn") {
				t.Errorf("%s: the cast does not say the later damage comes: %s", c.slug, res)
			}
		}
		if !landed {
			t.Fatalf("%s never landed on the target", c.slug)
		}
		before := monsterHPOf(camp, target)
		endTargetsTurn()
		lost := before - monsterHPOf(camp, target)
		if lost < c.low || lost > c.high || pending() != 0 {
			t.Errorf("%s: the target lost %d at the end of its turn (%d to %d expected), %d still pending", c.slug, lost, c.low, c.high, pending())
		}
		before = monsterHPOf(camp, target)
		endTargetsTurn() // dealt once: the next turn end deals nothing
		if lost := before - monsterHPOf(camp, target); lost != 0 {
			t.Errorf("%s: the later damage came again (%d)", c.slug, lost)
		}
	}
}
