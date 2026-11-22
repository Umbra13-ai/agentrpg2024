package main

// A creature's own senses and light-bound traits, read from its stat block's wording (monsters and companions:
// creatureTraitsAndVitals), each read by the owner it belongs to:
//   - Darkvision unimpeded by magical Darkness (the SRD 5.2.1 devils' Senses line: "Darkvision 120 ft. (unimpeded by magical
//     Darkness)"): its Darkvision sees into Magical Darkness, read by piercesMagicalDarknessWithin / piercesMagicalDarkness
//     (monster_darkness_2024.go), the one sight model of that darkness;
//   - Sunlight Sensitivity ("While in sunlight, the kobold has Disadvantage on ability checks and attack rolls"; the variant "on
//     D20 Tests" covers saving throws too): read by attackSources, saveSources and the monster Hide action, while the creature
//     stands in sunlight (inSunlight: the scene's lighting set to sunlight by the GM, POST /api/gm/set-lighting {"lighting":
//     "sunlight"}, and not inside Magical Darkness);
//   - the traits that are communication or a sense the server has no state for (telepathy, ferrous-metal scent, a mind link):
//     rows of monsterTraitRules marked GM, so the GM's stat block says plainly that they are the GM's.

import (
	"regexp"
	"strings"
)

// unimpededDarkvisionRe: a Senses line whose Darkvision magical Darkness doesn't impede.
var unimpededDarkvisionRe = regexp.MustCompile(`(?i)\bDarkvision\s+\d+\s*ft\.?\s*\(unimpeded by magical Darkness\)`)

// sunlightRe: Sunlight Sensitivity in a stat block; group 1 "D20 Tests" for the variant that covers saving throws.
var sunlightRe = regexp.MustCompile(`(?i)While in sunlight, the [^,]+? has Disadvantage on (?:ability checks and attack rolls|(D20 Tests))`)

// creatureHasTraitText: a trait of the creature (a monster or companion in this fight) matches the wording.
func creatureHasTraitText(lobbyID, id int, re *regexp.Regexp) (srdMonsterEntry, bool) {
	if id == 0 || (id > 0 && !isCompanionCombatID(id)) {
		return srdMonsterEntry{}, false
	}
	traits, _ := creatureTraitsAndVitals(lobbyID, id)
	for _, t := range traits {
		if re.MatchString(t.Text) {
			return t, true
		}
	}
	return srdMonsterEntry{}, false
}

// creatureUnimpededDarkvision: the creature's Darkvision sees into Magical Darkness (its stat block's Senses say so; a monster or
// companion of the fight).
func creatureUnimpededDarkvision(lobbyID, id int) bool {
	meta := creatureMetaOf(lobbyID, id)
	return meta != nil && unimpededDarkvisionRe.MatchString(meta.Senses)
}

// inSunlight: the creature stands in sunlight: the scene is sunlit (the GM's lighting) and it isn't inside Magical Darkness.
func inSunlight(lobbyID, id int) bool {
	if lobbyID <= 0 || id == 0 || sceneLighting(lobbyID) != "sunlight" {
		return false
	}
	return !magicalDarknessAt(lobbyID, id)
}

// sunlightPenalty: the creature has Sunlight Sensitivity and stands in sunlight; saves is the variant that covers every D20 Test.
func sunlightPenalty(lobbyID, id int) (applies, saves bool) {
	t, ok := creatureHasTraitText(lobbyID, id, sunlightRe)
	if !ok || !inSunlight(lobbyID, id) {
		return false, false
	}
	m := sunlightRe.FindStringSubmatch(t.Text)
	return true, m != nil && strings.TrimSpace(m[1]) != ""
}
