package main

import (
	"regexp"
	"strings"
)

// Disadvantage riders of a stat-block hit or failed save (monster_effects_2024.go readRider). What is applied, by tier:
//   - "Disadvantage on its next attack roll before the start of the <source>'s next turn": the Sap effect (combat_effects kind "sap"),
//     consumed by the next attack roll (attackSources), removed at the start of the source's next turn (expireCombatEffectsOf).
//   - "Disadvantage on attack rolls until the end of its/the target's next turn": a marker condition "disadv_attacks" on the target,
//     counted down at the end of the target's turns like the Speed markers (giveSpeed); attackSources reads it for every attack roll.
//   - "Disadvantage on saving throws to maintain Concentration until the end of its/the target's next turn": the marker
//     "disadv_concentration", read by the save sources when the save is a Concentration save.
//
// Not applied, always left in Extra for the GM: Disadvantage on ability checks, an end anchored to the ATTACKER's turn ("until the end
// of the cyclops's next turn"), no duration given, Disadvantage on Strength-based D20 Tests. Never dropped, never guessed.
var (
	disadvSapRe    = regexp.MustCompile(`(?i)Disadvantage on its next attack roll before the start of the [^.]*?next turn`)
	disadvAttackRe = regexp.MustCompile(`(?i)Disadvantage on attack rolls( and ability checks)? until the end of (?:its|the target['’]s|the creature['’]s) next turn`)
	// "the target can't take Reactions" with a duration of the TARGET's next turn anywhere in the same effect text (Slowing Ray, Slowing
	// Breath). An end anchored to the attacker ("until the start of the balor's next turn", "until the curse ends") is not applied.
	noReactRe      = regexp.MustCompile(`(?i)(?:the target|it) can['’]t take Reactions;?`)
	noReactUntilRe = regexp.MustCompile(`(?i)until the end of (?:its|the target['’]s|the creature['’]s) next turn`)
	disadvConcRe   = regexp.MustCompile(`(?i)Disadvantage on saving throws to maintain Concentration until the end of (?:its|the target['’]s|the creature['’]s) next turn`)
)

// readDisadvantage reads the applied Disadvantage riders of a text. handled is what readRider must not list for the GM again;
// gm is the part of a handled sentence the server does not apply.
func (e *saveEffect) readDisadvantage(text string) (handled []string, gm []string) {
	if m := disadvSapRe.FindString(text); m != "" {
		e.DisadvMode = append(e.DisadvMode, "sap")
		handled = append(handled, m)
	}
	if m := disadvAttackRe.FindStringSubmatch(text); m != nil {
		e.DisadvMode = append(e.DisadvMode, "attacks")
		handled = append(handled, m[0])
		if m[1] != "" {
			gm = append(gm, "Disadvantage on ability checks until the end of its next turn (ability checks are the GM's)")
		}
	}
	if m := disadvConcRe.FindString(text); m != "" {
		e.DisadvMode = append(e.DisadvMode, "concentration")
		handled = append(handled, m)
	}
	if m := noReactRe.FindString(text); m != "" && noReactUntilRe.MatchString(text) {
		e.DisadvMode = append(e.DisadvMode, "noreactions")
		handled = append(handled, m)
	}
	return
}

// giveDisadvantage puts the riders on the target. sourceID is the creature the text comes from (the Sap effect ends at its turn).
func (e saveEffect) giveDisadvantage(lobbyID, tid int, source string) []string {
	var parts []string
	for _, mode := range e.DisadvMode {
		if mode == "sap" {
			addCombatEffect(lobbyID, tid, e.SourceID, "sap")
			parts = append(parts, "Disadvantage on its next attack roll")
			continue
		}
		rounds := 1
		if currentTurnID(lobbyID) == tid {
			rounds = 2 // the current turn is not its "next turn"
		}
		cond, what := "disadv_attacks", "Disadvantage on attack rolls"
		if mode == "concentration" {
			cond, what = "disadv_concentration", "Disadvantage on saves to maintain Concentration"
		}
		if mode == "noreactions" {
			cond, what = "no_reactions", "can't take Reactions"
		}
		addCondition(lobbyID, tid, condAdd{Condition: cond, Source: source, Rounds: rounds, SourceID: e.SourceID, Replace: true})
		parts = append(parts, what+" until the end of its next turn")
	}
	return parts
}

// hasCondMarker: the creature has the marker condition now.
func hasCondMarker(lobbyID, id int, cond string) bool {
	for _, r := range loadConditionRecords(lobbyID, id) {
		if r.Condition == cond {
			return true
		}
	}
	return false
}

// stripDisadvantage removes the handled sentences from a sentence (what is left is for the GM).
func stripDisadvantage(s string) string {
	for _, re := range []*regexp.Regexp{disadvSapRe, disadvAttackRe, disadvConcRe, noReactRe} {
		s = re.ReplaceAllString(s, "")
	}
	return strings.NewReplacer("the target has", "", "and the target has", "", "The target has", "").Replace(s)
}

// cantTakeReactions: the creature has the "can't take Reactions" marker (a hit or failed save gave it until the end of its next turn).
// Enforced where a monster's Reaction is resolved (resolveMonsterReaction) and where a character's Reaction is spent or offered
// (reactionReady, reactionSpent's callers listed in docs/MONSTER_SYSTEM.md); not every Reaction route reads it.
func cantTakeReactions(lobbyID, id int) bool { return hasCondMarker(lobbyID, id, "no_reactions") }

func charCantTakeReactions(charID int) bool {
	var lobby int
	db.QueryRow(`SELECT COALESCE(lobby_id,0) FROM characters WHERE id = $1`, charID).Scan(&lobby)
	return lobby > 0 && cantTakeReactions(lobby, charID)
}

const cantTakeReactionsMsg = "You can't take Reactions until the end of your next turn (an effect says so). Nothing was spent."
