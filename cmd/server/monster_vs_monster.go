package main

// A monster's attack on another monster or NPC of the fight (an ally turned on its master, a charmed monster, a Legendary Action
// aimed at a rival). The owners are the same as for a character target: the attack roll is attackCtx (attack_2024.go; Advantage and
// Disadvantage from both sides' conditions, the target's Armor Class from the fight), the damage is hurtMonster (resistance, immunity,
// absorption, Undead Fortitude), the conditions a hit gives are saveEffect.giveConditions, a hit's own saving throw is applySaveEffect.
// What this path does not have, because a monster has none of it: reactions the target could take, Shield and standing orders,
// items. Exhaustion applies mechanically (exhaustion_any_2024.go). The reply says so; nothing is skipped silently.

import (
	"fmt"
	"net/http"
	"strings"
)

// monsterAttacksMonster resolves chosen (a single attack or a Multiattack's plan) against the monster tid. info["made"] says the
// attack was made against a target (a limited-use attack counts then, whether it hit or not).
func monsterAttacksMonster(r *http.Request, lobbyID, attackerID int, attackerName string, attackerConds []string, chosen, actions []monsterActionRow, tid int, apply bool) (string, map[string]interface{}) {
	if apply {
		markExecuted(r)
	}
	c := monsterAttackCursor{Attacker: attackerID, AttackerName: attackerName, Target: tid, Apply: apply, Actions: actions}
	for _, a := range chosen {
		c.Chosen = append(c.Chosen, savedMonsterAttack{Action: a, Alternatives: a.Alts})
	}
	text, info, err := runMonsterAttackSequence(lobbyID, c)
	if err != nil {
		addGMNotice(lobbyID, "The monster attack sequence could not finish. Reconcile its saved work before continuing.")
		info["resolution_error"] = true
	}
	return text, info
}

// monsterAlive: the monster tid of the fight has Hit Points left.
func monsterAlive(lobbyID, tid int) bool {
	for _, c := range combatEntries(lobbyID) {
		if asInt(c["id"]) == tid {
			return asInt(c["hp"]) > 0
		}
	}
	return false
}

// monsterTargetReactionNote says what the target's Reactions can still do against this attack: the server does not play them by itself
// (the GM chooses when a monster reacts), so the note names them, whether its Reaction is spent, and how to resolve one against the hit.
func monsterTargetReactionNote(lobbyID, tid int) string {
	_, meta, ok := monsterCombatant(lobbyID, tid)
	if !ok || meta == nil || len(meta.Reactions) == 0 {
		return "The target has no Reactions in its stat block. Standing orders and Opportunity Attacks of the other side are not played on this path: the GM plays any"
	}
	var names []string
	for _, e := range meta.Reactions {
		names = append(names, stripUsage(e.Name))
	}
	spent := ""
	if v, ok := monsterReactionRound.Load(fmt.Sprintf("%d:%d", lobbyID, tid)); ok && v.(int) == currentRound(lobbyID) {
		spent = " (its Reaction is already used this round)"
	}
	return fmt.Sprintf("The target's Reactions are not played automatically: %s%s. To react to this hit, POST /api/gm/narrate monster_action with the Reaction's name for %s right now, and the server resolves it against this hit. Standing orders and Opportunity Attacks are the GM's", strings.Join(names, ", "), spent, creatureName(lobbyID, tid))
}
