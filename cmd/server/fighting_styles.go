package main

import (
	"encoding/json"
	"strings"
)

// FightingStyle represents a combat specialization for Fighters, Paladins, and Rangers
type FightingStyle struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Classes     []string `json:"classes"`  // Which classes can choose this style
	Mechanic    string   `json:"mechanic"` // Mechanical effect key
}

// The Fighting Style feats of the SRD 5.2.1 (a Fighting Style feature gives one of these feats), plus the two class options that
// replace the feat: Blessed Warrior (Paladin) and Druidic Warrior (Ranger).
var fightingStyles = map[string]FightingStyle{
	"archery": {
		Slug:        "archery",
		Name:        "Archery",
		Description: "You gain a +2 bonus to attack rolls you make with Ranged weapons.",
		Classes:     []string{"fighter", "paladin", "ranger"},
		Mechanic:    "ranged_attack_+2",
	},
	"defense": {
		Slug:        "defense",
		Name:        "Defense",
		Description: "While you are wearing Light, Medium, or Heavy armor, you gain a +1 bonus to Armor Class.",
		Classes:     []string{"fighter", "paladin", "ranger"},
		Mechanic:    "armor_ac_+1",
	},
	"great_weapon_fighting": {
		Slug:        "great_weapon_fighting",
		Name:        "Great Weapon Fighting",
		Description: "When you roll damage for an attack you make with a Melee weapon that you are holding with two hands, you can treat any 1 or 2 on a damage die as a 3. The weapon must have the Two-Handed or Versatile property to gain this benefit.",
		Classes:     []string{"fighter", "paladin", "ranger"},
		Mechanic:    "treat_1_2_as_3",
	},
	"two_weapon_fighting": {
		Slug:        "two_weapon_fighting",
		Name:        "Two-Weapon Fighting",
		Description: "When you make an extra attack as a result of using a weapon that has the Light property, you can add your ability modifier to the damage of that attack if you are not already adding it to the damage.",
		Classes:     []string{"fighter", "paladin", "ranger"},
		Mechanic:    "offhand_ability_mod",
	},
	"blessed_warrior": {
		Slug:        "blessed_warrior",
		Name:        "Blessed Warrior",
		Description: "You learn two Cleric cantrips of your choice (send them as cantrips). Guidance and Sacred Flame are recommended. The chosen cantrips count as Paladin spells for you, and Charisma is your spellcasting ability for them. Whenever you gain a Paladin level, you can replace one of these cantrips with another Cleric cantrip.",
		Classes:     []string{"paladin"},
		Mechanic:    "cantrips_cleric",
	},
	"druidic_warrior": {
		Slug:        "druidic_warrior",
		Name:        "Druidic Warrior",
		Description: "You learn two Druid cantrips of your choice (send them as cantrips). Guidance and Starry Wisp are recommended. The chosen cantrips count as Ranger spells for you, and Wisdom is your spellcasting ability for them. Whenever you gain a Ranger level, you can replace one of these cantrips with another Druid cantrip.",
		Classes:     []string{"ranger"},
		Mechanic:    "cantrips_druid",
	},
}

// getFightingStyles returns the fighting styles a character has chosen
func getFightingStyles(charID int) []string {
	var stylesJSON []byte
	err := db.QueryRow("SELECT COALESCE(fighting_styles, '[]') FROM characters WHERE id = $1", charID).Scan(&stylesJSON)
	if err != nil {
		return []string{}
	}
	var styles []string
	json.Unmarshal(stylesJSON, &styles)
	return styles
}

func hasFightingStyle(charID int, slug string) bool {
	styles := getFightingStyles(charID)
	for _, s := range styles {
		if s == slug {
			return true
		}
	}
	return hasSpecificFeat(charID, slug) // a Fighting Style feat taken as a feat
}

// getMaxFightingStyles returns how many fighting styles a character can have
func getMaxFightingStyles(class string, level int, subclass string) int {
	class = strings.ToLower(class)
	switch class {
	case "fighter":
		if level >= 1 {
			// 2024 Champion: Additional Fighting Style at level 7
			if subclass == "champion" && level >= 7 {
				return 2
			}
			return 1
		}
	case "paladin":
		if level >= 2 {
			return 1
		}
	case "ranger":
		if level >= 2 {
			return 1
		}
	}
	return 0
}

// getAvailableFightingStyles returns styles available to a character's class
func getAvailableFightingStyles(class string) []FightingStyle {
	class = strings.ToLower(class)
	available := []FightingStyle{}
	for _, style := range fightingStyles {
		for _, c := range style.Classes {
			if c == class {
				available = append(available, style)
				break
			}
		}
	}
	return available
}
