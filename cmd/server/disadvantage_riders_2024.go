package main

import (
	"regexp"
	"strings"
)

// The roll riders of a stat-block hit or failed save (monster_effects_2024.go readRider), each read with its duration (riderEnd,
// rider_duration_2024.go):
//   - "the next attack roll made against the target before the start of the <source>'s next turn has Advantage": the "exposed"
//     effect (the same one Guiding Bolt gives), used up by the next attack roll against it, removed the same way.
//   - "Disadvantage on attack rolls until ...": the marker "disadv_attacks", read by attackSources for every attack roll it makes.
//   - "the pirate has Advantage on the next attack roll it makes before the end of this turn" (SRD 5.2.1 Pirate Captain Rapier): the
//     hit's own attacker gets the marker "adv_next_attack" (not the target), used up by its next attack roll (attackSources) or
//     ending with its current turn (one round counted down at the end of its turns).
//   - "Disadvantage on the next attack roll it makes before the end of its next turn" (SRD 5.2.1 Ettin Morningstar): the marker
//     "disadv_next_attack" for that duration, used up by the next attack roll it makes (attackSources, takeCondMarker).
//   - "Disadvantage on saving throws to maintain Concentration until ...": the marker "disadv_concentration" (saveSources).
//   - "the target can't take Reactions" with a duration in its sentence (or the effect's text): the marker "no_reactions".
//
// A marker lasts for its duration (riderEnd.markerAdd); a rider used up by one roll is applied only when it ends at the start of
// the source's next turn, the end the combat effects keep.
//
//   - "... and ability checks": the marker "disadv_checks" for the same duration, read by the check owner (check_2024.go checkCtx.sources).
//
// Not applied, always left in Extra for the GM: no duration given, Disadvantage on Strength-based D20 Tests. Never dropped, never guessed.

var (
	exposedRe      = regexp.MustCompile(`(?i)the next attack roll (?:made )?against (?:the target|it),? ` + riderUntil + `,? has Advantage`)
	disadvAttackRe = riderPattern(`Disadvantage on attack rolls( and ability checks)?`)
	disadvConcRe   = riderPattern(`Disadvantage on saving throws to maintain Concentration`)
	disadvNextRe   = riderPattern(`Disadvantage on the next attack roll it makes`)
	advSelfNextRe  = regexp.MustCompile(`(?i)(?:, and )?the [\pL][\pL '’-]*? has Advantage on the next attack roll it makes before the end of this turn`)
	noReactRe      = regexp.MustCompile(`(?i)(?:the target|it) can['’]t take Reactions;?`)
)

// readDisadvantage reads the applied roll riders of a text. handled is what readRider must not list for the GM again;
// gm is the part of a handled sentence the server does not apply.
func (e *saveEffect) readDisadvantage(text string) (handled []string, gm []string) {
	add := func(mode string, end riderEnd, m string) {
		if e.RollRiderEnd == nil {
			e.RollRiderEnd = map[string]riderEnd{}
		}
		e.DisadvMode = append(e.DisadvMode, mode)
		e.RollRiderEnd[mode] = end
		handled = append(handled, m)
	}
	// used up by one roll: only with the end the combat effects keep (the start of the source's next turn)
	if m := exposedRe.FindStringSubmatch(text); m != nil && riderEndIn(m) == riderEndSourceTurnStart {
		add("exposed", riderEndSourceTurnStart, m[0])
	}
	if m := disadvAttackRe.FindStringSubmatch(text); m != nil && riderEndIn(m) != riderEndNone {
		add("attacks", riderEndIn(m), m[0])
		if m[1] != "" { // "... and ability checks": the same duration, read by the check owner (check_2024.go)
			e.DisadvMode = append(e.DisadvMode, "checks")
			e.RollRiderEnd["checks"] = riderEndIn(m)
		}
	}
	if m := disadvConcRe.FindStringSubmatch(text); m != nil && riderEndIn(m) != riderEndNone {
		add("concentration", riderEndIn(m), m[0])
	}
	if m := disadvNextRe.FindStringSubmatch(text); m != nil && riderEndIn(m) != riderEndNone {
		add("nextattack", riderEndIn(m), m[0])
	}
	if m := advSelfNextRe.FindString(text); m != "" {
		e.DisadvMode = append(e.DisadvMode, "selfnextadv")
		handled = append(handled, m)
	}
	if m := noReactRe.FindString(text); m != "" {
		end := readRiderEnd(text)
		for _, sentence := range sentenceSplitRe.Split(text, -1) {
			if strings.Contains(sentence, strings.TrimSuffix(m, ";")) {
				end = riderEndFor(sentence, text)
				break
			}
		}
		if end != riderEndNone {
			add("noreactions", end, m)
		}
	}
	return
}

