package main

// Mechanics a GM attaches to a stat-block entry (owner, Nov 23). The server's text patterns read SRD 5.2.1 wording only
// (monster_pattern_guard_2024_test.go); a monster the GM imports may word an entry another way, and a GM may house-rule an SRD
// monster. Instead of a new pattern for each wording, the GM attaches a structured mechanic to the entry: a kind from
// mechanicCatalog and its parameters. The mechanics are generic (no monster, book or wording is named in them); the owner that applies
// each kind is the same owner that applies the SRD wording of it.
//
// mechanicCatalog is the one owner of the kinds: what each does, which entries it attaches to, its parameters and their limits. The
// import, edit and preview check an entry's mechanics with mechanicProblem (through validateCustomMonster); the GM's report and stat
// block list them (mechanicsView, entryReport); entryVerdicts marks the sentences a mechanic covers as applied; GET
// /api/gm/compendium/mechanics serves the table. A mechanic's covers is the entry's own words it replaces: rulesText leaves them out of
// what the server's text rules read, so they are applied once, by the mechanic, and never shown to the GM as unapplied.
//
// A new kind is one row here plus its rule in the owner it belongs to (which reads it with attachedMechanic).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// entryMechanic is one mechanic attached to an entry: {"kind": "...", "covers": "...", <the kind's parameters>}.
type entryMechanic map[string]interface{}

// mechanicParam is one parameter of a kind.
type mechanicParam struct {
	Name string // the field name
	Type string // "choice" (one of Options), "condition" (a rules condition), "number" (Min to Max), "text" (up to MaxLen bytes),
	// "flag" (true or false), "dice" (NdM), "damage_types" (a list of Min or more damage types), "conditions" (a list of Min or more rules conditions), "entry_options" (a list
	// naming each effect the entry offers once)
	Options  []string // a choice's values
	Min, Max int      // a number's range
	MaxLen   int      // a text's length
	Required bool     // always required
	Only     string   // allowed only when this holds: "param=value" or "param" (given)
	Needs    string   // required when this param is given
	Does     string   // what it means, for the GM
}

// mechanicKind is one row of the catalogue.
type mechanicKind struct {
	Kind      string
	Does      string
	On        string // the entries it attaches to: a key of mechanicFits
	AppliedBy string // the owner that applies it
	Params    []mechanicParam
	Example   entryMechanic
	ExampleOn string                                          // an invented entry's text the example attaches to
	describe  func(m entryMechanic) string                    // what this attached mechanic does, in the GM's words
	check     func(e srdMonsterEntry, m entryMechanic) string // a check of the entry this mechanic needs besides its parameters
}

// mechanicCoversLen bounds covers; maxEntryMechanics the mechanics of one entry.
const (
	mechanicCoversLen = 2000
	maxEntryMechanics = 10
)

// rulesConditions: the conditions a mechanic may give (the rules conditions but Exhaustion, which has levels).
func rulesConditions() []string {
	var out []string
	for _, c := range game.AllConditions {
		if c != game.ConditionExhaustion {
			out = append(out, c)
		}
	}
	return out
}

// mechanicFits: the entries a kind attaches to, each with what it says and the check of one entry (a refusal, or "").
var mechanicFits = map[string]struct {
	Says  string
	Check func(group string, e srdMonsterEntry) string
}{
	"attack": {"an action, Bonus Action, Reaction or Legendary Action with an attack roll", func(group string, e srdMonsterEntry) string {
		if group == "trait" || !isAttackEntry(e.Text) {
			return "attaches to an entry with an attack roll (\"Melee Attack Roll:\" or \"Ranged Attack Roll:\")"
		}
		return ""
	}},
	"trait": {"a trait", func(group string, e srdMonsterEntry) string {
		if group != "trait" {
			return "attaches to a trait"
		}
		return ""
	}},
	"effect": {"an action, Bonus Action or Legendary Action (not a trait or a Reaction)", func(group string, e srdMonsterEntry) string {
		if group == "trait" || group == "reaction" {
			return "attaches to an action, Bonus Action or Legendary Action"
		}
		return ""
	}},
	"save": {"an action, Bonus Action or Legendary Action with a saving throw (\"Strength Saving Throw: DC 15, ...\")", func(group string, e srdMonsterEntry) string {
		if group == "trait" || group == "reaction" {
			return "attaches to an action, Bonus Action or Legendary Action with a saving throw"
		}
		if _, ok := parseSaveEffect(rulesText(e)); !ok {
			return "attaches to an entry with a saving throw (\"Strength Saving Throw: DC 15, one creature ...\"): preview it to see whether the server reads one"
		}
		return ""
	}},
	"options": {"an action, Bonus Action or Legendary Action that offers several named effects (\"Name. Text\" after a colon, as the SRD's rays are written)", func(group string, e srdMonsterEntry) string {
		if group == "trait" || group == "reaction" || len(splitSaveOptions(rulesText(e))) < 2 {
			return "attaches to an entry that offers two or more named effects, each written \"Name. Text\" after the entry's colon (preview it: the report names the effects it reads)"
		}
		return ""
	}},
}

