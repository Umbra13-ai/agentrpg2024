package main

import (
	"fmt"
	"regexp"
	"strings"
)

// hitAfterDamage is what a stat-block hit does to its target after the damage it dealt, called once by every hit route (a character,
// monster_attacks.go; a monster, monster_attack_continuation_2024.go; a companion, companionHitRiders): the Hit Point maximum drain
// (drainHPMaximum) and "it starts burning" (SRD 5.2.1 Magmin Touch: the Burning owner, burning_2024.go). A new effect of this kind is one
// call here, never one more call in each route. taken is the damage the hit dealt; byType what each damage type of it dealt (a typed
// drain: "the Acid damage taken").
func hitAfterDamage(lobby, attacker, target int, desc, source string, taken int, byType map[string]int) string {
	recordLastHit(lobby, attacker, target, taken) // what it hit, for an entry used right after (Rampage; creature_movement_2024.go)
	return drainHPMaximum(lobby, target, desc, taken, byType) + burnOnHit(lobby, target, desc, source) +
		hitForcedMove(lobby, attacker, target, desc) // a push or pull (creature_movement_2024.go)
}

// burnOnHitRe: "If the target is a creature or a flammable object that isn't being worn or carried, it starts burning." A sentence with
// this clause applies to a creature target only when it has no gate or its gate names a creature; "If the target is a flammable object
// ..., it starts burning" (Barbed and Horned Devil Hurl Flame) is about objects, never a creature, and stays in the GM's text.
var burnOnHitRe = regexp.MustCompile(`(?i)\b(?:it|the target) starts burning\b`)

// burnSentenceForCreatures: the sentence of text that sets a creature target burning ("" when none does).
func burnSentenceForCreatures(text string) string {
	for _, sentence := range sentenceSplitRe.Split(text, -1) {
		if !burnOnHitRe.MatchString(sentence) {
			continue
		}
		gated := regexp.MustCompile(`(?i)^\s*If the target is\b`).MatchString(sentence)
		if !gated || regexp.MustCompile(`(?i)\bcreature\b`).MatchString(sentence) {
			return sentence
		}
	}
	return ""
}

// stripBurnForCreatures removes that sentence from a residual (applied by burnOnHit); an object-only sentence stays.
func stripBurnForCreatures(text string) string {
	if s := burnSentenceForCreatures(text); s != "" {
		return strings.Replace(text, s, "", 1)
	}
	return text
}

// burnOnHit sets the target burning (startBurning) when the hit's text says so for a creature.
func burnOnHit(lobby, target int, desc, source string) string {
	if burnSentenceForCreatures(hitDamageText(desc)) == "" {
		return ""
	}
	startBurning(lobby, target, source)
	return fmt.Sprintf(" [%s starts burning: 1d4 Fire damage at the start of each of its turns until the fire is put out]", creatureName(lobby, target))
}
