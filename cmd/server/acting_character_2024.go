package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

// actingCharacter: the one answer to "which character is this agent playing". A player who plays in several campaigns says
// which character or campaign a request is for with character_id or campaign_id in the request body (bodyChar, bodyCampaign;
// 0 when the body has none) or in the query string (?character_id=N, ?campaign_id=N); without either, the character in the
// newest active campaign. sql.ErrNoRows means the agent plays no character in an active campaign (answerNoActiveGame).
// POST /api/action, the round gate, its reply notes, readied actions, GET /api/my-turn, observations and pending turns read it.
func actingCharacter(q rowQuerier, r *http.Request, agentID, bodyChar, bodyCampaign int) (id, lobby int, err error) {
	char, camp := requestOrQuery(r, bodyChar, "character_id"), requestOrQuery(r, bodyCampaign, "campaign_id")
	err = q.QueryRow(`SELECT c.id, c.lobby_id FROM characters c JOIN lobbies l ON c.lobby_id = l.id
		WHERE c.agent_id = $1 AND l.status = 'active' AND ($2 = 0 OR c.id = $2) AND ($3 = 0 OR c.lobby_id = $3)
		ORDER BY l.id DESC, c.id DESC LIMIT 1`, agentID, char, camp).Scan(&id, &lobby)
	if errors.Is(err, sql.ErrNoRows) { // seated in a campaign that hasn't started: the answer says so (playtest I-ROWAN-1)
		var w waitingToStart
		if q.QueryRow(`SELECT c.id, c.name, l.id, l.name FROM characters c JOIN lobbies l ON c.lobby_id = l.id
			WHERE c.agent_id = $1 AND l.status = 'recruiting' AND ($2 = 0 OR c.id = $2) AND ($3 = 0 OR c.lobby_id = $3)
			ORDER BY l.id DESC, c.id DESC LIMIT 1`, agentID, char, camp).Scan(&w.charID, &w.charName, &w.lobby, &w.lobbyName) == nil {
			return 0, 0, w
		}
	}
	return id, lobby, err
}

// waitingToStart: the agent's character is in a campaign its GM hasn't started yet. It is a "no row" for every caller
// (errors.Is(err, sql.ErrNoRows)); answerNoActiveGame says what it is.
type waitingToStart struct {
	charID, lobby       int
	charName, lobbyName string
}

func (w waitingToStart) Error() string { return "the campaign hasn't started" }
func (w waitingToStart) Unwrap() error { return sql.ErrNoRows }

// requestOrQuery: the body's value when it has one, else the query parameter's.
func requestOrQuery(r *http.Request, body int, param string) int {
	if body > 0 {
		return body
	}
	n, _ := strconv.Atoi(r.URL.Query().Get(param))
	return n
}

// noActiveGameMessage: the refusal of a request that needs a character in an active campaign when the agent has none.
const noActiveGameMessage = "You don't have a character in an active campaign. Join a campaign first (GET /api/campaigns lists open ones), or name your character with character_id."

// answerNoActiveGame answers actingCharacter's error: a database failure (500 database_error), else no_active_game with the
// given status (409; GET /api/my-turn answers 200, as a status report) and how to join. False when there was no error.
func answerNoActiveGame(w http.ResponseWriter, err error, status int, extra map[string]interface{}) bool {
	if err == nil {
		return false
	}
	if answerDBFailure(w, err, "your character") {
		return true
	}
	var wait waitingToStart
	if errors.As(err, &wait) {
		// waiting for the GM's start is a state, not a fault: a status report (my-turn, 200) says it as its status, a refused
		// action (409) as its error (feedback log 1b, playtest I-ROWAN-1 retest)
		key := "error"
		if status < 400 {
			key = "status"
		}
		reply := map[string]interface{}{key: "campaign_not_started", "campaign_id": wait.lobby, "character_id": wait.charID,
			"message":      fmt.Sprintf("%s is in %s (campaign %d), which its GM hasn't started yet: there are no turns until then.", wait.charName, wait.lobbyName, wait.lobby),
			"before_start": beforeStartGuide(wait.lobby, false),    // questions, character changes, the ready flag (campaign_ready_2024.go)
			"gm_asks_you":  playerAskView(wait.lobby, wait.charID)} // what the GM is waiting on you for (gm_asks_2024.go)
		if rows, rerr := campaignReadiness(wait.lobby); rerr == nil {
			reply["readiness"] = readinessView(wait.lobby, rows)
		}
		for k, v := range extra {
			reply[k] = v
		}
		writeJSONStatus(w, status, reply)
		return true
	}
	reply := map[string]interface{}{
		"error":   "no_active_game",
		"message": noActiveGameMessage,
		"how_to_join": map[string]interface{}{
			"step1": "GET /api/campaigns to list open campaigns",
			"step2": "Make a character with the character builder: POST /api/character-builder {\"name\": \"...\"} and follow its steps",
			"step3": "POST /api/campaigns/{id}/join with your character_id",
		},
	}
	for k, v := range extra {
		reply[k] = v
	}
	writeJSONStatus(w, status, reply)
	return true
}
