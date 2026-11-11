package main

// Cones and Lines on the battle map (SRD 5.2.1 "Areas of Effect"; F58, Nov 8, 2026). A Cone extends in a chosen direction from its
// point of origin, and its width at any point along its length equals that point's distance from the origin. A Line extends from its
// origin in a straight path up to its length, as wide as it says (5 feet unless the text gives another width). The direction is
// toward the first target (the creature or point the caster aims at), and the origin is the edge of the caster's space on that side.
// A creature is in the area when the center of one of its squares is. Before this, a Cone or a Line caught every creature within its
// length in any direction (behind the caster too), and monsters' area actions weren't checked against the map at all.

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

type mapPoint struct{ X, Y float64 }

// tokenCenter is the middle of a token's space (square (x, y) spans x..x+1).
func tokenCenter(t mapToken) mapPoint {
	s := float64(t.Size)
	if s < 1 {
		s = 1
	}
	return mapPoint{float64(t.Position.X) + s/2, float64(t.Position.Y) + s/2}
}

// inLineOrCone: the point p is inside a Cone or Line from the center o of the caster's space toward aim; length and width in feet.
// offset is how far the caster's space reaches past its center square ((size - 1) / 2 squares): the area starts at its edge.
func inLineOrCone(shape string, o, aim, p mapPoint, lengthFt, widthFt, offset float64) bool {
	dx, dy := aim.X-o.X, aim.Y-o.Y
	n := math.Hypot(dx, dy)
	if n == 0 {
		return false
	}
	ux, uy := dx/n, dy/n
	vx, vy := p.X-o.X, p.Y-o.Y
	t := vx*ux + vy*uy - offset  // distance along the direction past the caster's space, in squares
	d := math.Abs(vx*uy - vy*ux) // distance from the center line
	if t <= 0 || t > lengthFt/battleMapStepFeetF+1e-9 {
		return false
	}
	if shape == "line" {
		return d <= widthFt/battleMapStepFeetF/2+1e-9
	}
	return d <= t/2+1e-9 // a Cone's width at any point equals its distance from the origin
}

const battleMapStepFeetF = float64(battleMapStepFeet)

// tokenInLineOrCone: a square of the target's space is inside the area the caster aims at aim.
func tokenInLineOrCone(shape string, caster, aim, target mapToken, lengthFt, widthFt float64) bool {
	c, a := tokenCenter(caster), tokenCenter(aim)
	size := caster.Size
	if size < 1 {
		size = 1
	}
	offset := float64(size-1) / 2
	for _, sq := range target.footprint(target.Position) {
		if inLineOrCone(shape, c, a, mapPoint{float64(sq.X) + 0.5, float64(sq.Y) + 0.5}, lengthFt, widthFt, offset) {
			return true
		}
	}
	return false
}

var lineWidthRe = regexp.MustCompile(`(?i)(\d+)[- ]f(?:oo|ee)t[- ]?wide`)

// lineWidthFeet reads a Line's width from its text (5 feet when it doesn't say).
func lineWidthFeet(text string) float64 {
	if m := lineWidthRe.FindStringSubmatch(text); m != nil {
		if w, err := strconv.Atoi(m[1]); err == nil && w > 0 {
			return float64(w)
		}
	}
	return 5
}

var (
	monsterLineRe   = regexp.MustCompile(`(\d+)-foot-long, (\d+)-foot-?wide Line`) // the SRD text has "5-footwide" too
	monsterConeRe   = regexp.MustCompile(`(\d+)-foot Cone`)
	monsterSphereRe = regexp.MustCompile(`(\d+)-foot-radius Sphere`)
)

// monsterAreaPoint: the grid square a GM aims a monster's area at during one narration (monster_action "point": [x, y]; O-5).
var monsterAreaPoint sync.Map // lobby id -> mapSquare

func pointToken(sq mapSquare) mapToken {
	return mapToken{Name: fmt.Sprintf("the square (%d, %d)", sq.X, sq.Y), Position: sq, Size: 1}
}

// monsterAreaAim: where a monster's area is aimed: the GM's point, else its first target.
func monsterAreaAim(lobbyID int, m encounterMap, targets []int) (mapToken, bool) {
	if v, ok := monsterAreaPoint.Load(lobbyID); ok {
		return pointToken(v.(mapSquare)), true
	}
	if len(targets) == 0 {
		return mapToken{}, false
	}
	return m.token(targets[0])
}

