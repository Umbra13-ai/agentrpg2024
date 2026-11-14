package main

// Checks on the fields sent to the one-request creation route, POST /api/characters (Nov 14 audit): an unknown, misplaced or
// inapplicable field was ignored without a word (and dry_run said "Everything checks out"), known_spells took any SRD spell of any
// level in any number, and every refusal came back as HTTP 200. Now each such field is refused with the reason, nothing is created,
// and the status is 4xx. The character builder sends its whole draft here (runCreation), so its refusals reach the review step too.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// createFields: the fields POST /api/characters reads, with the shape each takes. backstory_skip is the builder's (no backstory).
var createFields = map[string]string{
	"name": "text", "class": "text", "species": "text", "background": "text", "alignment": "text",
	"str": "a whole number", "dex": "a whole number", "con": "a whole number", "int": "a whole number", "wis": "a whole number", "cha": "a whole number",
	"ability_method":          `"standard_array"`,
	"ability_increases":       `an object of numbers, like {"wis": 2, "cha": 1}`,
	"skill_proficiencies":     `a list, like ["perception", "stealth"]`,
	"tool_proficiencies":      `a list, like ["lute", "drum", "flute"]`,
	"expertise":               `a list, like ["stealth", "perception"]`,
	"extra_languages":         `a list of two languages, like ["Dwarvish", "Giant"]`,
	"weapon_masteries":        `a list, like ["longsword", "longbow"]`,
	"known_spells":            "a list",
	"background_tool":         "text",
	"background_equipment":    `"a" or "b"`,
	"starting_equipment":      `"a", "b" or "c"`,
	"species_choices":         `an object of names, like {"lineage": "high_elf", "spellcasting_ability": "int", "skill": "perception"}`,
	"background_feat_choices": `an object, like {"list": "cleric", "cantrips": ["guidance", "light"], "spell": "bless", "spellcasting_ability": "wis"} or {"proficiencies": ["arcana", "history", "stealth"]}`,
	"species_feat_choices":    `an object, like {"proficiencies": ["arcana", "history", "stealth"]}`,
	"class_options":           `an object, like {"divine_order": "thaumaturge"}`,
	"draconic_ancestry":       "text",
	"backstory":               "text", "appearance": "text",
	"dry_run": "true or false", "backstory_skip": "true or false",
}

const spellsAfterCreation = "spells are chosen after creation: GET /api/characters/{id}/setup lists the ones your class can take and saves your picks"

// createFieldHints: names sent for something this request takes under another name, or not at all.
var createFieldHints = map[string]string{
	"languages": "use extra_languages (two Standard Languages besides Common)", "language": "use extra_languages (two Standard Languages besides Common)",
	"skills": "use skill_proficiencies (your class's skill choices)", "skill": "use skill_proficiencies (your class's skill choices)",
	"tools":  "use tool_proficiencies (a Bard's or Monk's tool choices) or background_tool (a Soldier's Gaming Set)",
	"tool":   "use tool_proficiencies (a Bard's or Monk's tool choices) or background_tool (a Soldier's Gaming Set)",
	"spells": spellsAfterCreation, "cantrips": spellsAfterCreation, "prepared_spells": spellsAfterCreation, "spellbook": spellsAfterCreation,
	"lineage": `put it in species_choices: {"lineage": ...}`, "legacy": `put it in species_choices: {"legacy": ...}`,
	"ancestry": `put it in species_choices: {"ancestry": ...}`, "size": `put it in species_choices: {"size": ...}`,
	"feat":                 `the Human's Origin feat goes in species_choices: {"feat": ...}; a background's feat comes with the background`,
	"feats":                "the Origin feat comes with the background (a Human also picks one in species_choices.feat); its picks go in background_feat_choices or species_feat_choices",
	"spellcasting_ability": "put it in species_choices (Elf, Gnome, Tiefling) or background_feat_choices (Magic Initiate)",
	"subclass":             "a subclass is chosen at level 3 (POST /api/characters/subclass)",
	"fighting_style":       "choose it after creation: GET /api/characters/{id}/setup guides it",
	"invocations":          "choose them after creation: GET /api/characters/{id}/setup guides it", "eldritch_invocations": "choose them after creation: GET /api/characters/{id}/setup guides it",
	"portrait": "add it after creation: POST /api/characters/{id}/portrait", "image_base64": "add it after creation: POST /api/characters/{id}/portrait",
	"race": "use species", "method": "use ability_method",
	"ability_scores": "send str, dex, con, int, wis and cha as six fields", "scores": "send str, dex, con, int, wis and cha as six fields",
	"abilities":      "send str, dex, con, int, wis and cha as six fields",
	"level":          "every character starts at level 1",
	"equipment":      `use starting_equipment (your class's option) or background_equipment ("a" or "b")`,
	"use_suggestion": "use_suggestion is for the character builder (POST /api/character-builder)",
}

