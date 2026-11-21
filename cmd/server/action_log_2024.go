package main

// Reading the action log.
//
// Every place that shows what happened in a campaign (the feed, the campaign and spectator views, the web feed and log, GM
// status, my-turn's recent events, the GM's nudge, a character's recent actions) reads the actions table through readActions.
// The table allows NULL in every column: GM narration, monster turns and level-ups have no character_id, and many rows have no
// result. Before Nov 15 2026 each reader picked its own columns and its own NULL handling; a reader that scanned a NULL into an
// int or a string failed silently and showed the row blank (the feed showed GM narration as an empty record dated year 0001),
// and each named the GM differently ("System", "Unknown", "DM", "GM").
//
// readActions reads every column NULL-safely, names the actor once (the character, else the campaign's GM, else "GM"), orders
// by time and then id (rows written in the same instant keep their order), and skips nothing it was not asked to skip. A row
// that still can't be read is logged, not shown blank. actionRecord.view is the one JSON form of a row.

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"
)

// actionNarration is the action type of the GM's narration (recordGMAction).
const actionNarration = "narration"

// actionRecord is one row of the action log.
type actionRecord struct {
	ID          int
	CharacterID int    // 0 for a row no character made (GM narration, a monster's turn, a level-up)
	Actor       string // the character's name, else the campaign GM's name, else "GM"
	Type        string
	Description string
	Result      string // as stored: the GM's log, with every number (playerText hides them from players)
	At          time.Time
}

// actionFilter says which rows to read.
type actionFilter struct {
	lobby       int       // the campaign (0: any)
	character   int       // only this character's rows (0: every row)
	inCampaign  bool      // only rows written in a campaign (none written outside one)
	since       time.Time // only rows written after this (zero: no limit)
	within      time.Duration
	skipTypes   []string // action types to leave out ("poll", "joined" ...)
	hidePolls   bool     // leave out the routine "Checked game status" polls
	newestFirst bool
	limit       int // 0: 100
	offset      int
}

// notPlayTypes: the log's bookkeeping records, never a character's play: checking the game, joining, following the party, saying it
// is ready before the start, the GM opening play. Every reader that asks "when did this character last play" leaves them out
// (notPlaySQL), so a new bookkeeping record is one entry here.
var notPlayTypes = []string{"poll", "joined", "left", "following", "ready", "round_open", "started_without", "level_up", "level_range"}

// notPlaySQL: notPlayTypes and any extra types as a quoted SQL list, for "action_type NOT IN (...)".
func notPlaySQL(extra ...string) string {
	var q []string
	for _, t := range append(append([]string{}, notPlayTypes...), extra...) {
		q = append(q, "'"+t+"'")
	}
	return strings.Join(q, ", ")
}

// readActions reads the action log.
func readActions(f actionFilter) ([]actionRecord, error) {
	q := `SELECT a.id, COALESCE(a.character_id, 0), COALESCE(c.name, g.name, 'GM'), COALESCE(a.action_type, ''),
		COALESCE(a.description, ''), COALESCE(a.result, ''), a.created_at
		FROM actions a
		LEFT JOIN characters c ON c.id = a.character_id
		LEFT JOIN lobbies l ON l.id = a.lobby_id
		LEFT JOIN agents g ON g.id = l.dm_id
		WHERE TRUE`
	var args []interface{}
	arg := func(v interface{}) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.lobby != 0 {
		q += " AND a.lobby_id = " + arg(f.lobby)
	}
	if f.character != 0 {
		q += " AND a.character_id = " + arg(f.character)
	}
	if f.inCampaign {
		q += " AND a.lobby_id IS NOT NULL"
	}
	if !f.since.IsZero() {
		q += " AND a.created_at > " + arg(f.since.UTC().Format("2006-01-02 15:04:05.999999"))
	}
	if f.within > 0 {
		q += " AND a.created_at > NOW() - " + arg(fmt.Sprintf("%d seconds", int(f.within.Seconds()))) + "::interval"
	}
	for _, t := range f.skipTypes {
		q += " AND COALESCE(a.action_type, '') <> " + arg(t)
	}
	if f.hidePolls {
		q += " AND NOT (COALESCE(a.action_type, '') = 'poll' AND COALESCE(a.description, '') = 'Checked game status')"
	}
	if f.newestFirst {
		q += " ORDER BY a.created_at DESC NULLS LAST, a.id DESC"
	} else {
		q += " ORDER BY a.created_at ASC NULLS FIRST, a.id ASC"
	}
	if f.limit <= 0 {
		f.limit = 100
	}
	q += " LIMIT " + arg(f.limit)
	if f.offset > 0 {
		q += " OFFSET " + arg(f.offset)
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []actionRecord{}
	for rows.Next() {
		var a actionRecord
		var at sql.NullTime
		if err := rows.Scan(&a.ID, &a.CharacterID, &a.Actor, &a.Type, &a.Description, &a.Result, &at); err != nil {
			log.Printf("action log: row unreadable: %v", err)
			continue
		}
		a.At = at.Time
		out = append(out, a)
	}
	return out, rows.Err()
}

// lastAction is the newest row the filter allows.
func lastAction(f actionFilter) (actionRecord, bool) {
	f.newestFirst, f.limit = true, 1
	rows, err := readActions(f)
	if err != nil || len(rows) == 0 {
		return actionRecord{}, false
	}
	return rows[0], true
}

// view is the JSON form of a row, the same in every reply: the result as players read it (no monster AC or save numbers).
func (a actionRecord) view() map[string]interface{} {
	return map[string]interface{}{
		"id": a.ID, "character_id": a.CharacterID, "actor": a.Actor, "type": a.Type,
		"description": a.Description, "result": playerText(a.Result), "created_at": cursorTime(a.At),
	}
}

// cursorTime is how a feed shows when a record was written: RFC3339 with every fraction of a second the database keeps, so that
// the created_at of the last record read, sent back as ?since=, returns only later records (to the second, a record written in
// the same second came back again).
func cursorTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// isNarration: the row is the GM's narration.
func (a actionRecord) isNarration() bool { return strings.EqualFold(a.Type, actionNarration) }

// parseClientTime reads a time a client sends (a feed's ?since=, a repair tool's timestamp): RFC3339, with or without fractions of a
// second, or a date. ok is false for a value that isn't a time; "" is the zero time.
func parseClientTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, true
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// execer is the database or a transaction.
type execer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}

// writeAction writes one row of the action log: the only writer (TestNoOtherActionLogWriter). charID 0 is a row no character made
// (GM narration, a monster's turn, a level-up). Before Nov 15 2026 each of 56 places wrote its own INSERT, and five named a column
// that doesn't exist (campaign_id, action): those rows were never written, and nothing said so. A failure is logged here and
// returned.
func writeAction(q execer, lobbyID, charID int, actionType, description, result string) error {
	// the shared log is where every reader looks: a GM's private note (splitGMNotes) is never stored in it, whoever the caller is.
	// The callers that know their notes (the narrate route) split earlier and name the monster; this is the boundary for the rest.
	result, held := splitGMNotes(result)
	for _, n := range held {
		addGMNotice(lobbyID, n)
	}
	var char interface{}
	if charID > 0 {
		char = charID
	}
	var lobby interface{}
	if lobbyID > 0 {
		lobby = lobbyID
	}
	_, err := q.Exec(`INSERT INTO actions (lobby_id, character_id, action_type, description, result) VALUES ($1, $2, $3, $4, $5)`,
		lobby, char, actionType, description, result)
	if err != nil {
		log.Printf("action log: couldn't write %q for character %d in campaign %d: %v", actionType, charID, lobbyID, err)
	}
	return err
}
