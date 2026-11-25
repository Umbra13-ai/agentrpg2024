package main

import (
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// Dodge (SRD 5.2.1): "Until the start of your next turn, any attack roll made against you has Disadvantage if you can see the
// attacker, and you make Dexterity saving throws with Advantage. You lose this benefit if you have the Incapacitated condition
// or if your Speed is 0." The marker is "dodging" in the character's conditions.

// dodgeActive: the character took the Dodge action and still has its benefit (not Incapacitated, Speed not 0). It is read in the
// two shared places only, so every attack roll against the character and every Dexterity save gets it: the "the target is
// Dodging" row of attackSources (attack_2024.go) and the "Dodge" row of saveSources (save_2024.go).
func dodgeActive(charID int) bool {
	if charID <= 0 || db == nil || !hasCondition(charID, "dodging") || isIncapacitated(charID) {
		return false
	}
	return !hasCondition(charID, "grappled") && !hasCondition(charID, "restrained") // Speed 0 ends the benefit
}

// dodgeResult: what a Dodge answers: the whole rule (both halves: attacks and Dexterity saving throws), the words of the Dodge row
// of the action table (action_types_2024.go).
func dodgeResult() string { t, _ := lookupActionType("dodge"); return "Dodging. " + t.What }

// dashMoveNote: a Dash adds movement; moving is its own request.
const dashMoveNote = "Move with POST /api/action {\"action\": \"move\", \"movement_cost\": <feet>} to use it."

// untilYourNextTurn: markers that last until the start of the character's next turn (SRD 5.2.1): the Dodge action, and
// Reckless Attack ("attack rolls against you have Advantage until the start of your next turn"). One list, cleared in one place.
var untilYourNextTurn = []string{"dodging", "reckless"}

// endUntilNextTurn drops those markers, and a readied action, when the character's next turn starts (resetTurnFlags) and when
// the fight ends (clearCombatInitiative).
func endUntilNextTurn(charID int) {
	if db == nil {
		return
	}
	endReadied(charID) // a readied action lasts until the start of your next turn too (readied_2024.go)
	for _, m := range untilYourNextTurn {
		if hasCondition(charID, m) {
			removeConditionsWhere(0, charID, func(r conditionRecord) bool { return containsStr(untilYourNextTurn, r.entry()) })
			return
		}
	}
}

// undeadFortitude (Zombie, Ogre Zombie; SRD 5.2.1): "If damage reduces the zombie to 0 Hit Points, it makes a Constitution saving
// throw (DC 5 plus the damage taken) unless the damage is Radiant or from a Critical Hit. On a successful save, the zombie drops
// to 1 Hit Point instead." hp is what the damage left it with; e is its turn-order entry, still holding the Hit Points before.
func undeadFortitude(lobbyID int, e map[string]interface{}, hp, taken int, dtype string, crit bool) (int, string) {
	return undeadFortitudeFor(lobbyID, asInt(e["id"]), fmt.Sprint(e["name"]), asInt(e["hp"]), hp, taken, dtype, crit)
}

// undeadFortitudeFor is the same for the places that hold the monster's id and Hit Points and not its turn-order entry. It is
// called wherever damage can take a monster to 0 Hit Points: a weapon hit and a Graze (attack_target.go), every spell, item and
// feature that deals damage (damageMonster, cleric_2024.go), a companion's attack (companions_2024.go), and exact damage
// (exactDamageFortitude: the GM's adjust-monster, the Sphere of Annihilation; I-ALDENQA-35).
func undeadFortitudeFor(lobbyID, id int, name string, hpBefore, hp, taken int, dtype string, crit bool) (int, string) {
	if hp > 0 || taken <= 0 || id >= 0 || hpBefore <= 0 || crit || strings.EqualFold(strings.TrimSpace(dtype), "radiant") {
		return hp, ""
	}
	if !monsterHasTrait(lobbyID, id, "Undead Fortitude") {
		return hp, ""
	}
	dc := 5 + taken
	sv := monsterSaveRoll(lobbyID, id, "con", dc)
	if sv.Success {
		return 1, " Undead Fortitude: " + sv.Line(name) + " and drops to 1 Hit Point instead."
	}
	return hp, " Undead Fortitude: " + sv.Line(name) + "."
}

// endUntilNextTurnIn: the fight is over, so is every effect that lasts until a character's next turn (clearCombatInitiative).
func endUntilNextTurnIn(campaignID int) {
	rows, err := db.Query(`SELECT id FROM characters WHERE lobby_id = $1`, campaignID)
	if err != nil {
		return
	}
	var ids []int
	for rows.Next() {
		var id int
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		endUntilNextTurn(id)
	}
}

// exactDamageFortitude: the damage to deal exactly (loseMonsterHP, Temporary Hit Points first) once Undead Fortitude has had its say: when
// the damage would leave the monster at 0, the save is rolled and a success leaves it 1 Hit Point (the damage dealt is cut to that).
func exactDamageFortitude(lobbyID, id, amount int, dtype string) (int, string) {
	f, err := readFight(db, lobbyID)
	if err != nil {
		return amount, ""
	}
	_, e := f.entry(id)
	if e == nil {
		return amount, ""
	}
	v := monsterVitals(e)
	if r := game.TakeDamage(v, amount, false, false); r.After.HP > 0 {
		return amount, ""
	}
	hp, note := undeadFortitudeFor(lobbyID, id, fmt.Sprint(e["name"]), v.HP, 0, amount, dtype, false)
	if hp == 1 {
		return v.Temp + v.HP - 1, note
	}
	return amount, note
}
