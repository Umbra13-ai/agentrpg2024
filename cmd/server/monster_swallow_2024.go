package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
)

// monster_swallow_2024.go: the published Swallow lifecycle (SRD 5.2.1 Giant Frog). A swallow action
// takes a Small-or-smaller target the monster is grappling: the grapple ends, the swallowed target
// has the Blinded and Restrained conditions (owned by the swallow, they end with it), and at the end
// of the monster's next turn the target takes the listed damage; a survivor is disgorged and exits
// Prone. While swallowing, the monster is locked out of the action its text names ("the frog can't
// use Bite" - monster_attack_lockout_2024.go). If the monster dies, the swallowed target is no longer
// Restrained; the corpse escape costs 5 feet of movement, which is the open movement-state gap's GM
// adjudication, with the Prone exit noted to the GM.
//
// The state row is combat_effects kind='swallowed' (source_id = the monster, target_id = the meal).
// Its text carries the owned conditions in the attach owner's format ("owns: ... @ source") so
// endAttachOwnedConditions ends them, plus the damage spec and the arming marker for the next-turn
// schedule. Total Cover against attacks and effects from outside the monster is NOT yet enforced at
// targeting time: recorded open scope, GM adjudicated until the targeting gate lands.

var swallowActionRe = regexp.MustCompile(`(?i)swallows an? (\w+) or smaller target it is grappling`)
var swallowAcidRe = regexp.MustCompile(`(?i)at the end of the [\w() '’-]+ next turn, the swallowed target takes (\d+) \(([^)]+)\) (\w+) damage`)
var swallowCondsRe = regexp.MustCompile(`(?i)has the ([A-Za-z]+) and ([A-Za-z]+) conditions`)
var swallowArmedRe = regexp.MustCompile(`\| armed`)

type swallowSpec struct {
	maxSize  string
	acidAvg  string
	acidDice string
	acidType string
	conds    []string
}

// readSwallow parses the published swallow grammar; nil when the text is not a swallow action. Every
// clause the lifecycle enforces must be present: a partial text stays the GM's whole, never silently
// half-enforced.
func readSwallow(text string) *swallowSpec {
	m := swallowActionRe.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	spec := &swallowSpec{maxSize: strings.ToLower(m[1])}
	if _, ok := sizeOrder[spec.maxSize]; !ok {
		return nil
	}
	a := swallowAcidRe.FindStringSubmatch(text)
	if a == nil || !strings.Contains(text, "disgorges") {
		return nil
	}
	spec.acidAvg, spec.acidDice, spec.acidType = a[1], strings.ReplaceAll(a[2], " ", ""), strings.ToLower(a[3])
	cm := swallowCondsRe.FindStringSubmatch(text)
	if cm == nil {
		return nil
	}
	spec.conds = []string{strings.ToLower(cm[1]), strings.ToLower(cm[2])}
	return spec
}

// swallowRow reads the monster's one live swallow row.
func swallowRow(lobbyID, monsterID int) (rowID, targetID int, text string, ok bool) {
	err := db.QueryRow(`SELECT id, target_id, text FROM combat_effects WHERE lobby_id=$1 AND kind='swallowed' AND source_id=$2`, lobbyID, monsterID).Scan(&rowID, &targetID, &text)
	if err == sql.ErrNoRows {
		return 0, 0, "", false
	}
	if err != nil {
		log.Printf("swallow: couldn't read the row for source %d in campaign %d: %v", monsterID, lobbyID, err)
		return 0, 0, "", false
	}
	return rowID, targetID, text, true
}

func swallowGrappleHeld(lobbyID, monsterID, targetID int) bool {
	return len(findConditionRecords(`target_id = $1 AND source_id = $2 AND condition = 'grappled'`, targetID, monsterID)) > 0
}

// resolveMonsterSwallow: the swallow action itself. Every gate is an atomic refusal before any side
// effect; on success the grapple ends and the row plus its owned conditions are written together.
func resolveMonsterSwallow(r *http.Request, lobbyID, monsterID int, entryName, text string, targets []int, apply bool) ([]string, bool) {
	spec := readSwallow(text)
	if spec == nil {
		return nil, false
	}
	name := creatureName(lobbyID, monsterID)
	if len(targets) != 1 {
		return []string{fmt.Sprintf("%s: name the one target %s is grappling (target). Nothing was rolled.", entryName, name)}, true
	}
	tid := targets[0]
	tname := creatureName(lobbyID, tid)
	safe := fmt.Sprintf("%s can't swallow %s. Nothing was rolled.", name, tname)
	if _, _, _, ok := swallowRow(lobbyID, monsterID); ok {
		return []string{refusalAudiences(safe, fmt.Sprintf("%s is already swallowing a target (its stat block's rule)", name))}, true
	}
	if !swallowGrappleHeld(lobbyID, monsterID, tid) {
		return []string{refusalAudiences(safe, fmt.Sprintf("%s swallows only a target it is grappling (its stat block's rule)", name))}, true
	}
	if isCompanionCombatID(tid) { // the server does not know a companion's size: the GM checks
		return []string{refusalAudiences(safe, fmt.Sprintf("%s swallows only a target of %s size or smaller: check the companion's size (the GM's ruling)", name, titleCase(spec.maxSize)))}, true
	}
	if sizeOrder[strings.ToLower(creatureSize(lobbyID, tid))] > sizeOrder[spec.maxSize] {
		return []string{refusalAudiences(safe, fmt.Sprintf("%s swallows only a target of %s size or smaller; %s is %s", name, titleCase(spec.maxSize), tname, creatureSize(lobbyID, tid)))}, true
	}
	if !apply {
		return []string{fmt.Sprintf("%s swallows %s (not applied).", name, tname)}, true
	}
	source := name + "'s " + entryName
	// the grapple the swallow rode on ends: "the target isn't Grappled"
	grapples := findConditionRecords(`target_id = $1 AND source_id = $2 AND condition = 'grappled'`, tid, monsterID)
	endConditionRecords(grapples)
	rowText := "swallow | owns: " + strings.Join(spec.conds, ", ") + " @ " + source +
		fmt.Sprintf(" | acid: %s (%s) %s", spec.acidAvg, spec.acidDice, spec.acidType)
	if _, err := db.Exec(`INSERT INTO combat_effects (lobby_id, target_id, source_id, kind, text) VALUES ($1,$2,$3,'swallowed',$4)`, lobbyID, tid, monsterID, rowText); err != nil {
		log.Printf("swallow: couldn't record the row for source %d in campaign %d: %v", monsterID, lobbyID, err)
		return []string{"The swallow could not be recorded. Nothing was rolled."}, true
	}
	for _, cond := range spec.conds {
		addCondition(lobbyID, tid, condAdd{Condition: cond, Source: source, SourceID: monsterID})
	}
	markExecuted(r)
	return []string{fmt.Sprintf("%s swallows %s. %s has the %s and %s conditions. At the end of %s's next turn, %s takes %s (%s) %s damage; if it survives, %s disgorges it and it exits Prone.",
		name, tname, tname, titleCase(spec.conds[0]), titleCase(spec.conds[1]), name, tname, spec.acidAvg, spec.acidDice, titleCase(spec.acidType), name)}, true
}

