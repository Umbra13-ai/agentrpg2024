package main

import (
	"fmt"
	"strings"
	"testing"
)

// The casting words name no one; anything else names someone (I-CALLA-24).
func TestDescribesOthers(t *testing.T) {
	for d, want := range map[string]bool{
		"":                                      false,
		"I cast Cure Wounds at 2nd level":       false,
		"cast prayer of healing using a slot":   false,
		"on myself":                             false,
		"Calla presses a hand to her own wound": true, // "presses", "hand" ... name more than the caster: the caster's name is read first, by parseTargetsFromDescription
		"over the five freed prisoners":         true,
		"heal the guard captain":                true,
	} {
		if got := describesOthers(d); got != want {
			t.Errorf("describesOthers(%q) = %v, want %v", d, got, want)
		}
	}
}

// I-CALLA-24: a healing spell aimed at creatures the server doesn't track (NPC prisoners) is cast (the slot is spent) and handed to the
// GM; it never heals the caster instead. Aimed at no one, it heals the caster; at characters by name, every one of them (Prayer of
// Healing's group, not only the first); at a creature of the fight by its target_id, that creature through its own Hit Point owner.
func TestPostgresHealingSpellAimsAtWhomItNames(t *testing.T) {
	testPostgres(t)
	gm, toks, camp, chars, mons := pgFightWith(t, "ally aim", 2)
	tok, healer, ally, monster := toks[0], chars[0], chars[1], mons[0]
	pgExec(t, `UPDATE characters SET class = 'cleric', subclass = NULL, level = 5, wis = 18, spell_slots_used = '{}',
		known_spells = '["sacred-flame"]', prepared_spells = '["cure-wounds","prayer-of-healing","healing-word"]' WHERE id = $1`, healer)
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/end", camp), gm, map[string]interface{}{}); c != 200 { // outside a fight: casts are immediate
		t.Fatalf("combat end: %d %v", c, j)
	}
	hp := func(id int) int {
		var n int
		db.QueryRow(`SELECT hp FROM characters WHERE id = $1`, id).Scan(&n)
		return n
	}
	hurt := func() {
		pgExec(t, `UPDATE characters SET hp = 3, spell_slots_used = '{}', action_used = false, bonus_action_used = false WHERE id IN ($1, $2)`, healer, ally)
	}
	cast := func(body map[string]interface{}) (int, string) {
		t.Helper()
		pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "The moment passes."}) // a new exploration beat
		body["action"] = "cast"
		c, j := pgCall(t, "POST", "/api/action", tok, body)
		return c, fmt.Sprint(j["result"], j["message"])
	}

	// NPCs named: cast, the GM applies it, the caster isn't healed
	hurt()
	c, res := cast(map[string]interface{}{"spell_slug": "prayer-of-healing", "slot_level": 2, "description": "Calla's companions pray over the five freed prisoners"})
	if c != 200 || !strings.Contains(res, "the GM applies it") || hp(healer) != 3 {
		// "Calla" names the caster here only through the first name rule when it is the caster's: the test character has another name
		t.Errorf("a heal aimed at NPCs: %d %s (caster at %d)", c, res, hp(healer))
	}

	// no one named: the caster
	hurt()
	if c, res = cast(map[string]interface{}{"spell_slug": "cure-wounds", "description": "I cast Cure Wounds"}); c != 200 || hp(healer) <= 3 || hp(ally) != 3 {
		t.Errorf("a heal aimed at no one: %d %s (caster %d, ally %d)", c, res, hp(healer), hp(ally))
	}

	// both characters by name: both healed
	var healerName, allyName string
	db.QueryRow(`SELECT name FROM characters WHERE id = $1`, healer).Scan(&healerName)
	db.QueryRow(`SELECT name FROM characters WHERE id = $1`, ally).Scan(&allyName)
	hurt()
	if c, res = cast(map[string]interface{}{"spell_slug": "prayer-of-healing", "slot_level": 2, "description": "a prayer for " + healerName + " and " + allyName}); c != 200 || hp(healer) <= 3 || hp(ally) <= 3 {
		t.Errorf("a group heal naming two characters: %d %s (caster %d, ally %d)", c, res, hp(healer), hp(ally))
	}

	// a creature of the fight by its target_id: its own Hit Point owner, not the caster
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/start", camp), gm, map[string]interface{}{}); c != 200 {
		t.Fatalf("combat start: %d %v", c, j)
	}
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{
		"combatants": []map[string]interface{}{{"name": "Rescued Guard", "monster_key": "goblin-warrior", "hp": 20, "ac": 10}}})
	guard := 0
	for _, id := range monsterIDsByKeyOrName(camp, "Rescued Guard") {
		guard = id
	}
	_ = monster
	if guard == 0 {
		t.Fatal("no guard in the fight")
	}
	if err := setMonsterHP(camp, guard, 2); err != nil { // wounded: 2 of 20
		t.Fatal(err)
	}
	pgExec(t, `UPDATE characters SET hp = 3, spell_slots_used = '{}', action_used = false, bonus_action_used = false WHERE id = $1`, healer)
	resetTurnFlags(healer, camp)
	c, res = cast(map[string]interface{}{"spell_slug": "healing-word", "target_id": guard, "description": "Healing Word on the guard"})
	if c != 200 || hp(healer) != 3 || monsterHPNow(camp, guard) <= 2 || !strings.Contains(res, "Rescued Guard regains") {
		t.Errorf("a heal at a creature of the fight: %d %s (caster %d, guard %d)", c, res, hp(healer), monsterHPNow(camp, guard))
	}
}
