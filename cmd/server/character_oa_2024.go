package main

// A character's Opportunity Attack against a monster that moves out of its reach (SRD 5.2.1). Monsters move when the GM updates the
// battle map. When a monster that was within a character's reach (5 feet, 10 with a Reach weapon in hand) ends up outside it, the
// character can take its Reaction to make one melee attack against it: POST /api/action {"action":"opportunity_attack","target_id":-N}.
// The chance lasts until the turn order moves on. Only a monster's own movement provokes: the GM's map update marks forced movement
// and teleports with "forced_movement": true, and monsters that took the Disengage action with "disengaged": [ids].

import (
	"fmt"
	"strings"
	"sync"
)

var oaWindows sync.Map // "lobby:char" -> map[monsterID]bool, open until the turn order moves on

func oaKey(lobbyID, charID int) string { return fmt.Sprintf("%d:%d", lobbyID, charID) }

// characterMeleeReach is how far a character's melee attack reaches: 10 feet with a Reach weapon in the main hand, otherwise 5.
func characterMeleeReach(charID int) int {
	var key string
	db.QueryRow(`SELECT COALESCE(equipped_main_hand,'') FROM characters WHERE id = $1`, charID).Scan(&key)
	w, ok := srdWeapons[strings.ToLower(strings.ReplaceAll(key, " ", "_"))]
	if ok && w.Type == "melee" {
		for _, p := range w.Properties {
			if strings.EqualFold(p, "reach") {
				return 10
			}
		}
	}
	return 5
}

// noteMonstersLeavingReach compares the map before and after a GM update and opens an Opportunity Attack chance for each character
// whose reach a monster left. It returns the notes for the feed.
func noteMonstersLeavingReach(lobbyID int, before, after encounterMap, disengaged []int) []string {
	var notes []string
	alive := livingMonsterIDs(lobbyID)
	skip := map[int]bool{}
	for _, id := range disengaged {
		skip[id] = true
	}
	for _, mOld := range before.Tokens {
		if mOld.ID >= 0 || !alive[mOld.ID] || skip[mOld.ID] {
			continue
		}
		mNew, ok := after.token(mOld.ID)
		if !ok || mNew.Position == mOld.Position {
			continue
		}
		for _, c := range before.Tokens {
			if c.ID <= 0 {
				continue
			}
			cNew, ok := after.token(c.ID)
			if !ok {
				continue
			}
			reach := characterMeleeReach(c.ID)
			if tokenDistanceFeet(c, mOld) <= reach && tokenDistanceFeet(cNew, mNew) > reach {
				var reactionUsed, dead bool
				var hp int
				db.QueryRow(`SELECT COALESCE(reaction_used,false), COALESCE(is_dead,false), hp FROM characters WHERE id = $1`, c.ID).Scan(&reactionUsed, &dead, &hp)
				if reactionUsed || dead || hp <= 0 || hasCondition(c.ID, "incapacitated") || hasCondition(c.ID, "unconscious") || hasCondition(c.ID, "paralyzed") || hasCondition(c.ID, "stunned") {
					continue
				}
				v, _ := oaWindows.LoadOrStore(oaKey(lobbyID, c.ID), &sync.Map{})
				v.(*sync.Map).Store(mOld.ID, true)
				notes = append(notes, fmt.Sprintf("⚔️ %s moves out of %s's reach: %s can take a Reaction to make an Opportunity Attack (POST /api/action {\"action\":\"opportunity_attack\",\"target_id\":%d}).", mOld.Name, c.Name, c.Name, mOld.ID))
			}
		}
	}
	return notes
}

// takeOAWindow uses up a character's Opportunity Attack chance against a monster (false if there is none).
func takeOAWindow(lobbyID, charID, monsterID int) bool {
	v, ok := oaWindows.Load(oaKey(lobbyID, charID))
	if !ok {
		return false
	}
	_, had := v.(*sync.Map).LoadAndDelete(monsterID)
	return had
}

func hasOAWindow(lobbyID, charID, monsterID int) bool {
	v, ok := oaWindows.Load(oaKey(lobbyID, charID))
	if !ok {
		return false
	}
	_, had := v.(*sync.Map).Load(monsterID)
	return had
}

// clearOAWindows closes every Opportunity Attack chance in a campaign (the turn order moved on).
func clearOAWindows(lobbyID int) {
	prefix := fmt.Sprintf("%d:", lobbyID)
	oaWindows.Range(func(k, _ interface{}) bool {
		if strings.HasPrefix(k.(string), prefix) {
			oaWindows.Delete(k)
		}
		return true
	})
}

// attackTargetOf is the monster the character's current action is aimed at (target_id), or 0.
func attackTargetOf(charID int) int {
	if v, ok := attackTargets.Load(charID); ok {
		if id, ok := v.(int); ok {
			return id
		}
	}
	return 0
}

// openOpportunityAttacks lists the monsters a character may make an Opportunity Attack against right now (for GET /api/my-turn).
func openOpportunityAttacks(lobbyID, charID int) []int {
	var out []int
	if v, ok := oaWindows.Load(oaKey(lobbyID, charID)); ok {
		v.(*sync.Map).Range(func(k, _ interface{}) bool {
			out = append(out, k.(int))
			return true
		})
	}
	return out
}
