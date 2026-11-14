package main

// Builder checks from an AI player's test of the live server (Nov 13): an unrecognized field was saved silently (a typo looked like a
// successful choice), invalid answers stayed in the draft while flagged, the review step had no summary of the character, and the
// one-character-per-campaign rule came as a surprise after creation.

import (
	"fmt"
	"regexp"

	"github.com/agentrpg/agentrpg/game"
	"sort"
	"strings"
)

var paragraphSplit = regexp.MustCompile(`\n[ \t]*\n`)

// builderFields: every field the builder reads or the creation request takes.
var builderFields = map[string]bool{
	"name": true, "class": true, "background": true, "species": true, "alignment": true, "backstory": true, "appearance": true, "backstory_skip": true,
	"str": true, "dex": true, "con": true, "int": true, "wis": true, "cha": true, "ability_method": true, "ability_increases": true,
	"skill_proficiencies": true, "tool_proficiencies": true, "expertise": true, "weapon_masteries": true, "class_options": true,
	"starting_equipment": true, "background_tool": true, "background_equipment": true, "background_feat_choices": true,
	"species_choices": true, "species_feat_choices": true, "extra_languages": true, "known_spells": true, "draconic_ancestry": true,
	"divine_order": true, "primal_order": true, "use_suggestion": true,
}

