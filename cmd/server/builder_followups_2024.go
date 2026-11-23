package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// builder_followups_2024.go: what a pick in the character builder brings with it, said on the option before it is picked (agent report, Nov 23):
//   - followUpOfBackground / followUpOfSpecies: the follow-up question a background's Origin feat (Magic Initiate: cantrips, spell, ability) or a species'
//     choices (a lineage, an ability, a skill, a size ...) ask, with the fields to send, so the pick and its answers go in one request. The fields come from
//     featFollowUps and the species' own Needs (game/species.go): nothing is written twice.
//   - skillSources / classSkillsUsedBy: which class skills a background gives, so the class step marks them before the pick and the background's option says
//     which of the class's skills it uses up. The check that refuses an overlap (character_builder_2024.go, class step) reads the same game data.
//   - countRoseText: why the count of choices left went up, in the reply that raised it.

// featFollowUps: the fields an Origin feat's follow-up takes (feat slug to what to send). A feat not listed takes none.
var featFollowUps = map[string]map[string]interface{}{
	"magic_initiate": {"cantrips": "two cantrips from the feat's spell list", "spell": "one level 1 spell from that list", "spellcasting_ability": "int, wis or cha"},
	"skilled":        {"proficiencies": "three skills or tools"},
}

// followUpOfBackground: nil when the background asks for nothing more; else {field, send, same_request, text}.
func followUpOfBackground(b *game.Background) map[string]interface{} {
	fields, ok := featFollowUps[b.Feat]
	if !ok {
		return nil
	}
	send := map[string]interface{}{"background": b.Key, "background_feat_choices": fields}
	return map[string]interface{}{
		"field": "background_feat_choices", "send": send, "same_request": true,
		"text": fmt.Sprintf("%s's Origin feat, %s, asks a follow-up: %s. Send background_feat_choices with the background in one request, or send the background now and the step stays open for the follow-up.",
			b.Name, strings.TrimSpace(titleWords(strings.ReplaceAll(b.Feat, "_", " "))+" "+b.FeatNote), fieldList(fields)),
	}
}

// followUpOfSpecies: nil when the species asks for nothing more (its Needs: lineage, ancestry, size, skill, spellcasting_ability, a feat ...).
func followUpOfSpecies(s game.Species) map[string]interface{} {
	if len(s.Needs) == 0 {
		return nil
	}
	fields := map[string]interface{}{}
	for _, n := range s.Needs {
		if n == "feat" {
			fields[n] = "an Origin feat (its own picks go in species_feat_choices)"
		} else {
			fields[n] = "your choice (the species step lists the options)"
		}
	}
	send := map[string]interface{}{"species": s.Key, "species_choices": s.Needs}
	return map[string]interface{}{
		"field": "species_choices", "send": send, "same_request": true,
		"text": fmt.Sprintf("%s asks a follow-up: %s. Send species_choices with the species in one request, or send the species now and the step stays open for the follow-up.", s.Name, strings.Join(s.Needs, ", ")),
	}
}

func fieldList(fields map[string]interface{}) string {
	var keys []string
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s (%v)", k, fields[k]))
	}
	return strings.Join(parts, "; ")
}

// skillSources: for each skill, the backgrounds that give it ("Arcana": sage ...). The class step marks the skills a background gives with it.
func skillSources() map[string][]string {
	out := map[string][]string{}
	for _, k := range game.BackgroundSlugs() {
		if b := game.GetBackground(k); b != nil {
			for _, s := range b.Skills {
				out[game.SkillName(s)] = append(out[game.SkillName(s)], b.Key)
			}
		}
	}
	return out
}

// classSkillsUsedBy: the skills of the class's list that the background also gives: picks the class step would have to change.
func classSkillsUsedBy(class string, b *game.Background) []string {
	_, allowed := classSkillChoices(class)
	var out []string
	for _, s := range b.Skills {
		if containsStr(allowed, game.SkillName(s)) {
			out = append(out, game.SkillName(s))
		}
	}
	return out
}

// countRoseText says why the number of choices left went up, naming the steps that grew and the fields they ask (the reply that raised it).
func countRoseText(before int, steps []builderStep, stepsBefore []builderStep) string {
	after := 0
	for _, s := range steps {
		after += s.Remaining
	}
	if after <= before {
		return ""
	}
	was := map[string]int{}
	for _, s := range stepsBefore {
		was[s.Key] = s.Remaining
	}
	var grew []string
	for i := range steps {
		s := &steps[i]
		if s.Remaining > was[s.Key] {
			with, without := openChoices(s)
			grew = append(grew, fmt.Sprintf("%s: %s", strings.TrimSpace(s.Title), strings.Join(append(with, without...), ", ")))
		}
	}
	return fmt.Sprintf("Choices left went from %d to %d: what you just chose opened a follow-up (%s). That is a new question, not an error.", before, after, strings.Join(grew, "; "))
}
