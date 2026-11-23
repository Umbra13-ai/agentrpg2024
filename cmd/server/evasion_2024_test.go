package main

import (
	"fmt"
	"strings"
	"testing"
)

// I-WL-6: Evasion is the creature's, whoever makes it save. A character's Dexterity-save spell at a monster with Evasion (the SRD
// Assassin) and at a character with Evasion (a level 7 Rogue) deals no damage on a success; the other routes (a stat block's save effect at
// a character, a monster or a companion, the GM's area cast) read the same owner (creatureEvasion, game.SaveDamage).
func TestPostgresEvasionOnEverySaveRoute(t *testing.T) {
	testPostgres(t)
	gm, toks, camp, chars, _ := pgFightWith(t, "evasion", 2)
	caster, rogue := chars[0], chars[1]
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Evasive Assassin", "monster_key": "assassin", "hp": 3000, "ac": 1}}}); c != 200 {
		t.Fatalf("add the assassin: %d %v", c, j)
	}
	assassin := 0
	for _, e := range combatEntries(camp) {
		if fmt.Sprint(e["name"]) == "Evasive Assassin" {
			assassin = asInt(e["id"])
		}
	}
	if assassin >= 0 {
		t.Fatalf("no assassin in the fight: %v", combatEntries(camp))
	}
	// the caster's DC is 6 (Intelligence 1): the Assassin's Dexterity save (+7) and the Rogue's (+8) always succeed
	pgExec(t, `UPDATE characters SET class = 'wizard', subclass = NULL, level = 5, intl = 1, known_spells = '["burning-hands"]'::jsonb,
		prepared_spells = '["burning-hands"]'::jsonb, inventory = $2::jsonb WHERE id = $1`, caster, focusInventory)
	pgExec(t, `UPDATE characters SET class = 'rogue', level = 7, dex = 20, hp = 500, max_hp = 500 WHERE id = $1`, rogue)
	cast := func(body map[string]interface{}) string {
		t.Helper()
		pgExec(t, `UPDATE characters SET action_used = false, bonus_action_used = false, spell_slots_used = '{}'::jsonb, slot_spent_turn = '' WHERE id = $1`, caster)
		body["action"], body["spell_slug"], body["character_id"] = "cast", "burning-hands", caster
		c, j := pgCall(t, "POST", "/api/action", toks[0], body)
		if c != 200 {
			t.Fatalf("cast: %d %v", c, j)
		}
		return fmt.Sprint(j["result"])
	}
	before := monsterHPOf(camp, assassin)
	res := cast(map[string]interface{}{"target_ids": []int{assassin}})
	if after := monsterHPOf(camp, assassin); after != before || !strings.Contains(res, "Evasion") {
		t.Errorf("the Assassin's successful Dexterity save with Evasion: Hit Points %d to %d (none lost): %s", before, after, res)
	}
	var hpBefore, hpAfter int
	db.QueryRow(`SELECT hp FROM characters WHERE id = $1`, rogue).Scan(&hpBefore)
	res = cast(map[string]interface{}{"target_ids": []int{rogue}})
	db.QueryRow(`SELECT hp FROM characters WHERE id = $1`, rogue).Scan(&hpAfter)
	if hpAfter != hpBefore || !strings.Contains(res, "Evasion") {
		t.Errorf("the Rogue's successful Dexterity save with Evasion: Hit Points %d to %d (none lost): %s", hpBefore, hpAfter, res)
	}
}
