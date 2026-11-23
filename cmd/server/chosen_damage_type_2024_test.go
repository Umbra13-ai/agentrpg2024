package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A spell whose damage is of a type the caster chooses is typed by the caster's words and refused without them (FUNC-07).
func TestPostgresChosenDamageTypeOverTheCatalog(t *testing.T) {
	testPostgres(t)
	var found []string
	for slug, sp := range srdSpellsMemory {
		offered := spellOfferedTypes(sp)
		if len(offered) == 0 {
			continue
		}
		found = append(found, slug)
		for _, ty := range offered {
			got, why := withChosenType(sp, "Cast "+sp.Name+" choosing "+strings.ToUpper(ty[:1])+ty[1:]+" damage")
			if why != "" || got.DamageType != ty {
				t.Errorf("%s choosing %s: type %q, refusal %q", slug, ty, got.DamageType, why)
			}
		}
		if _, why := withChosenType(sp, "Cast "+sp.Name+" at the dragon"); !strings.HasPrefix(why, "Cannot cast") || !strings.Contains(why, offered[0]) {
			t.Errorf("%s with no type named: %q", slug, why)
		}
		// a type the spell does not offer is not a choice
		if _, why := withChosenType(sp, "Cast "+sp.Name+" choosing necrotic damage"); why == "" {
			t.Errorf("%s accepted a type it does not offer", slug)
		}
	}
	sort.Strings(found)
	if strings.Join(found, ",") != "chromatic-orb,sorcerous-burst" {
		t.Errorf("the spells that offer a type of damage: %v", found)
	}
	if got := spellOfferedTypes(srdSpellsMemory["chromatic-orb"]); strings.Join(got, ",") != "acid,cold,fire,lightning,poison,thunder" {
		t.Errorf("Chromatic Orb offers %v", got)
	}
	if got := spellOfferedTypes(srdSpellsMemory["sorcerous-burst"]); len(got) != 7 {
		t.Errorf("Sorcerous Burst offers %v", got)
	}
	// a spell with a fixed type is untouched
	if sp, why := withChosenType(srdSpellsMemory["fire-bolt"], "Cast Fire Bolt"); why != "" || sp.DamageType != srdSpellsMemory["fire-bolt"].DamageType {
		t.Errorf("fire bolt: %q %q", sp.DamageType, why)
	}
}

// Through POST /api/action: a Sorcerous Burst with no type named is refused before anything is spent; naming one deals damage of that type.
func TestPostgresChosenDamageTypeOnTheCast(t *testing.T) {
	testPostgres(t)
	_, toks, camp, chars, mons := pgFightWith(t, "chosentype", 1)
	tok, char, target := toks[0], chars[0], mons[0]
	_ = camp
	reset := func() {
		pgExec(t, `UPDATE characters SET class = 'sorcerer', level = 5, cha = 20, hp = max_hp, action_used = false, bonus_action_used = false, attacks_remaining = NULL,
			slot_spent_turn = '', spell_slots_used = '{}'::jsonb, known_spells = '["sorcerous-burst"]'::jsonb, prepared_spells = '["sorcerous-burst"]'::jsonb WHERE id = $1`, char)
	}
	cast := func(desc string) (int, string) {
		reset()
		c, j := pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "cast", "spell_slug": "sorcerous-burst", "target_id": target, "description": desc})
		return c, fmt.Sprint(j["result"]) + fmt.Sprint(j["message"]) + fmt.Sprint(j["error"])
	}
	c, res := cast("Cast Sorcerous Burst at the enemy")
	if !strings.Contains(res, "Cannot cast Sorcerous Burst: name the damage type") || !strings.Contains(res, "cold") {
		t.Errorf("no type named: %d %q", c, res)
	}
	var used bool
	db.QueryRow(`SELECT action_used FROM characters WHERE id = $1`, char).Scan(&used)
	if used {
		t.Error("a refused cast spent the action")
	}
	typed := false
	for try := 0; try < 8 && !typed; try++ {
		_, res = cast("Cast Sorcerous Burst choosing Cold damage")
		typed = regexp.MustCompile(`HIT, \d+ cold damage`).MatchString(res)
	}
	if !typed {
		t.Errorf("no hit of cold damage in 8 casts, last: %q", res)
	}
	if strings.Contains(res, "HIT, ") && regexp.MustCompile(`HIT, \d+ damage`).MatchString(res) {
		t.Errorf("untyped damage: %q", res)
	}
}
