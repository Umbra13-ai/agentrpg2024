package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Dodge (SRD 5.2.1): "Until the start of your next turn, any attack roll made against you has Disadvantage if you can see the
// attacker, and you make Dexterity saving throws with Advantage. You lose this benefit if you have the Incapacitated condition
// or if your Speed is 0." The marker is "dodging" in the character's conditions.

// dodgeActive: the character took the Dodge action and still has its benefit (not Incapacitated, Speed not 0). It is read in the
// two shared places only, so every attack roll against the character and every Dexterity save gets it: buffAttackDisadvantage
// and buffSaveAdvantage (buff_spells_2024.go).
func dodgeActive(charID int) bool {
	if charID <= 0 || db == nil || !hasCondition(charID, "dodging") || isIncapacitated(charID) {
		return false
	}
	return !hasCondition(charID, "grappled") && !hasCondition(charID, "restrained") // Speed 0 ends the benefit
}

// endDodge drops the marker when the character's next turn starts.
func endDodge(charID int) {
	if db == nil || !hasCondition(charID, "dodging") {
		return
	}
	var raw []byte
	if db.QueryRow(`SELECT COALESCE(conditions, '[]') FROM characters WHERE id = $1`, charID).Scan(&raw) != nil {
		return
	}
	var conds, kept []string
	json.Unmarshal(raw, &conds)
	kept = []string{}
	for _, c := range conds {
		if c != "dodging" {
			kept = append(kept, c)
		}
	}
	out, _ := json.Marshal(kept)
	db.Exec(`UPDATE characters SET conditions = $1 WHERE id = $2`, out, charID)
}

// undeadFortitude (Zombie, Ogre Zombie; SRD 5.2.1): "If damage reduces the zombie to 0 Hit Points, it makes a Constitution saving
// throw (DC 5 plus the damage taken) unless the damage is Radiant or from a Critical Hit. On a successful save, the zombie drops
// to 1 Hit Point instead." hp is what the damage left it with; e is its turn-order entry, still holding the Hit Points before.
func undeadFortitude(lobbyID int, e map[string]interface{}, hp, taken int, dtype string, crit bool) (int, string) {
	return undeadFortitudeFor(lobbyID, asInt(e["id"]), fmt.Sprint(e["name"]), asInt(e["hp"]), hp, taken, dtype, crit)
}

// undeadFortitudeFor is the same for the places that hold the monster's id and Hit Points and not its turn-order entry. It is
// called wherever damage can take a monster to 0 Hit Points: a weapon hit and a Graze (attack_target.go), every spell, item and
// feature that deals damage (damageMonster, cleric_2024.go), and a companion's attack (companions_2024.go).
func undeadFortitudeFor(lobbyID, id int, name string, hpBefore, hp, taken int, dtype string, crit bool) (int, string) {
	if hp > 0 || taken <= 0 || id >= 0 || hpBefore <= 0 || crit || strings.EqualFold(strings.TrimSpace(dtype), "radiant") {
		return hp, ""
	}
	if !monsterHasTrait(lobbyID, id, "Undead Fortitude") {
		return hp, ""
	}
	dc := 5 + taken
	saved, total := monsterSaveRoll(lobbyID, id, "con", dc)
	if saved {
		return 1, fmt.Sprintf(" Undead Fortitude: %s makes its Constitution save (%d vs DC %d) and drops to 1 Hit Point instead.", name, total, dc)
	}
	return hp, fmt.Sprintf(" Undead Fortitude: %s fails its Constitution save (%d vs DC %d).", name, total, dc)
}
