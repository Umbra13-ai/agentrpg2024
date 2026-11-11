package game

// Multiclassing, 2024 rules (SRD 5.2.1, "Character Creation", Multiclassing).

import "strings"

var fullCasters = map[string]bool{"bard": true, "cleric": true, "druid": true, "sorcerer": true, "wizard": true}
var halfCasters = map[string]bool{"paladin": true, "ranger": true}

// MulticlassSlots returns a character's spell slots (the Spellcasting feature) and Pact Magic slots, kept apart because each is spent
// and recovered on its own:
//   - with the Spellcasting feature from only one class, that class's own table at its level;
//   - from more than one class, the Multiclass Spellcaster table at the total of the Bard, Cleric, Druid, Sorcerer and Wizard levels plus
//     half the Paladin and Ranger levels, rounded up (the 2014 rule rounded down);
//   - Warlock levels give Pact Magic slots from the Warlock table at the Warlock level.
func MulticlassSlots(classLevels map[string]int) (regular, pact map[int]int) {
	regular, pact = map[int]int{}, map[int]int{}
	casters, combined, only, onlyLevel := 0, 0, "", 0
	for c, l := range classLevels {
		c = strings.ToLower(c)
		switch {
		case l <= 0:
			continue
		case fullCasters[c]:
			combined += l
		case halfCasters[c]:
			combined += (l + 1) / 2
		case c == "warlock":
			pact = SpellSlots("warlock", l)
			continue
		default:
			continue
		}
		casters++
		only, onlyLevel = c, l
	}
	switch {
	case casters == 1:
		regular = SpellSlots(only, onlyLevel)
	case casters > 1:
		regular = SpellSlots("wizard", combined) // the Multiclass Spellcaster table is the full caster table
	}
	if regular == nil {
		regular = map[int]int{}
	}
	if pact == nil {
		pact = map[int]int{}
	}
	return regular, pact
}

// MulticlassExtraAttacks: Extra Attack doesn't stack between classes; the most attacks any one class gives (Fighter 11: 3).
func MulticlassExtraAttacks(classLevels map[string]int) int {
	best := 1
	for c, l := range classLevels {
		if n := ExtraAttackCount(c, l); n > best {
			best = n
		}
	}
	return best
}
