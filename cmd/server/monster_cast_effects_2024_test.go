package main

import (
	"fmt"
	"strings"
	"testing"
)

// A monster's cast of a spell that is neither a spell attack nor a saving throw has the effect a character's cast has, through the
// same owners (monster_cast_effects_2024.go), at every kind of target: Invisibility on itself (ended by its attack roll and by the end
// of its Concentration), Greater Invisibility (not ended by an attack), Bless, Shield of Faith and Sanctuary (spell_effects read by
// the attack, save and Armor Class owners), Healing Word at a character, a monster and a companion (each one's Hit Point owner);
// "casts the Sleep spell" is cast; an entry that offers several spells waits for the GM's use; a utility spell is the GM's, named,
// with its Concentration started and its daily use left for the GM to count. The stat block wording is invented, with the published
// pattern.
func TestPostgresMonsterCastEffects(t *testing.T) {
	testPostgres(t)
	gm, player, camp, charID, petID, beastID := companionReactionFixture(t)
	pet := companionIDBase + petID
	m := compendiumFixture()
	m.Name, m.Type, m.CR, m.XP, m.PB, m.Traits, m.Legendary, m.LegendaryIn, m.DmgImmune, m.Resistances = "Glimmer Sprite", "Fey", "1", 200, 2, nil, nil, "", "", ""
	m.Abilities["cha"] = abilityLine{Score: 16, Mod: 3, Save: 3}
	m.Actions = []srdMonsterEntry{
		{Name: "Claw", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 1 (1d1) Slashing damage."},
		{Name: "Fade", Text: "The sprite casts Invisibility on itself, requiring no spell components and using Charisma as the spellcasting ability."},
		{Name: "Veil", Text: "The sprite casts Greater Invisibility on itself, requiring no spell components and using Charisma as the spellcasting ability."},
		{Name: "Dream Dust (1/Day)", Text: "The sprite casts the Sleep spell, requiring no spell components and using Charisma as the spellcasting ability (spell save DC 10)."},
		{Name: "Spellcasting", Text: "The sprite casts one of the following spells, requiring no spell components and using Charisma as the spellcasting ability (spell save DC 12): At Will: Light 1/Day Each: Fog Cloud, Animal Friendship"},
	}
	m.BonusActions = []srdMonsterEntry{
		{Name: "Small Blessings (1/Day)", Text: "The sprite casts Bless, Healing Word, or Sanctuary, using the same spellcasting ability as Spellcasting."},
		{Name: "Ward of Faith (2/Day)", Text: "The sprite casts Shield of Faith, using Charisma as the spellcasting ability."},
	}
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented spellcasting fixture", "monsters": []srdMonster2024{m}})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	key := fmt.Sprint(imported["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	var add []map[string]interface{}
	for _, n := range []string{"Sprite A", "Sprite B", "Sprite C", "Sprite D", "Sprite E"} {
		add = append(add, map[string]interface{}{"name": n, "monster_key": key, "hp": 100, "ac": 1, "initiative": -900})
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": add}); c != 200 {
		t.Fatalf("add: %d %v", c, j)
	}
	id := func(name string) int { return monsterIDsByKeyOrName(camp, name)[0] }
	cast := func(who, action string, target interface{}, use string) string {
		t.Helper()
		ma := map[string]interface{}{"monster": who, "action": action}
		if target != nil {
			ma["target"] = fmt.Sprint(target)
		}
		if use != "" {
			ma["use"] = use
		}
		c, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": ma})
		if !narrateAnswered(c, r) {
			t.Fatalf("%s %s: %d %v", who, action, c, r)
		}
		return fmt.Sprint(r["monster_action_result"])
	}
	invisible := func(cid int) (bool, int) {
		for _, rec := range loadConditionRecords(camp, cid) {
			if rec.Condition == "invisible" {
				return true, rec.SourceID
			}
		}
		return false, 0
	}

	// Invisibility on itself: recorded with its caster, Concentration on it; its attack roll ends it; so does the end of its Concentration
	a := id("Sprite A")
	res := cast("Sprite A", "Fade", nil, "")
	if inv, src := invisible(a); !inv || src != a || monsterConcentrationOn(camp, a) != "Invisibility" {
		t.Fatalf("Fade: invisible %v (source %d), concentrating on %q: %s", inv, src, monsterConcentrationOn(camp, a), res)
	}
	res = cast("Sprite A", "Claw", charID, "")
	if inv, _ := invisible(a); inv || !strings.Contains(res, "Invisibility ends") {
		t.Errorf("the attack roll left the Invisibility: %v %s", inv, res)
	}
	cast("Sprite A", "Fade", nil, "")
	endMonsterConcentration(camp, a, "Sprite A", "test")
	if inv, _ := invisible(a); inv {
		t.Error("the end of the monster's Concentration left the Invisibility")
	}
	// Greater Invisibility: the attack roll doesn't end it
	b := id("Sprite B")
	cast("Sprite B", "Veil", nil, "")
	cast("Sprite B", "Claw", charID, "")
	if inv, _ := invisible(b); !inv {
		t.Error("Greater Invisibility ended with an attack roll")
	}

	// an entry that offers several spells: nothing is cast and no use is spent until the GM names one
	c := id("Sprite C")
	if res = cast("Sprite C", "Small Blessings (1/Day)", charID, ""); !strings.Contains(res, "casts one of Bless, Healing Word, Sanctuary") || !strings.Contains(res, "Nothing was cast") {
		t.Errorf("the choice was guessed: %s", res)
	}
	if d := loadDayUses(camp, c); len(d) != 0 {
		t.Errorf("an unnamed choice spent a use: %v", d)
	}
	// Healing Word at a character: 2d4 + its Charisma modifier through the character's Hit Point owner, the use counted, then refused
	pgExec(t, `UPDATE characters SET hp = 100, max_hp = 5000 WHERE id = $1`, charID)
	res = cast("Sprite C", "Small Blessings (1/Day)", charID, "Healing Word")
	var hp int
	db.QueryRow(`SELECT hp FROM characters WHERE id = $1`, charID).Scan(&hp)
	if hp < 105 || hp > 111 || !strings.Contains(res, "2d4+3") {
		t.Errorf("Healing Word at a character: hp %d: %s", hp, res)
	}
	if d := loadDayUses(camp, c); len(d) != 1 {
		t.Errorf("Healing Word's use wasn't counted: %v", d)
	}
	res = cast("Sprite C", "Small Blessings (1/Day)", charID, "Bless")
	var blessed int
	db.QueryRow(`SELECT COUNT(*) FROM spell_effects WHERE lobby_id = $1 AND caster_id = $2`, camp, c).Scan(&blessed)
	if blessed != 0 || strings.Contains(res, "Day use counted") {
		t.Errorf("the 1/Day entry was cast twice: %s", res)
	}
	// Healing Word at a monster and at a companion: each one's own Hit Point owner
	pgExec(t, `UPDATE companions SET hp = 10, max_hp = 500 WHERE id = $1`, petID)
	for _, tg := range []struct {
		who    string
		target interface{}
		hp     func() int
	}{
		{"Sprite D", "Opponent", func() int {
			for _, e := range combatEntries(camp) {
				if asInt(e["id"]) == beastID {
					return asInt(e["hp"])
				}
			}
			return -1
		}},
		{"Sprite E", "React Pet", func() int { p, _ := loadCompanion(petID); return p.HP }},
	} {
		if tg.who == "Sprite D" {
			if err := setMonsterHP(camp, beastID, 10); err != nil {
				t.Fatal(err)
			}
		}
		before := tg.hp()
		res = cast(tg.who, "Small Blessings (1/Day)", tg.target, "Healing Word")
		if after := tg.hp(); after < before+5 || after > before+11 {
			t.Errorf("Healing Word at %v: %d -> %d: %s", tg.target, before, after, res)
		}
	}
	_ = pet

	// Bless (from a fresh creature: the entry is 1/Day) on the character: the save and attack owners read it
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Sprite F", "monster_key": key, "hp": 100, "ac": 1, "initiative": -900}, {"name": "Sprite G", "monster_key": key, "hp": 100, "ac": 1, "initiative": -900}}})
	res = cast("Sprite F", "Small Blessings (1/Day)", charID, "Bless")
	if !hasSpellEffect(camp, charID, "bless") || monsterConcentrationOn(camp, id("Sprite F")) != "Bless" {
		t.Errorf("Bless: %s", res)
	}
	if adj, note := spellRollAdjust(camp, charID, "attack"); adj < 1 || !strings.Contains(note, "Bless") {
		t.Errorf("the attack owner doesn't read the monster's Bless: %d %q", adj, note)
	}
	// Sanctuary: "the same spellcasting ability as Spellcasting" takes the Spellcasting entry's DC 12; a DC no entry gives is
	// 8 + Proficiency Bonus 2 + Charisma 3; a monster that names no spellcasting ability has none
	res = cast("Sprite G", "Small Blessings (1/Day)", charID, "Sanctuary")
	if e, ok := buffEffect(camp, charID, "sanctuary"); !ok || e.Detail != "12" || !strings.Contains(res, "DC 12") {
		t.Errorf("Sanctuary: %v %+v: %s", ok, e, res)
	}
	if dc := monsterSpellDC(camp, id("Sprite G"), 0); dc != 13 {
		t.Errorf("the DC a stat block omits: %d", dc)
	}
	if dc := monsterSpellDC(camp, beastID, 0); dc != 0 {
		t.Errorf("a monster that names no spellcasting ability: DC %d", dc)
	}
	// Shield of Faith: +2 to the character's Armor Class as every attack reads it
	res = cast("Sprite D", "Ward of Faith (2/Day)", charID, "")
	if spellACAdjust(camp, charID) < 2 || !strings.Contains(res, "+2 bonus to AC") {
		t.Errorf("Shield of Faith: %d: %s", spellACAdjust(camp, charID), res)
	}
	if d := loadDayUses(camp, id("Sprite D")); d["action:ward of faith"] != 1 && len(d) == 0 {
		t.Errorf("Shield of Faith's use wasn't counted: %v", d)
	}

	// "casts the Sleep spell": the spell is cast (a saving throw is rolled), not left as text
	if res = cast("Sprite E", "Dream Dust (1/Day)", charID, ""); !strings.Contains(res, "Sleep") || !strings.Contains(strings.ToLower(res), "saving throw") {
		t.Errorf("Dream Dust: %s", res)
	}
	// Animal Friendship at a creature that isn't a Beast: no effect, said so
	if res = cast("Sprite E", "Spellcasting", charID, "Animal Friendship"); !strings.Contains(res, "isn't a Beast") {
		t.Errorf("Animal Friendship: %s", res)
	}
	// a utility spell is the GM's: named, its Concentration started, its daily use left for the GM to count
	res = cast("Sprite E", "Spellcasting", nil, "Fog Cloud")
	if !strings.Contains(res, "GM") || monsterConcentrationOn(camp, id("Sprite E")) != "Fog Cloud" {
		t.Errorf("Fog Cloud: concentrating on %q: %s", monsterConcentrationOn(camp, id("Sprite E")), res)
	}
	// a character's Shield of Faith reads the same lasting-effect table (one creature, 10 minutes)
	db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1`, camp)
	pgExec(t, `UPDATE characters SET class='cleric', level=1, wis=16, known_spells='["shield-of-faith"]', prepared_spells='["shield-of-faith"]', spell_slots_used='{}', bonus_action_used=false, action_used=false, concentrating_on=NULL, inventory=$2::jsonb WHERE id=$1`, charID, `[{"id":"i1","name":"Component Pouch","type":"focus","spellcasting_focus":true}]`)
	code, cr := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"action": "cast", "spell_slug": "shield-of-faith", "target_character_id": charID})
	var rounds int
	db.QueryRow(`SELECT COALESCE(MAX(rounds_left),0) FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND caster_id = $2 AND kind = 'shield_of_faith'`, camp, charID).Scan(&rounds)
	if rounds != 100 {
		t.Errorf("a character's Shield of Faith: %d rounds (%d %v)", rounds, code, cr)
	}
}

// The parts of a monster's cast that are read from text and tables: "casts the X spell" names the spell; a list of spells is a
// choice; Entangle and Web free a creature with a Strength (Athletics) check, not a repeated save; Animal Friendship charms a Beast
// for 24 hours; a character's Bless and Shield of Faith read the same lasting-effect table as a monster's.
func TestPostgresMonsterCastReading(t *testing.T) {
	testPostgres(t)
	savedSpells := srdSpellsMemory // other tests replace the shared spell cache: reload the real data, put the cache back afterwards
	t.Cleanup(func() { srdSpellsMemory = savedSpells })
	srdSpellsMemory = map[string]SRDSpell{}
	if err := loadSRDFromDB(); err != nil || srdSpellsMemory["healing-word"].Name == "" {
		t.Fatalf("reloading the spell data: %v", err)
	}
	if m := mfxSpellRe.FindStringSubmatch("The mephit casts the Sleep spell, requiring no spell components."); m == nil || m[1] != "Sleep" {
		t.Errorf("casts the Sleep spell: %q", m)
	}
	if m := mfxSpellRe.FindStringSubmatch("The imp casts Invisibility on itself, requiring no spell components."); m == nil || m[1] != "Invisibility" {
		t.Errorf("casts Invisibility on itself: %q", m)
	}
	if got := castChoiceList("The priest casts Bless, Healing Word, or Sanctuary, using the same spellcasting ability as Spellcasting."); strings.Join(got, "|") != "Bless|Healing Word|Sanctuary" {
		t.Errorf("choice list: %q", got)
	}
	if got := castChoiceList("The mephit casts the Sleep spell, requiring no spell components."); got != nil {
		t.Errorf("one spell read as a choice: %q", got)
	}
	for _, slug := range []string{"entangle", "web"} {
		cs := conditionSpells[slug]
		e := saveEffect{Ability: cs.Save, DC: 13, Conditions: cs.Conditions, Rounds: cs.Rounds, EndSave: cs.EndSave, Escapes: cs.escapes(13)}
		a := e.conditionAdd("restrained", "Sprite (Entangle)")
		if a.EndSave != nil || len(a.Escapes) != 1 || a.Escapes[0].Skill != "Athletics" || a.Escapes[0].DC != 13 || a.Escapes[0].Ability != "Strength" {
			t.Errorf("%s: end save %v, escapes %+v", slug, a.EndSave, a.Escapes)
		}
	}
	if cs := conditionSpells["animal-friendship"]; cs.Save != "wis" || cs.Rounds != 14400 || spellCreatureType["animal-friendship"] != "beast" {
		t.Errorf("animal friendship: %+v", cs)
	}
	for _, c := range []struct {
		slug             string
		slot, limit, rds int
		kind             string
	}{{"bless", 1, 3, 10, "bless"}, {"bless", 3, 5, 10, "bless"}, {"shield-of-faith", 1, 1, 100, "shield_of_faith"}, {"invisibility", 2, 1, 600, "invisibility"}, {"invisibility", 4, 3, 600, "invisibility"}} {
		kind, rounds, limit, _, ok := lastingSpellEffect(c.slug, c.slot)
		if !ok || kind != c.kind || rounds != c.rds || limit != c.limit {
			t.Errorf("%s at %d: %s %d %d", c.slug, c.slot, kind, rounds, limit)
		}
	}
	if dice, mod := spellHealDice(srdSpellsMemory["healing-word"], 2); dice != "4d4" || !mod {
		t.Errorf("Healing Word at level 2: %q %v", dice, mod)
	}
	if crProficiencyBonus("1/4") != 2 || crProficiencyBonus("5") != 3 || crProficiencyBonus("9") != 4 || crProficiencyBonus("17") != 6 {
		t.Error("Proficiency Bonus by CR")
	}
}
