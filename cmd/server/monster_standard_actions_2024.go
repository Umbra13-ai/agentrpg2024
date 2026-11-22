package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// A stat-block entry that takes one of the standard actions ("Nimble Escape. The goblin takes the Disengage or Hide action.";
// "Cunning Action. The spy takes the Dash, Disengage, or Hide action."; SRD 5.2.1). One reader for every such entry:
//   - an entry that offers several actions is refused until the GM names one in monster_action.use (nothing is done);
//   - Disengage is recorded (combat effect "disengaged" on the monster, by itself): its movement on the battle map opens no
//     Opportunity Attack chance for the rest of the turn (noteMonstersLeavingReach reads it with the GM's own disengaged list),
//     and expireCombatEffectsOf ends it at the start of its next turn;
//   - Hide (a Stealth check that needs Heavy Obscurement or cover out of sight), Dash (movement) and Dodge stay with the GM, said so.
var monsterStandardActionRe = regexp.MustCompile(`(?i)\btakes the ((?:Dash|Disengage|Hide)(?:(?:,|,? or|,? and) (?:Dash|Disengage|Hide))*) action\b`)

var standardActionNameRe = regexp.MustCompile(`(?i)Dash|Disengage|Hide`)

// monsterStandardActions: the actions an entry's text takes, in its order ("" when it takes none).
func monsterStandardActions(text string) []string {
	m := monsterStandardActionRe.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	var out []string
	for _, a := range standardActionNameRe.FindAllString(m[1], -1) {
		out = append(out, titleCase(strings.ToLower(a)))
	}
	return out
}

// resolveMonsterStandardAction applies the entry's standard action for the monster; handled is false when the text takes none.
func resolveMonsterStandardAction(lobbyID, monsterID int, name, entryName, text, use string, apply bool) (line string, handled bool) {
	options := monsterStandardActions(text)
	if len(options) == 0 {
		return "", false
	}
	chosen := ""
	if len(options) == 1 {
		chosen = options[0]
	} else if m, ok := game.MatchString(use, options); ok {
		chosen = m
	}
	if chosen == "" {
		return fmt.Sprintf("%s: name the action in use (%s). Nothing was done.", entryName, strings.Join(options, ", ")), true
	}
	switch chosen {
	case "Disengage":
		if apply {
			addCombatEffect(lobbyID, monsterID, monsterID, "disengaged")
		}
		return fmt.Sprintf("%s uses %s: it takes the Disengage action; its movement doesn't provoke Opportunity Attacks for the rest of the turn.", name, entryName), true
	case "Hide":
		return attemptMonsterHide(lobbyID, monsterID, name, entryName, text, apply), true
	}
	// gm-one-shot: the Dash, Disengage or Hide the GM resolves in the same breath
	return fmt.Sprintf("%s uses %s: it takes the %s action (the GM resolves it).", name, entryName, chosen), true
}

// monsterDisengaged: the monster took the Disengage action this turn (resolveMonsterStandardAction).
func monsterDisengaged(lobbyID, monsterID int) bool {
	for _, e := range combatEffectsOn(lobbyID, monsterID) {
		if e.Kind == "disengaged" {
			return true
		}
	}
	return false
}
