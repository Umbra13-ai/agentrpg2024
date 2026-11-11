package main

import "testing"

// 2024 Wizard: "You can use an Arcane Focus or your spellbook as a Spellcasting Focus."
func TestWizardSpellbookCountsAsFocus(t *testing.T) {
	inv := []map[string]interface{}{{"name": "Spellbook"}}
	if msg := checkSpellComponents("V, S, M", nil, inv, false, nil, nil, "wizard", 3); msg != "" {
		t.Errorf("a wizard with a spellbook should be able to cast: %q", msg)
	}
	// but not a sorcerer
	if msg := checkSpellComponents("V, S, M", nil, inv, false, nil, nil, "sorcerer", 3); msg == "" {
		t.Error("a spellbook is not a focus for a sorcerer")
	}
}

func TestWizardQuarterstaffFocusFlagWorks(t *testing.T) {
	inv := []map[string]interface{}{{"name": "Quarterstaff", "spellcasting_focus": true}}
	if msg := checkSpellComponents("V, S, M", nil, inv, false, nil, nil, "wizard", 3); msg != "" {
		t.Errorf("a flagged quarterstaff focus should work: %q", msg)
	}
}

func TestWizardKitMarksStaffAndSpellbookAsFocus(t *testing.T) {
	res, err := buildStartingEquipment("wizard", "a", "")
	if err != nil {
		t.Fatal(err)
	}
	flagged := map[string]bool{}
	for _, it := range res.Items {
		if itemBool(it, "spellcasting_focus") {
			flagged[itemStr(it, "name")] = true
		}
	}
	if !flagged["Quarterstaff"] || !flagged["Spellbook"] {
		t.Errorf("staff and spellbook should both be focuses, got %v", flagged)
	}
}

func TestRemoveFocusPlaceholders(t *testing.T) {
	inv := []invItem{
		{"id": "i1", "name": "Arcane Focus", "type": "focus", "source": "class"},
		{"id": "i2", "name": "Quarterstaff", "spellcasting_focus": true, "source": "class"},
		{"id": "i3", "name": "Arcane Focus, Crystal", "source": "class"}, // a real focus, kept
	}
	got := removeFocusPlaceholders(inv)
	if len(got) != 2 || itemStr(got[0], "name") != "Quarterstaff" {
		t.Errorf("got %v", got)
	}
}
