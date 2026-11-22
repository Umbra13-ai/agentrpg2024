package main

import (
	"fmt"
	"strings"
	"testing"
)

// A monster's cast of a spell with no attack roll and no saving throw lands through the owners a character's cast uses: Magic Missile's darts
// (beamPlan) and the Power Words (powerWordOn).
func TestPostgresMonsterCastDirectSpells(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, _, _ := companionReactionFixture(t)
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	for i, m := range []struct{ name, key string }{{"Direct Hag", "night-hag"}, {"Direct Glabrezu", "glabrezu"}, {"Direct Lich", "lich"}, {"Direct Sphinx", "sphinx-of-lore"}} {
		pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": m.name, "monster_key": m.key, "hp": 1000, "ac": 1, "initiative": -500 - i}}})
	}
	cast := func(monster, spell string) string {
		t.Helper()
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": monster, "action": "Spellcasting", "use": spell, "target": fmt.Sprint(charID)}})
		return fmt.Sprint(r["monster_action_result"])
	}
	hp := func() (h int) { db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, charID).Scan(&h); return }
	pgExec(t, `UPDATE characters SET hp=1000, max_hp=1000, species='human' WHERE id=$1`, charID)

	// Magic Missile: three darts, each 1d4+1 Force, no roll to hit
	before := hp()
	res := cast("Direct Hag", "Magic Missile")
	var darts int // 3 at level 1, one more for each level above (the Night Hag casts it at level 4)
	for n := 3; n <= 9; n++ {
		if strings.Contains(res, fmt.Sprintf("%d dart(s)", n)) {
			darts = n
		}
	}
	if lost := before - hp(); darts == 0 || lost < 2*darts || lost > 5*darts {
		t.Errorf("Magic Missile: %d darts, lost %d Hit Points (each dart is 1d4+1): %s", darts, lost, res)
	}
	// Power Word Kill: a target with 100 Hit Points or fewer dies; more takes 12d12 Psychic
	pgExec(t, `UPDATE characters SET hp=1000 WHERE id=$1`, charID)
	before = hp()
	res = cast("Direct Lich", "Power Word Kill")
	if lost := before - hp(); lost < 12 || !strings.Contains(res, "more than 100 Hit Points") {
		t.Errorf("Power Word Kill on 1000 Hit Points lost %d: %s", lost, res)
	}
	pgExec(t, `DELETE FROM monster_day_uses WHERE campaign_id=$1`, camp) // Power Word Kill is 1/Day
	pgExec(t, `UPDATE characters SET hp=60, max_hp=1000 WHERE id=$1`, charID)
	res = cast("Direct Lich", "Power Word Kill")
	if !strings.Contains(res, "dies") || hp() != 0 {
		t.Errorf("Power Word Kill on 60 Hit Points: hp %d: %s", hp(), res)
	}
	// Power Word Stun: 150 Hit Points or fewer is Stunned (the condition record the server runs)
	pgExec(t, `UPDATE characters SET hp=140, max_hp=1000, conditions='[]' WHERE id=$1`, charID)
	res = cast("Direct Glabrezu", "Power Word Stun")
	if !strings.Contains(res, "Stunned") || len(recordsOf(camp, charID, "stunned")) == 0 {
		t.Errorf("Power Word Stun: %s", res)
	}
	// Remove Curse: the curse record a stat block gave ends (the same owner as a character's cast)
	pgExec(t, `UPDATE characters SET hp=1000, conditions='[]' WHERE id=$1`, charID)
	addCondition(camp, charID, condAdd{Condition: condCursed + ":no_regain", Source: "the test's curse"})
	if len(recordsOf(camp, charID, condCursed)) == 0 {
		t.Fatal("the curse was not recorded")
	}
	if res := cast("Direct Sphinx", "Remove Curse"); len(recordsOf(camp, charID, condCursed)) != 0 {
		t.Errorf("Remove Curse left the curse: %s", res)
	}
}
