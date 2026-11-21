package main

// Monster traits: which ones the server applies, and how a trait is found: one owner.
//
// A monster's traits are the SRD 5.2.1 stat block's own text (the embedded monsters, monsterMeta). A trait the server applies is
// one row of monsterTraitRules: found by its name (Pack Tactics, Magic Resistance) or, for a trait whose name varies between
// monsters, by its wording (an aura, Regeneration). The row says what the server does and where that rule lives (an attack
// source, a saving throw source, a turn hook). Every other trait is the GM's to apply, and the GM's view says which is which
// (traitsForGM). monsterHasTrait reads the loaded stat block; a handler never searches the trait text itself.

import (
	"regexp"
	"strings"
)

type monsterTraitRule struct {
	Name  string         // the trait's name in the stat block, without its parenthesis ("Legendary Resistance"); "" for a rule by wording
	Match *regexp.Regexp // the wording that marks the trait when its name varies (auras, Regeneration)
	Does  string         // what the server does, as the GM reads it
	Where string         // the rule's owner in the code
	Aura  bool           // it acts on its own timing around the monster: the GM's monster-features view lists it under
	// automatic_traits, and gm/monster-features {"auras": false} switches it off (Regeneration aside)
}

var monsterTraitRules = []monsterTraitRule{
	{Name: "Agile", Does: "Its movement does not provoke Opportunity Attacks when the GM updates the battle map.",
		Where: "noteMonstersLeavingReach (character_oa_2024.go)"},
	{Name: "Flyby", Does: "Its flight does not provoke Opportunity Attacks when the GM includes its combat ID in flying on the battle map update.",
		Where: "noteMonstersLeavingReach (character_oa_2024.go)"},
	{Name: "Pack Tactics", Does: "Advantage on its attack rolls when one of its allies (not Incapacitated) is within 5 feet of the target: the battle map, or ally_adjacent in the monster_action",
		Where: "attackSources (attack_2024.go), allyNextToTarget"},
	{Name: "Immutable Form", Does: "Cannot be shape-shifted by Polymorph", Where: "polymorphCast (spells_more_2024.go)"},
	{Match: damageDisadvantageRe, Does: "Taking the specified damage imposes Disadvantage on attack rolls until the end of its next turn. The GM is notified if ability checks also have Disadvantage.", Where: "monster_damage_traits_2024.go and the shared Hit Point and attack owners"},
	{Match: monsterLightRe, Does: "Adds the listed Bright and Dim Light to mapped attack targets. For an unplaced creature or other checks, the GM resolves its lighting.", Where: "monsterLightingAt (monster_lighting_2024.go)"},
	{Name: "Magic Resistance", Does: "Advantage on saving throws against spells and stat-block effects explicitly labeled magical", Where: "saveSources (save_2024.go)"},
	{Name: "Legendary Resistance", Does: "a failed saving throw can succeed instead, a number of times a day (policy: POST /api/gm/monster-features)",
		Where: "monster_features_2024.go, saveCtx.resist"},
	{Name: "Undead Fortitude", Does: "damage that drops it to 0 Hit Points (not Radiant, not a Critical Hit) gets a Constitution save, DC 5 + the damage, to stay at 1 Hit Point",
		Where: "undeadFortitude (dodge_2024.go)"},
	{Match: deathTriggerRe, Does: "when it dies, its saving throw effect is rolled for every creature in its Emanation on the battle map (monsters too), after the request that killed it or at the next turn start; without a map the GM is told",
		Where: "recordDeathTriggerIn, drainDeathTriggers (monster_death_trigger_2024.go)"},
	{Name: "Evasion", Does: "a Dexterity saving throw against an effect that halves damage on a success: a success takes none, a failure half",
		Where: "applySaveEffectOnMonster (monster_save_targets.go) for a monster that is the target of a monster's save effect"},
	{Name: "Aura of Authority", Does: "it and its allies within 10 feet have Advantage on attack rolls and saving throws", Where: "attackSources, saveSources", Aura: true},
	{Match: regenRe, Does: "regains Hit Points at the start of each of its turns (not after the damage the trait names)", Where: "monster_features_2024.go", Aura: true},
	{Match: startAuraRe, Does: "an aura: each enemy that starts its turn in it makes the saving throw", Where: "monster_features_2024.go", Aura: true},
	{Match: endAuraRe, Does: "an aura: each creature in it takes the damage at the end of the monster's turn", Where: "monster_features_2024.go", Aura: true},
}

// traitName: a trait's name without its parenthesis ("Legendary Resistance (3/Day, or 4/Day in Lair)" -> "Legendary Resistance").
func traitName(n string) string {
	if i := strings.Index(n, " ("); i > 0 {
		n = n[:i]
	}
	return strings.TrimSpace(n)
}

// traitRuleFor: the rule that applies a stat block trait, if the server applies it.
func traitRuleFor(t srdMonsterEntry) (monsterTraitRule, bool) {
	for _, r := range monsterTraitRules {
		if (r.Name != "" && r.Name == traitName(t.Name)) || (r.Match != nil && r.Match.MatchString(t.Text)) {
			return r, true
		}
	}
	return monsterTraitRule{}, false
}

// metaHasTrait: the stat block has the trait (a Name of monsterTraitRules).
func metaHasTrait(meta *monsterMeta, trait string) bool {
	if meta == nil {
		return false
	}
	for _, t := range meta.Traits {
		if traitName(t.Name) == trait {
			return true
		}
	}
	return false
}

// auraTraits: the names of a stat block's traits whose rule acts on its own timing around the monster (Aura rows).
func auraTraits(meta *monsterMeta) []string {
	var out []string
	for _, t := range meta.Traits {
		if r, ok := traitRuleFor(t); ok && r.Aura {
			out = append(out, t.Name)
		}
	}
	return out
}

// monsterHasTrait: the monster in this fight (its combat id) has the trait in its stat block. trait is a Name of
// monsterTraitRules (TestMonsterTraitsHaveOneOwner checks).
func monsterHasTrait(lobbyID, id int, trait string) bool {
	_, meta, ok := monsterCombatant(lobbyID, id)
	return ok && metaHasTrait(meta, trait)
}

// traitsForGM: a monster's traits as the GM's stat block shows them: each with what the server does about it, or that the GM
// applies it.
func traitsForGM(key string) []map[string]interface{} {
	meta := monsterMetaFor(key)
	if meta == nil {
		return nil
	}
	out := []map[string]interface{}{}
	for _, t := range meta.Traits {
		e := map[string]interface{}{"name": t.Name, "text": t.Text}
		if r, ok := traitRuleFor(t); ok {
			e["server_applies"], e["server_does"] = true, r.Does
		} else {
			e["server_applies"], e["server_does"] = false, "the server doesn't apply this trait: the GM does"
		}
		out = append(out, e)
	}
	return out
}
