package main

import (
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// A spell or feature that heals or restores works on a living creature (SRD 5.2.1: a dead creature is no longer a creature but an
// object; Revivify, Raise Dead, Resurrection and True Resurrection are what work on the dead). This file is the one owner of that
// rule, so a heal aimed at a dead ally is refused before anything is spent (no slot, no Channel Divinity, no pool, no potion, no
// action) and a pending turn takes its fallback (feedback log 67, I-ALDRIC-4: a queued Healing Word spent Aldric's last 5th-level
// slot on Doran, who had died earlier in the round, and the fallback never ran because the cast wasn't a refusal):
//   - which spells help a living creature: spellRestores (the spell data's healing, and restoringSpells); the spells that bring the
//     dead back (revivalSpells, cleric_fixes_2024.go) never do;
//   - which features: the action table's "heals" tag (action_types_2024.go);
//   - whom a spell that helps is aimed at: spellAllyTargets, which the healing spells (applySpellHealing), Lesser and Greater
//     Restoration and Spare the Dying read too;
//   - the refusal: livingTargetRefusal, asked once at the start of resolveAction (every route: POST /api/action, a pending turn's
//     step, a readied action, the GM's stand-in), and deadTargetRefusal, which the potion given to an ally asks.

// restoringSpells: spells without a healing amount that restore a creature (end a condition, a curse, stabilize, ward it).
var restoringSpells = map[string]bool{
	"lesser-restoration": true, "greater-restoration": true, "remove-curse": true, "spare-the-dying": true,
	"power-word-heal": true, "death-ward": true,
}

// spellRestores: the spell heals or restores a living creature (and so can't be aimed at the dead).
func spellRestores(key string, sp SRDSpell) bool {
	if _, revives := revivalSpells[key]; revives {
		return false
	}
	return sp.Healing != "" || restoringSpells[key]
}

// allyAim is whom a spell that helps is aimed at (spellAllyAim).
type allyAim struct {
	IDs        []int // characters and companions: target_id / target_ids, else every character the description names
	Combatants []int // creatures of the fight a negative target_id names (an NPC ally the GM added): their own Hit Point owner
	Others     bool  // the description names creatures that are none of these (NPCs the server doesn't track): the GM applies it to them
	Described  bool  // IDs are the characters the description names (the request named none)
}

// spellAllyAim: whom a spell that helps is aimed at, the one answer for every such spell (healing, Lesser and Greater Restoration,
// Spare the Dying, the conditions a spell ends, the dead-target refusal). The request's targets first; else every character the
// description names (Prayer of Healing's five, in order); else the caster, but only when the description names no one else
// (empty, "myself", or the spell's own words): a description that names creatures the server doesn't know (rescued prisoners)
// is never turned into a heal on the caster (playtest I-CALLA-24).
func spellAllyAim(charID int, description string) allyAim {
	var a allyAim
	a.IDs = spellCharacterTargets(charID)
	if v, ok := castTargets.Load(charID); ok {
		for _, id := range v.([]int) {
			if id < 0 {
				a.Combatants = append(a.Combatants, id)
			}
		}
	}
	if v, ok := attackTargets.Load(charID); ok { // a cast's negative target_id is kept with the attack's (handleAction)
		if id, _ := v.(int); id < 0 && !containsInt(a.Combatants, id) {
			a.Combatants = append(a.Combatants, id)
		}
	}
	if len(a.IDs) > 0 || len(a.Combatants) > 0 {
		return a
	}
	if named := parseTargetsFromDescription(description, charID); len(named) > 0 {
		a.IDs, a.Described = named, true
		return a
	}
	if id := aimedCharacter(description, charID); id > 0 {
		a.IDs, a.Described = []int{id}, true
		return a
	}
	if describesOthers(description) {
		a.Others = true
		return a
	}
	a.IDs = []int{charID}
	return a
}

// spellAllyTargets: the characters (and companions) a spell that helps is aimed at (spellAllyAim's IDs; none when the description
// names only creatures the server doesn't track).
func spellAllyTargets(charID int, description string) []int {
	return spellAllyAim(charID, description).IDs
}

// othersNote: what the reply says when a helping spell names creatures the server doesn't track.
const othersNote = " No character or creature of the fight was named, so the GM applies it to the creatures the description names" +
	" (to aim at a character, send target_character_id or target_ids; at a creature of the fight, its target_id)."

// describesOthers: the description names someone, once the casting words are taken away ("I cast Cure Wounds at 2nd level" names no
// one; "over the five freed prisoners" does). "myself", "own" and the like name the caster.
func describesOthers(description string) bool {
	casting := map[string]bool{"i": true, "cast": true, "casts": true, "casting": true, "a": true, "an": true, "the": true, "at": true,
		"on": true, "to": true, "with": true, "using": true, "use": true, "uses": true, "level": true, "slot": true, "spell": true,
		"upcast": true, "upcasting": true, "1st": true, "2nd": true, "3rd": true, "4th": true, "5th": true, "6th": true, "7th": true,
		"8th": true, "9th": true, "of": true, "as": true, "and": true, "my": true, "bonus": true, "action": true, "reaction": true,
		"heal": true, "healing": true, "cure": true, "wounds": true, "word": true, "prayer": true, "mass": true, "restoration": true,
		"lesser": true, "greater": true, "spare": true, "dying": true, "aid": true, "power": true, "for": true, "it": true, "now": true,
		// what a restoring spell does, not whom: "cure the poison", "end the deafness", "remove the curse"
		"cures": true, "end": true, "ends": true, "remove": true, "removes": true, "restore": true, "restores": true, "lift": true,
		"break": true, "stop": true, "curse": true, "disease": true, "condition": true, "reduction": true, "effect": true, "this": true}
	namesCondition := func(w string) bool { // "poison", "deafness", "paralysis": a condition's stem (conditionStem)
		for _, c := range game.AllConditions {
			if st := conditionStem(c); len(st) >= 4 && strings.HasPrefix(w, st[:4]) {
				return true
			}
		}
		return false
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(description), func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') }) {
		switch {
		case casting[w] || len(w) == 1 || (w[0] >= '0' && w[0] <= '9') || namesCondition(w):
		case w == "myself" || w == "me" || w == "self" || w == "own" || w == "herself" || w == "himself" || w == "themself" || w == "itself":
			return false
		default:
			return true
		}
	}
	return false
}

// healFeatureTarget: the character a feature that heals is aimed at: the request's target_character_id (or a positive target_id),
// else, when the request aims at no monster, a character the description names (0: none, the feature's own default).
func healFeatureTarget(charID int, description string) int {
	if v, ok := aimedTargets.Load(charID); ok {
		if id, _ := v.(int); id > 0 {
			return id
		}
	}
	if _, _, atMonster := attackedMonster(charID); atMonster {
		return 0 // Divine Spark at an enemy: the names in the description aren't its target
	}
	return characterNamedIn(description, charID)
}

// livingTargetRefusal: "" unless the action heals or restores and is aimed at a dead character; then the refusal, made before
// anything is spent.
func livingTargetRefusal(action, description string, charID int, castSpellSlug ...string) string {
	if action == "cast" {
		key := ""
		if len(castSpellSlug) > 0 {
			key = castSpellSlug[0]
		}
		if key == "" {
			key = parseSpellFromDescription(description)
		}
		key = normalizeSpellSlug(key)
		sp, ok := srdSpellsMemory[key]
		if !ok || !spellRestores(key, sp) {
			return ""
		}
		if r := allyAimRefusal(charID, sp, description); r != "" {
			return r
		}
		return deadTargetRefusal("cast "+sp.Name, spellAllyTargets(charID, description))
	}
	t, ok := lookupActionType(action)
	if !ok || !t.hasTag("heals") {
		return ""
	}
	if id := healFeatureTarget(charID, description); id > 0 {
		return deadTargetRefusal("use "+t.Title, []int{id})
	}
	return ""
}

// deadTargetRefusal: "Cannot cast Healing Word: Doran Ashwake is dead ..." for the first dead character of ids ("" when none is).
// what is the verb and the thing: "cast Healing Word", "use Lay on Hands", "give the Potion of Healing to", "use Healing Touch on".
func deadTargetRefusal(what string, ids []int) string {
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		var name string
		var dead bool
		if db.QueryRow(`SELECT name, COALESCE(is_dead,false) FROM characters WHERE id = $1`, id).Scan(&name, &dead) != nil || !dead {
			continue
		}
		subject := strings.TrimSpace(what)
		if strings.HasSuffix(subject, " to") || strings.HasSuffix(subject, " on") {
			subject += " " + name
		}
		return fmt.Sprintf("Cannot %s: %s is dead, and healing and restoring work only on a living creature (Revivify, Raise Dead, "+
			"Resurrection and True Resurrection bring back the dead). Nothing was spent.", subject, name)
	}
	return ""
}

