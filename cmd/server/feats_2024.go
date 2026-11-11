package main

// Feats, 2024 rules (SRD 5.2.1). Only the feats of the SRD are offered: Origin (Alert, Magic Initiate, Savage Attacker, Skilled),
// General (Ability Score Improvement, Grappler), Fighting Style (Archery, Defense, Great Weapon Fighting, Two-Weapon Fighting)
// and Epic Boon (Boon of Combat Prowess, Dimensional Travel, Fate, Irresistible Offense, Spell Recall, the Night Spirit, Truesight).
// Mechanics:
//	Savage Attacker             once per turn, a weapon hit rolls the damage dice twice and keeps the higher result.
//	Boon of Combat Prowess      a miss becomes a hit (Peerless Aim), once until the start of your next turn.
//	Boon of Irresistible Offense  Bludgeoning, Piercing and Slashing damage ignores Resistance; a natural 20 adds the boon's ability score.
//	Boon of Spell Recall        casting with a level 1-4 slot rolls 1d4; a match leaves the slot unspent.
//	Boon of Fate                action "improve_fate": roll 2d4 to add to or subtract from a D20 Test within 60 feet.
//	Boon of the Night Spirit    action "merge_with_shadows" (Bonus Action) gives the Invisible condition until you act.
//	Boon of Dimensional Travel / Truesight  action "blink_steps": teleport up to 30 feet after the Attack or Magic action.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

const featsScoreCap = 20

func init() {
	for slug, f := range originFeatsSRD() {
		availableFeats[slug] = f
	}
}

