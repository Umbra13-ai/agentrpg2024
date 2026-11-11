package main

import (
	"strings"
	"testing"
)

// F61: the item spells the charge parser couldn't read, the staves as weapons, item names.
func TestLegendaryMoreItemSpells(t *testing.T) {
	loadItemUses()
	has := func(item, spell string, once bool) {
		t.Helper()
		u := itemUses[itemUseKey(item)]
		if u == nil {
			t.Fatalf("%s: not loaded", item)
		}
		for _, s := range u.Spells {
			if s.Slug == spell {
				if s.Once != once {
					t.Errorf("%s: %s once=%v, want %v", item, spell, s.Once, once)
				}
				return
			}
		}
		t.Errorf("%s: no %s in %+v", item, spell, u.Spells)
	}
	has("Luck Blade", "wish", true)
	has("Crystal Ball", "scrying", false)
	has("Crystal Ball of Telepathy", "suggestion", true)
	has("Crystal Ball of Mind Reading", "detect-thoughts", false)
	for _, p := range planeWords {
		if len(elementalSpells[p]) < 4 {
			t.Errorf("Ring of Elemental Command (%s): %+v", p, elementalSpells[p])
		}
	}
	ring := customItemUse(invItem{"name": "Ring of Elemental Command (Fire)"})
	if ring == nil || len(ring.Spells) != 4 || ring.Spells[1].Slug != "fireball" || ring.Spells[1].Cost != 2 || ring.Spells[1].DC != 18 {
		t.Errorf("the fire ring's spells: %+v", ring)
	}
	moon := customItemUse(invItem{"name": "Moon card wishes", "cast_spells": []interface{}{"wish"}, "charges": 2.0, "charges_max": 2.0})
	if moon == nil || moon.Max != "2" || len(moon.Spells) != 1 || moon.Spells[0].Slug != "wish" {
		t.Errorf("the Moon card's wishes: %+v", moon)
	}
	if sm := namedWeaponFor("Staff of the Magi"); sm == nil || sm.Bonus != 2 || len(sm.Options) != 1 || sm.Options[0] != "quarterstaff" {
		t.Errorf("Staff of the Magi as a weapon: %+v", sm)
	}
	if baseItemName("Ring of Elemental Command (Fire)") != "Ring of Elemental Command" || baseItemName("Horn of Valhalla (Iron)") != "Horn of Valhalla" {
		t.Error("baseItemName")
	}
	if crystalBallNote("Crystal Ball of True Seeing", "scrying") == "" || crystalBallNote("Crystal Ball", "scrying") != "" {
		t.Error("crystalBallNote")
	}
	loadSpellAdvItems()
	for _, n := range []string{"ring of spell turning", "scarab of protection", "staff of the magi"} {
		if _, ok := spellAdvItems[n]; !ok {
			t.Errorf("%s gives Advantage on saves against spells: %v", n, spellAdvItems)
		}
	}
	if spellAdvItems["staff of the magi"] != "held" || spellAdvItems["ring of spell turning"] != "worn" || spellAdvItems["scarab of protection"] != "carried" {
		t.Errorf("where they work: %v", spellAdvItems)
	}
	if !strings.HasPrefix(rodWeaponKey(invItem{"rod_form": "flame_shortsword"}), "shortsword") || rodPlus(invItem{"rod_form": "flame_longsword"}) != 0 || rodPlus(invItem{}) != 3 || rodWeaponKey(invItem{"rod_form": "ram"}) != "" {
		t.Error("the rod's forms")
	}
	if m := defenderTransferRe.FindStringSubmatch("swing the Defender, 2 to AC"); m == nil || m[1]+m[2] != "2" {
		t.Errorf("Defender transfer: %v", m)
	}
}

func TestDeckChoiceParsing(t *testing.T) {
	if up, down := balanceAbilities("+dex -str"); up != "dex" || down != "str" {
		t.Errorf("balance +dex -str: %q %q", up, down)
	}
	if up, down := balanceAbilities("wisdom up, charisma down"); up != "wis" || down != "cha" {
		t.Errorf("balance wisdom up charisma down: %q %q", up, down)
	}
	if abilityFromText("wisdom") != "wis" || abilityFromText("int please") != "intl" || abilityFromText("luck") != "" {
		t.Error("abilityFromText")
	}
	if m := deckChoiceRe.FindStringSubmatch("Star: Constitution"); m == nil || strings.ToLower(m[1]) != "star" || strings.TrimSpace(m[2]) != "Constitution" {
		t.Errorf("deckChoiceRe: %v", m)
	}
	if deckChoiceRe.MatchString("draw 3") {
		t.Error("a draw isn't a choice")
	}
	if addListWord("insight, history", "persuasion") != "insight, history, persuasion" || addListWord("persuasion", "Persuasion") != "persuasion" || addListWord("", "history") != "history" {
		t.Error("addListWord")
	}
	for _, c := range []string{"Balance", "Star", "Throne", "Jester"} {
		if p, l, ok := deckCardPending(0, c); !ok || p != c || l == "" {
			t.Errorf("%s waits for a choice: %q %q %v", c, p, l, ok)
		}
	}
	if p, _, ok := deckCardPending(0, "Puzzle"); !ok || !strings.HasPrefix(p, "Puzzle:") {
		t.Errorf("Puzzle keeps its roll: %q", p)
	}
	if _, l, ok := deckCardPending(0, "Key"); !ok || !strings.Contains(l, "Dagger +2") { // O-4: a Rare weapon appears
		t.Errorf("the Key gives a Rare magic weapon: %q %v", l, ok)
	}
}
