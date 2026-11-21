package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
)

// The Ready action (SRD 5.2.1): "you take the Ready action on your turn, which lets you act by taking a Reaction before the start
// of your next turn ... When the trigger occurs, you can either take your Reaction right after the trigger finishes or ignore the
// trigger." This file is the one owner of a readied action:
//   - what a Ready holds (readiedHold): the trigger, the action type and the held request's own fields (spell_slug, target_id,
//     target_ids, target_character_id ...), saved by the Ready itself (holdReadied, from resolveAction's "ready");
//   - firing it (fireReadied): the held request runs through POST /api/action itself, as the character's own action, so a readied
//     Fire Bolt makes its attack roll against the target's Armor Class and deals damage only on a hit, a readied save spell rolls
//     the save, exactly as a cast on the character's turn does; it takes the Reaction instead of the action. The player's
//     POST /api/trigger-readied and the GM's POST /api/gm/trigger-readied both call it (playtest I-CALLA-10, I-CALLA-12);
//   - its end: it lasts until the start of the character's next turn (endUntilNextTurn, dodge_2024.go) and ends with the fight
//     (clearCombatInitiative); my-turn shows it only while it holds (I-CALLA-11).

// readiedHold is what a Ready holds.
type readiedHold struct {
	Trigger     string                 `json:"trigger"`
	Action      string                 `json:"action"`
	Description string                 `json:"description"`
	Request     map[string]interface{} `json:"request,omitempty"` // the held action's POST /api/action body (no character_id)
}

// readyRequests: the Ready request's own fields while handleAction resolves it, for holdReadied (the same pattern as attackTargets).
var readyRequests sync.Map // charID -> actionFields

// readiedFiringKey marks a POST /api/action that is a readied action firing: it takes the Reaction. Only fireReadied sets it (a
// request context value: no request body can claim it).
type readiedFiringKey struct{}

func readiedFiring(r *http.Request) bool {
	v, _ := r.Context().Value(readiedFiringKey{}).(bool)
	return v
}

// holdReadied saves the Ready the character just took and answers the reply's words.
func holdReadied(charID int, description string) string {
	trigger, action, desc, _ := readiedAction(description)
	hold := readiedHold{Trigger: trigger, Action: action, Description: desc, Request: map[string]interface{}{"action": action, "description": desc}}
	if v, ok := readyRequests.Load(charID); ok {
		f := v.(actionFields)
		for k, val := range map[string]interface{}{"spell_slug": f.SpellSlug, "target": f.Target, "target_id": f.TargetID,
			"target_character_id": f.TargetCharacterID, "slot_level": f.SlotLevel, "item": f.Item, "charges": f.Charges,
			"skill": f.Skill, "object_id": f.ObjectID, "movement_cost": f.MovementCost} {
			switch x := val.(type) {
			case string:
				if x != "" {
					hold.Request[k] = x
				}
			case int:
				if x != 0 {
					hold.Request[k] = x
				}
			}
		}
		if len(f.TargetIDs) > 0 {
			hold.Request["target_ids"] = f.TargetIDs
		}
		if len(f.Features) > 0 {
			hold.Request["features"] = f.Features
		}
	}
	raw, _ := json.Marshal(hold)
	db.Exec("UPDATE characters SET readied_action = $1 WHERE id = $2", raw, charID)
	return fmt.Sprintf("Readied action: When '%s' → %s (%s). %s", trigger, action, desc, readiedHowItFires)
}

// readiedHowItFires: how a readied action happens, said by the Ready reply and my-turn.
const readiedHowItFires = "When the trigger happens it takes your Reaction and runs as that action (attack rolls and saves are rolled then): " +
	"the GM fires it (POST /api/gm/trigger-readied), or you can (POST /api/trigger-readied). It ends at the start of your next turn or when the fight ends."