// swallowArm: the monster's turn start arms the schedule - the damage lands at the end of THIS turn
// ("at the end of the frog's next turn"), never at the end of the turn the swallow happened on.
func swallowArm(lobbyID, monsterID int) {
	if _, err := db.Exec(`UPDATE combat_effects SET text = text || ' | armed' WHERE lobby_id=$1 AND kind='swallowed' AND source_id=$2 AND text NOT LIKE '%| armed%'`, lobbyID, monsterID); err != nil {
		log.Printf("swallow: couldn't arm the rows for source %d in campaign %d: %v", monsterID, lobbyID, err)
	}
}

// swallowTurnEnd: at the end of the monster's turn, every armed row deals its damage; a survivor is
// disgorged and exits Prone (the owned conditions end, the swallow row closes).
func swallowTurnEnd(lobbyID, monsterID int, name string) []string {
	rowID, tid, text, ok := swallowRow(lobbyID, monsterID)
	if !ok || !swallowArmedRe.MatchString(text) {
		return nil
	}
	var log2 []string
	tname := creatureName(lobbyID, tid)
	spec := readSwallowRow(text)
	if spec != nil {
		if applied := recurringApplyOne(lobbyID, tid, *spec); applied != "" {
			log2 = append(log2, fmt.Sprintf("%s %s (swallowed by %s).", tname, applied, name))
		}
	}
	if attachSideAlive(lobbyID, tid) {
		db.Exec(`DELETE FROM combat_effects WHERE id=$1 AND lobby_id=$2`, rowID, lobbyID)
		ended := endAttachOwnedConditions(lobbyID, monsterID, tid, text)
		addCondition(lobbyID, tid, condAdd{Condition: "prone", Source: creatureName(lobbyID, monsterID) + "'s Swallow", SourceID: monsterID})
		log2 = append(log2, fmt.Sprintf("%s disgorges %s%s; it exits Prone.", name, tname, attachEndedNote(lobbyID, tid, ended)))
	}
	return log2
}

// readSwallowRow reads the damage spec back off a stored row.
func readSwallowRow(text string) *recurringDamageSpec {
	m := regexp.MustCompile(`acid: \d+ \(([^)]+)\) (\w+)`).FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	return &recurringDamageSpec{Dice: m[1], Type: m[2]}
}

// swallowSweep: a dead monster's swallow ends its Restrained at the first turn start after the death
// (same sweep model as attachSweep). The corpse escape costs 5 feet of movement - the open
// movement-state gap, so the escape itself and its Prone exit are the GM's adjudication, said in the
// note; the row and the Blinded stay until the GM's escape or the combat's end clears them.
func swallowSweep(lobbyID int) (notes []string) {
	if db == nil {
		return nil
	}
	rows, err := db.Query(`SELECT id, source_id, target_id, text FROM combat_effects WHERE lobby_id=$1 AND kind='swallowed'`, lobbyID)
	if err != nil {
		log.Printf("swallow sweep: couldn't read campaign %d: %v", lobbyID, err)
		return
	}
	type ended struct {
		rowID, src, tgt int
		text            string
	}
	var stale []ended
	for rows.Next() {
		var e ended
		if rows.Scan(&e.rowID, &e.src, &e.tgt, &e.text) == nil && !monsterAlive(lobbyID, e.src) {
			stale = append(stale, e)
		}
	}
	rows.Close()
	for _, e := range stale {
		where := `target_id = $1 AND source_id = $2 AND condition = 'restrained'`
		args := []interface{}{e.tgt, e.src}
		if e.tgt < 0 || isCompanionCombatID(e.tgt) {
			where += ` AND lobby_id = $3`
			args = append(args, lobbyID)
		}
		endConditionRecords(findConditionRecords(where, args...))
		notes = append(notes, fmt.Sprintf("%s is no longer Restrained inside %s's corpse and can escape using 5 feet of movement, exiting Prone (the GM adjudicates the movement).",
			creatureName(lobbyID, e.tgt), creatureName(lobbyID, e.src)))
	}
	return notes
}
