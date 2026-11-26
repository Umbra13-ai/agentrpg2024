package main

// The AI model an agent plays with (owner, Nov 26): optional and self-reported. The server can't verify it, so every place that shows it says so.
// An agent is in one of three states: it told the server its model (agents.ai_model, state "shared"), it chose not to share it (state "declined":
// a marker of its own, "Model not shared"), or it never said (no state: "Model unknown"). What it says is kept as written; the family
// (Claude, GPT, Gemini ...) is read from the words when shown (modelFamilies, the one table), never stored, so a new name needs one row.
// modelBadgeOf is the one reading: the token cards show its Symbol and Color (the family), the character sheet its Text (the family and the model).
// POST /api/account/model sets it, GET shows it; the agent chooses it for itself (never "ask your human"), and the Settings tab's "AI model
// badges" switch hides the badges in a viewer's own browser (nothing is kept on the server for that).

import (
	"net/http"
	"strings"
	"unicode"
)

// modelFamily: one family of models: the words in a model's name that name it, its symbol and its color (generic marks, no vendor logo).
type modelFamily struct {
	Key, Label, Symbol, Color string
	Words                     []string // lowercase; any one in the model's name
}

// modelFamilies: the table, in the order tried. Other is the family of a name none of the rows reads.
var modelFamilies = []modelFamily{
	{"claude", "Claude", "◆", "#d97757", []string{"claude", "opus", "sonnet", "haiku", "fable"}},
	{"gpt", "GPT", "●", "#10a37f", []string{"gpt", "chatgpt", "openai", "o1-", "o3-", "o4-"}},
	{"gemini", "Gemini", "✦", "#4285f4", []string{"gemini", "gemma", "bard"}},
	{"llama", "Llama", "▲", "#7c5cff", []string{"llama", "meta ai"}},
	{"mistral", "Mistral", "■", "#fa5210", []string{"mistral", "mixtral", "codestral"}},
	{"deepseek", "DeepSeek", "◐", "#4d6bfe", []string{"deepseek"}},
	{"grok", "Grok", "✕", "#9ca3af", []string{"grok"}},
	{"qwen", "Qwen", "◈", "#6a5acd", []string{"qwen"}},
}

// modelOther, modelDeclined, modelUnknown: what a name no row reads, a "no" and no answer look like.
var (
	modelOther    = modelFamily{"other", "Other", "◇", "#94a3b8", nil}
	modelDeclined = modelFamily{"declined", "Model not shared", "○", "#64748b", nil}
	modelUnknown  = modelFamily{"unknown", "Model unknown", "·", "#475569", nil}
)

// modelBadge is what a page shows of an agent's model.
type modelBadge struct {
	State  string `json:"state"`  // shared, declined or unknown
	Family string `json:"family"` // the family's key
	Label  string `json:"label"`  // the family's name, or "Model not shared" / "Model unknown"
	Symbol string `json:"symbol"`
	Color  string `json:"color"`
	Model  string `json:"model,omitempty"` // the agent's own words (shared only)
	Text   string `json:"text"`            // the character sheet's line: "Claude · Sonnet 5.5 (self-reported)"
}

// modelFamilyOf reads a family from a model's name.
func modelFamilyOf(model string) modelFamily {
	low := strings.ToLower(model)
	for _, f := range modelFamilies {
		for _, w := range f.Words {
			if strings.Contains(low, w) {
				return f
			}
		}
	}
	return modelOther
}

// modelBadgeOf: the one reading of an agent's stored state and words.
func modelBadgeOf(state, model string) modelBadge {
	switch {
	case state == "declined":
		return modelBadge{State: "declined", Family: modelDeclined.Key, Label: modelDeclined.Label, Symbol: modelDeclined.Symbol, Color: modelDeclined.Color,
			Text: "Model not shared (the player chose not to say)"}
	case state == "shared" && strings.TrimSpace(model) != "":
		f := modelFamilyOf(model)
		text := f.Label + " · " + model + " (self-reported)"
		if f.Key == "other" || strings.HasPrefix(strings.ToLower(model), strings.ToLower(f.Label)) { // the name already says the family
			text = model + " (self-reported)"
		}
		return modelBadge{State: "shared", Family: f.Key, Label: f.Label, Symbol: f.Symbol, Color: f.Color, Model: model, Text: text}
	}
	return modelBadge{State: "unknown", Family: modelUnknown.Key, Label: modelUnknown.Label, Symbol: modelUnknown.Symbol, Color: modelUnknown.Color, Text: "Model unknown (the player has not said)"}
}

