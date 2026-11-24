package main

// Mechanics a GM attaches to an entry (monster_mechanics_2024.go). Every monster and wording here is invented.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const (
	ashClawText     = "Melee Attack Roll: +60, reach 5 ft. Hit: 3 (1d4 + 1) Slashing damage. If this damage reduces the target to 0 Hit Points, it crumbles to ash."
	ashClawCovers   = "If this damage reduces the target to 0 Hit Points, it crumbles to ash."
	numbStingText   = "Melee Attack Roll: +60, reach 5 ft. Hit: 3 (1d4 + 1) Piercing damage. A creature this sting fells lies numb and still, poisoned and unmoving for an hour."
	numbStingCovers = "A creature this sting fells lies numb and still, poisoned and unmoving for an hour."
)

func ashClaw() srdMonsterEntry {
	return srdMonsterEntry{Name: "Ash Claw", Text: ashClawText, Mechanics: []entryMechanic{{"kind": "drop_to_zero", "result": "dies", "note": "nothing is left to raise", "covers": ashClawCovers}}}
}

func numbSting() srdMonsterEntry {
	return srdMonsterEntry{Name: "Numbing Sting", Text: numbStingText, Mechanics: []entryMechanic{{"kind": "drop_to_zero", "result": "stable", "condition": "Poisoned", "minutes": float64(60), "also": "paralyzed", "covers": numbStingCovers}}}
}

// mechanicsFromJSON: mechanics as a request carries them (numbers are float64).
func mechanicsFromJSON(t *testing.T, s string) []entryMechanic {
	t.Helper()
	var out []entryMechanic
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestMechanicProblems: every refusal the catalogue's rows make, and the valid shapes; each row's own example is valid.
func TestMechanicProblems(t *testing.T) {
	cases := []struct {
		group, text, mechanics, want string
	}{
		{"action", ashClawText, `[{"kind":"drop_to_zero","result":"dies"}]`, ""},
		{"action", ashClawText, `[{"kind":"drop_to_zero","result":"STABLE","condition":"poisoned","minutes":60,"also":"Paralyzed"}]`, ""},
		{"action", ashClawText, `[{"kind":"teleport_home"}]`, "kind must be one of drop_to_zero"},
		{"action", ashClawText, `[{"kind":"drop_to_zero"}]`, "result is required"},
		{"action", ashClawText, `[{"kind":"drop_to_zero","result":"explodes"}]`, "result must be one of dies, stable"},
		{"action", ashClawText, `[{"kind":"drop_to_zero","result":"dies","condition":"poisoned","minutes":60}]`, "condition is only taken with result stable"},
		{"action", ashClawText, `[{"kind":"drop_to_zero","result":"stable","condition":"poisoned"}]`, "condition needs minutes"},
		{"action", ashClawText, `[{"kind":"drop_to_zero","result":"stable","minutes":60}]`, "minutes is only taken with condition"},
		{"action", ashClawText, `[{"kind":"drop_to_zero","result":"stable","condition":"poisoned","minutes":0}]`, "minutes must be a whole number from 1 to 14400"},
		{"action", ashClawText, `[{"kind":"drop_to_zero","result":"stable","condition":"poisoned","minutes":1.5}]`, "minutes must be a whole number"},
		{"action", ashClawText, `[{"kind":"drop_to_zero","result":"stable","condition":"exhaustion","minutes":5}]`, "condition must be one of"},
		{"action", ashClawText, `[{"kind":"drop_to_zero","result":"stable","condition":"poisoned","minutes":5,"also":"Poisoned"}]`, "also must be another condition"},
		{"action", ashClawText, `[{"kind":"drop_to_zero","result":"dies","dc":12}]`, "unknown field dc"},
		{"action", ashClawText, `[{"kind":"drop_to_zero","result":"dies"},{"kind":"drop_to_zero","result":"dies"}]`, "an entry takes each kind once"},
		{"trait", ashClawText, `[{"kind":"drop_to_zero","result":"dies"}]`, "attaches to an entry with an attack roll"},
		{"action", "The creature hums a low note.", `[{"kind":"drop_to_zero","result":"dies"}]`, "attaches to an entry with an attack roll"},
		{"action", ashClawText, `[{"kind":"drop_to_zero","result":"dies","covers":"it turns to glass"}]`, "covers must be words of the entry's text"},
		{"action", ashClawText, `[{"kind":"drop_to_zero","result":"dies","covers":"Melee Attack Roll: +60"}]`, "covers can't include the attack line"},
		{"action", ashClawText, `[{"kind":"drop_to_zero","result":"dies","covers":7}]`, "covers is the entry's own words"},
		{"action", ashClawText, `[{"kind":"drop_to_zero","result":"dies","note":""}]`, "note must be text"},
	}
	for _, c := range cases {
		got := mechanicProblem(c.group, srdMonsterEntry{Name: "Ash Claw", Text: c.text, Mechanics: mechanicsFromJSON(t, c.mechanics)})
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s %s: got %q, want %q", c.group, c.mechanics, got, c.want)
		}
	}
	for _, k := range mechanicCatalog {
		b, _ := json.Marshal(k.Example)
		e := srdMonsterEntry{Name: "Example", Text: k.ExampleOn, Mechanics: mechanicsFromJSON(t, "["+string(b)+"]")}
		group := map[bool]string{true: "trait", false: "action"}[k.On == "trait"]
		if p := mechanicProblem(group, e); p != "" {
			t.Errorf("the catalogue's own example of %s is refused: %s", k.Kind, p)
		}
		if k.describe == nil || k.Does == "" || k.AppliedBy == "" || mechanicFits[k.On].Check == nil {
			t.Errorf("%s: every row says what it does, who applies it and how it reads attached", k.Kind)
		}
	}
	// a choice is stored as its option
	e := srdMonsterEntry{Name: "x", Text: ashClawText, Mechanics: mechanicsFromJSON(t, `[{"kind":"drop_to_zero","result":"STABLE","condition":"Poisoned","minutes":60}]`)}
	if mechanicProblem("action", e) != "" || e.Mechanics[0].str("result") != "stable" || e.Mechanics[0].str("condition") != "poisoned" {
		t.Errorf("choices not stored as their options: %v", e.Mechanics[0])
	}
}

// TestRulesTextLeavesTheCoveredWordsOut: the words a mechanic covers are out of what the text rules read; with no mechanic the text is
// the entry's own, byte for byte, for every SRD entry (the play rows are built from it).
func TestRulesTextLeavesTheCoveredWordsOut(t *testing.T) {
	if got := rulesText(ashClaw()); strings.Contains(got, "crumbles") || !strings.HasSuffix(got, "Slashing damage.") {
		t.Errorf("rulesText: %q", got)
	}
	for _, m := range srdMonsters2024() {
		for _, g := range [][]srdMonsterEntry{m.Traits, m.Actions, m.BonusActions, m.Reactions, m.Legendary} {
			for _, e := range g {
				if rulesText(e) != strings.TrimSpace(e.Text) {
					t.Fatalf("%s / %s: rulesText changed an entry with no mechanics", m.Name, e.Name)
				}
			}
		}
	}
}

// TestCoveredSentencesAreApplied: the report marks a covered sentence applied by the mechanic, and the same sentence without it is the GM's.
func TestCoveredSentencesAreApplied(t *testing.T) {
	verdictOf := func(e srdMonsterEntry, sentence string) sentenceVerdict {
		for _, v := range entryVerdicts("action", e) {
			if strings.Contains(v.Sentence, sentence) {
				return v
			}
		}
		t.Fatalf("no sentence %q in %v", sentence, entryVerdicts("action", e))
		return sentenceVerdict{}
	}
	bare := ashClaw()
	bare.Mechanics = nil
	if v := verdictOf(bare, "crumbles to ash"); v.Status == "applied" {
		t.Fatalf("an invented wording no rule reads was applied: %+v", v)
	}
	for _, e := range []srdMonsterEntry{ashClaw(), numbSting()} {
		v := verdictOf(e, strings.TrimSuffix(e.Mechanics[0].str("covers"), "."))
		if v.Status != "applied" || !strings.Contains(v.Rule, "attached mechanic drop_to_zero") {
			t.Errorf("%s: covered sentence %+v", e.Name, v)
		}
		r := entryReport("action", e)
		mv, _ := r["mechanics"].([]map[string]interface{})
		if len(mv) != 1 || mv[0]["kind"] != "drop_to_zero" || mv[0]["covers"] == nil {
			t.Errorf("%s: report's mechanics %v", e.Name, r["mechanics"])
		}
	}
	if r := entryReport("action", bare); r["mechanics"] != nil {
		t.Errorf("an entry without mechanics lists some: %v", r["mechanics"])
	}
	if d := mechanicsView(numbSting())[0]["does"]; d != "a target this hit drops to 0 Hit Points becomes Stable and has the Poisoned condition for 1 hour, and the Paralyzed condition while Poisoned" {
		t.Errorf("describe: %v", d)
	}
}