var mechanicCatalog = []mechanicKind{
	{
		Kind: "drop_to_zero",
		Does: "When a hit of this attack reduces its target to 0 Hit Points, the target dies (result dies) or becomes Stable (result stable), " +
			"optionally with a condition for a time and a second condition while the first lasts. A monster or companion at 0 Hit Points is " +
			"already out of the fight; Stable keeps it alive at 0 for the GM.",
		On:        "attack",
		AppliedBy: "droppedToZeroOnHit (hit_after_damage_2024.go), on every hit route: a character, a monster and a companion as the target",
		Params: []mechanicParam{
			{Name: "result", Type: "choice", Options: []string{"dies", "stable"}, Required: true, Does: "what becomes of a target this hit drops to 0 Hit Points"},
			{Name: "condition", Type: "condition", Only: "result=stable", Needs: "minutes", Does: "a condition the Stable target has"},
			{Name: "minutes", Type: "number", Min: 1, Max: 14400, Only: "condition", Does: "how long the condition lasts, in minutes (60 is 1 hour)"},
			{Name: "also", Type: "condition", Only: "condition", Does: "a second condition the target has while it has the first"},
			{Name: "note", Type: "text", MaxLen: 300, Does: "what follows that the server does not apply (what becomes of the body, a creature that rises), shown with the result"},
		},
		Example:   entryMechanic{"kind": "drop_to_zero", "result": "dies", "note": "the body crumbles to ash", "covers": "If this damage reduces the target to 0 Hit Points, it crumbles to ash."},
		ExampleOn: "Melee Attack Roll: +5, reach 5 ft. Hit: 7 (1d8 + 3) Slashing damage. If this damage reduces the target to 0 Hit Points, it crumbles to ash.",
		check: func(e srdMonsterEntry, m entryMechanic) string {
			if m.str("also") != "" && strings.EqualFold(m.str("also"), m.str("condition")) {
				return "also must be another condition than condition"
			}
			return ""
		},
		describe: func(m entryMechanic) string {
			if m.str("result") == "dies" {
				return "a target this hit drops to 0 Hit Points dies" + noteWords(m)
			}
			s := "a target this hit drops to 0 Hit Points becomes Stable"
			if c := m.str("condition"); c != "" {
				s += fmt.Sprintf(" and has the %s condition for %s", titleCase(c), minutesWords(m.num("minutes")))
				if a := m.str("also"); a != "" {
					s += fmt.Sprintf(", and the %s condition while %s", titleCase(a), titleCase(c))
				}
			}
			return s + noteWords(m)
		},
	},
	{
		Kind: "chosen_damage_type",
		Does: "The damage amounts of this attack's Hit that name no damage type (\"22 (4d10) damage\") are of a type the GM picks from types each time: " +
			"the GM names it in monster_action's use, and nothing is rolled until it names one of them.",
		On:        "attack",
		AppliedBy: "prepareMonsterAttack (monster_damage_choice_2024.go: withListedDamageType), on every attack route and in a Multiattack",
		Params: []mechanicParam{
			{Name: "types", Type: "damage_types", Min: 2, Required: true, Does: "the damage types the GM picks from"},
		},
		Example:   entryMechanic{"kind": "chosen_damage_type", "types": []interface{}{"acid", "cold", "fire"}, "covers": "of a type it chooses: Acid, Cold, or Fire"},
		ExampleOn: "Ranged Attack Roll: +5, range 60 ft. Hit: 9 (2d8) damage of a type it chooses: Acid, Cold, or Fire.",
		check: func(e srdMonsterEntry, m entryMechanic) string {
			if !untypedAmountRe.MatchString(hitDamageText(rulesText(e))) {
				return "the Hit has no damage amount without a type (\"22 (4d10) damage\"): cover the words that say the type is chosen, so the amount is left untyped"
			}
			return ""
		},
		describe: func(m entryMechanic) string {
			return "the untyped damage of its Hit is of the type the GM names in use: " + strings.Join(m.list("types"), ", ")
		},
	},
	{
		Kind: "random_choice",
		Does: "When the GM names none of the effects this entry offers in use, the server rolls a die with one face for each effect and resolves the one it rolls " +
			"(order: the effects in the die's order, else the entry's). The GM may still name one in use (a reroll the text asks for, a choice).",
		On:        "options",
		AppliedBy: "resolveMonsterEffectText (monster_effects_2024.go), where the GM's use picks one of the effects (splitSaveOptions)",
		Params: []mechanicParam{
			{Name: "order", Type: "entry_options", Does: "the entry's effects in the order of the die's faces (1 first); every effect once"},
			{Name: "note", Type: "text", MaxLen: 300, Does: "what the server does not apply about the roll (a reroll rule), shown with the result"},
		},
		Example: entryMechanic{"kind": "random_choice", "order": []interface{}{"Frost Glance", "Ember Glance"}, "note": "reroll an effect already used this turn",
			"covers": "(roll a die; reroll an effect already used this turn)"},
		ExampleOn: "The watcher glances at one creature it can see within 60 feet (roll a die; reroll an effect already used this turn): Frost Glance. " +
			"Constitution Saving Throw: DC 13. Failure: 9 (2d8) Cold damage. Ember Glance. Dexterity Saving Throw: DC 13. Failure: 9 (2d8) Fire damage.",
		describe: func(m entryMechanic) string {
			s := "with no effect named in use, the server rolls for it"
			if o := m.list("order"); len(o) > 0 {
				s += " (in this order: " + strings.Join(o, ", ") + ")"
			}
			return s + noteWords(m)
		},
	},
	{
		Kind: "random_condition",
		Does: "A hit of this attack gives its target one condition the server rolls from conditions (a die with one face for each, in the list's order), " +
			"for the time until says: until the end of the target's next turn, until the start or the end of the attacker's next turn, or for minutes. " +
			"It is given as the SRD's own condition riders are, so the target's Immunity and the condition's end are honored.",
		On:        "attack",
		AppliedBy: "randomConditionOnHit through hitAfterDamage (hit_after_damage_2024.go) and the hit condition owner (parseHitRider, giveConditions), on every hit route",
		Params: []mechanicParam{
			{Name: "conditions", Type: "conditions", Min: 2, Required: true, Does: "the conditions in the die's order (1 first)"},
			{Name: "until", Type: "choice", Options: []string{"end_of_its_next_turn", "start_of_attackers_next_turn", "end_of_attackers_next_turn", "minutes"}, Required: true,
				Does: "how long the rolled condition lasts"},
			{Name: "minutes", Type: "number", Min: 1, Max: 14400, Only: "until=minutes", Does: "how long, in minutes, with until minutes"},
		},
		Example: entryMechanic{"kind": "random_condition", "conditions": []interface{}{"charmed", "frightened", "poisoned"}, "until": "start_of_attackers_next_turn",
			"covers": "Until the start of the dancer's next turn, the target has a condition the dancer rolls for: Charmed, Frightened or Poisoned."},
		ExampleOn: "Melee Attack Roll: +5, reach 5 ft. Hit: 7 (1d8 + 3) Psychic damage. Until the start of the dancer's next turn, the target has a condition the dancer rolls for: Charmed, Frightened or Poisoned.",
		check: func(e srdMonsterEntry, m entryMechanic) string {
			if m.str("until") == "minutes" && m.num("minutes") == 0 {
				return "until minutes needs minutes"
			}
			return ""
		},
		describe: func(m entryMechanic) string {
			return "a hit gives its target a condition rolled from " + strings.Join(m.list("conditions"), ", ") + ", " + conditionTimeWords(m)
		},
	},
	{
		Kind: "forced_move",
		Does: "A hit of this attack moves its target up to feet: pushed straight away from the attacker (push), pulled straight toward it (pull), or " +
			"moved some other way the GM places (gm_places), only for a target of max_size or smaller when it is set. On the battle map the server moves " +
			"the token as the SRD's own pushes are moved; without a map the GM places it.",
		On:        "attack",
		AppliedBy: "hitForcedMove (creature_movement_2024.go: forceToken) through hitAfterDamage, on every hit route",
		Params: []mechanicParam{
			{Name: "direction", Type: "choice", Options: []string{"push", "pull", "gm_places"}, Required: true, Does: "which way the target moves"},
			{Name: "feet", Type: "number", Min: 5, Max: 300, Required: true, Does: "how far, in feet (a multiple of 5)"},
			{Name: "max_size", Type: "choice", Options: []string{"tiny", "small", "medium", "large", "huge", "gargantuan"}, Does: "the largest size of target it moves"},
		},
		Example: entryMechanic{"kind": "forced_move", "direction": "push", "feet": float64(10), "max_size": "large",
			"covers": "A Large or smaller target is flung 10 feet back."},
		ExampleOn: "Melee Attack Roll: +5, reach 5 ft. Hit: 7 (1d8 + 3) Bludgeoning damage. A Large or smaller target is flung 10 feet back.",
		check: func(e srdMonsterEntry, m entryMechanic) string {
			if m.num("feet")%5 != 0 {
				return "feet must be a multiple of 5"
			}
			return ""
		},
		describe: func(m entryMechanic) string {
			s := map[string]string{"push": "a hit pushes its target up to %d feet straight away from it", "pull": "a hit pulls its target up to %d feet straight toward it",
				"gm_places": "a hit moves its target up to %d feet, placed by the GM"}[m.str("direction")]
			s = fmt.Sprintf(s, m.num("feet"))
			if z := m.str("max_size"); z != "" {
				s += " (a " + titleCase(z) + " or smaller target)"
			}
			return s
		},
	},
	{
		Kind: "light_penalty",
		Does: "While the creature is in light (sunlight; or bright_light: Bright Light at it, sunlight included), its own trait gives it Disadvantage on " +
			"what on says: attack rolls, attack rolls and ability checks, or every D20 Test (saving throws too). It is the SRD's Sunlight Sensitivity rule; " +
			"the GM's lighting (POST /api/gm/set-lighting) and the battle map's lights decide the light.",
		On:        "trait",
		AppliedBy: "lightPenaltyNow (creature_senses_2024.go), asked by attackSources, checkCtx.sources (the monster Hide too) and saveSources",
		Params: []mechanicParam{
			{Name: "light", Type: "choice", Options: []string{"sunlight", "bright_light"}, Required: true, Does: "the light it suffers in"},
			{Name: "on", Type: "choice", Options: []string{"attack_rolls", "attack_rolls_and_checks", "d20_tests"}, Required: true, Does: "which rolls have Disadvantage"},
		},
		Example: entryMechanic{"kind": "light_penalty", "light": "bright_light", "on": "attack_rolls",
			"covers": "In bright light the gloomling's eyes water, and its attack rolls have Disadvantage."},
		ExampleOn: "In bright light the gloomling's eyes water, and its attack rolls have Disadvantage.",
		describe: func(m entryMechanic) string {
			light := map[string]string{"sunlight": "sunlight", "bright_light": "Bright Light"}[m.str("light")]
			on := map[string]string{"attack_rolls": "attack rolls", "attack_rolls_and_checks": "attack rolls and ability checks", "d20_tests": "D20 Tests"}[m.str("on")]
			return "Disadvantage on " + on + " while in " + light
		},
	},
	{
		Kind: "light_turn_damage",
		Does: "The creature takes damage (a fixed amount of a type) when it starts its turn in light (sunlight; or bright_light, sunlight included), through " +
			"its Hit Point owner (its Resistances and Immunities apply).",
		On:        "trait",
		AppliedBy: "lightTurnDamage at the monster's turn start (monsterFeaturesTurnStart, monster_features_2024.go), through damageMonster",
		Params: []mechanicParam{
			{Name: "light", Type: "choice", Options: []string{"sunlight", "bright_light"}, Required: true, Does: "the light that harms it"},
			{Name: "amount", Type: "number", Min: 1, Max: 1000, Required: true, Does: "the damage it takes"},
			{Name: "damage_type", Type: "choice", Options: sortedDamageTypes(), Required: true, Does: "the damage's type"},
		},
		Example: entryMechanic{"kind": "light_turn_damage", "light": "sunlight", "amount": float64(10), "damage_type": "radiant",
			"covers": "The gloomling takes 10 Radiant damage when it starts its turn in sunlight."},
		ExampleOn: "The gloomling takes 10 Radiant damage when it starts its turn in sunlight.",
		describe: func(m entryMechanic) string {
			light := map[string]string{"sunlight": "sunlight", "bright_light": "Bright Light"}[m.str("light")]
			return fmt.Sprintf("takes %d %s damage when it starts its turn in %s", m.num("amount"), titleCase(m.str("damage_type")), light)
		},
	},
	{
		Kind: "obscuring_zone",
		Does: "Using the entry fills an area with fog, smoke or a cloud that is Heavily (or Lightly) Obscured: a Sphere of radius size or a Cube of side size, " +
			"centered on the monster (at self) or on the square the GM aims at (at point: monster_action's point). It lasts minutes, or until the GM ends it " +
			"(POST /api/gm/obscured-area). Without a battle map, the creatures the action names are inside it. Anything else the entry does still runs.",
		On:        "effect",
		AppliedBy: "obscuringZone in resolveMonsterEffectText, through the obscured-area owner (startObscuredArea, obscured_areas_2024.go)",
		Params: []mechanicParam{
			{Name: "area", Type: "choice", Options: []string{"heavily_obscured", "lightly_obscured"}, Required: true, Does: "how much it blocks sight"},
			{Name: "shape", Type: "choice", Options: []string{"sphere", "cube"}, Required: true, Does: "its shape"},
			{Name: "size", Type: "number", Min: 5, Max: 300, Required: true, Does: "a Sphere's radius or a Cube's side, in feet (a multiple of 5)"},
			{Name: "at", Type: "choice", Options: []string{"self", "point"}, Required: true, Does: "centered on the monster, or on the square the GM aims at"},
			{Name: "minutes", Type: "number", Min: 1, Max: 1440, Does: "how long it lasts (without it: until the GM ends it)"},
		},
		Example: entryMechanic{"kind": "obscuring_zone", "area": "heavily_obscured", "shape": "sphere", "size": float64(15), "at": "point", "minutes": float64(1),
			"covers": "Choking smoke fills a 15-foot-radius Sphere it can see for 1 minute; the area is Heavily Obscured."},
		ExampleOn: "The smoker hurls a pot. Choking smoke fills a 15-foot-radius Sphere it can see for 1 minute; the area is Heavily Obscured.",
		check: func(e srdMonsterEntry, m entryMechanic) string {
			if m.num("size")%5 != 0 {
				return "size must be a multiple of 5"
			}
			return ""
		},
		describe: func(m entryMechanic) string {
			s := fmt.Sprintf("using it fills a %d-foot %s %s that is %s", m.num("size"), map[string]string{"sphere": "radius", "cube": "side"}[m.str("shape")],
				titleCase(m.str("shape")), titleCase(strings.ReplaceAll(m.str("area"), "_", " ")))
			if m.str("at") == "point" {
				s += ", centered where the GM aims (point)"
			} else {
				s += ", centered on itself"
			}
			if n := m.num("minutes"); n > 0 {
				return s + ", for " + minutesWords(n)
			}
			return s + ", until the GM ends it"
		},
	},
	{
		Kind: "no_healing_aura",
		Does: "Creatures within feet of the monster (on the battle map) can't regain Hit Points while it is alive and in the fight. Without a battle map " +
			"the server can't tell who is within reach, and the GM applies it.",
		On:        "trait",
		AppliedBy: "noHealingAuraOver in cantRegainHP (condition_lasting_effects_2024.go), asked by every heal of a character, monster or companion",
		Params: []mechanicParam{
			{Name: "feet", Type: "number", Min: 5, Max: 300, Required: true, Does: "how far it reaches, in feet (a multiple of 5)"},
		},
		Example:   entryMechanic{"kind": "no_healing_aura", "feet": float64(30), "covers": "No wound closes within 30 feet of the blightwood."},
		ExampleOn: "No wound closes within 30 feet of the blightwood.",
		check: func(e srdMonsterEntry, m entryMechanic) string {
			if m.num("feet")%5 != 0 {
				return "feet must be a multiple of 5"
			}
			return ""
		},
		describe: func(m entryMechanic) string {
			return fmt.Sprintf("creatures within %d feet of it can't regain Hit Points", m.num("feet"))
		},
	},
	{
		Kind: "curse",
		Does: "A hit of this attack curses its target, as the SRD's curses are kept: a cursed record that a Remove Curse or Greater Restoration ends, " +
			"and that lasts hours (or, without them, until a spell ends it). While cursed the target can't regain Hit Points (no_regain), gains no " +
			"benefit from Short or Long Rests (no_rest_benefit), loses max_hp_loss (dice) from its Hit Point maximum every 24 hours of the story " +
			"(it doesn't return with a Long Rest; a creature is cursed once), and has conditions until the curse ends.",
		On:        "attack",
		AppliedBy: "curseOnHit through hitAfterDamage and the curse owner (readCurse, giveCurse: curse_records_2024.go), on every hit route",
		Params: []mechanicParam{
			{Name: "hours", Type: "number", Min: 1, Max: 8760, Does: "how long the curse lasts (without it: until a spell ends it)"},
			{Name: "no_regain", Type: "flag", Does: "true: the cursed target can't regain Hit Points"},
			{Name: "no_rest_benefit", Type: "choice", Options: []string{"short", "long", "short_and_long"}, Does: "the rests that give the cursed target no benefit"},
			{Name: "max_hp_loss", Type: "dice", Does: "dice its Hit Point maximum loses every 24 hours (\"3d6\")"},
			{Name: "conditions", Type: "conditions", Min: 1, Does: "conditions the target has until the curse ends"},
			{Name: "note", Type: "text", MaxLen: 300, Does: "what the curse does that the server does not apply, shown with the result"},
		},
		Example: entryMechanic{"kind": "curse", "no_regain": true, "max_hp_loss": "2d6", "note": "the wound smells of grave soil",
			"covers": "The target is cursed: its wounds won't close, and it withers by 2d6 Hit Points a day."},
		ExampleOn: "Melee Attack Roll: +5, reach 5 ft. Hit: 7 (1d8 + 3) Necrotic damage. The target is cursed: its wounds won't close, and it withers by 2d6 Hit Points a day.",
		describe: func(m entryMechanic) string {
			var parts []string
			if h := m.num("hours"); h > 0 {
				parts = append(parts, fmt.Sprintf("for %d hour%s", h, map[bool]string{true: "s", false: ""}[h != 1]))
			} else {
				parts = append(parts, "until a spell ends it")
			}
			if b, _ := m["no_regain"].(bool); b {
				parts = append(parts, "can't regain Hit Points")
			}
			if r := m.str("no_rest_benefit"); r != "" {
				parts = append(parts, "no benefit from "+strings.ReplaceAll(strings.ReplaceAll(r, "short_and_long", "Short or Long"), "_", " ")+" Rests")
			}
			if d := m.str("max_hp_loss"); d != "" {
				parts = append(parts, "its Hit Point maximum loses "+d+" every 24 hours")
			}
			if c := m.list("conditions"); len(c) > 0 {
				parts = append(parts, "has the "+strings.Join(c, " and ")+" condition while cursed")
			}
			return "a hit curses its target (" + strings.Join(parts, "; ") + ")" + noteWords(m)
		},
	},
	{
		Kind: "engulf",
		Does: "A creature that fails this entry's saving throw is engulfed, held or buried: it has the Restrained condition, the also conditions while " +
			"Restrained, and damage (dice of damage_type) at the start of each of its turns while Restrained. It, or a creature within 5 feet of it, " +
			"can take an action to make the escape_skill check against escape_dc (POST /api/conditions/escape), ending all of it on a success.",
		On:        "save",
		AppliedBy: "giveEngulf in the save owner's finishTarget (monster_effects_2024.go), on every target route, through the hit and condition owners",
		Params: []mechanicParam{
			{Name: "escape_dc", Type: "number", Min: 1, Max: 40, Required: true, Does: "the DC of the check that ends it"},
			{Name: "escape_skill", Type: "choice", Options: []string{"athletics", "acrobatics"}, Does: "the skill of that check (Athletics by default)"},
			{Name: "also", Type: "conditions", Min: 1, Does: "conditions the creature has while Restrained (Blinded inside a creature)"},
			{Name: "damage", Type: "dice", Needs: "damage_type", Does: "the damage it takes at the start of each of its turns while Restrained"},
			{Name: "damage_type", Type: "choice", Options: sortedDamageTypes(), Only: "damage", Does: "that damage's type"},
			{Name: "note", Type: "text", MaxLen: 300, Does: "what the server doesn't apply (Total Cover, suffocating, moving with it), shown with the result"},
		},
		Example: entryMechanic{"kind": "engulf", "escape_dc": float64(13), "also": []interface{}{"blinded"}, "damage": "2d6", "damage_type": "acid",
			"note": "Total Cover from outside; it moves with the blob", "covers": "The target is drawn inside the blob, blinded and dissolving."},
		ExampleOn: "Dexterity Saving Throw: DC 13, one creature within 5 feet. Failure: The target is drawn inside the blob, blinded and dissolving.",
		describe: func(m entryMechanic) string {
			s := "a failed save leaves the creature Restrained"
			if a := m.list("also"); len(a) > 0 {
				s += " (and " + strings.Join(a, " and ") + " while Restrained)"
			}
			if d := m.str("damage"); d != "" {
				s += fmt.Sprintf(", taking %s %s damage at the start of each of its turns", d, titleCase(m.str("damage_type")))
			}
			skill := m.str("escape_skill")
			if skill == "" {
				skill = "athletics"
			}
			return s + fmt.Sprintf("; escape: an action and a DC %d %s check", m.num("escape_dc"), titleCase(skill)) + noteWords(m)
		},
	},
}

