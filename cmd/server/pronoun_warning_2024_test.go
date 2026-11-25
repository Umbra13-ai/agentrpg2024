package main

// I-ALDENQA-1 (owner, Nov 25: read a monster's own he/his/she/her as "its"): an imported entry that calls the creature he or she is read as it
// by every rule (readAsIt, both views the rules read), its text stays as the GM wrote it, and the import report says so.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestItsWording(t *testing.T) {
	for in, want := range map[string]string{
		"Until the start of his next turn, he appears ghostly and gains Resistance to all damage.": "Until the start of its next turn, it appears ghostly and gains Resistance to all damage.",
		"Trigger: A creature hits her with an attack roll. Response: She teleports up to 15 feet.": "Trigger: A creature hits it with an attack roll. Response: It teleports up to 15 feet.",
		"She regains 10 Hit Points at the start of her turns and moves toward her.":                "It regains 10 Hit Points at the start of its turns and moves toward it.",
		"He hides himself; the hag swaps herself with her and her twin.":                           "It hides itself; the hag swaps itself with it and its twin.",
		"Until the start of its next turn, the witch has Resistance to all damage.":                "Until the start of its next turn, the witch has Resistance to all damage.",
		"The ether hums.": "The ether hums.",
	} {
		if got := itsWording(in); got != want {
			t.Errorf("itsWording(%q)\n got %q\nwant %q", in, got, want)
		}
	}
	// the caller's monster is not changed
	m := inventedMonster("Test Bell Witch", "Fey", []srdMonsterEntry{{Name: "Ghostly", Text: "Until the start of his next turn, he is ghostly."}}, nil)
	r := readAsIt(m)
	if m.Traits[0].Text != "Until the start of his next turn, he is ghostly." || r.Traits[0].Text != "Until the start of its next turn, it is ghostly." {
		t.Errorf("readAsIt: kept %q, read %q", m.Traits[0].Text, r.Traits[0].Text)
	}
	if meta := metaFromMonster("preview", m); !strings.Contains(meta.Traits[0].Text, "its next turn") {
		t.Errorf("the rules' stat block: %q", meta.Traits[0].Text)
	}
	stats, _ := json.Marshal(customMonsterStats("custom:test", m))
	if strings.Contains(string(stats), "his next turn") || !strings.Contains(string(stats), "its next turn") {
		t.Errorf("the play rows: %s", stats)
	}
}

func TestImportReportFlagsHeAndShe(t *testing.T) {
	m := inventedMonster("Test Bell Witch", "Fey", []srdMonsterEntry{{Name: "Ghostly", Text: "Until the start of his next turn, the witch has Resistance to all damage."}},
		[]srdMonsterEntry{{Name: "Claw", Text: "Melee Attack Roll: +4, reach 5 ft. Hit: 5 (1d6 + 2) Slashing damage."}})
	m.Reactions = []srdMonsterEntry{{Name: "Slip", Text: "Trigger: A creature hits her with an attack roll. Response: She teleports up to 15 feet."}}
	w := fmt.Sprint(customMonsterReport(m, nil, false)["warnings"])
	if !strings.Contains(w, `Ghostly ("his")`) || !strings.Contains(w, `Slip ("her")`) || strings.Contains(w, "Claw") || !strings.Contains(w, "read the creature's own he, his, she and her as it and its") {
		t.Errorf("the warning: %s", w)
	}
	m.Traits[0].Text = "Until the start of its next turn, the witch has Resistance to all damage."
	m.Reactions = nil
	if w := personalPronounWarning(m); w != "" {
		t.Errorf("SRD wording flagged: %s", w)
	}
}

// TestPostgresHeAndSheReadAsIt: the reported case through the server: an imported Bonus Action that says "Until the start of his next turn" is
// listed as an open item like the SRD's "its", and the stored definition keeps the GM's words.
func TestPostgresHeAndSheReadAsIt(t *testing.T) {
	testPostgres(t)
	gm, _, camp, chars, _ := pgFightWith(t, "pronoun", 1)
	m := compendiumFixture()
	m.Name, m.Type, m.Traits, m.Legendary, m.LegendaryIn = "Test Bell Witch", "Fey", nil, nil, ""
	m.BonusActions = []srdMonsterEntry{{Name: "Ghostly Step (1/Day)", Text: "The witch teleports up to 30 feet to an unoccupied space he can see. Until the start of his next turn, he appears ghostly and has Resistance to all damage."}}
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented pronoun fixture", "monsters": []srdMonster2024{m}})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	key := fmt.Sprint(imported["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	var def string
	db.QueryRow(`SELECT definition::text FROM custom_monsters WHERE key=$1`, key).Scan(&def)
	if !strings.Contains(def, "his next turn") {
		t.Errorf("the GM's words are not kept: %s", def)
	}
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Bell Witch", "monster_key": key, "hp": 100, "ac": 1}}})
	if c, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "The witch flickers.",
		"monster_action": map[string]interface{}{"monster": "Bell Witch", "action": "Ghostly Step", "target": fmt.Sprint(chars[0])}}); c != 200 {
		t.Fatalf("Ghostly Step: %d %v", c, j)
	}
	_, j := pgCall(t, "GET", fmt.Sprintf("/api/gm/open-items?campaign_id=%d", camp), gm, nil)
	if got := fmt.Sprint(j["open_items"]); !strings.Contains(got, "Ghostly Step") {
		t.Errorf("the lasting sentence written with his is not an open item: %v", got)
	}
}
