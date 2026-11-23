package main

// Spell riders on monsters (F250, Nov 11, live play): a natural Vicious Mockery dealt its damage but never gave the target Disadvantage
// on its next attack; Guiding Bolt, Ray of Frost, Chill Touch, Shocking Grasp, Mind Sliver and Starry Wisp likewise left no effect.
// On a hit (attack spells) or a failed save (save spells) the rider is written as a combat effect caused by the caster: it shows in
// adjust-monster's readback, is used up by its trigger, and ends when the spell's text says (spellRiderEnds: the start or end of the caster's or the target's next turn).

import (
	"fmt"
	"regexp"
	"strings"
)

var spellRiders = map[string]struct{ kind, text string }{
	"vicious-mockery": {"sap", "Disadvantage on its next attack roll"},
	"guiding-bolt":    {"exposed", "the next attack roll against it has Advantage"},
	"ray-of-frost":    {"slow", "Speed reduced by 10 feet"},
	"shocking-grasp":  {"addled", "it can't make Opportunity Attacks"},
	"chill-touch":     {"no_healing", "it can't regain Hit Points"},
	"mind-sliver":     {"mind_sliver", "it subtracts 1d4 from its next saving throw"},
	"starry-wisp":     {"wisp_lit", "it can't benefit from the Invisible condition"},
}

// spellRiderEndRe: the rider's duration in the spell's own words ("until the end of your next turn", "until the start of its next turn", "before the end of
// its next turn"): your is the caster's turn, its the target's.
var spellRiderEndRe = regexp.MustCompile(`(?i)(?:until|before) the (start|end) of (your|its) next turn`)

// spellRiderEnds is when a spell's rider ends, from its text (addCombatEffectUntil's ends); the start of the caster's next turn when the text names none.
func spellRiderEnds(spellKey string) (ends, words string) {
	sp, ok := srdSpellsMemory[spellKey]
	if !ok {
		return "", "until the start of your next turn"
	}
	m := spellRiderEndRe.FindStringSubmatch(sp.Description)
	if m == nil {
		return "", "until the start of your next turn"
	}
	edge, who := strings.ToLower(m[1]), strings.ToLower(m[2])
	words = "until the " + edge + " of " + map[string]string{"your": "your", "its": "its"}[who] + " next turn"
	switch {
	case who == "your" && edge == "start":
		return "", words
	case who == "your":
		return "source_end", words
	case edge == "start":
		return "target_start", words
	}
	return "target_end", words
}

// spellRider writes the spell's rider on the monster for the time its text gives and says so ("" for spells without one).
func spellRider(lobbyID, casterID, monsterID int, spellKey string) string {
	r, ok := spellRiders[spellKey]
	if !ok || monsterID >= 0 {
		return ""
	}
	ends, words := spellRiderEnds(spellKey)
	addCombatEffectUntil(lobbyID, monsterID, casterID, r.kind, ends)
	return fmt.Sprintf(" %s, %s.", strings.ToUpper(words[:1])+words[1:], r.text)
}
