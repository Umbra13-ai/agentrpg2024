package main

import "testing"

// Hands for spell components (hands_2024.go, O-6), War Caster (F78), and the Amulet that is no Holy Symbol (F79).

func TestComponentLetters(t *testing.T) {
	if v, s, m := componentLetters("V, S, M (a diamond worth 300+ GP)"); !v || !s || !m {
		t.Fatalf("V, S, M with the material: got %v %v %v", v, s, m)
	}
	if _, s, m := componentLetters("V, M"); s || !m {
		t.Fatalf("V, M: got S %v M %v", s, m)
	}
}

func TestHandsFree(t *testing.T) {
	cases := []struct {
		h    castHands
		want int
	}{
		{castHands{}, 2},
		{castHands{Main: "Mace", Shield: true}, 0},
		{castHands{Main: "Greatsword"}, 1}, // a Two-Handed weapon is held in one hand while it isn't swung
		{castHands{Main: "Scimitar", Off: "Shortsword"}, 0},
		{castHands{Shield: true}, 1},
		{castHands{Shield: true, Blade: true}, 0}, // the Flame Blade fills a hand
	}
	for _, c := range cases {
		if got := c.h.free(); got != c.want {
			t.Errorf("%+v: %d free hands, want %d", c.h, got, c.want)
		}
	}
}

func TestAmuletIsNoHolySymbol(t *testing.T) {
	if k := itemFocusKinds(map[string]interface{}{"name": "Amulet of Health"}); len(k) != 0 {
		t.Fatalf("an Amulet of Health counted as a focus: %v", k)
	}
	if k := itemFocusKinds(map[string]interface{}{"name": "Holy Symbol, Amulet"}); len(k) == 0 || k[0] != focusDivine {
		t.Fatalf("Holy Symbol, Amulet: %v", k)
	}
}

func TestWornFocus(t *testing.T) {
	amulet := []map[string]interface{}{{"name": "Holy Symbol, Amulet", "location": "worn"}}
	if !wornFocus("cleric", amulet, false) {
		t.Error("a worn Amulet needs no hand")
	}
	if wornFocus("wizard", amulet, false) {
		t.Error("a Holy Symbol is no Wizard's focus")
	}
	if wornFocus("cleric", []map[string]interface{}{{"name": "Holy Symbol, Reliquary", "location": "worn"}}, true) {
		t.Error("a Reliquary is held")
	}
	if !wornFocus("paladin", []map[string]interface{}{{"name": "Holy Symbol, Emblem", "location": "i1"}}, true) {
		t.Error("an Emblem is borne on the Shield")
	}
}

func TestHasWarCaster(t *testing.T) {
	if !hasWarCaster([]string{"alert", "war_caster"}) || !hasWarCaster([]string{"war-caster"}) || hasWarCaster([]string{"alert"}) {
		t.Fatal("war_caster detection")
	}
}

func TestAtoiSigned(t *testing.T) {
	if atoiSigned("-1") != -1 || atoiSigned(" 12 ") != 12 {
		t.Fatal("atoiSigned")
	}
}