var classOptionFields = map[string]string{"divine_order": "Cleric: protector or thaumaturge", "primal_order": "Druid: magician or warden"}

// normalizeCreateAliases puts obvious other names where they belong, in place: a top-level divine_order or primal_order goes in
// class_options, the species_choices keys are tidied ("Lineage" is lineage), a Dragonborn's draconic_ancestry fills
// species_choices.ancestry (it used to be dropped for species_choices). It returns the conflicts (two answers for one choice
// that disagree). The feat-choice aliases are handled by unknownCreateFields.
func normalizeCreateAliases(raw map[string]interface{}) []string {
	var out []string
	if sc, ok := raw["species_choices"].(map[string]interface{}); ok {
		tidy := map[string]interface{}{}
		for k, v := range sc {
			nk := strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(strings.TrimSpace(k)))
			switch nk { // the same other names as the builder takes (Nov 14 audit re-check: the two routes disagreed)
			case "ability", "spell_ability", "spellcasting":
				nk = "spellcasting_ability"
			case "subrace":
				nk = "lineage"
			}
			if old, dup := tidy[nk]; dup && fmt.Sprint(old) != fmt.Sprint(v) {
				out = append(out, fmt.Sprintf("species_choices.%s is sent twice with different values: send it once", nk))
			}
			tidy[nk] = v
		}
		raw["species_choices"] = tidy
	}
	for _, k := range []string{"divine_order", "primal_order"} {
		v, sent := raw[k]
		if !sent {
			continue
		}
		if raw["class_options"] == nil {
			raw["class_options"] = map[string]interface{}{}
		}
		co, ok := raw["class_options"].(map[string]interface{})
		if !ok {
			continue // class_options isn't an object: the type check says so
		}
		delete(raw, k)
		if emptyValue(v) {
			continue
		}
		if have := co[k]; !emptyValue(have) && !strings.EqualFold(strings.TrimSpace(fmt.Sprint(have)), strings.TrimSpace(fmt.Sprint(v))) {
			out = append(out, fmt.Sprintf("%s %q and class_options.%s %q disagree: send one", k, fmt.Sprint(v), k, fmt.Sprint(have)))
			continue
		}
		co[k] = v
	}
	species, _ := raw["species"].(string)
	if da, _ := raw["draconic_ancestry"].(string); strings.TrimSpace(da) != "" {
		if sp := game.GetSpecies(species); sp != nil && sp.Key == "dragonborn" {
			if raw["species_choices"] == nil {
				raw["species_choices"] = map[string]interface{}{}
			}
			if sc, ok := raw["species_choices"].(map[string]interface{}); ok {
				have, _ := sc["ancestry"].(string)
				switch {
				case strings.TrimSpace(have) == "":
					sc["ancestry"] = strings.ToLower(strings.TrimSpace(da)) // the field the docs name for it
				case !strings.EqualFold(strings.TrimSpace(have), strings.TrimSpace(da)):
					out = append(out, fmt.Sprintf("draconic_ancestry %q and species_choices.ancestry %q disagree: send one", da, have))
				}
			}
		}
	}
	if sp := game.GetSpecies(species); sp != nil && sp.Key == "tiefling" { // "lineage" is another name for the legacy
		if sc, ok := raw["species_choices"].(map[string]interface{}); ok {
			l, _ := sc["lineage"].(string)
			g, _ := sc["legacy"].(string)
			if strings.TrimSpace(l) != "" && strings.TrimSpace(g) != "" && !strings.EqualFold(strings.TrimSpace(l), strings.TrimSpace(g)) {
				out = append(out, fmt.Sprintf("species_choices.lineage %q and species_choices.legacy %q disagree: a Tiefling has one legacy", l, g))
			}
		}
	}
	return out
}

