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

	"github.com/agentrpg/agentrpg/game"
)

type monsterTraitRule struct {
	Name  string         // the trait's name in the stat block, without its parenthesis ("Legendary Resistance"); "" for a rule by wording
	Match *regexp.Regexp // the wording that marks the trait when its name varies (auras, Regeneration)
	Does  string         // what the server does, as the GM reads it
	Where string         // the rule's owner in the code
	Aura  bool           // it acts on its own timing around the monster: the GM's monster-features view lists it under
	// automatic_traits, and gm/monster-features {"auras": false} switches it off (Regeneration aside)
	GM bool // the trait is the GM's by design (communication, a sense the server keeps no state for): Does says why
}

var monsterTraitRules = []monsterTraitRule{
	{Match: swarmSpaceRe, Does: "it can occupy another creature's space and vice versa: the battle map lets it and other creatures share its space (a move may end there)",
		Where: "markMapSpaces, validate, passableTokens, endsInCreature (map_spaces_2024.go, battle_map.go)"},
	{Match: incorporealRe, Does: "it moves through creatures and objects (its token may stand inside a wall or a closed door); ending its turn inside an object deals the trait's damage",
		Where: "markMapSpaces (map_spaces_2024.go), objectEndDamage (turn-end hook)"},
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
	{Match: regenRe, Does: "regains Hit Points at the start of each of its turns (not after the damage the trait names); one that \"dies only if it starts its turn with 0 Hit Points and doesn't regenerate\" keeps its turn at 0 Hit Points, regenerates from 0, and dies at a turn start at 0 that brings no regeneration",
		Where: "monsterFeaturesTurnStart (monster_features_2024.go), noTurnFor (turn_advance_2024.go)", Aura: true},
	{Match: startAuraRe, Does: "an aura: each enemy that starts its turn in it makes the saving throw", Where: "monster_features_2024.go", Aura: true},
	{Match: rollTraitRe, Does: "Advantage on its attack rolls (melee ones when the trait says so), and on its saving throws when the trait says so, while it is Bloodied or against a target that doesn't have all its Hit Points, as its trait says",
		Where: "rollTraitAdvantage (monster_traits_2024.go), read by attackSources and saveSources"},
	{Match: endAuraRe, Does: "an aura: each creature in it (of its choice: its enemies) takes the damage at the end of the monster's turn", Where: "monster_features_2024.go", Aura: true},
	{Match: blurredFormRe, Does: "attack rolls against it have Disadvantage (unless it has the Incapacitated condition, when the trait says so)",
		Where: "attackSources (attack_2024.go), attackedWithDisadvantage (monster_condition_traits_2024.go)"},
	{Match: saveEndAuraRe, Does: "an aura: each creature in it (of its choice: its enemies) makes the saving throw at the end of the monster's turn; one that is immune to it after a success is left out",
		Where: "endSaveAura (monster_features_2024.go)", Aura: true},
	{Match: sunlightRe, Does: "while it stands in sunlight (the GM's lighting set to sunlight): Disadvantage on its attack rolls and its Hide check, and on its saving throws where the trait says D20 Tests",
		Where: "attackSources, saveSources, attemptMonsterHide; sunlightPenalty (creature_senses_2024.go)"},
	// the GM's by design: no server state to apply them to (asserted, so the GM's stat block says so plainly)
	{Match: regexp.MustCompile(`(?i)\btelepath`), Does: "telepathic communication or a mind link: the GM plays it (the server keeps no messages between creatures)", Where: "the GM", GM: true},
	{Match: regexp.MustCompile(`(?i)\bcan pinpoint the location of\b`), Does: "a sense that finds things (ferrous metal, a scent): the GM tells what it finds", Where: "the GM", GM: true},
	{Match: regexp.MustCompile(`(?i)\bcan communicate with\b|\bcan speak with\b`), Does: "speaking with creatures: the GM plays it", Where: "the GM", GM: true},
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
		if r, ok := traitRuleFor(t); ok && !r.GM {
			e["server_applies"], e["server_does"] = true, r.Does
		} else if ok {
			e["server_applies"], e["server_does"] = false, "the GM applies this trait: "+r.Does
		} else {
			e["server_applies"], e["server_does"] = false, "the server doesn't apply this trait: the GM does"
		}
		out = append(out, e)
	}
	return out
}

