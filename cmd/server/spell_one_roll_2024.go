package main

// The bonuses a spell's damage gets once: "add your Intelligence modifier to one damage roll of that spell" (the Evoker's Empowered
// Evocation, a Wizard spell of the Evocation school) and "add your Charisma modifier to one damage roll of that spell" (the Draconic
// Sorcerer's Elemental Affinity, damage of the ancestry's type). spellOneRollBonus is the one reader. The player's cast text adds it to the
// damage it shows, a spell that rolls separate beams or darts (Magic Missile, Scorching Ray, Eldritch Blast) adds it to the first one that
// hits (`firstHit`), and the GM's area cast adds it to the damage it rolls. Staged test I-ALDEN-PARTY-02: Magic Missile announced
// "Empowered Evocation +5" and left it out of the total, because only the cast text's damage had it.

import (
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// oneRollBonus is what a caster adds to one damage roll of a spell, by feature.
type oneRollBonus struct {
	Empowered   int    // Empowered Evocation (Intelligence modifier)
	Affinity    int    // Elemental Affinity (Charisma modifier)
	AncestryDmg string // the damage type that gave the Affinity
}

func (b oneRollBonus) Total() int { return b.Empowered + b.Affinity }

// Note is the bracket a cast text adds ("" when there is no bonus): " (Elemental Affinity: +3)" then " (Empowered Evocation: +5)".
func (b oneRollBonus) Note() string {
	var s string
	if b.Affinity != 0 {
		s += fmt.Sprintf(" (Elemental Affinity: +%d)", b.Affinity)
	}
	if b.Empowered != 0 {
		s += fmt.Sprintf(" (Empowered Evocation: +%d)", b.Empowered)
	}
	return s
}

// spellOneRollBonus reads the caster's features against the spell: Empowered Evocation for a Wizard Evocation spell, Elemental Affinity for
// damage of the Draconic Sorcerer's ancestry type. A feature that adds nothing (a modifier of 0 or less) is not reported.
func spellOneRollBonus(charID int, spellKey string, spell SRDSpell) oneRollBonus {
	var b oneRollBonus
	class, sub, level := subclassOf(charID)
	if wizardEvocation(spellKey, spell) && evokerHas(charID, "empowered_evocation") {
		var intl int
		db.QueryRow(`SELECT intl FROM characters WHERE id = $1`, charID).Scan(&intl)
		b.Empowered = max(0, game.Modifier(intl))
	}
	if strings.EqualFold(class, "sorcerer") && strings.EqualFold(sub, "draconic") && spell.DamageType != "" && hasSubclassFeature(sub, level, "elemental_affinity") {
		if ancestry := getDragonAncestryDamageType(charID); ancestry != "" && strings.EqualFold(spell.DamageType, ancestry) {
			var cha int
			db.QueryRow(`SELECT cha FROM characters WHERE id = $1`, charID).Scan(&cha)
			if m := game.Modifier(cha); m > 0 {
				b.Affinity, b.AncestryDmg = m, ancestry
			}
		}
	}
	return b
}

// firstHit adds the bonus to the first damage roll of a spell that rolls several (the first beam or dart that hits) and then gives it up:
// it returns the damage with the bonus and the bracket for that roll's line, and nothing for the rest.
type firstHit struct{ left oneRollBonus }

func (f *firstHit) add(d int) (int, string) {
	b := f.left
	if b.Total() == 0 {
		return d, ""
	}
	f.left = oneRollBonus{}
	return d + b.Total(), b.Note()
}