// TestPostgresAttachedDropToZeroOnEveryTarget: an imported monster's attached drop_to_zero applied on every hit route (a character, a
// monster, a companion), the covered words never reported as unapplied; a hit that leaves the target standing does nothing.
func TestPostgresAttachedDropToZeroOnEveryTarget(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	m := inventedMonster("Test Ash Wight", "Undead", nil, []srdMonsterEntry{ashClaw(), numbSting()})
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented mechanics fixture", "monsters": []srdMonster2024{m}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Test Ash Wight", "monster_key": key, "hp": 500, "ac": 1, "initiative": -500},
		{"name": "Ash Victim", "monster_key": "goblin-warrior", "hp": 1000, "ac": 1, "initiative": -501}}})
	wight := monsterIDsByKeyOrName(camp, "Test Ash Wight")[0]
	victim := monsterIDsByKeyOrName(camp, "Ash Victim")[0]
	hit := func(action, target string) string {
		t.Helper()
		_, line := narrateHit(t, gm, camp, wight, action, target, "")
		if !strings.Contains(line, "HIT") {
			t.Fatalf("%s never hit %s: %s", action, target, line)
		}
		return line
	}
	reset := func(hp int) {
		pgExec(t, `UPDATE characters SET hp=$2,max_hp=1000,ac=1,is_dead=false,is_stable=false,death_save_failures=0,death_save_successes=0 WHERE id=$1`, hero, hp)
		pgExec(t, `UPDATE companions SET hp=$2,max_hp=1000,ac=1 WHERE id=$1`, petID, hp)
		setMonsterHP(camp, victim, hp)
		endConditionRecords(append(recordsOf(camp, hero, "poisoned"), recordsOf(camp, hero, "paralyzed")...))
	}
	unapplied := func(line string) bool {
		return strings.Contains(line, "crumbles to ash") || strings.Contains(line, "numb and still")
	}
	// standing: nothing
	reset(1000)
	if line := hit("Ash Claw", fmt.Sprint(hero)); strings.Contains(line, "dies") || unapplied(line) {
		t.Fatalf("a hit that left the target standing: %s", line)
	}
	// dies, on every target
	for _, tg := range []struct {
		id   int
		name string
	}{{hero, fmt.Sprint(hero)}, {victim, "Ash Victim"}, {pet, "React Pet"}} {
		reset(1)
		line := hit("Ash Claw", tg.name)
		if tg.id == pet { // a companion at 0 Hit Points disappears, and the hit's other effects with it (companions_2024.go)
			if !strings.Contains(line, "it disappears") || unapplied(line) {
				t.Errorf("Ash Claw dropped the companion: %s", line)
			}
			continue
		}
		if !strings.Contains(line, "dies") || !strings.Contains(line, "nothing is left to raise") || unapplied(line) {
			t.Errorf("Ash Claw dropped %s: %s", tg.name, line)
		}
		if hp, _ := creatureHP(camp, tg.id); hp != 0 {
			t.Errorf("%s at %d Hit Points", tg.name, hp)
		}
		if tg.id == hero {
			var dead bool
			db.QueryRow(`SELECT COALESCE(is_dead,false) FROM characters WHERE id=$1`, hero).Scan(&dead)
			if !dead {
				t.Error("the character the Ash Claw dropped is not dead")
			}
		}
	}
	var dead bool
	// stable with the conditions (a character)
	reset(1)
	line := hit("Numbing Sting", fmt.Sprint(hero))
	var stable bool
	db.QueryRow(`SELECT COALESCE(is_stable,false), COALESCE(is_dead,false) FROM characters WHERE id=$1`, hero).Scan(&stable, &dead)
	if !stable || dead || !hasCondMarker(camp, hero, "poisoned") || !hasCondMarker(camp, hero, "paralyzed") || unapplied(line) {
		t.Fatalf("Numbing Sting: stable %v dead %v poisoned %v paralyzed %v: %s", stable, dead, hasCondMarker(camp, hero, "poisoned"), hasCondMarker(camp, hero, "paralyzed"), line)
	}
	if rs := recordsOf(camp, hero, "poisoned"); len(rs) != 1 || rs[0].DurationRounds != 600 {
		t.Errorf("Poisoned for 60 minutes (600 rounds): %+v", rs)
	}
	endConditionRecords(recordsOf(camp, hero, "poisoned"))
	if hasCondMarker(camp, hero, "paralyzed") {
		t.Error("Paralyzed outlived the Poisoned record")
	}
	// the GM's stat block reads the mechanics and marks the covered words applied
	sb := monsterStatBlock(key)
	b, _ := json.Marshal(sb["entry_rules"])
	if !strings.Contains(string(b), "attached mechanic drop_to_zero") || !strings.Contains(string(b), `"mechanics"`) {
		t.Errorf("stat block entry_rules: %s", b)
	}
}

// TestPostgresMechanicsInPreviewAndCatalogue: the preview refuses a bad mechanic with the catalogue's words and reports a good one; the
// catalogue route lists every kind and gives an SRD stat block to copy.
func TestPostgresMechanicsInPreviewAndCatalogue(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "mechanics gm")
	pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Mechanics", "max_players": 2})
	bad := ashClaw()
	bad.Mechanics = []entryMechanic{{"kind": "drop_to_zero", "result": "explodes"}}
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters/preview", gm, map[string]interface{}{"monsters": []srdMonster2024{inventedMonster("Test Bad Mech", "Undead", nil, []srdMonsterEntry{bad})}})
	b, _ := json.Marshal(j)
	if c != 200 || !strings.Contains(string(b), `"valid":false`) || !strings.Contains(string(b), "result must be one of dies, stable") {
		t.Fatalf("preview of a bad mechanic: %d %s", c, b)
	}
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "x", "monsters": []srdMonster2024{inventedMonster("Test Bad Mech", "Undead", nil, []srdMonsterEntry{bad})}})
	if c != 400 {
		t.Fatalf("import of a bad mechanic: %d %v", c, j)
	}
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters/preview", gm, map[string]interface{}{"monsters": []srdMonster2024{inventedMonster("Test Good Mech", "Undead", nil, []srdMonsterEntry{ashClaw()})}})
	b, _ = json.Marshal(j)
	if c != 200 || !strings.Contains(string(b), "attached mechanic drop_to_zero") || !strings.Contains(string(b), `"sentences_the_server_will_not_mention":0`) {
		t.Fatalf("preview of a good mechanic: %d %s", c, b)
	}
	c, j = pgCall(t, "GET", "/api/gm/compendium/mechanics?srd=Goblin%20Warrior", gm, nil)
	if c != 200 || len(j["mechanics"].([]interface{})) != len(mechanicCatalog) || j["srd_monster"].(map[string]interface{})["name"] != "Goblin Warrior" {
		t.Fatalf("catalogue: %d %v", c, j)
	}
	if c, _ = pgCall(t, "GET", "/api/gm/compendium/mechanics?srd=Not%20A%20Monster", gm, nil); c != 404 {
		t.Errorf("an unknown SRD name: %d", c)
	}
}

const (
	spitText   = "Ranged Attack Roll: +60, range 60 ft. Hit: 9 (2d8) damage of a type it chooses: Acid, Cold, or Fire."
	spitCovers = "of a type it chooses: Acid, Cold, or Fire"
	glareText  = "The watcher glances at one creature it can see within 60 feet (roll a die; reroll an effect already used this turn): Frost Glance. " +
		"Constitution Saving Throw: DC 30. Failure: 9 (2d8) Cold damage. Ember Glance. Dexterity Saving Throw: DC 30. Failure: 9 (2d8) Fire damage."
	glareCovers = "(roll a die; reroll an effect already used this turn)"
)

