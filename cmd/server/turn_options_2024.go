package main

// What my-turn tells a player they can do (LOW-LEVEL-PLAN phase D, Nov 10). The options listed Attack, Dodge and Help and a few class
// entries; an AI player at level 3 didn't learn of Dash, Disengage, Hide, Bardic Inspiration, Lay on Hands, Innate Sorcery, Action
// Surge, Channel Divinity, Steady Aim or the Martial Arts strike, Wild Shape was listed as an action (a Bonus Action in 2024) and Second
// Wind said "once per short rest" (2014). turnOptions2024 adds the SRD 5.2.1 actions and each class's features of the character's
// levels, with the word to send and the uses left.

import (
	"encoding/json"
	"fmt"
	"strings"
)

func resourceLeft(charID int, class string, level int, key string) (left, max int) {
	var cha int
	var raw []byte
	db.QueryRow(`SELECT cha, COALESCE(class_resources_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&cha, &raw)
	used := map[string]int{}
	json.Unmarshal(raw, &used)
	max = classResourceMax(charID, class, level, key, (cha-10)/2)
	return max - used[key], max
}

func opt(name, use, what string) map[string]interface{} {
	return map[string]interface{}{"name": name, "use": use, "description": what}
}

// standardActions2024: the actions of the SRD 5.2.1 everyone has, besides Attack, Dodge, Help and Cast.
func standardActions2024() []map[string]interface{} {
	return []map[string]interface{}{
		opt("Dash", `{"action": "dash"}`, "Gain extra movement equal to your Speed this turn."),
		opt("Disengage", `{"action": "disengage"}`, "Your movement doesn't provoke Opportunity Attacks this turn."),
		opt("Hide", `{"action": "hide"}`, "A DC 15 Dexterity (Stealth) check while heavily obscured or behind cover; on a success you have the Invisible condition until you're found."),
		opt("Search", `{"action": "search", "description": "search the room"}`, "A Wisdom check (Insight, Medicine, Perception or Survival) to discern something."),
		opt("Study", `{"action": "study", "description": "recall what I know of trolls"}`, "An Intelligence check (Arcana, History, Investigation, Nature or Religion)."),
		opt("Influence", `{"action": "influence", "description": "persuade the guard"}`, "A Charisma (or Wisdom for animals) check to sway a creature."),
		opt("Ready", `{"action": "ready", "description": "attack the first goblin through the door"}`, "Prepare an action and its trigger; it takes your Reaction when the trigger happens."),
		opt("Utilize", `{"action": "use_item", "item": "Potion of Healing"}`, "Use a nonmagical object or a magic item (a potion: a Bonus Action)."),
	}
}

// turnOptions2024 returns the class features (actions, bonus actions, free actions) of the character's levels.
func turnOptions2024(charID int, class, subclass string, level int, raging, wildShaped bool) (actions, bonus, free []map[string]interface{}) {
	c := strings.ToLower(class)
	sub := strings.ToLower(subclass)
	left := func(key string) string {
		l, m := resourceLeft(charID, c, level, key)
		if m <= 0 {
			return ""
		}
		return fmt.Sprintf(" (%d of %d left)", l, m)
	}
	switch c {
	case "barbarian":
		if level >= 3 && sub == "berserker" {
			free = append(free, opt("Frenzy", `{"action": "attack", "description": "reckless attack"}`, "While raging, a Reckless Attack's first Strength hit each turn deals extra d6s equal to your Rage Damage."))
		}
	case "bard":
		bonus = append(bonus, opt("Bardic Inspiration", `{"action": "bardic_inspiration", "description": "on Aldren"}`, "Give an ally within 60 feet a Bardic Inspiration die (d6) to add to a failed D20 Test"+left("bardic_inspiration")+"."))
	case "cleric":
		if level >= 2 {
			actions = append(actions, opt("Turn Undead (Channel Divinity)", `{"action": "turn_undead", "target_ids": [-1]}`, "Each Undead you choose within 30 feet makes a Wisdom save or is Frightened and Incapacitated for 1 minute"+left("channel_divinity")+"."))
			actions = append(actions, opt("Divine Spark (Channel Divinity)", `{"action": "divine_spark", "target_id": -1}`, "Heal a creature within 30 feet, or deal Radiant or Necrotic damage (Constitution save for half): 1d8 + your Wisdom modifier"+left("channel_divinity")+"."))
		}
		if level >= 3 && sub == "life" {
			actions = append(actions, opt("Preserve Life (Channel Divinity)", `{"action": "preserve_life", "description": "Aldren 8, Millie 7"}`, fmt.Sprintf("Share %d Hit Points among Bloodied creatures within 30 feet (up to half each one's maximum)", 5*level)+left("channel_divinity")+"."))
		}
	case "druid":
		if level >= 2 {
			if wildShaped {
				bonus = append(bonus, opt("Revert Wild Shape", `{"action": "revert_wild_shape"}`, "Return to your normal form."))
			} else {
				bonus = append(bonus, opt("Wild Shape", `{"action": "wild_shape", "description": "wolf"}`, "Shape-shift into one of your known Beast forms (Temporary Hit Points equal to your Druid level)"+left("wild_shape")+"."))
			}
		}
		if level >= 3 && sub == "land" {
			actions = append(actions, opt("Land's Aid", `{"action": "lands_aid", "target_id": -1, "description": "heal Aldren"}`, "Spend a use of Wild Shape: 2d6 Necrotic damage (Constitution save for half) in a 10-foot sphere and heal one creature 2d6."))
		}
	case "fighter":
		bonus = append(bonus, opt("Second Wind", `{"action": "second_wind"}`, fmt.Sprintf("Regain 1d10 + %d Hit Points", level)+left("second_wind")+"; one use comes back on a Short Rest, all on a Long Rest."))
		if level >= 2 {
			free = append(free, opt("Action Surge", `{"action": "action_surge"}`, "Take one more action this turn (not Magic)"+left("action_surge")+"."))
		}
	case "monk":
		bonus = append(bonus, opt("Martial Arts strike", `{"action": "martial_arts", "target_id": -1}`, "After you take the Attack action, one Unarmed Strike as a Bonus Action."))
	case "paladin":
		bonus = append(bonus, opt("Lay on Hands", `{"action": "lay_on_hands", "description": "heal Aldren 5"}`, fmt.Sprintf("Restore Hit Points from a pool of %d, or spend 5 to end the Poisoned condition", 5*level)+left("lay_on_hands")+"."))
		if level >= 3 {
			bonus = append(bonus, opt("Divine Sense (Channel Divinity)", `{"action": "divine_sense"}`, "Sense Celestials, Fiends and Undead within 60 feet for 10 minutes"+left("channel_divinity")+"."))
			if sub == "devotion" {
				free = append(free, opt("Sacred Weapon (Channel Divinity)", `{"action": "sacred_weapon"}`, "When you attack: add your Charisma modifier to attack rolls with the weapon for 10 minutes; it sheds light"+left("channel_divinity")+"."))
			}
		}
	case "rogue":
		if level >= 3 {
			bonus = append(bonus, opt("Steady Aim", `{"action": "steady_aim"}`, "If you haven't moved this turn: Advantage on your next attack roll this turn; your Speed becomes 0 until the turn ends."))
		}
	case "sorcerer":
		bonus = append(bonus, opt("Innate Sorcery", `{"action": "innate_sorcery"}`, "For 1 minute: your Sorcerer spells' save DC +1 and Advantage on their attack rolls"+left("innate_sorcery")+"."))
		if level >= 2 {
			free = append(free, opt("Font of Magic", `POST /api/characters/flexible-casting {"character_id": N, "action": "create_slot", "slot_level": 1}`, "Turn Sorcery Points into a spell slot (a Bonus Action) or a slot into points"+left("sorcery_points")+"."))
		}
	}
	return
}

// knownActionWord: action words the server resolves (getActionResourceType's lists and the class actions). An unknown word is free
// narration for the GM; a spell's name is not (F245).
func knownActionWord(a string) bool {
	switch a {
	case "attack", "cast", "dash", "disengage", "dodge", "help", "hide", "ready", "search", "study", "influence", "extinguish", "use_item",
		"stabilize", "death_save", "grapple", "shove", "move", "stand", "other", "turn_undead", "preserve_life", "divine_spark", "lands_aid",
		"magical_cunning", "opportunity_attack", "spiritual_weapon", "flame_blade", "gaseous_form", "breath", "healing_touch", "fey_step", "fell_glare":
		return true
	}
	return false
}