// availableFeats are the feats a character can take now (SRD 5.2.1).
var availableFeats = map[string]Feat{
	"ability_score_improvement": {
		Name: "Ability Score Improvement", Category: "General", Prerequisite: "level:4", Repeatable: true,
		Description: "Increase one ability score of your choice by 2, or increase two ability scores of your choice by 1. This feat can't increase an ability score above 20. Repeatable. You can take this feat more than once.",
		Benefits:    []string{"Increase one ability score of your choice by 2, or increase two ability scores of your choice by 1 (use POST /api/characters/{id}/asi)"},
	},
	"grappler": {
		Name: "Grappler", Category: "General", Prerequisite: "level:4;str_or_dex:13",
		Description:    "You gain the following benefits.",
		AbilityChoices: []string{"str", "dex"}, AbilityPoints: 1, MaxScore: 20,
		Benefits: []string{
			"Ability Score Increase. Increase your Strength or Dexterity score by 1, to a maximum of 20.",
			"Punch and Grab. When you hit a creature with an Unarmed Strike as part of the Attack action on your turn, you can use both the Damage and the Grapple option. You can use this benefit only once per turn.",
			"Attack Advantage. You have Advantage on attack rolls against a creature Grappled by you.",
			"Fast Wrestler. You don't have to spend extra movement to move a creature Grappled by you if the creature is your size or smaller.",
		},
	},
	"archery": {
		Name: "Archery", Category: "Fighting Style", Prerequisite: "fighting_style_feature",
		Description: "You gain a +2 bonus to attack rolls you make with Ranged weapons.",
		Benefits:    []string{"You gain a +2 bonus to attack rolls you make with Ranged weapons."},
	},
	"defense": {
		Name: "Defense", Category: "Fighting Style", Prerequisite: "fighting_style_feature",
		Description: "While you're wearing Light, Medium, or Heavy armor, you gain a +1 bonus to Armor Class.",
		Benefits:    []string{"While you're wearing Light, Medium, or Heavy armor, you gain a +1 bonus to Armor Class."},
	},
	"great_weapon_fighting": {
		Name: "Great Weapon Fighting", Category: "Fighting Style", Prerequisite: "fighting_style_feature",
		Description: "When you roll damage for an attack you make with a Melee weapon that you are holding with two hands, you can treat any 1 or 2 on a damage die as a 3. The weapon must have the Two-Handed or Versatile property to gain this benefit.",
		Benefits:    []string{"When you roll damage for an attack you make with a Melee weapon that you are holding with two hands, you can treat any 1 or 2 on a damage die as a 3. The weapon must have the Two-Handed or Versatile property to gain this benefit."},
	},
	"two_weapon_fighting": {
		Name: "Two-Weapon Fighting", Category: "Fighting Style", Prerequisite: "fighting_style_feature",
		Description: "When you make an extra attack as a result of using a weapon that has the Light property, you can add your ability modifier to the damage of that attack if you aren't already adding it to the damage.",
		Benefits:    []string{"When you make an extra attack as a result of using a weapon that has the Light property, you can add your ability modifier to the damage of that attack if you aren't already adding it to the damage."},
	},
	"boon_of_combat_prowess": {
		Name: "Boon of Combat Prowess", Category: "Epic Boon", Prerequisite: "level:19",
		Description:    "You gain the following benefits.",
		AbilityChoices: []string{"str", "dex", "con", "int", "wis", "cha"}, AbilityPoints: 1, MaxScore: 30,
		Benefits: []string{
			"Ability Score Increase. Increase one ability score of your choice by 1, to a maximum of 30.",
			"Peerless Aim. When you miss with an attack roll, you can hit instead. Once you use this benefit, you can't use it again until the start of your next turn.",
		},
	},
	"boon_of_dimensional_travel": {
		Name: "Boon of Dimensional Travel", Category: "Epic Boon", Prerequisite: "level:19",
		Description:    "You gain the following benefits.",
		AbilityChoices: []string{"str", "dex", "con", "int", "wis", "cha"}, AbilityPoints: 1, MaxScore: 30,
		Benefits: []string{
			"Ability Score Increase. Increase one ability score of your choice by 1, to a maximum of 30.",
			"Blink Steps. Immediately after you take the Attack action or the Magic action, you can teleport up to 30 feet to an unoccupied space you can see.",
		},
	},
	"boon_of_fate": {
		Name: "Boon of Fate", Category: "Epic Boon", Prerequisite: "level:19",
		Description:    "You gain the following benefits.",
		AbilityChoices: []string{"str", "dex", "con", "int", "wis", "cha"}, AbilityPoints: 1, MaxScore: 30,
		Benefits: []string{
			"Ability Score Increase. Increase one ability score of your choice by 1, to a maximum of 30.",
			"Improve Fate. When you or another creature within 60 feet of you succeeds on or fails a D20 Test, you can roll 2d4 and apply the total rolled as a bonus or penalty to the d20 roll. Once you use this benefit, you can't use it again until you roll Initiative or finish a Short or Long Rest.",
		},
	},
	"boon_of_irresistible_offense": {
		Name: "Boon of Irresistible Offense", Category: "Epic Boon", Prerequisite: "level:19",
		Description:    "You gain the following benefits.",
		AbilityChoices: []string{"str", "dex"}, AbilityPoints: 1, MaxScore: 30,
		Benefits: []string{
			"Ability Score Increase. Increase your Strength or Dexterity score by 1, to a maximum of 30.",
			"Overcome Defenses. The Bludgeoning, Piercing, and Slashing damage you deal always ignores Resistance.",
			"Overwhelming Strike. When you roll a 20 on the d20 for an attack roll, you can deal extra damage to the target equal to the ability score increased by this feat. The extra damage's type is the same as the attack's type.",
		},
	},
	"boon_of_spell_recall": {
		Name: "Boon of Spell Recall", Category: "Epic Boon", Prerequisite: "level:19;spellcasting",
		Description:    "You gain the following benefits.",
		AbilityChoices: []string{"int", "wis", "cha"}, AbilityPoints: 1, MaxScore: 30,
		Benefits: []string{
			"Ability Score Increase. Increase your Intelligence, Wisdom, or Charisma score by 1, to a maximum of 30.",
			"Free Casting. Whenever you cast a spell with a level 1-4 spell slot, roll 1d4. If the number you roll is the same as the slot's level, the slot isn't expended.",
		},
	},
	"boon_of_the_night_spirit": {
		Name: "Boon of the Night Spirit", Category: "Epic Boon", Prerequisite: "level:19",
		Description:    "You gain the following benefits.",
		AbilityChoices: []string{"str", "dex", "con", "int", "wis", "cha"}, AbilityPoints: 1, MaxScore: 30,
		Benefits: []string{
			"Ability Score Increase. Increase one ability score of your choice by 1, to a maximum of 30.",
			"Merge with Shadows. While within Dim Light or Darkness, you can give yourself the Invisible condition as a Bonus Action. The condition ends on you immediately after you take an action, a Bonus Action, or a Reaction.",
			"Shadowy Form. While within Dim Light or Darkness, you have Resistance to all damage except Psychic and Radiant.",
		},
	},
	"boon_of_truesight": {
		Name: "Boon of Truesight", Category: "Epic Boon", Prerequisite: "level:19",
		Description:    "You gain the following benefits.",
		AbilityChoices: []string{"str", "dex", "con", "int", "wis", "cha"}, AbilityPoints: 1, MaxScore: 30,
		Benefits: []string{
			"Ability Score Increase. Increase one ability score of your choice by 1, to a maximum of 30.",
			"Truesight. You have Truesight with a range of 60 feet.",
		},
	},
}

