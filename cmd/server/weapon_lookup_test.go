package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/agentrpg/agentrpg/game"
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

// Every weapon is stored through setWeapon, so its properties have one spelling whatever the source.
func TestWeaponPropertiesOneSpelling(t *testing.T) {
	old, had := srdWeapons["test-blade"]
	defer func() {
		if had {
			srdWeapons["test-blade"] = old
		} else {
			delete(srdWeapons, "test-blade")
		}
	}()
	setWeapon("test-blade", SRDWeapon{Name: "Test Blade", Properties: []string{"Finesse", " Light ", "Thrown (Range 20/60)", ""}})
	if got := strings.Join(srdWeapons["test-blade"].Properties, "|"); got != "finesse|light|thrown (range 20/60)" {
		t.Errorf("stored properties %q", got)
	}
	for _, w := range srdWeapons {
		for _, p := range w.Properties {
			if p != strings.ToLower(strings.TrimSpace(p)) {
				t.Errorf("%s has a property in another spelling: %q", w.Name, p)
			}
		}
	}
	files, _ := filepath.Glob("*.go")
	write := regexp.MustCompile(`srdWeapons\[[^\]]+\]\s*=[^=]`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for n, line := range strings.Split(string(b), "\n") {
			if write.MatchString(line) && !strings.Contains(line, "srdWeapons[key] = w") {
				t.Errorf("%s:%d stores a weapon without setWeapon", f, n+1)
			}
		}
	}
}

// Every attacker's line comes from game.AttackRoll.HeadForAttacker (through LineForAttacker or attackHead): no handler writes
// "N to hit" itself (the GM's monster lines are game.AttackRoll.Line).
func TestNoOtherAttackerLine(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	own := regexp.MustCompile(`"[^"]*: %d to hit|Attack roll: %d|natural 1: miss|nat %d CRITICAL`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for n, line := range strings.Split(string(b), "\n") {
			if own.MatchString(line) {
				t.Errorf("%s:%d writes an attack line itself: use attackHead or LineForAttacker", f, n+1)
			}
		}
	}
}

// Every message that names a class's spell list names spellListRoute, a route that answers with that list; no handler writes
// the route by hand.
func TestSpellListRoutesComeFromOneHelper(t *testing.T) {
	if got := spellListRoute("Wizard", 0); got != "GET /api/universe/class-spells/wizard?level=0" {
		t.Errorf("spellListRoute: %q", got)
	}
	files, _ := filepath.Glob("*.go")
	byHand := regexp.MustCompile(`universe/class-spells/%s|universe/class-spells/" \+|universe/spells\?class=`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "spell_lists_2024.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		for n, line := range strings.Split(string(b), "\n") {
			if byHand.MatchString(line) {
				t.Errorf("%s:%d writes a spell list route by hand: use spellListRoute", f, n+1)
			}
		}
	}
}

// An error reply has a 4xx or 5xx status: no handler writes {"error": ...} without setting one first.
func TestNoErrorBodyWith200(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	enc := regexp.MustCompile(`Encode\(map\[string\](string|interface\{\})\{"error"`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		lines := strings.Split(string(b), "\n")
		for i, line := range lines {
			if !enc.MatchString(line) {
				continue
			}
			from := i - 3
			if from < 0 {
				from = 0
			}
			if !strings.Contains(strings.Join(lines[from:i], "\n"), "WriteHeader") {
				t.Errorf("%s:%d writes an error body with status 200: use writeJSONStatus", f, i+1)
			}
		}
	}
}

// A save the reader's own feature forces (Topple) shows its DC to players; the saver's total stays with the GM.
func TestSaveAgainstShowsTheDCOnly(t *testing.T) {
	r := game.SavingThrow{Ability: "con", DC: 13, Bonus: 5}.Judge(9)
	gm := r.LineAgainst("Dummy")
	pl := playerText(gm)
	if !strings.Contains(gm, "14") || !strings.Contains(pl, "against DC 13") || strings.Contains(pl, "14") {
		t.Errorf("GM %q / player %q", gm, pl)
	}
}
