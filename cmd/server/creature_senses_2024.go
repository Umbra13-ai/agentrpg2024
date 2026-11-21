package main

// A creature's own senses and light-bound traits, read from its stat block's wording (monsters and companions:
// creatureTraitsAndVitals), each read by the owner it belongs to:
//   - Devil's Sight ("Magical Darkness doesn't impede the imp's Darkvision"): its Darkvision sees into Magical Darkness, read by
//     piercesMagicalDarknessWithin / piercesMagicalDarkness (monster_darkness_2024.go), the one sight model of that darkness;
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

// devilsSightRe: Devil's Sight in a stat block.
var devilsSightRe = regexp.MustCompile(`(?i)Magical Darkness doesn['’]t impede the [^.]*?\bDarkvision\b`)

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

// creatureDevilsSight: the creature's Darkvision sees into Magical Darkness (a monster's or companion's Devil's Sight trait).
func creatureDevilsSight(lobbyID, id int) bool {
	_, ok := creatureHasTraitText(lobbyID, id, devilsSightRe)
	return ok
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
