package main

import (
	"strings"
	"testing"
)

// The Mysterious Deck table: every card has its SRD text, the 13-card deck draws only its 13 cards, and removed cards don't come back.
func TestMysteriousDeckTable(t *testing.T) {
	in13 := map[string]bool{}
	covered13, covered22 := 0, 0
	for _, c := range deckCards {
		if deckCardText(c.Name) == "" {
			t.Errorf("no SRD text for the %s card", c.Name)
		}
		if c.Lo13 > 0 {
			in13[c.Name] = true
			covered13 += c.Hi13 - c.Lo13 + 1
		}
		covered22 += c.Hi22 - c.Lo22 + 1
	}
	if len(in13) != 13 || covered13 != 100 || covered22 != 100 || len(deckCards) != 22 {
		t.Fatalf("table: %d cards in the 13-card deck, d100 covered %d and %d, %d cards", len(in13), covered13, covered22, len(deckCards))
	}
	for i := 0; i < 300; i++ {
		if c := drawDeckCard(13, map[string]bool{"Jester": true}); !in13[c] || c == "Jester" {
			t.Fatalf("13-card deck drew %q", c)
		}
		if c := drawDeckCard(22, map[string]bool{"Fool": true}); c == "Fool" {
			t.Fatal("a removed Fool was drawn")
		}
	}
	if !strings.Contains(deckCardText("Skull"), "Avatar of Death") {
		t.Errorf("Skull text: %q", deckCardText("Skull"))
	}
}

func TestItemCooldownTicks(t *testing.T) {
	it := invItem{"name": "Horn of Valhalla (Iron)", "dawns_until_ready": 2}
	if n := tickItemCooldown(0, it); n != "" || itemCooldownLeft(it) != 1 {
		t.Fatalf("first dawn: %q, %d left", n, itemCooldownLeft(it))
	}
	if n := tickItemCooldown(0, it); !strings.Contains(n, "can be used again") || itemCooldownLeft(it) != 0 {
		t.Fatalf("second dawn: %q, %d left", n, itemCooldownLeft(it))
	}
	if n := tickItemCooldown(0, invItem{"name": "Rope"}); n != "" {
		t.Errorf("an item without a wait: %q", n)
	}
}

func TestAutoHitPattern(t *testing.T) {
	if !autoHitRe.MatchString("Melee Attack Roll: Automatic hit, reach 5 ft.") || autoHitRe.MatchString("Melee Attack Roll: +6, reach 5 ft.") {
		t.Error("autoHitRe")
	}
}
