package game

import (
	"testing"
)

func TestNormalizeSpecies(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Human", "human"},
		{"High Elf", "high_elf"},
		{"High Elf", "high_elf"},
		{"wood_elf", "wood_elf"},
		{"DWARF", "dwarf"},
	}

	for _, tt := range tests {
		got := normalizeSpecies(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeSpecies(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestGetSpeciesSize(t *testing.T) {
	tests := []struct {
		species     string
		expected string
	}{
		{"Human", SizeMedium},
		{"Elf", SizeMedium},
		{"Dwarf", SizeMedium},
		{"Orc", SizeMedium},
		{"Halfling", SizeSmall},
		{"Lightfoot Halfling", SizeSmall},
		{"Gnome", SizeSmall},
		{"Rock Gnome", SizeSmall},
		{"Tiefling", SizeMedium},
		{"Dragonborn", SizeMedium},
	}

	for _, tt := range tests {
		got := GetSpeciesSize(tt.species)
		if got != tt.expected {
			t.Errorf("GetSpeciesSize(%q) = %q, want %q", tt.species, got, tt.expected)
		}
	}
}

func TestGetDefaultSpeed(t *testing.T) {
	tests := []struct {
		species     string
		expected int
	}{
		{"Human", 30},
		{"Elf", 30},
		{"Wood Elf", 35},
		{"Dwarf", 25},
		{"Hill Dwarf", 25},
		{"Halfling", 25},
		{"Gnome", 25},
		{"Orc", 30},
		{"Tiefling", 30},
		{"Dragonborn", 30},
	}

	for _, tt := range tests {
		got := GetDefaultSpeed(tt.species)
		if got != tt.expected {
			t.Errorf("GetDefaultSpeed(%q) = %d, want %d", tt.species, got, tt.expected)
		}
	}
}

func TestSizeOrder(t *testing.T) {
	if SizeOrder(SizeTiny) >= SizeOrder(SizeSmall) {
		t.Error("Tiny should be smaller than Small")
	}
	if SizeOrder(SizeSmall) >= SizeOrder(SizeMedium) {
		t.Error("Small should be smaller than Medium")
	}
	if SizeOrder(SizeMedium) >= SizeOrder(SizeLarge) {
		t.Error("Medium should be smaller than Large")
	}
	if SizeOrder(SizeLarge) >= SizeOrder(SizeHuge) {
		t.Error("Large should be smaller than Huge")
	}
	if SizeOrder(SizeHuge) >= SizeOrder(SizeGargantuan) {
		t.Error("Huge should be smaller than Gargantuan")
	}
}

func TestIsSizeAtLeastOneLarger(t *testing.T) {
	// Horse (Large) can carry Medium rider
	if !IsSizeAtLeastOneLarger(SizeLarge, SizeMedium) {
		t.Error("Large should be at least one size larger than Medium")
	}

	// Mastiff (Medium) can carry Small rider
	if !IsSizeAtLeastOneLarger(SizeMedium, SizeSmall) {
		t.Error("Medium should be at least one size larger than Small")
	}

	// Medium cannot carry Medium
	if IsSizeAtLeastOneLarger(SizeMedium, SizeMedium) {
		t.Error("Medium is not larger than Medium")
	}

	// Small cannot carry Medium
	if IsSizeAtLeastOneLarger(SizeSmall, SizeMedium) {
		t.Error("Small is not larger than Medium")
	}
}

func TestKeywordChecks(t *testing.T) {
	// Frighten keywords
	if !CheckFrightenKeywords("The dragon's terrifying presence") {
		t.Error("Should match 'terrifying'")
	}
	if !CheckFrightenKeywords("Save against being frightened") {
		t.Error("Should match 'frightened'")
	}
	if CheckFrightenKeywords("A normal attack") {
		t.Error("Should not match normal attack")
	}

	// Charm keywords
	if !CheckCharmKeywords("You are charmed by the vampire") {
		t.Error("Should match 'charmed'")
	}
	if !CheckCharmKeywords("The suggestion spell") {
		t.Error("Should match 'suggestion'")
	}
	if CheckCharmKeywords("A fire attack") {
		t.Error("Should not match fire attack")
	}

	// Poison keywords
	if !CheckPoisonKeywords("You are poisoned") {
		t.Error("Should match 'poisoned'")
	}
	if !CheckPoisonKeywords("Venomous bite") {
		t.Error("Should match 'venomous'")
	}
	if CheckPoisonKeywords("A slashing attack") {
		t.Error("Should not match slashing attack")
	}
}

func TestRacialSaveAdvantages(t *testing.T) {
	// Halfling Brave
	if !CheckHalflingBrave("Halfling", "dragon's frightening presence") {
		t.Error("Halfling should get Brave advantage vs frightening presence")
	}
	if CheckHalflingBrave("Human", "dragon's frightening presence") {
		t.Error("Human should not get Halfling Brave")
	}
	if CheckHalflingBrave("Halfling", "fire damage") {
		t.Error("Halfling Brave only applies to frighten saves")
	}

	// Fey Ancestry (Charm)
	if !CheckFeyAncestryCharm("High Elf", "vampire's charm") {
		t.Error("High Elf should get Fey Ancestry advantage vs charm")
	}
	if !CheckFeyAncestryCharm("Wood Elf", "dominate person") {
		t.Error("a Wood Elf should get Fey Ancestry advantage vs dominate")
	}
	if CheckFeyAncestryCharm("Dwarf", "vampire's charm") {
		t.Error("Dwarf should not get Fey Ancestry")
	}

	// Dwarven Resilience (Poison)
	if !CheckDwarvenResiliencePoison("Dwarf", "poisoned blade") {
		t.Error("Dwarf should get advantage vs poison")
	}
	if CheckDwarvenResiliencePoison("Elf", "poisoned blade") {
		t.Error("Elf should not get Dwarven Resilience")
	}

	// Gnomish Cunning (Magic + INT/WIS/CHA)
	if !CheckGnomeCunningMagic("Gnome", "wis", true) {
		t.Error("Gnome should get advantage on WIS save vs magic")
	}
	if !CheckGnomeCunningMagic("Rock Gnome", "int", true) {
		t.Error("Rock Gnome should get advantage on INT save vs magic")
	}
	if CheckGnomeCunningMagic("Gnome", "str", true) {
		t.Error("Gnomish Cunning doesn't apply to STR saves")
	}
	if !CheckGnomeCunningMagic("Gnome", "wis", false) {
		t.Error("2024 Gnomish Cunning applies to all INT, WIS and CHA saves, not only magic")
	}
	if CheckGnomeCunningMagic("Human", "wis", true) {
		t.Error("Human should not get Gnomish Cunning")
	}
}

func TestApplyHalflingLucky(t *testing.T) {
	// When a Halfling rolls a 1, they reroll
	// We can't predict the reroll, but we can verify the behavior

	// Non-1 rolls should pass through unchanged
	final, rerolled, orig := ApplyHalflingLucky(15, true)
	if rerolled {
		t.Error("Should not reroll non-1")
	}
	if final != 15 || orig != 15 {
		t.Errorf("Expected 15/15, got %d/%d", final, orig)
	}

	// Non-Halfling rolling 1 should not reroll
	final, rerolled, orig = ApplyHalflingLucky(1, false)
	if rerolled {
		t.Error("Non-Halfling should not reroll")
	}
	if final != 1 {
		t.Errorf("Expected 1, got %d", final)
	}

	// Halfling rolling 1 should reroll (result is random but should be 1-20)
	final, rerolled, orig = ApplyHalflingLucky(1, true)
	if !rerolled {
		t.Error("Halfling should reroll a 1")
	}
	if orig != 1 {
		t.Errorf("Original roll should be 1, got %d", orig)
	}
	if final < 1 || final > 20 {
		t.Errorf("Reroll should be 1-20, got %d", final)
	}
}

func TestBreathWeaponDamage(t *testing.T) {
	tests := []struct {
		level    int
		expected string
	}{
		{1, "1d10"},
		{4, "1d10"},
		{5, "2d10"},
		{10, "2d10"},
		{11, "3d10"},
		{16, "3d10"},
		{17, "4d10"},
		{20, "4d10"},
	}

	for _, tt := range tests {
		got := BreathWeaponDamage(tt.level)
		if got != tt.expected {
			t.Errorf("BreathWeaponDamage(%d) = %q, want %q", tt.level, got, tt.expected)
		}
	}
}

func TestDragonAncestries(t *testing.T) {
	// Test all 10 dragon types exist
	colors := []string{"black", "blue", "brass", "bronze", "copper", "gold", "green", "red", "silver", "white"}
	for _, color := range colors {
		ancestry := GetDragonAncestry(color)
		if ancestry == nil {
			t.Errorf("Missing dragon ancestry for %s", color)
			continue
		}
		if ancestry.DamageType == "" {
			t.Errorf("Dragon %s has no damage type", color)
		}
		if ancestry.BreathArea == "" {
			t.Errorf("Dragon %s has no breath area", color)
		}
		if ancestry.SaveAbility != "dex" && ancestry.SaveAbility != "con" {
			t.Errorf("Dragon %s has invalid save ability: %s", color, ancestry.SaveAbility)
		}
	}

	// Test case insensitivity
	red := GetDragonAncestry("RED")
	if red == nil {
		t.Error("Should find RED dragon ancestry")
	}

	// Test invalid color
	invalid := GetDragonAncestry("purple")
	if invalid != nil {
		t.Error("Should not find purple dragon ancestry")
	}

	// Verify specific ancestries
	gold := GetDragonAncestry("gold")
	if gold.DamageType != "fire" || gold.BreathArea != "15ft cone" {
		t.Errorf("Gold dragon should be fire/15ft cone, got %s/%s", gold.DamageType, gold.BreathArea)
	}

	black := GetDragonAncestry("black")
	if black.DamageType != "acid" || black.BreathArea != "5x30ft line" {
		t.Errorf("Black dragon should be acid/5x30ft line, got %s/%s", black.DamageType, black.BreathArea)
	}
}
