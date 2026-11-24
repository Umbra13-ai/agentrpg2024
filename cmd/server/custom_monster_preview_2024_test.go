package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// invented, SRD-worded and not: the fixture's Slam is read by the attack owner; "Mournful Chime" is wording no rule reads
func previewFixture() srdMonster2024 {
	m := compendiumFixture()
	m.Name = "Test Dirge Warden"
	m.Traits = append(m.Traits, srdMonsterEntry{Name: "Dread Presence", Text: "Creatures that start their turn near the warden are filled with dread."})
	m.Actions = append(m.Actions, srdMonsterEntry{Name: "Mournful Chime", Text: "The warden sounds a chime that stirs the restless dead nearby."})
	return m
}

func TestNormalizeAndWarnCustomMonster(t *testing.T) {
	m := compendiumFixture()
	m.Abilities["wis"] = abilityLine{Score: 18} // a save left out where the modifier is +4
	m.Abilities["int"] = abilityLine{Score: 10} // a save of 0 where the modifier is 0: nothing to fill in
	notes := normalizeCustomMonster(&m)
	if m.Abilities["wis"].Save != 4 || m.Abilities["int"].Save != 0 || m.Abilities["str"].Save != 8 {
		t.Fatalf("saves after normalizing: %+v", m.Abilities)
	}
	if len(notes) != 2 || !strings.Contains(notes[0], "WIS") || !strings.Contains(notes[1], "initiative left out") { // the fixture gives no initiative either
		t.Fatalf("notes %v", notes)
	}
	if w := customMonsterWarnings(m); len(w) != 2 { // the two traits the server applies by name
		for _, x := range w {
			t.Log(x)
		}
		t.Fatalf("warnings on a well-formed monster: %d", len(w))
	}
	bad := compendiumFixture()
	bad.LegendaryIn = ""
	bad.Traits = []srdMonsterEntry{{Name: "Legendary Resistance", Text: "The warden can choose to succeed on a failed saving throw."}}
	bad.Speed = ""
	bad.Abilities["dex"] = abilityLine{Score: 20, Save: 1}
	joined := strings.Join(customMonsterWarnings(bad), "\n")
	for _, want := range []string{"Legendary Action Uses", "Legendary Resistance (3/Day", "speed is empty", "DEX saving throw +1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no warning says %q:\n%s", want, joined)
		}
	}
}

