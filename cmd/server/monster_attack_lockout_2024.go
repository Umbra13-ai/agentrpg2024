package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Attack lockouts from a stat block's own text (SRD 5.2.1): "While attached, the stirge can't make Proboscis attacks." (attach owner,
// combat effect kind "attached") and "While swallowing the target, the frog can't use Bite." (swallow owner, kind "swallowed"). The
// lock reads live state and is refused in resolveMonsterAttack, the one attack entry every route uses, before anything is rolled or
// spent.
var attackLockoutRe = regexp.MustCompile(`(?i)While attached, the [\w() '’-]+can(?:'|’)t make ([A-Za-z][A-Za-z ]*?) attacks?\b`)

// swallowLockoutRe: the swallow action's own grammar locks the named action while the swallow lasts
// (Giant Frog: "While swallowing the target, the frog can't use Bite."). The lock reads the swallow
// owner's row (kind 'swallowed'), so it ends exactly with the swallow.
var swallowLockoutRe = regexp.MustCompile(`(?i)While swallowing the target, the [\w() '’-]+can(?:'|’)t use ([A-Za-z][A-Za-z ]*?)[,.]`)

type attackLockout struct {
	action  string // lowercased base name of the locked attack
	swallow bool   // true: locked while swallowing a creature (the swallow owner owns the row); false: while attached
}

// attackLockouts reads every lockout a stat block declares, from any of its actions.
func attackLockouts(actions []monsterActionRow) []attackLockout {
	var out []attackLockout
	for _, a := range actions {
		for _, m := range attackLockoutRe.FindAllStringSubmatch(a.Desc, -1) {
			out = append(out, attackLockout{action: strings.ToLower(strings.TrimSpace(m[1]))})
		}
		for _, m := range swallowLockoutRe.FindAllStringSubmatch(a.Desc, -1) {
			out = append(out, attackLockout{action: strings.ToLower(strings.TrimSpace(m[1])), swallow: true})
		}
	}
	return out
}

// monsterAttackLockout: the refusal when a creature the attacker is attached to, or one it swallowed, locks this
// attack; "" when the attack is free. Nothing is rolled or spent on a refusal.
func monsterAttackLockout(lobbyID, attackerID int, actions []monsterActionRow, actionBase string) string {
	for _, l := range attackLockouts(actions) {
		if l.action != strings.ToLower(actionBase) {
			continue
		}
		name := creatureName(lobbyID, attackerID)
		safe := fmt.Sprintf("%s can't make %s attacks. Nothing was rolled.", name, actionBase)
		if l.swallow {
			if !attachSideAlive(lobbyID, attackerID) {
				continue // a dead creature holds no swallow
			}
			rows, err := db.Query(`SELECT target_id FROM combat_effects WHERE lobby_id=$1 AND source_id=$2 AND kind='swallowed'`, lobbyID, attackerID)
			if err != nil {
				continue
			}
			locked := false
			for rows.Next() {
				var tid int
				if rows.Scan(&tid) == nil && attachSideAlive(lobbyID, tid) {
					locked = true // only a live swallow locks
				}
			}
			rows.Close()
			if locked {
				return refusalAudiences(fmt.Sprintf("%s can't use %s. Nothing was rolled.", name, actionBase),
					fmt.Sprintf("%s can't use %s while swallowing a target (its stat block's rule)", name, actionBase))
			}
			continue
		}
		if !attachSideAlive(lobbyID, attackerID) {
			continue // a dead creature holds no attach (rows can outlive it between turn starts)
		}
		rows, err := db.Query(`SELECT target_id FROM combat_effects WHERE lobby_id=$1 AND source_id=$2 AND kind='attached'`, lobbyID, attackerID)
		if err != nil {
			continue
		}
		locked := false
		for rows.Next() {
			var tid int
			if rows.Scan(&tid) == nil && attachSideAlive(lobbyID, tid) {
				locked = true // only a live attach locks: a dead target ends it (attachSweep owns the row)
			}
		}
		rows.Close()
		if locked {
			return refusalAudiences(safe, fmt.Sprintf("%s can't make %s attacks while attached (its stat block's rule)", name, actionBase))
		}
	}
	return ""
}

// refusalAudiences: a refusal's two audiences (docs/skill.md "Refusals and audience"). The caller - the GM,
// these are GM routes - always reads the full reason in the reply; the shared action log the players read
// stores only the player-safe observation, because writeAction splits [GM: ...] into the GM's private
// notices before persisting. No monster limits, resources or stat-block clauses in the public text.
func refusalAudiences(safe, privateReason string) string {
	if privateReason == "" {
		return safe
	}
	return safe + " [GM: " + privateReason + "]"
}
