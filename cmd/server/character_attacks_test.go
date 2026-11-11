package main

import (
	"strings"
	"testing"
)

func attackByName(t *testing.T, sb map[string]interface{}, name string) sbEntry {
	t.Helper()
	for _, a := range sb["attacks"].([]sbEntry) {
		if strings.HasPrefix(a.Name, name) {
			return a
		}
	}
	t.Fatalf("no attack named %q in %v", name, sb["attacks"])
	return sbEntry{}
}

// Numbers checked against the premade level 10 Human Fighter sheet
// (Strength 20, +4 proficiency, Champion): Longsword +9 to hit, 1d8 + 5.
func TestFighterLongswordMatchesPremadeSheet(t *testing.T) {
	sb := map[string]interface{}{"speed_feet": 30}
	addCombatBlock(sb, combatInput{
		Class: "fighter", Level: 10, Str: 20, Dex: 9, WeaponProfs: "simple, martial",
		Inventory: []invItem{{"id": "i1", "name": "longsword"}, {"id": "i2", "name": "handaxe", "quantity": 5.0}},
		MainHand:  "longsword", OffHand: "handaxe", // only equipped weapons are listed
		BaseSpeed: 30,
	})
	ls := attackByName(t, sb, "Longsword")
	want := "Melee Weapon Attack: +9 to hit, reach 5 ft., one target. Hit: 1d8 + 5 slashing damage (1d10 + 5 if used with two hands)."
	if ls.Description != want {
		t.Errorf("longsword = %q\nwant      %q", ls.Description, want)
	}
	jav := attackByName(t, sb, "Handaxe")
	if !strings.Contains(jav.Description, "Melee or Ranged Weapon Attack: +9 to hit, reach 5 ft. or range 20/60 ft.") {
		t.Errorf("handaxe = %q", jav.Description)
	}
	if !strings.Contains(jav.Name, "(5)") {
		t.Errorf("handaxe quantity missing: %q", jav.Name)
	}
	if got := sb["attack_action"].(string); !strings.Contains(got, "twice") {
		t.Errorf("fighter level 10 should attack twice, got %q", got)
	}
}

// High Elf Wizard 10: Dexterity 16, shortsword finesse +7 to hit (premade sheet).
func TestWizardShortswordUsesFinesseAndProficiency(t *testing.T) {
	sb := map[string]interface{}{}
	addCombatBlock(sb, combatInput{
		Class: "wizard", Level: 10, Str: 10, Dex: 16, WeaponProfs: "dagger, shortsword",
		Inventory: []invItem{{"id": "i1", "name": "shortsword"}}, MainHand: "shortsword", BaseSpeed: 30,
	})
	got := attackByName(t, sb, "Shortsword").Description
	if !strings.Contains(got, "+7 to hit") || !strings.Contains(got, "1d6 + 3 piercing") {
		t.Errorf("shortsword = %q", got)
	}
}

func TestNotProficientGetsNoProficiencyBonus(t *testing.T) {
	sb := map[string]interface{}{}
	addCombatBlock(sb, combatInput{
		Class: "wizard", Level: 10, Str: 10, Dex: 10, WeaponProfs: "dagger",
		Inventory: []invItem{{"id": "i1", "name": "greatsword"}}, MainHand: "greatsword", BaseSpeed: 30,
	})
	if got := attackByName(t, sb, "Greatsword").Description; !strings.Contains(got, "+0 to hit") {
		t.Errorf("unproficient greatsword = %q", got)
	}
}

func TestLostBagWeaponsAreNotListed(t *testing.T) {
	sb := map[string]interface{}{}
	addCombatBlock(sb, combatInput{
		Class: "fighter", Level: 1, Str: 16, Dex: 10, WeaponProfs: "simple, martial",
		Inventory: []invItem{
			{"id": "i1", "name": "Backpack", "type": "container", "lost": true},
			{"id": "i2", "name": "longsword", "location": "i1"},
		}, BaseSpeed: 30,
	})
	for _, a := range sb["attacks"].([]sbEntry) {
		if a.Name == "Longsword" {
			t.Error("sword in a lost bag should not be listed as an attack")
		}
	}
}