// closestField: the known field nearest to a typo (edit distance), or "".
func closestField(s string) string {
	best, bestD := "", 4
	for f := range builderFields {
		if d := editDistance(strings.ToLower(s), f); d < bestD {
			best, bestD = f, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			c := 1
			if ra[i-1] == rb[j-1] {
				c = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// unknownBuilderFields: the fields sent that the builder doesn't know, each with a hint.
func unknownBuilderFields(upd map[string]interface{}) []string {
	var out []string
	out = append(out, featChoiceSubfields(upd)...)
	for k := range upd {
		if !builderFields[k] {
			msg := fmt.Sprintf("%q is not a field the builder knows", k)
			if c := closestField(k); c != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", c)
			}
			out = append(out, msg)
		}
	}
	sort.Strings(out)
	return out
}

// rejectedFields: the fields just sent whose answer has a problem; those answers are not saved (the earlier answer stays).
func rejectedFields(steps []builderStep, sent map[string]interface{}) []map[string]interface{} {
	var out []map[string]interface{}
	seen := map[string]bool{}
	for _, st := range steps {
		for _, c := range st.Choices {
			prob, _ := c["problem"].(string)
			if prob == "" {
				continue
			}
			f, _ := c["field"].(string)
			for _, part := range strings.Split(f, ", ") {
				top := strings.Split(part, ".")[0]
				if _, was := sent[top]; was && !seen[top] {
					seen[top] = true
					out = append(out, map[string]interface{}{"field": top, "you_sent": sent[top], "problem": prob})
				}
			}
		}
		for _, p := range st.Problems { // Step 5's Standard Array check is a step problem
			if st.Key == "ability_scores" && strings.Contains(p, "Standard Array") {
				for _, k := range abilityKeys6 {
					if _, was := sent[k]; was && !seen[k] {
						seen[k] = true
						out = append(out, map[string]interface{}{"field": k, "you_sent": sent[k], "problem": p})
					}
				}
			}
		}
	}
	return out
}

// draftSummary: the character the draft makes, in plain lines, for the review step.
func draftSummary(body map[string]interface{}, wc map[string]interface{}) []string {
	var out []string
	add := func(label string, v interface{}) {
		s := strings.TrimSpace(fmt.Sprint(v))
		if s != "" && s != "<nil>" && s != "[]" && s != "map[]" {
			out = append(out, label+": "+s)
		}
	}
	add("Name", wc["name"])
	sp := fmt.Sprint(wc["species"])
	if sc, ok := body["species_choices"].(map[string]interface{}); ok && len(sc) > 0 {
		var parts []string
		for k, v := range sc {
			parts = append(parts, fmt.Sprintf("%s %v", strings.ReplaceAll(k, "_", " "), v))
		}
		sort.Strings(parts)
		sp += " (" + strings.Join(parts, ", ") + ")"
	}
	add("Class", titleCase(fmt.Sprint(wc["class"])))
	add("Background", titleCase(fmt.Sprint(wc["background"])))
	add("Species", titleCase(sp))
	add("Alignment", wc["alignment"])
	// final scores: the Standard Array plus the background's increases
	inc := map[string]int{}
	if m, ok := body["ability_increases"].(map[string]interface{}); ok {
		for k, v := range m {
			inc[strings.ToLower(k)] = bint(map[string]interface{}{"v": v}, "v")
		}
	}
	var scores []string
	for _, k := range abilityKeys6 {
		base := bint(body, k)
		s := fmt.Sprintf("%s %d", strings.ToUpper(k[:1])+k[1:], base+inc[k])
		if inc[k] > 0 {
			s += fmt.Sprintf(" (%d +%d)", base, inc[k])
		}
		scores = append(scores, s)
	}
	out = append(out, "Ability scores: "+strings.Join(scores, ", "))
	add("Hit Points", wc["hp"])
	add("Skills", wc["skills"])
	add("Expertise", wc["expertise"])
	add("Tools", wc["tools"])
	add("Languages", wc["languages"])
	add("Weapon Mastery", wc["weapon_masteries"])
	if bg := game.GetBackground(strings.ToLower(fmt.Sprint(body["background"]))); bg != nil {
		feat := titleCase(strings.ReplaceAll(bg.Feat, "_", " "))
		if fc, ok := body["background_feat_choices"].(map[string]interface{}); ok && len(fc) > 0 {
			feat += fmt.Sprintf(" (%v list: cantrips %v, spell %v, cast with %v)", fc["list"], fc["cantrips"], fc["spell"], fc["spellcasting_ability"])
		}
		add("Origin feat (background)", feat)
	}
	if sc, ok := body["species_choices"].(map[string]interface{}); ok && sc["feat"] != nil {
		feat := titleCase(strings.ReplaceAll(fmt.Sprint(sc["feat"]), "_", " "))
		if fc, ok := body["species_feat_choices"].(map[string]interface{}); ok && len(fc) > 0 {
			feat += fmt.Sprintf(" (%v)", fc)
		}
		add("Origin feat (Human)", feat)
	}
	if o, ok := body["class_options"].(map[string]interface{}); ok {
		for k, v := range o {
			add(titleCase(strings.ReplaceAll(k, "_", " ")), v)
		}
	}
	add("Starting equipment", wc["starting_equipment"])
	if be, _ := body["background_equipment"].(string); be != "" {
		add("Background equipment", map[string]string{"a": "the background's package", "b": "50 GP instead"}[be])
	}
	if s, _ := body["backstory"].(string); s != "" {
		add("Backstory", fmt.Sprintf("%d paragraph(s) written", len(paragraphSplit.Split(strings.TrimSpace(s), -1))))
	}
	return out
}

// campaignSeats: the campaigns where this player already has a character (one character per player in a campaign).
func campaignSeats(agentID int) []string {
	rows, err := db.Query(`SELECT c.name, l.name FROM characters c JOIN lobbies l ON l.id = c.lobby_id WHERE c.agent_id = $1 AND COALESCE(l.status,'') <> 'completed'`, agentID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var cn, ln string
		rows.Scan(&cn, &ln)
		out = append(out, fmt.Sprintf("%s in %s", cn, ln))
	}
	return out
}

// featChoiceAliases: names an answer may use for the Origin feat's choices (some are the names the options were once listed under).
var featChoiceAliases = map[string]string{
	"cantrips_choose_2": "cantrips", "level_1_spell_choose_1": "spell", "level_1_spell": "spell", "level1_spell": "spell",
	"spellcasting_ability_choose_1": "spellcasting_ability", "ability": "spellcasting_ability", "list_choose_1": "list",
	"spell_list": "list", "skills": "proficiencies",
}

var featChoiceFields = map[string]bool{"list": true, "cantrips": true, "spell": true, "spellcasting_ability": true, "proficiencies": true}

// featChoiceSubfields puts the aliases in background_feat_choices and species_feat_choices under their real names, and reports a
// part the builder doesn't know (Nov 14 tester: picks sent as cantrips_choose_2 and level_1_spell_choose_1 were kept but ignored,
// the step stayed unfinished and the suggestion replaced them, with no error).
func featChoiceSubfields(upd map[string]interface{}) []string {
	var out []string
	for _, field := range []string{"background_feat_choices", "species_feat_choices"} {
		fc, ok := upd[field].(map[string]interface{})
		if !ok {
			continue
		}
		for k, v := range fc {
			if real, ok := featChoiceAliases[k]; ok {
				delete(fc, k)
				if real == "spell" { // a list of one spell is the spell
					if l, ok := v.([]interface{}); ok && len(l) == 1 {
						v = l[0]
					}
				}
				fc[real] = v
			}
		}
		if l, ok := fc["spells"].([]interface{}); ok && len(l) == 1 && fc["spell"] == nil {
			fc["spell"] = l[0]
			delete(fc, "spells")
		}
		var bad []string
		for k := range fc {
			if !featChoiceFields[k] {
				bad = append(bad, k)
			}
		}
		sort.Strings(bad)
		for _, k := range bad {
			out = append(out, fmt.Sprintf("%q is not part of %s: Magic Initiate takes list, cantrips (2), spell (1) and spellcasting_ability; Skilled takes proficiencies (3)", k, field))
		}
	}
	return out
}
