package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// findWeapon is the one lookup of a weapon's data: every way the server writes a weapon finds the same entry.
func TestFindWeaponIsTheOneLookup(t *testing.T) {
	for _, c := range []struct{ in, name string }{
		{"longsword", "Longsword"}, {"Longsword", "Longsword"}, {"Longsword +1", "Longsword"}, {"longsword+2", "Longsword"},
		{"Light Crossbow", "Light Crossbow"}, {"light-crossbow", "Light Crossbow"}, {"light_crossbow", "Light Crossbow"},
		{"Crossbow, Light", "Light Crossbow"}, {"Dancing Sword (Rapier)", "Rapier"}, {"Frost Brand (Longsword)", "Longsword"},
		{"Holy Avenger (Hand Crossbow)", "Hand Crossbow"},
	} {
		slug, w, ok := findWeapon(c.in)
		if !ok || w.Name != c.name || srdWeapons[slug].Name != c.name {
			t.Errorf("findWeapon(%q) = %q (%s), %v; want %s", c.in, slug, w.Name, ok, c.name)
		}
	}
	for _, not := range []string{"", "Shield", "Torch", "Holy Symbol", "Chain Mail"} {
		if _, _, ok := findWeapon(not); ok {
			t.Errorf("findWeapon(%q) found a weapon", not)
		}
	}
}

// The character sheet shows a held weapon's details from the weapon data (it used to query a column that doesn't exist).
func TestHeldWeaponView(t *testing.T) {
	v, ok := heldWeaponView("Longsword +1").(map[string]interface{})
	if !ok {
		t.Fatal("a longsword is shown as a name only")
	}
	ls := srdWeapons["longsword"]
	if v["name"] != ls.Name || v["damage_dice"] != ls.Damage || v["damage_type"] != ls.DamageType || v["held_as"] != "Longsword +1" ||
		!strings.Contains(v["properties"].(string), "versatile") {
		t.Errorf("sheet view %v", v)
	}
	if s, ok := heldWeaponView("Torch").(string); !ok || s != "Torch" {
		t.Errorf("a torch is shown as %v", heldWeaponView("Torch"))
	}
}

// No handler reads a weapon's details from the weapons table or indexes srdWeapons with a key it builds itself.
func TestNoOtherWeaponLookup(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	dbRead := regexp.MustCompile(`FROM weapons`)
	handBuilt := regexp.MustCompile(`srdWeapons\[(strings\.|baseWeaponOf|fmt\.)`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for n, line := range strings.Split(string(b), "\n") {
			if dbRead.MatchString(line) && f != "database_init.go" && f != "universe_data.go" {
				t.Errorf("%s:%d reads weapon data from the database: use findWeapon", f, n+1)
			}
			if handBuilt.MatchString(line) {
				t.Errorf("%s:%d indexes srdWeapons with a hand-built key: use findWeapon", f, n+1)
			}
		}
	}
}
