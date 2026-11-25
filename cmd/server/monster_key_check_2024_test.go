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

// A monster takes one Bonus Action in a turn (monster_bonus_action_2024.go): the second is refused before anything is rolled, a refused or blocked
// try spends none, the next turn gives it back, and the GM can say this monster's stat block gives it more.
func TestPostgresMonsterBonusActionOnceATurn(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, id := earlyRulesFixture(t, "Test Bonus Knave", nil)
	// the fixture's monster has only actions; give it two Bonus Actions through an imported monster
	knave := inventedMonster("Test Two Bonus", "Humanoid", nil, []srdMonsterEntry{{Name: "Poke", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 3 (1d6) Piercing damage."}})
	knave.BonusActions = []srdMonsterEntry{
		{Name: "Quick Jab", Text: "Dexterity Saving Throw: DC 40, one creature within 5 feet. Failure: 2 (1d4) Piercing damage. Success: Half damage."},
		{Name: "Shadow Step", Text: "The creature moves up to 20 feet to an unoccupied space it can see."},
	}
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"campaign_id": camp, "source_note": "invented bonus action fixture", "monsters": []srdMonster2024{knave}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key = $1`, key) })
	_ = id
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Test Two Bonus", "monster_key": key, "initiative": 30}}})
	mid := monsterIDsByKeyOrName(camp, "Test Two Bonus")[0]
	act := func(action string, extra map[string]interface{}) (int, string) {
		ma := map[string]interface{}{"monster": "Test Two Bonus", "action": action, "target": fmt.Sprint(hero), "confirm_targeting": true}
		for k, v := range extra {
			ma[k] = v
		}
		code, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": ma})
		return code, fmt.Sprint(r["monster_action_result"], r["message"], r["error"])
	}
	taken := func() int { return bonusActionsTaken(camp, mid) }
	// an ordinary Action never takes the Bonus Action
	if code, res := act("Poke", nil); code != 200 || taken() != 0 {
		t.Fatalf("an Action took a Bonus Action: %d %s (taken %d)", code, res, taken())
	}
	// a preview reads it and spends nothing
	if _, _ = act("Quick Jab", map[string]interface{}{"apply_damage": false}); taken() != 0 {
		t.Errorf("a preview spent the Bonus Action")
	}
	if code, res := act("Quick Jab", nil); code != 200 || taken() != 1 {
		t.Fatalf("the first Bonus Action: %d %s (taken %d)", code, res, taken())
	}
	if code, res := act("Shadow Step", nil); code != 409 || !strings.Contains(res, "already taken its Bonus Action") || taken() != 1 {
		t.Errorf("the second Bonus Action of the turn: %d %s (taken %d)", code, res, taken())
	}
	// the GM says the stat block gives this monster two
	if code, j := pgCall(t, "POST", "/api/gm/monster-features", gm, map[string]interface{}{"campaign_id": camp, "combatant_id": mid, "bonus_actions": 2}); code != 200 {
		t.Fatalf("bonus_actions: %d %v", code, j)
	}
	if code, res := act("Shadow Step", nil); code != 200 || taken() != 2 {
		t.Errorf("the second Bonus Action once the GM allowed two: %d %s (taken %d)", code, res, taken())
	}
	if code, res := act("Quick Jab", nil); code != 409 || taken() != 2 {
		t.Errorf("a third: %d %s (taken %d)", code, res, taken())
	}
	if code, _ := pgCall(t, "POST", "/api/gm/monster-features", gm, map[string]interface{}{"campaign_id": camp, "combatant_id": mid, "bonus_actions": 9}); code != 400 {
		t.Errorf("bonus_actions 9: %d", code)
	}
	// its next turn gives them back
	resetBonusActions(camp, mid)
	if code, res := act("Quick Jab", nil); code != 200 || taken() != 1 {
		t.Errorf("after its turn began: %d %s (taken %d)", code, res, taken())
	}
}

// newMonsterTurn: a test that sends several of one monster's Bonus Actions stands each for a turn of its own (the monster's next turn gives the
// Bonus Action back: resetBonusActions).
func newMonsterTurn(camp int) {
	if db != nil {
		db.Exec(`UPDATE combat_monster_state SET bonus_used = 0 WHERE lobby_id = $1`, camp)
	}
}

// "The target is immune to this banshee's Horrify for 24 hours": the immunity is that one banshee's. A second banshee of the fight, and a banshee
// of the next fight (its combat id starts again from -1), is another creature and makes the target save again.
func TestPostgresSaveImmunityIsPerCreature(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, _ := earlyRulesFixture(t, "Test Immunity Host", nil)
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	horrify := inventedMonster("Test Banshee", "Undead", nil, []srdMonsterEntry{{Name: "Horrify", Text: "Wisdom Saving Throw: DC 1, one creature within 60 feet. Success: The target is immune to this banshee's Horrify for 24 hours. Failure: The target has the Frightened condition for 1 minute."}})
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"campaign_id": camp, "source_note": "invented immunity fixture", "monsters": []srdMonster2024{horrify}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key = $1`, key) })
	add := func(name string, init int) {
		pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": name, "monster_key": key, "initiative": init}}})
	}
	horrifies := func(name string) string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": name, "action": "Horrify", "target": fmt.Sprint(hero), "confirm_targeting": true}})
		return fmt.Sprint(r["monster_action_result"], r["message"])
	}
	add("Banshee A", 30)
	add("Banshee B", 29)
	// the target succeeds against A's Horrify (a DC of 1: only a natural 1 fails; send it again until the save holds)
	saved := false
	for try := 0; try < 40 && !saved; try++ {
		db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND kind = 'effect_immune'`, camp)
		res := horrifies("Banshee A")
		saved = strings.Contains(res, "succeeds")
	}
	if !saved {
		t.Fatal("the target never succeeded against a DC 1")
	}
	if res := horrifies("Banshee A"); !strings.Contains(res, "immune") {
		t.Fatalf("the same banshee's second Horrify: %s", res)
	}
	if res := horrifies("Banshee B"); strings.Contains(res, "every target is immune") {
		t.Errorf("another banshee of the same fight: %s", res)
	}
	// the next fight: the ids start again from -1, and the immunity is gone with the monsters
	pgCall(t, "POST", cp+"/combat/end", gm, nil)
	pgCall(t, "POST", cp+"/combat/start", gm, nil)
	add("Banshee C", 30)
	if res := horrifies("Banshee C"); strings.Contains(res, "every target is immune") {
		t.Errorf("a banshee of the next fight: %s", res)
	}
}
