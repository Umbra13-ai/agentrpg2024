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
}

var monsterTraitRules = []monsterTraitRule{
	{Name: "Pack Tactics", Does: "Advantage on its attack rolls when one of its allies (not Incapacitated) is within 5 feet of the target: the battle map, or ally_adjacent in the monster_action",
		Where: "attackSources (attack_2024.go), allyNextToTarget"},
	{Name: "Magic Resistance", Does: "Advantage on saving throws against spells", Where: "saveSources (save_2024.go)"},
	{Name: "Legendary Resistance", Does: "a failed saving throw can succeed instead, a number of times a day (policy: POST /api/gm/monster-features)",
		Where: "monster_features_2024.go, saveCtx.resist"},
	{Name: "Undead Fortitude", Does: "damage that drops it to 0 Hit Points (not Radiant, not a Critical Hit) gets a Constitution save, DC 5 + the damage, to stay at 1 Hit Point",
		Where: "undeadFortitude (dodge_2024.go)"},
	{Name: "Aura of Authority", Does: "it and its allies within 10 feet have Advantage on attack rolls and saving throws", Where: "attackSources, saveSources"},
	{Match: regenRe, Does: "regains Hit Points at the start of each of its turns (not after the damage the trait names)", Where: "monster_features_2024.go"},
	{Match: startAuraRe, Does: "an aura: each enemy that starts its turn in it makes the saving throw", Where: "monster_features_2024.go"},
	{Match: endAuraRe, Does: "an aura: each creature in it takes the damage at the end of the monster's turn", Where: "monster_features_2024.go"},
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

// monsterHasTrait: the monster in this fight (its combat id) has the trait in its stat block. trait is a Name of
// monsterTraitRules (TestMonsterTraitsHaveOneOwner checks).
func monsterHasTrait(lobbyID, id int, trait string) bool {
	_, meta, ok := monsterCombatant(lobbyID, id)
	if !ok || meta == nil {
		return false
	}
	for _, t := range meta.Traits {
		if traitName(t.Name) == trait {
			return true
		}
	}
	return false
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
