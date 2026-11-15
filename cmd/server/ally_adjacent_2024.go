package main

// "An ally of the attacker is within 5 feet of the target and isn't Incapacitated": one rule for Sneak Attack (SRD 5.2.1:
// "if at least one of your allies is within 5 feet of the target") and Pack Tactics ("if at least one of the creature's allies
// is within 5 feet of the target and the ally doesn't have the Incapacitated condition").
//
// With a battle map holding the target, the map decides, counting the attacker's own side: other characters for a character,
// other monsters for a monster. Without one the server can't see where anyone stands, so the attacker says it: ally_adjacent
// in a player's action (or pending step), ally_adjacent in a GM's monster_action. Nothing is assumed.

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
)

var allyContextRE = regexp.MustCompile(`\[ctx ally_adjacent=1\]`)

// withAllyContext marks an action's description with the player's ally_adjacent (as withActionContext does close_range).
func withAllyContext(desc string, said bool) string {
	if !said {
		return desc
	}
	return desc + " [ctx ally_adjacent=1]"
}

func allyAdjacentSaid(desc string) bool { return allyContextRE.MatchString(desc) }

type allySaidKey struct{}

// withAllySaid carries a GM's ally_adjacent for a monster's attack in the request.
func withAllySaid(r *http.Request, said bool) *http.Request {
	if !said {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), allySaidKey{}, true))
}

func allySaidIn(r *http.Request) bool {
	if r == nil {
		return false
	}
	b, _ := r.Context().Value(allySaidKey{}).(bool)
	return b
}

// creatureDown: the creature can't count as an ally: at 0 Hit Points or Incapacitated.
func creatureDown(lobby, id int) bool {
	if id > 0 {
		var hp int
		db.QueryRow(`SELECT hp FROM characters WHERE id = $1`, id).Scan(&hp)
		return hp <= 0 || isIncapacitated(id)
	}
	for _, e := range combatEntries(lobby) {
		if asInt(e["id"]) == id {
			return asInt(e["hp"]) <= 0 || monsterIncapacitated(lobby, id)
		}
	}
	return true
}

// allyNextToTarget: the rule. said is the attacker's word, used only when no battle map holds the target.
func allyNextToTarget(lobby, attacker, target int, said bool) bool {
	if target == 0 {
		return false
	}
	if m, ok := activeEncounterMap(lobby); ok {
		if tt, ok := m.token(target); ok {
			for _, t := range m.Tokens {
				if t.ID == attacker || t.ID == target || (t.ID > 0) != (attacker > 0) {
					continue // not on the attacker's side
				}
				if !creatureDown(lobby, t.ID) && tokensAdjacent(t, tt) {
					return true
				}
			}
			return false
		}
	}
	return said
}

// allyNote: how the rule was decided, for a reply.
func allyNote(lobby, target int) string {
	if m, ok := activeEncounterMap(lobby); ok {
		if _, ok := m.token(target); ok {
			return "the battle map shows an ally within 5 feet of the target"
		}
	}
	return fmt.Sprint("an ally is within 5 feet of the target (as you said: ally_adjacent)")
}