// TestPostgresCompendiumPreview: the preview saves nothing; the import's and the edit's reply, the preview and the GM's stat block
// are one verdict; and a save left out is the ability modifier in the stored monster.
func TestPostgresCompendiumPreview(t *testing.T) {
	testPostgres(t)
	isolateCustomMonsters(t)
	_, gm := pgAgent(t, "preview gm")
	_, player := pgAgent(t, "preview player")
	if c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Preview test", "max_players": 4}); c != 200 {
		t.Fatalf("campaign %d %v", c, j)
	}
	path := "/api/gm/compendium/monsters"
	m := previewFixture()
	m.Abilities["wis"] = abilityLine{Score: 18} // save left out
	count := func() int {
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM custom_monsters`).Scan(&n)
		return n
	}

	// access: a GM only, POST only
	if c, _ := pgCall(t, "POST", path+"/preview", player, map[string]interface{}{"monsters": []srdMonster2024{m}}); c != 403 {
		t.Fatalf("a player previewed: %d", c)
	}
	if c, _ := pgCall(t, "POST", path+"/preview", "", map[string]interface{}{"monsters": []srdMonster2024{m}}); c != 401 {
		t.Fatalf("anonymous previewed: %d", c)
	}
	if c, _ := pgCall(t, "GET", path+"/preview", gm, nil); c != 405 {
		t.Fatalf("GET preview: %d", c)
	}
	if c, _ := pgCall(t, "POST", path+"/preview", gm, map[string]interface{}{"monsters": []srdMonster2024{}}); c != 400 {
		t.Fatalf("empty preview: %d", c)
	}

	// the preview itself
	before := count()
	sent := map[string]interface{}{} // the WIS line as a client leaves its save out: no "Save" key (a written 0 is a +0 save)
	b, _ := json.Marshal(m)
	json.Unmarshal(b, &sent)
	sent["abilities"].(map[string]interface{})["wis"] = map[string]interface{}{"Score": 18}
	c, j := pgCall(t, "POST", path+"/preview", gm, map[string]interface{}{"monsters": []interface{}{sent}})
	if c != 200 || j["saved"] != false || count() != before {
		t.Fatalf("preview %d %v (rows %d -> %d)", c, j, before, count())
	}
	p := j["monsters"].([]interface{})[0].(map[string]interface{})
	if p["valid"] != true || !strings.Contains(fmt.Sprint(p["notes"]), "WIS saving throw left out") {
		t.Fatalf("preview of a valid monster %v", p)
	}
	entry := func(name string) map[string]interface{} {
		for _, e := range p["entries"].([]interface{}) {
			if e.(map[string]interface{})["name"] == name {
				return e.(map[string]interface{})
			}
		}
		t.Fatalf("no entry %q in %v", name, p["entries"])
		return nil
	}
	if a := entry("Slam")["applied"].([]interface{}); len(a) == 0 {
		t.Fatalf("the attack owner reads nothing of the Slam: %v", entry("Slam"))
	}
	chime := entry("Mournful Chime")["gm_resolves"].([]interface{})
	if len(chime) != 1 || !strings.Contains(fmt.Sprint(chime[0]), "stirs the restless dead") {
		t.Fatalf("a sentence no rule reads is not the GM's: %v", entry("Mournful Chime"))
	}
	if g := entry("Dread Presence")["gm_resolves"].([]interface{}); len(g) != 1 {
		t.Fatalf("a trait no rule reads is not the GM's: %v", entry("Dread Presence"))
	}
	if a := entry("Magic Resistance")["applied"].([]interface{}); len(a) == 0 {
		t.Fatal("the Magic Resistance row is not said to apply")
	}
	// an invalid monster says so and the rest still report
	bad := m
	bad.HP = 0
	c, j = pgCall(t, "POST", path+"/preview", gm, map[string]interface{}{"monsters": []srdMonster2024{bad, m}})
	if c != 200 {
		t.Fatalf("preview with an invalid monster %d %v", c, j)
	}
	list := j["monsters"].([]interface{})
	if list[0].(map[string]interface{})["valid"] != false || list[1].(map[string]interface{})["valid"] != true {
		t.Fatalf("validity per monster: %v", list)
	}

	// import: the same notes and counts, and the save is stored as the modifier
	c, j = pgCall(t, "POST", path, gm, map[string]interface{}{"source_note": "preview test", "monsters": []interface{}{sent}})
	if c != 201 {
		t.Fatalf("import %d %v", c, j)
	}
	imported := j["monsters"].([]interface{})[0].(map[string]interface{})
	key := imported["monster_key"].(string)
	report := imported["report"].(map[string]interface{})
	if fmt.Sprint(report["summary"]) != fmt.Sprint(p["summary"]) || fmt.Sprint(report["notes"]) != fmt.Sprint(p["notes"]) || fmt.Sprint(report["warnings"]) != fmt.Sprint(p["warnings"]) {
		t.Fatalf("the import's report differs from the preview's:\n%v\n%v", report, p)
	}
	if _, has := report["entries"]; has {
		t.Fatal("an import's reply carries every sentence")
	}
	if got := monsterSaveBonus(key, "wis", 18); got != 4 {
		t.Fatalf("a save left out is stored as %d, not the ability modifier", got)
	}
	// the GM's stat block reads the same verdicts
	sb := monsterStatBlock(key)
	rules, ok := sb["entry_rules"].([]map[string]interface{})
	if !ok || len(rules) != len(p["entries"].([]interface{})) {
		t.Fatalf("the stat block's entry_rules: %v", sb["entry_rules"])
	}
	for i, e := range p["entries"].([]interface{}) {
		pe := e.(map[string]interface{})
		if fmt.Sprint(pe["applied"]) != fmt.Sprint(rules[i]["applied"]) || fmt.Sprint(pe["gm_resolves"]) != fmt.Sprint(rules[i]["gm_resolves"]) {
			t.Errorf("entry %q: the stat block and the preview disagree", pe["name"])
		}
	}
	// an edit reports as an import does
	m.Speed = ""
	c, j = pgCall(t, "POST", path+"/edit", gm, map[string]interface{}{"monster_key": key, "monster": m})
	if c != 201 || !strings.Contains(fmt.Sprint(j["report"]), "speed is empty") {
		t.Fatalf("edit report %d %v", c, j)
	}
}

// TestEntryReportsAreTheSRDCoverage: the report a GM reads is the verdict the SRD tests hold: over all 330 stat blocks its counts are the
// coverage test's.
func TestEntryReportsAreTheSRDCoverage(t *testing.T) {
	applied, gm := 0, 0
	for _, m := range srdMonsters2024() {
		for _, r := range entryReports(metaFromMonster(monsterSlug(m.Name), m)) {
			applied += len(r["applied"].([]map[string]interface{}))
			gm += len(r["gm_resolves"].([]map[string]interface{}))
		}
	}
	wantApplied, wantGM := 0, 0
	for _, v := range srdSentenceCoverage() {
		if v.Status == "applied" {
			wantApplied++
		} else {
			wantGM++
		}
	}
	if applied != wantApplied || gm != wantGM {
		t.Fatalf("reports: applied %d, GM %d; coverage: applied %d, GM %d", applied, gm, wantApplied, wantGM)
	}
}

// A written saving throw of +0 (a proficient save of a creature whose modifier is -2) is kept, from the JSON definition and from the text;
// only a save that isn't written is the ability modifier.
func TestWrittenZeroSaveIsKept(t *testing.T) {
	var m srdMonster2024
	if err := json.Unmarshal([]byte(`{"abilities": {"cha": {"Score": 7, "Mod": -2, "Save": 0}, "wis": {"Score": 7, "Mod": -2}, "int": {"score": 7, "mod": -2, "save": 0}}}`), &m); err != nil {
		t.Fatal(err)
	}
	normalizeCustomMonster(&m)
	if m.Abilities["cha"].Save != 0 || m.Abilities["int"].Save != 0 {
		t.Errorf("a written +0 save was replaced: cha %+d, int %+d", m.Abilities["cha"].Save, m.Abilities["int"].Save)
	}
	if m.Abilities["wis"].Save != -2 {
		t.Errorf("a save left out is the modifier: wis %+d", m.Abilities["wis"].Save)
	}
	// stored and read back, the written 0 is still written
	b, _ := json.Marshal(m)
	var again srdMonster2024
	json.Unmarshal(b, &again)
	normalizeCustomMonster(&again)
	if again.Abilities["cha"].Save != 0 {
		t.Errorf("read back: cha %+d", again.Abilities["cha"].Save)
	}
}

// An attack that play refuses whole (damage text with no amount) is a warning at import, from the same check play runs; a conditional extra
// amount is not refused (the primary damage is dealt and the GM is told the extra), so nothing is warned and its sentence is the GM's.
func TestImportWarnsOfAnAttackPlayRefuses(t *testing.T) {
	m := compendiumFixture()
	m.Actions = append(m.Actions, srdMonsterEntry{Name: "Knave Dagger", Text: "Melee Attack Roll: +5, reach 5 ft. Hit: 5 (1d4 + 3) Piercing damage plus extra Piercing damage."})
	found := false
	for _, w := range customMonsterWarnings(m) {
		if strings.Contains(w, `"Knave Dagger" will be refused in play`) {
			found = true
		}
	}
	if !found || attackEntryRefusal(m.Actions[len(m.Actions)-1]) == "" {
		t.Errorf("no warning for an attack play refuses: %v", customMonsterWarnings(m))
	}
	m.Actions[len(m.Actions)-1].Text = "Melee Attack Roll: +5, reach 5 ft. Hit: 5 (1d4 + 3) Piercing damage plus an extra 5 (2d4) Piercing damage if the knave has at least 18 Hit Points."
	if p := attackEntryRefusal(m.Actions[len(m.Actions)-1]); p != "" {
		t.Errorf("a conditional extra is refused: %s", p)
	}
	for _, w := range customMonsterWarnings(m) {
		if strings.Contains(w, "Knave Dagger") {
			t.Errorf("a warning for an attack that rolls: %s", w)
		}
	}
}
