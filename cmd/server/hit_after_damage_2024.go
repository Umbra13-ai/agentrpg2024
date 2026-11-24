package main

import (
	"fmt"
	"regexp"
	"strconv"
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
	return drainHPMaximum(lobby, attacker, target, desc, taken, byType) + burnOnHit(lobby, target, desc, source) + abilityDecreaseOnHit(lobby, target, desc, source) +
		corrodeArmorOnHit(lobby, target, desc) + // item_state_2024.go
		hitForcedMove(lobby, attacker, target, desc) + // a push or pull (creature_movement_2024.go)
		droppedToZeroOnHit(lobby, attacker, target, desc, source, taken) +
		randomConditionOnHit(lobby, attacker, target, desc, source) + // an attached random_condition (monster_mechanics_2024.go)
		curseOnHit(lobby, attacker, target, desc, source) // an attached curse (monster_mechanics_2024.go)
}

// dropRiderRe: what a hit does when it reduces its target to 0 Hit Points ("If this damage reduces the target to 0 Hit Points, the
// target becomes Stable, and it has the Poisoned condition for 1 hour. While Poisoned, the target also has the Paralyzed condition.":
// SRD 5.2.1 Phase Spider Bite; "If the target is reduced to 0 Hit Points by this attack, the target becomes Stable but has the Poisoned
// condition for 1 hour. While it has the Poisoned condition, the target has the Paralyzed condition.": Vampire Familiar Umbral Dagger).
// parseHitRider never applies these on a hit (hitRiderDeferredRe, dependsOnDeferred); this applies them when the hit did it.
var dropRiderRe = regexp.MustCompile(`(?i)If (?:this damage reduces the target to 0 Hit Points|the target is reduced to 0 Hit Points by this attack), the target becomes Stable,? (?:and it|but) has the ([A-Za-z]+) condition for (\d+) (minute|hour)s?\.\s*While (?:it has the )?([A-Za-z]+)(?: condition)?, the target (?:also )?has the ([A-Za-z]+) condition\.?`)

// dieOnDropRe: "The target dies if it is reduced to 0 Hit Points by this attack." (SRD 5.2.1 Gibbering Mouther Bite), "A creature dies
// and turns to dust if reduced to 0 Hit Points by this attack." (Mummy Rotting Fist): droppedToZeroOnHit kills it (what becomes of the
// body stays in the GM's text).
var dieOnDropRe = regexp.MustCompile(`(?i)(?:The target|A creature) dies(?: and turns to dust)? if (?:it is )?reduced to 0 Hit Points by this attack\.?`)

// stripDropRider removes those texts from a residual (applied by droppedToZeroOnHit on every hit route).
func stripDropRider(text string) string {
	return dieOnDropRe.ReplaceAllString(dropRiderRe.ReplaceAllString(text, ""), "")
}

// hitDroppedToZero: this hit, which dealt taken damage, reduced the target to 0 Hit Points. A character that was already at 0 took a
// Death Saving Throw failure from it (or is dead), so a character at 0 with no failures was dropped by this hit; a monster or companion
// at 0 after damage was.
func hitDroppedToZero(lobby, target, taken int) bool {
	if taken <= 0 {
		return false
	}
	if hp, _ := creatureHP(lobby, target); hp != 0 {
		return false
	}
	if target > 0 && !isCompanionCombatID(target) {
		var failures int
		var dead bool
		db.QueryRow(`SELECT COALESCE(death_save_failures,0), COALESCE(is_dead,false) FROM characters WHERE id=$1`, target).Scan(&failures, &dead)
		return failures == 0 && !dead
	}
	return true
}

// dieOnDrop: the target the hit dropped dies (a character through the Hit Point owner; a monster or companion at 0 is already dead).
func dieOnDrop(lobby, target int) string {
	name := creatureName(lobby, target)
	if target > 0 && !isCompanionCombatID(target) {
		if _, err := killCharacter(target); err != nil {
			return fmt.Sprintf(" [%s dies by this attack's rule: not recorded (%s); the GM applies it]", name, notFoundOrDBError(err, "no such character"))
		}
	}
	return fmt.Sprintf(" [%s is reduced to 0 Hit Points by this attack and dies]", name)
}