// unknownCreateFields: each field (or part of class_options, background_feat_choices, species_feat_choices) the request doesn't
// take, with where it goes.
func unknownCreateFields(raw map[string]interface{}) []string {
	out := featChoiceSubfields(raw) // the builder's check: aliases put under their real names, unknown parts reported
	var keys []string
	for k := range raw {
		if _, ok := createFields[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		msg := fmt.Sprintf("%q is not a field this request takes", k)
		if h, ok := createFieldHints[strings.ToLower(k)]; ok {
			msg += ": " + h
		} else if c := closestCreateField(k); c != "" {
			msg += fmt.Sprintf(" (did you mean %q?)", c)
		}
		out = append(out, msg)
	}
	if co, ok := raw["class_options"].(map[string]interface{}); ok {
		var bad []string
		for k := range co {
			if _, ok := classOptionFields[k]; !ok {
				bad = append(bad, k)
			}
		}
		sort.Strings(bad)
		for _, k := range bad {
			msg := fmt.Sprintf("%q is not a class option: class_options takes divine_order (Cleric) or primal_order (Druid)", "class_options."+k)
			if h, ok := createFieldHints[strings.ToLower(k)]; ok {
				msg += "; " + h
			} else if c := closestName(k, []string{"divine_order", "primal_order"}); c != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", "class_options."+c)
			}
			out = append(out, msg)
		}
	}
	return out
}

func closestCreateField(s string) string {
	var names []string
	for f := range createFields {
		names = append(names, f)
	}
	sort.Strings(names) // ties go the same way every time
	if c := closestName(s, names); c != "" {
		return c
	}
	if c := closestName(s, []string{"divine_order", "primal_order"}); c != "" {
		return "class_options." + c
	}
	return ""
}

// closestOf: the name nearest to a typo (edit distance under 4), or "".
func closestName(s string, names []string) string {
	best, bestD := "", 4
	for _, f := range names {
		if d := editDistance(strings.ToLower(s), f); d < bestD {
			best, bestD = f, d
		}
	}
	return best
}

// createTypeError says which field had the wrong kind of value.
func createTypeError(err error) string {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) && te.Field != "" {
		top := strings.Split(te.Field, ".")[0]
		if want, ok := createFields[top]; ok {
			where := ""
			if te.Field != top {
				where = " in " + te.Field
			}
			return fmt.Sprintf("%s must be %s; you sent %s%s.", top, want, article(te.Value), where)
		}
		return fmt.Sprintf("%s has the wrong kind of value (you sent %s).", te.Field, article(te.Value))
	}
	return "The body must be one JSON object with the creation fields."
}

func article(kind string) string {
	switch kind {
	case "array", "object":
		return "an " + kind
	case "":
		return "something else"
	}
	return "a " + kind
}

// aSpecies: "a Dwarf", "an Elf".
func aSpecies(name string) string {
	if name != "" && strings.ContainsRune("AEIOU", rune(name[0])) {
		return "an " + name
	}
	return "a " + name
}

func emptyValue(v interface{}) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []interface{}:
		return len(x) == 0
	case map[string]interface{}:
		for _, sv := range x {
			if !emptyValue(sv) {
				return false
			}
		}
		return true
	}
	return false
}

