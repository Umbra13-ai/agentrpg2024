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

// A list ends where its sentence or line does, so the last spell of the last list is still a spell (and counted) when more text follows, a list
// ends at a semicolon without leaving it on the name, and a use that carries the entry's own qualifier is the same spell.
func TestSpellListEndsWithItsSentence(t *testing.T) {
	for _, c := range []struct {
		text, spell string
		want        string
	}{
		{"The witch casts: At will: light 3/Day Each: dispel magic, fly, polymorph. The witch can also use a power twice.", "Polymorph", "spell:polymorph"},
		{"The witch casts: 3/day each: charm person, detect magic, sleep\nShe wakes at dawn and regains her spells.", "Sleep", "spell:sleep"},
		{"The witch casts: 1/Day Each: web, fly; and she is wise and quite learned in all matters of the arcane arts.", "Fly", "spell:fly"},
		{"At Will: dancing lights, minor illusion; 1/day each: summon fiend (demon only)", "Summon Fiend (demon only)", "spell:summon fiend"},
		{"At Will: dancing lights, minor illusion; 1/day each: summon fiend (demon only)", "summon fiend", "spell:summon fiend"},
	} {
		if k, l := monsterDayUse(srdMonsterEntry{Name: "Spellcasting", Text: c.text}, c.spell); k != c.want || l == 0 {
			t.Errorf("%q in %q: key %q limit %d, want %q", c.spell, c.text, k, l, c.want)
		}
	}
	for _, tr := range spellTiersOf("At Will: dancing lights, mage hand, minor illusion; 1/day each: summon fiend (demon only)") {
		for _, sp := range tr.Spells {
			if strings.ContainsAny(sp, ";.") {
				t.Errorf("a spell name with punctuation left on it: %q", sp)
			}
		}
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
	// a cast that names no target is asked for them and spends nothing: the same use is cast once the target is named
	if res := cast("Tasha's Hideous Laughter", ""); !strings.Contains(res, "name the targets") {
		t.Errorf("a cast with no target: %s", res)
	}
	// a spell under its wizard's name resolves as the SRD's spell
	if res := cast("Otto's irresistible dance", fmt.Sprint(hero)); !strings.Contains(res, "Irresistible Dance") || strings.Contains(res, "the GM resolves it)") {
		t.Errorf("Otto's irresistible dance: %s", res)
	}
	if res := cast("Tasha's Hideous Laughter", fmt.Sprint(hero)); !strings.Contains(res, "Hideous Laughter") || strings.Contains(res, "the GM resolves it)") || strings.Contains(res, "Nothing was rolled") {
		t.Errorf("Tasha's Hideous Laughter: %s", res)
	}
	// a cast the GM says aims at a willing target rolls no save for a spell that has a save and no damage; a spell that deals damage still does
	willing := func(spell, target string) string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Test Wording Witch", "action": "Spellcasting", "use": spell, "target": target, "willing": true}})
		return fmt.Sprint(r["monster_action_result"], r["message"], r["error"])
	}
	if res := willing("Polymorph", fmt.Sprint(hero)); !strings.Contains(res, "willing target: no saving throw is rolled") {
		t.Errorf("Polymorph on a willing target: %s", res)
	}
	// a cast on itself rolls no save against its own spell
	if res := cast("Polymorph", fmt.Sprint(witch)); strings.Contains(strings.ToLower(res), "saving throw") {
		t.Errorf("Polymorph on itself rolled a save: %s", res)
	}
}

// TestDayLimitAfterAClause (I-ALDENQA-34): a use limit that follows a clause of its own in an entry's name ("Moon Ray (Gleam Only; 3/Day)")
// is counted like a plain one, by the one day-use owner every route asks.
func TestDayLimitAfterAClause(t *testing.T) {
	for _, c := range []struct {
		name, key string
		limit     int
	}{
		{"Moon Ray (Gleam Only; 3/Day)", "action:moon ray", 3},
		{"Spores (1/Day)", "action:spores", 1},
		{"Sun Ray (Glister Only; 3/day)", "action:sun ray", 3},
		{"Bite (Bat or Vampire Form Only)", "", 0},
	} {
		if k, l := monsterDayUse(srdMonsterEntry{Name: c.name}, ""); k != c.key || l != c.limit {
			t.Errorf("%q: key %q limit %d, want %q %d", c.name, k, l, c.key, c.limit)
		}
	}
}

