package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Monster rules target the 330 SRD 5.2.1 stat blocks (owner, Nov 21). Every monster text pattern of the server must match at least
// one SRD stat block, or an invented test monster written in a test, so no rule is kept for wording the SRD doesn't have.
//
// monsterTextPatterns lists every package-level regular expression of the files that read stat-block text (monsterTextFile);
// TestEveryMonsterPatternIsListed (go/ast) fails on one that is in neither this list nor notMonsterText, so a new pattern is
// classified when it is written.
var monsterTextPatterns = map[string]*regexp.Regexp{
	"secondFailureLimitText":   secondFailureLimitText,   // monster_effects_2024.go
	"savePushAndRe":            savePushAndRe,            // monster_effects_2024.go
	"savePushTailRe":           savePushTailRe,           // monster_effects_2024.go
	"savePushRe":               savePushRe,               // monster_effects_2024.go
	"judgedAlternativeRe":      judgedAlternativeRe,      // monster_effects_2024.go
	"legendaryOnceSentenceRe":  legendaryOnceSentenceRe,  // monster_effects_2024.go
	"saveSectionHeadRe":        saveSectionHeadRe,        // monster_effects_2024.go
	"lineWidthRe":              lineWidthRe,              // area_geometry_2024.go
	"monsterConeRe":            monsterConeRe,            // area_geometry_2024.go
	"monsterLineRe":            monsterLineRe,            // area_geometry_2024.go
	"monsterSphereRe":          monsterSphereRe,          // area_geometry_2024.go
	"woundedTargetAdvantage":   woundedTargetAdvantage,   // attack_2024.go
	"absorptionTraitRe":        absorptionTraitRe,        // attack_target.go
	"monsterVisionRe":          monsterVisionRe,          // combat_handlers.go
	"lastingAlsoRe":            lastingAlsoRe,            // condition_lasting_effects_2024.go
	"lastingGateRe":            lastingGateRe,            // condition_lasting_effects_2024.go
	"lastingNoRegainRe":        lastingNoRegainRe,        // condition_lasting_effects_2024.go
	"lastingRepeatRe":          lastingRepeatRe,          // condition_lasting_effects_2024.go
	"lastingTickRe":            lastingTickRe,            // condition_lasting_effects_2024.go
	"lastingWakeRe":            lastingWakeRe,            // condition_lasting_effects_2024.go
	"recurringTailRe":          recurringTailRe,          // condition_lasting_effects_2024.go
	"afterBloodiedHitRe":       afterBloodiedHitRe,       // creature_movement_2024.go
	"chargeAltRe":              chargeAltRe,              // creature_movement_2024.go
	"chargeAnyRe":              chargeAnyRe,              // creature_movement_2024.go
	"chargeSentenceRe":         chargeSentenceRe,         // creature_movement_2024.go
	"hitForcedMoveRe":          hitForcedMoveRe,          // creature_movement_2024.go
	"jumpSpendRe":              jumpSpendRe,              // creature_movement_2024.go
	"moveUpToRe":               moveUpToRe,               // creature_movement_2024.go
	"noDragCostRe":             noDragCostRe,             // creature_movement_2024.go
	"noOARe":                   noOARe,                   // creature_movement_2024.go
	"speedKindRe":              speedKindRe,              // creature_movement_2024.go
	"straightEnemyRe":          straightEnemyRe,          // creature_movement_2024.go
	"teleportUpToRe":           teleportUpToRe,           // creature_movement_2024.go
	"teleportWithinRe":         teleportWithinRe,         // creature_movement_2024.go
	"sunlightRe":               sunlightRe,               // creature_senses_2024.go
	"unimpededDarkvisionRe":    unimpededDarkvisionRe,    // creature_senses_2024.go
	"disadvAttackRe":           disadvAttackRe,           // disadvantage_riders_2024.go
	"disadvConcRe":             disadvConcRe,             // disadvantage_riders_2024.go
	"exposedRe":                exposedRe,                // disadvantage_riders_2024.go
	"noReactRe":                noReactRe,                // disadvantage_riders_2024.go
	"hideLightGateRe":          hideLightGateRe,          // hide_2024.go
	"abilityDecreaseRe":        abilityDecreaseRe,        // hit_after_damage_2024.go
	"burnOnHitRe":              burnOnHitRe,              // hit_after_damage_2024.go
	"drainRegainRe":            drainRegainRe,            // hp_max_drain_2024.go
	"hpMaxDrainRe":             hpMaxDrainRe,             // hp_max_drain_2024.go
	"hpMaxDrainSentenceRe":     hpMaxDrainSentenceRe,     // hp_max_drain_2024.go
	"armorOnHitRe":             armorOnHitRe,             // item_state_2024.go
	"corrosiveAmmoRe":          corrosiveAmmoRe,          // item_state_2024.go
	"corrosiveWeaponRe":        corrosiveWeaponRe,        // item_state_2024.go
	"mendingRemovesRe":         mendingRemovesRe,         // item_state_2024.go
	"metalObjectOnlyRe":        metalObjectOnlyRe,        // item_state_2024.go
	"objectOnFailureRe":        objectOnFailureRe,        // item_state_2024.go
	"rangeFeetRe":              rangeFeetRe,              // map_rules.go
	"rangeFtRe":                rangeFtRe,                // map_rules.go
	"rangeSingleRe":            rangeSingleRe,            // map_rules.go
	"reachFtRe":                reachFtRe,                // map_rules.go
	"incorporealRe":            incorporealRe,            // map_spaces_2024.go
	"insideObjectRe":           insideObjectRe,           // map_spaces_2024.go
	"swarmSpaceRe":             swarmSpaceRe,             // map_spaces_2024.go
	"attachOnHitRe":            attachOnHitRe,            // monster_attach_2024.go
	"attachTieRe":              attachTieRe,              // monster_attach_2024.go
	"detachAutoRe":             detachAutoRe,             // monster_attach_2024.go
	"detachCheckRe":            detachCheckRe,            // monster_attach_2024.go
	"whileAttachedOnlyAdvRe":   whileAttachedOnlyAdvRe,   // monster_attach_2024.go
	"attackLockoutRe":          attackLockoutRe,          // monster_attack_lockout_2024.go
	"swallowLockoutRe":         swallowLockoutRe,         // monster_attack_lockout_2024.go
	"anyCombinationRe":         anyCombinationRe,         // monster_attacks.go
	"anyCombinationSegRe":      anyCombinationSegRe,      // monster_attacks.go
	"attackChoiceRe":           attackChoiceRe,           // monster_attacks.go
	"meleeRe":                  meleeRe,                  // monster_attacks.go
	"rangedOnlyRe":             rangedOnlyRe,             // monster_attacks.go
	"replaceAttackRe":          replaceAttackRe,          // monster_attacks.go
	"replaceAttackSentenceRe":  replaceAttackSentenceRe,  // monster_attacks.go
	"castAbilityRe":            castAbilityRe,            // monster_cast_effects_2024.go
	"healUpToRe":               healUpToRe,               // monster_cast_effects_2024.go
	"blurredFormRe":            blurredFormRe,            // monster_condition_traits_2024.go
	"advantageOnlyRe":          advantageOnlyRe,          // monster_damage_choice_2024.go
	"alternativeHeadRe":        alternativeHeadRe,        // monster_damage_choice_2024.go
	"atkBonusRe":               atkBonusRe,               // monster_damage_choice_2024.go
	"chosenTypeExtraRe":        chosenTypeExtraRe,        // monster_damage_choice_2024.go
	"damageBackRefRe":          damageBackRefRe,          // monster_damage_choice_2024.go
	"damageClauseRe":           damageClauseRe,           // monster_damage_choice_2024.go
	"damageConditionRe":        damageConditionRe,        // monster_damage_choice_2024.go
	"damageJoinRe":             damageJoinRe,             // monster_damage_choice_2024.go
	"damageWordRe":             damageWordRe,             // monster_damage_choice_2024.go
	"flatHitRe":                flatHitRe,                // monster_damage_choice_2024.go
	"hitDmgRe":                 hitDmgRe,                 // monster_damage_choice_2024.go
	"hitSentenceEnd":           hitSentenceEnd,           // monster_damage_choice_2024.go
	"subjectedLeadRe":          subjectedLeadRe,          // monster_damage_choice_2024.go
	"validAttackDiceRe":        validAttackDiceRe,        // monster_damage_choice_2024.go
	"damageDisadvantageRe":     damageDisadvantageRe,     // monster_damage_traits_2024.go
	"magicalDarknessRe":        magicalDarknessRe,        // monster_darkness_2024.go
	"magicalDarknessSenseRe":   magicalDarknessSenseRe,   // monster_darkness_2024.go
	"spellListHeadRe":          spellListHeadRe,          // monster_day_uses.go
	"spellParenRe":             spellParenRe,             // monster_day_uses.go
	"deathTriggerRe":           deathTriggerRe,           // monster_death_trigger_2024.go
	"embeddedDamageText":       embeddedDamageText,       // monster_direct_damage_2024.go
	"advantageGateRe":          advantageGateRe,          // monster_effects_2024.go
	"autoSucceedTypesRe":       autoSucceedTypesRe,       // monster_effects_2024.go
	"condDurationRe":           condDurationRe,           // monster_effects_2024.go
	"endsOnDamageRe":           endsOnDamageRe,           // monster_effects_2024.go
	"hitRiderChoiceRe":         hitRiderChoiceRe,         // monster_effects_2024.go
	"hitRiderDeferredRe":       hitRiderDeferredRe,       // monster_effects_2024.go
	"mfxAttackRe":              mfxAttackRe,              // monster_effects_2024.go
	"mfxAutoSucceedRe":         mfxAutoSucceedRe,         // monster_effects_2024.go
	"mfxCondRe":                mfxCondRe,                // monster_effects_2024.go
	"mfxDamageRe":              mfxDamageRe,              // monster_effects_2024.go
	"mfxDurationRe":            mfxDurationRe,            // monster_effects_2024.go
	"mfxEmanRe":                mfxEmanRe,                // monster_effects_2024.go
	"mfxEndSaveRe":             mfxEndSaveRe,             // monster_effects_2024.go
	"mfxEscapeRe":              mfxEscapeRe,              // monster_effects_2024.go
	"mfxExhaustRe":             mfxExhaustRe,             // monster_effects_2024.go
	"mfxOptionRe":              mfxOptionRe,              // monster_effects_2024.go
	"mfxRestrainRe":            mfxRestrainRe,            // monster_effects_2024.go
	"mfxSaveRe":                mfxSaveRe,                // monster_effects_2024.go
	"mfxSizeGateRe":            mfxSizeGateRe,            // monster_effects_2024.go
	"mfxSpellAtkRe":            mfxSpellAtkRe,            // monster_effects_2024.go
	"mfxSpellDCRe":             mfxSpellDCRe,             // monster_effects_2024.go
	"mfxSpellRe":               mfxSpellRe,               // monster_effects_2024.go
	"mfxSuccessImmunityTimeRe": mfxSuccessImmunityTimeRe, // monster_effects_2024.go
	"mfxUntilEndRe":            mfxUntilEndRe,            // monster_effects_2024.go
	"mfxUntilStartRe":          mfxUntilStartRe,          // monster_effects_2024.go
	"mfxUsesRe":                mfxUsesRe,                // monster_effects_2024.go
	"oneRepeatRe":              oneRepeatRe,              // monster_effects_2024.go
	"saveMarginHeading":        saveMarginHeading,        // monster_effects_2024.go
	"secondFailureText":        secondFailureText,        // monster_effects_2024.go
	"secondFailureWakeText":    secondFailureWakeText,    // monster_effects_2024.go
	"sentenceSplitRe":          sentenceSplitRe,          // monster_effects_2024.go
	"severePoisonText":         severePoisonText,         // monster_effects_2024.go
	"severeSleepText":          severeSleepText,          // monster_effects_2024.go
	"targetIsCreatureGateRe":   targetIsCreatureGateRe,   // monster_effects_2024.go
	"endAuraRe":                endAuraRe,                // monster_features_2024.go
	"laUsesRe":                 laUsesRe,                 // monster_features_2024.go
	"legendaryOnceRe":          legendaryOnceRe,          // monster_features_2024.go
	"lrRe":                     lrRe,                     // monster_features_2024.go
	"rechargeRe":               rechargeRe,               // monster_features_2024.go
	"regenBlockRe":             regenBlockRe,             // monster_features_2024.go
	"regenFromZeroRe":          regenFromZeroRe,          // monster_features_2024.go
	"regenRe":                  regenRe,                  // monster_features_2024.go
	"saveEndAuraRe":            saveEndAuraRe,            // monster_features_2024.go
	"startAuraRe":              startAuraRe,              // monster_features_2024.go
	"monsterLightRe":           monsterLightRe,           // monster_lighting_2024.go
	"monsterLightToggleRe":     monsterLightToggleRe,     // monster_lighting_2024.go
	"perDayRe":                 perDayRe,                 // monster_reactions_2024.go
	"reactACRe":                reactACRe,                // monster_reactions_2024.go
	"reactReduceRe":            reactReduceRe,            // monster_reactions_2024.go
	"reactRiposte":             reactRiposte,             // monster_reactions_2024.go
	"reactSelfHitRe":           reactSelfHitRe,           // monster_reactions_2024.go
	"reactUsesRe":              reactUsesRe,              // monster_reactions_2024.go
	"attachGatePrefixRe":       attachGatePrefixRe,       // monster_recurring_damage_2024.go
	"attachLockoutLeadRe":      attachLockoutLeadRe,      // monster_recurring_damage_2024.go
	"grappleGatePrefixRe":      grappleGatePrefixRe,      // monster_recurring_damage_2024.go
	"recurringDamageRe":        recurringDamageRe,        // monster_recurring_damage_2024.go
	"recurringLockoutTailRe":   recurringLockoutTailRe,   // monster_recurring_damage_2024.go
	"replaceLevelRe":           replaceLevelRe,           // monster_replace_2024.go
	"replaceMarkerRe":          replaceMarkerRe,          // monster_replace_2024.go
	"replaceSentenceRe":        replaceSentenceRe,        // monster_replace_2024.go
	"formGateRe":               formGateRe,               // monster_shape_shift_2024.go
	"shapeShiftRe":             shapeShiftRe,             // monster_shape_shift_2024.go
	"monsterStandardActionRe":  monsterStandardActionRe,  // monster_standard_actions_2024.go
	"standardActionNameRe":     standardActionNameRe,     // monster_standard_actions_2024.go
	"swallowAcidRe":            swallowAcidRe,            // monster_swallow_2024.go
	"swallowActionRe":          swallowActionRe,          // monster_swallow_2024.go
	"swallowCondsRe":           swallowCondsRe,           // monster_swallow_2024.go
	"monsterBlindsightRe":      monsterBlindsightRe,      // monster_target_prerequisites_2024.go
	"singleSaveTargetRe":       singleSaveTargetRe,       // monster_target_prerequisites_2024.go
	"rollTraitBloodiedRe":      rollTraitBloodiedRe,      // monster_traits_2024.go
	"rollTraitMeleeRe":         rollTraitMeleeRe,         // monster_traits_2024.go
	"rollTraitRe":              rollTraitRe,              // monster_traits_2024.go
	"rollTraitSavesRe":         rollTraitSavesRe,         // monster_traits_2024.go
	"rollTraitWoundedRe":       rollTraitWoundedRe,       // monster_traits_2024.go
	"legendaryResRe":           legendaryResRe,           // monsters_2024.go
	"speedNumRe":               speedNumRe,               // monsters_2024.go
	"usesRe":                   usesRe,                   // monsters_2024.go
	"obscuringTextRe":          obscuringTextRe,          // obscured_areas_2024.go
	"riderEndRe":               riderEndRe,               // rider_duration_2024.go
	"speedHalvedRe":            speedHalvedRe,            // speed_owner_2024.go
	"speedMinusRe":             speedMinusRe,             // speed_owner_2024.go
	"speedZeroRe":              speedZeroRe,              // speed_owner_2024.go
	"multiattackCountRe":       multiattackCountRe,       // wild_shape_2024.go
	"multiattackNamedRe":       multiattackNamedRe,       // wild_shape_2024.go
}