func spit() srdMonsterEntry {
	return srdMonsterEntry{Name: "Prism Spit", Text: spitText, Mechanics: []entryMechanic{{"kind": "chosen_damage_type", "types": []interface{}{"Acid", "cold", "FIRE"}, "covers": spitCovers}}}
}

func glare() srdMonsterEntry {
	return srdMonsterEntry{Name: "Twin Glance", Text: glareText, Mechanics: []entryMechanic{{"kind": "random_choice", "note": "reroll an effect already used this turn", "covers": glareCovers}}}
}

// TestChosenTypeAndRandomChoiceProblems: the entry checks and list parameters of the two kinds; the rows they make.
func TestChosenTypeAndRandomChoiceProblems(t *testing.T) {
	cases := []struct{ text, mechanics, want string }{
		{spitText, `[{"kind":"chosen_damage_type","types":["acid","cold"],"covers":"of a type it chooses: Acid, Cold, or Fire"}]`, ""},
		{spitText, `[{"kind":"chosen_damage_type","types":["acid"],"covers":"of a type it chooses: Acid, Cold, or Fire"}]`, "types must be a list of 2 or more"},
		{spitText, `[{"kind":"chosen_damage_type","types":["acid","sonic"],"covers":"of a type it chooses: Acid, Cold, or Fire"}]`, "types[1] must be one of"},
		{spitText, `[{"kind":"chosen_damage_type","types":["acid","Acid"],"covers":"of a type it chooses: Acid, Cold, or Fire"}]`, "each once"},
		{ashClawText, `[{"kind":"chosen_damage_type","types":["acid","cold"]}]`, "the Hit has no damage amount without a type"},
		{glareText, `[{"kind":"random_choice"}]`, ""},
		{glareText, `[{"kind":"random_choice","order":["Ember Glance","frost glance"]}]`, ""},
		{glareText, `[{"kind":"random_choice","order":["Ember Glance"]}]`, "order must be a list of every one once of: Frost Glance, Ember Glance"},
		{glareText, `[{"kind":"random_choice","order":["Ember Glance","Ember Glance"]}]`, "each once"},
		{ashClawText, `[{"kind":"random_choice"}]`, "attaches to an entry that offers two or more named effects"},
		{glareText, `[{"kind":"chosen_damage_type","types":["acid","cold"]}]`, "attaches to an entry with an attack roll"},
	}
	for _, c := range cases {
		got := mechanicProblem("action", srdMonsterEntry{Name: "x", Text: c.text, Mechanics: mechanicsFromJSON(t, c.mechanics)})
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.mechanics, got, c.want)
		}
	}
	if mechanicProblem("reaction", glare()) == "" {
		t.Error("random_choice on a Reaction")
	}
	e := spit()
	if p := mechanicProblem("action", e); p != "" {
		t.Fatal(p)
	}
	row := entryActionRow(e)
	if strings.Join(row.ChosenTypes, ",") != "acid,cold,fire" || !strings.HasSuffix(row.Desc, "9 (2d8) damage.") {
		t.Fatalf("row: %+v", row)
	}
	if _, problem := prepareMonsterAttack(row, ""); !strings.Contains(problem, "name one in use (acid, cold, fire). Nothing was rolled") {
		t.Errorf("no type named: %q", problem)
	}
	if _, problem := prepareMonsterAttack(row, "force"); problem == "" {
		t.Error("a type not on the list was taken")
	}
	if got, problem := prepareMonsterAttack(row, "Cold"); problem != "" || !strings.EqualFold(got.DamageType, "cold") || got.DamageDice != "2d8" {
		t.Errorf("cold: %+v %q", got, problem)
	}
	for _, v := range entryVerdicts("action", e) {
		if v.Status != "applied" {
			t.Errorf("Prism Spit sentence not applied: %+v", v)
		}
	}
}

// TestPostgresChosenTypeAndRandomChoiceInPlay: the GM's type through narrate (refused until named, then dealt as that type), and the
// rolled effect when use names none (named, it is the one named).
func TestPostgresChosenTypeAndRandomChoiceInPlay(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, watcher := earlyRulesFixture(t, "Test Prism Watcher", []srdMonsterEntry{spit(), glare()})
	hp := func() int {
		var n int
		db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, hero).Scan(&n)
		return n
	}
	if c, line := narrateAs(t, gm, camp, watcher, "Prism Spit", fmt.Sprint(hero), ""); c < 400 || hp() != 1000 || strings.Contains(line, "HIT") {
		t.Fatalf("no type named: %d %s (hp %d)", c, line, hp())
	}
	if c, _ := narrateAs(t, gm, camp, watcher, "Prism Spit", fmt.Sprint(hero), "force"); c < 400 || hp() != 1000 {
		t.Fatalf("a type off the list: %d", c)
	}
	if _, line := narrateHit(t, gm, camp, watcher, "Prism Spit", fmt.Sprint(hero), "cold"); !strings.Contains(strings.ToLower(line), "cold damage") || hp() >= 1000 {
		t.Fatalf("cold: %s", line)
	}
	seen := map[string]bool{}
	for i := 0; i < 30 && len(seen) < 2; i++ {
		c, line := narrateAs(t, gm, camp, watcher, "Twin Glance", fmt.Sprint(hero), "")
		if c != 200 || !strings.Contains(line, "Twin Glance: rolled 1d2 = ") || !strings.Contains(line, "reroll an effect already used this turn") || strings.Contains(line, "Name the one") {
			t.Fatalf("rolled glance: %d %s", c, line)
		}
		for _, n := range []string{"Frost Glance", "Ember Glance"} {
			if strings.Contains(line, "rolled 1d2 = 1: "+n) || strings.Contains(line, "rolled 1d2 = 2: "+n) {
				seen[n] = true
			}
		}
	}
	if len(seen) != 2 {
		t.Errorf("both effects should come up in 30 rolls: %v", seen)
	}
	if _, line := narrateAs(t, gm, camp, watcher, "Twin Glance", fmt.Sprint(hero), "Ember Glance"); strings.Contains(line, "rolled 1d2") || !strings.Contains(line, "Ember Glance") {
		t.Errorf("named in use: %s", line)
	}
	if c, _ := narrateAs(t, gm, camp, watcher, "Twin Glance", fmt.Sprint(hero), "Sun Glance"); c < 400 {
		t.Errorf("a use that names no effect: %d", c)
	}
}

const (
	dazeText    = "Melee Attack Roll: +60, reach 5 ft. Hit: 1 (1d1) Psychic damage. Until the end of its next turn, the target has a condition the dancer rolls for: Charmed or Frightened."
	dazeCovers  = "Until the end of its next turn, the target has a condition the dancer rolls for: Charmed or Frightened."
	flingText   = "Melee Attack Roll: +60, reach 5 ft. Hit: 1 (1d1) Bludgeoning damage. A Medium or smaller target is flung 10 feet back."
	flingCovers = "A Medium or smaller target is flung 10 feet back."
)

func daze() srdMonsterEntry {
	return srdMonsterEntry{Name: "Dazing Touch", Text: dazeText, Mechanics: []entryMechanic{{"kind": "random_condition", "conditions": []interface{}{"Charmed", "frightened"}, "until": "end_of_its_next_turn", "covers": dazeCovers}}}
}

func fling() srdMonsterEntry {
	return srdMonsterEntry{Name: "Fling", Text: flingText, Mechanics: []entryMechanic{{"kind": "forced_move", "direction": "push", "feet": float64(10), "max_size": "medium", "covers": flingCovers}}}
}

