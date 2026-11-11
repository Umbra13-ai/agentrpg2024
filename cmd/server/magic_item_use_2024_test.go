package main

import (
	"strings"
	"testing"
)

// The charges, dawn recharge and spells of the SRD items are read from their text (BUGS 54).
func TestItemChargesFromTheSRD(t *testing.T) {
	loadItemUses()
	want := map[string]struct {
		max, regain string
		last        bool
		spells      []string
	}{
		"Wand of Fireballs":        {"7", "1d6 + 1", true, []string{"fireball"}},
		"Wand of Magic Missiles":   {"7", "1d6 + 1", true, []string{"magic-missile"}},
		"Staff of Fire":            {"10", "1d6 + 4", true, []string{"burning-hands", "fireball", "wall-of-fire"}},
		"Staff of Healing":         {"10", "1d6 + 4", true, []string{"cure-wounds", "lesser-restoration", "mass-cure-wounds"}},
		"Staff of Power":           {"20", "2d8 + 4", true, []string{"cone-of-cold", "fireball", "magic-missile", "wall-of-force"}},
		"Staff of Charming":        {"10", "1d8 + 2", true, []string{"charm-person", "command", "comprehend-languages"}},
		"Cube of Force":            {"10", "1d6", false, []string{"mage-armor", "shield", "wall-of-force"}},
		"Eyes of Charming":         {"3", "all", false, []string{"charm-person"}},
		"Ring of Three Wishes":     {"3", "", false, []string{"wish"}},
		"Rod of Resurrection":      {"5", "1", true, []string{"heal", "resurrection"}},
		"Ring of Animal Influence": {"3", "1d3", false, []string{"animal-friendship", "fear", "speak-with-animals"}},
		"Medallion of Thoughts":    {"5", "1d4", false, []string{"detect-thoughts"}},
		"Winged Boots":             {"4", "1d4", false, nil},
		"Luck Blade":               {"1d3", "", false, nil},
	}
	for name, w := range want {
		u, ok := itemUses[itemUseKey(name)]
		if !ok {
			t.Errorf("%s not found", name)
			continue
		}
		if u.Max != w.max || u.Regain != w.regain || (u.Last != "") != w.last {
			t.Errorf("%s: max %q regain %q last %q", name, u.Max, u.Regain, u.Last)
		}
		have := map[string]itemSpell{}
		for _, s := range u.Spells {
			have[s.Slug] = s
		}
		for _, s := range w.spells {
			if _, ok := have[s]; !ok {
				t.Errorf("%s should cast %s; it casts %v", name, s, u.Spells)
			}
		}
	}
	fb := itemUses[itemUseKey("Wand of Fireballs")].Spells[0]
	if !fb.Upcast || fb.MaxCost != 3 || fb.Level != 3 || fb.DC != 15 {
		t.Errorf("Wand of Fireballs: %+v", fb)
	}
	for _, s := range itemUses[itemUseKey("Staff of Power")].Spells {
		if s.Slug == "fireball" && (s.Level != 5 || s.Cost != 5 || s.DC != 0) {
			t.Errorf("Staff of Power's Fireball (level 5 version, the caster's DC): %+v", s)
		}
	}
	for _, s := range itemUses[itemUseKey("Staff of Healing")].Spells {
		if s.Slug == "cure-wounds" && (!s.PerLevel || s.MaxLevel != 4) {
			t.Errorf("Staff of Healing's Cure Wounds is one charge per level, up to 4: %+v", s)
		}
	}
	for _, s := range itemUses[itemUseKey("Wand of Fear")].Spells {
		if s.DC != 15 {
			t.Errorf("Wand of Fear spells use DC 15: %+v", s)
		}
	}
	for _, s := range itemUses[itemUseKey("Dragon Orb")].Spells {
		if s.Slug == "cure-wounds" && s.Level != 9 {
			t.Errorf("Dragon Orb: Cure Wounds (level 9 version): %+v", s)
		}
	}
	for _, s := range itemUses[itemUseKey("Rod of Resurrection")].Spells {
		if s.Slug == "resurrection" && s.Cost != 5 {
			t.Errorf("Rod of Resurrection: Resurrection expends 5 charges: %+v", s)
		}
	}
	// every spell an item lists is a spell of the SRD
	for _, u := range itemUses {
		for _, s := range u.Spells {
			if s.Slug == "" {
				t.Errorf("%s: %q has no slug", u.Name, s.Name)
			}
		}
	}
}