// TestPostgresEntryUnlessCondition (I-ALDENQA-27): an entry its creature can't use while it has a condition ("..., unless the test wyrm has the
// Blinded condition.") is refused before anything is rolled or spent while it has it, and runs once it doesn't.
func TestPostgresEntryUnlessCondition(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, wyrm := earlyRulesFixture(t, "Test Wyrm", []srdMonsterEntry{{Name: "Fire Line",
		Text: "Dexterity Saving Throw: DC 18, each creature in a 60-foot-long, 5-foot-wide Line, unless the test wyrm has the Blinded condition. Failure: 10 (3d6) Fire damage. Success: Half damage."}})
	var before, after int
	db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, hero).Scan(&before)
	applyCondition(camp, wyrm, "blinded", "test", 10, "", 0)
	c, line := narrateAs(t, gm, camp, wyrm, "Fire Line", fmt.Sprint(hero), "")
	db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, hero).Scan(&after)
	if c != 409 || !strings.Contains(line, "can't use Fire Line while it has the Blinded condition") || after != before {
		t.Errorf("a blinded wyrm's Fire Line: %d (hp %d -> %d) %s", c, before, after, line)
	}
	removeConditionsWhere(camp, wyrm, func(r conditionRecord) bool { return r.Condition == "blinded" })
	if c, line := narrateAs(t, gm, camp, wyrm, "Fire Line", fmt.Sprint(hero), ""); c != 200 || !strings.Contains(line, "saving throw") {
		t.Errorf("the wyrm's Fire Line once it sees: %d %s", c, line)
	}
	// the older books' words: "Unless it is blinded, ..." (a rules condition only: "unless it is in water" is no gate)
	if got := entryConditionGate(camp, wyrm, "Unless it is blinded, the test wyrm breathes fire. Dexterity Saving Throw: DC 18."); got != "" {
		t.Errorf("a gate with the wyrm not blinded: %q", got)
	}
	applyCondition(camp, wyrm, "blinded", "test", 10, "", 0)
	if got := entryConditionGate(camp, wyrm, "Unless it is blinded, the test wyrm breathes fire. Dexterity Saving Throw: DC 18."); got != "blinded" {
		t.Errorf("'Unless it is blinded' with the wyrm blinded: %q", got)
	}
	if got := entryConditionGate(camp, wyrm, "Unless it is submerged, the test wyrm breathes fire."); got != "" {
		t.Errorf("a gate on a word that is no condition: %q", got)
	}
}

// TestCanRepeatTheSavingThrow (I-ALDENQA-28): "A creature can repeat the saving throw at the end of each of its turns, ending the effect on
// itself on a success." (older books' words) is the repeated save, never the GM's text.
func TestCanRepeatTheSavingThrow(t *testing.T) {
	e, ok := parseSaveEffect("Constitution Saving Throw: DC 10, each creature in a 15-foot Cone. Failure: The target has the Blinded condition for 1 minute. A creature can repeat the saving throw at the end of each of its turns, ending the effect on itself on a success.")
	if !ok || !e.EndSave || strings.Contains(e.Extra, "repeat the saving throw") {
		t.Errorf("the repeated save: ok %v, EndSave %v, GM text %q", ok, e.EndSave, e.Extra)
	}
}

// TestPostgresBlurAndEvasionInTheirWords (I-ALDENQA-5, -6): a trait that gives attacks against its creature Disadvantage in the 2024 words
// ("have Disadvantage unless ... Incapacitated ... or its speed is 0") holds while it moves freely and not at Speed 0 or Incapacitated; a
// trait in Evasion's words under another name (Avoidance) is Evasion for any saving throw when its words name none.
func TestPostgresBlurAndEvasionInTheirWords(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, _ := earlyRulesFixture(t, "Test Blur Host", nil)
	m := inventedMonster("Test Blur", "Fey", []srdMonsterEntry{
		{Name: "Blurred Movement", Text: "Attack rolls against the test blur have Disadvantage unless the test blur has the Incapacitated condition or its speed is 0."},
		{Name: "Avoidance", Text: "If the test blur is subjected to an effect that allows it to make a saving throw to take only half damage, it instead takes no damage if it succeeds on the saving throw and only half damage if it fails."}}, nil)
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented blur fixture", "monsters": []srdMonster2024{m}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key = $1`, key) })
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Test Blur", "monster_key": key, "initiative": 3}}})
	blur := monsterIDsByKeyOrName(camp, "Test Blur")[0]
	conds := func() []string { return creatureConditions(camp, blur) }
	if got := attackedWithDisadvantage(camp, blur, conds()); got != "Blurred Movement" {
		t.Errorf("free to move: %q, want Blurred Movement", got)
	}
	applyCondition(camp, blur, "restrained", "test", 10, "", 0)
	if got := attackedWithDisadvantage(camp, blur, conds()); got != "" {
		t.Errorf("Restrained (Speed 0): %q, want none", got)
	}
	removeConditionsWhere(camp, blur, func(r conditionRecord) bool { return true })
	applyCondition(camp, blur, "stunned", "test", 10, "", 0)
	if got := attackedWithDisadvantage(camp, blur, conds()); got != "" {
		t.Errorf("Stunned (Incapacitated): %q, want none", got)
	}
	for _, ab := range []string{"dex", "con", "wis"} {
		if !creatureEvasion(camp, blur, ab) {
			t.Errorf("Avoidance is not Evasion for a %s save", ab)
		}
	}
}
