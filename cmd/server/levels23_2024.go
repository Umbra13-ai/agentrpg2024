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
	"net/http"
	"strings"
)

var barbarianSkills = []string{"animal handling", "athletics", "intimidation", "nature", "perception", "survival"}

var allSkills = []string{"acrobatics", "animal handling", "arcana", "athletics", "deception", "history", "insight", "intimidation",
	"investigation", "medicine", "nature", "perception", "performance", "persuasion", "religion", "sleight of hand", "stealth", "survival"}

// skillKey: "sleight-of-hand", "Sleight_of_Hand" and "sleight of hand" are the same skill.
func skillKey(s string) string { return normSkill(strings.ReplaceAll(s, "-", " ")) }

func heldSkills(charID int) (map[string]bool, string) {
	var csv string
	db.QueryRow(`SELECT COALESCE(skill_proficiencies,'') FROM characters WHERE id = $1`, charID).Scan(&csv)
	held := map[string]bool{}
	for _, s := range strings.Split(csv, ",") {
		if s = skillKey(s); s != "" {
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
	for _, s := range allSkills {
		if !held[s] {
			open = append(open, s)
		}
	}
	if len(bonus) != 3 {
		return nil, "invalid_count", fmt.Sprintf("Bonus Proficiencies: three skills of your choice (you sent %d). Skills you aren't proficient in: %s. Nothing was changed.", len(bonus), strings.Join(open, ", "))
	}
	var add []string
	for _, b := range bonus {
		s := skillKey(b)
		if !containsStr(allSkills, s) {
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

// applySkillChoice handles primal_knowledge and bonus_proficiencies; false when neither was sent.
func applySkillChoice(w http.ResponseWriter, charID int, primal string, bonus []string) bool {
	if primal == "" && len(bonus) == 0 {
		return false
	}
	class, sub, _ := subclassOf(charID)
	levels := classLevelsOf(charID)
	held, csv := heldSkills(charID)
	choices := subclassChoicesOf(charID)
	fail := func(code, msg string) bool {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": code, "message": msg})
		return true
	}
	var add []string
	key := ""
	if primal != "" {
		barbLevel := levels["barbarian"]
		if barbLevel == 0 && strings.EqualFold(class, "barbarian") {
			db.QueryRow(`SELECT COALESCE(level,1) FROM characters WHERE id = $1`, charID).Scan(&barbLevel)
		}
		if barbLevel < 3 {
			return fail("not_available", "Primal Knowledge is a Barbarian level 3 feature.")
		}
		if choices["primal_knowledge"] != "" {
			return fail("already_chosen", "Primal Knowledge was chosen: "+choices["primal_knowledge"]+".")
		}
		s := skillKey(primal)
		if !containsStr(barbarianSkills, s) {
			return fail("invalid_skill", "Primal Knowledge: one skill from the Barbarian list: "+strings.Join(barbarianSkills, ", ")+".")
		}
		if held[s] {
			return fail("already_proficient", "You are already proficient in "+s+"; choose another.")
		}
		add, key = []string{s}, "primal_knowledge"
	} else {
		var l int
		db.QueryRow(`SELECT COALESCE(level,1) FROM characters WHERE id = $1`, charID).Scan(&l)
		bardLevel := levels["bard"]
		if bardLevel == 0 && strings.EqualFold(class, "bard") {
			bardLevel = l
		}
		if bardLevel < 3 || !strings.Contains(strings.ToLower(sub), "lore") {
			return fail("not_available", "Bonus Proficiencies is a College of Lore feature (Bard level 3).")
		}
		if choices["bonus_proficiencies"] != "" {
			return fail("already_chosen", "Bonus Proficiencies were chosen: "+choices["bonus_proficiencies"]+".")
		}
		var code, msg string
		if add, code, msg = checkBonusProficiencies(held, bonus); code != "" {
			return fail(code, msg)
		}
		key = "bonus_proficiencies"
	}
	newCSV := csv
	for _, s := range add {
		if strings.TrimSpace(newCSV) == "" {
			newCSV = s
		} else {
			newCSV += ", " + s
		}
	}
	choices[key] = strings.Join(add, ", ")
	b, _ := json.Marshal(choices)
	db.Exec(`UPDATE characters SET skill_proficiencies = $1, subclass_choices = $2 WHERE id = $3`, newCSV, string(b), charID)
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, key: add, "skill_proficiencies": newCSV,
		"message": fmt.Sprintf("You are now proficient in %s.", joinNames(add))})
	return true
}

// levels23OpenChoices: the level 2-3 choices classOpenChoices didn't list (land type, Hunter's Prey, Primal Knowledge, Bonus
// Proficiencies, spells to prepare after a level-up).
func levels23OpenChoices(charID int) []string {
	class, sub, level := subclassOf(charID)
	levels := classLevelsOf(charID)
	if len(levels) == 0 {
		levels = map[string]int{strings.ToLower(class): level}
	}
	choices := subclassChoicesOf(charID)
	s := strings.ToLower(sub)
	var out []string
	if s == "land" && levels["druid"] >= 3 && choices["circle_land"] == "" {
		out = append(out, fmt.Sprintf("Circle of the Land: choose a land type, arid, polar, temperate or tropical (POST /api/characters/%d/class-options {\"land_type\": ...})", charID))
	}
	if s == "hunter" && levels["ranger"] >= 3 && choices["hunters_prey"] == "" {
		out = append(out, fmt.Sprintf("Hunter's Prey: choose colossus_slayer or horde_breaker (POST /api/characters/subclass-choice {\"character_id\": %d, \"feature\": \"hunters_prey\", \"choice\": ...})", charID))
	}
	if s == "hunter" && levels["ranger"] >= 7 && choices["defensive_tactics"] == "" {
		out = append(out, fmt.Sprintf("Defensive Tactics: choose escape_the_horde or multiattack_defense (POST /api/characters/subclass-choice {\"character_id\": %d, \"feature\": \"defensive_tactics\", \"choice\": ...})", charID))
	}
	if levels["barbarian"] >= 3 && choices["primal_knowledge"] == "" {
		out = append(out, fmt.Sprintf("Primal Knowledge: one more skill from the Barbarian list (POST /api/characters/%d/class-options {\"primal_knowledge\": \"perception\"})", charID))
	}
	if strings.Contains(s, "lore") && levels["bard"] >= 3 && choices["bonus_proficiencies"] == "" {
		out = append(out, fmt.Sprintf("Bonus Proficiencies: three skills of your choice (POST /api/characters/%d/class-options {\"bonus_proficiencies\": [...]})", charID))
	}
	return out
}