// ---- A creature's own trait that gives its rolls Advantage on a condition (Blood Frenzy, Bloodied Fury, Bloodied Frenzy), found
// by its wording: "While Bloodied, ... has Advantage on attack rolls [and saving throws]", "has Advantage on melee attack rolls while
// it is Bloodied", "has Advantage on attack rolls against any creature that doesn't have all its Hit Points". The attack roll owner
// (attackSources) and the saving throw owner (saveSources) each read it through rollTraitAdvantage; Pack Tactics and the auras have
// rows of their own.

// rollTraitRe: the wording of such a trait (the Match of its monsterTraitRules row).
var rollTraitRe = regexp.MustCompile(`(?i)\bhas Advantage on (?:melee )?attack rolls\b[^.]*?(?:\bBloodied\b|\bhave all its Hit Points\b)|\bwhile (?:it is )?Bloodied\b[^.]*?\bhas Advantage on (?:melee )?attack rolls\b`)

var (
	rollTraitBloodiedRe = regexp.MustCompile(`(?i)\bwhile (?:it is )?Bloodied\b`)
	rollTraitWoundedRe  = regexp.MustCompile(`(?i)\bagainst any creature that (?:doesn['’]t|does not) have all its Hit Points\b`)
	rollTraitMeleeRe    = regexp.MustCompile(`(?i)\bAdvantage on melee attack rolls\b`)
	rollTraitSavesRe    = regexp.MustCompile(`(?i)\battack rolls and saving throws\b`)
)

// rollTrait: what one such trait says.
type rollTrait struct {
	Name      string
	Bloodied  bool // only while the creature itself is Bloodied
	Wounded   bool // only against a target that doesn't have all its Hit Points
	MeleeOnly bool // melee attack rolls only
	Saves     bool // its saving throws too
}

// rollTraitOf reads a trait's text; ok is false for any other trait.
func rollTraitOf(name, text string) (rollTrait, bool) {
	if !rollTraitRe.MatchString(text) {
		return rollTrait{}, false
	}
	r := rollTrait{Name: traitName(name), Bloodied: rollTraitBloodiedRe.MatchString(text), Wounded: rollTraitWoundedRe.MatchString(text),
		MeleeOnly: rollTraitMeleeRe.MatchString(text), Saves: rollTraitSavesRe.MatchString(text)}
	return r, r.Bloodied || r.Wounded
}

// creatureTraitsAndVitals: a monster's (combat id < 0) or a companion's traits and its Hit Points now.
func creatureTraitsAndVitals(lobbyID, id int) ([]srdMonsterEntry, game.Vitals) {
	if isCompanionCombatID(id) {
		cp, ok := loadCompanion(id - companionIDBase)
		if !ok {
			return nil, game.Vitals{}
		}
		out := make([]srdMonsterEntry, 0, len(cp.Traits))
		for _, t := range cp.Traits {
			out = append(out, srdMonsterEntry{Name: t.Name, Text: t.Text})
		}
		return out, game.Vitals{HP: cp.HP, Max: cp.MaxHP}
	}
	if id >= 0 {
		return nil, game.Vitals{}
	}
	_, meta, ok := monsterCombatant(lobbyID, id)
	if !ok || meta == nil {
		return nil, game.Vitals{}
	}
	return meta.Traits, monsterEntryVitals(lobbyID, id)
}

// rollTraitAdvantage: the name of the creature's trait that gives this roll Advantage, or "". save: a saving throw (else an attack
// roll, melee or not, against target).
func rollTraitAdvantage(lobbyID, id int, save, melee bool, target int) string {
	traits, v := creatureTraitsAndVitals(lobbyID, id)
	for _, t := range traits {
		r, ok := rollTraitOf(t.Name, t.Text)
		if !ok || (save && !r.Saves) || (!save && r.MeleeOnly && !melee) {
			continue
		}
		if r.Bloodied && !v.Bloodied() {
			continue
		}
		if r.Wounded && (save || target == 0 || !targetMissingHitPoints(lobbyID, target)) {
			continue
		}
		return r.Name
	}
	return ""
}