func TestItemChargeState(t *testing.T) {
	wand := invItem{"name": "Wand of Fireballs"}
	if cur, max, ok := itemCharges(wand); !ok || cur != 7 || max != 7 {
		t.Errorf("a new wand is full: %d/%d %v", cur, max, ok)
	}
	setItemCharges(wand, 2, 7)
	if cur, max, _ := itemCharges(wand); cur != 2 || max != 7 || wand["magic_item"] != "Wand of Fireballs" {
		t.Errorf("after use: %d/%d %v", cur, max, wand)
	}
	if _, _, ok := itemCharges(invItem{"name": "Rope"}); ok {
		t.Error("rope has no charges")
	}
	if u := itemUseFor(invItem{"name": "Wand of the War Mage +2"}); u == nil {
		t.Error("a +N item is found by its base name")
	}
}

func TestSpellScrolls(t *testing.T) {
	srdSpellsMemory["fireball"] = SRDSpell{Name: "Fireball", Level: 3}
	srdSpellsMemory["fire-bolt"] = SRDSpell{Name: "Fire Bolt", Level: 0}
	loadItemUses()
	for name, want := range map[string]scrollInfo{
		"Spell Scroll (Fireball)":          {Slug: "fireball", Level: 3, DC: 15, Attack: 7},
		"Scroll of Fireball":               {Slug: "fireball", Level: 3, DC: 15, Attack: 7},
		"Spell Scroll (Fireball, level 5)": {Slug: "fireball", Level: 5, DC: 17, Attack: 9},
		"Spell Scroll (Fire Bolt)":         {Slug: "fire-bolt", Level: 0, DC: 13, Attack: 5},
	} {
		got, ok := scrollOf(invItem{"name": name})
		if !ok || got.Slug != want.Slug || got.Level != want.Level || got.DC != want.DC || got.Attack != want.Attack {
			t.Errorf("%s: %+v %v", name, got, ok)
		}
	}
	scribed, ok := scrollOf(invItem{"name": "Spell Scroll (Fireball)", "spell": "fireball", "spell_level": 3, "save_dc": 16, "attack_bonus": 8})
	if !ok || scribed.DC != 16 || scribed.Attack != 8 {
		t.Errorf("a scribed scroll uses the scribe's numbers: %+v", scribed)
	}
	for _, n := range []string{"Spell Scroll (Cantrip)", "Spell Scroll (Level 1)", "Case, Map or Scroll", "Scroll Case"} {
		if _, ok := scrollOf(invItem{"name": n}); ok {
			t.Errorf("%s is not a scroll of a spell", n)
		}
	}
	if withoutMaterial("V, S, M (a tiny ball of bat guano and sulfur)") != "V, S" {
		t.Error("withoutMaterial")
	}
}

func TestPotionKinds(t *testing.T) {
	for name, want := range map[string][2]string{
		"Potion of Healing":                {"healing", ""},
		"Potion of Greater Healing":        {"healing", "greater"},
		"Potion of Healing (supreme)":      {"healing", "supreme"},
		"Potion of Giant Strength (fire)":  {"giant strength", "fire"},
		"Potion of Frost Giant Strength":   {"giant strength", "frost"},
		"Potion of Resistance (lightning)": {"resistance", "lightning"},
		"Potion of Fire Resistance":        {"resistance", "fire"},
		"Potion of Heroism":                {"potion of heroism", ""},
		"Potion of Speed":                  {"potion of speed", ""},
	} {
		k, v := potionKind(name)
		if k != want[0] || v != want[1] {
			t.Errorf("%s: %q %q", name, k, v)
		}
	}
	if !isPotion(invItem{"name": "Potion of Flying"}) || !isPotion(invItem{"name": "Oil of Sharpness"}) || isPotion(invItem{"name": "Wand of Web"}) {
		t.Error("isPotion")
	}
	if potionRounds("you gain a Fly Speed equal to your Speed for 1 hour") != 600 || potionRounds("for 10 minutes (no Concentration required)") != 100 {
		t.Error("potionRounds")
	}
	if !itemEffectSource("Potion of Heroism") || itemEffectSource("Bless") {
		t.Error("itemEffectSource")
	}
	list := potionAndScrollList()
	potions, scrolls := 0, 0
	for _, p := range list {
		switch p["type"] {
		case "potion":
			potions++
		case "scroll":
			scrolls++
		}
	}
	if potions < 20 || scrolls != 10 {
		t.Errorf("the catalog lists %d potions and %d scroll levels", potions, scrolls)
	}
	if !strings.Contains(joinOr([]string{"A", "B", "C"}), "B or C") {
		t.Error("joinOr")
	}
}