// rollRiderWords: the marker condition and the words of each marker rider.
func rollRiderWords(mode string) (cond, what string) {
	switch {
	case mode == "concentration":
		return "disadv_concentration", "Disadvantage on saves to maintain Concentration"
	case mode == "noreactions":
		return "no_reactions", "can't take Reactions"
	case mode == "checks":
		return "disadv_checks", "Disadvantage on ability checks"
	case mode == "nextattack":
		return "disadv_next_attack", "Disadvantage on its next attack roll"
	case mode == "selfnextadv":
		return "adv_next_attack", "Advantage on its next attack roll this turn"
	}
	return "disadv_attacks", "Disadvantage on attack rolls"
}

// giveDisadvantage puts the roll riders on the target. e.SourceID is the creature the text comes from (the riders anchored to its
// turn end on it).
func (e saveEffect) giveDisadvantage(lobbyID, tid int, source string) []string {
	var parts []string
	for _, mode := range e.DisadvMode {
		end := e.RollRiderEnd[mode]
		if mode == "exposed" {
			what := effectText[mode]
			if e.SourceID == 0 {
				parts = append(parts, riderNotApplied(what))
				continue
			}
			addCombatEffect(lobbyID, tid, e.SourceID, mode)
			parts = append(parts, what+" "+strings.Replace(end.describe(lobbyID, e.SourceID), "until", "before", 1))
			continue
		}
		cond, what := rollRiderWords(mode)
		if mode == "selfnextadv" { // the attacker's own marker, for the rest of this turn
			if e.SourceID == 0 {
				parts = append(parts, riderNotApplied(what))
				continue
			}
			rounds := 1
			if currentTurnID(lobbyID) != e.SourceID { // not on its own turn (a Reaction): there is no "this turn" to end
				parts = append(parts, riderNotApplied(what))
				continue
			}
			addCondition(lobbyID, e.SourceID, condAdd{Condition: cond, Source: source, SourceID: e.SourceID, Rounds: rounds, Replace: true})
			parts = append(parts, creatureName(lobbyID, e.SourceID)+" has "+what)
			continue
		}
		a, ok := end.markerAdd(lobbyID, tid, e.SourceID, cond, source, e.IsSpell)
		if !ok {
			parts = append(parts, riderNotApplied(what))
			continue
		}
		addCondition(lobbyID, tid, a)
		parts = append(parts, what+" "+end.describe(lobbyID, e.SourceID))
	}
	return parts
}

// takeCondMarker: the creature had the marker, which is used up now (a rider for one roll: "disadv_next_attack").
func takeCondMarker(lobbyID, id int, cond string) bool {
	if !hasCondMarker(lobbyID, id, cond) {
		return false
	}
	return len(removeConditionsWhere(lobbyID, id, func(r conditionRecord) bool { return r.Condition == cond })) > 0
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

// rollRiderPatterns: every roll rider wording readDisadvantage applies (what the GM is no longer told).
func rollRiderPatterns() []*regexp.Regexp {
	return []*regexp.Regexp{exposedRe, disadvAttackRe, disadvConcRe, disadvNextRe, advSelfNextRe, noReactRe}
}

// stripDisadvantage removes the handled sentences from a sentence (what is left is for the GM).
func stripDisadvantage(s string) string {
	for _, re := range rollRiderPatterns() {
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
