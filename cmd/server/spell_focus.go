package main

// Spellcasting focuses (2024 rules). Each class uses its own kind of focus:
//	Bard                          Musical Instrument
//	Cleric, Paladin               Holy Symbol
//	Druid, Ranger                 Druidic Focus
//	Sorcerer, Warlock, Wizard     Arcane Focus (a Wizard may also use a spellbook)
// A Component Pouch can stand in for the material components of any caster.
// Classes that cast through a subclass (Eldritch Knight, Arcane Trickster) use an Arcane Focus.

import (
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

const (
	focusArcane     = "arcane"
	focusDivine     = "divine"
	focusPrimal     = "primal"
	focusInstrument = "instrument"
)

// classFocusKind is the kind of focus a class casts with.
func classFocusKind(class string) string {
	switch strings.ToLower(strings.TrimSpace(class)) {
	case "bard":
		return focusInstrument
	case "cleric", "paladin":
		return focusDivine
	case "druid", "ranger":
		return focusPrimal
	case "sorcerer", "warlock", "wizard":
		return focusArcane
	default:
		return "" // no class focus (Barbarian, Fighter, Monk, Rogue, or a species or Magic Initiate spell): only a Component Pouch
	}
}

// itemFocusKinds says which kinds of focus an inventory item is. An empty
// result means the item is not a focus. "any" means it works for every class
// (a Component Pouch, or a placeholder focus with no kind).
func itemFocusKinds(item map[string]interface{}) []string {
	name := ""
	if n, ok := item["name"].(string); ok {
		name = n
	} else if n, ok := item["item"].(string); ok {
		name = n
	}
	n := strings.ToLower(name)

	// an item marked as a focus carries its kind when it came from a class kit
	if flagged, _ := item["spellcasting_focus"].(bool); flagged {
		if k, _ := item["focus_kind"].(string); k != "" {
			return []string{strings.ToLower(k)}
		}
		return []string{"any"}
	}

	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(n, w) {
				return true
			}
		}
		return false
	}
	var kinds []string
	switch {
	case has("component pouch", "spellcasting focus"):
		return []string{"any"}
	}
	if has("arcane focus", "crystal", "orb", "rod") {
		kinds = append(kinds, focusArcane)
	}
	if has("staff", "wand") { // an arcane staff or wand, or a druid's wooden staff or yew wand
		kinds = append(kinds, focusArcane, focusPrimal)
	}
	if has("holy symbol", "emblem", "reliquary") { // not "amulet": an Amulet of Health is no Holy Symbol (F79)
		kinds = append(kinds, focusDivine)
	}
	if has("druidic focus", "totem", "mistletoe", "sprig") {
		kinds = append(kinds, focusPrimal)
	}
	if has("musical instrument") || game.IsToolOfCategory(n, "instrument") {
		kinds = append(kinds, focusInstrument)
	}
	return kinds
}

// hasUsableFocus reports whether the inventory holds something the class can cast with.
func hasUsableFocus(class string, inventory []map[string]interface{}) bool {
	want := classFocusKind(class)
	for _, item := range inventory {
		// 2024 Wizard: the spellbook is a Spellcasting Focus
		if strings.ToLower(class) == "wizard" {
			if n, _ := item["name"].(string); strings.Contains(strings.ToLower(n), "spellbook") {
				return true
			}
		}
		for _, k := range itemFocusKinds(item) {
			if k == "any" || k == want {
				return true
			}
		}
	}
	return false
}

// somaticHandFree reports whether the hand that isn't holding a Shield can perform a spell's Somatic component (F64).
// SRD 5.2.1, Components: a Material component needs "a hand free to access them, but it can be the same hand used to perform
// Somatic components", and a Spellcasting Focus must be held to stand in for the materials. So the hand is free when it holds
// nothing, and it performs the gestures while it holds a focus the caster can use in place of the spell's Material component
// (focusForMaterial: the materials have no cost and aren't consumed). A weapon in that hand leaves no hand for the gestures.
func somaticHandFree(mainHand, comps string, focusForMaterial bool, inventory []map[string]interface{}, class string) bool {
	held := strings.TrimSpace(mainHand)
	if held == "" {
		return true
	}
	if !focusForMaterial || !strings.Contains(strings.ToLower(comps), "m") {
		return false
	}
	for _, item := range inventory {
		if n, _ := item["name"].(string); strings.EqualFold(strings.TrimSpace(n), held) && hasUsableFocus(class, []map[string]interface{}{item}) {
			return true
		}
	}
	return false
}

// focusNeededName names the focus a class needs, for error messages.
func focusNeededName(class string) string {
	switch classFocusKind(class) {
	case focusInstrument:
		return "a Musical Instrument"
	case focusDivine:
		return "a Holy Symbol"
	case focusPrimal:
		return "a Druidic Focus"
	case focusArcane:
		return "an Arcane Focus"
	default:
		return "a Component Pouch"
	}
}

// carriesNamedMaterial: the inventory holds the item a spell's material names ("a Holy Symbol worth 5+ GP" and a Holy Symbol). The
// components a spell lists can always be used themselves instead of a focus or a Component Pouch (SRD 5.2.1, Material components);
// a consumed or costly one is checked where its cost is (F244).
func carriesNamedMaterial(material string, inventory []map[string]interface{}) bool {
	m := strings.ToLower(material)
	if m == "" {
		return false
	}
	for _, it := range inventory {
		name := strings.ToLower(strings.TrimSpace(fmt.Sprint(it["name"])))
		if i := strings.Index(name, ","); i > 0 { // "Holy Symbol, Amulet" names a Holy Symbol
			name = strings.TrimSpace(name[:i])
		}
		if len(name) >= 5 && strings.Contains(m, name) {
			return true
		}
	}
	return false
}
