package main

// species_choices sent before a species is chosen (builder step 4). Each value is checked against every species that asks for that
// choice: a value no species accepts (ancestry "purple") is refused at once with the options, instead of being saved and flagged only
// once a species is chosen. Keys no species asks for are left to the general species_choices check.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// speciesChoicesBeforeSpecies returns step 4 choices with a problem for each species_choices value no species would accept (a skill
// the class or background already gives is refused by every species too).
func speciesChoicesBeforeSpecies(body map[string]interface{}, class string, bg *game.Background) []map[string]interface{} {
	sc := bmap(body, "species_choices")
	held := map[string]bool{}
	for _, s := range blist(body, "skill_proficiencies") {
		held[strings.ReplaceAll(s, "_", " ")] = true
	}
	if bg != nil {
		for _, s := range bg.Skills {
			held[strings.ToLower(s)] = true
		}
	}
	options := map[string][]string{} // choice -> every value some species accepts; "skill" is any skill (the Human's)
	offers := map[string][]string{}  // choice -> "Dragonborn: black, blue, ..." per species (species with the same options together)
	add := func(need, name string, opts []string) {
		if _, ok := options[need]; !ok {
			options[need] = nil
		}
		for _, o := range opts {
			if !containsStr(options[need], o) {
				options[need] = append(options[need], o)
			}
		}
		list := strings.Join(opts, ", ")
		for i, line := range offers[need] {
			if strings.HasSuffix(line, ": "+list) {
				offers[need][i] = strings.Replace(line, ": ", ", "+name+": ", 1)
				return
			}
		}
		offers[need] = append(offers[need], name+": "+list)
	}
	for _, s := range game.AllSpecies() {
		for _, need := range s.Needs {
			opts, _ := speciesChoiceOptions(s.Key, need, class, map[string]bool{})
			add(need, s.Name, opts)
			if need == "legacy" {
				add("lineage", s.Name, opts) // a Tiefling's legacy may be sent as its lineage
			}
		}
	}
	keys := make([]string, 0, len(sc))
	for k := range sc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []map[string]interface{}
	for _, k := range keys {
		opts, asked := options[k]
		if !asked || sc[k] == nil {
			continue
		}
		s, isText := sc[k].(string)
		v := strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), " ", "_"), "-", "_")
		offered := "any skill"
		if k != "skill" {
			offered = strings.Join(offers[k], "; ")
		}
		prob := ""
		switch {
		case !isText || v == "":
			prob = fmt.Sprintf("species_choices.%s must be text (%s).", k, offered)
		case k == "skill" && !game.IsSkill(v):
			prob = fmt.Sprintf("%q is not a skill.", s)
		case k == "skill" && held[strings.ReplaceAll(v, "_", " ")]:
			prob = fmt.Sprintf("You already have %s (from your class or background): choose a different skill.", titleWords(v))
		case k == "feat" && !containsStr(opts, v) && phbNote("feat", s) != "":
			prob = phbNote("feat", s) + " Choose one of: " + strings.Join(opts, ", ") + "."
		case k != "skill" && !containsStr(opts, v):
			prob = fmt.Sprintf("No species offers %q for %s (%s).", s, k, offered)
		}
		if prob != "" {
			out = append(out, choice("species_choices."+k, k, 1, opts, sc[k], false, prob))
		}
	}
	return out
}
