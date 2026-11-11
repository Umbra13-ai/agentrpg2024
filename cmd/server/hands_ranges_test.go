package main

import (
	"strings"
	"testing"
)

func TestHandsTwoHandsOnly(t *testing.T) {
	cases := []struct {
		name       string
		main, off  string
		shield, ok bool
	}{
		{"one-handed sword", "longsword", "", false, true},
		{"sword and shield", "longsword", "", true, true},
		{"two weapons", "shortsword", "dagger", false, true},
		{"two-handed alone", "greatsword", "", false, true},
		{"two-handed with a second weapon", "greatsword", "dagger", false, false},
		{"two-handed with a shield", "greatsword", "", true, false},
		{"two weapons and a shield", "shortsword", "dagger", true, false},
		{"weapon and shield only", "dagger", "", true, true},
		{"a two-handed weapon in the off hand", "dagger", "greatsword", false, false},
		{"nothing", "", "", false, true},
		{"shield alone", "", "", true, true},
	}
	for _, c := range cases {
		ok, why := handsAllow(c.main, c.off, c.shield)
		if ok != c.ok {
			t.Errorf("%s: handsAllow(%q, %q, %v) = %v (%s), want %v", c.name, c.main, c.off, c.shield, ok, why, c.ok)
		}
		if !ok && why == "" {
			t.Errorf("%s: a refusal needs a reason", c.name)
		}
	}
}

func TestIsTwoHandedWeapon(t *testing.T) {
	if !isTwoHandedWeapon("greatsword") || !isTwoHandedWeapon("Greataxe") || !isTwoHandedWeapon("longbow") {
		t.Error("greatsword, greataxe and longbow are two-handed")
	}
	if isTwoHandedWeapon("longsword") || isTwoHandedWeapon("dagger") || isTwoHandedWeapon("") {
		t.Error("versatile and light weapons are one-handed here")
	}
}

func TestEquipRuleOncePerTurnAndDropToSwitch(t *testing.T) {
	if ok, _ := equipRule(false, false); !ok {
		t.Error("an empty hand and no earlier interaction should be allowed")
	}
	if ok, code := equipRule(true, false); ok || code != "hand_not_empty" {
		t.Errorf("an occupied hand must be refused (drop it first): %v %q", ok, code)
	}
	if ok, code := equipRule(false, true); ok || code != "already_equipped_or_unequipped_this_turn" {
		t.Errorf("a second equip in a turn must be refused: %v %q", ok, code)
	}
	if !strings.Contains(msgHandNotEmpty, "drop the weapon in your hand first") || !strings.Contains(msgHandNotEmpty, "anyone can pick it up") {
		t.Error("the message should explain dropping and what can happen to a dropped weapon")
	}
}

func TestBundleKeepsContentsTogether(t *testing.T) {
	inv := []invItem{
		{"id": "i1", "name": "Backpack", "type": "container", "location": "worn"},
		{"id": "i2", "name": "Pouch", "type": "container", "location": "i1"},
		{"id": "i3", "name": "Coin", "location": "i2"},
		{"id": "i4", "name": "Rope", "location": "i1"},
		{"id": "i5", "name": "Dagger", "location": "worn"},
	}
	b := collectBundle(inv, "i1")
	if len(b) != 4 || itemStr(b[0], "id") != "i1" {
		t.Fatalf("backpack bundle should be the backpack and 3 things inside, got %d", len(b))
	}
	rest := removeBundle(inv, "i1")
	if len(rest) != 1 || itemStr(rest[0], "name") != "Dagger" {
		t.Errorf("only the dagger should remain: %v", rest)
	}
	if got := collectBundle(inv, "i5"); len(got) != 1 {
		t.Errorf("a plain item is a bundle of one, got %d", len(got))
	}
}

func TestRangedAttackInMeleeRangeHasDisadvantage(t *testing.T) {
	bow := srdWeapons["shortbow"]
	if d, note := meleeRangeEffects(bow, "I shoot the goblin", true, false); !d || !strings.Contains(note, "within 5 feet") {
		t.Errorf("a bow in melee range has Disadvantage: %v %q", d, note)
	}
	if d, _ := meleeRangeEffects(bow, "I shoot the goblin", false, false); d {
		t.Error("a bow with no enemy next to you is normal")
	}
	if d, _ := meleeRangeEffects(srdWeapons["light_crossbow"], "I shoot", true, true); d {
		t.Error("Crossbow Expert ignores the penalty")
	}
}

func TestMeleeWeaponsAreNotPenalisedInMeleeRange(t *testing.T) {
	if d, _ := meleeRangeEffects(srdWeapons["longsword"], "I strike", true, false); d {
		t.Error("a sword in melee range is normal")
	}
	// a thrown dagger counts as ranged only when the player says they throw it
	dag := srdWeapons["dagger"]
	if d, _ := meleeRangeEffects(dag, "I stab the goblin", true, false); d {
		t.Error("stabbing with a dagger is a melee attack")
	}
	if d, _ := meleeRangeEffects(dag, "I throw my dagger at the goblin", true, false); !d {
		t.Error("throwing a dagger at an adjacent enemy has Disadvantage")
	}
	if d, _ := meleeRangeEffects(dag, "I throw my dagger at the archer", false, false); d {
		t.Error("throwing a dagger with nobody next to you is normal")
	}
}

func TestCloseRangeFlagRoundTrip(t *testing.T) {
	d := withActionContext("I shoot the goblin", true)
	if !parseActionContext(d) {
		t.Error("the close_range flag should survive")
	}
	if parseActionContext(withActionContext("I shoot", false)) {
		t.Error("no flag means not close range")
	}
	if withActionContext("plain", false) != "plain" {
		t.Error("no flag leaves the text alone")
	}
	if k := parseWeaponFromDescription(d); k != "" && k != "shortbow" {
		t.Errorf("the marker must not add a weapon, got %q", k)
	}
}
