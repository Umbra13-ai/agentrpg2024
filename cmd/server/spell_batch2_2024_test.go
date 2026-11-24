package main

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/agentrpg/agentrpg/game"
)

// The tester's second spell notes (Nov 23): a damaging spell's own condition, a rider's time, the wrong recipient refused before anything is spent,
// Befuddlement's spellcasting and Glibness's Charisma checks.

// Rule 1: the condition a damaging spell gives is read from its text, for every spell of the catalogue that words one the cast resolves.
func TestPostgresCastConditionsOverTheCatalog(t *testing.T) {
	testPostgres(t)
	var got []string
	for slug, sp := range srdSpellsMemory {
		if _, ok := castConditionOf(sp); ok {
			got = append(got, slug)
		}
	}
	sort.Strings(got)
	want := "black-tentacles,contagion,ray-of-sickness,storm-of-vengeance,sunbeam,sunburst,weird" // the summons' later hits and Contact Other Plane's own save are not
	if strings.Join(got, ",") != want {
		t.Errorf("the spells read as giving a condition with their damage: %v; want %s", got, want)
	}
	for slug, w := range map[string]castCondition{
		"sunburst":        {Condition: "blinded", Rounds: 10, Repeat: "con"},
		"sunbeam":         {Condition: "blinded", End: riderEndSourceTurnStart},
		"ray-of-sickness": {Condition: "poisoned", OnHit: true, End: riderEndSourceTurnEnd},
		"contagion":       {Condition: "poisoned", Spell: true},
		"black-tentacles": {Condition: "restrained", Spell: true},
		"weird":           {Condition: "frightened", Spell: true, Repeat: "wis"},
	} {
		if cc, _ := castConditionOf(srdSpellsMemory[slug]); cc != w {
			t.Errorf("%s: %+v, want %+v", slug, cc, w)
		}
	}
}

// Rule 1 through the cast: Sunburst blinds on a failed save; Ray of Sickness poisons on a hit; Black Tentacles restrains for its Concentration.
func TestPostgresCastConditionsApplied(t *testing.T) {
	testPostgres(t)
	_, char, camp, target, cast := spellFightFixture(t, "castcond")
	pgExec(t, `UPDATE characters SET class = 'wizard', subclass = NULL, level = 17, intl = 30, hp = max_hp,
		known_spells = '["sunburst","ray-of-sickness","black-tentacles"]'::jsonb, prepared_spells = '["sunburst","ray-of-sickness","black-tentacles"]'::jsonb, inventory = $2::jsonb WHERE id = $1`, char, focusInventory)
	has := func(cond string) bool {
		recs, _ := readConditionRecords(db, camp, target)
		for _, r := range recs {
			if r.Condition == cond {
				return true
			}
		}
		return false
	}
	clear := func() { removeConditionsWhere(camp, target, func(conditionRecord) bool { return true }) }
	for _, c := range []struct{ slug, cond, sign string }{{"sunburst", "blinded", "fails"}, {"black-tentacles", "restrained", "fails"}, {"ray-of-sickness", "poisoned", "HIT"}} {
		applied := false
		for try := 0; try < 12 && !applied; try++ {
			clear()
			res := cast(c.slug, map[string]interface{}{"slot_level": map[string]int{"sunburst": 8, "black-tentacles": 4, "ray-of-sickness": 1}[c.slug], "target_id": target})
			if strings.Contains(res, c.sign) {
				applied = true
				if !has(c.cond) {
					t.Errorf("%s: %s but the target has no %s condition: %q", c.slug, c.sign, c.cond, res)
				}
			}
		}
		if !applied {
			t.Logf("%s: no %s in 12 casts", c.slug, c.sign)
		}
	}
}