// notMonsterText: patterns of those files that read the server's own stored rows or request fields, not stat-block text.
var notMonsterText = map[string]string{
	"attachOwnedFromRowRe":    "reads the attach owner's own stored row (\"owns: ... @ ...\")",
	"suffocationRowRe":        "reads the suffocation owner's stored row (\"| gained: N\")",
	"swallowArmedRe":          "reads the swallow owner's stored row (\"| armed\")",
	"recurringFromTextRe":     "reads the recurring-damage owner's stored description of a tick",
	"companionDamageChoiceRE": "reads a player's request description (\"damage type: fire\")",
	"multiNumberRe":           "a bare number token of a Multiattack count, after the words are read",
	"lastingJoinRe":           "the joining words (\", and\") left between two clauses already read",
	"rangeRe":                 "a weapon's range property \"(80/320)\" from the equipment data, not a stat block",
	"damageConditionTexts":    "a table: each row's pattern is checked by TestMonsterPatternsServeSRDWording",
	"monsterTraitRules":       "a table: each wording rule is a package-level pattern listed by its own name",
}

// localMonsterPatterns: regular expressions built inside functions of those files (not reachable by name here), each with what it
// splits or reads; the pattern it serves is checked through the package-level pattern or the owner's own test named.
var localMonsterPatterns = map[string]string{
	"condition_lasting_effects_2024.go:readLastingEffects": "splits a list of condition names on \", \" / \" and \"",
	"hit_after_damage_2024.go:burnSentenceForCreatures":    "\"If the target is ...\" and \"creature\" in a burning sentence (TestPostgresBurningOnHitEveryTarget)",
	"monster_attach_2024.go:attachDetachFromRow":           "reads the attach owner's stored row (\"detach: an action, DC N ...\")",
	"monster_attach_2024.go:attachOwnedConditions":         "splits a list of condition names",
	"monster_effects_2024.go:parseSaveEffect":              "splits the creature types of \"(demons succeed automatically)\"",
	"monster_effects_2024.go:readRider":                    "splits a list of condition names",
	"monster_effects_2024.go:unappliedPart":                "splits a residual sentence on \";\" and \",\"",
	"monster_shape_shift_2024.go:shapeShiftForms":          "drops the parenthetical Speeds of a form list and splits it (TestPostgresShapeShiftForms)",
	"monster_swallow_2024.go:readSwallowRow":               "reads the swallow owner's stored row (\"acid: N (dice) type\")",
	"monsters_2024.go:monsterSlug":                         "a name's slug",
	"rider_duration_2024.go:riderPattern":                  "builds each rider pattern with its duration (each one is a listed package-level pattern)",
}

