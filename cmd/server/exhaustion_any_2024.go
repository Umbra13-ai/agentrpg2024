package main

import (
	"fmt"
	"strconv"
)

// The cross-target Exhaustion owner. SRD 5.2.1: each Exhaustion level subtracts 2 from every D20 Test
// and 5 feet from Speed; at level 6 the creature dies. A character's level lives in the characters
// table (exhaustionLevelOf); a monster's or companion's has no column, so it lives in the condition
// owner's records: one record, condition "exhaustion", detail the level (the same store every other
// monster and companion condition uses, with the fight's lifecycle). Every D20 Test reads the
// cross-target level (exhaustionLevelOfAny): the shared attack and save functions subtract 2 per level
// (game.ExhaustionD20Penalty), for the character, the monster and the companion alike. Immunity - a
// stat block's or a buff's - is the condition owner's immuneToCondition, the same table addCondition
// asks. Level 6 kills through the HP owners (setMonsterHP, endCompanion, and addExhaustion's
// killCharacter). The Speed penalty has no store to land in: the server does not move monsters or
// companions on a map, so there is no numeric monster Speed to reduce; that part of the rule is
// recorded as unsupported here rather than faked.

// exhaustionLevelOfAny: the creature's Exhaustion level, whatever its kind (0 when none).
func exhaustionLevelOfAny(lobbyID, id int) int {
	if id > 0 && !isCompanionCombatID(id) {
		return exhaustionLevelOf(id)
	}
	where := `target_id = $1 AND condition = 'exhaustion'`
	args := []interface{}{id}
	if id < 0 { // a monster's combat id is the fight's; a companion's is globally unique
		where += ` AND lobby_id = $2`
		args = append(args, lobbyID)
	}
	level := 0
	for _, r := range findConditionRecords(where, args...) {
		if n, err := strconv.Atoi(r.Detail); err == nil && n > level {
			level = n
		}
	}
	return level
}

// addExhaustionAny gives (or removes, n < 0) Exhaustion levels, whatever the target's kind. Immunity is
// checked on a gain, never on a removal. It answers the public text ("Exhaustion level 2.", "no
// Exhaustion (its stat block)", the level 6 death).
func addExhaustionAny(lobbyID, id int, n int, source string) string {
	if n > 0 {
		if why := immuneToCondition(lobbyID, id, condAdd{Condition: "exhaustion", Source: source}); why != "" {
			return fmt.Sprintf("no Exhaustion (%s)", why)
		}
	}
	if id > 0 && !isCompanionCombatID(id) {
		return addExhaustion(id, n) // the characters-table store; level 6 kills there
	}
	lob := 0 // companion records are globally keyed
	if id < 0 {
		lob = lobbyID
	}
	cur := exhaustionLevelOfAny(lobbyID, id)
	level := max(0, min(6, cur+n))
	if level == cur {
		return fmt.Sprintf("Exhaustion level %d.", level)
	}
	err := changeConditions(lob, id, func(c *conditionChange) error {
		if _, err := c.remove(func(r conditionRecord) bool { return r.Condition == "exhaustion" }); err != nil {
			return err
		}
		if level > 0 {
			_, err := c.insert(conditionRecord{Condition: "exhaustion", Detail: strconv.Itoa(level), Source: source})
			return err
		}
		return nil
	})
	logConditionFailure("add exhaustion", id, err)
	if level >= 6 && cur < 6 {
		if id < 0 {
			setMonsterHP(lobbyID, id, 0) // the Hit Point owner (Power Word Kill kills the same way)
		} else {
			endCompanion(id-companionIDBase, "Exhaustion level 6")
		}
		return fmt.Sprintf("Exhaustion level 6: %s dies.", creatureName(lobbyID, id))
	}
	return fmt.Sprintf("Exhaustion level %d.", level)
}
