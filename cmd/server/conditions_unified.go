package main

// One set of conditions per character.
// The rules' conditions (Blinded, Charmed, Deafened, Frightened, Grappled, Incapacitated, Invisible, Paralyzed, Petrified, Poisoned,
// Prone, Restrained, Stunned, Unconscious) live in condition_records, for characters as for monsters: that is where their source,
// duration, repeated save and ways to escape are kept. The characters.conditions list mirrors them (the many readers that call
// getCharConditions keep working) and also holds markers that are not conditions (raging, reckless, dodging, hidden, sanctuary:DC,
// sacred_weapon:..., exhaustion:N ...), which only live there. Exhaustion is the exhaustion_level column.
// Writers keep the two in step with one of two calls:
//	syncRecordsFromList(charID)  after code changed the characters.conditions list (the list is right): a condition in the list
//	                             without a record gets one; a record whose condition left the list is deleted.
//	syncListFromRecords(charID)  after code changed condition_records (the records are right): the list gets the conditions of the
//	                             records and loses the conditions that have no record. Markers are never touched.
// reconcileAllConditions runs at startup and joins both sides (nothing is lost) for characters whose two sides drifted apart.

import (
	"encoding/json"
	"strings"
)

var ruleConditions = map[string]bool{
	"blinded": true, "charmed": true, "deafened": true, "frightened": true, "grappled": true, "incapacitated": true, "invisible": true,
	"paralyzed": true, "petrified": true, "poisoned": true, "prone": true, "restrained": true, "stunned": true, "unconscious": true,
}

// conditionBase is the condition without its parameter ("charmed:12" -> "charmed").
func conditionBase(c string) string {
	c = strings.ToLower(strings.TrimSpace(c))
	if i := strings.Index(c, ":"); i >= 0 {
		return c[:i]
	}
	return c
}

func isRuleCondition(c string) bool { return ruleConditions[conditionBase(c)] }

func writeCharList(charID int, list []string) {
	if list == nil {
		list = []string{}
	}
	b, _ := json.Marshal(list)
	db.Exec(`UPDATE characters SET conditions = $1 WHERE id = $2`, string(b), charID)
}

// recordBases are the conditions that have a record for the character (in its campaign).
func recordBases(lobbyID, charID int) map[string]bool {
	out := map[string]bool{}
	for _, r := range loadConditionRecords(lobbyID, charID) {
		out[conditionBase(r.Condition)] = true
	}
	return out
}

// syncRecordsFromList: the characters.conditions list is right; make the records match it.
func syncRecordsFromList(charID int) {
	if db == nil || charID <= 0 {
		return
	}
	lobby := lobbyOfChar(charID)
	inList := map[string]bool{}
	for _, c := range getCharConditions(charID) {
		if isRuleCondition(c) {
			inList[conditionBase(c)] = true
		}
	}
	have := recordBases(lobby, charID)
	for base := range inList {
		if !have[base] {
			db.Exec(`INSERT INTO condition_records (lobby_id, target_id, condition, source, escapes, duration_rounds, notes) VALUES ($1,$2,$3,'','[]',0,'')`, lobby, charID, base)
		}
	}
	for base := range have {
		if !inList[base] {
			db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND condition = $3`, lobby, charID, base)
		}
	}
}

// syncListFromRecords: the records are right; make the characters.conditions list match them (markers stay).
func syncListFromRecords(charID int) {
	if db == nil || charID <= 0 {
		return
	}
	have := recordBases(lobbyOfChar(charID), charID)
	var list []string
	inList := map[string]bool{}
	for _, c := range getCharConditions(charID) {
		if isRuleCondition(c) && !have[conditionBase(c)] {
			continue // its record is gone
		}
		list = append(list, c)
		inList[conditionBase(c)] = true
	}
	for base := range have {
		if !inList[base] {
			list = append(list, base)
		}
	}
	writeCharList(charID, list)
}

// deleteConditionRecord removes one record and keeps a character's list in step.
func deleteConditionRecord(recordID int) {
	var target int
	if db.QueryRow(`DELETE FROM condition_records WHERE id = $1 RETURNING target_id`, recordID).Scan(&target) == nil && target > 0 {
		syncListFromRecords(target)
	}
}

// dropMarkersKeepConditions removes the combat markers (dodging, reckless, hidden ...) from a character's list and keeps the rules'
// conditions: starting a fight must not cure Poisoned or stand a Prone character up.
func dropMarkersKeepConditions(charID int) {
	var keep []string
	for _, c := range getCharConditions(charID) {
		if isRuleCondition(c) || strings.HasPrefix(c, "exhaustion:") || strings.HasPrefix(c, "disease:") {
			keep = append(keep, c)
		}
	}
	writeCharList(charID, keep)
}

// reconcileAllConditions joins both sides for every character in a campaign: a condition in the list without a record gets one, and
// a record without the condition in the list adds it to the list.
func reconcileAllConditions() {
	if db == nil {
		return
	}
	rows, err := db.Query(`SELECT id FROM characters WHERE COALESCE(lobby_id,0) <> 0`)
	if err != nil {
		return
	}
	var ids []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		lobby := lobbyOfChar(id)
		have := recordBases(lobby, id)
		list := getCharConditions(id)
		inList := map[string]bool{}
		for _, c := range list {
			if isRuleCondition(c) {
				inList[conditionBase(c)] = true
			}
		}
		changed := false
		for base := range have {
			if !inList[base] {
				list = append(list, base)
				changed = true
			}
		}
		if changed {
			writeCharList(id, list)
		}
		for base := range inList {
			if !have[base] {
				db.Exec(`INSERT INTO condition_records (lobby_id, target_id, condition, source, escapes, duration_rounds, notes) VALUES ($1,$2,$3,'','[]',0,'')`, lobby, id, base)
			}
		}
	}
}
