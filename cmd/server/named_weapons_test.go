package main

import (
	"strings"
	"testing"
)

// F49: named magic weapons read their base weapons and bonus from the SRD 5.2.1 item text.
func TestNamedWeaponsFromSRD(t *testing.T) {
	v := namedWeaponFor("Vorpal Sword")
	// the unit-test weapon map lacks a few weapons the server loads from its database (Glaive, Scimitar)
	if v == nil || v.Bonus != 3 || !v.allows("greatsword") || !v.allows("longsword") || v.allows("rapier") {
		t.Fatalf("Vorpal Sword: %+v", v)
	}
	if ha := namedWeaponFor("holy avenger"); ha == nil || ha.Bonus != 3 || ha.Any != "simple or martial" || !ha.allows("mace") {
		t.Errorf("Holy Avenger: %+v", ha)
	}
	if d := namedWeaponFor("Defender"); d == nil || d.Bonus != 3 || !d.allows("longsword") || d.allows("longbow") {
		t.Errorf("Defender (any Melee weapon): %+v", d)
	}
	if dv := namedWeaponFor("Dagger of Venom"); dv == nil || dv.Bonus != 1 || len(dv.Options) != 1 || dv.Options[0] != "dagger" {
		t.Errorf("Dagger of Venom: %+v", dv)
	}
	if ft := namedWeaponFor("Flame Tongue"); ft == nil || ft.Bonus != 0 {
		t.Errorf("Flame Tongue has no bonus: %+v", ft)
	}
	if namedWeaponFor("Weapon, +1, +2, or +3") != nil || namedWeaponFor("Longsword") != nil {
		t.Error("generic +N weapons and plain weapons aren't named weapons")
	}
}

func TestEquipNamedWeaponBase(t *testing.T) {
	item := invItem{"name": "Vorpal Sword"}
	if _, _, why := equipNamedWeapon(item, ""); !strings.Contains(why, "base_weapon") {
		t.Errorf("a Vorpal Sword needs a choice: %q", why)
	}
	if _, _, why := equipNamedWeapon(item, "rapier"); !strings.Contains(why, "can't be a rapier") {
		t.Errorf("a Vorpal Sword can't be a rapier: %q", why)
	}
	if k, changed, why := equipNamedWeapon(item, "Greatsword"); k != "greatsword" || !changed || why != "" {
		t.Errorf("greatsword: %q %v %q", k, changed, why)
	}
	item["base_weapon"] = "greatsword"
	if k, changed, _ := equipNamedWeapon(item, ""); k != "greatsword" || changed {
		t.Errorf("the kept base weapon: %q %v", k, changed)
	}
	if k, changed, _ := equipNamedWeapon(invItem{"name": "Dagger of Venom"}, ""); k != "dagger" || !changed {
		t.Errorf("Dagger of Venom is a dagger: %q %v", k, changed)
	}
	if k, _, why := equipNamedWeapon(invItem{"name": "Longsword"}, ""); k != "" || why != "" {
		t.Errorf("a plain weapon isn't settled here: %q %q", k, why)
	}
}
