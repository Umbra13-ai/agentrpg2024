package main

import (
	"fmt"
	"regexp"
	"strings"
)

// A hit whose target loses Hit Point maximum: "If the target is a creature, its Hit Point maximum decreases by an amount equal to the
// damage taken." (SRD 5.2.1 Specter and Wraith Life Drain). The SRD glossary's Long Rest gives it back ("If your Hit Point maximum was
// reduced, it returns to normal"). One reader (hpMaxDrainRe) and one writer per target kind: reduceMaxHP (a character, recorded for the
// Long Rest, hit_points_2024.go), reduceMonsterMaxHP and reduceCompanionMaxHP (monster_hp_2024.go). Every hit route calls drainHPMaximum
// with the damage the hit dealt (Temporary Hit Points included, as the server's Concentration DC counts damage taken): the character
// route (monster_attacks.go), the monster route (monster_attack_continuation_2024.go) and the companion route (companionHitRiders).
// Not yet automated, left in the GM's text: the same clause in a save effect (Wight, Succubus, Vampire, Vampire Spawn: the save owner
// does not hand the damage taken to its riders) and the typed variant "the Acid damage taken" (Clay Golem). The maximum never drops below
// 1 here: a text that kills at 0 is the GM's.
var hpMaxDrainRe = regexp.MustCompile(`(?i)\b(?:its|the target['’]s) Hit Point maximum decreases by an amount equal to the damage taken\b`)

// hpMaxDrainSentenceRe is the sentence the clause sits in, with its creature gate: what the GM no longer reads once it is applied.
var hpMaxDrainSentenceRe = regexp.MustCompile(`(?i)(?:If the target is a creature, )?(?:its|the target['’]s) Hit Point maximum decreases by an amount equal to the damage taken\.?`)

// drainHPMaximum applies the clause of a hit's text to its target for the damage it took; "" when the text has none or nothing was taken.
func drainHPMaximum(lobby, target int, desc string, taken int) string {
	if taken <= 0 || !hpMaxDrainRe.MatchString(hitDamageText(desc)) {
		return ""
	}
	var cut int
	var err error
	switch {
	case isCompanionCombatID(target):
		cut, err = reduceCompanionMaxHP(target-companionIDBase, taken)
	case target < 0:
		cut, err = reduceMonsterMaxHP(lobby, target, taken)
	default:
		cut, err = reduceMaxHP(target, taken)
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

// hitDamageText is the text after "Hit:" (the clause belongs to the hit, not to a save effect elsewhere in the entry).
func hitDamageText(desc string) string {
	if i := strings.Index(desc, "Hit:"); i >= 0 {
		return desc[i:]
	}
	return ""
}
