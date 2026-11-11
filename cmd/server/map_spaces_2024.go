package main

// Moving around other creatures on the battle map (SRD 5.2.1, Playing the Game, "Moving Around Other Creatures"): you can move
// through the space of an ally, of a creature that has the Incapacitated condition, of a Tiny creature, or of a creature two sizes
// larger or smaller than you; another creature's space is Difficult Terrain for you, and you can't willingly end a move in it.
// Halfling Nimbleness adds any creature a size larger than the Halfling. (F115: the map refused every occupied square.)

import (
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// passableTokens: the tokens whose spaces the actor may move through.
func passableTokens(lobby int, m encounterMap, actor mapToken) map[int]bool {
	out := map[int]bool{}
	mine, ok := sizeOrder[strings.SplitN(creatureSize(lobby, actor.ID), " ", 2)[0]]
	if !ok {
		mine = 2
	}
	nimble := actor.ID > 0 && halflingNimble(actor.ID)
	for _, t := range m.Tokens {
		if t.ID == actor.ID {
			continue
		}
		if actor.ID > 0 && t.ID > 0 { // party members are allies
			out[t.ID] = true
			continue
		}
		if t.ID < 0 && monsterIncapacitated(lobby, t.ID) || t.ID > 0 && isIncapacitated(t.ID) {
			out[t.ID] = true
			continue
		}
		theirs, ok := sizeOrder[strings.SplitN(creatureSize(lobby, t.ID), " ", 2)[0]]
		if !ok {
			continue
		}
		if theirs == 0 || theirs-mine >= 2 || mine-theirs >= 2 || (nimble && theirs > mine) {
			out[t.ID] = true
		}
	}
	return out
}

func halflingNimble(charID int) bool {
	var species string
	db.QueryRow(`SELECT lower(COALESCE(species,'')) FROM characters WHERE id = $1`, charID).Scan(&species)
	return strings.Contains(species, "halfling")
}

// endsInCreature: the square is in another creature's space (a move can't end there).
func (m encounterMap) endsInCreature(actor mapToken, sq mapSquare) string {
	for _, p := range actor.footprint(sq) {
		for _, t := range m.Tokens {
			if t.ID != actor.ID && p.X >= t.Position.X && p.X < t.Position.X+t.Size && p.Y >= t.Position.Y && p.Y < t.Position.Y+t.Size {
				return t.Name
			}
		}
	}
	return ""
}

// speciesTraitsOf: the species' traits for the character sheet (F116).
func speciesTraitsOf(species string) []game.Trait {
	if sp := game.GetSpecies(strings.TrimSpace(strings.SplitN(species, "(", 2)[0])); sp != nil {
		return sp.Traits
	}
	return []game.Trait{}
}
