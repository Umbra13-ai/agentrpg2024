package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

type combatFrame struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}
type combatFrameScope struct {
	Lobby  int
	Job    string
	Frames []combatFrame
}

var combatFrames = struct {
	sync.Mutex
	byGoroutine map[uint64]combatFrameScope
}{byGoroutine: map[uint64]combatFrameScope{}}
var combatResumeHandlers = map[string]func(int, json.RawMessage) (map[string]interface{}, error){}

func armCombatFrame(lobby int, kind string, data interface{}) (func(), error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	gid := goroutineID()
	combatFrames.Lock()
	before := combatFrames.byGoroutine[gid]
	if before.Lobby != 0 && before.Lobby != lobby {
		combatFrames.Unlock()
		return nil, fmt.Errorf("continuation crosses campaigns")
	}
	now := before
	now.Lobby = lobby
	now.Frames = append(append([]combatFrame(nil), before.Frames...), combatFrame{kind, raw})
	combatFrames.byGoroutine[gid] = now
	combatFrames.Unlock()
	return func() {
		combatFrames.Lock()
		if before.Lobby == 0 {
			delete(combatFrames.byGoroutine, gid)
		} else {
			combatFrames.byGoroutine[gid] = before
		}
		combatFrames.Unlock()
		if before.Lobby == 0 {
			finishDirectConcentrationJournal(lobby)
		}
	}, nil
}
func currentCombatFrames(lobby int) combatFrameScope {
	combatFrames.Lock()
	s := combatFrames.byGoroutine[goroutineID()]
	combatFrames.Unlock()
	if s.Lobby != lobby {
		s = combatFrameScope{}
	}
	if s.Job == "" {
		s.Job = currentConcentrationJournal(lobby).ID
	}
	if s.Job != "" {
		s.Lobby = lobby
	}
	return s
}

// Called by the concentration transaction: a pending decision and the exact
// continuation become durable together. No executable code comes from a client.
func captureCombatWorkIn(tx *sql.Tx, lobby int) (string, error) {
	s := currentCombatFrames(lobby)
	if len(s.Frames) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(s.Frames)
	if err != nil {
		return "", err
	}
	id := s.Job
	if id == "" {
		id, err = newConcentrationToken()
		if err != nil {
			return "", err
		}
	}
	_, err = tx.Exec(`INSERT INTO combat_paused_work(id,lobby_id,status,frames) VALUES($1,$2,'capturing',$3::jsonb)
 ON CONFLICT(id) DO UPDATE SET status='capturing',frames=EXCLUDED.frames WHERE combat_paused_work.lobby_id=EXCLUDED.lobby_id`, id, lobby, string(raw))
	if err == nil {
		if currentConcentrationJournal(lobby).ID == "" {
			keepConcentrationJournal(lobby, concentrationJournal{ID: id, Owned: s.Job == ""})
		}
		noteCapturedCombatWork(lobby, id)
	}
	return id, err
}

// A save becomes durable inside the current step. That step may still have a
// cost/log to finish. Only its normal unwind marks the captured continuation
// safe to resume. A crash or panic before then leaves capturing and requires GM
// reconciliation, rather than silently skipping those unfinished side effects.
func finishCapturedCombatWork(lobby int, id string) {
	if id == "" || currentConcentrationJournal(lobby).Fault || requestHasDBFailure() {
		return
	}
	_, err := db.Exec(`UPDATE combat_paused_work w SET status=CASE WHEN w.status='running' THEN 'done' WHEN EXISTS(SELECT 1 FROM monster_concentration c WHERE c.lobby_id=w.lobby_id AND c.work_id=w.id AND c.pending<>'') THEN 'waiting' ELSE 'ready' END,result=CASE WHEN w.status='running' THEN '{"success":true}'::jsonb ELSE w.result END WHERE w.lobby_id=$1 AND w.id=$2 AND w.status IN ('capturing','running')`, lobby, id)
	if err != nil {
		concentrationFailure(lobby, "Saved combat work", err)
	}
}