// patternWitness: where an SRD stat block uses a wording a pattern reads in a part of a sentence the generic splitting below doesn't
// cut out (the owner hands the pattern a clause of its own making), or an invented test monster for a generic rule.
type patternWitness struct {
	Monster, Entry, Fragment string // an SRD stat block's entry and the part of its text the pattern reads (Fragment must be in it)
	Invented, Test           string // or: an invented sentence the pattern reads, used by that test (it must be in a _test.go file)
}

var patternWitnesses = map[string]patternWitness{
	// the damage owner hands these the words after an amount ("... Slashing damage" + tail)
	"advantageOnlyRe":         {Monster: "Goblin Warrior", Entry: "Scimitar", Fragment: "if the attack roll had Advantage."},
	"damageJoinRe":            {Monster: "Goblin Warrior", Entry: "Scimitar", Fragment: "5 (1d6 + 2) Slashing damage, plus"},
	"alternativeHeadRe":       {Monster: "Chimera", Entry: "Bite", Fragment: "11 (2d6 + 4) Piercing damage, or"},
	"chargeAltRe":             {Monster: "Giant Seahorse", Entry: "Ram", Fragment: "if the seahorse moved 20+ feet straight toward the target immediately before the hit"},
	"recurringTailRe":         {Monster: "Remorhaz", Entry: "Swallow", Fragment: " plus 10 (3d6) Fire damage at the start of each of "},
	"damageConditionTexts[0]": {Monster: "Chimera", Entry: "Bite", Fragment: "if the chimera had Advantage on the attack roll"},
	"damageConditionTexts[1]": {Monster: "Blood Hawk", Entry: "Beak", Fragment: "if the target is Bloodied"},
	"damageConditionTexts[2]": {Monster: "Mimic", Entry: "Bite", Fragment: "if the target is Grappled by the mimic"},
	"damageConditionTexts[3]": {Monster: "Swarm of Crawling Claws", Entry: "Swarm of Grasping Hands", Fragment: "if the swarm is Bloodied"},
}