func originFeatsSRD() map[string]Feat {
	return map[string]Feat{
		"alert": {
			Name: "Alert", Category: "Origin",
			Description: "You gain the following benefits.",
			Benefits: []string{
				"Initiative Proficiency. When you roll Initiative, you can add your Proficiency Bonus to the roll.",
				"Initiative Swap. Immediately after you roll Initiative, you can swap your Initiative with the Initiative of one willing ally in the same combat. You can't make this swap if you or the ally has the Incapacitated condition.",
			},
			Features: map[string]string{"initiative_bonus": "proficiency", "initiative_swap": "true", "origin_feat": "true"},
		},
		"magic_initiate": {
			Name: "Magic Initiate", Category: "Origin", Repeatable: true,
			Description: "You gain the following benefits.",
			Benefits: []string{
				"Two Cantrips. You learn two cantrips of your choice from the Cleric, Druid, or Wizard spell list. Intelligence, Wisdom, or Charisma is your spellcasting ability for this feat's spells (choose when you select this feat).",
				"Level 1 Spell. Choose a level 1 spell from the same list you selected for this feat's cantrips. You always have that spell prepared. You can cast it once without a spell slot, and you regain the ability to cast it in that way when you finish a Long Rest. You can also cast the spell using any spell slots you have.",
				"Spell Change. Whenever you gain a new level, you can replace one of the spells you chose for this feat with a different spell of the same level from the chosen spell list.",
				"Repeatable. You can take this feat more than once, but you must choose a different spell list each time.",
			},
			Features: map[string]string{"origin_feat": "true", "cantrips": "2", "level1_spell": "1"},
		},
		"savage_attacker": {
			Name: "Savage Attacker", Category: "Origin",
			Description: "You've trained to deal particularly damaging strikes. Once per turn when you hit a target with a weapon, you can roll the weapon's damage dice twice and use either roll against the target.",
			Benefits:    []string{"Once per turn when you hit a target with a weapon, you can roll the weapon's damage dice twice and use either roll against the target."},
			Features:    map[string]string{"reroll_melee_damage": "true", "origin_feat": "true"},
		},
		"skilled": {
			Name: "Skilled", Category: "Origin", Repeatable: true,
			Description: "You gain proficiency in any combination of three skills or tools of your choice.",
			Benefits: []string{
				"You gain proficiency in any combination of three skills or tools of your choice.",
				"Repeatable. You can take this feat more than once.",
			},
			Features: map[string]string{"origin_feat": "true", "proficiencies": "3"},
		},
	}
}

// lookupFeat finds a feat of the SRD 5.2.1.
func lookupFeat(slug string) (Feat, bool) {
	f, ok := availableFeats[slug]
	return f, ok
}

