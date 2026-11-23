package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// statBlockText writes a stat block out in the layout the SRD 5.2.1 prints (the reader's contract): the line under the name as printed,
// AC and Initiative, HP, Speed, the ability grid, the labelled lines, then each section with "Name. Text" entries.
func statBlockText(m srdMonster2024, typeLine, crLine string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n", m.Name, typeLine)
	fmt.Fprintf(&b, "AC %d Initiative %+d (%d)\nHP %d (%s)\nSpeed %s\n", m.AC, m.Initiative.Mod, m.Initiative.Score, m.HP, m.HPFormula, m.Speed)
	b.WriteString("MOD SAVE MOD SAVE MOD SAVE\n")
	row := func(keys ...string) {
		var parts []string
		for _, k := range keys {
			a := m.Abilities[k]
			parts = append(parts, fmt.Sprintf("%s %d %+d %+d", strings.ToUpper(k[:1])+k[1:], a.Score, a.Mod, a.Save))
		}
		b.WriteString(strings.Join(parts, " ") + "\n")
	}
	row("str", "dex", "con")
	row("int", "wis", "cha")
	label := func(name, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s %s\n", name, v)
		}
	}
	label("Skills", m.Skills)
	label("Vulnerabilities", m.Vulnerable)
	label("Resistances", m.Resistances)
	imm := m.DmgImmune
	if m.CondImmune != "" {
		if imm != "" {
			imm += "; "
		}
		imm += m.CondImmune
	}
	label("Immunities", imm)
	label("Gear", m.Gear)
	label("Senses", m.Senses)
	label("Languages", m.Languages)
	label("CR", crLine)
	section := func(title string, entries []srdMonsterEntry, intro string) {
		if len(entries) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s\n", title)
		if intro != "" {
			b.WriteString(intro + "\n")
		}
		for _, e := range entries {
			fmt.Fprintf(&b, "%s. %s\n", e.Name, e.Text)
		}
	}
	section("Traits", m.Traits, "")
	section("Actions", m.Actions, "")
	section("Bonus Actions", m.BonusActions, "")
	section("Reactions", m.Reactions, "")
	section("Legendary Actions", m.Legendary, m.LegendaryIn)
	return b.String()
}

