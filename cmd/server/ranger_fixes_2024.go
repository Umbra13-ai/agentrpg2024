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
	if w, ok := srdWeapons[baseWeaponOf(heldItem)]; ok {
		dtype = strings.ToLower(w.DamageType)
	}
	d, how := adjustMonsterDamage(monsterKey, dtype, game.RollDamage("2d6", crit))
	return d, fmt.Sprintf(" Vicious Weapon: +%d %s%s.", d, titleCase(dtype), map[string]string{"resistant": " (it resists)", "immune": " (it is immune)", "vulnerable": " (it is vulnerable)"}[how])
}

// scimitarOfSpeedReady: "" when the character holds an attuned Scimitar of Speed, else why not.
func scimitarOfSpeedReady(charID int) string {
	h := handsOf(charID)
	if !strings.HasPrefix(strings.ToLower(h.Main), "scimitar of speed") && !strings.HasPrefix(strings.ToLower(h.Off), "scimitar of speed") {
		return "A Bonus Action attack (bonus_attack) needs a Scimitar of Speed in hand. Nothing was spent."
	}
	if _, ok := attunedTo(charID, "scimitar of speed"); !ok {
		return "The Scimitar of Speed's Bonus Action attack needs attunement (POST /api/characters/attune). Nothing was spent."
	}
	return ""
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