// featPrerequisite checks a feat's prerequisite (";"-separated codes) and says what is missing.
func featPrerequisite(prereq, class string, level, str, dex int) (bool, string) {
	if prereq == "" {
		return true, ""
	}
	for _, code := range strings.Split(prereq, ";") {
		switch {
		case strings.HasPrefix(code, "level:"):
			n, _ := strconv.Atoi(strings.TrimPrefix(code, "level:"))
			if level < n {
				return false, fmt.Sprintf("Requires character level %d or higher (you are level %d)", n, level)
			}
		case strings.HasPrefix(code, "str_or_dex:"):
			n, _ := strconv.Atoi(strings.TrimPrefix(code, "str_or_dex:"))
			if str < n && dex < n {
				return false, fmt.Sprintf("Requires Strength or Dexterity %d or higher", n)
			}
		case code == "fighting_style_feature":
			c := strings.ToLower(class)
			if c != "fighter" && c != "paladin" && c != "ranger" {
				return false, "Requires the Fighting Style feature (Fighter, Paladin or Ranger)"
			}
		case code == "spellcasting", code == "spellcaster":
			c := strings.ToLower(class)
			if !map[string]bool{"bard": true, "cleric": true, "druid": true, "paladin": true, "ranger": true, "sorcerer": true, "warlock": true, "wizard": true}[c] {
				return false, "Requires the Spellcasting or Pact Magic feature"
			}
		case strings.HasPrefix(code, "str:"):
			n, _ := strconv.Atoi(strings.TrimPrefix(code, "str:"))
			if str < n {
				return false, fmt.Sprintf("Requires Strength %d (you have %d)", n, str)
			}
		case strings.HasPrefix(code, "dex:"):
			n, _ := strconv.Atoi(strings.TrimPrefix(code, "dex:"))
			if dex < n {
				return false, fmt.Sprintf("Requires Dexterity %d (you have %d)", n, dex)
			}
		}
	}
	return true, ""
}

// asiLevelsFor lists the levels where a class gets the Ability Score Improvement feature (a Fighter gets two more, a Rogue one more).
func asiLevelsFor(class string) []int {
	switch strings.ToLower(class) {
	case "fighter":
		return []int{4, 6, 8, 12, 14, 16, 19}
	case "rogue":
		return []int{4, 8, 10, 12, 16, 19}
	}
	return []int{4, 8, 12, 16, 19}
}

