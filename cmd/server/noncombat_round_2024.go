package main

// Rounds outside a fight (Matt's directions section 7 and answers 11.2, 11.6, 11.10; turn-schedule-design.md 1.4, stage E). The
// same seats, times, rounds, submissions, deadlines and stand-ins as a fight, without Initiative: the GM resolves the submitted
// turns at its check-ins in an order that suits the story, takes stand-ins at the round's final check-in, and the round closes when
// every turn has resolved; the next opens at once. Which requests are submissions is round_effects_2024.go.
//
//   - GET /api/campaigns/{id}/round: the open round's status (checkInAt) and the submissions (a player sees its own; the GM all).
//     DELETE ...?character_id=N withdraws a player's unresolved submission.
//   - POST .../round/resolve (the GM): applies a character's submission, its steps in order, each as the player's own request
//     (resolveSubmission); or, with a ruling, the GM decides it instead (a rest interrupted, an action that doesn't happen). A
//     submission whose every step is refused leaves the turn unresolved: the player is told why and can send a new one.
//   - POST .../round/stand-in (the GM): a meaningful in-character turn for a character with no submission, only once the round's
//     final check-in has come (standInAllowed).
//   - closeRoundIfDone: when every seated character that chooses its turn has resolved, the round closes and the next opens.
//   - A noncombat round opens when the GM starts the campaign (Matt 11.10), when a fight ends and when the GM turns the schedule on.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"
)

