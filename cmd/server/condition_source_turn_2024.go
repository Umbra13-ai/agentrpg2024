package main

import (
	"fmt"
	"strings"
)

const (
	notesUntilStartOfSourceTurn = "until_start_of_next_turn"
	notesUntilEndOfSourceTurn   = "until_end_of_next_turn"
	notesSkipSuffix             = ":skip_current"
)

func sourceTurnEdge(notes string) string {
	switch notes {
	case notesUntilStartOfSourceTurn:
		return "start"
	case notesUntilEndOfSourceTurn, notesUntilEndOfSourceTurn + notesSkipSuffix:
		return "end"
	}
	return ""
}

// Both turn hooks use the exact source id. Legacy records without an id may
// still match the historical monster naming patterns.
func expireSourceTurnConditions(lobby, source int, name, edge string) []string {
	if db == nil || source == 0 {
		return nil
	}
	recorded := func(notes string) []conditionRecord {
		return findConditionRecords(inCampaignSQL+` AND notes = $3 AND
   (source_id = $2 OR ($2 < 0 AND COALESCE(source_id, 0) = 0 AND
    (starts_with(source, $4) OR starts_with(source, $5))))`, lobby, source, notes, name+"'s ", name+" (")
	}
	notes := notesUntilStartOfSourceTurn
	if edge == "end" {
		notes = notesUntilEndOfSourceTurn
	}
	var lines []string
	for _, r := range endConditionRecords(recorded(notes)) {
		if r.Condition == damageDisadvantageMarker {
			lines = append(lines, fmt.Sprintf("%s's Disadvantage from %s ends.", name, r.Source))
			continue
		}
		lines = append(lines, fmt.Sprintf("The %s condition on %s ends (%s of %s's turn).", r.Condition, creatureName(lobby, r.TargetID), edge, name))
	}
	if edge == "end" {
		renoteConditionRecords(recorded(notes+notesSkipSuffix), notes)
	}
	return lines
}

func conditionSourceFighting(lobby int, r conditionRecord) bool {
	entries := combatEntries(lobby)
	var source int
	for _, entry := range entries {
		id := asInt(entry["id"])
		if r.SourceID != 0 {
			if id == r.SourceID {
				source = id
				break
			}
			continue
		}
		name := fmt.Sprint(entry["name"])
		if id < 0 && (strings.HasPrefix(r.Source, name+"'s ") || strings.HasPrefix(r.Source, name+" (")) {
			source = id
			break
		}
	}
	return source != 0 && !combatDeadFn(lobby)(source)
}

// The GM sees conditions whose source cannot take the next turn named by its
// text. The holder keeps the condition pending adjudication.
func conditionOutlastsSourceNotices(lobby, id int, name string) []string {
	var notices []string
	for _, r := range loadConditionRecords(lobby, id) {
		edge := sourceTurnEdge(r.Notes)
		if edge == "" || conditionSourceFighting(lobby, r) {
			continue
		}
		reason := "it is down or no longer in the fight"
		if isCompanionCombatID(r.SourceID) {
			if _, active := activeCompanionInCampaign(lobby, r.SourceID); active {
				reason = "the companion has no individual turn in this fight"
			}
		}
		notices = append(notices, fmt.Sprintf("%s still has the %s condition from %s: it lasts until the %s of that creature's next turn, and %s. Nothing was changed. The GM can adjudicate its end with the conditions route.", name, r.Condition, r.Source, edge, reason))
	}
	return notices
}

// In the owner's initiative mode a companion can act without its own turn.
// The stat block's source-turn duration then needs a GM ruling.
func noticeIfCompanionSourceHasNoTurn(lobby, target, source int, condition, notes string) {
	edge := sourceTurnEdge(notes)
	if edge == "" || !isCompanionCombatID(source) {
		return
	}
	if _, active := activeCompanionInCampaign(lobby, source); !active {
		return
	}
	if !conditionSourceFighting(lobby, conditionRecord{SourceID: source}) {
		addGMNotice(lobby, fmt.Sprintf("%s has the %s condition until the %s of %s's next turn. The companion has no individual turn in this fight. Adjudicate when the condition ends with the conditions route.", creatureName(lobby, target), condition, edge, creatureName(lobby, source)))
	}
}