// TestRandomConditionAndForcedMoveProblems: the two kinds' refusals, and the movement rule the mechanic gives.
func TestRandomConditionAndForcedMoveProblems(t *testing.T) {
	cases := []struct{ mechanics, want string }{
		{`[{"kind":"random_condition","conditions":["charmed","stunned"],"until":"start_of_attackers_next_turn"}]`, ""},
		{`[{"kind":"random_condition","conditions":["charmed","stunned"],"until":"minutes","minutes":10}]`, ""},
		{`[{"kind":"random_condition","conditions":["charmed"],"until":"end_of_its_next_turn"}]`, "conditions must be a list of 2 or more"},
		{`[{"kind":"random_condition","conditions":["charmed","bored"],"until":"end_of_its_next_turn"}]`, "conditions[1] must be one of"},
		{`[{"kind":"random_condition","conditions":["charmed","stunned"]}]`, "until is required"},
		{`[{"kind":"random_condition","conditions":["charmed","stunned"],"until":"minutes"}]`, "until minutes needs minutes"},
		{`[{"kind":"random_condition","conditions":["charmed","stunned"],"until":"end_of_its_next_turn","minutes":5}]`, "minutes is only taken with until minutes"},
		{`[{"kind":"forced_move","direction":"pull","feet":15}]`, ""},
		{`[{"kind":"forced_move","direction":"gm_places","feet":20,"max_size":"Huge"}]`, ""},
		{`[{"kind":"forced_move","direction":"sideways","feet":15}]`, "direction must be one of push, pull, gm_places"},
		{`[{"kind":"forced_move","direction":"push","feet":12}]`, "feet must be a multiple of 5"},
		{`[{"kind":"forced_move","direction":"push"}]`, "feet is required"},
		{`[{"kind":"forced_move","direction":"push","feet":10,"max_size":"colossal"}]`, "max_size must be one of"},
	}
	for _, c := range cases {
		got := mechanicProblem("action", srdMonsterEntry{Name: "x", Text: ashClawText, Mechanics: mechanicsFromJSON(t, c.mechanics)})
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.mechanics, got, c.want)
		}
	}
	for _, e := range []srdMonsterEntry{daze(), fling()} {
		if p := mechanicProblem("action", e); p != "" {
			t.Fatal(p)
		}
		for _, v := range entryVerdicts("action", e) {
			if v.Status != "applied" {
				t.Errorf("%s: sentence not applied: %+v", e.Name, v)
			}
		}
	}
	if d := mechanicsView(fling())[0]["does"]; d != "a hit pushes its target up to 10 feet straight away from it (a Medium or smaller target)" {
		t.Errorf("forced_move describe: %v", d)
	}
}

// TestPostgresRandomConditionAndForcedMoveOnEveryTarget: the rolled condition and the push on a character, a monster and a companion.
func TestPostgresRandomConditionAndForcedMoveOnEveryTarget(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	m := inventedMonster("Test Dancer", "Fey", nil, []srdMonsterEntry{daze(), fling()})
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented mechanics fixture", "monsters": []srdMonster2024{m}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Test Dancer", "monster_key": key, "hp": 500, "ac": 1, "initiative": -500},
		{"name": "Dance Victim", "monster_key": "goblin-warrior", "hp": 100000, "ac": 1, "initiative": -501},
		{"name": "Dance Ogre", "monster_key": "ogre", "hp": 100000, "ac": 1, "initiative": -502}}})
	pgExec(t, `UPDATE characters SET hp=100000, max_hp=100000, ac=1 WHERE id=$1`, hero)
	pgExec(t, `UPDATE companions SET hp=100000, max_hp=100000, ac=1 WHERE id=$1`, petID)
	dancer := monsterIDsByKeyOrName(camp, "Test Dancer")[0]
	victim := monsterIDsByKeyOrName(camp, "Dance Victim")[0]
	ogre := monsterIDsByKeyOrName(camp, "Dance Ogre")[0]
	putMap := func(target, size int) {
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"..........", "..........", ".........."}, Tokens: []mapToken{
			{ID: dancer, Name: "dancer", Position: mapSquare{0, 0}, Size: 1}, {ID: target, Name: "target", Position: mapSquare{1, 0}, Size: size}}},
			"forced_movement": true}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	at := func(target int) mapSquare {
		mp, _ := activeEncounterMap(camp)
		tk, _ := mp.token(target)
		return tk.Position
	}
	for _, tg := range []struct {
		id   int
		name string
	}{{hero, fmt.Sprint(hero)}, {victim, "Dance Victim"}, {pet, "React Pet"}} {
		putMap(tg.id, 1)
		_, line := narrateHit(t, gm, camp, dancer, "Fling", tg.name, "")
		if p := at(tg.id); p != (mapSquare{3, 0}) || !strings.Contains(line, "pushed 10 feet") || strings.Contains(line, "flung") {
			t.Errorf("Fling %s: at %v: %s", tg.name, p, line)
		}
		putMap(tg.id, 1) // back within reach
		_, line = narrateHit(t, gm, camp, dancer, "Dazing Touch", tg.name, "")
		if !strings.Contains(line, "Dazing Touch: rolled 1d2 = ") || strings.Contains(line, "rolls for") {
			t.Errorf("Dazing Touch %s: %s", tg.name, line)
		}
		if !hasCondMarker(camp, tg.id, "charmed") && !hasCondMarker(camp, tg.id, "frightened") {
			t.Errorf("Dazing Touch gave %s no condition: %s", tg.name, line)
		}
	}
	putMap(ogre, 2) // Large: Fling moves a Medium or smaller target
	if _, line := narrateHit(t, gm, camp, dancer, "Fling", "Dance Ogre", ""); at(ogre) != (mapSquare{1, 0}) || strings.Contains(line, "pushed") {
		t.Errorf("a Large target was moved: %s", line)
	}
}

const (
	glareTraitText   = "In bright light the gloomling's eyes water, and its attack rolls and ability checks have Disadvantage."
	sunBurnTraitText = "The gloomling takes 10 Radiant damage when it starts its turn in sunlight."
)

func gloomTraits() []srdMonsterEntry {
	return []srdMonsterEntry{
		{Name: "Watering Eyes", Text: glareTraitText, Mechanics: []entryMechanic{{"kind": "light_penalty", "light": "bright_light", "on": "attack_rolls_and_checks", "covers": glareTraitText}}},
		{Name: "Sun Scorch", Text: sunBurnTraitText, Mechanics: []entryMechanic{{"kind": "light_turn_damage", "light": "sunlight", "amount": float64(10), "damage_type": "Radiant", "covers": sunBurnTraitText}}},
	}
}

// TestLightMechanicProblems: the trait fit and the two light kinds' refusals; the GM's trait view and open items read the mechanics.
func TestLightMechanicProblems(t *testing.T) {
	cases := []struct{ group, mechanics, want string }{
		{"trait", `[{"kind":"light_penalty","light":"sunlight","on":"d20_tests"}]`, ""},
		{"action", `[{"kind":"light_penalty","light":"sunlight","on":"d20_tests"}]`, "attaches to a trait"},
		{"trait", `[{"kind":"light_penalty","light":"moonlight","on":"d20_tests"}]`, "light must be one of sunlight, bright_light"},
		{"trait", `[{"kind":"light_penalty","light":"sunlight"}]`, "on is required"},
		{"trait", `[{"kind":"light_turn_damage","light":"sunlight","amount":5,"damage_type":"radiant"}]`, ""},
		{"trait", `[{"kind":"light_turn_damage","light":"sunlight","amount":0,"damage_type":"radiant"}]`, "amount must be a whole number from 1 to 1000"},
		{"trait", `[{"kind":"light_turn_damage","light":"sunlight","amount":5,"damage_type":"holy"}]`, "damage_type must be one of"},
	}
	for _, c := range cases {
		got := mechanicProblem(c.group, srdMonsterEntry{Name: "x", Text: ashClawText, Mechanics: mechanicsFromJSON(t, c.mechanics)})
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s %s: got %q, want %q", c.group, c.mechanics, got, c.want)
		}
	}
	for _, tr := range gloomTraits() {
		b, _ := json.Marshal(tr.Mechanics)
		tr.Mechanics = mechanicsFromJSON(t, string(b))
		if p := mechanicProblem("trait", tr); p != "" {
			t.Fatal(p)
		}
		for _, v := range entryVerdicts("trait", tr) {
			if v.Status != "applied" {
				t.Errorf("%s: %+v", tr.Name, v)
			}
		}
	}
	if got := recurringGMTraits(gloomTraits()); len(got) != 0 {
		t.Errorf("a trait an attached mechanic applies is listed as the GM's: %v", got)
	}
	bare := gloomTraits()[1]
	bare.Mechanics = nil
	if len(recurringGMTraits([]srdMonsterEntry{bare})) != 1 {
		t.Error("the same words without the mechanic should be the GM's open item")
	}
	if p, ok := mechanicLightPenalty(gloomTraits()[0]); !ok || p.Light != "bright" || !p.Attacks || !p.Checks || p.Saves {
		t.Errorf("light penalty: %+v", p)
	}
}

