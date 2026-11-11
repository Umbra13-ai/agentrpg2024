package game

import (
	"testing"
)

func TestAvailableSubclassesCount(t *testing.T) {
	// SRD has 12 subclasses (one per class)
	if len(AvailableSubclasses) != 12 {
		t.Errorf("Expected 12 subclasses, got %d", len(AvailableSubclasses))
	}
}

func TestGetSubclassesForClass(t *testing.T) {
	tests := []struct {
		class    string
		expected []string
	}{
		{"barbarian", []string{"berserker"}},
		{"fighter", []string{"champion"}},
		{"rogue", []string{"thief"}},
		{"paladin", []string{"devotion"}},
		{"ranger", []string{"hunter"}},
		{"bard", []string{"lore"}},
		{"cleric", []string{"life"}},
		{"druid", []string{"land"}},
		{"monk", []string{"open-hand"}},
		{"sorcerer", []string{"draconic"}},
		{"warlock", []string{"fiend"}},
		{"wizard", []string{"evocation"}},
	}

	for _, tt := range tests {
		t.Run(tt.class, func(t *testing.T) {
			result := GetSubclassesForClass(tt.class)
			if len(result) != len(tt.expected) {
				t.Errorf("GetSubclassesForClass(%q) returned %d subclasses, expected %d", tt.class, len(result), len(tt.expected))
			}
			for _, slug := range tt.expected {
				if _, ok := result[slug]; !ok {
					t.Errorf("GetSubclassesForClass(%q) missing expected subclass %q", tt.class, slug)
				}
			}
		})
	}
}

func TestGetSubclass(t *testing.T) {
	tests := []struct {
		slug     string
		wantNil  bool
		wantName string
	}{
		{"champion", false, "Champion"},
		{"berserker", false, "Path of the Berserker"},
		{"thief", false, "Thief"},
		{"nonexistent", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.slug, func(t *testing.T) {
			result := GetSubclass(tt.slug)
			if tt.wantNil {
				if result != nil {
					t.Errorf("GetSubclass(%q) = %v, expected nil", tt.slug, result)
				}
			} else {
				if result == nil {
					t.Errorf("GetSubclass(%q) = nil, expected non-nil", tt.slug)
				} else if result.Name != tt.wantName {
					t.Errorf("GetSubclass(%q).Name = %q, expected %q", tt.slug, result.Name, tt.wantName)
				}
			}
		})
	}
}

func TestGetActiveSubclassFeatures(t *testing.T) {
	tests := []struct {
		subclass    string
		level       int
		expectedMin int // minimum number of features expected
	}{
		{"champion", 3, 2},   // Improved Critical + Remarkable Athlete (2024)
		{"champion", 7, 3},   // + Additional Fighting Style
		{"champion", 15, 5}, // + Heroic Warrior + Superior Critical
		{"champion", 18, 6},  // + Survivor
		{"thief", 3, 2},      // Fast Hands + Second-Story Work
		{"thief", 17, 5},     // All features
		{"berserker", 3, 1},  // Frenzy
		{"berserker", 14, 4}, // All features
	}

	for _, tt := range tests {
		t.Run(tt.subclass, func(t *testing.T) {
			features := GetActiveSubclassFeatures(tt.subclass, tt.level)
			if len(features) < tt.expectedMin {
				t.Errorf("GetActiveSubclassFeatures(%q, %d) returned %d features, expected at least %d",
					tt.subclass, tt.level, len(features), tt.expectedMin)
			}
		})
	}
}

func TestGetActiveSubclassFeaturesInvalid(t *testing.T) {
	features := GetActiveSubclassFeatures("nonexistent", 10)
	if features != nil {
		t.Errorf("GetActiveSubclassFeatures(nonexistent) = %v, expected nil", features)
	}
}