// submissionStep: one saved request of a submission.
type submissionStep struct {
	ID          int       `json:"-"`
	CharacterID int       `json:"character_id"`
	Step        int       `json:"step"`
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	Body        string    `json:"-"`
	What        string    `json:"what"`
	Status      string    `json:"status"` // pending, applied, refused, ruled, withdrawn
	Result      string    `json:"result,omitempty"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// roundSteps reads a round's submission steps (charID 0: every character's), in the order they were sent.
func roundSteps(q rowsQuerier, roundID, charID int) ([]submissionStep, error) {
	rows, err := q.Query(`SELECT id, character_id, step, method, path, body, what, status, result, submitted_at FROM round_submissions
		WHERE round_id = $1 AND ($2 = 0 OR character_id = $2) ORDER BY submitted_at, id`, roundID, charID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []submissionStep
	for rows.Next() {
		var s submissionStep
		if err := rows.Scan(&s.ID, &s.CharacterID, &s.Step, &s.Method, &s.Path, &s.Body, &s.What, &s.Status, &s.Result, &s.SubmittedAt); err != nil {
			return nil, err
		}
		s.SubmittedAt = s.SubmittedAt.UTC()
		s.What = submissionWhat(s.Method, s.Path, []byte(s.Body)) // from the request as sent: the whole text, also of steps saved when it was cut (playtest I-GM-9)
		out = append(out, s)
	}
	return out, rows.Err()
}

// withdrawOpenSubmissions: the open round of a campaign closes before its submissions resolved (a fight starts, the schedule is
// turned off): they are withdrawn, saying why, so none is applied later.
func withdrawOpenSubmissions(tx *sql.Tx, lobby int, why string) error {
	_, err := tx.Exec(`UPDATE round_submissions SET status = 'withdrawn', result = $2
		WHERE status = 'pending' AND round_id IN (SELECT id FROM campaign_rounds WHERE lobby_id = $1 AND closed_at IS NULL)`, lobby, why)
	return err
}

// ---------------------------------------------------------------- GET / DELETE .../round

// handleCampaignRound godoc
// @Summary The open round of a campaign with the turn schedule, and its submissions
// @Description GET (the GM and the campaign's players): check_in, the open round's status for every character (the same answer as GET /api/campaigns/{id}/schedule), and submissions: the steps saved for a round outside a fight (a player sees its own characters', the GM everyone's), each with method, path, what, status (pending: the GM applies it at a check-in; applied; refused, with result: why; ruled: the GM decided it, with the ruling; withdrawn) and submitted_at. In a campaign whose turn schedule is on or paused, a game action sent while a round outside a fight is open (a story action with POST /api/action, a feature, a rest, movement, buying or selling and changing gear once the character has played a round) is saved as the character's submission instead of applied (202, submitted: true); talk, choices, settings and the GM's requests stay immediate. DELETE ?character_id=N (the character's player): withdraws its unresolved submission for the open round; send a new one any time before the round's final check-in.
// @Tags Campaigns
// @Produce json
// @Param id path int true "Campaign ID"
// @Param character_id query int false "DELETE: the character whose submission to withdraw"
// @Param Authorization header string true "Bearer <token>"
// @Success 200 {object} map[string]interface{} "check_in and submissions (GET); withdrawn (DELETE)"
// @Failure 404 {object} map[string]interface{} "no_open_round, nothing_to_withdraw"
// @Router /campaigns/{id}/round [get]
// @Router /campaigns/{id}/round [delete]
func handleCampaignRound(w http.ResponseWriter, r *http.Request, lobby int, rest []string) {
	w.Header().Set("Content-Type", "application/json")
	if len(rest) > 0 {
		switch rest[0] {
		case "resolve":
			handleRoundResolve(w, r, lobby)
		case "stand-in":
			handleRoundStandIn(w, r, lobby)
		case "open":
			handleRoundOpen(w, r, lobby) // play opens after a held start (table_hold_2024.go)
		default:
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "unknown_round_action", "actions": []string{"resolve", "stand-in", "open"}})
		}
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		agentID, role, ok := requireCampaignMember(w, r, lobby, "see the round")
		if !ok {
			return
		}
		in, now, err := loadCheckIn(lobby)
		if answerDBFailure(w, err, "the round's status") {
			return
		}
		reply := map[string]interface{}{"campaign_id": lobby, "check_in": checkInAt(in, now), "submissions": []submissionStep{}}
		if in.Round != nil {
			steps, err := roundSteps(db, in.Round.ID, 0)
			if answerDBFailure(w, err, "the round's submissions") {
				return
			}
			mine := map[int]bool{}
			if role != "gm" {
				ids, err := charactersOfAgentIn(lobby, agentID)
				if answerDBFailure(w, err, "your characters") {
					return
				}
				for _, id := range ids {
					mine[id] = true
				}
			}
			shown := []submissionStep{}
			for _, s := range steps {
				if role == "gm" || mine[s.CharacterID] {
					shown = append(shown, s)
				}
			}
			reply["submissions"] = shown
		}
		json.NewEncoder(w).Encode(reply)
	case http.MethodDelete:
		charID, _ := strconv.Atoi(r.URL.Query().Get("character_id"))
		if !noFields(w, r) {
			return
		}
		if charID <= 0 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "character_id_required", "message": "Send ?character_id=N: the character whose submission to withdraw. Nothing was changed."})
			return
		}
		if _, ok := requireCharacterOwner(w, r, charID, "withdraw its submission"); !ok {
			return
		}
		round, err := openRoundOf(db, lobby)
		if answerDBFailure(w, err, "the open round") {
			return
		}
		if round == nil {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "no_open_round", "message": "This campaign has no open round. Nothing was changed."})
			return
		}
		res, err := db.Exec(`UPDATE round_submissions SET status = 'withdrawn', result = 'withdrawn by the player' WHERE round_id = $1 AND character_id = $2 AND status = 'pending'`, round.ID, charID)
		if err != nil {
			writeDBWriteError(w, err, "the submission")
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "nothing_to_withdraw", "message": fmt.Sprintf("Character %d has no unresolved submission in round %d. Nothing was changed.", charID, round.Number)})
			return
		}
		writeAction(db, lobby, charID, "submission", fmt.Sprintf("%s withdraws the submission for round %d", getCharacterName(charID), round.Number), "")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "withdrawn": true, "round": round.Number,
			"message": fmt.Sprintf("Your submission for round %d is withdrawn. Send a new one any time before the round's final check-in.", round.Number)})
	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "GET or DELETE."})
	}
}

// charactersOfAgentIn: the agent's characters in a campaign.
func charactersOfAgentIn(lobby, agentID int) ([]int, error) {
	rows, err := db.Query(`SELECT id FROM characters WHERE lobby_id = $1 AND agent_id = $2`, lobby, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- the GM's resolve

// openNoncombatRound: the campaign's open round, which must be outside a fight with the schedule in use; it writes the refusal.
func openNoncombatRound(w http.ResponseWriter, lobby int) (*roundRecord, string, bool) {
	state, err := scheduleStateOf(db, lobby)
	if answerDBFailure(w, err, "the campaign's turn schedule") {
		return nil, "", false
	}
	if !usesSchedule(state) {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "schedule_off", "message": "This campaign doesn't use the turn schedule: actions outside a fight apply at once, so there is nothing to resolve. Nothing was changed."})
		return nil, "", false
	}
	round, err := openRoundOf(db, lobby)
	if answerDBFailure(w, err, "the open round") {
		return nil, "", false
	}
	if round == nil {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no_open_round", "message": fmt.Sprintf("No round is open: a round outside a fight opens when the campaign starts (POST /api/campaigns/%d/start) or a fight ends. Nothing was changed.", lobby)})
		return nil, "", false
	}
	if round.Kind != roundNoncombat {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "combat_round", "message": fmt.Sprintf("%s is a fight's round: resolve it with POST /api/campaigns/%d/combat/resolve and combat/stand-in. Nothing was changed.", roundTitle(round.Kind, round.Number), lobby)})
		return nil, "", false
	}
	return round, state, true
}

// handleRoundResolve godoc
// @Summary Resolve submitted turns of a round outside a fight (GM only)
// @Description At your check-in, resolve the submitted turns of the open round outside a fight, in an order that suits the story. {"character_id": N} resolves that character's submission: its steps run in the order they were sent, each as the player's own request (the server checks each one now; a refused step says why). Without character_id every submitted turn resolves, in the order they were sent. {"character_id": N, "ruling": "..."} decides that submission yourself instead of applying it (a rest interrupted by an ambush, an action that can't happen): the ruling is written to the log and the turn is resolved. A turn whose every step is refused is not resolved: the player is told why and can send a new one. When every character who chooses its turn has resolved, the round closes and the next opens at once (round_complete). The reply reminds you of your next check-in and the round's final check-in.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Body which submission, and an optional ruling
// @Success 200 {object} map[string]interface{} "resolved: each character's steps and how its turn ended; round_complete when the round closed"
// @Failure 404 {object} map[string]interface{} "nothing_submitted"
// @Failure 409 {object} map[string]interface{} "schedule_off, no_open_round, combat_round, turn_already_resolved"
// @Router /campaigns/{id}/round/resolve [post]
func handleRoundResolve(w http.ResponseWriter, r *http.Request, lobby int) {
	if !ptRequireGM(w, r, lobby, "resolve the round's turns") {
		return
	}
	var req struct {
		CharacterID int    `json:"character_id"` // the character whose submission to resolve (none: every submitted turn, in the order sent)
		Ruling      string `json:"ruling"`       // decide the submission yourself instead of applying it (needs character_id)
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}
	req.Ruling = strings.TrimSpace(req.Ruling)
	if req.Ruling != "" && req.CharacterID == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "character_id_required", "message": "A ruling decides one character's submission: send character_id with it. Nothing was changed."})
		return
	}
	defer lockPendingWalk(lobby)()
	round, _, ok := openNoncombatRound(w, lobby)
	if !ok {
		return
	}
	steps, err := roundSteps(db, round.ID, req.CharacterID)
	if answerDBFailure(w, err, "the round's submissions") {
		return
	}
	var order []int
	byChar := map[int][]submissionStep{}
	for _, s := range steps {
		if s.Status != "pending" {
			continue
		}
		if _, seen := byChar[s.CharacterID]; !seen {
			order = append(order, s.CharacterID)
		}
		byChar[s.CharacterID] = append(byChar[s.CharacterID], s)
	}
	if len(order) == 0 {
		msg := fmt.Sprintf("No submitted turn is waiting in round %d. Nothing was changed.", round.Number)
		if req.CharacterID != 0 {
			msg = fmt.Sprintf("%s has no submitted turn waiting in round %d. Nothing was changed.", getCharacterName(req.CharacterID), round.Number)
		}
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "nothing_submitted", "message": msg})
		return
	}
	var resolved []map[string]interface{}
	for _, id := range order {
		out, err := resolveSubmission(lobby, round, id, byChar[id], req.Ruling)
		if err != nil {
			writeDBError(w, err, "the round's submissions")
			return
		}
		resolved = append(resolved, out)
	}
	reply := map[string]interface{}{"success": true, "round": round.Number, "resolved": resolved}
	if err := afterRoundChange(lobby, round, reply); err != nil {
		writeDBError(w, err, "the round")
		return
	}
	json.NewEncoder(w).Encode(reply)
}

// resolveSubmission applies (or, with a ruling, decides) one character's pending steps and records how its turn ended. A turn
// whose every step is refused stays unresolved.
func resolveSubmission(lobby int, round *roundRecord, charID int, steps []submissionStep, ruling string) (map[string]interface{}, error) {
	name := getCharacterName(charID)
	out := map[string]interface{}{"character_id": charID, "name": name}
	var owner int
	if err := db.QueryRow(`SELECT COALESCE(agent_id, 0) FROM characters WHERE id = $1`, charID).Scan(&owner); err != nil {
		return nil, err
	}
	var results []map[string]interface{}
	applied := 0
	for _, s := range steps {
		status, result := "ruled", ruling
		if ruling == "" {
			code, body := replayAsPlayer(owner, s.Method, s.Path, []byte(s.Body))
			reply := map[string]interface{}{}
			json.Unmarshal(body, &reply)
			status, result = "applied", replyText(reply)
			if okStep, why := replyOutcome(code, reply); !okStep {
				status, result = "refused", why
			}
			results = append(results, map[string]interface{}{"step": s.Step, "what": s.What, "status": status, "result": result, "reply": reply})
		} else {
			results = append(results, map[string]interface{}{"step": s.Step, "what": s.What, "status": status, "result": result})
		}
		if status != "refused" {
			applied++
		}
		if _, err := db.Exec(`UPDATE round_submissions SET status = $2, result = $3, resolved_at = NOW() WHERE id = $1`, s.ID, status, result); err != nil {
			return nil, err
		}
	}
	out["steps"] = results
	if applied == 0 {
		out["resolved"] = false
		out["message"] = fmt.Sprintf("Every step of %s's submission was refused, so the turn is not resolved: the player is told why and can send a new one before the round's final check-in.", name)
		return out, writeAction(db, lobby, charID, "submission", fmt.Sprintf("%s's submission for round %d was refused at resolution", name, round.Number), "")
	}
	now, err := campaignNow(db, lobby)
	if err != nil {
		return nil, err
	}
	if _, err := recordResult(db, round.ID, charID, false, name, howOwn, now); err != nil {
		return nil, err
	}
	out["resolved"], out["how"] = true, howOwn
	if ruling != "" {
		out["ruling"] = ruling
		if err := writeAction(db, lobby, charID, "ruling", fmt.Sprintf("The GM rules on %s's turn in round %d: %s", name, round.Number, ruling), ""); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// replyText: the words of a route's reply for the log (message, result or narrative).
func replyText(reply map[string]interface{}) string {
	for _, k := range []string{"message", "result", "narrative"} {
		if s, ok := reply[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return "done"
}

// replayAsPlayer sends a saved request as the character's own player, straight to the routes (not through roundGate, so it is
// applied), and answers the status and the reply.
func replayAsPlayer(agentID int, method, path string, body []byte) (int, []byte) {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	rec := serveAs(agentID, req, nil)
	return rec.Code, rec.Body.Bytes()
}

// ---------------------------------------------------------------- the GM's stand-in

// handleRoundStandIn godoc
// @Summary Take a turn as stand-in in a round outside a fight (GM only)
// @Description Once the round's final check-in has come (409 stand_in_too_early with final_check_in before then; none while the schedule is paused), take a meaningful in-character turn for a character with no submission: what the character would do, knowing only what it knows, not a default and not a skip. {"character_id": N, "description": "Mira keeps watch by the fire and studies the map", "action": {an optional POST /api/action body run as the character, such as {"action": "study", "description": "..."}}}. A character with a submission waiting has its submission resolved instead (409 submission_waiting: POST .../round/resolve); a character whose turn has resolved, or whose turn is automatic (dying, stable, unconscious, dead), gets none. When every turn has resolved the round closes and the next opens.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Body the character, what it does, and an optional action
// @Success 200 {object} map[string]interface{} "stand_in: the turn taken; round_complete when the round closed"
// @Failure 400 {object} map[string]interface{} "character_id_required, description_required"
// @Failure 409 {object} map[string]interface{} "stand_in_too_early, submission_waiting, turn_already_resolved, automatic_turn, stand_in_refused"
// @Router /campaigns/{id}/round/stand-in [post]
func handleRoundStandIn(w http.ResponseWriter, r *http.Request, lobby int) {
	if !ptRequireGM(w, r, lobby, "take a turn as stand-in") {
		return
	}
	var req struct {
		CharacterID int                    `json:"character_id"` // the character with no submission
		Description string                 `json:"description"`  // what the character does: a meaningful in-character turn, from what it knows
		Action      map[string]interface{} `json:"action"`       // optional: a POST /api/action body, run as the character (its fields)
	}
	if !readRequest(w, r, &req) {
		return
	}
	req.Description = strings.TrimSpace(req.Description)
	switch {
	case req.CharacterID <= 0:
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "character_id_required", "message": "Send character_id: the character whose turn you take. Nothing was changed."})
		return
	case req.Description == "":
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "description_required", "message": "Send description: what the character does this round, in character and from what it knows (not a default, not a skip). Nothing was changed."})
		return
	}
	defer lockPendingWalk(lobby)()
	round, state, ok := openNoncombatRound(w, lobby)
	if !ok {
		return
	}
	var name string
	var owner int
	err := db.QueryRow(`SELECT c.name, COALESCE(c.agent_id, 0) FROM campaign_seats s JOIN characters c ON c.id = s.character_id WHERE s.lobby_id = $1 AND s.character_id = $2`, lobby, req.CharacterID).Scan(&name, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_in_campaign", "message": fmt.Sprintf("Character %d holds no seat in this campaign. Nothing was changed.", req.CharacterID)})
		return
	}
	if answerDBFailure(w, err, "the character") {
		return
	}
	refuse := func(code, msg string) {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": code, "message": msg + " Nothing was changed."})
	}
	var done string
	err = db.QueryRow(`SELECT how FROM turn_results WHERE round_id = $1 AND combatant_id = $2 AND NOT second_turn`, round.ID, req.CharacterID).Scan(&done)
	if err == nil {
		refuse(codeTurnAlreadyResolve, fmt.Sprintf("%s's turn in round %d has resolved (%s).", name, round.Number, turnResultHows[done]))
		return
	} else if answerDBFailure(w, notFoundIsNothing(err), "the round's results") {
		return
	}
	need, err := turnNeeds(req.CharacterID)
	if answerDBFailure(w, err, "the character") {
		return
	}
	if need.Kind != needChoose {
		refuse("automatic_turn", fmt.Sprintf("%s has no action to choose this round (%s): no stand-in is taken.", name, need.Why))
		return
	}
	var waiting int
	if err := db.QueryRow(`SELECT COUNT(*) FROM round_submissions WHERE round_id = $1 AND character_id = $2 AND status = 'pending'`, round.ID, req.CharacterID).Scan(&waiting); answerDBFailure(w, err, "the round's submissions") {
		return
	}
	if waiting > 0 {
		refuse("submission_waiting", fmt.Sprintf("%s has a submission waiting: a submitted turn is resolved, never replaced. POST /api/campaigns/%d/round/resolve {\"character_id\": %d}.", name, lobby, req.CharacterID))
		return
	}
	now, err := campaignNow(db, lobby)
	if answerDBFailure(w, err, "the campaign's clock") {
		return
	}
	if sc := standInAllowed(state, round, now, false); !sc.Allowed {
		writeJSONStatus(w, http.StatusConflict, sc.refusal(name))
		return
	}
	standIn := map[string]interface{}{"character_id": req.CharacterID, "name": name, "description": req.Description}
	if len(req.Action) > 0 {
		body := map[string]interface{}{}
		for k, v := range req.Action {
			body[k] = v
		}
		body["character_id"], body["campaign_id"] = req.CharacterID, lobby
		raw, _ := json.Marshal(body)
		code, rawReply := replayAsPlayer(owner, http.MethodPost, "/api/action", raw)
		reply := map[string]interface{}{}
		json.Unmarshal(rawReply, &reply)
		if okStep, why := replyOutcome(code, reply); !okStep {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "stand_in_refused", "reply": reply,
				"message": fmt.Sprintf("The stand-in's action for %s was refused: %s. Send another (or none: the description alone is the turn). Nothing was recorded.", name, why)})
			return
		}
		standIn["action_reply"] = reply
	}
	if err := writeAction(db, lobby, req.CharacterID, "stand_in", fmt.Sprintf("Stand-in for %s in round %d: %s", name, round.Number, req.Description), ""); err != nil {
		writeDBError(w, err, "the campaign's log")
		return
	}
	if _, err := recordResult(db, round.ID, req.CharacterID, false, name, howStandIn, now); err != nil {
		writeDBError(w, err, "the round's results")
		return
	}
	reply := map[string]interface{}{"success": true, "round": round.Number, "stand_in": standIn}
	if err := afterRoundChange(lobby, round, reply); err != nil {
		writeDBError(w, err, "the round")
		return
	}
	json.NewEncoder(w).Encode(reply)
}

// ---------------------------------------------------------------- closing the round

// afterRoundChange: after a resolve or a stand-in, the round closes when every turn has resolved (and the next opens), and the
// reply gets the round's status and the GM's reminder (Matt, section 4).
func afterRoundChange(lobby int, round *roundRecord, reply map[string]interface{}) error {
	next, err := closeRoundIfDone(lobby, round)
	if err != nil {
		return err
	}
	if next != nil {
		reply["round_complete"] = roundCompleteText(round.Kind, round.Number, next)
		reply["next_round"] = next.Number
	}
	in, now, err := loadCheckIn(lobby)
	if err != nil {
		return err
	}
	ci := checkInAt(in, now)
	reply["check_in"] = ci
	reply["reminder"] = gmReminder(ci)
	return nil
}

// gmReminder: the GM's next check-in and the round's final check-in, in words (Matt, section 4).
func gmReminder(ci checkIn) string {
	if ci.Round == nil {
		return ""
	}
	parts := []string{roundTitle(ci.Round.Kind, ci.Round.Number)}
	if ci.NextGMCheckIn != nil {
		parts = append(parts, fmt.Sprintf("your next check-in is %s", clockText(*ci.NextGMCheckIn)))
	}
	if ci.Round.FinalCheckIn != nil {
		parts = append(parts, fmt.Sprintf("the round's final check-in is %s", clockText(*ci.Round.FinalCheckIn)))
	} else if ci.Round.Paused {
		parts = append(parts, "the schedule is paused (no deadline)")
	}
	return strings.Join(parts, ": ") + "."
}

// closeRoundIfDone closes a round outside a fight when every seated character that chooses its turn has resolved, and opens the
// next (nil when the round stays open).
func closeRoundIfDone(lobby int, round *roundRecord) (*roundRecord, error) {
	seats, err := heldSeats(db, lobby)
	if err != nil || len(seats) == 0 {
		return nil, err
	}
	done, err := roundResults(db, round.ID)
	if err != nil {
		return nil, err
	}
	for _, s := range seats {
		if done[s.CharacterID] != "" {
			continue
		}
		need, err := turnNeeds(s.CharacterID)
		if err != nil {
			return nil, err
		}
		if need.Kind == needChoose {
			return nil, nil
		}
	}
	return roundOpens(lobby, roundNoncombat, 0, false, roundNoncombat)
}

// scheduleCampaignStarts: the campaign's first round outside a fight opens when the GM starts it (Matt 11.10), unless a round is
// open already.
func scheduleCampaignStarts(lobby int) {
	_, err := roundOpens(lobby, roundNoncombat, 0, false, openOnlyIfNone)
	logScheduleProblem(lobby, "the campaign started", err)
}

// gmOwnRequest: the request is the GM's own for the character (sent with the GM's token), the GM's ruling that overrides a limit such as
// the Long Rest's 24 story hours. A submission the GM resolves or a stand-in runs as the player's own request (replayAsPlayer) and is held
// to the same rules as the player's (staged test ON3.1: with the turn schedule on, resolving a rest used to skip the check every time).
func gmOwnRequest(r *http.Request, charID int) bool {
	agentID, err := getAgentFromAuth(r)
	return err == nil && isCharacterGM(agentID, charID)
}
