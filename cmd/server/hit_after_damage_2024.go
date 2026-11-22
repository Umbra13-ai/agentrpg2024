package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// hitAfterDamage is what a stat-block hit does to its target after the damage it dealt, called once by every hit route (a character,
// monster_attacks.go; a monster, monster_attack_continuation_2024.go; a companion, companionHitRiders): the Hit Point maximum drain
// (drainHPMaximum) and "it starts burning" (SRD 5.2.1 Magmin Touch: the Burning owner, burning_2024.go). A new effect of this kind is one
// call here, never one more call in each route. taken is the damage the hit dealt; byType what each damage type of it dealt (a typed
// drain: "the Acid damage taken").
func hitAfterDamage(lobby, attacker, target int, desc, source string, taken int, byType map[string]int) string {
	recordLastHit(lobby, attacker, target, taken) // what it hit, for an entry used right after (Rampage; creature_movement_2024.go)
	return drainHPMaximum(lobby, target, desc, taken, byType) + burnOnHit(lobby, target, desc, source) + abilityDecreaseOnHit(lobby, target, desc, source) +
		corrodeArmorOnHit(lobby, target, desc) + // item_state_2024.go
		hitForcedMove(lobby, attacker, target, desc) // a push or pull (creature_movement_2024.go)
}

// abilityDecreaseRe: "and the target's Strength score decreases by 1d4. The target dies if this reduces that score to 0." (SRD 5.2.1
// Shadow Draining Swipe): the ability, the amount, and whether a score of 0 kills.
var abilityDecreaseRe = regexp.MustCompile(`(?i),?\s*(?:and\s+)?(?:the target['’]s|its) (Strength|Dexterity|Constitution|Intelligence|Wisdom|Charisma) score decreases by (\d+d\d+(?:\s*[+-]\s*\d+)?|\d+)\.?(\s*The target dies if this reduces that score to 0\.?)?`)

// stripAbilityDecrease removes that sentence from a residual (applied by abilityDecreaseOnHit on every hit route).
func stripAbilityDecrease(text string) string { return abilityDecreaseRe.ReplaceAllString(text, "") }

// abilityDecreaseOnHit lowers the target's score through the ability score owner (reduceAbility, ability_owner_2024.go) and kills a
// target whose score falls to 0 when the text says so (a character through the Hit Point owner, a monster or companion at 0 Hit Points).
func abilityDecreaseOnHit(lobby, target int, desc, source string) string {
	m := abilityDecreaseRe.FindStringSubmatch(hitDamageText(desc))
	if m == nil {
		return ""
	}
	n := game.RollDamage(strings.ReplaceAll(m[2], " ", ""), false)
	name := creatureName(lobby, target)
	now, err := reduceAbility(lobby, target, m[1], n, source)
	if err != nil {
		return fmt.Sprintf(" [%s's %s score decreases by %s: not recorded (%s); the GM applies it]", name, m[1], m[2], notFoundOrDBError(err, "no such creature"))
	}
	text := fmt.Sprintf(" [%s's %s score decreases by %d (%s), to %d until an effect such as Greater Restoration ends the reduction", name, m[1], n, m[2], now)
	if now == 0 && m[3] != "" {
		switch {
		case target > 0 && !isCompanionCombatID(target):
			killCharacter(target) // the Hit Point owner (hit_points_2024.go)
		case isCompanionCombatID(target):
			if c, ok := loadCompanion(target - companionIDBase); ok {
				changeCompanionHP(c.ID, func(v game.Vitals) game.Vitals { return v.SetHP(0) }, nil)
			}
		default:
			setMonsterHP(lobby, target, 0) // monster_hp_2024.go
		}
		text += fmt.Sprintf("; %s dies: its %s is 0", name, m[1])
	}
	return text + "]"
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