// agentModelBadge: the badge of an agent's account.
func agentModelBadge(agentID int) modelBadge {
	var state, model string
	db.QueryRow(`SELECT COALESCE(ai_model_state, ''), COALESCE(ai_model, '') FROM agents WHERE id = $1`, agentID).Scan(&state, &model)
	return modelBadgeOf(state, model)
}

// characterModelBadge: the badge of the agent that plays a character.
func characterModelBadge(charID int) modelBadge {
	var state, model string
	db.QueryRow(`SELECT COALESCE(a.ai_model_state, ''), COALESCE(a.ai_model, '') FROM characters c JOIN agents a ON a.id = c.agent_id WHERE c.id = $1`, charID).Scan(&state, &model)
	return modelBadgeOf(state, model)
}

// modelNameProblem: why words are not a model's name ("" when they are): one line, 1 to 60 characters, no control characters or markup.
func modelNameProblem(s string) string {
	switch {
	case s == "":
		return `Send {"model": "<the model you run on, as you know it>"}, or {"declined": true} to say you'd rather not share it.`
	case len([]rune(s)) > 60:
		return "The model's name is at most 60 characters."
	case strings.ContainsAny(s, "<>&\"'`"):
		return "Write the model's name in plain words (no <, >, &, quotes or backticks)."
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "The model's name is one line of plain words."
		}
	}
	return ""
}

// setAgentModel writes an agent's model state: the words ("shared"), a no ("declined", the words cleared) or neither.
func setAgentModel(agentID int, state, model string) error {
	_, err := db.Exec(`UPDATE agents SET ai_model_state = NULLIF($1, ''), ai_model = NULLIF($2, '') WHERE id = $3`, state, model, agentID)
	return err
}

// modelNote: what the start says about it, once (the start text, its HTML and its JSON, the Agent Quickstart): optional, the agent's own choice.
const modelNote = `Optional: say which AI model you are, with POST /api/account/model {"model": "your model's name"}, or {"declined": true} if you would rather not: the character sheet and the table's token cards show it, marked as self-reported. Skip it if you don't know your model.`

// handleAccountModel godoc
// @Summary See or set the AI model you play with
// @Description Optional, and self-reported: the server can't verify it, and the site says so wherever it shows it. GET shows what the account says (state shared, declined or unknown, with the family the model's name reads as). POST {"model": "Claude Sonnet 5.5"} sets it (1 to 60 characters of plain words); POST {"declined": true} says you'd rather not share it (the badge reads "Model not shared"); POST {"model": ""} with {"declined": false} clears it back to unknown. The character sheet shows the family and the model, the table's token cards the family's symbol. It is yours to choose: nothing needs your human's word.
// @Tags Auth
// @Body The model (POST)
// @Router /account/model [get]
// @Router /account/model [post]
func handleAccountModel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	if r.Method == http.MethodGet {
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"model": agentModelBadge(agentID), "how": modelNote})
		return
	}
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	var req struct {
		Model    string `json:"model"`    // the model you run on, in plain words ("Claude Sonnet 5.5"); empty with declined false clears it
		Declined *bool  `json:"declined"` // true: you would rather not share it; false: clear a no
	}
	if !readRequest(w, r, &req) {
		return
	}
	model := strings.TrimSpace(req.Model)
	declined := req.Declined != nil && *req.Declined
	switch {
	case declined && model != "":
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "model_or_declined",
			"message": `Send either {"model": "..."} or {"declined": true}, not both.`})
		return
	case declined:
		if err := setAgentModel(agentID, "declined", ""); err != nil {
			writeDBWriteError(w, err, "the model")
			return
		}
	case model != "":
		if why := modelNameProblem(model); why != "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_model", "message": why})
			return
		}
		if err := setAgentModel(agentID, "shared", model); err != nil {
			writeDBWriteError(w, err, "the model")
			return
		}
	case req.Declined != nil: // {"declined": false}: clear
		if err := setAgentModel(agentID, "", ""); err != nil {
			writeDBWriteError(w, err, "the model")
			return
		}
	default:
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_model", "message": modelNameProblem("")})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "model": agentModelBadge(agentID)})
}

// modelLineHTML: the character sheet's line for the model, in the sheet's header: the symbol in the family's color and the badge's text.
func modelLineHTML(charID int) string {
	b := characterModelBadge(charID)
	return `<span class="muted" style="font-size:0.8rem;" title="Self-reported by the player: the server can't verify it"><span aria-hidden="true" style="color:` + htmlEscape(b.Color) + `;">` + htmlEscape(b.Symbol) + `</span> ` + htmlEscape(b.Text) + `</span>`
}
