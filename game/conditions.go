// Package game provides the core game rules (SRD 5.2.1).
// conditions.go handles 5e condition effects and checks.
package game

import (
	"strings"
)

// Condition constants for the 15 PHB conditions
const (
	ConditionBlinded       = "blinded"
	ConditionCharmed       = "charmed"
	ConditionDeafened      = "deafened"
	ConditionFrightened    = "frightened"
	ConditionGrappled      = "grappled"
	ConditionIncapacitated = "incapacitated"
	ConditionInvisible     = "invisible"
	ConditionParalyzed     = "paralyzed"
	ConditionPetrified     = "petrified"
	ConditionPoisoned      = "poisoned"
	ConditionProne         = "prone"
	ConditionRestrained    = "restrained"
	ConditionStunned       = "stunned"
	ConditionUnconscious   = "unconscious"
	ConditionExhaustion    = "exhaustion"
)

// ConditionInfo describes the mechanical effects of a condition.
type ConditionInfo struct {
	Name        string
	Description string
	Effects     []string
}

// HasCondition checks if a condition list contains a specific condition.
// Case-insensitive matching.
func HasCondition(conditions []string, condition string) bool {
	condition = strings.ToLower(condition)
	for _, c := range conditions {
		if strings.ToLower(c) == condition {
			return true
		}
		// Handle prefixed conditions like "charmed:123" or "frightened:456"
		if strings.HasPrefix(strings.ToLower(c), condition+":") {
			return true
		}
	}
	return false
}

// IsIncapacitated checks if conditions prevent taking actions or reactions.
// Per 5e: paralyzed, stunned, unconscious, petrified, and incapacitated all prevent actions.
func IsIncapacitated(conditions []string) bool {
	incapConditions := []string{
		ConditionIncapacitated, ConditionParalyzed, ConditionStunned,
		ConditionUnconscious, ConditionPetrified,
	}
	for _, inc := range incapConditions {
		if HasCondition(conditions, inc) {
			return true
		}
	}
	return false
}

// SpeedZero reports whether a condition sets Speed to 0 (SRD 5.2.1: Grappled, Paralyzed, Petrified,
// Restrained, Unconscious). Stunned does not.
func SpeedZero(condition string) bool {
	switch strings.ToLower(strings.SplitN(condition, ":", 2)[0]) {
	case ConditionGrappled, ConditionParalyzed, ConditionPetrified, ConditionRestrained, ConditionUnconscious:
		return true
	}
	return false
}

// ExhaustionD20Penalty is the amount subtracted from every D20 Test: 2 times the Exhaustion level.
func ExhaustionD20Penalty(level int) int {
	if level < 0 {
		return 0
	}
	return 2 * level
}

// ExhaustionSpeedPenalty is the number of feet Speed is reduced: 5 times the Exhaustion level.
func ExhaustionSpeedPenalty(level int) int {
	if level < 0 {
		return 0
	}
	return 5 * level
}

// AutoFailsSave checks if conditions cause automatic save failure.
// Paralyzed, Petrified, Stunned and Unconscious fail Strength and Dexterity saves automatically (SRD 5.2.1).
func AutoFailsSave(conditions []string, ability string) bool {
	ability = strings.ToUpper(ability)
	if ability != "STR" && ability != "DEX" {
		return false
	}
	// These conditions cause auto-fail on STR/DEX saves
	autoFailConditions := []string{
		ConditionParalyzed, ConditionPetrified, ConditionStunned, ConditionUnconscious,
	}
	for _, cond := range autoFailConditions {
		if HasCondition(conditions, cond) {
			return true
		}
	}
	return false
}

// IsAutoCrit checks if conditions make melee attacks automatic crits.
// Paralyzed and unconscious targets take auto-crits from melee hits within 5ft.
func IsAutoCrit(conditions []string) bool {
	autoCritConditions := []string{ConditionParalyzed, ConditionUnconscious}
	for _, cond := range autoCritConditions {
		if HasCondition(conditions, cond) {
			return true
		}
	}
	return false
}

