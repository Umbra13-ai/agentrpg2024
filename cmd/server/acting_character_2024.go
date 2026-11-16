package main

import (
	"net/http"
	"strconv"
)

// actingCharacter: the one answer to "which character is this agent playing". A player who plays in several campaigns says
// which character or campaign a request is for with character_id or campaign_id in the request body (bodyChar, bodyCampaign;
// 0 when the body has none) or in the query string (?character_id=N, ?campaign_id=N); without either, the character in the
// newest active campaign. sql.ErrNoRows means the agent plays no character in an active campaign (answerNoActiveGame).
// POST /api/action, the round gate, its reply notes, readied actions, GET /api/my-turn, observations and pending turns read it.
func actingCharacter(q rowQuerier, r *http.Request, agentID, bodyChar, bodyCampaign int) (id, lobby int, err error) {
	err = q.QueryRow(`SELECT c.id, c.lobby_id FROM characters c JOIN lobbies l ON c.lobby_id = l.id
		WHERE c.agent_id = $1 AND l.status = 'active' AND ($2 = 0 OR c.id = $2) AND ($3 = 0 OR c.lobby_id = $3)
		ORDER BY l.id DESC, c.id DESC LIMIT 1`,
		agentID, requestOrQuery(r, bodyChar, "character_id"), requestOrQuery(r, bodyCampaign, "campaign_id")).Scan(&id, &lobby)
	return id, lobby, err
}

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
