package main

// Fixes from Fizzwick's coverage session (level20-battle-tests/s26_fizzwick_wizard.py, Nov 8 2026; FINDINGS F118-):
//	Staff of Power         a magic weapon equipped in a hand wasn't "held", so its spells were refused (F118, equipped_weapons.go)
//	Evocation Wizard       the player's cast action ignored Empowered Evocation, Sculpt Spells and Potent Cantrip; only the GM's
//	                       /gm/aoe-cast had them (F119)

import (
	"fmt"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

func evokerHas(charID int, feature string) bool {
	class, sub, level := subclassOf(charID)
	return strings.EqualFold(class, "wizard") && sub != "" && hasSubclassFeature(sub, level, feature)
}

func wizardEvocation(spellKey string, spell SRDSpell) bool {
	return strings.EqualFold(spell.School, "evocation") && isSpellOnClassList(spellKey, "wizard")
}

// empoweredEvocation: + Intelligence modifier to one damage roll of a Wizard Evocation spell (Evoker level 10).
func empoweredEvocation(charID int, spellKey string, spell SRDSpell) (int, string) {
	if !wizardEvocation(spellKey, spell) || !evokerHas(charID, "empowered_evocation") {
		return 0, ""
	}
	var intl int
	db.QueryRow(`SELECT intl FROM characters WHERE id = $1`, charID).Scan(&intl)
	b := max(0, game.Modifier(intl))
	return b, fmt.Sprintf(" (Empowered Evocation: +%d)", b)
}

var sculptLeft sync.Map // charID -> creatures still protected by Sculpt Spells in the spell being resolved

// sculptSpells: "sculpt" in the description of a Wizard Evocation spell: 1 + the spell's level of the creatures in the area (the
// characters among the targets) automatically succeed on their saves and take no damage that would be halved on a success.
func sculptSpells(charID int, spellKey string, spell SRDSpell, slotLevel int, description string) {
	sculptLeft.Delete(charID)
	if hasWord(description, "sculpt") && wizardEvocation(spellKey, spell) && evokerHas(charID, "sculpt_spells") {
		sculptLeft.Store(charID, 1+max(spell.Level, slotLevel))
	}
}

// sculpted: the next character target is protected (and counted).
func sculpted(charID int) bool {
	v, ok := sculptLeft.Load(charID)
	if !ok || v.(int) <= 0 {
		return false
	}
	sculptLeft.Store(charID, v.(int)-1)
	return true
}

// potentCantrip: an Evoker's cantrip that misses, or whose save succeeds, deals half its damage (Evoker level 6).
func potentCantrip(charID int, spell SRDSpell) bool {
	return spell.Level == 0 && evokerHas(charID, "potent_cantrip")
}

// handHeld: an item named in the character's hand counts as held even when its inventory entry has no place yet (F118).
func handHeld(charID int, it invItem) {
	if itemStr(it, "location") == "held" {
		return
	}
	h := handsOf(charID)
	if n := itemStr(it, "name"); n != "" && (strings.EqualFold(h.Main, n) || strings.EqualFold(h.Off, n)) {
		it["location"] = "held"
	}
}

// signatureFree: "signature" in a level 20 Wizard's cast description, for one of the two Signature Spells: no slot, once per rest (F120).
func signatureFree(charID int, spellKey string) bool {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(signature_spells,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var list []string
	jsonUnmarshal(raw, &list)
	for _, s := range list {
		if s == spellKey {
			return true
		}
	}
	return false
}

// eversmokingBottle: a Magic action opens or closes it (F121).
func eversmokingBottle(charID int, it invItem) string {
	open := false
	editInventory(charID, func(inv []invItem) []invItem {
		for _, x := range inv {
			if itemStr(x, "id") == itemStr(it, "id") {
				open = x["open"] != true
				x["open"] = open
			}
		}
		return inv
	})
	if open {
		return "You open the Eversmoking Bottle (a Magic action): thick smoke fills a 60-foot Emanation from the bottle, Heavily Obscured, growing 10 feet each minute it stays open, to 120 feet. Mark the smoke on the battle map (GM). Use it again to close it."
	}
	return "You close the Eversmoking Bottle (a Magic action): the cloud stays where it is and disperses after 10 minutes (1 minute in a strong wind such as Gust of Wind)."
}

// clearSignatureUses: a Short or Long Rest gives the Signature Spells back (F120).
func clearSignatureUses(charID int) {
	used := classResourcesUsed(charID)
	changed := false
	for k := range used {
		if strings.HasPrefix(k, "signature_") {
			delete(used, k)
			changed = true
		}
	}
	if changed {
		writeUsed(charID, used)
	}
}