// monsterTextFile: a file whose patterns read stat-block text.
func monsterTextFile(name string) bool {
	for _, p := range []string{"monster_", "creature_", "condition_lasting_effects", "disadvantage_riders", "rider_duration", "hit_after_damage",
		"hp_max_drain", "item_state", "obscured_areas", "hide_2024", "map_spaces", "speed_owner", "area_geometry", "map_rules", "wild_shape",
		"attack_2024", "attack_target", "monsters_2024", "combat_handlers"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// srdTextPieces: every SRD 5.2.1 stat block's texts, cut the ways the owners cut them: whole entries and names, the Senses, Speed and
// other lines, sentences, the parts after each heading ("Hit:", "Failure:", "Trigger:" ...), clauses, and parenthetical asides.
func srdTextPieces() map[string]string {
	pieces := map[string]string{} // piece -> "Monster|Entry"
	add := func(p, where string) {
		p = strings.TrimSpace(p)
		if p != "" {
			if _, ok := pieces[p]; !ok {
				pieces[p] = where
			}
		}
	}
	heading := regexp.MustCompile(`(?:Hit or Miss|Hit|First Failure|Second Failure|Failure by \d+ or More|Failure or Success|Failure|Success|Trigger|Response)(?:—| -|:)\s*`)
	sentence := regexp.MustCompile(`(?:[.!?])\s+|\n`)
	clause := regexp.MustCompile(`, or |, and |; |, | plus | and | or `)
	paren := regexp.MustCompile(`\(([^()]*)\)`)
	for _, m := range srdMonsters2024() {
		for _, line := range []string{m.Speed, m.Senses, m.Languages, m.Gear, m.Resistances, m.DmgImmune, m.CondImmune, m.Vulnerable, m.LegendaryIn} {
			add(line, m.Name+"|"+"(stat line)")
		}
		for _, group := range [][]srdMonsterEntry{m.Traits, m.Actions, m.BonusActions, m.Reactions, m.Legendary} {
			for _, e := range group {
				where := m.Name + "|" + e.Name
				add(e.Name, where)
				add(e.Text, where)
				var parts []string
				for _, sec := range heading.Split(e.Text, -1) {
					parts = append(parts, sec)
					parts = append(parts, sentence.Split(sec, -1)...)
				}
				parts = append(parts, sentence.Split(e.Text, -1)...)
				for _, p := range parts {
					add(p, where)
					add(strings.TrimSuffix(strings.TrimSpace(p), "."), where)
					for _, c := range clause.Split(p, -1) {
						add(strings.TrimSuffix(strings.TrimSpace(c), "."), where)
					}
				}
				for _, pm := range paren.FindAllStringSubmatch(e.Text, -1) {
					add(pm[1], where)
				}
			}
		}
	}
	return pieces
}

func firstSRDMatch(rx *regexp.Regexp, pieces map[string]string, keys []string) string {
	for _, k := range keys {
		if rx.MatchString(k) {
			return pieces[k] + ": " + k
		}
	}
	return ""
}

// TestMonsterPatternsServeSRDWording: every monster text pattern (the list above, the trait table's wording rules, the damage
// conditions) matches an SRD stat block, or the stated witness: an SRD entry's own words, or an invented sentence a test uses.
func TestMonsterPatternsServeSRDWording(t *testing.T) {
	pieces := srdTextPieces()
	keys := make([]string, 0, len(pieces))
	for k := range pieces {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	tests := testSources(t)
	check := func(name string, rx *regexp.Regexp) {
		if w, ok := patternWitnesses[name]; ok {
			switch {
			case w.Monster != "":
				text := srdWitnessText(w.Monster, w.Entry)
				if text == "" || !strings.Contains(text, w.Fragment) || !rx.MatchString(w.Fragment) {
					t.Errorf("%s: its SRD witness %s|%s doesn't hold (fragment in the entry: %v; pattern matches it: %v)", name, w.Monster, w.Entry,
						text != "" && strings.Contains(text, w.Fragment), rx.MatchString(w.Fragment))
				}
			default:
				if _, named := tests[w.Test]; !named || !rx.MatchString(w.Invented) || !strings.Contains(testFilesText(t), w.Invented) {
					t.Errorf("%s: its invented witness isn't read by the pattern or isn't in %s", name, w.Test)
				}
			}
			return
		}
		if firstSRDMatch(rx, pieces, keys) == "" {
			t.Errorf("%s matches no SRD 5.2.1 stat block: archive the rule, or name its SRD (or invented test) witness in patternWitnesses", name)
		}
	}
	for name, rx := range monsterTextPatterns {
		check(name, rx)
	}
	for _, r := range monsterTraitRules {
		if r.Match != nil {
			continue // the trait table's wording rules are package-level patterns of their own files, checked above by name
		}
		found := false
		for _, m := range srdMonsters2024() {
			for _, tr := range m.Traits {
				found = found || traitName(tr.Name) == r.Name
			}
		}
		if !found {
			t.Errorf("monsterTraitRules names %q, which no SRD 5.2.1 trait has", r.Name)
		}
	}
	for i, d := range damageConditionTexts {
		check(fmt.Sprintf("damageConditionTexts[%d]", i), d.re)
	}
}

// srdWitnessText: the text of one SRD 5.2.1 entry ("" when there is none by that name).
func srdWitnessText(monster, entry string) string {
	for _, m := range srdMonsters2024() {
		if m.Name != monster {
			continue
		}
		if entry == "(stat line)" {
			return strings.Join([]string{m.Speed, m.Senses, m.Languages, m.Gear, m.Resistances, m.DmgImmune, m.CondImmune, m.LegendaryIn}, "\n")
		}
		for _, group := range [][]srdMonsterEntry{m.Traits, m.Actions, m.BonusActions, m.Reactions, m.Legendary} {
			for _, e := range group {
				if e.Name == entry {
					return e.Text
				}
			}
		}
	}
	return ""
}

// testFilesText: every _test.go file's text (an invented witness may sit in a fixture variable beside its test).
func testFilesText(t *testing.T) string {
	files, _ := filepath.Glob("*_test.go")
	var b strings.Builder
	for _, f := range files {
		b.WriteString(readFileOrFail(t, f))
	}
	return b.String()
}

// testSources: the text of every test function, by name.
func testSources(t *testing.T) map[string]string {
	t.Helper()
	files, _ := filepath.Glob("*_test.go")
	fset := token.NewFileSet()
	out := map[string]string{}
	for _, f := range files {
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		src := readFileOrFail(t, f)
		for _, d := range af.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				out[fd.Name.Name] = src[fset.Position(fd.Pos()).Offset:fset.Position(fd.End()).Offset]
			}
		}
	}
	return out
}

// TestEveryMonsterPatternIsListed: every regular expression of a file that reads stat-block text is classified (go/ast): a
// package-level one is in monsterTextPatterns or notMonsterText, one built inside a function is in localMonsterPatterns.
func TestEveryMonsterPatternIsListed(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || !monsterTextFile(f) {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range af.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				ast.Inspect(d, func(n ast.Node) bool {
					if c, ok := n.(*ast.CallExpr); ok {
						if s, ok := c.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "MustCompile" {
							key := f + ":" + d.Name.Name
							if _, ok := localMonsterPatterns[key]; !ok {
								t.Errorf("%s builds a pattern inside a function: list it in localMonsterPatterns with what it reads", key)
							}
						}
					}
					return true
				})
			case *ast.GenDecl:
				if d.Tok != token.VAR {
					continue
				}
				for _, sp := range d.Specs {
					vs := sp.(*ast.ValueSpec)
					for i, name := range vs.Names {
						if i >= len(vs.Values) || !isPatternExpr(vs.Values[i]) {
							continue
						}
						seen[name.Name] = true
						_, listed := monsterTextPatterns[name.Name]
						_, exempt := notMonsterText[name.Name]
						if !listed && !exempt {
							t.Errorf("%s (%s) is a pattern of a stat-block reader that is in neither monsterTextPatterns nor notMonsterText", name.Name, f)
						}
					}
				}
			}
		}
	}
	for name := range monsterTextPatterns {
		if !seen[name] {
			t.Errorf("monsterTextPatterns lists %s, which is no package-level pattern of a stat-block reader any more", name)
		}
	}
	for name := range notMonsterText {
		if !seen[name] {
			t.Errorf("notMonsterText lists %s, which is no package-level pattern of a stat-block reader any more", name)
		}
	}
}

// isPatternExpr: the expression compiles a pattern (regexp.MustCompile or riderPattern).
func isPatternExpr(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			switch fn := c.Fun.(type) {
			case *ast.SelectorExpr:
				found = found || fn.Sel.Name == "MustCompile"
			case *ast.Ident:
				found = found || fn.Name == "riderPattern"
			}
		}
		return true
	})
	return found
}

func readFileOrFail(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