// TestPostgresLightMechanicsInPlay: Disadvantage on the monster's attack and its check in Bright Light, none in Dim Light; Radiant damage
// at the start of its turn in sunlight, none in Bright Light; the GM's stat block marks both traits applied.
func TestPostgresLightMechanicsInPlay(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, _, _ := companionReactionFixture(t)
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	m := inventedMonster("Test Gloomling", "Monstrosity", gloomTraits(), []srdMonsterEntry{{Name: "Claw", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 1 (1d1) Slashing damage."}})
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented mechanics fixture", "monsters": []srdMonster2024{m}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Test Gloomling", "monster_key": key, "hp": 500, "ac": 1, "initiative": -500}}})
	pgExec(t, `UPDATE characters SET hp=100000, max_hp=100000, ac=1 WHERE id=$1`, hero)
	gloom := monsterIDsByKeyOrName(camp, "Test Gloomling")[0]
	light := func(l string) {
		if c, r := pgCall(t, "POST", "/api/gm/set-lighting", gm, map[string]interface{}{"campaign_id": camp, "lighting": l}); c != 200 {
			t.Fatalf("set lighting %s: %d %v", l, c, r)
		}
	}
	light("bright")
	if _, line := narrateAs(t, gm, camp, gloom, "Claw", fmt.Sprint(hero), ""); !strings.Contains(line, "Watering Eyes") {
		t.Errorf("Bright Light: no Disadvantage from Watering Eyes: %s", line)
	}
	if w := (checkCtx{lobby: camp, who: gloom}).sources(); !w.hasDis("Watering Eyes") {
		t.Errorf("Bright Light: its check has no Disadvantage: %+v", w)
	}
	light("dim")
	if _, line := narrateAs(t, gm, camp, gloom, "Claw", fmt.Sprint(hero), ""); strings.Contains(line, "Watering Eyes") {
		t.Errorf("Dim Light gave Disadvantage: %s", line)
	}
	turnStart := func() string {
		t.Helper()
		for n := 0; n < 12; n++ {
			_, j := pgCall(t, "POST", cp+"/combat/next", gm, map[string]interface{}{})
			f, err := readFight(db, camp)
			if err != nil {
				t.Fatal(err)
			}
			if asInt(f.current()["id"]) == gloom {
				return fmt.Sprint(j)
			}
		}
		t.Fatal("the gloomling's turn never came")
		return ""
	}
	light("sunlight")
	before := monsterEntryVitals(camp, gloom).HP
	if res := turnStart(); monsterEntryVitals(camp, gloom).HP != before-10 {
		t.Errorf("sunlight at its turn start: HP %d -> %d: %s", before, monsterEntryVitals(camp, gloom).HP, res)
	}
	light("bright")
	before = monsterEntryVitals(camp, gloom).HP
	if turnStart(); monsterEntryVitals(camp, gloom).HP != before {
		t.Errorf("Bright Light (not sunlight) burned it: %d -> %d", before, monsterEntryVitals(camp, gloom).HP)
	}
	b, _ := json.Marshal(monsterStatBlock(key)["traits"])
	if strings.Count(string(b), `"server_applies":true`) != 2 {
		t.Errorf("the GM's trait view: %s", b)
	}
}

const (
	smokeText  = "The smoker hurls a pot. Choking smoke fills a 15-foot-radius Sphere it can see for 1 minute; the area is Heavily Obscured."
	blightText = "No wound closes within 30 feet of the blightwood."
)

// TestZoneMechanicProblems: the effect and trait fits of the two zone kinds and their checks.
func TestZoneMechanicProblems(t *testing.T) {
	cases := []struct{ group, mechanics, want string }{
		{"action", `[{"kind":"obscuring_zone","area":"heavily_obscured","shape":"cube","size":20,"at":"self"}]`, ""},
		{"bonus", `[{"kind":"obscuring_zone","area":"lightly_obscured","shape":"sphere","size":10,"at":"point","minutes":10}]`, ""},
		{"trait", `[{"kind":"obscuring_zone","area":"heavily_obscured","shape":"cube","size":20,"at":"self"}]`, "attaches to an action, Bonus Action or Legendary Action"},
		{"reaction", `[{"kind":"obscuring_zone","area":"heavily_obscured","shape":"cube","size":20,"at":"self"}]`, "attaches to an action, Bonus Action or Legendary Action"},
		{"action", `[{"kind":"obscuring_zone","area":"heavily_obscured","shape":"cone","size":20,"at":"self"}]`, "shape must be one of sphere, cube"},
		{"action", `[{"kind":"obscuring_zone","area":"heavily_obscured","shape":"cube","size":22,"at":"self"}]`, "size must be a multiple of 5"},
		{"trait", `[{"kind":"no_healing_aura","feet":30}]`, ""},
		{"action", `[{"kind":"no_healing_aura","feet":30}]`, "attaches to a trait"},
		{"trait", `[{"kind":"no_healing_aura","feet":33}]`, "feet must be a multiple of 5"},
	}
	for _, c := range cases {
		got := mechanicProblem(c.group, srdMonsterEntry{Name: "x", Text: smokeText, Mechanics: mechanicsFromJSON(t, c.mechanics)})
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s %s: got %q, want %q", c.group, c.mechanics, got, c.want)
		}
	}
}

// TestPostgresZoneMechanicsInPlay: the smoke at the square the GM aims at, as the obscured-area owner records it; no healing within
// 30 feet of the blightwood on the battle map (the heal owner refuses it), and healing again once the creature is beyond it.
func TestPostgresZoneMechanicsInPlay(t *testing.T) {
	testPostgres(t)
	smoke := srdMonsterEntry{Name: "Smoke Pot (1/Day)", Text: smokeText, Mechanics: []entryMechanic{{"kind": "obscuring_zone", "area": "heavily_obscured", "shape": "sphere",
		"size": float64(15), "at": "point", "minutes": float64(1), "covers": smokeText}}}
	gm, _, camp, hero, blight := earlyRulesFixture(t, "Test Blightwood", []srdMonsterEntry{smoke})
	entry, _, _ := monsterCombatant(camp, blight)
	key := fmt.Sprint(entry["monster_key"])
	pgExec(t, `UPDATE custom_monsters SET definition = jsonb_set(definition, '{traits}', $2::jsonb) WHERE key=$1`, key,
		`[{"name": "Blight", "text": "`+blightText+`", "mechanics": [{"kind": "no_healing_aura", "feet": 30, "covers": "`+blightText+`"}]}]`)
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	putMap := func(heroAt int) {
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"..........", "..........", ".........."}, Tokens: []mapToken{
			{ID: blight, Name: "blight", Position: mapSquare{0, 0}, Size: 1}, {ID: hero, Name: "hero", Position: mapSquare{heroAt, 0}, Size: 1}}}, "forced_movement": true}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	putMap(2) // 10 feet away
	c, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
		"monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", blight), "action": "Smoke Pot", "point": []int{5, 1}}})
	line := fmt.Sprint(r["monster_action_result"])
	areas := obscuredAreas(camp)
	if c != 200 || len(areas) != 1 || !areas[0].Heavy || areas[0].Radius != 15 || areas[0].Center != (mapSquare{5, 1}) || areas[0].Rounds != 10 ||
		!strings.Contains(line, "the area is established") || strings.Contains(line, "GM resolves") {
		t.Fatalf("Smoke Pot: %d %+v %s", c, areas, line)
	}
	pgExec(t, `UPDATE characters SET hp=10, max_hp=100 WHERE id=$1`, hero)
	if cant, why := cantRegainHP(camp, hero); !cant || !strings.Contains(why, "within 30 feet") {
		t.Fatalf("within 30 feet: %v %q", cant, why)
	}
	if h, _ := healHP(hero, 20); h.After.HP != 10 {
		t.Errorf("a heal within the aura: %+v", h)
	}
	putMap(8) // 40 feet away
	if h, _ := healHP(hero, 20); h.After.HP != 30 {
		t.Errorf("a heal beyond the aura: %+v", h)
	}
}