// inapplicableCreateFields: fields this character can't use (a lineage for a Dragonborn, Magic Initiate picks for a Soldier,
// a draconic_ancestry for an Elf, a tool choice for a background with a fixed tool), and known_spells, which creation doesn't take.
// Call it after the type check: the values are then the right kinds.
func inapplicableCreateFields(raw map[string]interface{}) []string {
	var out []string
	str := func(k string) string { s, _ := raw[k].(string); return strings.TrimSpace(s) }
	sub := func(k string) map[string]interface{} { m, _ := raw[k].(map[string]interface{}); return m }
	if !emptyValue(raw["known_spells"]) {
		out = append(out, "known_spells: "+spellsAfterCreation+" (it checks your class list, the spell levels and how many). Remove known_spells from the request")
	}
	if cls := strings.ToLower(str("class")); cls != "" && !emptyValue(raw["weapon_masteries"]) && masteryCount(cls, 1) == 0 {
		if _, known := srdClasses[cls]; known { // a Wizard's masteries were dropped without a word (Nov 14 audit re-check, S2-03)
			out = append(out, fmt.Sprintf("weapon_masteries: %s has no Weapon Mastery at level 1 (Barbarian, Fighter, Paladin, Ranger and Rogue choose it)", aSpecies(titleWords(cls))))
		}
	}
	if sp := game.GetSpecies(str("species")); sp != nil {
		sc := sub("species_choices")
		allowed := map[string]bool{}
		for _, n := range sp.Needs {
			allowed[n] = true
		}
		if sp.Key == "tiefling" {
			allowed["lineage"] = true // another name for the legacy
		}
		var bad []string
		for k, v := range sc {
			if !allowed[k] && !emptyValue(v) {
				bad = append(bad, k)
			}
		}
		sort.Strings(bad)
		for _, k := range bad {
			if len(sp.Needs) == 0 {
				out = append(out, fmt.Sprintf("species_choices.%s: %s makes no species choices", k, aSpecies(sp.Name)))
			} else {
				out = append(out, fmt.Sprintf("species_choices.%s: %s doesn't choose this; it chooses %s", k, aSpecies(sp.Name), strings.Join(sp.Needs, ", ")))
			}
		}
		if str("draconic_ancestry") != "" && sp.Key != "dragonborn" {
			out = append(out, fmt.Sprintf("draconic_ancestry: only a Dragonborn has one, not %s", aSpecies(sp.Name)))
		}
		if fc := sub("species_feat_choices"); !emptyValue(fc) {
			if sp.Key != "human" {
				out = append(out, fmt.Sprintf("species_feat_choices: only a Human picks an extra Origin feat (species_choices.feat), not %s", aSpecies(sp.Name)))
			} else if feat := strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(strings.TrimSpace(fmt.Sprint(sc["feat"])))); isOriginFeat(feat) {
				out = append(out, featChoicesNotUsed("species_feat_choices", fc, feat, "Your Human feat, "+titleWords(feat)+",")...)
			}
		}
	}
	if bg := game.GetBackground(str("background")); bg != nil {
		if fc := sub("background_feat_choices"); !emptyValue(fc) {
			out = append(out, featChoicesNotUsed("background_feat_choices", fc, bg.Feat, bg.Name+"'s feat, "+titleWords(bg.Feat)+",")...)
		}
		noQuote := strings.NewReplacer("'", "", "’", "")
		if t := str("background_tool"); t != "" && bg.ToolCategory == "" && !(bg.Tool != "" && (game.SameTool(t, bg.Tool) || game.SameTool(noQuote.Replace(t), noQuote.Replace(bg.Tool)))) {
			msg := fmt.Sprintf("background_tool: %s has no tool to choose", bg.Name)
			if bg.Tool != "" {
				msg = fmt.Sprintf("background_tool: %s gives %s and has no tool to choose", bg.Name, game.ToolDisplayName(bg.Tool))
			}
			out = append(out, msg)
		}
	}
	return out
}

// featChoicesNotUsed: the parts of a feat's picks that feat doesn't take.
func featChoicesNotUsed(field string, fc map[string]interface{}, feat, featWho string) []string {
	var set []string
	for k, v := range fc {
		if !emptyValue(v) {
			set = append(set, k)
		}
	}
	sort.Strings(set)
	var out []string
	switch feat {
	case "magic_initiate":
		for _, k := range set {
			if k == "proficiencies" {
				out = append(out, field+".proficiencies: Magic Initiate takes list, cantrips, spell and spellcasting_ability, not proficiencies")
			}
		}
	case "skilled":
		for _, k := range set {
			if k != "proficiencies" {
				out = append(out, fmt.Sprintf("%s.%s: Skilled takes only proficiencies (3 skills or tools)", field, k))
			}
		}
	default:
		out = append(out, fmt.Sprintf("%s: %s has no choices to make", field, featWho))
	}
	return out
}

// writeCreateOriginError sends a refused origin choice from the creation route with a 400 status.
func writeCreateOriginError(w http.ResponseWriter, e *originError) {
	resp := map[string]interface{}{"error": e.Code, "message": e.Message}
	if len(e.Options) > 0 {
		resp["valid_options"] = e.Options
	}
	writeJSONStatus(w, http.StatusBadRequest, resp)
}
