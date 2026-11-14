package main

// Class features built after the 2024 rules audit (part C):
//   Fleet Step (Warrior of the Open Hand 11): after a Bonus Action other than Step of the Wind, the monk can use Step of the Wind right
//     away without another Bonus Action (once per turn; the Focus Point cost, if any, still applies).
//   Second-Story Work (Thief 3): a Climb Speed equal to the Speed, and jump distance from Dexterity instead of Strength (the server uses
//     the higher score).
//   Acrobatic Movement (Monk 9): while not wearing armor or wielding a Shield, the monk moves along vertical surfaces and across liquids
//     on its turn without falling. The battle map has no climbable walls or water terrain ('~' is any difficult terrain), so this is
//     shown in my-turn for the GM.
//   Evocation Savant (Evoker 3): two Evocation spells of level 2 or lower, then one per new spell slot level, added to the spellbook
//     free (the server charges nothing for spellbook additions; GET /api/characters/{id}/spells lists the allowance).

import (
	"fmt"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

var (
	stepOfWindTurn sync.Map // character id -> combat turn marker when Step of the Wind used the Bonus Action
	fleetStepTurn  sync.Map // character id -> combat turn marker when Fleet Step was used
)

// noteStepOfWind records that Step of the Wind took this turn's Bonus Action.
func noteStepOfWind(charID int) {
	if m := combatTurnMarker(lobbyOfChar(charID)); m != "" {
		stepOfWindTurn.Store(charID, m)
	}
}

// fleetStep: an Open Hand monk of level 11+ whose Bonus Action this turn went to something other than Step of the Wind may use Step
// of the Wind now without one. It marks the use and returns true.
func fleetStep(charID int) bool {
	class, sub, level := subclassOf(charID)
	if class != "monk" || sub != "open-hand" || level < 11 {
		return false
	}
	var bonusUsed bool
	db.QueryRow(`SELECT COALESCE(bonus_action_used,false) FROM characters WHERE id = $1`, charID).Scan(&bonusUsed)
	m := combatTurnMarker(lobbyOfChar(charID))
	if !bonusUsed || m == "" {
		return false
	}
	if v, ok := stepOfWindTurn.Load(charID); ok && v.(string) == m {
		return false // the Bonus Action was Step of the Wind itself
	}
	if v, ok := fleetStepTurn.Load(charID); ok && v.(string) == m {
		return false
	}
	fleetStepTurn.Store(charID, m)
	return true
}

// gazeOfTwoMinds (Eldritch Invocation, Warlock 5): a Bonus Action to touch a willing creature and perceive through its senses until the
// end of your next turn; a Bonus Action on later turns keeps the link going. While you perceive through it you have its special
// senses, and you can cast spells as if you were in your space or its space if the two of you are within 60 feet of each other.
func gazeOfTwoMinds(charID int, description string) string {
	if !hasInvocation(charID, "gaze-of-two-minds") {
		return "You don't have the Gaze of Two Minds invocation."
	}
	target := parseTargetFromDescription(description, charID)
	if target <= 0 || target == charID {
		return "Gaze of Two Minds: name the willing creature you touch (an ally's name in the description). Nothing was spent."
	}
	return fmt.Sprintf("👁️ Gaze of Two Minds: you touch %s and perceive through its senses until the end of your next turn (a Bonus Action on a later turn keeps the link going while you're on the same plane). You have its special senses, and you can cast spells as if you were in its space while the two of you are within 60 feet of each other.", characterNameOf(target))
}

// jumpScore is the score a jump uses: Strength, or Dexterity for a Thief of level 3+ (Second-Story Work) when it is higher.
func jumpScore(charID, str, dex int) (int, string) {
	if _, sub, level := subclassOf(charID); sub == "thief" && level >= 3 && dex > str {
		return dex, " (Dexterity: Second-Story Work)"
	}
	return str, ""
}

// movementFeatureNotes: climbing and Acrobatic Movement, for my-turn.
func movementFeatureNotes(charID int) map[string]interface{} {
	class, sub, level := subclassOf(charID)
	out := map[string]interface{}{}
	var species string
	db.QueryRow(`SELECT COALESCE(species,'human') FROM characters WHERE id = $1`, charID).Scan(&species)
	if sub == "thief" && level >= 3 {
		out["climb_speed_ft"] = characterSpeed(charID, species)
		out["second_story_work"] = "Climber: a Climb Speed equal to your Speed (climbing costs no extra movement). Jumper: your jumps use Dexterity."
	}
	if class == "monk" && level >= 9 {
		var armor string
		var shield bool
		db.QueryRow(`SELECT COALESCE(equipped_armor,''), COALESCE(equipped_shield,false) FROM characters WHERE id = $1`, charID).Scan(&armor, &shield)
		if (armor == "" || strings.EqualFold(armor, "none")) && !shield {
			out["acrobatic_movement"] = "You can move along vertical surfaces and across liquids on your turn without falling during the movement (the GM places your token)."
		} else {
			out["acrobatic_movement"] = "Not now: it needs no armor and no Shield."
		}
	}
	return out
}

// evocationSavantInfo: the free Evocation spells of an Evoker (level 3+) for the spellbook.
func evocationSavantInfo(charID int) map[string]interface{} {
	class, sub, level := subclassOf(charID)
	if class != "wizard" || sub != "evocation" || level < 3 {
		return nil
	}
	maxSlot := 0
	for lvl, n := range game.SpellSlots("wizard", level) {
		if n > 0 && lvl > maxSlot {
			maxSlot = lvl
		}
	}
	extra := maxSlot - 2 // one per spell slot level gained after level 3 (where the level 2 slots come)
	if extra < 0 {
		extra = 0
	}
	var evocations []string
	for slug, sp := range srdSpellsMemory {
		if strings.EqualFold(sp.School, "evocation") && sp.Level >= 1 && sp.Level <= maxSlot && containsStr(getClassSpellList("wizard"), slug) && !inSpellbookStrict(charID, slug) {
			evocations = append(evocations, slug)
		}
	}
	return map[string]interface{}{
		"free_spells":   2 + extra,
		"rule":          fmt.Sprintf("Evocation Savant: two Wizard Evocation spells of level 2 or lower, and one more of a level you have slots for each time you gained a new spell slot level (%d so far), added to your spellbook for free.", 2+extra),
		"how":           `PUT /api/characters/{id}/spells {"add": ["fireball"]}: the spellbook takes them without cost.`,
		"not_yet_owned": len(evocations),
	}
}

// inSpellbookStrict: the spell is in the recorded spellbook (inSpellbook allows anything when none is recorded).
func inSpellbookStrict(charID int, slug string) bool {
	return containsStr(jsonListOf(charID, "known_spells"), slug)
}

func jsonListOf(charID int, col string) []string {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(`+col+`,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	return jsonList(raw)
}

// savantFit: how a Wizard's free spellbook spells fit the Spellbook feature (any Wizard spell: 6 at level 1, two more each level)
// and an Evoker's Evocation Savant (two Evocation spells of level 2 or lower at level 3, one Evocation spell each new spell slot
// level). baseUsed is how many free spells fall to the Spellbook feature once every Evocation spell that can go to Savant has;
// savantOpen is how many Savant spells are still to choose. Rule, not count: only Evocation spells fill Savant (setup checker, Nov
// 14: any spell was accepted as a Savant spell).
func savantFit(charID, level int, list []string) (baseUsed, base, savantOpen int) {
	base = 6 + 2*(level-1)
	low, extra := 0, 0
	if _, wsub, _ := subclassOf(charID); strings.EqualFold(wsub, "evocation") && level >= 3 {
		low = 2
		for _, l := range []int{5, 7, 9, 11, 13, 15, 17} {
			if level >= l {
				extra++
			}
		}
	}
	origin := originSpellsOf(charID)
	copied := spellbookCopied(charID)
	n, e2, e3 := 0, 0, 0
	for _, s := range list {
		if _, o := origin[s]; o || containsStr(copied, s) {
			continue
		}
		sp, ok := srdSpellsMemory[s]
		if !ok || sp.Level == 0 {
			continue
		}
		switch {
		case !strings.EqualFold(sp.School, "evocation"):
			n++
		case sp.Level <= 2:
			e2++
		default:
			e3++
		}
	}
	a := minInt(low, e2)
	rest := e2 - a + e3
	b := minInt(extra, rest)
	return n + rest - b, base, (low - a) + (extra - b)
}
