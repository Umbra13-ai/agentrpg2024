package main

import (
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
)

// The suffocation owner for attach-tied suffocating ("... and is suffocating while the darkmantle is
// attached in this way", Darkmantle Crush). SRD 5.2.1 Suffocation: a creature that runs out of breath or
// is choking gains 1 Exhaustion level at the end of each of its turns; when it can breathe again it
// removes the levels it gained from suffocating (the same rule the GM route /api/gm/suffocation runs by
// hand). The state is a combat effect (kind "suffocating"), one row per (attacher, target): two creatures
// covering the same target keep two rows, and a source's end removes exactly what that source gave. The
// row text carries the source label and the count of levels given ("<label> | gained: N").
//
// Lifecycle: the marker starts when the cover clause that carries it applies (the hit's Advantage and
// size gates held - addAttach's coverOK) and ends when the attach that owns it ends (the death sweep, the
// GM route, the detach action: endAttachSuffocation). A character's Exhaustion is the condition owner's
// store; a monster or companion has no Exhaustion store (the GM tracks it, as everywhere else), so its
// turn-end tick is a GM note and its cleanup says how many levels the GM removes.

// attachSuffocates: the action's text ties suffocating to the attach.
func attachSuffocates(desc string) bool {
	for _, s := range sentenceSplitRe.Split(desc, -1) {
		if attachTieRe.MatchString(s) && strings.Contains(strings.ToLower(s), "suffocat") {
			return true
		}
	}
	return false
}

var suffocationRowRe = regexp.MustCompile(`^(.*) \| gained: (\d+)$`)

// startSuffocation records the suffocating state of an attach whose cover applies. A repeated cover (the
// attach row replaced without the attach ending) keeps the row it has: its gained count keeps counting.
func startSuffocation(lobbyID, attacherID, targetID int, source string) {
	var one int
	err := db.QueryRow(`SELECT 1 FROM combat_effects WHERE lobby_id=$1 AND kind='suffocating' AND source_id=$2 AND target_id=$3`,
		lobbyID, attacherID, targetID).Scan(&one)
	if err == nil {
		return
	}
	if err != sql.ErrNoRows {
		log.Printf("suffocation: couldn't read the rows of campaign %d: %v", lobbyID, err)
		return
	}
	if _, err := db.Exec(`INSERT INTO combat_effects (lobby_id, target_id, source_id, kind, text) VALUES ($1,$2,$3,'suffocating',$4)`,
		lobbyID, targetID, attacherID, source+" | gained: 0"); err != nil {
		log.Printf("suffocation: couldn't record the row for source %d in campaign %d: %v", attacherID, lobbyID, err)
	}
}

// suffocationActive: the target is suffocating from this attacher's cover (its sentence is enforced, not
// the GM's).
func suffocationActive(lobbyID, attacherID, targetID int) bool {
	if db == nil {
		return false
	}
	var one int
	return db.QueryRow(`SELECT 1 FROM combat_effects WHERE lobby_id=$1 AND kind='suffocating' AND source_id=$2 AND target_id=$3`,
		lobbyID, attacherID, targetID).Scan(&one) == nil
}

// dropSuffocatingSentences removes the sentences an active suffocation state enforces from a GM residual.
func dropSuffocatingSentences(rider string) string {
	var kept []string
	for _, s := range sentenceSplitRe.Split(rider, -1) {
		if strings.Contains(strings.ToLower(s), "suffocat") {
			continue
		}
		if k := strings.Trim(s, " ,;.\n"); k != "" {
			kept = append(kept, k)
		}
	}
	return strings.Join(kept, ". ")
}

// suffocationTurnEnd: "it gains 1 Exhaustion level at the end of each of its turns" - the suffocating
// creature's own turn end ticks each of its suffocation rows, whatever its kind: the level lands in the
// cross-target Exhaustion owner's store (exhaustion_any_2024.go), whose immunity table can refuse it.
// The row counts what was given, so the end removes exactly that.
func suffocationTurnEnd(lobbyID, id int, name string) (public []string) {
	if db == nil {
		return nil
	}
	rows, err := db.Query(`SELECT id, text FROM combat_effects WHERE lobby_id=$1 AND kind='suffocating' AND target_id=$2`, lobbyID, id)
	if err != nil {
		log.Printf("suffocation: couldn't read the rows of campaign %d: %v", lobbyID, err)
		return nil
	}
	type row struct {
		id     int
		source string
		gained int
	}
	var mine []row
	for rows.Next() {
		var r row
		var text string
		if rows.Scan(&r.id, &text) != nil {
			continue
		}
		if m := suffocationRowRe.FindStringSubmatch(text); m != nil {
			r.source, r.gained = m[1], 0
			r.gained, _ = strconv.Atoi(m[2])
			mine = append(mine, r)
		}
	}
	rows.Close()
	for _, r := range mine {
		note := addExhaustionAny(lobbyID, id, 1, r.source)
		if strings.HasPrefix(note, "no Exhaustion") { // immune: the state lasts, no level, no count
			public = append(public, fmt.Sprintf("%s is suffocating: %s", name, note))
			continue
		}
		gained := r.gained + 1
		if _, err := db.Exec(`UPDATE combat_effects SET text=$1 WHERE id=$2`, fmt.Sprintf("%s | gained: %d", r.source, gained), r.id); err != nil {
			log.Printf("suffocation: couldn't count the turn on row %d: %v", r.id, err)
		}
		public = append(public, fmt.Sprintf("%s is suffocating: it gains 1 Exhaustion level. %s", name, note))
	}
	return public
}

