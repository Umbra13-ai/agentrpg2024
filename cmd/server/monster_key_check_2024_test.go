package main

// A monster key that names nothing (a guessed "srd:goblin") is refused before anything is added, never a creature with stand-in stats; and a spell
// a Spellcasting entry lists that the server has no data for (outside the SRD) is cast by its listed name, handed to the GM and counted.

import (
	"fmt"
	"strings"
	"testing"
)

func TestPostgresUnknownMonsterKeyIsRefused(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, _ := earlyRulesFixture(t, "Test Key Check", nil)
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	before := len(combatEntries(camp))
	for _, key := range []string{"srd:goblin", "goblin warrior!", "custom:does-not-exist"} {
		c, j := pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Key QA", "monster_key": key, "initiative": 5}}})
		if c != 404 || fmt.Sprint(j["error"]) != "monster_not_found" || len(combatEntries(camp)) != before {
			t.Errorf("monster_key %q: %d %v (entries %d -> %d)", key, c, j, before, len(combatEntries(camp)))
		}
	}
	if c, j := pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Key QA", "monster_key": "goblin-warrior", "initiative": 5}}}); c != 200 || len(combatEntries(camp)) != before+1 {
		t.Errorf("a real slug: %d %v", c, j)
	}
	// a combatant with no key at all is the GM's own (its Hit Points and Armor Class are its own words)
	if c, j := pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Plain QA", "hp": 30, "ac": 12, "initiative": 4}}}); c != 200 {
		t.Errorf("a keyless combatant: %d %v", c, j)
	}
}

func TestPostgresListedSpellWithoutDataIsCastByName(t *testing.T) {
	testPostgres(t)
	if _, ok := srdSpellsMemory["friends"]; ok {
		t.Skip("Friends has data now: pick a spell outside the SRD")
	}
	gm, _, camp, hero, id := earlyRulesFixture(t, "Test Listed Spell", []srdMonsterEntry{
		{Name: "Spellcasting", Text: "The kenku casts one of the following spells, using Wisdom as the spellcasting ability (spell save DC 12): At Will: Mage Hand 1/day each: Friends, Disguise Self"},
	})
	cast := func(spell string) string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", id), "action": "Spellcasting", "use": spell, "target": fmt.Sprint(hero)}})
		return fmt.Sprint(r["monster_action_result"], r["message"], r["error"])
	}
	for _, spell := range []string{"Friends", "friends"} {
		res := cast(spell)
		if strings.Contains(res, "isn't one of them") || !strings.Contains(strings.ToLower(res), "friends") {
			t.Fatalf("%s: %s", spell, res)
		}
		if spell == "Friends" && !strings.Contains(res, "Day use counted") {
			t.Errorf("the per-day use of Friends was not counted: %s", res)
		}
		if spell == "friends" && !strings.Contains(res, "already used") {
			t.Errorf("a second Friends under 1/day each: %s", res)
		}
	}
	if res := cast("Zorro"); !strings.Contains(res, "isn't one of them") {
		t.Errorf("a spell the entry doesn't list: %s", res)
	}
}
