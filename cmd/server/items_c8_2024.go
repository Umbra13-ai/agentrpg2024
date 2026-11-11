package main

// Fixes from the C8 coverage session (level20-battle-tests/s38_c8.py, Nov 9 2026; FINDINGS F184-F185):
//	Talisman of Pure Good / Ultimate Evil  worn or held and attuned: +2 to spell attack rolls; Pure Rebuke / Ultimate End (a Magic action,
//	                        1 charge, target_id): DC 20 Dex (a Fiend or Undead / a Celestial has Disadvantage) or destroyed, else 4d6 Psychic;
//	                        the last charge destroys the talisman (F184)
//	Dragon Orb              "peer" (a Magic action): DC 15 Charisma save: control it, or be Charmed by it while attuned; only a controlled orb
//	                        casts its spells; "call dragons" once an hour (F185)

import (
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

func talismanActive(charID int, name string) bool {
	it := carriedItem(charID, name)
	if it == nil {
		return false
	}
	if loc := itemStr(it, "location"); loc != "worn" && loc != "held" {
		return false
	}
	_, ok := attunedTo(charID, name)
	return ok
}

// talismanSpellAttack: +2 to spell attack rolls while a talisman is worn or held (and attuned).
func talismanSpellAttack(charID int) int {
	for _, n := range []string{"talisman of pure good", "talisman of ultimate evil"} {
		if talismanActive(charID, n) {
			return 2
		}
	}
	return 0
}

func talismanRebuke(charID int, it invItem, req itemCastReq, good bool) string {
	name := itemStr(it, "name")
	base := "talisman of ultimate evil"
	if good {
		base = "talisman of pure good"
	}
	if !talismanActive(charID, base) {
		return "Cannot use the " + name + ": wear or hold it and attune to it first. Nothing was spent."
	}
	if req.TargetID == 0 {
		return "Name the creature on the ground within 120 feet with target_id. Nothing was spent."
	}
	note, refusal := chargedUse(charID, it, 1)
	if refusal != "" {
		return refusal + " Nothing was spent."
	}
	lobby := lobbyOfChar(charID)
	who := creatureName(lobby, req.TargetID)
	kind := ""
	if req.TargetID < 0 {
		kind = strings.ToLower(monsterTypeOf(lobby, req.TargetID))
	}
	dis := (good && (kind == "fiend" || kind == "undead")) || (!good && kind == "celestial")
	var saved bool
	if req.TargetID < 0 {
		saved, _ = monsterSave(lobby, req.TargetID, "dex", 20)
		if dis && saved {
			saved, _ = monsterSave(lobby, req.TargetID, "dex", 20) // Disadvantage: the second roll must succeed too
		}
	} else {
		saved, _ = characterSave(req.TargetID, "dex", 20)
	}
	label := "Ultimate End"
	if good {
		label = "Pure Rebuke"
	}
	disNote := ""
	if dis {
		disNote = ", with Disadvantage"
	}
	text := fmt.Sprintf("%s (a Magic action, 1 charge): a flaming fissure opens under %s (DC 20 Dexterity save%s): ", label, who, disNote)
	switch {
	case saved:
		dmg := game.RollDamage("4d6", false)
		var res string
		if req.TargetID < 0 {
			res, _ = damageMonster(lobby, req.TargetID, dmg, "psychic")
		} else {
			res = damageCharacter(req.TargetID, dmg, "psychic")
		}
		text += fmt.Sprintf("it saves and isn't cast in, but takes %d Psychic damage from the ordeal (4d6): %s.", dmg, strings.TrimSpace(res))
	case req.TargetID < 0:
		setMonsterHP(lobby, req.TargetID, 0)
		text += "it fails and falls into the fissure: it is destroyed, leaving no remains."
	default:
		db.Exec(`UPDATE characters SET hp = 0, is_dead = true WHERE id = $1`, req.TargetID)
		text += "it fails and falls into the fissure: it is destroyed, leaving no remains."
	}
	text += " The fissure closes." + note
	if left := carriedItem(charID, base); left != nil && itemNum(left, "charges") <= 0 && itemNum(left, "charges_max") > 0 {
		editInventory(charID, func(inv []invItem) []invItem {
			for _, x := range inv {
				if itemStr(x, "id") == itemStr(it, "id") {
					return takeOne(inv, x)
				}
			}
			return inv
		})
		if good {
			text += " That was the last charge: the talisman disperses into motes of golden light."
		} else {
			text += " That was the last charge: the talisman dissolves into foul-smelling slime."
		}
	}
	return text
}

// ---------------------------------------------------------------- Dragon Orb

func dragonOrb(charID int, it invItem, description string) string {
	if _, ok := attunedTo(charID, "dragon orb"); !ok {
		return "Cannot use the Dragon Orb: you must be attuned to it (POST /api/characters/attune). Nothing was spent."
	}
	d := strings.ToLower(description)
	lobby := lobbyOfChar(charID)
	if strings.Contains(d, "call") {
		if !itemBool(it, "orb_controlled") {
			return "You don't control the Dragon Orb: peer into it first (a DC 15 Charisma save). Nothing was spent."
		}
		if dawnUsed(it, "orb_call") {
			return "The Dragon Orb's call can't be used again so soon (once an hour). Nothing was spent."
		}
		markDawnUsed(charID, it, "orb_call")
		return "The orb issues a telepathic call 40 miles in every direction (a Magic action): chromatic dragons in range feel compelled to come to it by the most direct route, perhaps Hostile for being compelled. The GM runs them. (Again after an hour.)"
	}
	if itemBool(it, "orb_controlled") {
		return "You already control the Dragon Orb: cast its spells with the cast action and \"item\": \"Dragon Orb\" (Cure Wounds level 9: 4 charges, Daylight 1, Death Ward 2, Detect Magic 0, Scrying 3), or \"call dragons\"."
	}
	saved, total := characterSave(charID, "cha", 15)
	if saved {
		editInventory(charID, func(inv []invItem) []invItem {
			for _, x := range inv {
				if itemStr(x, "id") == itemStr(it, "id") {
					x["orb_controlled"] = true
				}
			}
			return inv
		})
		return fmt.Sprintf("You peer into the orb's swirling mist (a Magic action): Charisma save %d vs DC 15, success. You control the Dragon Orb while you stay attuned: its spells (cast with \"item\": \"Dragon Orb\") and its call are yours.", total)
	}
	addMonsterCondition(lobby, charID, "charmed", "Dragon Orb", 0, "", 0)
	return fmt.Sprintf("You peer into the orb's swirling mist (a Magic action): Charisma save %d vs DC 15, failure. The orb Charms you for as long as you stay attuned to it: you can't end the attunement, and it casts Suggestion on you at will (DC 18), urging its own ends. The GM plays the dragon's will.", total)
}

// dragonOrbControl: an orb's spells only for the one who controls it.
func dragonOrbControl(it invItem) string {
	if strings.HasPrefix(strings.ToLower(itemStr(it, "name")), "dragon orb") && !itemBool(it, "orb_controlled") {
		return "You don't control the Dragon Orb: peer into it first (use_item, a DC 15 Charisma save). Nothing was spent."
	}
	return ""
}