// engulfSentences: the SRD's own words for an engulf, which the hit and condition owners read (parseHitRider: the condition, what lasts
// while it does).
func engulfSentences(m entryMechanic) string {
	s := "Hit: The target has the Restrained condition."
	var while []string
	if a := m.list("also"); len(a) > 0 {
		names := make([]string, len(a))
		for i, x := range a {
			names[i] = titleCase(x)
		}
		cond := " condition"
		if len(a) > 1 {
			cond = " conditions"
		}
		while = append(while, "also has the "+strings.Join(names, " and ")+cond)
	}
	if d := m.str("damage"); d != "" {
		n, sides := game.ParseDice(d)
		while = append(while, fmt.Sprintf("takes %d (%s) %s damage at the start of each of its turns", n*(sides+1)/2, d, titleCase(m.str("damage_type"))))
	}
	if len(while) > 0 {
		s += " While Restrained, the target " + strings.Join(while, " and ") + "."
	}
	return s
}

// giveEngulf: the engulf a failed save gives (the save owner's finishTarget, for every target kind): the conditions and what lasts with
// them, and the escape on the Restrained record.
func (e saveEffect) giveEngulf(lobby, target int, source string) []string {
	if e.Engulf == nil {
		return nil
	}
	m := e.Engulf
	g, ok := parseHitRider(engulfSentences(m))
	if !ok {
		return []string{"[the engulf attached to this entry could not be read; the GM applies it]"}
	}
	skill := m.str("escape_skill")
	if skill == "" {
		skill = "athletics"
	}
	g.SourceID = e.SourceID
	g.Escapes = []conditionEscape{{Label: stripUsage(source), Ability: map[string]string{"athletics": "str", "acrobatics": "dex"}[skill], Skill: skill, DC: m.num("escape_dc"), Adjacent: true}} // it, or a creature within 5 feet of it
	out := g.giveConditions(lobby, target, source)
	out = append(out, fmt.Sprintf("escape: an action and a DC %d %s check (POST /api/conditions/escape)", m.num("escape_dc"), titleCase(skill)))
	if n := m.str("note"); n != "" {
		out = append(out, "the stat block adds: "+n)
	}
	return out
}

