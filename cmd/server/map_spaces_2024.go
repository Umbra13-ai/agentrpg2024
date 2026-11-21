package main

// Moving around other creatures on the battle map (SRD 5.2.1, Playing the Game, "Moving Around Other Creatures"): you can move
// through the space of an ally, of a creature that has the Incapacitated condition, of a Tiny creature, or of a creature two sizes
// larger or smaller than you; another creature's space is Difficult Terrain for you, and you can't willingly end a move in it.
// Halfling Nimbleness adds any creature a size larger than the Halfling. (F115: the map refused every occupied square.)

import (
	"fmt"
	"regexp"
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
		if theirs == 0 || theirs-mine >= 2 || mine-theirs >= 2 || (nimble && theirs > mine) || t.SharesSpace || actor.SharesSpace || actor.Incorporeal {
			out[t.ID] = true // a Swarm's space (and a Swarm or an incorporeal mover through any space): markMapSpaces
		}
	}
	return out
}

func halflingNimble(charID int) bool {
	var species string
	db.QueryRow(`SELECT lower(COALESCE(species,'')) FROM characters WHERE id = $1`, charID).Scan(&species)
	return strings.Contains(species, "halfling")
}

// endsInCreature: the square is in another creature's space (a move can't end there; a Swarm's space is shared both ways).
func (m encounterMap) endsInCreature(actor mapToken, sq mapSquare) string {
	for _, p := range actor.footprint(sq) {
		for _, t := range m.Tokens {
			if t.SharesSpace || actor.SharesSpace {
				continue
			}
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

// The stat block traits that change how a creature shares space (read from their wording, monsterTraitRules):
//   - a Swarm: "can occupy another creature's space and vice versa" (both ways: other creatures can enter and end in its space);
//   - Incorporeal Movement: "can move through other creatures and objects as if they were Difficult Terrain. It takes 5 (1d10)
//     Force damage if it ends its turn inside an object." Its token may stand inside a wall or a closed door; the damage is the
//     turn-end hook objectEndDamage.
var (
	swarmSpaceRe   = regexp.MustCompile(`(?i)can occupy another creature['’]s space and vice versa`)
	incorporealRe  = regexp.MustCompile(`(?i)can move through other creatures and objects as if they were Difficult Terrain`)
	insideObjectRe = regexp.MustCompile(`(?i)takes (\d+)(?: \(([^)]+)\))? (\w+) damage if it ends its turn inside an object`)
)

// monsterTraitText: the text of the monster's stat block trait whose wording matches re ("" when none does).
func monsterTraitText(lobby, id int, re *regexp.Regexp) string {
	_, meta, ok := monsterCombatant(lobby, id)
	if !ok || meta == nil {
		return ""
	}
	for _, t := range meta.Traits {
		if re.MatchString(t.Text) {
			return t.Text
		}
	}
	return ""
}

// markMapSpaces marks the map's tokens whose creatures share spaces (a Swarm) or pass through objects (Incorporeal Movement).
func markMapSpaces(lobby int, m *encounterMap) {
	for i, t := range m.Tokens {
		if t.ID >= 0 || db == nil {
			continue
		}
		m.Tokens[i].SharesSpace = monsterTraitText(lobby, t.ID, swarmSpaceRe) != ""
		m.Tokens[i].Incorporeal = monsterTraitText(lobby, t.ID, incorporealRe) != ""
	}
}

// objectEndDamage: the turn-end hook of a creature that ends its turn inside an object (its Incorporeal Movement's damage), dealt
// through the monster Hit Point owner. "" when it doesn't.
func objectEndDamage(lobby, id int) string {
	if id >= 0 || isCompanionCombatID(id) {
		return ""
	}
	text := monsterTraitText(lobby, id, insideObjectRe)
	if text == "" {
		return ""
	}
	m, ok := activeEncounterMap(lobby)
	if !ok {
		return ""
	}
	tk, ok := m.token(id)
	if !ok {
		return ""
	}
	inside := false
	for _, sq := range tk.footprint(tk.Position) {
		if blockedTile(m.tile(sq)) {
			inside = true
		}
	}
	if !inside {
		return ""
	}
	g := insideObjectRe.FindStringSubmatch(text)
	n := 0
	fmt.Sscanf(g[1], "%d", &n)
	dmg := n
	if g[2] != "" {
		dmg = rollStatBlockDamage(game.NormalizeDice(g[2]), nil, false)
	}
	h, ok := hurtMonster(lobby, id, dmg, strings.ToLower(g[3]), false)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s ends its turn inside an object: %s", tk.Name, h.Text)
}
