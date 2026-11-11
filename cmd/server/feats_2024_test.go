package main

import "testing"

func TestFeatPrerequisites(t *testing.T) {
	if ok, _ := featPrerequisite("level:19", "fighter", 18, 10, 10); ok {
		t.Error("Epic Boon needs level 19")
	}
	if ok, _ := featPrerequisite("level:19", "fighter", 19, 10, 10); !ok {
		t.Error("level 19 meets the Epic Boon prerequisite")
	}
	if ok, _ := featPrerequisite("level:4;str_or_dex:13", "wizard", 5, 10, 12); ok {
		t.Error("Grappler needs Strength or Dexterity 13")
	}
	if ok, _ := featPrerequisite("fighting_style_feature", "wizard", 5, 10, 10); ok {
		t.Error("Fighting Style feats need the feature")
	}
	if ok, _ := featPrerequisite("level:19;spellcasting", "fighter", 19, 10, 10); ok {
		t.Error("Boon of Spell Recall needs spellcasting")
	}
}

func TestSRDFeatList(t *testing.T) {
	if len(availableFeats) != 17 {
		t.Errorf("the SRD 5.2.1 has 17 feats, got %d", len(availableFeats))
	}
	for _, s := range []string{"lucky", "tough", "sentinel", "dueling"} {
		if _, ok := availableFeats[s]; ok {
			t.Errorf("%s is not an SRD 5.2.1 feat", s)
		}
	}
}

func TestASILevels(t *testing.T) {
	if len(asiLevelsFor("fighter")) != 7 || len(asiLevelsFor("rogue")) != 6 || len(asiLevelsFor("wizard")) != 5 {
		t.Error("Fighters have 7 Ability Score Improvements, Rogues 6, the others 5")
	}
}
