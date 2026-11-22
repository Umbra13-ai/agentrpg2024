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

// The Darkness spell a monster casts at the point the GM aims on the battle map fills a 15-foot-radius Sphere of Magical Darkness there (the same rows and
// readers as a stat block's Magical Darkness), and it ends with the caster's Concentration.
func TestPostgresMonsterDarknessSpellAtAPoint(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, _, _ := companionReactionFixture(t)
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Dark Glabrezu", "monster_key": "glabrezu", "hp": 1000, "ac": 1, "initiative": -500}}})
	glab := monsterIDsByKeyOrName(camp, "Dark Glabrezu")[0]
	_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
	req := map[string]interface{}{"map": encounterMap{Grid: []string{"................"}, Tokens: []mapToken{
		{ID: charID, Name: "hero", Position: mapSquare{0, 0}, Size: 1}, {ID: glab, Name: "Dark Glabrezu", Position: mapSquare{12, 0}, Size: 1}}}}
	if rev, ok := state["revision"].(string); ok {
		req["confirmation"] = rev
	}
	if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
		t.Fatalf("put map: %d %v", c, r)
	}
	narrate := func(extra map[string]interface{}) string {
		ma := map[string]interface{}{"monster": "Dark Glabrezu", "action": "Spellcasting", "use": "Darkness", "target": fmt.Sprint(charID)}
		for k, v := range extra {
			ma[k] = v
		}
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": ma})
		return fmt.Sprint(r["monster_action_result"])
	}
	// without a point the area can't be placed: the GM's (an open item), nothing is recorded
	if res := narrate(nil); len(magicalDarknessRows(camp)) != 0 {
		t.Fatalf("a Sphere was recorded with no point: %s", res)
	}
	pgExec(t, `DELETE FROM monster_day_uses WHERE campaign_id=$1`, camp)
	res := narrate(map[string]interface{}{"point": []int{3, 0}})
	rows := magicalDarknessRows(camp)
	if len(rows) != 1 || rows[0].at == nil || *rows[0].at != (mapSquare{3, 0}) || rows[0].radius != 15 {
		t.Fatalf("the Sphere at the point: %v / %s", rows, res)
	}
	if !magicalDarknessAt(camp, charID) { // the hero stands 15 feet from the point
		t.Errorf("the hero, 15 feet from the point, is not in the Darkness")
	}
	if !magicalDarknessBlocksSight(camp, charID, glab) { // the hero stands in the Darkness: it can't see the Glabrezu outside it (the hero has no sense that pierces it)
		t.Errorf("a creature in the Darkness sees out of it")
	}
	// the caster, standing 5 feet from the point, is in its own Sphere (a Sphere at a point has no creature origin to exclude)
	_, state = pgCall(t, "GET", cp+"/battle-map", gm, nil)
	req = map[string]interface{}{"map": encounterMap{Grid: []string{"................"}, Tokens: []mapToken{
		{ID: charID, Name: "hero", Position: mapSquare{0, 0}, Size: 1}, {ID: glab, Name: "Dark Glabrezu", Position: mapSquare{4, 0}, Size: 1}}}}
	if rev, ok := state["revision"].(string); ok {
		req["confirmation"] = rev
	}
	if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
		t.Fatalf("move the caster: %d %v", c, r)
	}
	if !magicalDarknessAt(camp, glab) {
		t.Errorf("the caster, 5 feet from the point, is not in its own Darkness")
	}
	endMonsterConcentration(camp, glab, "Dark Glabrezu", "test")
	if len(magicalDarknessRows(camp)) != 0 {
		t.Errorf("the Darkness outlived the caster's Concentration")
	}
}
