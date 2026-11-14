package main

// Skill proficiency choices for each class, from the 2024 SRD 5.2.1 "Core ...
// Traits" tables. The classes table in the database had 2 choices for every
// class and no skill lists. A Bard may choose any 3 skills, so its list is every SRD skill (an empty list let anything
// through, "flying" included: Nov 14 audit).

import (
	"log"
	"strings"
)

var classSkillChoices2024 = map[string]struct {
	Count  int
	Skills []string
}{
	"barbarian": {2, []string{"animal handling", "athletics", "intimidation", "nature", "perception", "survival"}},
	"bard":      {3, allSRDSkills},
	"cleric":    {2, []string{"history", "insight", "medicine", "persuasion", "religion"}},
	"druid":     {2, []string{"animal handling", "arcana", "insight", "medicine", "nature", "perception", "religion", "survival"}},
	"fighter":   {2, []string{"acrobatics", "animal handling", "athletics", "history", "insight", "intimidation", "persuasion", "perception", "survival"}},
	"monk":      {2, []string{"acrobatics", "athletics", "history", "insight", "religion", "stealth"}},
	"paladin":   {2, []string{"athletics", "insight", "intimidation", "medicine", "persuasion", "religion"}},
	"ranger":    {3, []string{"animal handling", "athletics", "insight", "investigation", "nature", "perception", "stealth", "survival"}},
	"rogue":     {4, []string{"acrobatics", "athletics", "deception", "insight", "intimidation", "investigation", "perception", "persuasion", "sleight of hand", "stealth"}},
	"sorcerer":  {2, []string{"arcana", "deception", "insight", "intimidation", "persuasion", "religion"}},
	"warlock":   {2, []string{"arcana", "deception", "history", "intimidation", "investigation", "nature", "religion"}},
	"wizard":    {2, []string{"arcana", "history", "insight", "investigation", "medicine", "nature", "religion"}},
}

// allSRDSkills: the 18 skills of the SRD 5.2.1 Skills table.
var allSRDSkills = []string{"acrobatics", "animal handling", "arcana", "athletics", "deception", "history", "insight", "intimidation",
	"investigation", "medicine", "nature", "perception", "performance", "persuasion", "religion", "sleight of hand", "stealth", "survival"}

// classCoreTraits2024: Hit Point Die, primary ability, saving throw proficiencies and spellcasting ability (SRD 5.2.1 "Core ... Traits").
var classCoreTraits2024 = []struct {
	Slug, Name string
	HitDie     int
	Primary    string
	Saves      string
	Casting    string
}{
	{"barbarian", "Barbarian", 12, "Strength", "STR, CON", ""},
	{"bard", "Bard", 8, "Charisma", "DEX, CHA", "CHA"},
	{"cleric", "Cleric", 8, "Wisdom", "WIS, CHA", "WIS"},
	{"druid", "Druid", 8, "Wisdom", "INT, WIS", "WIS"},
	{"fighter", "Fighter", 10, "Strength or Dexterity", "STR, CON", ""},
	{"monk", "Monk", 8, "Dexterity and Wisdom", "STR, DEX", ""},
	{"paladin", "Paladin", 10, "Strength and Charisma", "WIS, CHA", "CHA"},
	{"ranger", "Ranger", 10, "Dexterity and Wisdom", "STR, DEX", "WIS"},
	{"rogue", "Rogue", 8, "Dexterity", "DEX, INT", ""},
	{"sorcerer", "Sorcerer", 6, "Charisma", "CON, CHA", "CHA"},
	{"warlock", "Warlock", 8, "Charisma", "WIS, CHA", "CHA"},
	{"wizard", "Wizard", 6, "Intelligence", "INT, WIS", "INT"},
}

// seedClasses2024 writes the 12 SRD 5.2.1 classes into the classes table (and their skill choices).
func seedClasses2024() {
	for _, c := range classCoreTraits2024 {
		if _, err := db.Exec(`INSERT INTO classes (slug, name, hit_die, primary_ability, saving_throws, spellcasting_ability)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name, hit_die = EXCLUDED.hit_die, primary_ability = EXCLUDED.primary_ability,
				saving_throws = EXCLUDED.saving_throws, spellcasting_ability = EXCLUDED.spellcasting_ability`,
			c.Slug, c.Name, c.HitDie, c.Primary, c.Saves, c.Casting); err != nil {
			log.Printf("class %s: %v", c.Slug, err)
		}
	}
	applyClassSkillChoices2024()
}

// applyClassSkillChoices2024 writes the 2024 skill choices into the classes table.
func applyClassSkillChoices2024() {
	for class, c := range classSkillChoices2024 {
		if _, err := db.Exec(`UPDATE classes SET num_skill_choices = $1, skill_choices = $2 WHERE slug = $3`,
			c.Count, strings.Join(c.Skills, ", "), class); err != nil {
			log.Printf("skill choices for %s: %v", class, err)
		}
	}
}
