package main

import (
	"fmt"
	"strings"
	"testing"
)

// How many creatures a spell that heals or restores reaches is healTargetLimit, read by a monster's cast and a character's (allyAimRefusal).
func TestPostgresHealTargetLimitOverTheRestoringSpells(t *testing.T) {
	testPostgres(t) // the server's start loads the spells
	want := map[string]int{"cure-wounds": 1, "healing-word": 1, "heal": 1, "lesser-restoration": 1, "spare-the-dying": 1, "power-word-heal": 1,
		"aid": 3, "prayer-of-healing": 5, "mass-healing-word": 6, "mass-cure-wounds": 6, "conjure-celestial": 0}
	for key, n := range want {
		sp, ok := srdSpellsMemory[key]
		if !ok {
			t.Fatalf("%s isn't loaded", key)
		}
		if got := healTargetLimit(sp); got != n {
			t.Errorf("%s reaches %d, want %d", key, got, n)
		}
	}
}

// I-CALLA-31: a Cure Wounds sent with no target whose description named the caster, an ally and an NPC guard healed both characters
// and spent the slot. A cast aimed at more creatures than the spell reaches is refused before anything is spent, for the description's
// names and the request's targets alike; a description that names only creatures the server doesn't track stays the GM's.
func TestPostgresHealAimedAtMoreThanTheSpellReaches(t *testing.T) {
	testPostgres(t)
	_, toks, _, chars, _ := pgFightWith(t, "heal aim", 3)
	tok, healer, ally, other := toks[0], chars[0], chars[1], chars[2]
	pgExec(t, `UPDATE characters SET class = 'cleric', subclass = NULL, level = 9, wis = 18, spell_slots_used = '{}',
		known_spells = '["sacred-flame"]', prepared_spells = '["cure-wounds","mass-healing-word"]' WHERE id = $1`, healer)
	pgExec(t, `UPDATE characters SET hp = 5, max_hp = 60 WHERE id IN ($1, $2)`, ally, other)
	name := func(id int) string {
		var n string
		db.QueryRow(`SELECT name FROM characters WHERE id = $1`, id).Scan(&n)
		return n
	}
	hp := func(id int) int {
		var n int
		db.QueryRow(`SELECT hp FROM characters WHERE id = $1`, id).Scan(&n)
		return n
	}
	slots := func() string {
		var s string
		db.QueryRow(`SELECT COALESCE(spell_slots_used::text,'') FROM characters WHERE id = $1`, healer).Scan(&s)
		return s
	}
	reset := func() {
		pgExec(t, `UPDATE characters SET action_used = false, bonus_action_used = false, slot_spent_turn = NULL WHERE id = $1`, healer)
	}

	was := slots()
	desc := fmt.Sprintf("I cast Cure Wounds at level 2 on the wounded guard while %s and %s hold the gate", name(healer), name(ally))
	c, j := pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "cast", "spell_slug": "cure-wounds", "slot_level": 2, "description": desc})
	if c != 409 || !strings.Contains(fmt.Sprint(j), "reaches one creature") || slots() != was || hp(ally) != 5 {
		t.Fatalf("two characters named for Cure Wounds: %d %v (slots %s, ally %d HP)", c, j, slots(), hp(ally))
	}
	reset()
	c, j = pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "cast", "spell_slug": "cure-wounds", "slot_level": 1, "target_ids": []int{ally, other}})
	if c != 409 || hp(ally) != 5 || hp(other) != 5 {
		t.Errorf("two targets for Cure Wounds: %d %v", c, j)
	}
	reset()
	c, j = pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "cast", "spell_slug": "cure-wounds", "slot_level": 1, "description": "I cast Cure Wounds on the wounded guard"})
	if c != 200 || !strings.Contains(fmt.Sprint(j), "the GM applies it") || hp(ally) != 5 {
		t.Errorf("an NPC only: the GM's: %d %v", c, j)
	}
	reset()
	c, j = pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "cast", "spell_slug": "cure-wounds", "slot_level": 1, "target_character_id": ally})
	if c != 200 || hp(ally) <= 5 || hp(other) != 5 {
		t.Errorf("one ally named: %d %v", c, j)
	}
	reset()
	c, j = pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "cast", "spell_slug": "mass-healing-word", "slot_level": 3, "target_ids": []int{ally, other}})
	if c != 200 || hp(other) <= 5 {
		t.Errorf("Mass Healing Word reaches six: %d %v", c, j)
	}
}

// Divine Spark says what it rolled, so a heal its ally's maximum caps doesn't read as a flat amount (I-CALLA-31).
func TestPostgresDivineSparkShowsItsRoll(t *testing.T) {
	testPostgres(t)
	_, _, _, chars, _ := pgFightWith(t, "spark roll", 2)
	cleric, ally := chars[0], chars[1]
	pgExec(t, `UPDATE characters SET class = 'cleric', level = 9, wis = 16, class_resources_used = '{}' WHERE id = $1`, cleric)
	pgExec(t, `UPDATE characters SET hp = 58, max_hp = 60 WHERE id = $1`, ally)
	aimedTargets.Store(cleric, ally)
	defer aimedTargets.Delete(cleric)
	if line := divineSpark(cleric, "heal"); !strings.Contains(line, "2d8 [") || !strings.Contains(line, "+3 = ") || !strings.Contains(line, "regains 2 Hit Points") || !strings.Contains(line, "up to its Hit Point maximum") {
		t.Errorf("%s", line)
	}
}
