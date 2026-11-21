package main

import (
	"regexp"
	"strconv"
	"strings"
)

// Apply continuous emissions. A conditional light source needs its own
// activation state and therefore remains on the GM workflow.
var monsterLightRe = regexp.MustCompile(`(?i)^\s*The [\pL][\pL '’\-]*? sheds Bright Light in a (\d+)\s*-?\s*foot radius and Dim Light for an additional (\d+) feet?\.\s*$`)

// monsterLightToggleRe: a light the monster switches on and off ("Ignited Illumination. The magmin sets itself ablaze or extinguishes its
// flames. While ablaze, the magmin sheds Bright Light in a 10-foot radius and Dim Light for an additional 10 feet."): it sheds the light
// while its "ablaze" combat effect lasts (resolveLightToggle sets and clears it).
var monsterLightToggleRe = regexp.MustCompile(`(?i)sets itself ablaze or extinguishes its flames\. While ablaze, the [\pL][\pL '’\-]*? sheds Bright Light in a (\d+)\s*-?\s*foot radius and Dim Light for an additional (\d+) feet`)

// monsterLightRadii: the bright and dim radii of every light the monster sheds now (a trait's always; a switched light while it is on).
func monsterLightRadii(lobby, id int, meta *monsterMeta) [][]string {
	var out [][]string
	for _, trait := range meta.Traits {
		if radii := monsterLightRe.FindStringSubmatch(strings.TrimSpace(trait.Text)); radii != nil {
			out = append(out, radii)
		}
	}
	for _, group := range [][]srdMonsterEntry{meta.Actions, meta.BonusActions, meta.Traits} {
		for _, e := range group {
			if radii := monsterLightToggleRe.FindStringSubmatch(e.Text); radii != nil && monsterAblaze(lobby, id) {
				out = append(out, radii)
			}
		}
	}
	return out
}

// monsterAblaze: the monster's switched light is on.
func monsterAblaze(lobby, id int) bool {
	for _, e := range combatEffectsOn(lobby, id) {
		if e.Kind == "ablaze" {
			return true
		}
	}
	return false
}

// resolveLightToggle: the entry that switches the monster's light (use "ignite" or "extinguish"; without use it switches to the other
// state). handled is false when the text is no such entry.
func resolveLightToggle(lobby, id int, name, entryName, text, use string, apply bool) (line string, handled bool) {
	m := monsterLightToggleRe.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	on := !monsterAblaze(lobby, id)
	switch u := strings.ToLower(strings.TrimSpace(use)); {
	case strings.Contains(u, "extinguish"), u == "off", strings.Contains(u, "put out"):
		on = false
	case strings.Contains(u, "ignite"), strings.Contains(u, "ablaze"), u == "on", strings.Contains(u, "light"):
		on = true
	}
	if apply {
		db.Exec(`DELETE FROM combat_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = 'ablaze'`, lobby, id)
		if on {
			db.Exec(`INSERT INTO combat_effects (lobby_id, target_id, source_id, kind, text) VALUES ($1,$2,$2,'ablaze',$3)`, lobby, id,
				"Ablaze: sheds Bright Light in a "+m[1]+"-foot radius and Dim Light for an additional "+m[2]+" feet")
		}
	}
	if on {
		return name + " uses " + entryName + ": it is ablaze, shedding Bright Light in a " + m[1] + "-foot radius and Dim Light for an additional " + m[2] + " feet (the battle map's lighting reads it).", true
	}
	return name + " uses " + entryName + ": its flames go out; it sheds no light.", true
}

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
		for _, radii := range monsterLightRadii(lobby, id, meta) {
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
