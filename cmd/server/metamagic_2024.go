package main

// Metamagic, 2024 rules: the options, their costs and their text come from the SRD (Sorcerer, "Metamagic Options").
// Seeking Spell and Transmuted Spell are new; Twinned Spell now costs 1 and raises the effective level by 1; Heightened
// Spell costs 2. Metamagic is learned at Sorcerer level 2. One option per spell (two while Innate Sorcery is active from
// level 7, Sorcery Incarnate); Empowered Spell and Seeking Spell can be added to another option.

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

var seekingSpell sync.Map // character id -> true while the spell being cast has Seeking Spell

var metamagicCostRe = regexp.MustCompile(`^Cost: (\d+) Sorcery Point`)

func init() {
	for _, c := range srdClassTexts() {
		if c.Name != "Sorcerer" {
			continue
		}
		on := false
		for _, s := range c.Sections {
			if strings.Contains(s.Heading, "Metamagic Options") {
				on = true
				continue
			}
			if !on || !strings.HasSuffix(s.Heading, " Spell") {
				continue
			}
			var text []string
			for _, b := range s.Blocks {
				if str, ok := b.(string); ok {
					text = append(text, str)
				}
			}
			joined := strings.Join(text, " ")
			cost := 1
			if m := metamagicCostRe.FindStringSubmatch(joined); m != nil {
				fmt.Sscanf(m[1], "%d", &cost)
			}
			desc := strings.TrimSpace(metamagicCostRe.ReplaceAllString(joined, ""))
			desc = strings.TrimSpace(strings.TrimPrefix(desc, "s"))
			slug := strings.ToLower(strings.TrimSuffix(s.Heading, " Spell"))
			metamagicOptions[slug] = MetamagicOption{Slug: slug, Name: s.Heading, Cost: cost, Description: strings.TrimSpace(strings.TrimLeft(desc, " "))}
		}
	}
}

// metamagicAllowed checks the number of options on one spell: one, or two with Sorcery Incarnate; Empowered and Seeking are extra.
func metamagicAllowed(used []string, innateActiveAtSeven bool) (bool, string) {
	main := 0
	for _, m := range used {
		if m != "empowered" && m != "seeking" {
			main++
		}
	}
	limit := 1
	if innateActiveAtSeven {
		limit = 2
	}
	if main > limit {
		return false, fmt.Sprintf("You can use only %d Metamagic option(s) on one spell (Empowered and Seeking Spell can be added to another option).", limit)
	}
	return true, ""
}
