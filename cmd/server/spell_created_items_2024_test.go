package main

import (
	"fmt"
	"strings"
	"testing"
)

// Goodberry (SRD 5.2.1): the cast puts ten berries in the caster's inventory; eating one is a Bonus Action that restores 1 Hit Point; the
// berries disappear when the spell ends (24 hours of story time, the GM's pass-time). spell_created_items_2024.go.
func TestPostgresGoodberryMakesBerries(t *testing.T) {
	testPostgres(t)
	gm, toks, camp, chars, _ := pgFightWith(t, "goodberry", 1)
	char := chars[0]
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/end", camp), gm, nil)
	pgExec(t, `UPDATE characters SET class = 'druid', subclass = NULL, level = 3, wis = 16, known_spells = '["goodberry"]'::jsonb,
		prepared_spells = '["goodberry"]'::jsonb, inventory = $2::jsonb, hp = max_hp - 5 WHERE id = $1`, char, focusInventory)
	berries := func() int {
		n := 0
		for _, it := range inventoryOf(char) {
			if itemStr(it, "spell_item") == "goodberry" {
				n += itemQty(it)
			}
		}
		return n
	}
	c, j := pgCall(t, "POST", "/api/action", toks[0], map[string]interface{}{"action": "cast", "spell_slug": "goodberry", "character_id": char})
	if c != 200 || berries() != 10 || !strings.Contains(fmt.Sprint(j["result"]), "Goodberries are in your inventory") {
		t.Fatalf("the cast makes ten berries: %d, %d berries: %v", c, berries(), j["result"])
	}
	var before, after int
	db.QueryRow(`SELECT hp FROM characters WHERE id = $1`, char).Scan(&before)
	pgCall(t, "POST", fmt.Sprintf("/api/gm/narrate"), gm, map[string]interface{}{"campaign_id": camp, "narration": "A new beat."})
	c, j = pgCall(t, "POST", "/api/action", toks[0], map[string]interface{}{"action": "use_item", "item": "Goodberry", "character_id": char})
	db.QueryRow(`SELECT hp FROM characters WHERE id = $1`, char).Scan(&after)
	if c != 200 || after != before+1 || berries() != 9 {
		t.Fatalf("eating a berry: %d, Hit Points %d to %d, %d berries left: %v", c, before, after, berries(), j)
	}
	// 24 hours of story time end the spell: the berries are gone
	if c, j := pgCall(t, "POST", "/api/gm/pass-time", gm, map[string]interface{}{"campaign_id": camp, "minutes": 24 * 60}); c != 200 {
		t.Fatalf("pass time: %d %v", c, j)
	}
	if berries() != 0 {
		t.Errorf("the spell ended but %d berries are still in the inventory", berries())
	}
}
