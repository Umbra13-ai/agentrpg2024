package main

import (
	"regexp"
	"strings"
)

// Fixes from the coverage plan's small groups (level20-battle-tests/s40-s46, Nov 9 2026; FINDINGS F193-):
//	Petrified monsters   Resistance to all damage (SRD 5.2.1 Petrified); characters had it, monsters didn't (F196)
//	Light extra attack   allowed once the Attack action has begun, not only after its last attack (F197, actions.go offhand_attack)
//	Transmuted Spell     "transmuted lightning bolt into fire": the type after "into"/"to" wins; the spell's own type is skipped (F198)
//	Eldritch Spear       2024: +30 feet of range per Warlock level (720 feet at level 20), in the map's range check too (F195)
//	Witch Sight          Truesight 30 feet on the sheet; Devil's Sight 120 feet there too (F199); no pointer to a missing endpoint
//	Pact of the Blade    "psychic damage" (or necrotic, radiant) in the attack: the bonded weapon deals that type (F200)
//	Pact weapon attacks  resources_remaining and my-turn count Thirsting / Devouring Blade's attacks (F201)

// monsterPetrified: the combatant has the Petrified condition.
func monsterPetrified(lobbyID, id int) bool {
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND condition = 'petrified'`, lobbyID, id).Scan(&n)
	return n > 0
}

// petrifiedDamage halves damage to a Petrified monster that doesn't already resist it (or isn't immune).
func petrifiedDamage(lobbyID, id, d int, how string) (int, string) {
	if how == "resistant" || how == "immune" || d <= 0 || !monsterPetrified(lobbyID, id) {
		return d, how
	}
	return d / 2, "resistant"
}

// transmutedType picks Transmuted Spell's new damage type from the description: "into <type>" or "to <type>" first, then
// "transmuted <type>" other than the spell's own type (F198).
func transmutedType(desc, current string) string {
	types := []string{"acid", "cold", "fire", "lightning", "poison", "thunder"}
	for _, pre := range []string{"into ", "to "} {
		for _, t := range types {
			if strings.Contains(desc, pre+t) {
				return t
			}
		}
	}
	for _, t := range types {
		if !strings.EqualFold(t, current) && (strings.Contains(desc, "transmuted "+t) || strings.Contains(desc, "transmute "+t)) {
			return t
		}
	}
	return current
}

// eldritchSpearBonus: Eldritch Spear adds 30 feet per Warlock level to Eldritch Blast's range (SRD 5.2.1; F195). 0 without it.
func eldritchSpearBonus(charID int) int {
	if !hasInvocation(charID, "eldritch-spear") {
		return 0
	}
	lv := classLevelsOf(charID)["warlock"]
	if lv == 0 {
		if class, _, level := subclassOf(charID); strings.EqualFold(class, "warlock") {
			lv = level
		}
	}
	return 30 * lv
}

var pactDamageRe = regexp.MustCompile(`(?i)\b(necrotic|psychic|radiant) damage\b`)

// pactDamageType: "psychic damage" (or necrotic, radiant) in a pact weapon attack's description (F200); "" otherwise.
func pactDamageType(desc string) string {
	if m := pactDamageRe.FindStringSubmatch(desc); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}
