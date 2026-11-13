package main

// Every selection a character makes when it gains a level, by class, subclass and level (SRD 5.2.1). Used for the public level-up note.
// %d in a command is the character id.

import (
	"fmt"
	"strings"
)

type levelChoice struct {
	Level int
	Text  string
}

var classLevelChoices = map[string][]levelChoice{
	"barbarian": {
		{1, "Weapon Mastery: choose 2 kinds of weapon (POST /api/characters/weapon-masteries)"},
		{3, "Primal Knowledge: choose 1 more skill from the Barbarian list (POST /api/characters/%d/class-options {\"primal_knowledge\": ...})"},
		{4, "Weapon Mastery: choose 1 more kind of weapon (3 in all) (POST /api/characters/weapon-masteries)"},
		{10, "Weapon Mastery: choose 1 more kind of weapon (4 in all) (POST /api/characters/weapon-masteries)"},
	},
	"bard": {
		{2, "Expertise: choose 2 skills you are proficient in (POST /api/characters/%d/expertise)"},
		{9, "Expertise: choose 2 more skills (POST /api/characters/%d/expertise)"},
		{10, "Magical Secrets: your prepared spells can now come from the Bard, Cleric, Druid and Wizard lists (POST /api/characters/%d/spells)"},
	},
	"cleric": {
		{1, "Divine Order: choose Protector or Thaumaturge (POST /api/characters/%d/class-options)"},
		{7, "Blessed Strikes: choose Divine Strike or Potent Spellcasting (POST /api/characters/%d/class-options {\"blessed_strikes\": ...})"},
	},
	"druid": {
		{1, "Primal Order: choose Magician or Warden (POST /api/characters/%d/class-options)"},
		{2, "Wild Shape: you know 4 Beast forms (CR 1/4 or lower, no Fly Speed); name them to the GM"},
		{4, "Wild Shape: you know 6 Beast forms (CR 1/2 or lower, no Fly Speed); name the new ones to the GM"},
		{7, "Elemental Fury: choose Potent Spellcasting or Primal Strike (POST /api/characters/%d/class-options {\"elemental_fury\": ...})"},
		{8, "Wild Shape: you know 8 Beast forms (CR 1 or lower, Fly Speed allowed); name the new ones to the GM"},
	},
	"fighter": {
		{1, "Fighting Style: choose a Fighting Style feat (POST /api/characters/fighting-style)"},
		{1, "Weapon Mastery: choose 3 kinds of weapon (POST /api/characters/weapon-masteries)"},
		{4, "Weapon Mastery: choose 1 more kind of weapon (4 in all) (POST /api/characters/weapon-masteries)"},
		{10, "Weapon Mastery: choose 1 more kind of weapon (5 in all) (POST /api/characters/weapon-masteries)"},
		{16, "Weapon Mastery: choose 1 more kind of weapon (6 in all) (POST /api/characters/weapon-masteries)"},
	},
	"paladin": {
		{1, "Weapon Mastery: choose 2 kinds of weapon (POST /api/characters/weapon-masteries)"},
		{2, "Fighting Style: choose a Fighting Style feat or Blessed Warrior (POST /api/characters/fighting-style)"},
	},
	"ranger": {
		{1, "Weapon Mastery: choose 2 kinds of weapon (POST /api/characters/weapon-masteries)"},
		{2, "Deft Explorer: Expertise in 1 skill you are proficient in (POST /api/characters/%d/expertise) and 2 languages (POST /api/characters/%d/class-options {\"class_languages\": [...]})"},
		{2, "Fighting Style: choose a Fighting Style feat or Druidic Warrior (POST /api/characters/fighting-style)"},
		{9, "Expertise: choose 2 more skills (POST /api/characters/%d/expertise)"},
	},
	"rogue": {
		{1, "Expertise: choose 2 skills you are proficient in (POST /api/characters/%d/expertise)"},
		{1, "Thieves' Cant: you also learn 1 language of your choice (POST /api/characters/%d/class-options {\"class_languages\": [...]})"},
		{1, "Weapon Mastery: choose 2 kinds of weapon (POST /api/characters/weapon-masteries)"},
		{6, "Expertise: choose 2 more skills (POST /api/characters/%d/expertise)"},
	},
	"sorcerer": {
		{2, "Metamagic: choose 2 Metamagic options (POST /api/characters/metamagic)"},
		{10, "Metamagic: choose 2 more options (POST /api/characters/metamagic)"},
		{17, "Metamagic: choose 2 more options (POST /api/characters/metamagic)"},
	},
	"warlock": {
		{1, "Eldritch Invocations: choose 1 (POST /api/characters/invocations)"},
		{2, "Eldritch Invocations: choose 2 more (3 in all) (POST /api/characters/invocations)"},
		{5, "Eldritch Invocations: choose 2 more (5 in all) (POST /api/characters/invocations)"},
		{7, "Eldritch Invocations: choose 1 more (6 in all) (POST /api/characters/invocations)"},
		{9, "Eldritch Invocations: choose 1 more (7 in all) (POST /api/characters/invocations)"},
		{11, "Mystic Arcanum: choose a level 6 Warlock spell (POST /api/characters/mystic-arcanum)"},
		{12, "Eldritch Invocations: choose 1 more (8 in all) (POST /api/characters/invocations)"},
		{13, "Mystic Arcanum: choose a level 7 Warlock spell (POST /api/characters/mystic-arcanum)"},
		{15, "Mystic Arcanum: choose a level 8 Warlock spell (POST /api/characters/mystic-arcanum)"},
		{15, "Eldritch Invocations: choose 1 more (9 in all) (POST /api/characters/invocations)"},
		{17, "Mystic Arcanum: choose a level 9 Warlock spell (POST /api/characters/mystic-arcanum)"},
		{18, "Eldritch Invocations: choose 1 more (10 in all) (POST /api/characters/invocations)"},
	},
	"wizard": {
		{2, "Scholar: Expertise in Arcana, History, Investigation, Medicine, Nature or Religion (POST /api/characters/%d/expertise)"},
		{18, "Spell Mastery: choose a level 1 and a level 2 spell from your spellbook (POST /api/characters/spell-mastery)"},
		{20, "Signature Spells: choose two level 3 spells from your spellbook (POST /api/characters/signature-spells)"},
	},
}

