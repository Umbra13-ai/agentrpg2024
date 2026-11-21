package main

import (
	"fmt"
	"strings"
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

// spellAllyTargets: the characters a spell that helps is aimed at: target_id / target_ids (characters), else the character the
// request or the description names, else the caster.
func spellAllyTargets(charID int, description string) []int {
	if t := spellCharacterTargets(charID); len(t) > 0 {
		return t
	}
	if id := aimedCharacter(description, charID); id > 0 {
		return []int{id}
	}
	return []int{charID}
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
