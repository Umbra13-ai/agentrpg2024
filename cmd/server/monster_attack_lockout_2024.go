package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Attack lockouts from a stat block's own text: "Until the grapple ends, the kuo-toa can't make Pincer Staff
// attacks." (Kuo-toa Whip, Vine Blight) and "While attached, the stirge can't make Proboscis attacks." The
// server records the Grappled condition with the attacker's SourceID and ends it with the existing escape
// machinery, so the lock reads live state: a creature the attacker grappled locks the named attack until the
// grapple ends. Attach-lock uses the attach effect (kind "attached", attach owner); a grapple-lock has no use
// for it. The lock is refused in resolveMonsterAttack, the one attack entry every route uses, before anything
// is rolled or spent.
var attackLockoutRe = regexp.MustCompile(`(?i)(Until the grapple ends|While attached), the [\w() '-]+can(?:'|’)t make ([A-Za-z][A-Za-z ]*?) attacks?\b`)

// attackLockoutTailRe: the same grapple lockout when the recurring-damage clause leads the sentence (Vine
// Blight: "Until the grapple ends, the target takes 4 (1d8) ..., and the blight can't make Constricting Vine
// attacks."). One rule, one owner: the order of the clauses does not change the lock.
var attackLockoutTailRe = regexp.MustCompile(`(?i)Until the grapple ends[^.]*?\bcan(?:'|’)t make ([A-Za-z][A-Za-z ]*?) attacks?\b`)

// swallowLockoutRe: the swallow action's own grammar locks the named action while the swallow lasts
// (Giant Frog: "While swallowing the target, the frog can't use Bite."). The lock reads the swallow
// owner's row (kind 'swallowed'), so it ends exactly with the swallow.
var swallowLockoutRe = regexp.MustCompile(`(?i)While swallowing the target, the [\w() '’-]+can(?:'|’)t use ([A-Za-z][A-Za-z ]*?)[,.]`)

// grappleLockoutTextRe: the grapple variant only, for the hit residual: the server enforces it at the attack
// entry, so it is not the GM's. The attach variant stays with the GM until the attach owner exists.
var grappleLockoutTextRe = regexp.MustCompile(`(?i)Until the grapple ends, the [\w() '-]+can(?:'|’)t make [A-Za-z][A-Za-z ]*? attacks?\b`)

type attackLockout struct {
	action  string // lowercased base name of the locked attack
	attach  bool   // true: locked while attached to a creature; false: locked while a grapple it created lasts
	swallow bool   // true: locked while swallowing a creature (the swallow owner owns the row)
}

// attackLockouts reads every lockout a stat block declares, from any of its actions.
func attackLockouts(actions []monsterActionRow) []attackLockout {
	var out []attackLockout
	for _, a := range actions {
		for _, m := range attackLockoutRe.FindAllStringSubmatch(a.Desc, -1) {
			out = append(out, attackLockout{action: strings.ToLower(strings.TrimSpace(m[2])), attach: strings.HasPrefix(strings.ToLower(m[1]), "while")})
		}
		for _, m := range attackLockoutTailRe.FindAllStringSubmatch(a.Desc, -1) {
			out = append(out, attackLockout{action: strings.ToLower(strings.TrimSpace(m[1]))})
		}
		for _, m := range swallowLockoutRe.FindAllStringSubmatch(a.Desc, -1) {
			out = append(out, attackLockout{action: strings.ToLower(strings.TrimSpace(m[1])), swallow: true})
		}
	}
	return out
}

// monsterAttackLockout: the refusal when a live grapple the attacker created, or a creature it is attached to,
// locks this attack; "" when the attack is free. Nothing is rolled or spent on a refusal.
func monsterAttackLockout(lobbyID, attackerID int, actions []monsterActionRow, actionBase string) string {
	for _, l := range attackLockouts(actions) {
		if l.action != strings.ToLower(actionBase) {
			continue
		}
		name := creatureName(lobbyID, attackerID)
		safe := fmt.Sprintf("%s can't make %s attacks. Nothing was rolled.", name, actionBase)
		if l.attach {
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
			continue
		}
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
		var n int
		if attackerID < 0 && !monsterAlive(lobbyID, attackerID) {
			continue // a dead creature holds no grapple (grapple records can outlive a dead grappler)
		}
		if err := db.QueryRow(`SELECT count(*) FROM condition_records WHERE lobby_id=$1 AND source_id=$2 AND condition='grappled'`, lobbyID, attackerID).Scan(&n); err == nil && n > 0 {
			return refusalAudiences(safe, fmt.Sprintf("%s can't make %s attacks until the grapple it created ends (its stat block's rule)", name, actionBase))
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
