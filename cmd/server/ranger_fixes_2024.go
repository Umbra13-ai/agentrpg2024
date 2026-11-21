package main

// Fixes from Vex's coverage session (level20-battle-tests/s33_vex_ranger.py, Nov 9 2026; FINDINGS F143-F146):
//	Vicious Weapon     +2d6 damage of the weapon's type on a hit (F143, legendary_more_2024.go weaponHitEffects)
//	Scimitar of Speed  attuned, in hand: action "bonus_attack" makes one attack with it as a Bonus Action each turn (F144, actions.go)
//	Cloak of the Bat   worn and attuned: Advantage on Dexterity (Stealth) checks (F145, with the Boots of Elvenkind hook)
//	Colossus Slayer    read the target's Hit Points from the characters table only, so it never fired against a monster (F146)

import (
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

func viciousHit(monsterKey, heldItem string, crit bool) (int, string) {
	dtype := "slashing"
	if _, w, ok := findWeapon(baseWeaponOf(heldItem)); ok {
		dtype = strings.ToLower(w.DamageType)
	}
	d, how := adjustMonsterExtra(monsterKey, dtype, game.RollDamage("2d6", crit))
	if how == "absorbed" {
		return d, absorbedExtraText("Vicious Weapon", dtype, d)
	}
	return d, fmt.Sprintf(" Vicious Weapon: +%d %s%s.", d, titleCase(dtype), map[string]string{"resistant": " (it resists)", "immune": " (it is immune)", "vulnerable": " (it is vulnerable)"}[how])
}

// scimitarOfSpeedReady: "" when the character holds an attuned Scimitar of Speed, else why not.
func scimitarOfSpeedReady(charID int) string {
	return actionRefusal("bonus_attack", charID).text() // the action table's Who (action_types_2024.go)
}

// heldAndAttuned: a magic item (its name, or the start of it) is in one of the character's hands, and the character is attuned to it.
func heldAndAttuned(charID int, item string) (held, attuned bool) {
	h, name := handsOf(charID), strings.ToLower(item)
	held = strings.HasPrefix(strings.ToLower(h.Main), name) || strings.HasPrefix(strings.ToLower(h.Off), name)
	_, attuned = attunedTo(charID, name)
	return held, attuned
}

// cloakOfTheBatStealth: Advantage on Stealth while the cloak is worn and attuned.
func cloakOfTheBatStealth(charID int, skill string) bool {
	if !strings.EqualFold(strings.ReplaceAll(skill, " ", "_"), "stealth") {
		return false
	}
	if _, ok := itemActive(charID, "cloak of the bat", "worn"); !ok {
		return false
	}
	_, att := attunedTo(charID, "cloak of the bat")
	return att
}

// woundedTarget: the target (a character, or a monster with a negative id) is below its Hit Point maximum.
func woundedTarget(charID, targetID int) bool {
	if targetID == 0 { // a monster attacked with target_id arrives through attackTargets, not the description
		if _, m, ok := attackedMonster(charID); ok {
			targetID = m
		}
	}
	if targetID == 0 {
		return false
	}
	if targetID < 0 {
		for _, e := range combatEntries(lobbyOfChar(charID)) {
			if asInt(e["id"]) == targetID {
				return asInt(e["hp"]) < asInt(e["max_hp"])
			}
		}
		return false
	}
	var hp, maxHP int
	return db.QueryRow(`SELECT hp, max_hp FROM characters WHERE id = $1`, targetID).Scan(&hp, &maxHP) == nil && hp < maxHP
}