func readyCombatWorkIn(tx *sql.Tx, lobby int, id string) error {
	if id == "" {
		return nil
	}
	_, err := tx.Exec(`UPDATE combat_paused_work SET status='ready' WHERE id=$1 AND lobby_id=$2 AND status='waiting'`, id, lobby)
	return err
}
func cancelCombatWork(lobby int) {
	if db != nil {
		if _, err := db.Exec(`UPDATE combat_paused_work SET status='cancelled' WHERE lobby_id=$1 AND status IN ('capturing','waiting','ready','running')`, lobby); err != nil {
			concentrationFailure(lobby, "Saved combat work", err)
		}
	}
}
func hasPausedCombatWork(lobby int) (bool, error) {
	var yes bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM combat_paused_work WHERE lobby_id=$1 AND status IN ('capturing','waiting','ready','running'))`, lobby).Scan(&yes)
	return yes, err
}
func combatResolutionPaused(lobby int) bool {
	pending, err := pendingCombatDecision(lobby)
	return err != nil || pending
}

func resumeCombatWork(lobby int, id string) (map[string]interface{}, string, error) {
	defer forgetConcentrationJournal(lobby)
	var status string
	var raw, result []byte
	err := db.QueryRow(`SELECT status,frames,result FROM combat_paused_work WHERE id=$1 AND lobby_id=$2`, id, lobby).Scan(&status, &raw, &result)
	if err == sql.ErrNoRows {
		return nil, "work_not_found", nil
	}
	if err != nil {
		return nil, "", err
	}
	if status == "done" {
		var saved map[string]interface{}
		if err = json.Unmarshal(result, &saved); err != nil {
			return nil, "", err
		}
		return saved, "", nil
	}
	if status != "ready" {
		return nil, "work_not_ready", nil
	}
	pending, err := pendingConcentrationDecision(lobby)
	if err != nil {
		return nil, "", err
	}
	if pending {
		return nil, "combat_adjudication_pending", nil
	}
	var frames []combatFrame
	if err = json.Unmarshal(raw, &frames); err != nil {
		return nil, "", err
	}
	for _, f := range frames {
		if combatResumeHandlers[f.Kind] == nil {
			return nil, "unsupported_work", nil
		}
	}
	review, err := newConcentrationToken()
	if err != nil {
		return nil, "", err
	}
	res, err := db.Exec(`UPDATE combat_paused_work SET status='running',review_token=$3 WHERE id=$1 AND lobby_id=$2 AND status='ready'`, id, lobby, review)
	if err != nil {
		return nil, "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, "", err
	}
	if n != 1 {
		return nil, "work_not_ready", nil
	}
	keepConcentrationJournal(lobby, concentrationJournal{ID: id})
	out := map[string]interface{}{"success": true, "resumed": true}
	gid := goroutineID()
	for len(frames) > 0 {
		f := frames[len(frames)-1]
		frames = frames[:len(frames)-1]
		combatFrames.Lock()
		before := combatFrames.byGoroutine[gid]
		combatFrames.byGoroutine[gid] = combatFrameScope{Lobby: lobby, Job: id, Frames: frames}
		combatFrames.Unlock()
		value, runErr := combatResumeHandlers[f.Kind](lobby, f.Data)
		combatFrames.Lock()
		if before.Lobby == 0 {
			delete(combatFrames.byGoroutine, gid)
		} else {
			combatFrames.byGoroutine[gid] = before
		}
		combatFrames.Unlock()
		if runErr != nil {
			return nil, "", runErr
		}
		if value != nil {
			out["continued"] = value
		}
		if combatResolutionPaused(lobby) {
			if watchOf() == nil {
				finishCapturedCombatWork(lobby, id)
			}
			return map[string]interface{}{"success": true, "combat_paused": true}, "", nil
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, "", err
	}
	_, err = db.Exec(`UPDATE combat_paused_work SET status='done',result=$3::jsonb WHERE id=$1 AND lobby_id=$2 AND status='running'`, id, lobby, string(b))
	return out, "", err
}

// Reconciliation acknowledges the uncertain step after explicit GM correction.
// The saved frames describe only the known remainder. A fresh token is required
// for each interrupted attempt so an old acknowledgment cannot release a new one.
func combatReviewToken(id, stored string) string {
	if stored == "" {
		return "legacy:" + id
	}
	return stored
}
func reconcileCombatWork(lobby int, id, note, review string, continueWork bool) (string, error) {
	if strings.TrimSpace(note) == "" {
		return "reconciliation_note_required", nil
	}
	if review == "" {
		return "review_token_required", nil
	}
	pending, err := pendingConcentrationDecision(lobby)
	if err != nil {
		return "", err
	}
	if pending {
		return "combat_adjudication_pending", nil
	}
	var stored string
	err = db.QueryRow(`SELECT review_token FROM combat_paused_work WHERE id=$1 AND lobby_id=$2 AND status IN ('capturing','running')`, id, lobby).Scan(&stored)
	if err == sql.ErrNoRows {
		return "work_not_interrupted", nil
	}
	if err != nil {
		return "", err
	}
	if review != combatReviewToken(id, stored) {
		return "stale_review_token", nil
	}
	next, err := newConcentrationToken()
	if err != nil {
		return "", err
	}
	status := "reconciled"
	if continueWork {
		status = "ready"
	}
	res, err := db.Exec(`UPDATE combat_paused_work SET status=$3,reconciliation_note=$4,review_token=$5 WHERE id=$1 AND lobby_id=$2 AND status IN ('capturing','running') AND review_token=$6`, id, lobby, status, strings.TrimSpace(note), next, stored)
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "stale_review_token", nil
	}
	addGMNotice(lobby, "The GM reconciled the uncertain step in combat work "+id+": "+strings.TrimSpace(note)+". The uncertain step was not replayed.")
	return "", nil
}

// handleGMCombatResume godoc
// @Summary Resume combat work after adjudication
// @Description GET privately lists saved work, persisted event facts and review_token. POST resumes ready work; completed retries return the recorded result. Interrupted running/capturing work requires GM review/correction and reconciliation_note plus the current review_token: action=reconcile closes work after manual completion, or reconcile_and_resume continues only its known remainder. The uncertain step is never replayed.
// @Tags GM
// @Body resume_id
// @Router /gm/combat-resume [get]
// @Router /gm/combat-resume [post]
func handleGMCombatResume(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResumeID           string `json:"resume_id"`           // saved work id from the private notice or this endpoint's GET
		Action             string `json:"action"`              // resume (default), reconcile, or reconcile_and_resume after GM correction
		ReconciliationNote string `json:"reconciliation_note"` // required for reconciliation: describe the reviewed effects and corrections
		ReviewToken        string `json:"review_token"`        // current token from GET; required for reconciliation of this interrupted attempt
	}
	_, lobby, ok := gmRead(w, r, &req, optionalBody())
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		rows, err := db.Query(`SELECT id,status,context,review_token FROM combat_paused_work WHERE lobby_id=$1 AND status IN ('capturing','waiting','ready','running') ORDER BY created_at,id`, lobby)
		if err != nil {
			writeDBError(w, err, "saved combat work")
			return
		}
		defer rows.Close()
		list := []map[string]interface{}{}
		for rows.Next() {
			var id, status, review string
			var context json.RawMessage
			if err = rows.Scan(&id, &status, &context, &review); err != nil {
				writeDBError(w, err, "saved combat work")
				return
			}
			list = append(list, map[string]interface{}{"resume_id": id, "status": status, "context": context, "review_token": combatReviewToken(id, review)})
		}
		if err = rows.Err(); err != nil {
			writeDBError(w, err, "saved combat work")
			return
		}
		writeJSONStatus(w, 200, map[string]interface{}{"work": list})
		return
	}
	if r.Method != http.MethodPost {
		writeJSONStatus(w, 405, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	if req.ResumeID == "" {
		writeJSONStatus(w, 400, map[string]interface{}{"error": "resume_id_required", "message": "Send the saved work id from the private notice or GET on this endpoint."})
		return
	}
	if req.Action != "" && req.Action != "resume" && req.Action != "reconcile" && req.Action != "reconcile_and_resume" {
		writeJSONStatus(w, 400, map[string]interface{}{"error": "invalid_action", "message": "action must be resume, reconcile or reconcile_and_resume."})
		return
	}
	var result map[string]interface{}
	var code string
	var err error
	if req.Action == "reconcile" || req.Action == "reconcile_and_resume" {
		continueWork := req.Action == "reconcile_and_resume"
		code, err = reconcileCombatWork(lobby, req.ResumeID, req.ReconciliationNote, req.ReviewToken, continueWork)
		result = map[string]interface{}{"success": true, "reconciled": true, "replayed": false}
		if err == nil && code == "" && continueWork {
			result, code, err = resumeCombatWork(lobby, req.ResumeID)
		}
	} else {
		result, code, err = resumeCombatWork(lobby, req.ResumeID)
	}
	if err != nil {
		writeDBWriteError(w, err, "combat continuation")
		return
	}
	if code != "" {
		writeJSONStatus(w, 409, map[string]interface{}{"error": code, "message": "This work cannot resume now. Resolve pending decisions first; interrupted work requires GM reconciliation rather than blind replay."})
		return
	}
	writeJSONStatus(w, 200, result)
}
