package main

// Levels 2 and 3 choices a player makes for themselves (LOW-LEVEL-PLAN phase C, Nov 10). Two level 3 features said "tell the GM",
// so an AI player could not make them: Primal Knowledge (Barbarian 3: one more skill from the Barbarian list) and Bonus
// Proficiencies (College of Lore 3: three skills of your choice). Both go through POST /api/characters/{id}/class-options:
//	{"primal_knowledge": "perception"}
//	{"bonus_proficiencies": ["stealth", "arcana", "history"]}
// Each is made once, adds the skill proficiencies, and is kept in subclass_choices.

import (
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"strings"
)

func heldSkills(charID int) (map[string]bool, string) {
	var csv string
	db.QueryRow(`SELECT COALESCE(skill_proficiencies,'') FROM characters WHERE id = $1`, charID).Scan(&csv)
	held := map[string]bool{}
	for _, s := range strings.Split(csv, ",") {
		if s = game.SkillName(s); s != "" {
			held[s] = true
		}
	}
	return held, csv
}

func subclassChoicesOf(charID int) map[string]string {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(subclass_choices,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	m := map[string]string{}
	json.Unmarshal(raw, &m)
	return m
}

// checkBonusProficiencies checks College of Lore's Bonus Proficiencies (three different skills the character isn't proficient in):
// the skills to add, or an error code and message. Both routes use it (class-options bonus_proficiencies and the subclass request's
// bonus_skills).
func checkBonusProficiencies(held map[string]bool, bonus []string) ([]string, string, string) {
	var open []string
	for _, s := range game.SkillNames() {
		if !held[s] {
			open = append(open, s)
		}
	}
	if len(bonus) != 3 {
		return nil, "invalid_count", fmt.Sprintf("Bonus Proficiencies: three skills of your choice (you sent %d). Skills you aren't proficient in: %s. Nothing was changed.", len(bonus), strings.Join(open, ", "))
	}
	var add []string
	for _, b := range bonus {
		s := game.SkillName(b)
		if !containsStr(game.SkillNames(), s) {
			return nil, "invalid_skill", fmt.Sprintf("%q isn't a skill. Skills you aren't proficient in: %s. Nothing was changed.", b, strings.Join(open, ", "))
		}
		if containsStr(add, s) { // the same skill twice isn't "already proficient" (setup checker, Nov 14)
			return nil, "duplicate_skill", "You chose " + s + " twice: choose three different skills. Nothing was changed."
		}
		if held[s] {
			return nil, "already_proficient", fmt.Sprintf("You are already proficient in %s; choose another. Skills you aren't proficient in: %s. Nothing was changed.", s, strings.Join(open, ", "))
		}
		add = append(add, s)
	}
	return add, "", ""
}
