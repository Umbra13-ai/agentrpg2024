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
	"net/http"
	"strconv"
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
	lobby      int       // the campaign (0: any)
	character  int       // only this character's rows (0: every row)
	inCampaign bool      // only rows written in a campaign (none written outside one)
	since      time.Time // only rows written after this (zero: no limit)
	before     time.Time // only rows written before this (zero: no limit)
	within     time.Duration
	skipTypes  []string // action types to leave out ("poll", "joined" ...)
	withPolls  bool     // keep the routine "Checked game status" polls: only "when did the player last look" wants them (a read
	// is not play: every view of the log leaves them out, I-CALLA-19)
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
	if !f.before.IsZero() {
		q += " AND a.created_at < " + arg(f.before.UTC().Format("2006-01-02 15:04:05.999999"))
	}
	if f.within > 0 {
		q += " AND a.created_at > NOW() - " + arg(fmt.Sprintf("%d seconds", int(f.within.Seconds()))) + "::interval"
	}
	for _, t := range f.skipTypes {
		q += " AND COALESCE(a.action_type, '') <> " + arg(t)
	}
	if !f.withPolls {
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
		"description": playerText(a.Description), "result": playerText(a.Result), "created_at": cursorTime(a.At),
	}
}

// eventLine: the record as one line of a player's recent events (GET /api/my-turn): the GM's narration, or who did what, with the
// player's own words kept beside the server's result (a Dodge, a Dash or a Help said only its rule before: I-CALLA-13); the
// numbers players don't see are hidden by playerText.
func (a actionRecord) eventLine() string {
	desc, res := strings.TrimSpace(playerText(a.Description)), strings.TrimSpace(playerText(a.Result))
	switch {
	case a.Type == "level_up": // posted by the server, not the GM
		return "[Server]: " + desc
	case a.isNarration():
		return "[DM]: " + desc
	case res == "":
		return fmt.Sprintf("%s: %s", a.Actor, desc)
	case a.CharacterID > 0 && desc != "" && desc != res && !strings.Contains(res, desc):
		return fmt.Sprintf("%s %s: %s → %s", a.Actor, a.Type, desc, res)
	}
	return fmt.Sprintf("%s %s: %s", a.Actor, a.Type, res)
}

// cursorTime is how a feed shows when a record was written: RFC3339 with every fraction of a second the database keeps, so that
// the created_at of the last record read, sent back as ?since=, returns only later records (to the second, a record written in
// the same second came back again).
func cursorTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// isNarration: the row is the GM's narration.
func (a actionRecord) isNarration() bool { return strings.EqualFold(a.Type, actionNarration) }

// logPage is a page of a campaign's history (the feed's actions and messages, the party chat), the one paging rule of every
// list a client reads in pages (playtest I-GM-25: the feed always gave the oldest 100 records and ignored every paging word):
//   - neither since nor before: the newest records;
//   - since (the created_at of the last record read): the records after it, from the oldest on (reading forward);
//   - before (the created_at of the first record of a page): the records before it, the newest of them (reading back);
//   - limit: at most this many (1 to 100, default 100).
//
// A page is always oldest first, and more says whether records are left in the direction read (newer after a since page, older
// otherwise).
type logPage struct {
	since, before time.Time
	limit         int
}

// logPageParams: the query parameters of a paged list, for the replies that explain them.
const logPageParams = "since (the created_at of the last record you read: the records after it), before (the created_at of the first record of a page: the records before it), limit (1-100, default 100)"

// readLogPage reads a paged list's query parameters, or answers 400 (invalid_since, invalid_before, invalid_limit) and says no.
func readLogPage(w http.ResponseWriter, r *http.Request) (p logPage, ok bool) {
	q := r.URL.Query()
	refuse := func(code, msg string) (logPage, bool) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": code, "message": msg, "paging": logPageParams})
		return p, false
	}
	if p.since, ok = parseClientTime(q.Get("since")); !ok {
		return refuse("invalid_since", "since must be a time in RFC3339 form, such as 2026-11-15T16:05:00Z (the created_at of the last record you read).")
	}
	if p.before, ok = parseClientTime(q.Get("before")); !ok {
		return refuse("invalid_before", "before must be a time in RFC3339 form, such as 2026-11-15T16:05:00Z (the created_at of the first record of the page you read).")
	}
	p.limit = 100
	if s := strings.TrimSpace(q.Get("limit")); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 100 {
			return refuse("invalid_limit", "limit must be a whole number from 1 to 100.")
		}
		p.limit = n
	}
	return p, true
}

// newestFirst: the page is read from the newest end (every page but a forward one).
func (p logPage) newestFirst() bool { return p.since.IsZero() || !p.before.IsZero() }

// pageOf finishes a page from rows read in the page's order with one row more than its limit: the extra row means more are left;
// the page is turned oldest first.
func pageOf[T any](rows []T, p logPage) ([]T, bool) {
	more := len(rows) > p.limit
	if more {
		rows = rows[:p.limit]
	}
	if p.newestFirst() {
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
	}
	return rows, more
}

// readActionPage reads a page of the action log (f says which rows; the page sets the time window, the order and the limit).
func readActionPage(f actionFilter, p logPage) ([]actionRecord, bool, error) {
	f.since, f.before, f.limit, f.newestFirst = p.since, p.before, p.limit+1, p.newestFirst()
	rows, err := readActions(f)
	if err != nil {
		return nil, false, err
	}
	recs, more := pageOf(rows, p)
	return recs, more, nil
}

// readMessagePage reads a page of a campaign's party chat, each message in its one JSON form.
func readMessagePage(lobby int, p logPage) ([]map[string]interface{}, bool, error) {
	q := "SELECT id, COALESCE(agent_id, 0), COALESCE(agent_name, ''), COALESCE(message, ''), created_at FROM campaign_messages WHERE lobby_id = $1"
	args := []interface{}{lobby}
	if !p.since.IsZero() {
		args = append(args, p.since.UTC().Format("2006-01-02 15:04:05.999999"))
		q += fmt.Sprintf(" AND created_at > $%d", len(args))
	}
	if !p.before.IsZero() {
		args = append(args, p.before.UTC().Format("2006-01-02 15:04:05.999999"))
		q += fmt.Sprintf(" AND created_at < $%d", len(args))
	}
	if p.newestFirst() {
		q += " ORDER BY created_at DESC, id DESC"
	} else {
		q += " ORDER BY created_at ASC, id ASC"
	}
	args = append(args, p.limit+1)
	q += fmt.Sprintf(" LIMIT $%d", len(args))
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	msgs := []map[string]interface{}{}
	for rows.Next() {
		var id, agentID int
		var agentName, message string
		var createdAt time.Time
		if err := rows.Scan(&id, &agentID, &agentName, &message, &createdAt); err != nil {
			log.Printf("party chat: message row unreadable: %v", err)
			continue
		}
		msgs = append(msgs, map[string]interface{}{
			"id": id, "agent_id": agentID, "agent_name": agentName,
			"message": message, "type": "message",
			"created_at": cursorTime(createdAt),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	msgs, more := pageOf(msgs, p)
	return msgs, more, nil
}

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
//
// A character's refused request ("Cannot ...": nothing was spent) is answered to its sender only and never written: the log is the
// story every player and the GM reads (playtest I-CALLA-17).
func writeAction(q execer, lobbyID, charID int, actionType, description, result string) error {
	if charID > 0 && isRefusalResult(result) {
		return nil
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