const witherText = "Melee Attack Roll: +60, reach 5 ft. Hit: 1 (1d1) Necrotic damage. The target is cursed: its wounds won't close, it withers by 2d6 Hit Points a day, and it is sickened."

func witherTouch() srdMonsterEntry {
	return srdMonsterEntry{Name: "Withering Touch", Text: witherText, Mechanics: []entryMechanic{{"kind": "curse", "no_regain": true, "max_hp_loss": "2D6",
		"conditions": []interface{}{"poisoned"}, "note": "the wound smells of grave soil",
		"covers": "The target is cursed: its wounds won't close, it withers by 2d6 Hit Points a day, and it is sickened."}}}
}

// TestCurseMechanicProblems: the curse kind's refusals, and its sentences read back by the curse owner as the flags they stand for.
func TestCurseMechanicProblems(t *testing.T) {
	cases := []struct{ mechanics, want string }{
		{`[{"kind":"curse"}]`, ""},
		{`[{"kind":"curse","hours":24,"no_regain":true,"no_rest_benefit":"short_and_long","max_hp_loss":"3d6","conditions":["charmed","poisoned"]}]`, ""},
		{`[{"kind":"curse","no_regain":"yes"}]`, "no_regain must be true or false"},
		{`[{"kind":"curse","max_hp_loss":"3d6+2"}]`, "max_hp_loss must be dice such as 3d6"},
		{`[{"kind":"curse","max_hp_loss":"lots"}]`, "max_hp_loss must be dice"},
		{`[{"kind":"curse","no_rest_benefit":"all"}]`, "no_rest_benefit must be one of short, long, short_and_long"},
		{`[{"kind":"curse","conditions":[]}]`, "conditions must be a list of 1 or more"},
	}
	for _, c := range cases {
		got := mechanicProblem("action", srdMonsterEntry{Name: "x", Text: ashClawText, Mechanics: mechanicsFromJSON(t, c.mechanics)})
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.mechanics, got, c.want)
		}
	}
	m := mechanicsFromJSON(t, `[{"kind":"curse","hours":24,"no_regain":true,"no_rest_benefit":"short_and_long","max_hp_loss":"3d6","conditions":["charmed","poisoned"]}]`)[0]
	e, ok := parseHitRider(curseSentences(m))
	if !ok || e.Curse == nil || e.Curse.Rounds != 24*600 || !e.CurseLinked || strings.Join(e.Conditions, ",") != "charmed,poisoned" ||
		strings.Join(e.Curse.Flags, ",") != "no_regain,short_rest,long_rest,hpmax=3d6" || strings.TrimSpace(e.Extra) != "" {
		t.Errorf("the curse owner reads %q as %+v %+v", curseSentences(m), e.Curse, e)
	}
	w := witherTouch()
	if p := mechanicProblem("action", w); p != "" || w.Mechanics[0].str("max_hp_loss") != "2d6" {
		t.Fatalf("%q %v", p, w.Mechanics[0])
	}
}

// TestPostgresCurseMechanicOnEveryTarget: the attached curse on a character and a monster: the cursed record, its linked condition, no
// healing; a second hit starts no second count; Remove Curse ends the curse and the condition with it.
func TestPostgresCurseMechanicOnEveryTarget(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, wight := earlyRulesFixture(t, "Test Wither Wight", []srdMonsterEntry{witherTouch()})
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Wither Victim", "monster_key": "goblin-warrior", "hp": 1000, "ac": 1, "initiative": -600}}})
	victim := monsterIDsByKeyOrName(camp, "Wither Victim")[0]
	for _, tg := range []struct {
		id   int
		name string
	}{{hero, fmt.Sprint(hero)}, {victim, "Wither Victim"}} {
		_, line := narrateHit(t, gm, camp, wight, "Withering Touch", tg.name, "")
		if !hasCondMarker(camp, tg.id, "cursed") || !hasCondMarker(camp, tg.id, "poisoned") || !strings.Contains(line, "grave soil") || strings.Contains(line, "withers by") {
			t.Errorf("%s: cursed %v poisoned %v: %s", tg.name, hasCondMarker(camp, tg.id, "cursed"), hasCondMarker(camp, tg.id, "poisoned"), line)
		}
		if cant, _ := cantRegainHP(camp, tg.id); !cant {
			t.Errorf("%s regains Hit Points while cursed", tg.name)
		}
		if !holdsHPMaxCurse(camp, tg.id) {
			t.Errorf("%s: the Hit Point maximum count did not start", tg.name)
		}
		if _, line = narrateHit(t, gm, camp, wight, "Withering Touch", tg.name, ""); !strings.Contains(line, "already cursed") || len(recordsOf(camp, tg.id, "cursed")) != 1 {
			t.Errorf("%s: a second hit: %s", tg.name, line)
		}
		endCursesBySpell(camp, "remove-curse", []int{tg.id})
		if hasCondMarker(camp, tg.id, "cursed") || hasCondMarker(camp, tg.id, "poisoned") {
			t.Errorf("%s: Remove Curse left the curse or its condition", tg.name)
		}
	}
}

const gulpText = "Dexterity Saving Throw: DC 30, one creature within 5 feet. Failure: The target is drawn inside the blob, blinded and dissolving."

func gulp() srdMonsterEntry {
	return srdMonsterEntry{Name: "Gulp", Text: gulpText, Mechanics: []entryMechanic{{"kind": "engulf", "escape_dc": float64(5), "also": []interface{}{"blinded"},
		"damage": "2d6", "damage_type": "acid", "note": "Total Cover from outside", "covers": "The target is drawn inside the blob, blinded and dissolving."}}}
}

// TestEngulfMechanicProblems: the save fit and the engulf's parameters; its sentences read back as the condition, what lasts with it.
func TestEngulfMechanicProblems(t *testing.T) {
	cases := []struct{ group, text, mechanics, want string }{
		{"action", gulpText, `[{"kind":"engulf","escape_dc":12}]`, ""},
		{"action", ashClawText, `[{"kind":"engulf","escape_dc":12}]`, "attaches to an entry with a saving throw"},
		{"trait", gulpText, `[{"kind":"engulf","escape_dc":12}]`, "attaches to an action, Bonus Action or Legendary Action with a saving throw"},
		{"action", gulpText, `[{"kind":"engulf"}]`, "escape_dc is required"},
		{"action", gulpText, `[{"kind":"engulf","escape_dc":12,"damage":"2d6"}]`, "damage needs damage_type"},
		{"action", gulpText, `[{"kind":"engulf","escape_dc":12,"damage_type":"acid"}]`, "damage_type is only taken with damage"},
		{"action", gulpText, `[{"kind":"engulf","escape_dc":12,"escape_skill":"stealth"}]`, "escape_skill must be one of athletics, acrobatics"},
	}
	for _, c := range cases {
		got := mechanicProblem(c.group, srdMonsterEntry{Name: "x", Text: c.text, Mechanics: mechanicsFromJSON(t, c.mechanics)})
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s %s: got %q, want %q", c.group, c.mechanics, got, c.want)
		}
	}
	e, ok := parseHitRider(engulfSentences(gulp().Mechanics[0]))
	if !ok || strings.Join(e.Conditions, ",") != "restrained" || e.Lasting == nil || strings.Join(e.Lasting.Also, ",") != "blinded" || e.Lasting.Tick == nil || strings.TrimSpace(e.Extra) != "" {
		t.Errorf("engulf sentences %q read as %+v", engulfSentences(gulp().Mechanics[0]), e)
	}
}

