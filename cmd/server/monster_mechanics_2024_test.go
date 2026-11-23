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
		e := srdMonsterEntry{Name: "Example", Text: ashClawText, Mechanics: mechanicsFromJSON(t, "["+string(b)+"]")}
		if p := mechanicProblem("action", e); p != "" {
			t.Errorf("the catalogue's own example of %s is refused: %s", k.Kind, p)
		}
		if k.describe == nil || k.Does == "" || k.AppliedBy == "" {
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