func ensureFeatColumns() {
	if db != nil {
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS savage_attacker_used BOOLEAN DEFAULT FALSE`)
	}
}

// savageAttackerReroll: once per turn on a weapon hit, roll the damage dice again and keep the higher base result.
func savageAttackerReroll(charID int, dice string, crit bool, mod, current int) (int, string) {
	if dice == "" || !hasSpecificFeat(charID, "savage_attacker") {
		return current, ""
	}
	var used bool
	db.QueryRow(`SELECT COALESCE(savage_attacker_used,false) FROM characters WHERE id = $1`, charID).Scan(&used)
	if used {
		return current, ""
	}
	db.Exec(`UPDATE characters SET savage_attacker_used = true WHERE id = $1`, charID)
	again := game.RollDamage(dice, crit) + mod
	if again > current {
		return again, fmt.Sprintf(" [Savage Attacker: second roll %d is higher]", again)
	}
	return current, fmt.Sprintf(" [Savage Attacker: second roll %d, keeping %d]", again, current)
}

// peerlessAim turns a miss into a hit (Boon of Combat Prowess), once until the start of the character's next turn.
func peerlessAim(charID int) bool {
	if !hasSpecificFeat(charID, "boon_of_combat_prowess") {
		return false
	}
	used := classResourcesUsed(charID)
	if used["peerless_aim"] > 0 {
		return false
	}
	used["peerless_aim"] = 1
	writeUsed(charID, used)
	return true
}

// peerlessWillHit: a natural 1 against a monster that Peerless Aim will turn into a hit (it is checked, not spent, here; attack_target
// spends it). The attack then rolls its damage and on-hit extras like any hit (O-10).
func peerlessWillHit(charID int) bool {
	if _, _, ok := attackedMonster(charID); !ok || !hasSpecificFeat(charID, "boon_of_combat_prowess") {
		return false
	}
	return classResourcesUsed(charID)["peerless_aim"] == 0
}

func classResourcesUsed(charID int) map[string]int {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(class_resources_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	used := map[string]int{}
	json.Unmarshal(raw, &used)
	return used
}

// boonOffense: Boon of Irresistible Offense. Returns whether physical damage ignores Resistance and the extra damage of a natural 20.
func boonOffense(charID int, dtype string, natural int) (ignoreResistance bool, extra int) {
	if !hasSpecificFeat(charID, "boon_of_irresistible_offense") {
		return false, 0
	}
	switch strings.ToLower(dtype) {
	case "bludgeoning", "piercing", "slashing":
		ignoreResistance = true
	}
	if natural == 20 {
		var str, dex int
		db.QueryRow(`SELECT str, dex FROM characters WHERE id = $1`, charID).Scan(&str, &dex)
		extra = str
		if dex > str {
			extra = dex
		}
	}
	return
}

var spellRecallNotes sync.Map

// spellRecall: Boon of Spell Recall. Roll 1d4 on a level 1-4 slot; a match leaves the slot unspent.
func spellRecall(charID, slotLevel int) bool {
	if slotLevel < 1 || slotLevel > 4 || !hasSpecificFeat(charID, "boon_of_spell_recall") {
		return false
	}
	r := game.RollDie(4)
	if r == slotLevel {
		spellRecallNotes.Store(charID, fmt.Sprintf(" [Boon of Spell Recall: rolled %d, the level %d slot is not expended]", r, slotLevel))
		return true
	}
	return false
}

func takeSpellRecallNote(charID int) string {
	if v, ok := spellRecallNotes.LoadAndDelete(charID); ok {
		return v.(string)
	}
	return ""
}

// improveFate: Boon of Fate, once until Initiative is rolled or a Short or Long Rest.
func improveFate(charID int) string {
	if !hasSpecificFeat(charID, "boon_of_fate") {
		return "Improve Fate needs the Boon of Fate."
	}
	used := classResourcesUsed(charID)
	if used["improve_fate"] > 0 {
		return "You already used Improve Fate; it returns when you roll Initiative or finish a Short or Long Rest."
	}
	used["improve_fate"] = 1
	writeUsed(charID, used)
	a, b := game.RollDie(4), game.RollDie(4)
	return fmt.Sprintf("🎲 Improve Fate: 2d4 = %d + %d = %d. Add it to, or subtract it from, the d20 roll of the creature within 60 feet (the GM applies it to the roll).", a, b, a+b)
}

// mergeWithShadows: Boon of the Night Spirit, a Bonus Action in Dim Light or Darkness.
func mergeWithShadows(charID int) string {
	if !hasSpecificFeat(charID, "boon_of_the_night_spirit") {
		return "Merge with Shadows needs the Boon of the Night Spirit."
	}
	var name string
	db.QueryRow(`SELECT name FROM characters WHERE id = $1`, charID).Scan(&name)
	addCharacterCondition(charID, "invisible")
	return fmt.Sprintf("🌑 %s merges with the shadows and is Invisible until they take an action, a Bonus Action, or a Reaction. This works only in Dim Light or Darkness (the GM judges the light).", name)
}

// blinkSteps: Boon of Dimensional Travel, right after the Attack or Magic action.
func blinkSteps(charID int, description string) string {
	if !hasSpecificFeat(charID, "boon_of_dimensional_travel") {
		return "Blink Steps needs the Boon of Dimensional Travel."
	}
	return "✨ Blink Steps: you teleport up to 30 feet to an unoccupied space you can see. Say where in the description; the GM moves you."
}

// clearUsedKeys gives back class resources kept in class_resources_used.
func clearUsedKeys(charID int, keys ...string) {
	used := classResourcesUsed(charID)
	changed := false
	for _, k := range keys {
		if _, ok := used[k]; ok {
			delete(used, k)
			changed = true
		}
	}
	if changed {
		writeUsed(charID, used)
	}
}

// featPrereqText turns a prerequisite code into words.
func featPrereqText(code string) string {
	if code == "" {
		return ""
	}
	var parts []string
	for _, c := range strings.Split(code, ";") {
		switch {
		case strings.HasPrefix(c, "level:"):
			parts = append(parts, "Level "+strings.TrimPrefix(c, "level:")+"+")
		case strings.HasPrefix(c, "str_or_dex:"):
			parts = append(parts, "Strength or Dexterity "+strings.TrimPrefix(c, "str_or_dex:")+"+")
		case c == "fighting_style_feature":
			parts = append(parts, "Fighting Style Feature")
		case c == "spellcasting" || c == "spellcaster":
			parts = append(parts, "Spellcasting Feature")
		default:
			parts = append(parts, c)
		}
	}
	return strings.Join(parts, ", ")
}
