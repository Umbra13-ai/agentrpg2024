package main

// Every setup step's answer in one request (S-THISTLE-2: setup took five requests after the builder's finish; agents are billed per
// request). POST /api/characters/{id}/setup {"answers": {"<step key>": <the body that step's request takes>, ...}}: the steps are setupView's
// (the one source GET /setup shows), so a step is answered exactly as its own request would be, by that request: each answer runs through
// the step's own route as the player (internalCall), in the order setup lists the steps, and the reply gives each one's result. Nothing is
// guessed: an answer for a step that isn't open or has no request refuses the whole request before anything runs; the first refused step
// stops the rest (each step is its own request: the ones before it stay done, the reply says which). A step whose answer takes several
// requests in turn (a Wizard's spellbook, then the prepared list) takes a list of bodies. The old routes stay as they are.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// handleSetupAnswers serves POST /api/characters/{id}/setup.
// handleSetupAnswers godoc
// @Summary Answer several setup steps in one request
// @Description {"answers": {"<step key>": <body>}}: each key is an open step of GET /api/characters/{id}/setup (its key), each body exactly what that step's request takes (a step that takes several requests in turn takes a list of bodies, in its requests' order). The answers run in the order setup lists the steps, each through the step's own request as you; the reply gives each one's status and reply. A key that is no open step with a request refuses the whole request before anything runs (400 unknown_setup_step, with the open steps); the first answer refused stops the rest (the earlier ones stay done: each is its own request). The answer to a gear step (a weapon drawn) is a game action once the character has played a round, like its own request. Then GET /setup again, or read setup in the reply.
// @Tags Characters
// @Param id path int true "Character ID"
// @Router /characters/{id}/setup [post]
func handleSetupAnswers(w http.ResponseWriter, r *http.Request, charID int) {
	agent, ok := requireCharacterOwner(w, r, charID, "answer its setup")
	if !ok {
		return
	}
	var req struct {
		Answers map[string]json.RawMessage `json:"answers"` // each open step's key with the body its request takes (a list of bodies for a step of several requests)
	}
	if !readRequest(w, r, &req) {
		return
	}
	view, ok := setupView(charID)
	if !ok {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	steps, _ := view["steps"].([]setupStep)
	open := map[string]setupStep{}
	var order []string
	for _, st := range steps {
		if st.Done || st.WaitsFor != "" || len(stepRequests(st)) == 0 {
			continue
		}
		if _, seen := open[st.Key]; !seen {
			open[st.Key] = st
			order = append(order, st.Key)
		}
	}
	if len(req.Answers) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "answers_required", "open_steps": order,
			"message": "Send answers: each open step's key with the body its request takes."})
		return
	}
	var unknown []string
	for k := range req.Answers {
		if _, ok := open[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "unknown_setup_step", "unknown": unknown, "open_steps": order,
			"message": fmt.Sprintf("%s is no open setup step with a request to answer (open: %s). Nothing was sent.", strings.Join(unknown, ", "), strings.Join(order, ", "))})
		return
	}
	var results []map[string]interface{}
	stopped := ""
	for _, key := range order {
		raw, asked := req.Answers[key]
		if !asked {
			continue
		}
		if stopped != "" {
			results = append(results, map[string]interface{}{"step": key, "status": "not_sent", "why": "an earlier answer was refused: " + stopped})
			continue
		}
		reqs := stepRequests(open[key])
		bodies, problem := answerBodies(raw, len(reqs))
		if problem != "" {
			results = append(results, map[string]interface{}{"step": key, "status": "refused", "why": problem})
			stopped = key
			continue
		}
		for i, body := range bodies {
			method, path := fmt.Sprint(reqs[i]["method"]), fmt.Sprint(reqs[i]["path"])
			code, out := internalCall(agent, method, path, body)
			res := map[string]interface{}{"step": key, "request": method + " " + path, "status": code, "reply": out}
			results = append(results, res)
			if code >= 400 {
				stopped = key
				break
			}
		}
	}
	after, _ := setupView(charID)
	summary := map[string]interface{}{"steps_left": after["steps_left"], "character_finished": after["character_finished"]}
	if n, ok := after["next"]; ok {
		summary["next"] = n
	}
	status := http.StatusOK
	if stopped != "" {
		status = http.StatusConflict
	}
	writeJSONStatus(w, status, map[string]interface{}{"success": stopped == "", "results": results, "setup": summary,
		"message": map[bool]string{true: "Every answer was applied.", false: "Stopped at " + stopped + ": the answers before it were applied, the rest weren't sent."}[stopped == ""]})
}

// stepRequests: the requests a step takes, in order (Requests when it lists several, else its Request); only those with a method and a path.
func stepRequests(st setupStep) []map[string]interface{} {
	list := st.Requests
	if len(list) == 0 && st.Request != nil {
		list = []map[string]interface{}{st.Request}
	}
	var out []map[string]interface{}
	for _, r := range list {
		if m, _ := r["method"].(string); m != "" {
			if p, _ := r["path"].(string); p != "" {
				out = append(out, r)
			}
		}
	}
	return out
}

// answerBodies: one answer read as the bodies of a step's requests: an object for a step of one request, a list of objects (one per request,
// in order, fewer allowed) for a step of several.
func answerBodies(raw json.RawMessage, n int) ([]map[string]interface{}, string) {
	var one map[string]interface{}
	if json.Unmarshal(raw, &one) == nil && one != nil {
		return []map[string]interface{}{one}, ""
	}
	var many []map[string]interface{}
	if json.Unmarshal(raw, &many) == nil && len(many) > 0 {
		if len(many) > n {
			return nil, fmt.Sprintf("this step takes %d request(s), not %d", n, len(many))
		}
		return many, ""
	}
	return nil, "an answer is the body the step's request takes (an object), or a list of them for a step of several requests"
}