var subclassLevelChoices = map[string][]levelChoice{
	"lore": {
		{3, "Bonus Proficiencies: 3 skills of your choice (POST /api/characters/%d/class-options {\"bonus_proficiencies\": [...]})"},
		{6, "Magical Discoveries: learn 2 spells from the Cleric, Druid or Wizard lists (POST /api/characters/%d/spells)"},
	},
	"land": {
		{3, "Circle of the Land Spells: choose a land type, Arid, Polar, Temperate or Tropical (POST /api/characters/%d/class-options {\"land_type\": ...}; again after each Long Rest)"},
	},
	"hunter": {
		{3, "Hunter's Prey: choose Colossus Slayer or Horde Breaker (POST /api/characters/subclass-choice, hunters_prey; you can switch after a Short or Long Rest)"},
		{7, "Defensive Tactics: choose Escape the Horde or Multiattack Defense (POST /api/characters/subclass-choice, defensive_tactics; you can switch after a Short or Long Rest)"},
	},
	"champion": {
		{7, "Additional Fighting Style: choose another Fighting Style feat (POST /api/characters/fighting-style)"},
	},
	"draconic": {
		{6, "Elemental Affinity: choose Acid, Cold, Fire, Lightning or Poison (POST /api/characters/%d/class-options {\"elemental_affinity\": ...})"},
	},
}

var casterClasses = map[string]string{
	"bard": "learn a cantrip or spell your new level allows and prepare spells", "cleric": "prepare the spells your new level allows",
	"druid": "prepare the spells your new level allows", "paladin": "prepare the spells your new level allows",
	"ranger": "prepare the spells your new level allows", "sorcerer": "learn the cantrips and spells your new level allows",
	"warlock": "learn the cantrips and spells your new level allows", "wizard": "add 2 spells to your spellbook and prepare spells",
}

// levelChoicesFor lists the selections for levels after `from` up to `to`, in level order, with the character id filled in.
func levelChoicesFor(charID int, class, subclass string, from, to int) []string {
	var out []string
	fill := func(t string) string {
		if strings.Contains(t, "%d") {
			return fmt.Sprintf(t, charID)
		}
		return t
	}
	sub := strings.ToLower(strings.ReplaceAll(subclass, " ", "-"))
	for _, k := range []string{"lore", "land", "hunter", "champion", "draconic"} {
		if strings.Contains(sub, k) {
			sub = k
		}
	}
	for lvl := from + 1; lvl <= to; lvl++ {
		if lvl == 3 && subclass == "" {
			out = append(out, fmt.Sprintf("level 3: choose a subclass (POST /api/characters/subclass with character_id %d)", charID))
		}
		for _, l := range asiLevelsFor(class) {
			if l == lvl && lvl != 19 {
				out = append(out, fmt.Sprintf("level %d: Ability Score Improvement: +2 to one score or +1 to two, or a feat instead (POST /api/characters/%d/asi or /feat)", lvl, charID))
			}
		}
		if lvl == 19 {
			out = append(out, fmt.Sprintf("level 19: Epic Boon: choose an Epic Boon feat or another feat (POST /api/characters/%d/feat)", charID))
		}
		for _, c := range classLevelChoices[strings.ToLower(class)] {
			if c.Level == lvl {
				out = append(out, fmt.Sprintf("level %d: %s", lvl, fill(c.Text)))
			}
		}
		for _, c := range subclassLevelChoices[sub] {
			if c.Level == lvl {
				out = append(out, fmt.Sprintf("level %d: %s", lvl, fill(c.Text)))
			}
		}
	}
	if what, ok := casterClasses[strings.ToLower(class)]; ok && to > from {
		out = append(out, fmt.Sprintf("spells: new spell slots; %s (POST /api/characters/%d/spells, /prepare)", what, charID))
	}
	return out
}