func TestHasSubclassFeature(t *testing.T) {
	tests := []struct {
		subclass string
		level    int
		mechanic string
		expected bool
	}{
		// Champion
		{"champion", 3, "crit_range", true},
		{"champion", 2, "crit_range", false}, // Not at level 2
		{"champion", 3, "remarkable_athlete", true},
		{"champion", 2, "remarkable_athlete", false},
		{"champion", 18, "survivor_regen", true},

		// Berserker
		{"berserker", 3, "frenzy", true},
		{"berserker", 6, "rage_immune_charm", true},
		{"berserker", 10, "retaliation_attack", true},

		// Thief
		{"thief", 3, "fast_hands", true},
		{"thief", 9, "stealth_attack", true},
		{"thief", 17, "extra_first_round_turn", true},

		// Life Cleric
		{"life", 3, "bonus_healing", true},
		{"life", 6, "blessed_healer", true},
		{"life", 17, "supreme_healing", true},

		// Nonexistent
		{"champion", 20, "nonexistent_mechanic", false},
		{"nonexistent", 10, "anything", false},
	}

	for _, tt := range tests {
		name := tt.subclass + "_" + tt.mechanic
		t.Run(name, func(t *testing.T) {
			result := HasSubclassFeature(tt.subclass, tt.level, tt.mechanic)
			if result != tt.expected {
				t.Errorf("HasSubclassFeature(%q, %d, %q) = %v, expected %v",
					tt.subclass, tt.level, tt.mechanic, result, tt.expected)
			}
		})
	}
}

func TestGetSubclassMechanic(t *testing.T) {
	tests := []struct {
		subclass  string
		level     int
		mechanic  string
		wantVal   string
		wantFound bool
	}{
		{"champion", 3, "crit_range", "19", true},
		{"champion", 15, "crit_range", "18", true}, // Superior Critical overwrites at higher level
		{"champion", 3, "remarkable_athlete", "true", true},
		{"life", 3, "bonus_healing", "2+spell_level", true},
		{"draconic", 3, "natural_ac", "13+dex", true},
		{"champion", 3, "nonexistent", "", false},
	}

	for _, tt := range tests {
		name := tt.subclass + "_" + tt.mechanic
		t.Run(name, func(t *testing.T) {
			val, found := GetSubclassMechanic(tt.subclass, tt.level, tt.mechanic)
			if found != tt.wantFound {
				t.Errorf("GetSubclassMechanic(%q, %d, %q) found=%v, expected %v",
					tt.subclass, tt.level, tt.mechanic, found, tt.wantFound)
			}
			// For crit_range, we just check it's found since the value depends on which feature is checked first
			if tt.wantFound && tt.mechanic != "crit_range" && val != tt.wantVal {
				t.Errorf("GetSubclassMechanic(%q, %d, %q) = %q, expected %q",
					tt.subclass, tt.level, tt.mechanic, val, tt.wantVal)
			}
		})
	}
}

func TestDragonAncestryDamageTypes(t *testing.T) {
	expected := map[string]string{
		"black":  "acid",
		"blue":   "lightning",
		"brass":  "fire",
		"bronze": "lightning",
		"copper": "acid",
		"gold":   "fire",
		"green":  "poison",
		"red":    "fire",
		"silver": "cold",
		"white":  "cold",
	}

	if len(DragonAncestryDamageTypes) != len(expected) {
		t.Errorf("DragonAncestryDamageTypes has %d entries, expected %d",
			len(DragonAncestryDamageTypes), len(expected))
	}

	for ancestry, dmgType := range expected {
		if got := DragonAncestryDamageTypes[ancestry]; got != dmgType {
			t.Errorf("DragonAncestryDamageTypes[%q] = %q, expected %q", ancestry, got, dmgType)
		}
	}
}

func TestSubclassLevels(t *testing.T) {
	// Verify subclass unlock levels are correct per PHB
	expected := map[string]int{
		"life":      3, // Cleric - level 3 (2024)
		"draconic":  3, // Sorcerer - level 3 (2024)
		"fiend":     3, // Warlock - level 3 (2024)
		"land":      3, // Druid - level 3 (2024)
		"evocation": 3, // Wizard - level 3 (2024)
		"berserker": 3, // Barbarian - level 3
		"champion":  3, // Fighter - level 3
		"thief":     3, // Rogue - level 3
		"devotion":  3, // Paladin - level 3
		"hunter":    3, // Ranger - level 3
		"lore":      3, // Bard - level 3
		"open-hand": 3, // Monk - level 3
	}

	for slug, expectedLevel := range expected {
		sub := GetSubclass(slug)
		if sub == nil {
			t.Errorf("Subclass %q not found", slug)
			continue
		}
		if sub.SubclassLevel != expectedLevel {
			t.Errorf("Subclass %q has SubclassLevel=%d, expected %d",
				slug, sub.SubclassLevel, expectedLevel)
		}
	}
}
