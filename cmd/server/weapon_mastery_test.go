package main

import (
	"strings"
	"testing"
)

func TestMasteryCounts(t *testing.T) {
	cases := []struct {
		class string
		level int
		want  int
	}{
		{"barbarian", 1, 2}, {"barbarian", 4, 3}, {"barbarian", 10, 4},
		{"fighter", 1, 3}, {"fighter", 4, 4}, {"fighter", 10, 5}, {"fighter", 16, 6},
		{"paladin", 1, 2}, {"paladin", 20, 2}, {"ranger", 5, 2}, {"rogue", 9, 2},
		{"wizard", 1, 0}, {"cleric", 5, 0}, {"monk", 1, 0},
	}
	for _, c := range cases {
		if got := masteryCount(c.class, c.level); got != c.want {
			t.Errorf("%s %d: got %d, want %d", c.class, c.level, got, c.want)
		}
	}
}

func TestEligibleMasteryWeapons(t *testing.T) {
	applyWeaponMastery()
	has := func(class, name string) bool {
		for _, c := range eligibleMasteryWeapons(class) {
			if strings.EqualFold(c.Name, name) {
				return true
			}
		}
		return false
	}
	if !has("barbarian", "Greataxe") || has("barbarian", "Longbow") {
		t.Error("barbarians choose melee weapons only")
	}
	if !has("fighter", "Longbow") || !has("fighter", "Longsword") {
		t.Error("fighters choose any simple or martial weapon")
	}
	if !has("rogue", "Dagger") || !has("rogue", "Rapier") || !has("rogue", "Shortbow") {
		t.Error("rogues may choose simple weapons and finesse or light martial weapons")
	}
	if has("rogue", "Greatsword") || has("rogue", "Longsword") {
		t.Error("rogues cannot choose martial weapons without finesse or light")
	}
	if has("wizard", "Dagger") {
		t.Error("wizards have no Weapon Mastery")
	}
	if has("fighter", "Net") {
		t.Error("the Net is not a weapon")
	}
}

func TestValidateMasteryPicks(t *testing.T) {
	applyWeaponMastery()
	keys, err := validateMasteryPicks("fighter", 1, []string{"Longsword", "greatsword", "Light Crossbow"}, true)
	if err != nil || len(keys) != 3 {
		t.Fatalf("good picks: %v %v", keys, err)
	}
	if _, err := validateMasteryPicks("fighter", 1, []string{"Longsword"}, true); err == nil || err.Code != "weapon_masteries_required" {
		t.Errorf("too few: %v", err)
	}
	if _, err := validateMasteryPicks("fighter", 1, []string{"Longsword", "Greatsword", "Dagger", "Mace"}, false); err == nil {
		t.Error("too many should be refused")
	}
	if _, err := validateMasteryPicks("barbarian", 1, []string{"Longbow", "Greataxe"}, true); err == nil || err.Code != "invalid_weapon_mastery" {
		t.Errorf("a barbarian cannot choose a bow: %v", err)
	}
	if keys, err := validateMasteryPicks("wizard", 1, nil, true); err != nil || keys != nil {
		t.Errorf("wizards choose nothing: %v %v", keys, err)
	}
	// duplicates collapse, so two of the same kind is one choice and not enough
	if _, err := validateMasteryPicks("rogue", 1, []string{"Dagger", "dagger"}, true); err == nil {
		t.Error("the same weapon twice is one choice")
	}
}

func TestMasteryNoteInAttacks(t *testing.T) {
	applyWeaponMastery()
	testDB := setupSQLiteTestDBWithSpecies(t)
	seedCharacterWithSpecies(t, testDB, 1, "Fighty", "Human", `[]`, 0)
	testDB.Exec(`ALTER TABLE characters ADD COLUMN weapon_masteries TEXT`)
	testDB.Exec(`UPDATE characters SET weapon_masteries = ? WHERE id = 1`, `["longsword","greatsword","quarterstaff","spear"]`)

	note := func(key string, mod int) string { return masteryNote(1, key, srdWeapons[key], mod, 1) }
	if n := note("longsword", 3); !strings.Contains(n, "Sap") || !strings.Contains(n, "Disadvantage") {
		t.Errorf("longsword is Sap: %q", n)
	}
	if n := note("greatsword", 3); !strings.Contains(n, "Graze") || !strings.Contains(n, "3 slashing") {
		t.Errorf("greatsword Graze should deal the ability modifier: %q", n)
	}
	if n := note("greatsword", 0); !strings.Contains(n, "no damage") {
		t.Errorf("Graze with no positive modifier: %q", n)
	}
	if n := note("quarterstaff", 3); !strings.Contains(n, "DC 13") || !strings.Contains(n, "Prone") {
		t.Errorf("Topple DC is 8 + modifier + Proficiency Bonus: %q", n)
	}
	if n := note("dagger", 3); n != "" {
		t.Errorf("a weapon without chosen mastery adds nothing: %q", n)
	}
	if n := note("", 3); n != "" {
		t.Errorf("no weapon, no note: %q", n)
	}
}