// Rule 2: a rider lasts as the spell's text says, and the effect ends at that turn boundary.
func TestPostgresSpellRiderTimes(t *testing.T) {
	testPostgres(t)
	for slug, want := range map[string]string{"chill-touch": "source_end", "starry-wisp": "source_end", "guiding-bolt": "source_end", "mind-sliver": "source_end",
		"shocking-grasp": "target_start", "vicious-mockery": "target_end", "ray-of-frost": ""} {
		if _, ok := srdSpellsMemory[slug]; !ok {
			continue // a spell not in the catalogue
		}
		if got, _ := spellRiderEnds(slug); got != want {
			t.Errorf("%s ends %q, want %q", slug, got, want)
		}
	}
	_, char, camp, target, _ := spellFightFixture(t, "ridertimes")
	count := func(kind string) int {
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM combat_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = $3`, camp, target, kind).Scan(&n)
		return n
	}
	addCombatEffectUntil(camp, target, char, "no_healing", "source_end")
	addCombatEffectUntil(camp, target, char, "addled", "target_start")
	addCombatEffectUntil(camp, target, char, "slow", "")
	expireCombatEffectsOf(camp, char) // the caster's turn starts: only "slow" (the default) ends
	if count("slow") != 0 || count("no_healing") != 1 || count("addled") != 1 {
		t.Errorf("at the caster's turn start: slow %d, no_healing %d, addled %d", count("slow"), count("no_healing"), count("addled"))
	}
	expireCombatEffectsOf(camp, target) // the target's turn starts: Shocking Grasp's ends
	if count("addled") != 0 || count("no_healing") != 1 {
		t.Errorf("at the target's turn start: addled %d, no_healing %d", count("addled"), count("no_healing"))
	}
	// the caster's turn ends: Chill Touch's ends at the end of its NEXT turn (a cast during its own turn lasts through the turn after)
	casting := currentTurnID(camp) == char
	expireCombatEffectsAtTurnEnd(camp, char)
	if casting && count("no_healing") != 1 {
		t.Errorf("cast during the caster's own turn, the effect ended with that turn: no_healing %d", count("no_healing"))
	}
	if casting {
		expireCombatEffectsAtTurnEnd(camp, char)
	}
	if count("no_healing") != 0 {
		t.Errorf("at the end of the caster's next turn: no_healing %d", count("no_healing"))
	}
}

// Rule 3: a lasting spell aimed at an encounter creature that only characters can hold is refused before the slot or a material is spent.
func TestPostgresLastingSpellRefusedBeforeSpending(t *testing.T) {
	testPostgres(t)
	_, toks, _, chars, mons := pgFightWith(t, "lastingrefuse", 1)
	tok, char, target := toks[0], chars[0], mons[0]
	inv := `[{"id":"i1","name":"Holy Symbol","type":"focus","spellcasting_focus":true},{"id":"i2","name":"Component Pouch","type":"focus","spellcasting_focus":true},{"id":"i3","name":"Diamond Dust","quantity":1,"value_gp":100}]`
	pgExec(t, `UPDATE characters SET class = 'druid', subclass = NULL, level = 9, wis = 20, hp = max_hp, spell_slots_used = '{}'::jsonb,
		known_spells = '["stoneskin","heroism","resistance","bless"]'::jsonb, prepared_spells = '["stoneskin","heroism","resistance","bless"]'::jsonb, inventory = $2::jsonb WHERE id = $1`, char, inv)
	for _, slug := range []string{"stoneskin", "resistance"} {
		pgExec(t, `UPDATE characters SET action_used = false, slot_spent_turn = '', spell_slots_used = '{}'::jsonb WHERE id = $1`, char)
		c, j := pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "cast", "spell_slug": slug, "target_id": target, "slot_level": 4, "description": "Cast " + slug + " choosing fire"})
		res := fmt.Sprint(j["result"]) + fmt.Sprint(j["message"])
		if !strings.Contains(res, "Cannot cast") || !strings.Contains(res, "creature of the encounter") {
			t.Errorf("%s at an encounter creature: %d %q", slug, c, res)
		}
		var used string
		var n int
		db.QueryRow(`SELECT COALESCE(spell_slots_used::text,'{}') FROM characters WHERE id = $1`, char).Scan(&used)
		db.QueryRow(`SELECT COUNT(*) FROM spell_effects WHERE target_id = $1`, char).Scan(&n)
		if used != "{}" || n != 0 {
			t.Errorf("%s: slots used %s, effects on the caster %d", slug, used, n)
		}
	}
	var invNow string
	db.QueryRow(`SELECT inventory::text FROM characters WHERE id = $1`, char).Scan(&invNow)
	if !strings.Contains(invNow, "Diamond Dust") {
		t.Error("Stoneskin's diamond dust was used up by a refused cast")
	}
	if _, _, ok := lastingKindOf("heroism"); !ok {
		t.Error("Heroism is not read as a lasting spell")
	}
}

// Rule 4: Befuddlement takes away spellcasting until Heal or Greater Restoration ends it; Glibness makes a Charisma check's d20 at least 15.
func TestPostgresBefuddlementAndGlibness(t *testing.T) {
	testPostgres(t)
	tok, char, camp, target, cast := spellFightFixture(t, "befuddle")
	pgExec(t, `UPDATE characters SET class = 'bard', subclass = NULL, level = 15, cha = 30, hp = max_hp, known_spells = '["befuddlement","glibness"]'::jsonb,
		prepared_spells = '["befuddlement","glibness"]'::jsonb, inventory = $2::jsonb WHERE id = $1`, char, focusInventory)
	got := false
	for try := 0; try < 10 && !got; try++ {
		res := cast("befuddlement", map[string]interface{}{"slot_level": 8, "target_id": target})
		got = strings.Contains(res, "can't cast spells")
	}
	if got {
		if !cannotCastSpells(camp, target) || monsterCastBlocked(camp, target) == "" {
			t.Error("a befuddled monster can still cast")
		}
		endSpellConditions(camp, target, "heal", "")
		if cannotCastSpells(camp, target) {
			t.Error("Heal did not end Befuddlement")
		}
	} else {
		t.Log("no failed save in 10 casts")
	}
	addCondition(camp, char, condAdd{Condition: befuddledMarker, Source: "Befuddlement"})
	pgExec(t, `UPDATE characters SET action_used = false, spell_slots_used = '{}'::jsonb WHERE id = $1`, char)
	if c, j := pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "cast", "spell_slug": "glibness"}); c == 200 || !strings.Contains(fmt.Sprint(j["message"]), "Befuddlement") {
		t.Errorf("a befuddled caster cast: %d %v", c, j["message"])
	}
	removeConditionsWhere(camp, char, func(r conditionRecord) bool { return r.Condition == befuddledMarker })
	cast("glibness", map[string]interface{}{})
	if w := (checkCtx{who: char, ability: "cha", skill: "persuasion"}).sources(); w.Floor != 15 {
		t.Fatalf("Glibness's floor on a Charisma check: %d", w.Floor)
	}
	if w := (checkCtx{who: char, ability: "wis", skill: "insight"}).sources(); w.Floor != 0 {
		t.Errorf("Glibness raised a Wisdom check: %d", w.Floor)
	}
	for i := 0; i < 30; i++ {
		if r := (checkCtx{who: char, ability: "cha", skill: "persuasion"}).roll(); r.Final < 15 {
			t.Fatalf("a Charisma check under Glibness rolled %d", r.Final)
		}
	}
}

// Reincarnate brings back a dead creature in a new body (a row of revivalSpells, the one revival owner), its species rolled on the spell's table.
func TestPostgresReincarnateRevives(t *testing.T) {
	testPostgres(t)
	_, toks, _, chars, _ := pgFightWith(t, "reincarnate", 2)
	tok, druid, ally := toks[0], chars[0], chars[1]
	pgExec(t, `UPDATE characters SET class = 'druid', subclass = NULL, level = 9, wis = 20, hp = max_hp, action_used = false, spell_slots_used = '{}'::jsonb,
		known_spells = '["reincarnate"]'::jsonb, prepared_spells = '["reincarnate"]'::jsonb,
		inventory = '[{"id":"i1","name":"Druidic Focus","type":"focus","spellcasting_focus":true},{"id":"i2","name":"Rare Oils","quantity":1,"value_gp":1000}]'::jsonb WHERE id = $1`, druid)
	if _, err := killCharacter(ally); err != nil {
		t.Fatal(err)
	}
	c, j := pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "cast", "spell_slug": "reincarnate", "target_id": ally, "slot_level": 5})
	res := fmt.Sprint(j["result"]) + fmt.Sprint(j["message"])
	var dead bool
	var hp int
	db.QueryRow(`SELECT COALESCE(is_dead,false), hp FROM characters WHERE id = $1`, ally).Scan(&dead, &hp)
	if c != 200 || dead || hp < 1 || !strings.Contains(res, "the new body is") {
		t.Errorf("Reincarnate on a dead ally: %d, dead %v, hp %d: %q", c, dead, hp, res)
	}
}

// A roll that uses up an effect names what gave it: Vicious Mockery's Disadvantage, the Sap mastery's, the GM's rider, Guiding Bolt's Advantage.
func TestPostgresEffectRollsNameTheirOrigin(t *testing.T) {
	testPostgres(t)
	_, char, camp, target, _ := spellFightFixture(t, "effectorigin")
	roll := func(attacker, at int) game.AttackRoll {
		ar, _, _ := (&attackCtx{lobby: camp, attacker: attacker, target: at, name: "Probe", bonus: 1, melee: true, weapon: true}).roll()
		return ar
	}
	for _, c := range []struct {
		write func()
		want  string
	}{
		{func() { spellRider(camp, char, target, "vicious-mockery") }, "Vicious Mockery"},
		{func() { addCombatEffectFrom(camp, target, char, "sap", "", "Sap") }, "Sap"},
		{func() { addCombatEffectFrom(camp, target, gmRiderSource, "sap", "", "the GM's rider") }, "the GM's rider"},
		{func() { addCombatEffect(camp, target, char, "sap") }, "Disadvantage on its next attack roll"},
	} {
		c.write()
		if got := strings.Join(roll(target, char).Disadvantage, "; "); got != c.want {
			t.Errorf("the monster's next attack: Disadvantage from %q, want %q", got, c.want)
		}
	}
	if _, ok := srdSpellsMemory["guiding-bolt"]; ok {
		spellRider(camp, char, target, "guiding-bolt")
		if got := strings.Join(roll(char, target).Advantage, "; "); !strings.Contains(got, "Guiding Bolt") {
			t.Errorf("the next attack against it: Advantage from %q, want Guiding Bolt", got)
		}
	}
}
