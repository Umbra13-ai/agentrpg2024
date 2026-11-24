package main

// A monster's Bonus Action in its turn (owner, Nov 24): a creature takes one Bonus Action in a turn, so the second one a turn is refused before
// anything is rolled, unless the GM has said this monster may take more (its stat block gives it more: monster-features' bonus_actions). The
// count is kept per monster in the fight (combat_monster_state.bonus_used, cleared at the start of the monster's turn by
// monsterFeaturesTurnStart) and spent by narrate's monster_action when its entry is one of the stat block's Bonus Actions and the request ran
// it (a hand-off to the GM counts: the GM is using it), given back for a refusal or a stop before anything was rolled. A preview
// (apply_damage false) only reads it.

import (
	"fmt"
)

// aliveMonsterID: the monster the request names (the best match first) that still has Hit Points, 0 when none does.
func aliveMonsterID(lobbyID int, monster string) int {
	entries := combatEntries(lobbyID)
	for _, x := range monsterIDsByKeyOrName(lobbyID, monster) {
		for _, c := range entries {
			if asInt(c["id"]) == x && asInt(c["hp"]) > 0 {
				return x
			}
		}
	}
	return 0
}

// bonusActionsAllowed: how many Bonus Actions the monster may take in a turn (one unless the GM said more).
func bonusActionsAllowed(lobbyID, id int) int {
	var n int
	if db == nil || db.QueryRow(`SELECT bonus_allowed FROM combat_monster_state WHERE lobby_id = $1 AND monster_id = $2`, lobbyID, id).Scan(&n) != nil || n < 1 {
		return 1
	}
	return n
}

// bonusActionsTaken: how many Bonus Actions the monster has taken this turn.
func bonusActionsTaken(lobbyID, id int) int {
	var n int
	if db == nil || db.QueryRow(`SELECT bonus_used FROM combat_monster_state WHERE lobby_id = $1 AND monster_id = $2`, lobbyID, id).Scan(&n) != nil {
		return 0
	}
	return n
}

// bonusActionRefusal: why the monster can take no more Bonus Actions this turn, "" when it can.
func bonusActionRefusal(lobbyID, id int) string {
	allowed := bonusActionsAllowed(lobbyID, id)
	if bonusActionsTaken(lobbyID, id) < allowed {
		return ""
	}
	name := creatureName(lobbyID, id)
	if allowed == 1 {
		return fmt.Sprintf("%s has already taken its Bonus Action this turn. Nothing was rolled. [GM: a creature takes one Bonus Action a turn; if its stat block gives it more, set how many with POST /api/gm/monster-features {\"combatant_id\": %d, \"bonus_actions\": 2}]", name, id)
	}
	return fmt.Sprintf("%s has already taken its %d Bonus Actions this turn. Nothing was rolled.", name, allowed)
}

// reserveBonusAction takes one of the monster's Bonus Actions of the turn in one statement (two requests cannot both take the last one);
// false when none is left.
func reserveBonusAction(lobbyID, id int) bool {
	if db == nil {
		return true
	}
	var used int
	err := db.QueryRow(`INSERT INTO combat_monster_state (lobby_id, monster_id, bonus_used) VALUES ($1, $2, 1)
		ON CONFLICT (lobby_id, monster_id) DO UPDATE SET bonus_used = combat_monster_state.bonus_used + 1
		WHERE combat_monster_state.bonus_used < GREATEST(combat_monster_state.bonus_allowed, 1) RETURNING bonus_used`, lobbyID, id).Scan(&used)
	return err == nil
}

// releaseBonusAction gives a reserved Bonus Action back (the request ran nothing).
func releaseBonusAction(lobbyID, id int) {
	if db != nil {
		db.Exec(`UPDATE combat_monster_state SET bonus_used = GREATEST(bonus_used - 1, 0) WHERE lobby_id = $1 AND monster_id = $2`, lobbyID, id)
	}
}

// setBonusActionsAllowed: the GM says how many Bonus Actions this monster takes in a turn (its stat block gives it more than one).
func setBonusActionsAllowed(lobbyID, id, n int) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(`INSERT INTO combat_monster_state (lobby_id, monster_id, bonus_allowed) VALUES ($1, $2, $3)
		ON CONFLICT (lobby_id, monster_id) DO UPDATE SET bonus_allowed = EXCLUDED.bonus_allowed`, lobbyID, id, n)
	return err
}

// resetBonusActions: a new turn of the monster begins.
func resetBonusActions(lobbyID, id int) {
	if db != nil {
		db.Exec(`UPDATE combat_monster_state SET bonus_used = 0 WHERE lobby_id = $1 AND monster_id = $2`, lobbyID, id)
	}
}

// takeMonsterBonusAction is what narrate asks before it runs a monster's entry: when the entry is one of the monster's Bonus Actions the turn's
// Bonus Action is reserved (or the refusal given). held is "" or the monster id to give the action back to (release) when the request ran nothing.
func takeMonsterBonusAction(lobbyID int, monster, action string, apply bool) (held int, refusal string) {
	id := aliveMonsterID(lobbyID, monster)
	if id == 0 {
		return 0, ""
	}
	_, meta, ok := monsterCombatant(lobbyID, id)
	if !ok || meta == nil {
		return 0, ""
	}
	if _, kind, found := findMonsterEntry(meta, action); !found || kind != "bonus action" {
		return 0, ""
	}
	if !apply { // a preview only reads it
		return 0, bonusActionRefusal(lobbyID, id)
	}
	if !reserveBonusAction(lobbyID, id) {
		return 0, bonusActionRefusal(lobbyID, id)
	}
	return id, ""
}
