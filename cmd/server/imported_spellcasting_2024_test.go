package main

// A Spellcasting entry as an imported stat block words it (Witchlight folder reports, Nov 24): its per-day lists in any letter case, a spell
// by the wizard's name a stat block puts in front of it, a failed save against Web that deals no damage, and a cast on the caster itself that
// rolls no save against its own spell.

import (
	"fmt"
	"strings"
	"testing"
)

func TestImportedSpellcastingWordings(t *testing.T) {
	// the warning at import: a per-day list the server can't read is named, one it reads is not
	if got := unreadDayLists("At will: light. 1/day each: web, fly; 2/Day Each: sleep"); len(got) != 0 {
		t.Errorf("lists the server reads (any case) are reported unread: %v", got)
	}
	if got := unreadDayLists("At will: light. 3e/day each: polymorph, fly"); len(got) != 1 || !strings.Contains(got[0], "3e/day") {
		t.Errorf("an unreadable list heading: %v", got)
	}
	if tiers := spellTiersOf("At will: light. 1/day each: Web, Fly; 2/day each: Sleep"); len(tiers) != 3 || tiers[1].Limit != 1 || !tiers[1].Each || tiers[2].Limit != 2 {
		t.Errorf("lowercase heads: %+v", tiers)
	}
}

func TestPostgresImportedSpellcastingOnTheRoute(t *testing.T) {
	testPostgres(t)
	for in, want := range map[string]string{"Tasha's hideous laughter": "hideous-laughter", "Otto’s irresistible dance": "irresistible-dance", "Hideous Laughter": "hideous-laughter", "Fireball": "fireball"} {
		if got, ok := lookupSpell(in); !ok || got != want {
			t.Errorf("lookupSpell(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if _, ok := lookupSpell("Zorro's imaginary spell"); ok {
		t.Error("an unknown spell was found")
	}
	if !castDealsNoDamage("web") {
		t.Error("Web's cast deals damage: its fire damage is dealt when the webs burn")
	}
	if castDealsNoDamage("fireball") {
		t.Error("Fireball deals no damage on cast")
	}
	gm, _, camp, hero, _ := earlyRulesFixture(t, "Test Spell Wording Host", nil)
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000 WHERE id=$1`, hero)
	caster := inventedMonster("Test Wording Witch", "Humanoid", nil, []srdMonsterEntry{{Name: "Spellcasting",
		Text: "The witch casts one of the following spells, using Intelligence as the spellcasting ability (spell save DC 40): At Will: light 1/day each: web, Otto's irresistible dance, Tasha's hideous laughter 3/day each: polymorph"}})
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"campaign_id": camp, "source_note": "invented spell wording fixture", "monsters": []srdMonster2024{caster}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key = $1`, key) })
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Test Wording Witch", "monster_key": key, "initiative": 30}}})
	witch := monsterIDsByKeyOrName(camp, "Test Wording Witch")[0]
	cast := func(spell, target string) string {
		t.Helper()
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Test Wording Witch", "action": "Spellcasting", "use": spell, "target": target}})
		return fmt.Sprint(r["monster_action_result"], r["message"], r["error"])
	}
	// "1/day each": Web cast once, its failed save restrains and deals no fire damage; a second Web is refused
	var before int
	db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, hero).Scan(&before)
	res := cast("Web", fmt.Sprint(hero))
	var after int
	db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, hero).Scan(&after)
	if !strings.Contains(res, "Web") || strings.Contains(strings.ToLower(res), "fire damage") || after != before {
		t.Errorf("Web: %s (hp %d -> %d)", res, before, after)
	}
	if again := cast("Web", fmt.Sprint(hero)); !strings.Contains(again, "Nothing was rolled") {
		t.Errorf("a second Web under 1/day each: %s", again)
	}
	// a spell under its wizard's name resolves as the SRD's spell
	if res := cast("Otto's irresistible dance", fmt.Sprint(hero)); !strings.Contains(res, "Irresistible Dance") || strings.Contains(res, "the GM resolves it)") {
		t.Errorf("Otto's irresistible dance: %s", res)
	}
	if res := cast("Tasha's Hideous Laughter", fmt.Sprint(hero)); !strings.Contains(res, "Hideous Laughter") || strings.Contains(res, "the GM resolves it)") {
		t.Errorf("Tasha's Hideous Laughter: %s", res)
	}
	// a cast on itself rolls no save against its own spell
	if res := cast("Polymorph", fmt.Sprint(witch)); strings.Contains(strings.ToLower(res), "saving throw") {
		t.Errorf("Polymorph on itself rolled a save: %s", res)
	}
}