// GetSaveDisadvantage checks if conditions grant Disadvantage on a saving throw.
// Restrained gives Disadvantage on Dexterity saves. Exhaustion (2024) is a -2 per level penalty
// on the D20 Test, not Disadvantage; the level argument is kept for the callers.
func GetSaveDisadvantage(conditions []string, exhaustionLevel int, ability string) bool {
	ability = strings.ToUpper(ability)
	if ability == "DEX" && HasCondition(conditions, ConditionRestrained) {
		return true
	}
	return false
}

// GetAttackDisadvantage checks if conditions grant Disadvantage on attack rolls (SRD 5.2.1):
// Blinded, Poisoned, Prone, Restrained, and Frightened while the source of fear is in line of sight.
func GetAttackDisadvantage(conditions []string, exhaustionLevel int, frightenedSourceVisible bool) bool {
	for _, cond := range []string{ConditionBlinded, ConditionPoisoned, ConditionProne, ConditionRestrained} {
		if HasCondition(conditions, cond) {
			return true
		}
	}
	if frightenedSourceVisible && HasCondition(conditions, ConditionFrightened) {
		return true
	}
	return false
}

// GetAbilityCheckDisadvantage checks if conditions grant Disadvantage on ability checks:
// Poisoned, and Frightened while the source of fear is in line of sight.
func GetAbilityCheckDisadvantage(conditions []string, exhaustionLevel int, frightenedSourceVisible bool) bool {
	if HasCondition(conditions, ConditionPoisoned) {
		return true
	}
	if frightenedSourceVisible && HasCondition(conditions, ConditionFrightened) {
		return true
	}
	return false
}

// GetAttackAdvantage checks if target's conditions grant advantage to attackers.
// Blinded, paralyzed, prone (within 5ft), restrained, stunned, unconscious targets
// give attackers advantage.
func GetAttackAdvantage(targetConditions []string, isMelee bool, withinFiveFeet bool) bool {
	// Always advantage against these
	advantageConditions := []string{
		ConditionBlinded, ConditionParalyzed, ConditionPetrified, ConditionRestrained,
		ConditionStunned, ConditionUnconscious,
	}
	for _, cond := range advantageConditions {
		if HasCondition(targetConditions, cond) {
			return true
		}
	}

	// Prone: advantage if within 5ft, disadvantage otherwise
	if HasCondition(targetConditions, ConditionProne) {
		return withinFiveFeet
	}

	return false
}

// GetAttackDisadvantageVsTarget checks if target's conditions grant disadvantage to attackers.
// Invisible targets grant disadvantage (unless attacker has blindsight/truesight).
// Prone targets grant disadvantage on ranged attacks from > 5ft.
func GetAttackDisadvantageVsTarget(targetConditions []string, isRanged bool, withinFiveFeet bool) bool {
	// Invisible = disadvantage (blindsight/truesight should be checked separately)
	if HasCondition(targetConditions, ConditionInvisible) {
		return true
	}

	// Prone = disadvantage if ranged (not within 5ft)
	if HasCondition(targetConditions, ConditionProne) && isRanged && !withinFiveFeet {
		return true
	}

	return false
}

// ExhaustionEffects returns the SRD 5.2.1 effects for a given Exhaustion level.
func ExhaustionEffects(level int) string {
	switch {
	case level <= 0:
		return "No exhaustion"
	case level >= 6:
		return "DEAD (exhaustion level 6)"
	}
	return "D20 Tests are reduced by " + itoa(ExhaustionD20Penalty(level)) + "; Speed is reduced by " + itoa(ExhaustionSpeedPenalty(level)) + " feet"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

// SpeedZeroAny reports whether any condition in the list sets Speed to 0.
func SpeedZeroAny(conditions []string) bool {
	for _, c := range conditions {
		if SpeedZero(c) {
			return true
		}
	}
	return false
}
