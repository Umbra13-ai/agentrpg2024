package main

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Magical Darkness ("Magical Darkness fills a 15-foot Emanation originating from the darkmantle. This
// effect lasts while the darkmantle maintains Concentration on it, up to 10 minutes. Darkvision can't
// penetrate this area, and no light can illuminate it."). A localized, token-anchored area - the
// campaign lighting model is scene-wide and darkvision pierces it, so the aura is its own mechanic:
// a combat_effects row (kind 'magical_darkness') carrying the radius and the round it was made, read
// by the visibility owner with the battle map's geometry (the same anchors and sight lines the light
// traits use, monster_lighting_2024.go). It ends with the caster's Concentration (the concentration
// owner deletes the row in the ending's transaction), when the anchor dies, or 100 rounds (10
// minutes) after it was made; the lazy sweep in magicalDarknessRows is the backstop for the last two.

var magicalDarknessRe = regexp.MustCompile(`(?i)Magical Darkness fills a (\d+)-foot Emanation originating from the`)

const magicalDarknessRounds = 100 // 10 minutes at 6 seconds a round

type magicalDarknessRow struct {
	id, source, radius, round int
}

// startMagicalDarkness records the emanation. One row per source: a second use replaces the first.
func startMagicalDarkness(lobbyID, sourceID, radius int) error {
	if _, err := db.Exec(`DELETE FROM combat_effects WHERE lobby_id=$1 AND kind='magical_darkness' AND source_id=$2`, lobbyID, sourceID); err != nil {
		return err
	}
	_, err := db.Exec(`INSERT INTO combat_effects (lobby_id, target_id, source_id, kind, text) VALUES ($1,0,$2,'magical_darkness',$3)`,
		lobbyID, sourceID, fmt.Sprintf("radius: %d | round: %d", radius, currentRound(lobbyID)))
	return err
}

// magicalDarknessRows reads the live emanations, sweeping the ones that ended on their own: their
// anchor is dead or their 10 minutes passed. (Concentration endings delete their row in the
// concentration owner's transaction; this is the backstop, never the primary path.)
func magicalDarknessRows(lobbyID int) []magicalDarknessRow {
	rows, err := db.Query(`SELECT id, source_id, text FROM combat_effects WHERE lobby_id=$1 AND kind='magical_darkness'`, lobbyID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	hp := map[int]int{}
	for _, entry := range combatEntries(lobbyID) {
		hp[asInt(entry["id"])] = asInt(entry["hp"])
	}
	round := currentRound(lobbyID)
	var out []magicalDarknessRow
	for rows.Next() {
		var r magicalDarknessRow
		var text string
		if rows.Scan(&r.id, &r.source, &text) != nil {
			continue
		}
		if _, err := fmt.Sscanf(text, "radius: %d | round: %d", &r.radius, &r.round); err != nil {
			continue
		}
		if hp[r.source] <= 0 || round >= r.round+magicalDarknessRounds {
			db.Exec(`DELETE FROM combat_effects WHERE id=$1`, r.id)
			continue
		}
		out = append(out, r)
	}
	return out
}

// magicalDarknessAt: the creature's token stands in a live emanation (the anchor's token, the radius,
// and the map's ordinary geometry).
func magicalDarknessAt(lobbyID, creatureID int) bool {
	rows := magicalDarknessRows(lobbyID)
	if len(rows) == 0 || creatureID == 0 {
		return false
	}
	battle, ok := activeEncounterMap(lobbyID)
	if !ok {
		return false
	}
	at, ok := battle.token(creatureID)
	if !ok {
		return false
	}
	for _, r := range rows {
		if creatureID == r.source {
			continue // the origin's own square is not in its Emanation (SRD 5.2.1 "Emanation")
		}
		origin, ok := battle.token(r.source)
		if !ok {
			continue
		}
		if tokenDistanceFeet(origin, at) <= r.radius && battle.canSee(origin, at) {
			return true
		}
	}
	return false
}

var magicalDarknessSenseRe = regexp.MustCompile(`(?i)(blindsight|truesight)\s+(\d+)\s*ft`)

// piercesMagicalDarknessWithin: a sense that pierces magical darkness actually reaches the target -
// Blindsight and Truesight work only "within that range" (SRD 5.2.1 glossary), and a character's
// Devil's Sight reaches 120 feet ("darkness, both magical and nonmagical"). Darkvision is never one.
func piercesMagicalDarknessWithin(lobbyID, creatureID, distFeet int) bool {
	if distFeet < 1 {
		distFeet = 1
	}
	if creatureID > 0 && !isCompanionCombatID(creatureID) {
		_, blindsight, truesight := getCharacterVision(creatureID)
		return blindsight >= distFeet || truesight >= distFeet || (hasInvocation(creatureID, "devils-sight") && distFeet <= 120)
	}
	if creatureUnimpededDarkvision(lobbyID, creatureID) { // "Darkvision 120 ft. (unimpeded by magical Darkness)" (creature_senses_2024.go)
		if darkvision, _, _ := creatureVision(lobbyID, creatureID); darkvision >= distFeet {
			return true
		}
	}
	if creatureID < 0 {
		key := ""
		for _, entry := range combatEntries(lobbyID) {
			if asInt(entry["id"]) == creatureID {
				key = sprintAny(entry["monster_key"])
				break
			}
		}
		if meta := monsterMetaFor(key); meta != nil {
			for _, m := range magicalDarknessSenseRe.FindAllStringSubmatch(meta.Senses, -1) {
				if n, err := strconv.Atoi(m[2]); err == nil && n >= distFeet {
					return true
				}
			}
		}
	}
	return false
}

// segmentCrossesEmanation: the straight line between two squares passes through the emanation's
// interior. Altitude is not modeled here: the area is read in the map's plane, the same
// simplification the wall sight lines (encounterMap.canSee) already use.
func segmentCrossesEmanation(a, b, center mapSquare, radiusFeet int) bool {
	r := float64(radiusFeet) / float64(battleMapStepFeet)
	ax, ay := float64(a.X), float64(a.Y)
	bx, by := float64(b.X), float64(b.Y)
	cx, cy := float64(center.X), float64(center.Y)
	dx, dy := bx-ax, by-ay
	t := 0.0
	if l2 := dx*dx + dy*dy; l2 > 0 {
		t = ((cx-ax)*dx + (cy-ay)*dy) / l2
		if t < 0 {
			t = 0
		}
		if t > 1 {
			t = 1
		}
	}
	px, py := ax+t*dx-cx, ay+t*dy-cy
	return px*px+py*py < r*r
}

// magicalDarknessBlocksSight: magical darkness is opaque (the aura's own text: "Darkvision can't
// penetrate this area"), so the viewer can't see the target when the target stands in the emanation,
// the viewer stands in it, or the line between them crosses it - unless the viewer has a sense that
// pierces magical darkness and it reaches the target. The origin's own square is not in its
// emanation (SRD 5.2.1 "Emanation": the origin is excluded unless the creator decides otherwise),
// but the ring around it still blocks the line. No battle map, no block: the area's position can't
// be known (the readback in monsterLightingAt degrades the same way).
func magicalDarknessBlocksSight(lobbyID, viewerID, targetID int) bool {
	rows := magicalDarknessRows(lobbyID)
	if len(rows) == 0 || viewerID == 0 || targetID == 0 {
		return false
	}
	battle, ok := activeEncounterMap(lobbyID)
	if !ok {
		return false
	}
	v, okV := battle.token(viewerID)
	t, okT := battle.token(targetID)
	if !okV || !okT {
		return false
	}
	for _, r := range rows {
		origin, ok := battle.token(r.source)
		if !ok {
			continue
		}
		insideV := viewerID != r.source && tokenDistanceFeet(origin, v) <= r.radius
		insideT := targetID != r.source && tokenDistanceFeet(origin, t) <= r.radius
		if !insideV && !insideT && !segmentCrossesEmanation(v.Position, t.Position, origin.Position, r.radius) {
			continue
		}
		if piercesMagicalDarknessWithin(lobbyID, viewerID, tokenDistanceFeet(v, t)) {
			continue
		}
		return true
	}
	return false
}

// piercesMagicalDarkness: the senses that see in magical darkness - Blindsight, Truesight, and a
// character's Devil's Sight ("darkness, both magical and nonmagical"). Darkvision is not one.
func piercesMagicalDarkness(lobbyID, creatureID int) bool {
	if creatureID > 0 && !isCompanionCombatID(creatureID) {
		_, blindsight, truesight := getCharacterVision(creatureID)
		return blindsight > 0 || truesight > 0 || hasInvocation(creatureID, "devils-sight")
	}
	if creatureUnimpededDarkvision(lobbyID, creatureID) {
		if darkvision, _, _ := creatureVision(lobbyID, creatureID); darkvision > 0 {
			return true
		}
	}
	if creatureID < 0 {
		key := ""
		for _, entry := range combatEntries(lobbyID) {
			if asInt(entry["id"]) == creatureID {
				key = sprintAny(entry["monster_key"])
				break
			}
		}
		if meta := monsterMetaFor(key); meta != nil {
			s := strings.ToLower(meta.Senses)
			return strings.Contains(s, "blindsight") || strings.Contains(s, "truesight")
		}
	}
	return false
}

// resolveMagicalDarkness establishes the emanation (the resolveMonsterEffectText branch). A preview
// establishes nothing.
func resolveMagicalDarkness(r *http.Request, lobbyID, monsterID int, entryName, text string, apply bool, notes []string) []string {
	name := creatureName(lobbyID, monsterID)
	m := magicalDarknessRe.FindStringSubmatch(text)
	radius, _ := strconv.Atoi(m[1])
	if !apply {
		return append([]string{fmt.Sprintf("%s: Magical Darkness would fill a %d-foot Emanation around %s. Nothing is established.", entryName, radius, name)}, notes...)
	}
	if err := startMagicalDarkness(lobbyID, monsterID, radius); err != nil {
		log.Printf("magical darkness: couldn't record the emanation for %d in campaign %d: %v", monsterID, lobbyID, err)
		return append([]string{fmt.Sprintf("%s: the Magical Darkness could not be recorded; nothing is established. Try again.", entryName)}, notes...)
	}
	if err := startMonsterConcentration(lobbyID, monsterID, entryName); err != nil {
		concentrationFailure(lobbyID, entryName, err)
		db.Exec(`DELETE FROM combat_effects WHERE lobby_id=$1 AND kind='magical_darkness' AND source_id=$2`, lobbyID, monsterID)
		return append([]string{fmt.Sprintf("%s: the Magical Darkness could not be recorded; nothing is established. Try again.", entryName)}, notes...)
	}
	markExecuted(r) // the server established it: the (1/Day) use is counted (monster_day_uses.go)
	return append([]string{fmt.Sprintf("%s: Magical Darkness fills a %d-foot Emanation around %s. Darkvision can't penetrate it and no light illuminates it; it lasts while %s maintains Concentration, up to 10 minutes.", entryName, radius, name, name)}, notes...)
}
