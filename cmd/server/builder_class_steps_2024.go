package main

// Helpers for the builder's Steps 1-3 (class, class choices, background), Nov 14 audit: an answer is saved as sent (in its
// canonical spelling when an obvious variant is meant), or refused at the step with the reason; a step checks what creation
// checks, so the review never refuses an answer a step accepted; an answer left over from an earlier class or background is
// flagged on its step with how to remove it; and the suggestions of Steps 2 and 3 agree with each other.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// normSkillName: a skill as the builder stores it ("Sleight_of-Hand" -> "sleight of hand").
func normSkillName(s string) string {
	s = strings.ToLower(strings.NewReplacer("_", " ", "-", " ").Replace(s))
	return strings.Join(strings.Fields(s), " ")
}

// canonSkill: the SRD skill a name means, if it is one.
func canonSkillOK(s string) (string, bool) {
	n := normSkillName(s)
	return n, game.IsSkill(n)
}

// normalizeListField writes a list answer back with each item in its canonical form, so the draft keeps what the step checked
// ("sleight_of_hand" is saved as "sleight of hand", "Musical Instrument (Lute)" as "lute"). An item canon doesn't know stays as
// sent, for the step to refuse. It returns the items as the step reads them (lowercased, like blist).
func normalizeListField(body map[string]interface{}, key string, canon func(string) (string, bool)) []string {
	var raw []interface{}
	switch v := body[key].(type) {
	case []interface{}:
		raw = v
	case []string:
		for _, s := range v {
			raw = append(raw, s)
		}
	default:
		return blist(body, key)
	}
	out := make([]interface{}, 0, len(raw))
	var list []string
	changed := false
	for _, x := range raw {
		s, ok := x.(string)
		if !ok || strings.TrimSpace(s) == "" {
			out = append(out, x)
			continue
		}
		c, known := canon(s)
		if !known {
			out = append(out, s)
			list = append(list, strings.ToLower(strings.TrimSpace(s)))
			continue
		}
		if c != s {
			changed = true
		}
		out = append(out, c)
		list = append(list, c)
	}
	if changed {
		body[key] = out // a new list: the request's own value (shown in not_saved) is left as sent
	}
	return list
}

// hasAnswer: the field holds something (not missing, null, empty text, an empty list or an empty object).
func hasAnswer(v interface{}) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(x) != ""
	case []interface{}:
		return len(x) > 0
	case []string:
		return len(x) > 0
	case map[string]interface{}:
		return len(x) > 0
	}
	return true
}

// draftSpeciesProficiencies: the skill the draft's species gives (an Elf's or a Human's species_choices.skill) and the Human's
// Skilled picks (skills, and tools by name), as creation counts them. A choice left from another species is not counted.
func draftSpeciesProficiencies(body map[string]interface{}) (skill string, featSkills, featTools []string) {
	sp := game.GetSpecies(strings.ToLower(bstr(body, "species")))
	if sp == nil {
		return
	}
	sc := bmap(body, "species_choices")
	if containsStr(sp.Needs, "skill") {
		if s, ok := canonSkillOK(bstr(sc, "skill")); ok {
			skill = s
		}
	}
	feat := strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(firstNonEmpty(bstr(sc, "feat"), "skilled")))
	if sp.Key == "human" && feat == "skilled" {
		for _, p := range blist(bmap(body, "species_feat_choices"), "proficiencies") {
			if s, ok := canonSkillOK(p); ok {
				featSkills = append(featSkills, s)
			} else if t, ok := game.FindTool(p); ok {
				featTools = append(featTools, t.Name)
			}
		}
	}
	return
}

// classToolCanon: the class tool option a name means ("Musical Instrument (Lute)", "LUTE", "Smiths Tools"), as creation reads it.
func classToolCanon(options []string) func(string) (string, bool) {
	return func(s string) (string, bool) {
		if t, ok := findToolLoose(s); ok && containsStr(options, t.Name) {
			return t.Name, true
		}
		return "", false
	}
}

// bestBackground: the background whose ability increases suit the class best (+2 and +1 where the class's Standard Array row is
// highest; the third breaks a tie), leaving out one that gives a skill in skip (the class skills chosen: they would be lost).
// When every background gives one of them, the best of all.
func bestBackground(class string, skip map[string]bool) string {
	arr, ok := phbArray[class]
	if !ok {
		return ""
	}
	best, bestScore, top, topScore := "", -1, "", -1
	for _, k := range game.BackgroundSlugs() {
		b := game.GetBackground(k)
		if b == nil {
			continue
		}
		vals := []int{}
		for _, a := range b.AbilityScores {
			for i, k6 := range abilityKeys6 {
				if k6 == a {
					vals = append(vals, arr[i])
				}
			}
		}
		if len(vals) < 3 {
			continue
		}
		sort.Sort(sort.Reverse(sort.IntSlice(vals)))
		score := (2*vals[0]+vals[1])*100 + vals[2]
		if score > topScore {
			top, topScore = b.Key, score
		}
		overlap := false
		for _, s := range b.Skills {
			if skip[normSkillName(s)] {
				overlap = true
			}
		}
		if !overlap && score > bestScore {
			best, bestScore = b.Key, score
		}
	}
	if best == "" {
		return top
	}
	return best
}

// draftIncreases: the draft's ability_increases as numbers (a suggestion taken in puts them in as a map[string]int).
func draftIncreases(body map[string]interface{}) map[string]int {
	raw := bmap(body, "ability_increases")
	if raw == nil {
		if m, ok := body["ability_increases"].(map[string]int); ok {
			raw = map[string]interface{}{}
			for k, v := range m {
				raw[k] = v
			}
		}
	}
	inc := map[string]int{}
	for k := range raw {
		if n := bint(raw, k); n != 0 {
			inc[normAbility(k)] = n
		}
	}
	return inc
}

// formatIncreases: {"cha": 2, "dex": 1} as "+2 cha, +1 dex".
func formatIncreases(inc map[string]int) string {
	var parts []string
	for _, k := range abilityKeys6 {
		if v, ok := inc[k]; ok {
			parts = append(parts, fmt.Sprintf("%+d %s", v, k))
		}
	}
	for k, v := range inc {
		if !containsStr(abilityKeys6[:], k) {
			parts = append(parts, fmt.Sprintf("%+d %s", v, k))
		}
	}
	return strings.Join(parts, ", ")
}

// removeHint: how to take a field (or a part of one) out of the draft.
func removeHint(field string) string {
	if i := strings.Index(field, "."); i > 0 {
		return fmt.Sprintf("send {\"%s\": {\"%s\": null}} to remove it", field[:i], field[i+1:])
	}
	return fmt.Sprintf("send {\"%s\": null} to remove it", field)
}

// leftoverChoice: an answer the class or background doesn't take (left from an earlier one, or sent by mistake), shown on its
// step with the reason and how to remove it, so the step isn't done and the review never refuses it later.
func leftoverChoice(field string, current interface{}, why string) map[string]interface{} {
	return choice(field, "not for your current choices: remove it", 0, nil, current, false, why+": "+removeHint(field))
}

// backgroundsWithToolChoice: the backgrounds that let the player choose the kind of tool (background_tool).
func backgroundsWithToolChoice() []string {
	var out []string
	for _, k := range game.BackgroundSlugs() {
		if b := game.GetBackground(k); b != nil && b.ToolCategory != "" {
			out = append(out, b.Name)
		}
	}
	return out
}

// builderSpellSlug: a spell name as its slug ("Sacred Flame", "sacred_flame" -> "sacred-flame").
func builderSpellSlug(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.NewReplacer("_", " ", "-", " ").Replace(name))), "-")
}
