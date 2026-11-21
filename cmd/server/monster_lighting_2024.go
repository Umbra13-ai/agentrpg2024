package main

import (
	"regexp"
	"strconv"
	"strings"
)

// Apply continuous emissions. A conditional light source needs its own
// activation state and therefore remains on the GM workflow.
var monsterLightRe = regexp.MustCompile(`(?i)^\s*The [\pL][\pL '’\-]*? sheds Bright Light in a (\d+)\s*-?\s*foot radius and Dim Light for an additional (\d+) feet?\.\s*$`)

// monsterLightingAt adds mapped monster light to the scene's ambient lighting
// at a particular creature. The map's ordinary wall and door geometry applies.
func monsterLightingAt(lobby, target int) string {
	lighting := getCampaignLighting(lobby)
	if target == 0 {
		return lighting
	}
	if magicalDarknessAt(lobby, target) { // no light can illuminate the area, not even a bright scene
		return "magical darkness"
	}
	if lighting == "bright" {
		return lighting
	}
	battle, ok := activeEncounterMap(lobby)
	if !ok {
		return lighting
	}
	at, ok := battle.token(target)
	if !ok {
		return lighting
	}
	for _, entry := range combatEntries(lobby) {
		id := asInt(entry["id"])
		if id >= 0 || asInt(entry["hp"]) <= 0 {
			continue
		}
		origin, ok := battle.token(id)
		if !ok || !battle.canSee(origin, at) {
			continue
		}
		meta := monsterMetaFor(sprintAny(entry["monster_key"]))
		if meta == nil {
			continue
		}
		distance := tokenDistanceFeet(origin, at)
		for _, trait := range meta.Traits {
			radii := monsterLightRe.FindStringSubmatch(strings.TrimSpace(trait.Text))
			if radii == nil {
				continue
			}
			bright, brightErr := strconv.Atoi(radii[1])
			dim, dimErr := strconv.Atoi(radii[2])
			if brightErr != nil || dimErr != nil {
				continue
			}
			if distance <= bright {
				return "bright"
			}
			if distance-bright <= dim {
				lighting = "dim"
			}
		}
	}
	return lighting
}