// allyAimRefusal: the refusal of a cast that heals or restores aimed at more creatures than the spell reaches (healTargetLimit: Cure
// Wounds one, Mass Healing Word six), made before anything is spent: the creatures the request names, or the characters the description
// names when it names none (I-CALLA-31: a Cure Wounds whose description named the caster, an ally and an NPC guard healed both
// characters). "" when the aim fits.
func allyAimRefusal(charID int, sp SRDSpell, description string) string {
	limit := healTargetLimit(sp)
	aim := spellAllyAim(charID, description)
	if limit == 0 || len(aim.IDs)+len(aim.Combatants) <= limit {
		return ""
	}
	lobby := lobbyOfChar(charID)
	var names []string
	for _, id := range aim.IDs {
		names = append(names, creatureName(lobby, id))
	}
	for _, id := range aim.Combatants {
		names = append(names, creatureName(lobby, id))
	}
	reach := "one creature"
	if limit > 1 {
		reach = fmt.Sprintf("up to %d creatures", limit)
	}
	how := "it is aimed at " + joinNames(names)
	if aim.Described {
		how = "the description names " + joinNames(names) + " and no target was sent"
	}
	return fmt.Sprintf("Cannot cast %s: it reaches %s, and %s. Name the creature in target_character_id or target_ids (a creature of the fight: "+
		"its target_id); to aim it at a creature the server doesn't track, leave the characters' names out of the description and the GM applies "+
		"it. Nothing was spent.", sp.Name, reach, how)
}
