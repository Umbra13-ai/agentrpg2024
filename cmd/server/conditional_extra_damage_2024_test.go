package main

// An attack whose Hit sentence holds an extra amount the server has no reader for (a hit-point condition, charges the creature chooses to spend)
// rolls: its primary damage is dealt and the GM is told the extra, nothing is guessed, and nothing is refused (monster_damage_choice_2024.go).

import (
	"fmt"
	"strings"
	"testing"
)

func TestPostgresConditionalExtraDamageIsTheGMs(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, _ := earlyRulesFixture(t, "Test Extra Host", nil)
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,ac=1 WHERE id=$1`, hero)
	knave := inventedMonster("Test Extra Knave", "Humanoid", nil, []srdMonsterEntry{
		{Name: "Dagger", Text: "Melee Attack Roll: +30, reach 5 ft. Hit: 5 (1d4 + 3) Piercing damage plus an extra 5 (2d4) Piercing damage if the knave has at least 18 Hit Points."},
		{Name: "Staff", Text: "Melee Attack Roll: +30, reach 5 ft. Hit: 6 (1d6 + 3) Bludgeoning damage, and the knave can expend up to 3 of the staff's charges, dealing an extra 3 (1d6) Force damage for each expended charge."},
		{Name: "Multiattack", Text: "The knave makes two attacks, using Dagger or Staff in any combination."}})
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"campaign_id": camp, "source_note": "invented conditional-extra fixture", "monsters": []srdMonster2024{knave}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key = $1`, key) })
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Test Extra Knave", "monster_key": key, "initiative": 30}}})
	hp := func() (h int) { db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, hero).Scan(&h); return }
	run := func(ma map[string]interface{}) (int, map[string]interface{}) {
		ma["monster"], ma["target"] = "Test Extra Knave", fmt.Sprint(hero)
		return pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": ma})
	}
	for _, c := range []struct{ action, note string }{
		{"Dagger", "2d4"}, {"Staff", "1d6"},
	} {
		before := hp()
		code, r := run(map[string]interface{}{"action": c.action})
		res := fmt.Sprint(r["monster_action_result"])
		if code != 200 || hp() >= before || !strings.Contains(res, "applies as well when this holds") || !strings.Contains(res, "dealt its normal damage") {
			t.Errorf("%s: %d %q (hp %d -> %d)", c.action, code, res, before, hp())
		}
	}
	// inside a Multiattack the same attacks roll
	before := hp()
	code, r := run(map[string]interface{}{"action": "Multiattack", "attacks": []string{"Dagger", "Staff"}})
	if code != 200 || hp() >= before || strings.Contains(fmt.Sprint(r), "not understood") {
		t.Errorf("Multiattack: %d %v (hp %d -> %d)", code, r, before, hp())
	}
}