// areaCharacters lists the living characters on the map (not companions) that stand in an area.
func areaCharacters(m encounterMap, in func(mapToken) bool) []mapToken {
	var out []mapToken
	for _, t := range m.Tokens {
		if t.ID <= 0 || t.ID >= companionIDBase {
			continue
		}
		var dead bool
		db.QueryRow(`SELECT COALESCE(is_dead,false) FROM characters WHERE id = $1`, t.ID).Scan(&dead)
		if !dead && in(t) {
			out = append(out, t)
		}
	}
	return out
}

// spellAreaContains: a spell's area (sp.AoEShape, sp.AoESize) cast by caster and aimed at aim covers the token.
func spellAreaContains(sp SRDSpell, caster, aim, t mapToken) bool {
	switch sp.AoEShape {
	case "cone", "line":
		return tokenInLineOrCone(sp.AoEShape, caster, aim, t, float64(sp.AoESize), lineWidthFeet(sp.Description))
	case "emanation":
		return tokenDistanceFeet(caster, t) <= sp.AoESize
	}
	return tokenDistanceFeet(aim, t) <= sp.AoESize
}

// monsterSpellArea: the characters in the area of a monster's spell aimed at aim (a GM's point), and the characters in it that weren't
// listed (aimed at the first listed target). Every creature in an area is affected, not only the ones the GM named (O-5).
func monsterSpellArea(lobbyID, monsterID int, sp SRDSpell, aim mapToken, listed []int) (all []int, added []string) {
	m, ok := activeEncounterMap(lobbyID)
	if !ok || sp.AoEShape == "" || sp.AoESize <= 0 {
		return nil, nil
	}
	caster, ok := m.token(monsterID)
	if !ok {
		return nil, nil
	}
	seen := map[int]bool{}
	for _, id := range listed {
		seen[id] = true
	}
	for _, t := range areaCharacters(m, func(t mapToken) bool { return spellAreaContains(sp, caster, aim, t) }) {
		all = append(all, t.ID)
		if !seen[t.ID] {
			added = append(added, t.Name)
		}
	}
	return all, added
}

// monsterAreaFilter: the targets of a monster's area action (a Line, a Cone, a Sphere centered on the first target or on the GM's
// point) on the battle map: the listed ones that stand in it, and every other character in it too (O-5: an area affects everyone
// in it). Without a map, or for another area, the GM's targets stand. It returns the ids and a note.
func monsterAreaFilter(lobbyID, monsterID int, text string, targets []int) ([]int, string) {
	m, ok := activeEncounterMap(lobbyID)
	if !ok {
		return targets, ""
	}
	src, okS := m.token(monsterID)
	aim, okA := monsterAreaAim(lobbyID, m, targets)
	if !okS || !okA {
		return targets, ""
	}
	var shape, label string
	var length, width float64
	if x := monsterLineRe.FindStringSubmatch(text); x != nil {
		l, _ := strconv.Atoi(x[1])
		w, _ := strconv.Atoi(x[2])
		shape, length, width, label = "line", float64(l), float64(w), fmt.Sprintf("%d-foot-long, %d-foot-wide Line", l, w)
	} else if x := monsterConeRe.FindStringSubmatch(text); x != nil {
		l, _ := strconv.Atoi(x[1])
		shape, length, label = "cone", float64(l), x[0]
	} else if x := monsterSphereRe.FindStringSubmatch(text); x != nil {
		r, _ := strconv.Atoi(x[1])
		shape, length, label = "sphere", float64(r), x[0]
	} else {
		return targets, ""
	}
	var kept []int
	var dropped []string
	for _, id := range targets {
		t, ok := m.token(id)
		in := !ok // a creature not on the map stays the GM's call
		if ok {
			if shape == "sphere" {
				in = tokenDistanceFeet(aim, t) <= int(length)
			} else {
				in = tokenInLineOrCone(shape, src, aim, t, length, width)
			}
		}
		if in {
			kept = append(kept, id)
		} else {
			dropped = append(dropped, t.Name)
		}
	}
	listed := map[int]bool{}
	for _, id := range targets {
		listed[id] = true
	}
	var added []string
	for _, t := range areaCharacters(m, func(t mapToken) bool {
		if shape == "sphere" {
			return tokenDistanceFeet(aim, t) <= int(length)
		}
		return tokenInLineOrCone(shape, src, aim, t, length, width)
	}) {
		if !listed[t.ID] {
			kept = append(kept, t.ID)
			added = append(added, t.Name)
		}
	}
	note := ""
	if len(dropped) > 0 {
		note = fmt.Sprintf("Outside the %s on the battle map: %s.", label, strings.Join(dropped, ", "))
	}
	if len(added) > 0 {
		note = strings.TrimSpace(note + fmt.Sprintf(" Also in the %s: %s.", label, strings.Join(added, ", ")))
	}
	return kept, note
}
