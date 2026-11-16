package main

// A database failure is never reported as something else.
//
// Before Nov 15 2026 a failed read was taken for a missing row almost everywhere: with the database failing, a request about a
// character that exists answered 404 character_not_found, one about the GM's own campaign 403 not_gm, a list answered 200 with
// nothing in it, and a write answered success (a local fault test: 202 of 293 routes answered falsely). Only a missing row
// (sql.ErrNoRows) means "not found"; any other error is the server's problem and is answered as one, once, here:
// 500 {"error": "database_error", "message": ...}, logged with what was being read or written.
//
//   - dbFailed(err): err is a real failure (not nil, not a missing row).
//   - answerDBFailure(w, err, what): for a lookup: on a real failure it answers 500 and returns true; a missing row is left to the
//     caller's own 404 (or default).
//   - writeDBError / writeDBWriteError: the two answers, for a read and for a change that may not have been saved.
//   - dbFailureError(err, what): what a helper returns for a database failure when its other errors are refusals a player
//     reads (err.Error() of it is this file's words, never the driver's); the handler answers it with answerHelperDBFailure.
//
// No reply carries a database or driver error's own text ("pq: ...", "dial tcp ...: connection refused"): it goes to the log
// only. TestNoDatabaseErrorTextInReplies scans the handlers; guardDB (db_guard_2024.go) answers any reply of a request whose
// database call failed with this shape, whatever status the handler chose, unless the handler answered through this file.

import (
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
)

const dbErrorCode = "database_error"

// dbFailed: err is a database failure, not a missing row.
func dbFailed(err error) bool { return err != nil && !errors.Is(err, sql.ErrNoRows) }

// dbReadMessage is what a player reads when a lookup failed.
func dbReadMessage(what string) string {
	return fmt.Sprintf("The server couldn't read %s just now: a database problem on the server, not a problem with your request. Nothing was changed; try again in a minute.", what)
}

// writeDBError answers a request whose lookup failed.
func writeDBError(w http.ResponseWriter, err error, what string) {
	log.Printf("database error reading %s: %v", what, err)
	dbFailureAnswered()
	writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": dbErrorCode, "message": dbReadMessage(what)})
}

// writeDBWriteError answers a request whose change failed to save (part of it may have been saved before the failure).
func writeDBWriteError(w http.ResponseWriter, err error, what string) {
	if err != nil {
		log.Printf("database error saving %s: %v", what, err)
	}
	dbFailureAnswered()
	writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": dbErrorCode, "message": dbWriteMessage(what)})
}

// dbWriteMessage is what a player reads when a change failed to save.
func dbWriteMessage(what string) string {
	return fmt.Sprintf("The server couldn't save %s: a database problem on the server, not a problem with your request. Check the result before trying again: part of it may have been saved.", what)
}

// dbFailure is a helper's database failure: its text is dbWriteMessage, the driver's error is kept for the log.
type dbFailure struct {
	what string
	err  error
}

func (f dbFailure) Error() string { return dbWriteMessage(f.what) }
func (f dbFailure) Unwrap() error { return f.err }

// dbFailureError: what a helper returns for err (from a database call) when its other errors are refusals: a real failure
// becomes a dbFailure (what it was saving or reading); nil and a missing row are returned as they are.
func dbFailureError(err error, what string) error {
	if !dbFailed(err) {
		return err
	}
	return dbFailure{what, err}
}

// answerHelperDBFailure answers a helper's error when it is a database failure (true: answered, stop); a refusal and nil return
// false for the caller to answer.
func answerHelperDBFailure(w http.ResponseWriter, err error) bool {
	var f dbFailure
	if errors.As(err, &f) {
		writeDBWriteError(w, f.err, f.what)
		return true
	}
	return false
}

// answerDBFailure answers a lookup's error when it is a database failure (true: answered, stop); a missing row and no error
// return false for the caller to handle.
func answerDBFailure(w http.ResponseWriter, err error, what string) bool {
	if dbFailed(err) {
		writeDBError(w, err, what)
		return true
	}
	return false
}

// writeDBErrorPage is writeDBError for a web page: the same words, as a page.
func writeDBErrorPage(w http.ResponseWriter, err error, what string) {
	log.Printf("database error reading %s: %v", what, err)
	dbFailureAnswered()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprint(w, wrapHTML("Database problem", "<h1>Database problem</h1><p>"+template.HTMLEscapeString(dbReadMessage(what))+"</p>"))
}

// notFoundOrDBError: the code a helper returns for a failed lookup: dbErrorCode when the database failed, else notFound.
func notFoundOrDBError(err error, notFound string) string {
	if dbFailed(err) {
		log.Printf("database error: %v", err)
		return dbErrorCode
	}
	return notFound
}

// rowQuerier is the database or a transaction, for a read. A helper called inside a transaction reads through the transaction
// (never db): a second connection's read waits behind any lock the transaction holds or is queued behind, which deadlocked
// the whole server once (audit AUD-02: a sale's transaction, its own read of the character on another connection, and an
// ALTER TABLE waiting between them).
type rowQuerier interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}

// inTx runs change in one transaction: committed when it returns nil, rolled back otherwise (a failed commit is a dbFailure).
func inTx(change func(tx *sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return dbFailureError(err, "the change")
	}
	defer tx.Rollback()
	if err := change(tx); err != nil {
		return err
	}
	return dbFailureError(tx.Commit(), "the change")
}
