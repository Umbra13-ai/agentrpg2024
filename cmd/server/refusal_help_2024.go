package main

// What a refusal tells the caller (plan v2.1, S5; playtest S-ALDEN-CLARITY-03): a 4xx reply that carries one of the server's error codes
// also says what failed in a line, whether anything was changed (only where the server guarantees it) and the next safe step. It is
// built once, in the shared layer every request passes (withProtections), from one table: refusalHelp. The error code and every other
// field of the reply stay as the handler wrote them; a reply that already has next_step, or a code with no row, is left alone.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

// refusalRow: what one error code means. Nothing is true only when a refusal under this code is made before anything is written or
// spent (the body readers, the access owners, the action table's own refusals): then the reply says nothing was changed.
type refusalRow struct {
	Meaning, Next string
	Nothing       bool
}

var refusalHelp = map[string]refusalRow{
	// the body readers (request_body_2024.go): refused before anything is read into the game
	"unknown_field": {"The request has a field this request doesn't take.", "Send only the fields the request lists (its example is on /commands and in /docs).", true},
	"wrong_type":    {"A field has the wrong type.", "Fix the field named in `fields` and send it again.", true},
	"invalid_json":  {"The body is not one JSON object.", "Send a single JSON object as the body.", true},
	"body_required": {"This request needs a JSON body.", "Send the body its example shows (/commands).", true},
	"id_mismatch":   {"The id in the body is not the id in the path.", "Use the same id in both, or leave it out of the body.", true},
	// who may call, and whether the thing exists (the access owners: campaign_access_2024.go, gm_campaign_2024.go)
	"not_gm":                {"Only the campaign's GM can send this request.", "If you play in this campaign, use the player requests (GET /api/my-turn lists them); a GM names the campaign with campaign_id.", true},
	"not_your_character":    {"This request is for the character's owner.", "Ask the owner to send it, or read the character with GET /api/characters/{id}.", true},
	"not_in_campaign":       {"You are not a member of this campaign.", "Join it with POST /api/campaigns/{id}/join, or check the campaign id with GET /api/campaigns.", true},
	"campaign_not_found":    {"There is no such campaign.", "List the campaigns with GET /api/campaigns.", true},
	"character_not_found":   {"There is no such character, or it is not in this campaign.", "Read your characters with GET /api/characters.", true},
	"campaign_not_active":   {"The campaign has not started, or is over.", "GET /api/campaigns/{id} shows its status and what it is waiting on.", true},
	"campaign_id_required":  {"You run more than one campaign, so this request needs to know which.", "Add campaign_id to the body or the query.", true},
	"no_active_game":        {"You have no character in an active campaign.", "GET /api/campaigns?open=true lists campaigns with a seat; GET /api/heartbeat shows where you are.", true},
	"campaign_not_started":  {"Your campaign has not started yet.", "Wait for the GM to start it; GET /api/my-turn tells you what you are waiting on.", true},
	"table_held":            {"The GM is holding the table: no game action is taken yet.", "Talk in the party chat and finish your character; the GM opens play.", true},
	"round_not_open":        {"There is no open round for this.", "GET /api/campaigns/{id}/round shows the round and its check-ins.", true},
	"turn_already_resolved": {"This turn was already resolved.", "GET /api/my-turn shows what happened and what comes next.", true},
	// the action table's own refusals (action_types_2024.go): they spend nothing
	"unknown_action":                 {"That is not an action type.", "Send one of the actions in `actions` (GET /api/my-turn lists what your character can send now).", true},
	"action_refused":                 {"The action was refused, and the reason is in `message`.", "Read the message: it names what is missing. Nothing was spent; GET /api/my-turn lists what you can send now.", true},
	"resource_exhausted":             {"A spell slot, a use or a charge it needs is spent.", "Choose another action, or rest (GET /api/my-turn's elsewhere says how).", false},
	"declaration_has_its_own_action": {"A declaration that casts a spell or uses a feature must use that feature's own action.", "Send it as the cast or feature action (GET /api/my-turn: now and more).", true},
	"long_rest_too_soon":             {"A Long Rest needs 24 hours of story time since the last.", "The GM passes time (story_hours_left says how much); your Long Rest is allowed after that.", true},
	"stale_cast_token":               {"The cast this decision belongs to is no longer the current one.", "Read the current state first (GET /api/gm/status), then decide again.", false},
	"monster_action_refused":         {"The monster action was refused, and the reason is in `message`.", "Fix what the message names (often `use` or the target) and send it again: a refused action posts nothing.", false},
	"combat_operation_in_progress":   {"Another combat request is still being worked out.", "Wait a moment and read GET /api/gm/status before sending it again.", false},
	"players_not_ready":              {"Not every player has said they are ready.", "GET /api/campaigns/{id} shows who is waiting; start_without names players you start without.", true},
	"setup_not_finished":             {"The character still has choices to make.", "GET /api/characters/{id}/setup lists each with the request to send.", true},
	"request_too_large":              {"The request body is too large.", "Send a smaller body.", true},
}

// refusalHelpFor: the fields to add to a refusal's body, or nil.
func refusalHelpFor(body map[string]interface{}) map[string]interface{} {
	code, _ := body["error"].(string)
	row, ok := refusalHelp[code]
	if !ok || body["next_step"] != nil {
		return nil
	}
	out := map[string]interface{}{"what_failed": row.Meaning, "next_step": row.Next}
	if row.Nothing {
		out["nothing_changed"] = true
	}
	return out
}

// refusalHelpWriter wraps a response: a JSON 4xx reply is held until the handler is done, then the help is added to it. Every other
// reply passes straight through.
type refusalHelpWriter struct {
	http.ResponseWriter
	hold   bool
	status int
	buf    bytes.Buffer
}

func (h *refusalHelpWriter) WriteHeader(code int) {
	if code >= 400 && code < 500 && code != http.StatusUnauthorized && strings.HasPrefix(h.Header().Get("Content-Type"), "application/json") {
		h.hold, h.status = true, code
		return
	}
	h.ResponseWriter.WriteHeader(code)
}

func (h *refusalHelpWriter) Write(b []byte) (int, error) {
	if h.hold {
		return h.buf.Write(b)
	}
	return h.ResponseWriter.Write(b)
}

func (h *refusalHelpWriter) Unwrap() http.ResponseWriter { return h.ResponseWriter }

// finish writes a held refusal, with its help when it has a row.
func (h *refusalHelpWriter) finish() {
	if !h.hold {
		return
	}
	out := h.buf.Bytes()
	var body map[string]interface{}
	if json.Unmarshal(out, &body) == nil && body != nil {
		if add := refusalHelpFor(body); add != nil {
			for k, v := range add {
				body[k] = v
			}
			if b, err := json.Marshal(body); err == nil {
				out = append(b, '\n')
			}
		}
	}
	h.hold = false
	h.ResponseWriter.WriteHeader(h.status)
	h.ResponseWriter.Write(out)
}
