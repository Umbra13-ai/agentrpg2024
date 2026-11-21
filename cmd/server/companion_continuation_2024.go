package main

// Companion and Wild Shape plans are adapters to the same hit, damage, save and
// condition owners as other stat-block attacks. Choices/scaling are prepared by
// their existing companion owner before this sequence begins.
import (
	"encoding/json"
	"fmt"
	"strings"
)

type companionAttackCursor struct {
	Actor        companion
	Target, Next int
	Plan         []compAttack
}

func init() {
	combatResumeHandlers["companion-attack-plan"] = func(lobby int, raw json.RawMessage) (map[string]interface{}, error) {
		var c companionAttackCursor
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		if !monsterInFight(lobby, c.Target) {
			return nil, fmt.Errorf("saved companion target is no longer in this fight")
		}
		lines, err := runCompanionAttackPlan(lobby, c)
		text := c.Actor.Name + " continues its attacks: " + strings.Join(lines, " ")
		recordGMAction(db, lobby, "companion", text, text)
		return map[string]interface{}{"result": text}, err
	}
}
func companionAttackPlan(lobby int, actor companion, plan []compAttack, target int) ([]string, error) {
	c := companionAttackCursor{Actor: actor, Target: target}
	for _, a := range plan {
		count := max(1, a.Count)
		a.Count = 1
		for i := 0; i < count; i++ {
			c.Plan = append(c.Plan, a)
		}
	}
	return runCompanionAttackPlan(lobby, c)
}
func runCompanionAttackPlan(lobby int, c companionAttackCursor) ([]string, error) {
	var lines []string
	for i := c.Next; i < len(c.Plan); i++ {
		if c.Actor.ID > 0 {
			active, ok := activeCompanionInCampaign(lobby, c.Actor.CombatID)
			if !ok || active.OwnerID != c.Actor.OwnerID {
				return append(lines, c.Actor.Name+" is no longer active here; no further attack was made."), nil
			}
		} else {
			if lobbyOfChar(c.Actor.CombatID) != lobby {
				return nil, fmt.Errorf("saved form attack belongs to another campaign")
			}
			var form string
			if err := db.QueryRow(`SELECT COALESCE(wild_shape_form,'') FROM characters WHERE id=$1`, c.Actor.CombatID).Scan(&form); err != nil {
				return nil, err
			}
			if c.Actor.Form != "" && form != c.Actor.Form {
				return append(lines, c.Actor.Name+" is no longer in the saved form; no further form attack was made."), nil
			}
		}
		next := c
		next.Next = i + 1
		restore, err := armCombatFrame(lobby, "companion-attack-plan", next)
		if err != nil {
			return lines, err
		}
		lines = append(lines, companionAttack(lobby, c.Actor, c.Plan[i], c.Target))
		restore()
		if combatResolutionPaused(lobby) {
			return lines, nil
		}
	}
	return lines, nil
}
