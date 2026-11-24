package main

import (
	"fmt"
	"regexp"
	"strings"
)

// A creature that loses Hit Point maximum from the damage it took: "If the target is a creature, its Hit Point maximum decreases by an
// amount equal to the damage taken." (SRD 5.2.1 Specter and Wraith Life Drain, a hit), "Failure: 6 (1d8 + 2) Necrotic damage, and the
// target's Hit Point maximum decreases by an amount equal to the damage taken." (Wight, a saving throw), "... equal to the Necrotic damage
// taken, and the vampire regains Hit Points equal to that amount" (Vampire, Vampire Spawn: one damage type, and the source heals) and
// "... equal to the Acid damage taken" (Clay Golem, a hit). The SRD glossary's Long Rest gives it back ("If your Hit Point maximum was
// reduced, it returns to normal"). One reader (hpMaxDrainOf) and one writer per target kind (reduceHPMaximum): reduceMaxHP (a character,
// recorded for the Long Rest, hit_points_2024.go), reduceMonsterMaxHP and reduceCompanionMaxHP (monster_hp_2024.go). Every hit route
// calls drainHPMaximum through hitAfterDamage with the damage the hit dealt and what each type of it dealt; the saving throw owner calls
// saveEffect.drainAfterDamage with what each damage part of the effect dealt to each target (monster_effects_2024.go, the monster and
// companion save routes). Damage taken counts Temporary Hit Points, as the server's Concentration DC does. The maximum never drops below 1
// here: a text that kills at 0 is the GM's.
var hpMaxDrainRe = regexp.MustCompile(`(?i)\b(?:its|the target['’]s) Hit Point maximum decreases by an amount equal to the (?:([a-z]+) )?damage taken\b`)

// hpMaxDrainSentenceRe is the clause with its creature gate and the source's regain (healed by drainRegain): what the GM no longer reads
// once it is applied.
var hpMaxDrainSentenceRe = regexp.MustCompile(`(?i)(?:If the target is a creature, )?(?:and )?(?:its|the target['’]s) Hit Point maximum decreases by an amount equal to the (?:[a-z]+ )?damage taken(?:, and the [\w -]+? regains Hit Points equal to that amount)?\.?`)

// drainRegainRe: "..., and the vampire regains Hit Points equal to that amount": the source heals by what the drain took.
var drainRegainRe = regexp.MustCompile(`(?i)\bdamage taken, and the [\w -]+? regains Hit Points equal to that amount\b`)

// hpMaxDrainOf reads the clause: ok, and the damage type it counts ("" for all the damage taken).
func hpMaxDrainOf(text string) (dtype string, ok bool) {
	m := hpMaxDrainRe.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	return strings.ToLower(m[1]), true
}

// drainedAmount: the damage the clause counts, from the damage taken (total) and what each type of it dealt.
func drainedAmount(dtype string, total int, byType map[string]int) int {
	if dtype == "" {
		return total
	}
	return min(byType[dtype], total)
}

// drainHPMaximum applies the clause of a hit's text to its target for the damage it took; "" when the text has none or nothing was taken.
func drainHPMaximum(lobby, attacker, target int, entry, desc string, taken int, byType map[string]int) string {
	dtype, ok := hpMaxDrainOf(hitDamageText(desc))
	if !ok {
		dtype, ok = mechanicHPMaxDrain(lobby, attacker, entry) // an attached max_hp_drain (monster_mechanics_2024.go)
	}
	if !ok {
		return ""
	}
	return reduceHPMaximum(lobby, target, drainedAmount(dtype, taken, byType))
}

// reduceHPMaximum lowers a creature's Hit Point maximum by amount (a character's until a Long Rest) and answers the note.
func reduceHPMaximum(lobby, target, amount int) string {
	if amount <= 0 {
		return ""
	}
	var cut int
	var err error
	switch {
	case isCompanionCombatID(target):
		cut, err = reduceCompanionMaxHP(target-companionIDBase, amount)
	case target < 0:
		cut, err = reduceMonsterMaxHP(lobby, target, amount)
	default:
		cut, err = reduceMaxHP(target, amount)
	}
	if err != nil {
		logDBProblem("hit point maximum drain", err)
		return " [GM: the Hit Point maximum reduction could not be recorded; apply it by hand.]"
	}
	if cut <= 0 {
		return ""
	}
	if target > 0 && !isCompanionCombatID(target) {
		return fmt.Sprintf(" [Hit Point maximum -%d until a Long Rest]", cut)
	}
	return fmt.Sprintf(" [Hit Point maximum -%d]", cut)
}

// drainRegain heals the source monster by the amount drained when the text says so (Vampire, Vampire Spawn Bite).
func drainRegain(lobby, source int, amount int) string {
	if amount <= 0 || source >= 0 || isCompanionCombatID(source) {
		return ""
	}
	before, after, err := healMonster(lobby, source, amount) // the monster Hit Point owner: no healing past the maximum, none while it can't regain
	if err != nil {
		logDBProblem("drain regain", err)
		return ""
	}
	if after.HP > before.HP {
		return fmt.Sprintf(" [%s regains %d Hit Points]", creatureName(lobby, source), after.HP-before.HP)
	}
	return ""
}

// hitDamageText is the text after "Hit:" (the clause belongs to the hit, not to a save effect elsewhere in the entry).
func hitDamageText(desc string) string {
	if i := strings.Index(desc, "Hit:"); i >= 0 {
		return desc[i:]
	}
	return ""
}
