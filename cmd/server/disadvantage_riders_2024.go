package main

import (
	"regexp"
	"slices"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// The roll riders of a stat-block hit or failed save (monster_effects_2024.go readRider), each read with its duration (riderEnd,
// rider_duration_2024.go):
//   - "Disadvantage on its next attack roll before the start of the <source>'s next turn": the Sap effect (combat_effects kind
//     "sap"), used up by the next attack roll it makes (attackSources), removed at the start of the source's next turn
//     (expireCombatEffectsOf).
//   - "the next attack roll made against the target before the start of the <source>'s next turn has Advantage": the "exposed"
//     effect (the same one Guiding Bolt gives), used up by the next attack roll against it, removed the same way.
//   - "Disadvantage on attack rolls until ...": the marker "disadv_attacks", read by attackSources for every attack roll it makes.
//   - "Disadvantage on saving throws to maintain Concentration until ...": the marker "disadv_concentration" (saveSources).
//   - "Disadvantage on Intelligence saving throws until ...": the marker "disadv_save:<abilities>" (saveSources).
//   - "the target can't take Reactions" with a duration in its sentence (or the effect's text): the marker "no_reactions".
//
// A marker lasts for its duration (riderEnd.markerAdd); a rider used up by one roll is applied only when it ends at the start of
// the source's next turn, the end the combat effects keep.
//
// Not applied, always left in Extra for the GM: Disadvantage on ability checks, Disadvantage on the next attack roll before the end
// of its next turn, no duration given, Disadvantage on Strength-based D20 Tests. Never dropped, never guessed.
const saveAbilityNames = `(?:Strength|Dexterity|Constitution|Intelligence|Wisdom|Charisma)`

var (
	disadvSapRe     = riderPattern(`Disadvantage on its next attack roll`)
	exposedRe       = regexp.MustCompile(`(?i)the next attack roll (?:made )?against (?:the target|it),? ` + riderUntil + `,? has Advantage`)
	disadvAttackRe  = riderPattern(`Disadvantage on attack rolls( and ability checks)?`)
	disadvConcRe    = riderPattern(`Disadvantage on saving throws to maintain Concentration`)
	disadvSaveRe    = riderPattern(`Disadvantage on (` + saveAbilityNames + `(?:(?:,|,? and|,? or) ` + saveAbilityNames + `)*) saving throws`)
	noReactRe       = regexp.MustCompile(`(?i)(?:the target|it) can['’]t take Reactions;?`)
	abilityNameList = regexp.MustCompile(saveAbilityNames)
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
	if m := disadvSapRe.FindStringSubmatch(text); m != nil && riderEndIn(m) == riderEndSourceTurnStart {
		add("sap", riderEndSourceTurnStart, m[0])
	}
	if m := exposedRe.FindStringSubmatch(text); m != nil && riderEndIn(m) == riderEndSourceTurnStart {
		add("exposed", riderEndSourceTurnStart, m[0])
	}
	if m := disadvAttackRe.FindStringSubmatch(text); m != nil && riderEndIn(m) != riderEndNone {
		add("attacks", riderEndIn(m), m[0])
		if m[1] != "" {
			gm = append(gm, "Disadvantage on ability checks "+riderEndIn(m).describe(0, 0)+" (ability checks are the GM's)")
		}
	}
	if m := disadvConcRe.FindStringSubmatch(text); m != nil && riderEndIn(m) != riderEndNone {
		add("concentration", riderEndIn(m), m[0])
	}
	if m := disadvSaveRe.FindStringSubmatch(text); m != nil && riderEndIn(m) != riderEndNone {
		var abilities []string
		for _, a := range abilityNameList.FindAllString(m[1], -1) {
			abilities = append(abilities, shortAbility(strings.ToLower(a)))
		}
		add("save:"+strings.Join(abilities, ","), riderEndIn(m), m[0])
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
	case strings.HasPrefix(mode, "save:"):
		abilities := strings.Split(strings.TrimPrefix(mode, "save:"), ",")
		var names []string
		for _, a := range abilities {
			names = append(names, game.AbilityName(a))
		}
		return "disadv_save:" + strings.Join(abilities, ","), "Disadvantage on " + strings.Join(names, " and ") + " saving throws"
	}
	return "disadv_attacks", "Disadvantage on attack rolls"
}

// giveDisadvantage puts the roll riders on the target. e.SourceID is the creature the text comes from (the riders anchored to its
// turn end on it).
func (e saveEffect) giveDisadvantage(lobbyID, tid int, source string) []string {
	var parts []string
	for _, mode := range e.DisadvMode {
		end := e.RollRiderEnd[mode]
		if mode == "sap" || mode == "exposed" {
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

// saveDisadvantageRider: the saver has a "disadv_save" marker for this ability (a hit or failed save gave it, rollRiderWords).
func saveDisadvantageRider(lobbyID, saver int, ability string) bool {
	if lobbyID <= 0 || saver == 0 {
		return false
	}
	for _, r := range loadConditionRecords(lobbyID, saver) {
		if r.Condition == "disadv_save" && slices.Contains(strings.Split(r.Detail, ","), ability) {
			return true
		}
	}
	return false
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
	for _, re := range []*regexp.Regexp{disadvSapRe, exposedRe, disadvAttackRe, disadvConcRe, disadvSaveRe, noReactRe} {
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