// curseSentences: the SRD's own words for a curse mechanic, which the curse owner reads (readCurse): one owner of what a curse does.
func curseSentences(m entryMechanic) string {
	s := "Hit: The target is cursed"
	if h := m.num("hours"); h > 0 {
		s += fmt.Sprintf(" for %d hours", h)
	}
	s += "."
	if b, _ := m["no_regain"].(bool); b {
		s += " While cursed, the target can't regain Hit Points."
	}
	if r := m.str("no_rest_benefit"); r != "" {
		s += " Until the curse ends, the target gains no benefit from finishing " +
			map[string]string{"short": "Short Rests", "long": "Long Rests", "short_and_long": "Short or Long Rests"}[r] + "."
	}
	if d := m.str("max_hp_loss"); d != "" {
		n, sides := game.ParseDice(d)
		s += fmt.Sprintf(" Its Hit Point maximum decreases by %d (%s) every 24 hours that elapse.", n*(sides+1)/2, d)
	}
	if c := m.list("conditions"); len(c) > 0 {
		names := make([]string, len(c))
		for i, x := range c {
			names[i] = titleCase(x)
		}
		s += " Until the curse ends, the target has the " + strings.Join(names, " and ") + " condition"
		if len(c) > 1 {
			s += "s"
		}
		s += "."
	}
	return s
}

