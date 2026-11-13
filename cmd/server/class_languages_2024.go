package main

// Languages a class feature gives (Nov 13): Deft Explorer (Ranger 2: two languages) and Thieves' Cant (Rogue 1: Thieves' Cant and one
// other language). Before, both said "tell the GM" and nothing recorded them. The choice comes from the SRD's language tables.
// POST /api/characters/{id}/class-options {"class_languages": ["Elvish", "Giant"]}.

import (
	"encoding/json"
	"fmt"
	"strings"
)

var rareLanguages = []string{"Abyssal", "Celestial", "Deep Speech", "Infernal", "Primordial", "Sylvan", "Undercommon"} // Druidic and Thieves' Cant are class languages

type classLangDef struct {
	class, key, feature, text string
	level, count              int
}

var classLangDefs = []classLangDef{
	{"ranger", "deft_explorer_languages", "Deft Explorer", "Languages. You know two languages of your choice from the language tables in “Character Creation.”", 2, 2},
	{"rogue", "thieves_cant_language", "Thieves’ Cant", "You picked up various languages in the communities where you plied your roguish talents. You know Thieves’ Cant and one other language of your choice, which you choose from the language tables in “Character Creation.”", 1, 1},
}

func languagesOf(charID int) []string {
	var csv string
	db.QueryRow(`SELECT COALESCE(language_proficiencies,'') FROM characters WHERE id = $1`, charID).Scan(&csv)
	var out []string
	for _, l := range strings.Split(csv, ",") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func knowsLanguage(langs []string, l string) bool {
	for _, x := range langs {
		if strings.EqualFold(x, l) {
			return true
		}
	}
	return false
}

// pendingClassLanguage: the class language choice still open (nil when none).
func pendingClassLanguage(charID int) *classLangDef {
	levels := classLevelsOrSingle(charID)
	choices := subclassChoicesOf(charID)
	for i, d := range classLangDefs {
		if levels[d.class] >= d.level && choices[d.key] == "" {
			return &classLangDefs[i]
		}
	}
	return nil
}

func classLanguageOptions(charID int) []string {
	known := languagesOf(charID)
	var out []string
	for _, l := range append(append([]string{}, standardLanguages...), rareLanguages...) {
		if !knowsLanguage(known, l) && !strings.EqualFold(l, "common") {
			out = append(out, l)
		}
	}
	return out
}

// classLanguageStep: the setup step for it.
func classLanguageStep(charID int) *setupStep {
	d := pendingClassLanguage(charID)
	if d == nil {
		return nil
	}
	opts := classLanguageOptions(charID)
	pick := opts
	if len(pick) > d.count {
		pick = pick[:d.count]
	}
	st := setupStep{Key: "class_languages", Title: fmt.Sprintf("Choose %d language(s) for %s", d.count, d.feature), Why: d.feature + ". " + d.text,
		Choices: map[string]interface{}{"choose": d.count, "you_know": languagesOf(charID), "standard_languages": standardLanguages, "rare_languages": rareLanguages, "options": opts},
		Request: req("POST", fmt.Sprintf("/api/characters/%d/class-options", charID), map[string]interface{}{"class_languages": pick})}
	return &st
}

// setClassLanguages checks and records the languages ("" on success).
func setClassLanguages(charID int, langs []string) string {
	d := pendingClassLanguage(charID)
	if d == nil {
		return "You have no class language to choose now."
	}
	if len(langs) != d.count {
		return fmt.Sprintf("%s gives %d language(s); you sent %d.", d.feature, d.count, len(langs))
	}
	opts := classLanguageOptions(charID)
	known := languagesOf(charID)
	var add []string
	for _, l := range langs {
		ok := ""
		for _, o := range opts {
			if strings.EqualFold(o, strings.TrimSpace(l)) {
				ok = o
			}
		}
		if ok == "" {
			if knowsLanguage(known, l) {
				return fmt.Sprintf("You already know %s.", l)
			}
			return fmt.Sprintf("%q is not on the SRD's language tables. Choose from: %s.", l, strings.Join(opts, ", "))
		}
		if containsStr(add, ok) {
			return "you chose " + ok + " twice"
		}
		add = append(add, ok)
	}
	if d.class == "rogue" && !knowsLanguage(known, "Thieves' Cant") && !knowsLanguage(known, "Thieves’ Cant") {
		add = append(add, "Thieves' Cant")
	}
	all := append(known, add...)
	db.Exec(`UPDATE characters SET language_proficiencies = $1 WHERE id = $2`, strings.Join(all, ", "), charID)
	choices := subclassChoicesOf(charID)
	choices[d.key] = strings.Join(add, ", ")
	b, _ := json.Marshal(choices)
	db.Exec(`UPDATE characters SET subclass_choices = $1 WHERE id = $2`, string(b), charID)
	return ""
}