// dropRule: what a hit does to a target it reduces to 0 Hit Points: it dies, or it becomes Stable with First for Rounds and Second
// while First lasts (either may be empty). Read from the SRD wording (textDropRule) or from a drop_to_zero mechanic the GM attached
// (mechanicDropRule); applied by droppedToZeroOnHit only.
type dropRule struct {
	Dies          bool
	First, Second string
	Rounds        int
	Note          string // what follows that the server does not apply (an attached mechanic's note)
}

// textDropRule: the SRD wordings (dieOnDropRe, dropRiderRe) of the hit's text.
func textDropRule(hitText string) (dropRule, bool) {
	if dieOnDropRe.MatchString(hitText) {
		return dropRule{Dies: true}, true
	}
	m := dropRiderRe.FindStringSubmatch(hitText)
	if m == nil || !strings.EqualFold(m[1], m[4]) {
		return dropRule{}, false
	}
	n, _ := strconv.Atoi(m[2])
	return dropRule{First: strings.ToLower(m[1]), Second: strings.ToLower(m[5]), Rounds: n * map[string]int{"minute": 10, "hour": 600}[strings.ToLower(m[3])]}, true
}

// mechanicDropRule: the drop_to_zero mechanic attached to the attacking monster's entry (monster_mechanics_2024.go).
func mechanicDropRule(lobby, attacker int, desc string) (dropRule, bool) {
	m, ok := attachedMechanic(lobby, attacker, desc, "drop_to_zero")
	if !ok {
		return dropRule{}, false
	}
	return dropRule{Dies: m.str("result") == "dies", First: m.str("condition"), Second: m.str("also"), Rounds: m.num("minutes") * 10, Note: m.str("note")}, true
}

// droppedToZeroOnHit: the hit reduced its target to 0 Hit Points (it dealt damage and the target is at 0 now; a character that was already
// at 0 took a Death Saving Throw failure from it, so 0 failures means this hit dropped it): the target dies, or becomes Stable (a character
// through the Hit Point owner; a monster or companion has no Death Saving Throws: the GM keeps it alive at 0) and has the first condition
// for its time, and the second while it has the first (the condition owner, notesEndsWith). An attached mechanic replaces the text's rule.
func droppedToZeroOnHit(lobby, attacker, target int, desc, source string, taken int) string {
	rule, ok := mechanicDropRule(lobby, attacker, desc)
	if !ok {
		rule, ok = textDropRule(hitDamageText(desc))
	}
	if !ok || !hitDroppedToZero(lobby, target, taken) {
		return ""
	}
	note := ""
	if rule.Note != "" {
		note = "; the stat block adds: " + rule.Note
	}
	if rule.Dies {
		return strings.TrimSuffix(dieOnDrop(lobby, target), "]") + note + "]"
	}
	name := creatureName(lobby, target)
	text := ""
	if target > 0 && !isCompanionCombatID(target) {
		if _, err := stabilizeCharacter(target); err != nil {
			return fmt.Sprintf(" [%s drops to 0 Hit Points and becomes Stable by this attack's rule: not recorded (%s); the GM applies it]", name, notFoundOrDBError(err, "no such character"))
		}
		text = fmt.Sprintf(" [%s drops to 0 Hit Points and becomes Stable", name)
	} else {
		text = fmt.Sprintf(" [%s drops to 0 Hit Points; by this attack's rule it becomes Stable (the GM keeps it alive at 0)", name)
	}
	if rule.First == "" {
		return text + note + "]"
	}
	if res := addCondition(lobby, target, condAdd{Condition: rule.First, Source: source, Rounds: rule.Rounds, SourceID: attacker}); res.Immune != "" {
		return text + fmt.Sprintf("; not %s (%s)", titleCase(rule.First), res.Immune) + note + "]"
	}
	text += fmt.Sprintf("; %s for %s", titleCase(rule.First), minutesWords(rule.Rounds/10))
	if rule.Second != "" {
		if res := addCondition(lobby, target, condAdd{Condition: rule.Second, Source: source, SourceID: attacker, Notes: notesEndsWith + rule.First}); res.Immune != "" {
			text += fmt.Sprintf("; not %s (%s)", titleCase(rule.Second), res.Immune)
		} else {
			text += fmt.Sprintf(", and %s while %s", titleCase(rule.Second), titleCase(rule.First))
		}
	}
	return text + note + "]"
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