// curseOnHit: the attached curse of the attacking monster's entry, given by the curse owner from its SRD sentences.
func curseOnHit(lobby, attacker, target int, desc, source string) string {
	m, ok := attachedMechanic(lobby, attacker, desc, "curse")
	if !ok {
		return ""
	}
	e, ok := parseHitRider(curseSentences(m))
	if !ok || e.Curse == nil {
		return " [the curse attached to this attack could not be read; the GM applies it]"
	}
	e.SourceID = attacker
	note := ""
	if n := m.str("note"); n != "" {
		note = "; the stat block adds: " + n
	}
	return " [" + creatureName(lobby, target) + ": " + strings.Join(e.giveConditions(lobby, target, source), "; ") + note + "]"
}

// conditionTimeWords: a random_condition's time in the words of the SRD's riders (riderUntil, conditionDuration read them).
func conditionTimeWords(m entryMechanic) string {
	switch m.str("until") {
	case "start_of_attackers_next_turn":
		return "until the start of the attacker's next turn"
	case "end_of_attackers_next_turn":
		return "until the end of the attacker's next turn"
	case "minutes":
		return "for " + minutesWords(m.num("minutes"))
	}
	return "until the end of its next turn"
}

// randomConditionOnHit: the attached random_condition of the attacking monster's entry: the condition rolled, given by the hit condition
// owner from the SRD sentence it stands for (parseHitRider reads its time; giveConditions honors Immunity and records its end).
func randomConditionOnHit(lobby, attacker, target int, desc, source string) string {
	m, ok := attachedMechanic(lobby, attacker, desc, "random_condition")
	if !ok {
		return ""
	}
	conds := m.list("conditions")
	if len(conds) == 0 {
		return ""
	}
	roll := game.RollDamage(fmt.Sprintf("1d%d", len(conds)), false)
	if roll < 1 || roll > len(conds) {
		return ""
	}
	c := conds[roll-1]
	head := fmt.Sprintf(" [%s: rolled 1d%d = %d: %s", stripUsage(source), len(conds), roll, titleCase(c))
	e, ok := parseHitRider(fmt.Sprintf("Hit: The target has the %s condition %s.", titleCase(c), conditionTimeWords(m)))
	if !ok {
		return head + " (the GM applies it)]"
	}
	e.SourceID = attacker
	if given := e.giveConditions(lobby, target, source); len(given) > 0 {
		return head + "; " + strings.Join(given, "; ") + "]"
	}
	return head + "]"
}

// mechanicHitMove: the attached forced_move of the attacking monster's entry, as the movement owner's hitMove.
func mechanicHitMove(lobby, attacker int, desc string) (hitMove, bool) {
	m, ok := attachedMechanic(lobby, attacker, desc, "forced_move")
	if !ok {
		return hitMove{}, false
	}
	d := m.str("direction")
	return hitMove{MaxSize: m.str("max_size"), Feet: m.num("feet"), Toward: d == "pull", GMPlaces: d == "gm_places"}, true
}

// obscuringZone: the obscuring_zone of the entry being used (as the monster takes the action): the area recorded by the obscured-area owner.
// handled false when the entry has none.
func obscuringZone(r *http.Request, lobbyID, monsterID int, meta *monsterMeta, entryName string, targets []int, apply bool) (string, bool) {
	m, ok := entryMechanicIn(meta, entryName, "obscuring_zone")
	if !ok {
		return "", false
	}
	a := obscuredArea{Label: stripUsage(entryName), Heavy: m.str("area") == "heavily_obscured", Shape: m.str("shape"), Rounds: m.num("minutes") * 10}
	a.Radius = m.num("size")
	if a.Shape == "cube" {
		a.Radius = m.num("size") / 2
	}
	var point *mapSquare
	if v, ok := monsterAreaPoint.Load(lobbyID); ok && m.str("at") == "point" {
		sq := v.(mapSquare)
		point = &sq
	}
	centerOn := monsterID
	if m.str("at") == "point" && point == nil && len(targets) > 0 {
		centerOn = targets[0]
	}
	placeArea(lobbyID, monsterID, &a, point, centerOn, append([]int{monsterID}, targets...))
	what := mechanicsView(srdMonsterEntry{Mechanics: []entryMechanic{m}})[0]["does"]
	if !apply {
		return fmt.Sprintf("%s: it would make an area (%s, %s). Nothing is established.", stripUsage(entryName), what, areaWhere(lobbyID, a)), true
	}
	if _, err := startObscuredArea(lobbyID, monsterID, a); err != nil {
		return fmt.Sprintf("%s: the area could not be recorded; nothing is established. Try again.", stripUsage(entryName)), true
	}
	markExecuted(r) // the server established it: a limited use is counted (monster_day_uses.go)
	return fmt.Sprintf("%s: the area is established (%s), %s; the GM ends it early with POST /api/gm/obscured-area {\"end\": true}.", stripUsage(entryName), what, areaWhere(lobbyID, a)), true
}