// endAttachSuffocation ends the suffocating state an attach's cover started: the target breathes again
// and sheds exactly the levels this suffocation gave, whatever its kind (the cross-target Exhaustion
// owner; a removal is never immune). The row delete and the Exhaustion removal commit in ONE
// transaction, so a crash can no longer strand the levels the way the old delete-then-remove order
// could; when the crash already took the row, the gained count recorded in the pending_attach_endings
// row (pendAttachEnding read it while the row was live) still says what to remove. "" when this
// attach never suffocated (its cover's gates failed). A non-nil error leaves the pending ending
// recorded for the next drain.
func endAttachSuffocation(lobbyID, attacherID, targetID int, rowText string) (string, error) {
	if !strings.Contains(rowText, "suffocates") {
		return "", nil
	}
	gained := 0 // the count the detach committed with its pending row
	err := db.QueryRow(`SELECT suffocation_gained FROM pending_attach_endings WHERE lobby_id=$1 AND attacher_id=$2 AND target_id=$3`, lobbyID, attacherID, targetID).Scan(&gained)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var rowID int
	var text string
	rowErr := tx.QueryRow(`SELECT id, text FROM combat_effects WHERE lobby_id=$1 AND kind='suffocating' AND source_id=$2 AND target_id=$3 FOR UPDATE`,
		lobbyID, attacherID, targetID).Scan(&rowID, &text)
	switch {
	case rowErr == nil:
		if m := suffocationRowRe.FindStringSubmatch(text); m != nil { // the live row's count wins
			gained, _ = strconv.Atoi(m[2])
		}
		if _, err := tx.Exec(`DELETE FROM combat_effects WHERE id=$1`, rowID); err != nil {
			return "", err
		}
	case rowErr == sql.ErrNoRows && gained == 0:
		return "", nil // the cover never applied, and nothing recorded otherwise
	case rowErr != sql.ErrNoRows:
		return "", rowErr
	}
	name := creatureName(lobbyID, targetID)
	note := fmt.Sprintf(" %s can breathe again.", name)
	if gained > 0 {
		if err := removeExhaustionAnyIn(tx, lobbyID, targetID, gained); err != nil {
			return "", err
		}
		note = fmt.Sprintf(" %s can breathe again and removes the %d Exhaustion level(s) gained from suffocating.", name, gained)
	}
	// The acknowledgment commits WITH the removal: a crash can never leave the levels shed but the
	// pending row live, so a later drain has no count to remove twice (it finds no row and no count
	// and does nothing).
	if _, err := tx.Exec(`DELETE FROM pending_attach_endings WHERE lobby_id=$1 AND attacher_id=$2 AND target_id=$3`, lobbyID, attacherID, targetID); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return note, nil
}

// removeExhaustionAnyIn removes n levels inside tx, mirroring addExhaustionAny's two stores (the
// characters column, the condition record otherwise). A removal never kills and is never immune.
func removeExhaustionAnyIn(tx *sql.Tx, lobbyID, id, n int) error {
	lob := 0
	if id < 0 {
		lob = lobbyID
	}
	return changeConditionsIn(tx, lob, id, func(c *conditionChange) error {
		if id > 0 && !isCompanionCombatID(id) {
			return c.setExhaustion(c.exhaustion - n)
		}
		cur := 0
		for _, r := range c.recs {
			if r.Condition == "exhaustion" {
				cur, _ = strconv.Atoi(r.Detail)
				break
			}
		}
		level := max(0, min(6, cur-n))
		if level == cur {
			return nil
		}
		if _, err := c.remove(func(r conditionRecord) bool { return r.Condition == "exhaustion" }); err != nil {
			return err
		}
		if level > 0 {
			_, err := c.insert(conditionRecord{Condition: "exhaustion", Detail: strconv.Itoa(level), Source: "suffocating"})
			return err
		}
		return nil
	})
}
