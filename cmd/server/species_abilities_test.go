package main

import (
	"testing"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

func recFor(t *testing.T, species string, choices map[string]string) *originRecord {
	t.Helper()
	sp, err := game.ResolveSpecies(species, choices)
	if err != nil {
		t.Fatal(err)
	}
	return &originRecord{Species: sp}
}

func keysOf(list []speciesAbility) map[string]speciesAbility {
	m := map[string]speciesAbility{}
	for _, a := range list {
		m[a.Key] = a
	}
	return m
}

func TestSpeciesAbilityList(t *testing.T) {
	db4 := keysOf(speciesAbilitiesFor(recFor(t, "dragonborn", map[string]string{"ancestry": "red"}), 4))
	if _, ok := db4["breath_weapon"]; !ok || db4["breath_weapon"].Max != 2 {
		t.Errorf("breath weapon should have Proficiency Bonus uses (2 at level 4): %+v", db4)
	}
	if _, ok := db4["draconic_flight"]; ok {
		t.Error("Draconic Flight needs level 5")
	}
	db5 := keysOf(speciesAbilitiesFor(recFor(t, "dragonborn", map[string]string{"ancestry": "red"}), 5))
	if _, ok := db5["draconic_flight"]; !ok {
		t.Error("Draconic Flight at level 5")
	}
	if got := keysOf(speciesAbilitiesFor(recFor(t, "dwarf", nil), 9))["stonecunning"].Max; got != 4 {
		t.Errorf("Stonecunning at level 9 should have 4 uses, got %d", got)
	}
	orc := keysOf(speciesAbilitiesFor(recFor(t, "orc", nil), 1))["adrenaline_rush"]
	if orc.Max != 2 || orc.Recovery != "short or long rest" {
		t.Errorf("adrenaline rush: %+v", orc)
	}
	g := keysOf(speciesAbilitiesFor(recFor(t, "goliath", map[string]string{"ancestry": "stone"}), 5))
	if g["giant_ancestry"].Cost != "reaction" || g["large_form"].Max != 1 {
		t.Errorf("goliath: %+v", g)
	}
	elf1 := keysOf(speciesAbilitiesFor(recFor(t, "elf", map[string]string{"lineage": "high_elf", "spellcasting_ability": "int", "skill": "insight"}), 1))
	if len(elf1) != 0 {
		t.Errorf("a level 1 elf has no free-cast spells yet: %v", elf1)
	}
	elf5 := keysOf(speciesAbilitiesFor(recFor(t, "elf", map[string]string{"lineage": "high_elf", "spellcasting_ability": "int", "skill": "insight"}), 5))
	if _, ok := elf5["lineage_spell:detect-magic"]; !ok {
		t.Error("high elf has Detect Magic at 3")
	}
	if _, ok := elf5["lineage_spell:misty-step"]; !ok {
		t.Error("high elf has Misty Step at 5")
	}
	forest := keysOf(speciesAbilitiesFor(recFor(t, "gnome", map[string]string{"lineage": "forest", "spellcasting_ability": "wis"}), 1))
	if forest["lineage_spell:speak-with-animals"].Max != 2 {
		t.Errorf("forest gnome Speak with Animals uses: %+v", forest)
	}
	if got := speciesAbilitiesFor(recFor(t, "halfling", nil), 5); len(got) != 0 {
		t.Errorf("halflings have only passive traits: %v", got)
	}
}

func TestActiveEffectsExpire(t *testing.T) {
	now := time.Now()
	u := speciesUses{Effects: map[string]string{
		"large_form":      now.Add(5 * time.Minute).Format(time.RFC3339),
		"draconic_flight": now.Add(-time.Minute).Format(time.RFC3339),
	}}
	active := u.activeEffects(now)
	if _, ok := active["large_form"]; !ok || len(active) != 1 {
		t.Errorf("only the unexpired effect should be active: %v", active)
	}
}

func TestGiantBoons(t *testing.T) {
	for _, a := range []string{"cloud", "fire", "frost", "hill", "stone", "storm"} {
		if giantBoonName(a) == "" || giantBoonCost(a) == "" {
			t.Errorf("missing boon text for %s", a)
		}
	}
	if giantBoonCost("cloud") != "bonus action" || giantBoonCost("storm") != "reaction" {
		t.Error("boon action costs are wrong")
	}
}

// A Goliath names the boon in the attack description; the 10-minute traits are known by name (BUGS 50).
func TestGiantBoonInTheAttack(t *testing.T) {
	for d, want := range map[string]bool{"longsword, fire’s burn": true, "greataxe with hill's tumble": true, "giant ancestry": true, "longsword": false, "burn it": false} {
		noteGiantBoon(7, d)
		_, got := pendingGiantBoon.LoadAndDelete(7)
		if got != want {
			t.Errorf("%q: %v", d, got)
		}
	}
	if !isSpeciesTraitName("Large Form") || isSpeciesTraitName("Bless") {
		t.Error("trait names")
	}
	if largeFormAdvantage(0, "str") {
		t.Error("no character, no Large Form")
	}
}
