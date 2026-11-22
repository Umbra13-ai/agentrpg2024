package main

import (
	"regexp"
	"strings"
)

// Accept the complete simple rule. Clauses with other timing or extra
// effects stay in the GM's trait list for adjudication.
var damageDisadvantageRe = regexp.MustCompile(`(?i)^\s*If (?:it|the [a-z][a-z '’\-]*?) takes (Acid|Bludgeoning|Cold|Fire|Force|Lightning|Necrotic|Piercing|Poison|Psychic|Radiant|Slashing|Thunder) damage, it has Disadvantage on attack rolls( and ability checks)? until the end of its next turn\.\s*$`)

const damageDisadvantageMarker = "damage_disadvantage"

type damageDisadvantage struct {
	name          string
	abilityChecks bool
}

// amounts contains damage by type after the caller's damage defenses. Zero and
// absorbed amounts are not damage taken. Temporary HP still takes damage.
func damageDisadvantages(key string, amounts map[string]int) []damageDisadvantage {
	if len(amounts) == 0 {
		return nil
	}
	meta := monsterMetaFor(key)
	if meta == nil {
		return nil
	}
	var affected []damageDisadvantage
	for _, t := range meta.Traits {
		match := damageDisadvantageRe.FindStringSubmatch(t.Text)
		if match != nil && amounts[strings.ToLower(match[1])] > 0 {
			affected = append(affected, damageDisadvantage{t.Name, match[2] != ""})
		}
	}
	return affected
}

func damageTraitGMNotices(lobby, id int, name string, penalties []damageDisadvantage) {
	for range penalties {
		// "... and ability checks": the check owner reads the marker (check_2024.go checkCtx.sources); nothing is left for the GM
		noticeIfCompanionSourceHasNoTurn(lobby, id, id, damageDisadvantageMarker, notesUntilEndOfSourceTurn)
	}
}

func damageDisadvantageForAttack(lobby, attacker int) string {
	if lobby <= 0 || (attacker >= 0 && !isCompanionCombatID(attacker)) {
		return ""
	}
	var sources []string
	for _, r := range loadConditionRecords(lobby, attacker) {
		if r.Condition == damageDisadvantageMarker {
			sources = append(sources, r.Source)
		}
	}
	return strings.Join(sources, ", ")
}