// TestPostgresEngulfMechanicInPlay: a failed save engulfs the character (Restrained, Blinded while Restrained, the escape on the
// Restrained record) and a monster; the character's escape check ends both conditions.
func TestPostgresEngulfMechanicInPlay(t *testing.T) {
	testPostgres(t)
	gm, player, camp, hero, blob := earlyRulesFixture(t, "Test Gulp Blob", []srdMonsterEntry{gulp()})
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Gulp Victim", "monster_key": "goblin-warrior", "hp": 1000, "ac": 1, "initiative": -600}}})
	victim := monsterIDsByKeyOrName(camp, "Gulp Victim")[0]
	for _, tg := range []struct {
		id   int
		name string
	}{{hero, fmt.Sprint(hero)}, {victim, "Gulp Victim"}} {
		c, line := narrateAs(t, gm, camp, blob, "Gulp", tg.name, "")
		if c != 200 || !hasCondMarker(camp, tg.id, "restrained") || !hasCondMarker(camp, tg.id, "blinded") || !strings.Contains(line, "DC 5 Athletics") ||
			!strings.Contains(line, "Total Cover") || strings.Contains(line, "dissolving") {
			t.Errorf("Gulp at %s: %d restrained %v blinded %v: %s", tg.name, c, hasCondMarker(camp, tg.id, "restrained"), hasCondMarker(camp, tg.id, "blinded"), line)
		}
	}
	if g, _ := gatedEffectsOn(camp, hero, "recurring_damage"); len(g) != 1 || g[0].Gate != "restrained" || !strings.Contains(g[0].Text, "2d6") {
		t.Errorf("the acid at the start of each of its turns, while Restrained: %+v", g)
	}
	rs := recordsOf(camp, hero, "restrained")
	if len(rs) != 1 || len(rs[0].Escapes) != 1 || rs[0].Escapes[0].DC != 5 || rs[0].Escapes[0].Skill != "athletics" || !rs[0].Escapes[0].Adjacent {
		t.Fatalf("the Restrained record: %+v", rs)
	}
	var c int
	var j map[string]interface{}
	for i := 0; i < 40 && hasCondMarker(camp, hero, "restrained"); i++ { // a failed check leaves it: try again on a new turn, as a player would
		pgExec(t, `UPDATE characters SET action_used=false WHERE id=$1`, hero)
		c, j = pgCall(t, "POST", "/api/conditions/escape", player, map[string]interface{}{"character_id": hero, "record_id": rs[0].ID, "escape": 0})
	}
	if c != 200 || hasCondMarker(camp, hero, "restrained") || hasCondMarker(camp, hero, "blinded") {
		t.Errorf("the escape: %d %v; restrained %v blinded %v", c, j, hasCondMarker(camp, hero, "restrained"), hasCondMarker(camp, hero, "blinded"))
	}
}

const slumberText = "Melee Attack Roll: +60, reach 5 ft. Hit: 1 (1d1) Piercing damage. The target falls into a deep slumber for 1 minute unless something wakes it."

func slumberBite() srdMonsterEntry {
	return srdMonsterEntry{Name: "Slumber Bite", Text: slumberText, Mechanics: []entryMechanic{{"kind": "condition_on_hit", "conditions": []interface{}{"unconscious"},
		"until": "minutes", "minutes": float64(1), "ends_on_damage": true, "covers": "The target falls into a deep slumber for 1 minute unless something wakes it."}}}
}

func TestConditionOnHitProblems(t *testing.T) {
	cases := []struct{ mechanics, want string }{
		{`[{"kind":"condition_on_hit","conditions":["prone"],"until":"end_of_its_next_turn","max_size":"medium"}]`, ""},
		{`[{"kind":"condition_on_hit","conditions":["prone"]}]`, "until is required"},
		{`[{"kind":"condition_on_hit","conditions":[],"until":"end_of_its_next_turn"}]`, "conditions must be a list of 1 or more"},
		{`[{"kind":"condition_on_hit","conditions":["prone"],"until":"end_of_its_next_turn","ends_on_damage":true}]`, "ends_on_damage is only taken with until minutes"},
	}
	for _, c := range cases {
		got := mechanicProblem("action", srdMonsterEntry{Name: "x", Text: ashClawText, Mechanics: mechanicsFromJSON(t, c.mechanics)})
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.mechanics, got, c.want)
		}
	}
	m := mechanicsFromJSON(t, `[{"kind":"condition_on_hit","conditions":["prone","grappled"],"until":"end_of_attackers_next_turn","max_size":"large"}]`)[0]
	if e, ok := parseHitRider(conditionHitSentence(m.list("conditions"), m)); !ok || strings.Join(e.Conditions, ",") != "prone,grappled" || !e.UntilEnd || e.CondMaxSize != "large" {
		t.Errorf("%q read as %+v", conditionHitSentence(m.list("conditions"), m), e)
	}
}