// TestStatBlockTextRoundTrip: each of the 330 SRD 5.2.1 stat blocks, written out in the SRD's layout, reads back as the same stat block
// (the group is the book's, and a size of two ("Medium or Small") is the first, with a note).
func TestStatBlockTextRoundTrip(t *testing.T) {
	raw, err := os.ReadFile("data/srd521_monsters.json")
	if err != nil {
		t.Fatal(err)
	}
	var data []struct {
		srdMonster2024
		RawTypeLine string `json:"raw_type_line"`
		CRLine      string `json:"cr_line"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if len(data) != 330 {
		t.Fatalf("%d stat blocks", len(data))
	}
	failures := 0
	for _, d := range data {
		want := d.srdMonster2024
		want.Group = ""
		for _, g := range []*[]srdMonsterEntry{&want.Traits, &want.Actions, &want.BonusActions, &want.Reactions, &want.Legendary} {
			if len(*g) == 0 {
				*g = nil // the reader leaves a section it didn't see empty
			}
		}
		if first, _, two := strings.Cut(want.Size, " or "); two {
			want.Size = first
		}
		got, notes := readStatBlockText(statBlockText(d.srdMonster2024, d.RawTypeLine, d.CRLine))
		wj, _ := json.Marshal(want)
		gj, _ := json.Marshal(got)
		if string(wj) != string(gj) {
			failures++
			if failures <= 8 {
				t.Errorf("%s reads back differently (notes %v):\n want %s\n  got %s", want.Name, notes, trim(string(wj)), trim(string(gj)))
			}
		}
		for _, n := range notes {
			if !strings.Contains(n, "is two sizes") {
				t.Errorf("%s: unexpected note %q", want.Name, n)
			}
		}
	}
	if failures > 0 {
		t.Fatalf("%d of 330 stat blocks read back differently", failures)
	}
}

func trim(s string) string {
	if len(s) > 1500 {
		return s[:1500] + "…"
	}
	return s
}

// TestReadStatBlockTextEdges: layouts a GM's paste will have that the SRD's own print does not, and what is said when something is missing.
func TestReadStatBlockTextEdges(t *testing.T) {
	// an invented monster, wrapped lines, a Windows line end, a printed minus sign, a grid with no modifiers
	text := "Test Frost Warden\r\nMedium Construct, Unaligned\r\nAC 14\r\nInitiative \u22121 (9) HP 52 (8d8 + 16)\r\nSpeed 30 ft.\r\n" +
		"Str 14 Dex 8 Con 14\r\nInt 6 Wis 10 Cha 5\r\nImmunities Poison; Poisoned\r\nSenses Darkvision 60 ft.;\r\nPassive Perception 10\r\nCR 2 (XP 450; PB +2)\r\n" +
		"Actions\r\nSlam. Melee Attack Roll: +4, reach 5 ft. Hit: 7 (1d8 + 3) Bludgeoning damage.\r\nFrost Pulse (Recharge 5-6). Constitution Saving Throw: DC 12, each creature in a 10-foot Emanation.\r\nFailure: 9 (2d8) Cold damage.\r\nSuccess: Half damage.\r\n"
	m, notes := readStatBlockText(text)
	if len(notes) != 0 {
		t.Fatalf("notes on a readable block: %v", notes)
	}
	if m.Name != "Test Frost Warden" || m.Size != "Medium" || m.Type != "Construct" || m.Alignment != "Unaligned" || m.AC != 14 || m.HP != 52 || m.HPFormula != "8d8 + 16" {
		t.Fatalf("header %+v", m)
	}
	if m.Initiative.Mod != -1 || m.Initiative.Score != 9 {
		t.Fatalf("initiative %+v", m.Initiative)
	}
	if a := m.Abilities["str"]; a.Score != 14 || a.Mod != 2 || a.Save != 0 { // no modifier printed: the modifier from the score, the save left out
		t.Fatalf("grid without modifiers %+v", a)
	}
	if m.DmgImmune != "Poison" || m.CondImmune != "Poisoned" || m.Senses != "Darkvision 60 ft.; Passive Perception 10" || m.CR != "2" || m.XP != 450 || m.PB != 2 {
		t.Fatalf("labelled lines %+v", m)
	}
	if len(m.Actions) != 2 || m.Actions[0].Name != "Slam" || m.Actions[1].Name != "Frost Pulse (Recharge 5-6)" ||
		m.Actions[1].Text != "Constitution Saving Throw: DC 12, each creature in a 10-foot Emanation.\nFailure: 9 (2d8) Cold damage.\nSuccess: Half damage." {
		t.Fatalf("entries %+v", m.Actions)
	}
	// the import's own steps take it from here: a save left out becomes the modifier
	normalizeCustomMonster(&m)
	if m.Abilities["str"].Save != 2 {
		t.Fatalf("save after normalizing %+v", m.Abilities["str"])
	}
	if msg := validateCustomMonster(m); msg != "" {
		t.Fatalf("valid monster refused: %s", msg)
	}

	// what is missing is said
	_, notes = readStatBlockText("Nameless Thing\nit is large.\nSpeed 30 ft.\n")
	joined := strings.Join(notes, "\n")
	for _, want := range []string{"does not start with a size", "No AC found", "No HP found", "No Str score", "No Cha score", "CR line"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no note says %q:\n%s", want, joined)
		}
	}
	if _, notes = readStatBlockText("  \n"); len(notes) != 1 {
		t.Errorf("empty text: %v", notes)
	}
	// text above the first section and an entry-less section's lines are said, not dropped
	_, notes = readStatBlockText("Odd Block\nSmall Beast, Unaligned\nAC 10\nHP 3 (1d6)\nSpeed 20 ft.\nStr 10 +0 +0 Dex 10 +0 +0 Con 10 +0 +0\nInt 2 -4 -4 Wis 10 +0 +0 Cha 4 -3 -3\nCR 0 (XP 10; PB +2)\nActions\nsome stray words\n")
	if !strings.Contains(strings.Join(notes, "\n"), "before its first entry was not read") {
		t.Errorf("stray text in a section: %v", notes)
	}
}

// TestPostgresCompendiumPreviewText: a stat block pasted as text comes back as the monster it was read as, with the same report a
// structured one gets; a text with problems says so; text and monsters together, and too many, are refused.
func TestPostgresCompendiumPreviewText(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "text gm")
	if c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Text test", "max_players": 4}); c != 200 {
		t.Fatalf("campaign %d %v", c, j)
	}
	path := "/api/gm/compendium/monsters/preview"
	text := "Test Cinder Warden\nMedium Construct, Unaligned\nAC 15 Initiative +1 (11)\nHP 45 (6d8 + 18)\nSpeed 30 ft.\n" +
		"Str 16 +3 +3 Dex 12 +1 +1 Con 16 +3 +3\nInt 6 -2 -2 Wis 10 +0 +0 Cha 5 -3 -3\nImmunities Fire, Poison; Poisoned\nSenses Darkvision 60 ft.; Passive Perception 10\nCR 3 (XP 700; PB +2)\n" +
		"Traits\nIron Heart. The warden is mindful of its charge.\nActions\nSlam. Melee Attack Roll: +5, reach 5 ft. Hit: 8 (1d10 + 3) Bludgeoning damage.\nSmolder. The warden sets the ground alight.\n"
	var before int
	db.QueryRow(`SELECT COUNT(*) FROM custom_monsters`).Scan(&before)
	c, j := pgCall(t, "POST", path, gm, map[string]interface{}{"texts": []string{text}})
	if c != 200 || j["saved"] != false {
		t.Fatalf("preview of a text %d %v", c, j)
	}
	p := j["monsters"].([]interface{})[0].(map[string]interface{})
	read := p["monster"].(map[string]interface{})
	if p["valid"] != true || read["name"] != "Test Cinder Warden" || read["ac"] != float64(15) || len(p["read_notes"].([]interface{})) != 0 {
		t.Fatalf("the text read as %v", p)
	}
	slam, smolder := false, false
	for _, e := range p["entries"].([]interface{}) {
		e := e.(map[string]interface{})
		switch e["name"] {
		case "Slam":
			slam = len(e["applied"].([]interface{})) > 0
		case "Smolder":
			smolder = len(e["gm_resolves"].([]interface{})) == 1
		}
	}
	if !slam || !smolder {
		t.Fatalf("the report of a read text: %v", p["entries"])
	}
	var after int
	db.QueryRow(`SELECT COUNT(*) FROM custom_monsters`).Scan(&after)
	if after != before {
		t.Fatal("a preview saved a monster")
	}
	// what was read can be imported as it came back
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "text preview test", "monsters": []interface{}{read}})
	if c != 201 {
		t.Fatalf("importing what was read: %d %v", c, j)
	}
	// problems are said, and the monster is not valid
	c, j = pgCall(t, "POST", path, gm, map[string]interface{}{"texts": []string{"Broken\nit is large.\nSpeed 30 ft."}})
	p = j["monsters"].([]interface{})[0].(map[string]interface{})
	if c != 200 || p["valid"] != false || len(p["read_notes"].([]interface{})) < 4 {
		t.Fatalf("a text with problems %d %v", c, p)
	}
	// both kinds, none, too many
	if c, _ := pgCall(t, "POST", path, gm, map[string]interface{}{"texts": []string{text}, "monsters": []srdMonster2024{compendiumFixture()}}); c != 400 {
		t.Fatal("texts and monsters together accepted")
	}
	if c, _ := pgCall(t, "POST", path, gm, map[string]interface{}{}); c != 400 {
		t.Fatal("an empty preview accepted")
	}
	many := make([]string, 26)
	for i := range many {
		many[i] = text
	}
	if c, _ := pgCall(t, "POST", path, gm, map[string]interface{}{"texts": many}); c != 400 {
		t.Fatal("26 texts accepted")
	}
}