// readiedOf: the character's readied action, if one holds.
func readiedOf(q rowQuerier, charID int) (readiedHold, bool, error) {
	var raw []byte
	err := q.QueryRow("SELECT readied_action FROM characters WHERE id = $1", charID).Scan(&raw)
	if err != nil {
		return readiedHold{}, false, err
	}
	var h readiedHold
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &h) != nil || h.Action == "" {
		return readiedHold{}, false, nil
	}
	if h.Request == nil { // saved before the held request was kept: the action and its words
		h.Request = map[string]interface{}{"action": h.Action, "description": h.Description}
	}
	return h, true, nil
}

// endReadied: the readied action ends (the start of the character's next turn, the end of the fight).
func endReadied(charID int) {
	db.Exec("UPDATE characters SET readied_action = NULL WHERE id = $1 AND readied_action IS NOT NULL", charID)
}

// fireReadied runs the character's readied action now, as its own POST /api/action with the Reaction (agentID: the character's
// player; by: who fired it, for the log). It answers the status and the reply of the action; a refused action (no Reaction left,
// the target gone, out of range ...) spends nothing and the Ready still holds.
func fireReadied(charID, lobbyID int, by string) (int, map[string]interface{}) {
	hold, ok, err := readiedOf(db, charID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "database_error", "message": "The readied action could not be read. Nothing was changed."}
	}
	name := getCharacterName(charID)
	if !ok {
		return http.StatusBadRequest, map[string]interface{}{"success": false, "error": "no_readied_action",
			"message": fmt.Sprintf("%s has no readied action (a Ready lasts until the start of the character's next turn). Take the Ready action first with a trigger.", name)}
	}
	if reactionUsedNow(charID) {
		return http.StatusConflict, map[string]interface{}{"success": false, "error": "reaction_used", "readied_action": hold,
			"message": fmt.Sprintf("%s has already used their Reaction this round: the readied action can't be taken. Nothing was changed.", name)}
	}
	var agentID int
	db.QueryRow("SELECT COALESCE(agent_id, 0) FROM characters WHERE id = $1", charID).Scan(&agentID)
	body := map[string]interface{}{}
	for k, v := range hold.Request {
		body[k] = v
	}
	body["character_id"], body["campaign_id"] = charID, lobbyID
	raw, _ := json.Marshal(body)
	rq := httptest.NewRequest(http.MethodPost, "/api/action?campaign_id="+strconv.Itoa(lobbyID), bytes.NewReader(raw))
	ctx := context.WithValue(rq.Context(), internalAgentKey{}, agentID)
	rq = rq.WithContext(context.WithValue(ctx, readiedFiringKey{}, true))
	rr := httptest.NewRecorder()
	handleAction(rr, rq)
	out := map[string]interface{}{}
	json.Unmarshal(rr.Body.Bytes(), &out)
	if rr.Code >= 300 || out["success"] == false {
		out["readied_action"] = hold
		if msg, _ := out["message"].(string); msg != "" && !strings.Contains(msg, "readied action still holds") {
			out["message"] = msg + " The readied action still holds."
		}
		return rr.Code, out
	}
	endReadied(charID)
	result := fmt.Sprint(out["result"])
	out["trigger"], out["reaction_consumed"] = hold.Trigger, true
	out["message"] = fmt.Sprintf("%s's readied action (%s): '%s' → %s", name, by, hold.Trigger, result)
	return http.StatusOK, out
}

// reactionUsedNow: the character has no Reaction to take now (spent this round, or taken away).
func reactionUsedNow(charID int) bool {
	var used bool
	db.QueryRow("SELECT COALESCE(reaction_used, false) FROM characters WHERE id = $1", charID).Scan(&used)
	return used || charCantTakeReactions(charID) // a condition or effect that takes Reactions away (Shocking Grasp ...)
}

// readiedView: the readied action as my-turn shows it.
func readiedView(h readiedHold) map[string]interface{} {
	return map[string]interface{}{"trigger": h.Trigger, "action": h.Action, "description": h.Description, "request": h.Request,
		"how_to_trigger": readiedHowItFires}
}

// readiedLogType: the log type of a readied action that fired ("readied_cast").
func readiedLogType(action string) string { return "readied_" + strings.TrimSpace(action) }
