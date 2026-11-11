package main

// Fixes from Halvard's coverage session (level20-battle-tests/s32_halvard_paladin.py, Nov 9 2026; FINDINGS F140-F142):
//	Attack notes             Divine Smite, Lifedrinker, Hunter's Mark, Colossus Slayer and Divine Strike notes never reached a targeted
//	                         attack's reply (only Sneak Attack's did): the damage was added without saying why (F140, actions.go)
//	Brooch of Shielding      worn and attuned: Resistance to Force damage, Immunity to Magic Missile's damage (F141)
//	Periapt of Wound Closure worn and attuned: a Death Saving Throw of 9 or lower becomes a 10; a Hit Point Die heals double (F142)

import "strings"

// broochOfShielding: the brooch gives Resistance to Force damage.
func broochOfShielding(charID int, dtype string) bool {
	if !strings.EqualFold(dtype, "force") {
		return false
	}
	if _, ok := itemActive(charID, "brooch of shielding", "worn"); !ok {
		return false
	}
	_, att := attunedTo(charID, "brooch of shielding")
	return att
}

// magicMissileImmune: the brooch makes the wearer immune to Magic Missile's damage.
func magicMissileImmune(charID int, spellName string) bool {
	if !strings.EqualFold(spellName, "magic missile") {
		return false
	}
	if _, ok := itemActive(charID, "brooch of shielding", "worn"); !ok {
		return false
	}
	_, att := attunedTo(charID, "brooch of shielding")
	return att
}

// periaptActive: the Periapt of Wound Closure is worn and attuned.
func periaptActive(charID int) bool {
	if _, ok := itemActive(charID, "periapt of wound closure", "worn"); !ok {
		return false
	}
	_, att := attunedTo(charID, "periapt of wound closure")
	return att
}