// noHealingAuraOver: the living monster of the fight whose no_healing_aura reaches the creature on the battle map ("" when none).
func noHealingAuraOver(lobbyID, id int) string {
	battle, ok := activeEncounterMap(lobbyID)
	if !ok {
		return ""
	}
	at, ok := battle.token(id)
	if !ok {
		return ""
	}
	for _, e := range combatEntries(lobbyID) {
		mid := asInt(e["id"])
		if mid >= 0 || mid == id || asInt(e["hp"]) <= 0 {
			continue
		}
		meta := monsterMetaFor(sprintAny(e["monster_key"]))
		if meta == nil {
			continue
		}
		for _, t := range meta.Traits {
			m, ok := t.mechanic("no_healing_aura")
			if !ok {
				continue
			}
			if origin, ok := battle.token(mid); ok && tokenDistanceFeet(origin, at) <= m.num("feet") {
				return fmt.Sprintf("it is within %d feet of %s (%s)", m.num("feet"), creatureName(lobbyID, mid), t.Name)
			}
		}
	}
	return ""
}

// sortedDamageTypes: the damage types, in order (a choice's options).
func sortedDamageTypes() []string {
	var out []string
	for t := range attackDamageTypes {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// mechanicLightPenalty: a trait's attached light_penalty, as the light owner's lightPenalty.
func mechanicLightPenalty(t srdMonsterEntry) (lightPenalty, bool) {
	m, ok := t.mechanic("light_penalty")
	if !ok {
		return lightPenalty{}, false
	}
	on := m.str("on")
	return lightPenalty{Trait: t.Name, Light: map[string]string{"sunlight": "sunlight", "bright_light": "bright"}[m.str("light")],
		Attacks: true, Checks: on != "attack_rolls", Saves: on == "d20_tests"}, true
}

// lightTurnDamage: the monster's light_turn_damage traits at the start of its turn, each dealt by the monster Hit Point owner while the
// monster stands in its light.
func lightTurnDamage(lobbyID, id int, name string, meta *monsterMeta) []string {
	var out []string
	if meta == nil || monsterHPNow(lobbyID, id) <= 0 {
		return nil
	}
	for _, t := range meta.Traits {
		m, ok := t.mechanic("light_turn_damage")
		if !ok || !lightHolds(lobbyID, id, map[string]string{"sunlight": "sunlight", "bright_light": "bright"}[m.str("light")]) {
			continue
		}
		text, _ := damageMonster(lobbyID, id, m.num("amount"), m.str("damage_type"))
		out = append(out, fmt.Sprintf("%s starts its turn in %s (%s): %s", name, strings.ReplaceAll(m.str("light"), "_", " "), t.Name, strings.TrimSpace(text)))
	}
	return out
}

// untypedAmountRe: a damage amount with no type ("22 (4d10) damage"), the part a chosen_damage_type types; SRD 5.2.1 writes it before
// "of the type chosen for the Draconic Origin trait" (Half-Dragon), which chosenTypeExtraRe reads.
var untypedAmountRe = regexp.MustCompile(`(\d+ \([^)]+\)) damage\b`)

// withListedDamageType: the Hit with the type the GM named in use written into each untyped amount; the refusal, before anything is
// rolled, when use names none of the listed types.
func withListedDamageType(entry, text string, types []string, use string) (string, string) {
	pick, ok := game.MatchString(use, types)
	if !ok {
		return text, fmt.Sprintf("%s deals damage of a type the GM chooses; name one in use (%s). Nothing was rolled.", entry, strings.Join(types, ", "))
	}
	i := strings.Index(text, "Hit:")
	if i < 0 {
		return text, ""
	}
	return text[:i] + untypedAmountRe.ReplaceAllString(text[i:], "$1 "+titleCase(pick)+" damage"), ""
}

// entryActionRow: an entry's attack row as every reader builds it (the stored play rows, the preflight, the import's Multiattack check,
// the report): its rulesText parsed, with what its attached mechanics add to the row.
func entryActionRow(e srdMonsterEntry) monsterActionRow {
	row := parseMonsterAttack(e.Name, rulesText(e)).monsterActionRow
	if m, ok := e.mechanic("chosen_damage_type"); ok {
		row.ChosenTypes = m.list("types")
	}
	return row
}

// mechanic: the entry's attached mechanic of this kind.
func (e srdMonsterEntry) mechanic(kind string) (entryMechanic, bool) {
	for _, m := range e.Mechanics {
		if m.kind() == kind {
			return m, true
		}
	}
	return nil, false
}

// metaEntry: the stat block's action, Bonus Action, Reaction or Legendary Action of that name (its usage left aside).
func metaEntry(meta *monsterMeta, entryName string) (srdMonsterEntry, bool) {
	if meta == nil {
		return srdMonsterEntry{}, false
	}
	for _, g := range [][]srdMonsterEntry{meta.Actions, meta.BonusActions, meta.Reactions, meta.Legendary} {
		for _, e := range g {
			if game.Key(stripUsage(e.Name)) == game.Key(stripUsage(entryName)) {
				return e, true
			}
		}
	}
	return srdMonsterEntry{}, false
}

// entryMechanicIn: the mechanic of this kind attached to the stat block's entry of that name.
func entryMechanicIn(meta *monsterMeta, entryName, kind string) (entryMechanic, bool) {
	if e, ok := metaEntry(meta, entryName); ok {
		return e.mechanic(kind)
	}
	return nil, false
}

// rollForOption: the random_choice of the entry, when one is attached and use names nothing: the option rolled (its index in opts) and
// the words that say so; -1 when the entry has none.
func rollForOption(meta *monsterMeta, entryName string, opts []saveOption) (int, string) {
	m, ok := entryMechanicIn(meta, entryName, "random_choice")
	if !ok || len(opts) == 0 {
		return -1, ""
	}
	faces := m.list("order")
	if len(faces) == 0 {
		for _, o := range opts {
			faces = append(faces, o.Name)
		}
	}
	roll := game.RollDamage(fmt.Sprintf("1d%d", len(faces)), false)
	if roll < 1 || roll > len(faces) {
		return -1, ""
	}
	for i, o := range opts {
		if game.Key(o.Name) == game.Key(faces[roll-1]) {
			return i, fmt.Sprintf("%s: rolled 1d%d = %d: %s%s.", entryName, len(faces), roll, o.Name, noteWords(m))
		}
	}
	return -1, ""
}

func noteWords(m entryMechanic) string {
	if n := m.str("note"); n != "" {
		return " (the stat block adds: " + n + ")"
	}
	return ""
}

// minutesWords: "1 hour", "90 minutes".
func minutesWords(n int) string {
	if n > 0 && n%60 == 0 {
		h := n / 60
		return fmt.Sprintf("%d hour%s", h, map[bool]string{true: "s", false: ""}[h != 1])
	}
	return fmt.Sprintf("%d minute%s", n, map[bool]string{true: "s", false: ""}[n != 1])
}

func (m entryMechanic) str(k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

func (m entryMechanic) num(k string) int {
	f, _ := m[k].(float64)
	return int(f)
}

func (m entryMechanic) kind() string { return strings.ToLower(m.str("kind")) }

func (m entryMechanic) list(k string) []string {
	var out []string
	if l, ok := m[k].([]interface{}); ok {
		for _, v := range l {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func mechanicKindOf(kind string) (mechanicKind, bool) {
	for _, k := range mechanicCatalog {
		if k.Kind == strings.ToLower(strings.TrimSpace(kind)) {
			return k, true
		}
	}
	return mechanicKind{}, false
}

func mechanicKindNames() string {
	var n []string
	for _, k := range mechanicCatalog {
		n = append(n, k.Kind)
	}
	return strings.Join(n, ", ")
}

// mechanicProblem: what is wrong with one entry's mechanics ("" when nothing). group is trait, action, bonus, reaction or legendary.
func mechanicProblem(group string, e srdMonsterEntry) string {
	if len(e.Mechanics) > maxEntryMechanics {
		return fmt.Sprintf("%s: up to %d mechanics an entry", e.Name, maxEntryMechanics)
	}
	seen := map[string]bool{}
	for i, m := range e.Mechanics {
		where := fmt.Sprintf("%s: mechanics[%d]", e.Name, i)
		k, ok := mechanicKindOf(m.str("kind"))
		if !ok {
			return fmt.Sprintf("%s: kind must be one of %s (GET /api/gm/compendium/mechanics describes them)", where, mechanicKindNames())
		}
		where = fmt.Sprintf("%s (%s)", where, k.Kind)
		if seen[k.Kind] {
			return where + ": an entry takes each kind once"
		}
		seen[k.Kind] = true
		if p := mechanicFits[k.On].Check(group, e); p != "" {
			return where + ": " + p
		}
		if p := k.paramProblem(m, e); p != "" {
			return where + ": " + p
		}
		if c, ok := m["covers"]; ok {
			s, isText := c.(string)
			switch {
			case !isText || strings.TrimSpace(s) == "" || len(s) > mechanicCoversLen:
				return fmt.Sprintf("%s: covers is the entry's own words this mechanic replaces, 1 to %d bytes", where, mechanicCoversLen)
			case !strings.Contains(strings.ToLower(e.Text), strings.ToLower(strings.TrimSpace(s))):
				return where + ": covers must be words of the entry's text, copied exactly"
			case strings.Contains(strings.ToLower(strings.TrimSpace(s)), "attack roll:"):
				return where + ": covers can't include the attack line, which the attack owner reads"
			}
		}
	}
	return ""
}

// paramProblem: the parameters of one attached mechanic against its row.
func (k mechanicKind) paramProblem(m entryMechanic, e srdMonsterEntry) string {
	known := map[string]bool{"kind": true, "covers": true}
	for _, p := range k.Params {
		known[p.Name] = true
	}
	var unknown []string
	for f := range m {
		if !known[f] {
			unknown = append(unknown, f)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		var names []string
		for _, p := range k.Params {
			names = append(names, p.Name)
		}
		return fmt.Sprintf("unknown field %s; it takes %s and covers", strings.Join(unknown, ", "), strings.Join(names, ", "))
	}
	holds := func(cond string) bool {
		if cond == "" {
			return true
		}
		name, val, eq := strings.Cut(cond, "=")
		if _, given := m[name]; !given {
			return false
		}
		return !eq || strings.EqualFold(m.str(name), val)
	}
	for _, p := range k.Params {
		v, given := m[p.Name]
		if !given {
			if p.Required {
				return p.Name + " is required: " + p.Does
			}
			continue
		}
		if !holds(p.Only) {
			return fmt.Sprintf("%s is only taken with %s", p.Name, strings.ReplaceAll(p.Only, "=", " "))
		}
		if p.Needs != "" {
			if _, ok := m[p.Needs]; !ok {
				return fmt.Sprintf("%s needs %s", p.Name, p.Needs)
			}
		}
		switch p.Type {
		case "choice", "condition":
			opts := p.Options
			if p.Type == "condition" {
				opts = rulesConditions()
			}
			s, _ := v.(string)
			got, ok := game.MatchString(s, opts)
			if !ok {
				return fmt.Sprintf("%s must be one of %s", p.Name, strings.Join(opts, ", "))
			}
			m[p.Name] = got
		case "number":
			f, ok := v.(float64)
			if !ok || f != float64(int(f)) || int(f) < p.Min || int(f) > p.Max {
				return fmt.Sprintf("%s must be a whole number from %d to %d", p.Name, p.Min, p.Max)
			}
		case "text":
			s, ok := v.(string)
			if !ok || strings.TrimSpace(s) == "" || len(s) > p.MaxLen {
				return fmt.Sprintf("%s must be text of 1 to %d bytes", p.Name, p.MaxLen)
			}
		case "flag":
			if _, ok := v.(bool); !ok {
				return p.Name + " must be true or false"
			}
		case "dice":
			sv, _ := v.(string)
			n, sides := game.ParseDice(strings.TrimSpace(sv))
			if n < 1 || n > 100 || sides < 2 || sides > 100 || fmt.Sprintf("%dd%d", n, sides) != strings.ToLower(strings.TrimSpace(sv)) {
				return p.Name + " must be dice such as 3d6 (1 to 100 dice of 2 to 100 sides)"
			}
			m[p.Name] = fmt.Sprintf("%dd%d", n, sides)
		case "damage_types", "entry_options", "conditions":
			var opts []string
			if p.Type == "conditions" {
				opts = rulesConditions()
			} else if p.Type == "damage_types" {
				opts = sortedDamageTypes()
			} else {
				for _, o := range splitSaveOptions(rulesText(e)) {
					opts = append(opts, o.Name)
				}
			}
			l, ok := v.([]interface{})
			min := p.Min
			if p.Type == "entry_options" {
				min = len(opts)
			}
			if !ok || len(l) < min || len(l) > len(opts) {
				what := fmt.Sprintf("%d or more", min)
				if p.Type == "entry_options" {
					what = "every one once"
				}
				return fmt.Sprintf("%s must be a list of %s of: %s", p.Name, what, strings.Join(opts, ", "))
			}
			seen := map[string]bool{}
			for i, x := range l {
				s, _ := x.(string)
				got, ok := game.MatchString(s, opts)
				if !ok || seen[got] {
					return fmt.Sprintf("%s[%d] must be one of %s, each once", p.Name, i, strings.Join(opts, ", "))
				}
				seen[got] = true
				l[i] = got
			}
		}
	}
	if k.check != nil {
		if c := k.check(e, m); c != "" {
			return c
		}
	}
	return ""
}

// rulesText: the entry's text as the server's text rules read it, without the words its attached mechanics cover (the mechanic applies
// them). The GM's view keeps the whole text.
func rulesText(e srdMonsterEntry) string {
	text := e.Text
	for _, m := range e.Mechanics {
		c := m.str("covers")
		if c == "" {
			continue
		}
		if i := strings.Index(strings.ToLower(text), strings.ToLower(c)); i >= 0 {
			before, after := strings.TrimRight(text[:i], " "), strings.TrimLeft(text[i+len(c):], " ")
			if after != "" && !strings.ContainsAny(after[:1], ".,;:)") {
				after = " " + after
			}
			text = before + after
		}
	}
	return strings.TrimSpace(text)
}

// attachedMechanic: the mechanic of this kind attached to the entry of the attacking monster whose text the route is reading (desc: the
// row's text, which is the entry's rulesText). Only a compendium monster has attached mechanics.
func attachedMechanic(lobby, attacker int, desc, kind string) (entryMechanic, bool) {
	_, meta, ok := monsterCombatant(lobby, attacker)
	if !ok || meta == nil {
		return nil, false
	}
	want := strings.TrimSpace(desc)
	for _, g := range [][]srdMonsterEntry{meta.Actions, meta.BonusActions, meta.Reactions, meta.Legendary} {
		for _, e := range g {
			if len(e.Mechanics) == 0 || (rulesText(e) != want && strings.TrimSpace(e.Text) != want) {
				continue
			}
			return e.mechanic(kind)
		}
	}
	return nil, false
}

// mechanicsView: an entry's attached mechanics as the GM reads them (the report and the stat block).
func mechanicsView(e srdMonsterEntry) []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, m := range e.Mechanics {
		k, ok := mechanicKindOf(m.str("kind"))
		if !ok {
			continue
		}
		v := map[string]interface{}{"kind": k.Kind, "does": k.describe(m), "applied_by": k.AppliedBy}
		if c := m.str("covers"); c != "" {
			v["covers"] = c
		}
		out = append(out, v)
	}
	return out
}

// mechanicCovers: the attached mechanic whose covers holds this sentence (its words without the heading), "" when none.
func mechanicCovers(e srdMonsterEntry, sentence string) string {
	body := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(entryHeadingRe.ReplaceAllString(sentence, "")), "."))
	if body == "" {
		return ""
	}
	for _, m := range e.Mechanics {
		if c := strings.ToLower(m.str("covers")); c != "" && strings.Contains(c, body) {
			return m.kind()
		}
	}
	return ""
}

// mechanicCatalogView: the catalogue as GET /api/gm/compendium/mechanics serves it.
func mechanicCatalogView() []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, k := range mechanicCatalog {
		params := []map[string]interface{}{}
		for _, p := range k.Params {
			v := map[string]interface{}{"name": p.Name, "type": p.Type, "required": p.Required, "does": p.Does}
			switch p.Type {
			case "choice":
				v["options"] = p.Options
			case "condition":
				v["options"] = rulesConditions()
			case "number":
				v["min"], v["max"] = p.Min, p.Max
			case "text":
				v["max_length"] = p.MaxLen
			case "damage_types":
				v["options"], v["min_items"] = sortedDamageTypes(), p.Min
			case "entry_options":
				v["options"] = "the names of the effects the entry offers"
			case "conditions":
				v["options"], v["min_items"] = rulesConditions(), p.Min
			case "dice":
				v["format"] = "NdM, such as 3d6"
			}
			if p.Only != "" {
				v["only_with"] = strings.ReplaceAll(p.Only, "=", " ")
			}
			if p.Needs != "" {
				v["needs"] = p.Needs
			}
			params = append(params, v)
		}
		params = append(params, map[string]interface{}{"name": "covers", "type": "text", "required": false, "max_length": mechanicCoversLen,
			"does": "the entry's own words this mechanic replaces, copied exactly: the server's text rules then leave them out and the report marks them applied"})
		out = append(out, map[string]interface{}{"kind": k.Kind, "does": k.Does, "attaches_to": mechanicFits[k.On].Says,
			"applied_by": k.AppliedBy, "params": params, "example": k.Example, "example_entry_text": k.ExampleOn})
	}
	return out
}

// handleGMCompendiumMechanics godoc
// @Summary The mechanics a GM can attach to a monster's entries
// @Description Lists every mechanic kind a GM can attach to an entry of a monster it imports (the entry's mechanics list): what it does, which entries it attaches to, its parameters with their options and limits, and an example. Attached mechanics are how an imported monster's wording that the server doesn't read, or a house rule on an SRD monster (import a copy of it), is applied by the server's own rules; covers names the entry's words the mechanic replaces. The import, edit and preview check them and say which sentences they cover. campaign_id names a campaign you run (needed when you run more than one).
// @Tags GM
// @Param srd query string false "an SRD 5.2.1 monster's name: the reply adds its stat block as an import takes it (srd_monster), to copy with mechanics attached"
// @Param campaign_id query int false "a campaign you run (needed when you run more than one)"
// @Router /gm/compendium/mechanics [get]
func handleGMCompendiumMechanics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "Use GET."})
		return
	}
	agent, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	if _, code, msg := gmCampaignFor(agent, r, campaignBeforeStart()); code != "" {
		writeGMCampaignProblem(w, code, msg)
		return
	}
	out := map[string]interface{}{
		"mechanics": mechanicCatalogView(),
		"how":       "Add \"mechanics\": [{\"kind\": ..., <its params>, \"covers\": \"<the entry's words it replaces>\"}] to an entry of a monster you import (POST /api/gm/compendium/monsters) or preview (POST /api/gm/compendium/monsters/preview). To house-rule an SRD monster, ask this with srd=<its name>, attach the mechanics to the copy and import it.",
	}
	if name := strings.TrimSpace(r.URL.Query().Get("srd")); name != "" {
		m, ok := srdMonsterNamed(name)
		if !ok {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "monster_not_found", "message": "No SRD 5.2.1 monster has that name. GET /api/universe/monsters lists them."})
			return
		}
		out["srd_monster"] = m
	}
	writeJSONStatus(w, http.StatusOK, out)
}

// srdMonsterNamed: the SRD 5.2.1 stat block of that name or slug, as an import takes it.
func srdMonsterNamed(name string) (srdMonster2024, bool) {
	for _, m := range srdMonsters2024() {
		if game.Key(m.Name) == game.Key(name) || monsterSlug(m.Name) == strings.ToLower(name) {
			return m, true
		}
	}
	return srdMonster2024{}, false
}

// mechanicsKey: an entry's mechanics as text, for the report memo.
func mechanicsKey(e srdMonsterEntry) string {
	if len(e.Mechanics) == 0 {
		return ""
	}
	b, _ := json.Marshal(e.Mechanics)
	return string(b)
}