func TestPostgresConditionOnHitEndsOnDamage(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, viper := earlyRulesFixture(t, "Test Slumber Viper", []srdMonsterEntry{slumberBite(), {Name: "Nip", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 1 (1d1) Piercing damage."}})
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Slumber Victim", "monster_key": "goblin-warrior", "hp": 1000, "ac": 1, "initiative": -600}}})
	victim := monsterIDsByKeyOrName(camp, "Slumber Victim")[0]
	for _, tg := range []struct {
		id   int
		name string
	}{{hero, fmt.Sprint(hero)}, {victim, "Slumber Victim"}} {
		_, line := narrateHit(t, gm, camp, viper, "Slumber Bite", tg.name, "")
		if !hasCondMarker(camp, tg.id, "unconscious") || strings.Contains(line, "deep slumber") {
			t.Errorf("%s: Slumber Bite: %s", tg.name, line)
			continue
		}
		if rs := recordsOf(camp, tg.id, "unconscious"); len(rs) != 1 || rs[0].DurationRounds != 10 {
			t.Errorf("%s: Unconscious for 1 minute: %+v", tg.name, rs)
		}
		narrateHit(t, gm, camp, viper, "Nip", tg.name, "")
		if hasCondMarker(camp, tg.id, "unconscious") {
			t.Errorf("%s: damage did not wake it", tg.name)
		}
	}
}

func TestRollRiderSentencesReadBack(t *testing.T) {
	want := map[string]string{"exposed": "exposed", "disadvantage_attacks": "attacks", "disadvantage_attacks_and_checks": "attacks,checks",
		"disadvantage_next_attack": "nextattack", "no_reactions": "noreactions", "self_advantage_next_attack": "selfnextadv"}
	for rider, modes := range want {
		for _, until := range []string{"", "start_of_attackers_next_turn", "end_of_attackers_next_turn"} {
			if (rider == "exposed" && until == "end_of_attackers_next_turn") || (rider == "self_advantage_next_attack" && until != "") {
				continue // refused by the row's check (below)
			}
			m := entryMechanic{"kind": "roll_rider", "rider": rider}
			if until != "" {
				m["until"] = until
			}
			e, ok := parseHitRider(rollRiderSentence(m))
			if !ok || strings.Join(e.DisadvMode, ",") != modes || strings.TrimSpace(e.Extra) != "" {
				t.Errorf("%s %s: %q read as %v (extra %q)", rider, until, rollRiderSentence(m), e.DisadvMode, e.Extra)
			}
		}
	}
	for js, want := range map[string]string{
		`[{"kind":"roll_rider","rider":"confused"}]`:                                                  "rider must be one of",
		`[{"kind":"roll_rider","rider":"exposed","until":"end_of_its_next_turn"}]`:                    "exposed lasts until the start of the attacker's next turn",
		`[{"kind":"roll_rider","rider":"self_advantage_next_attack","until":"end_of_its_next_turn"}]`: "leave until out",
	} {
		if p := mechanicProblem("action", srdMonsterEntry{Name: "x", Text: ashClawText, Mechanics: mechanicsFromJSON(t, js)}); !strings.Contains(p, want) {
			t.Errorf("%s: %q", js, p)
		}
	}
}

func TestPostgresRollRiderOnHit(t *testing.T) {
	testPostgres(t)
	glare := srdMonsterEntry{Name: "Glare Strike", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 1 (1d1) Radiant damage. The target is dazzled by the glare.",
		Mechanics: []entryMechanic{{"kind": "roll_rider", "rider": "disadvantage_attacks", "covers": "The target is dazzled by the glare."}}}
	mark := srdMonsterEntry{Name: "Marking Strike", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 1 (1d1) Radiant damage. The target glows, easy to strike.",
		Mechanics: []entryMechanic{{"kind": "roll_rider", "rider": "exposed", "until": "start_of_attackers_next_turn", "covers": "The target glows, easy to strike."}}}
	gm, _, camp, hero, lamp := earlyRulesFixture(t, "Test Glare Lamp", []srdMonsterEntry{glare, mark})
	if _, line := narrateHit(t, gm, camp, lamp, "Glare Strike", fmt.Sprint(hero), ""); !hasCondMarker(camp, hero, "disadv_attacks") || strings.Contains(line, "dazzled") {
		t.Errorf("Glare Strike: %s", line)
	}
	if _, line := narrateHit(t, gm, camp, lamp, "Marking Strike", fmt.Sprint(hero), ""); !anyContains(combatEffectNames(camp, hero), "xposed") || strings.Contains(line, "glows") {
		t.Errorf("Marking Strike: %s", line)
	}
}

func TestPostgresEvasionAndRegainMechanics(t *testing.T) {
	testPostgres(t)
	leech := srdMonsterEntry{Name: "Leeching Bite", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 10 (1d1 + 9) Piercing damage. The leech grows fat on the blood it drinks.",
		Mechanics: []entryMechanic{{"kind": "regain_from_damage", "share": "half", "covers": "The leech grows fat on the blood it drinks."}}}
	gm, _, camp, hero, ghoul := earlyRulesFixture(t, "Test Leech Sprite", []srdMonsterEntry{leech})
	entry, _, _ := monsterCombatant(camp, ghoul)
	pgExec(t, `UPDATE custom_monsters SET definition = jsonb_set(definition, '{traits}', $2::jsonb) WHERE key=$1`, fmt.Sprint(entry["monster_key"]),
		`[{"name": "Slippery", "text": "The sprite twists out of the way of blasts.", "mechanics": [{"kind": "evasion", "covers": "The sprite twists out of the way of blasts."}]}]`)
	if !creatureEvasion(camp, ghoul, "dex") || creatureEvasion(camp, ghoul, "con") {
		t.Errorf("the attached evasion: dex %v con %v", creatureEvasion(camp, ghoul, "dex"), creatureEvasion(camp, ghoul, "con"))
	}
	setMonsterHP(camp, ghoul, 100) // of 500
	_, line := narrateHit(t, gm, camp, ghoul, "Leeching Bite", fmt.Sprint(hero), "")
	if hp := monsterEntryVitals(camp, ghoul).HP; hp < 105 || hp > 110 || !strings.Contains(line, "regains") {
		t.Errorf("regain half of 10 (20 on a critical): HP %d: %s", hp, line)
	}
	if p := mechanicProblem("action", srdMonsterEntry{Name: "x", Text: ashClawText, Mechanics: mechanicsFromJSON(t, `[{"kind":"regain_from_damage"}]`)}); !strings.Contains(p, "share is required") {
		t.Errorf("share left out: %q", p)
	}
}

func TestPostgresBurningMechanic(t *testing.T) {
	testPostgres(t)
	torch := srdMonsterEntry{Name: "Torch Swipe", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 1 (1d1) Fire damage. Flames catch on the target's clothes.",
		Mechanics: []entryMechanic{{"kind": "burning", "covers": "Flames catch on the target's clothes."}}}
	spit := srdMonsterEntry{Name: "Oil Spit", Text: "Dexterity Saving Throw: DC 30, one creature within 30 feet. Failure: 1 (1d1) Fire damage, and the oil on the target ignites.",
		Mechanics: []entryMechanic{{"kind": "burning", "covers": "and the oil on the target ignites."}}}
	gm, _, camp, hero, imp := earlyRulesFixture(t, "Test Torch Imp", []srdMonsterEntry{torch, spit})
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Oil Victim", "monster_key": "goblin-warrior", "hp": 1000, "ac": 1, "initiative": -600}}})
	victim := monsterIDsByKeyOrName(camp, "Oil Victim")[0]
	if _, line := narrateHit(t, gm, camp, imp, "Torch Swipe", fmt.Sprint(hero), ""); !isBurning(camp, hero) || strings.Contains(line, "catch on") {
		t.Errorf("Torch Swipe: %s", line)
	}
	if c, line := narrateAs(t, gm, camp, imp, "Oil Spit", "Oil Victim", ""); c != 200 || !isBurning(camp, victim) || strings.Contains(line, "ignites") {
		t.Errorf("Oil Spit at a monster: %d %s", c, line)
	}
	if p := mechanicProblem("trait", srdMonsterEntry{Name: "x", Text: "It glows.", Mechanics: mechanicsFromJSON(t, `[{"kind":"burning"}]`)}); !strings.Contains(p, "attack roll or a saving throw") {
		t.Errorf("burning on a trait: %q", p)
	}
}

func TestPostgresGrappleMechanic(t *testing.T) {
	testPostgres(t)
	vine := srdMonsterEntry{Name: "Coiling Vine", Text: "Melee Attack Roll: +60, reach 10 ft. Hit: 1 (1d1) Bludgeoning damage. The vine coils around a Large or smaller target (escape DC 13) and binds it.",
		Mechanics: []entryMechanic{{"kind": "grapple", "escape_dc": float64(13), "max_size": "large", "restrained": true, "note": "one target at a time",
			"covers": "The vine coils around a Large or smaller target (escape DC 13) and binds it."}}}
	gm, _, camp, hero, shrub := earlyRulesFixture(t, "Test Coil Shrub", []srdMonsterEntry{vine})
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Coil Victim", "monster_key": "goblin-warrior", "hp": 1000, "ac": 1, "initiative": -600},
		{"name": "Coil Giant", "monster_key": "hill-giant", "hp": 1000, "ac": 1, "initiative": -601}}})
	victim := monsterIDsByKeyOrName(camp, "Coil Victim")[0]
	giant := monsterIDsByKeyOrName(camp, "Coil Giant")[0]
	for _, tg := range []struct {
		id   int
		name string
	}{{hero, fmt.Sprint(hero)}, {victim, "Coil Victim"}} {
		_, line := narrateHit(t, gm, camp, shrub, "Coiling Vine", tg.name, "")
		if !hasCondMarker(camp, tg.id, "grappled") || !hasCondMarker(camp, tg.id, "restrained") || !strings.Contains(line, "one target at a time") || strings.Contains(line, "coils around") {
			t.Errorf("%s: %s", tg.name, line)
		}
		if rs := recordsOf(camp, tg.id, "grappled"); len(rs) != 1 || len(rs[0].Escapes) == 0 || rs[0].Escapes[0].DC != 13 {
			t.Errorf("%s: the grapple's escape: %+v", tg.name, rs)
		}
	}
	if _, line := narrateHit(t, gm, camp, shrub, "Coiling Vine", "Coil Giant", ""); hasCondMarker(camp, giant, "grappled") {
		t.Errorf("a Huge target was grappled: %s", line)
	}
}

func TestPostgresTeleportAndSelfConditionMechanics(t *testing.T) {
	testPostgres(t)
	blink := srdMonsterEntry{Name: "Blink Away", Text: "The sprite blinks away in a shower of sparks and fades from sight.",
		Mechanics: []entryMechanic{{"kind": "teleport", "feet": float64(30), "covers": "The sprite blinks away in a shower of sparks"},
			{"kind": "self_condition", "conditions": []interface{}{"invisible"}, "until": "start_of_its_next_turn", "covers": "and fades from sight."}}}
	gm, _, camp, _, sprite := earlyRulesFixture(t, "Test Blink Sprite", []srdMonsterEntry{blink})
	c, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
		"monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", sprite), "action": "Blink Away"}})
	line := fmt.Sprint(j["monster_action_result"])
	if c != 200 || !strings.Contains(line, "up to 30 ft") || !hasCondMarker(camp, sprite, "invisible") || strings.Contains(line, "GM resolves") || strings.Contains(line, "sparks") {
		t.Fatalf("Blink Away: %d invisible %v: %s", c, hasCondMarker(camp, sprite, "invisible"), line)
	}
	if v, ok := specialMoves.Load(creatureMoveKey{camp, sprite}); !ok || v.(specialMove).Feet != 30 || !v.(specialMove).Teleport {
		t.Errorf("the teleport granted: %v %v", v, ok)
	}
	if rs := recordsOf(camp, sprite, "invisible"); len(rs) != 1 || rs[0].Notes != notesUntilStartOfSourceTurn {
		t.Errorf("Invisible until the start of its next turn: %+v", rs)
	}
}
