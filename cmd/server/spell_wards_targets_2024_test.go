package main

import (
	"fmt"
	"strings"
	"testing"
)

// FUNC-03: a self-cast Invisibility or Sanctuary keeps the ward the cast made; the creature's next offense ends it.
// FUNC-10: a creature named through both target_id and target_ids is healed once.
func TestPostgresSpellWardsAndTargetSelectors(t *testing.T) {
	testPostgres(t)
	_, toks, _, chars, _ := pgFightWith(t, "wardsel", 2)
	tok, char, friend := toks[0], chars[0], chars[1]
	cast := func(slug string, body map[string]interface{}) string {
		t.Helper()
		pgExec(t, `UPDATE characters SET action_used = false, bonus_action_used = false, attacks_remaining = NULL, slot_spent_turn = '', spell_slots_used = '{}'::jsonb WHERE id = $1`, char)
		body["action"], body["spell_slug"] = "cast", slug
		c, j := pgCall(t, "POST", "/api/action", tok, body)
		if c != 200 {
			t.Fatalf("cast %s: %d %v", slug, c, j)
		}
		return fmt.Sprint(j["result"]) + fmt.Sprint(j["message"])
	}
	pgExec(t, `UPDATE characters SET class = 'cleric', subclass = NULL, level = 11, wis = 20, hp = max_hp, concentrating_on = NULL,
		known_spells = '["invisibility","sanctuary","mass-healing-word"]'::jsonb, prepared_spells = '["invisibility","sanctuary","mass-healing-word"]'::jsonb,
		inventory = '[{"id":"i1","name":"Holy Symbol","type":"focus","spellcasting_focus":true},{"id":"i2","name":"Component Pouch","type":"focus","spellcasting_focus":true}]'::jsonb WHERE id = $1`, char)

	// Sanctuary on oneself: the ward is there after the cast (the cast is not the offense that ends it)
	res := cast("sanctuary", map[string]interface{}{"target_id": char})
	if strings.Contains(res, "Sanctuary ends") {
		t.Errorf("the cast that made Sanctuary ended it: %q", res)
	}
	if _, ok := buffEffect(lobbyOfChar(char), char, "sanctuary"); !ok {
		t.Error("no Sanctuary after the cast")
	}
	// the next cast is an offense: it ends the ward it held
	res = cast("mass-healing-word", map[string]interface{}{"target_id": friend})
	if !strings.Contains(res, "Sanctuary ends") {
		t.Errorf("a later cast did not end Sanctuary: %q", res)
	}

	// Mass Healing Word naming one creature through both selectors heals it once: one "regains" line for it
	var name string
	db.QueryRow(`SELECT name FROM characters WHERE id = $1`, friend).Scan(&name)
	pgExec(t, `UPDATE characters SET hp = 1 WHERE id = $1`, friend)
	res = cast("mass-healing-word", map[string]interface{}{"target_id": friend, "target_ids": []int{char, friend}})
	if n := strings.Count(res, name+" regains"); n != 1 {
		t.Errorf("%s healed %d times by one cast: %q", name, n, res)
	}
}
