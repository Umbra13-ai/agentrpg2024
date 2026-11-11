package main

// Spell riders on monsters (F250, Nov 11, live play): a natural Vicious Mockery dealt its damage but never gave the target Disadvantage
// on its next attack; Guiding Bolt, Ray of Frost, Chill Touch, Shocking Grasp, Mind Sliver and Starry Wisp likewise left no effect.
// On a hit (attack spells) or a failed save (save spells) the rider is written as a combat effect caused by the caster: it shows in
// adjust-monster's readback, is used up by its trigger, and ends at the start of the caster's next turn (as Sap and Slow do).

import "fmt"

var spellRiders = map[string]struct{ kind, text string }{
	"vicious-mockery": {"sap", "Disadvantage on its next attack roll"},
	"guiding-bolt":    {"exposed", "the next attack roll against it has Advantage"},
	"ray-of-frost":    {"slow", "Speed reduced by 10 feet"},
	"shocking-grasp":  {"addled", "it can't make Opportunity Attacks"},
	"chill-touch":     {"no_healing", "it can't regain Hit Points"},
	"mind-sliver":     {"mind_sliver", "it subtracts 1d4 from its next saving throw"},
	"starry-wisp":     {"wisp_lit", "it can't benefit from the Invisible condition"},
}

// spellRider writes the spell's rider on the monster and says so ("" for spells without one).
func spellRider(lobbyID, casterID, monsterID int, spellKey string) string {
	r, ok := spellRiders[spellKey]
	if !ok || monsterID >= 0 {
		return ""
	}
	addCombatEffect(lobbyID, monsterID, casterID, r.kind)
	return fmt.Sprintf(" Until the start of your next turn, %s.", r.text)
}