func TestMonkUnarmedStrikeAndSpeed(t *testing.T) {
	sb := map[string]interface{}{}
	addCombatBlock(sb, combatInput{Class: "monk", Level: 10, Str: 8, Dex: 18, BaseSpeed: 25})
	u := attackByName(t, sb, "Unarmed Strike").Description
	// Martial Arts die at level 10 is d8 (2024); Dex +4; proficiency +4 -> +8
	if !strings.Contains(u, "+8 to hit") || !strings.Contains(u, "1d8 + 4 bludgeoning") {
		t.Errorf("monk unarmed strike = %q", u)
	}
	if sb["speed_feet"].(int) != 45 || sb["speed_note"] != "Unarmored Movement" {
		t.Errorf("monk 10 halfling speed = %v (%v), want 45", sb["speed_feet"], sb["speed_note"])
	}
	if sb["armor_class_note"] != "Unarmored Defense" {
		t.Errorf("monk without armor should note Unarmored Defense, got %v", sb["armor_class_note"])
	}
}

func TestBarbarianNotes(t *testing.T) {
	sb := map[string]interface{}{}
	addCombatBlock(sb, combatInput{Class: "barbarian", Level: 10, Str: 18, Dex: 14, BaseSpeed: 30})
	if sb["speed_feet"].(int) != 40 || sb["speed_note"] != "Fast Movement" {
		t.Errorf("barbarian speed = %v (%v)", sb["speed_feet"], sb["speed_note"])
	}
	if sb["initiative_note"] != "advantage (Feral Instinct)" {
		t.Errorf("initiative note = %v", sb["initiative_note"])
	}
}

// A player can only attack with equipped weapons: a sword in the pack or on the
// belt is not listed until it is equipped.
func TestOnlyEquippedWeaponsAreListed(t *testing.T) {
	inv := []invItem{
		{"id": "i1", "name": "Backpack", "type": "container", "location": "worn"},
		{"id": "i2", "name": "longsword", "location": "i1"},  // in the pack
		{"id": "i3", "name": "dagger", "location": "worn"},   // on the belt
		{"id": "i4", "name": "handaxe", "location": "held"},  // in hand
		{"id": "i5", "name": "greataxe", "location": "held"}, // pretend only one is equipped
	}
	sb := map[string]interface{}{}
	addCombatBlock(sb, combatInput{Class: "fighter", Level: 1, Str: 16, Dex: 10, WeaponProfs: "simple, martial",
		Inventory: inv, MainHand: "handaxe", BaseSpeed: 30})
	names := map[string]bool{}
	for _, a := range sb["attacks"].([]sbEntry) {
		names[a.Name] = true
	}
	if !names["Handaxe"] || !names["Unarmed Strike"] {
		t.Errorf("the equipped handaxe and Unarmed Strike should be listed: %v", names)
	}
	if names["Longsword"] || names["Dagger"] || names["Greataxe"] {
		t.Errorf("unequipped weapons must not be listed: %v", names)
	}
}

func TestSameWeaponMatchesSpellingsAndSlugs(t *testing.T) {
	if !sameWeapon("light_crossbow", "Light Crossbow") || !sameWeapon("crossbow-light", "light crossbow") {
		t.Error("crossbow spellings should match")
	}
	if sameWeapon("longsword", "shortsword") || sameWeapon("club", "greatclub") || sameWeapon("", "dagger") {
		t.Error("different weapons must not match")
	}
}

func TestParseWeaponPrefersTheLongestName(t *testing.T) {
	if got := parseWeaponFromDescription("I swing my greatsword at the ogre"); got != "greatsword" {
		t.Errorf("got %q", got)
	}
	if got := parseWeaponFromDescription("I draw my shortsword"); got != "shortsword" {
		t.Errorf("got %q", got)
	}
	for i := 0; i < 20; i++ { // the answer must not depend on map order
		if got := parseWeaponFromDescription("attack with the longsword"); got != "longsword" {
			t.Fatalf("run %d: got %q", i, got)
		}
	}
}
