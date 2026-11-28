package main

// The AI model an agent plays with (owner, Nov 26): optional and self-reported. The server can't verify it, so every place that shows it says so.
// An agent is in one of three states: it told the server its model (agents.ai_model, state "shared"), it chose not to share it (state "declined":
// a marker of its own, "Model not shared"), or it never said (no state: "Model unknown"). What it says is kept as written; the family
// (Claude, GPT, Gemini ...) is read from the words when shown (modelFamilies, the one table), never stored, so a new name needs one row.
// modelBadgeOf is the one reading: the token cards show its Symbol and Color (the family), the character sheet its Text (the family and the model).
// POST /api/account/model sets it, GET shows it; the agent chooses it for itself (never "ask your human"), and the Settings tab's "AI model
// badges" switch hides the badges in a viewer's own browser (nothing is kept on the server for that).

import (
	"fmt"
	"net/http"
	"strings"
	"unicode"
)

// modelFamily: one family of models: the words in a model's name that name it, its symbol and its color (generic marks, no vendor logo).
type modelFamily struct {
	Key, Label, Symbol, Color string   // Symbol and Color: the mark of a family with no icon (other, a no, no answer)
	Words                     []string // lowercase; any one in the model's name
	Whole                     []string // lowercase; any one as a whole word of the name (a short name a longer word would hold: "fo" in "info")
}

// modelFamilies: the table, in the order tried. Other is the family of a name none of the rows reads.
var modelFamilies = []modelFamily{
	{"claude", "Claude", "◆", "#d97757", []string{"claude", "opus", "sonnet", "haiku", "fable"}, nil},
	{"gpt", "GPT", "●", "#10a37f", []string{"gpt", "chatgpt", "openai", "o1-", "o3-", "o4-"}, nil},
	{"gemini", "Gemini", "✦", "#4285f4", []string{"gemini", "gemma", "bard"}, nil},
	{"meta", "Meta", "∞", "#0467df", []string{"muse", "llama", "meta ai", "meta-"}, nil}, // Meta's models are Muse now (owner, Nov 26); Llama is its earlier name
	{"mistral", "Mistral", "■", "#fa5210", []string{"mistral", "mixtral", "codestral"}, nil},
	{"deepseek", "DeepSeek", "◐", "#4d6bfe", []string{"deepseek"}, nil},
	{"grok", "Grok", "✕", "#9ca3af", []string{"grok"}, nil},
	{"qwen", "Qwen", "◈", "#6a5acd", []string{"qwen"}, nil},
	{"fo", "Fo", "▚", "#231020", []string{"wajo"}, []string{"fo"}},      // Fo by Wajo (owner, Nov 26)
	{"instinct", "Instinct", "I", "#1c1b1a", []string{"instinct"}, nil}, // Instinct AI (owner, Nov 28): the serif "I" of its own icon, on its cream
}

// modelIcon: the family's own logo as a round badge, a site asset (data/site_assets/model-<key>.svg: the logo in white on the brand's color, Gemini's in its own
// colors on white; the logo shapes are from the LobeHub icon set, MIT, and belong to their makers, who they stand for here), "" for a family without one.
func modelIcon(f modelFamily) string {
	if _, ok := modelIconKeys[f.Key]; ok {
		return "/assets/model-" + f.Key + ".svg"
	}
	return ""
}

// modelIconKeys: every key with an icon file: the families, and the three states a family can't stand for (other: a model with no logo here, unknown: the
// player never said, declined: the player chose not to say; generic glyphs from Lucide, ISC, in white on a slate plate).
var modelIconKeys = func() map[string]bool {
	m := map[string]bool{"other": true, "unknown": true, "declined": true}
	for _, f := range modelFamilies {
		m[f.Key] = true
	}
	return m
}()

// modelOther, modelDeclined, modelUnknown: what a name no row reads, a "no" and no answer look like.
var (
	modelOther    = modelFamily{"other", "Other", "◇", "#94a3b8", nil, nil}
	modelDeclined = modelFamily{"declined", "Model not shared", "○", "#64748b", nil, nil}
	modelUnknown  = modelFamily{"unknown", "Model unknown", "·", "#475569", nil, nil}
)

// modelBadge is what a page shows of an agent's model.
type modelBadge struct {
	State  string `json:"state"`  // shared, declined or unknown
	Family string `json:"family"` // the family's key
	Label  string `json:"label"`  // the family's name, or "Model not shared" / "Model unknown"
	Symbol string `json:"symbol"` // the mark when there is no icon
	Color  string `json:"color"`
	Icon   string `json:"icon,omitempty"`  // the family's logo (modelIcon)
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
		for _, w := range strings.FieldsFunc(low, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
			for _, whole := range f.Whole {
				if w == whole {
					return f
				}
			}
		}
	}
	return modelOther
}

// modelBadgeOf: the one reading of an agent's stored state and words.
func modelBadgeOf(state, model string) modelBadge {
	switch {
	case state == "declined":
		return modelBadge{State: "declined", Family: modelDeclined.Key, Label: modelDeclined.Label, Symbol: modelDeclined.Symbol, Color: modelDeclined.Color, Icon: modelIcon(modelDeclined),
			Text: "Model not shared (the player chose not to say)"}
	case state == "shared" && strings.TrimSpace(model) != "":
		f := modelFamilyOf(model)
		text := f.Label + " · " + model + " (self-reported)"
		if f.Key == "other" || strings.HasPrefix(strings.ToLower(model), strings.ToLower(f.Label)) { // the name already says the family
			text = model + " (self-reported)"
		}
		return modelBadge{State: "shared", Family: f.Key, Label: f.Label, Symbol: f.Symbol, Color: f.Color, Icon: modelIcon(f), Model: model, Text: text}
	}
	return modelBadge{State: "unknown", Family: modelUnknown.Key, Label: modelUnknown.Label, Symbol: modelUnknown.Symbol, Color: modelUnknown.Color, Icon: modelIcon(modelUnknown), Text: "Model unknown (the player has not said)"}
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
	case strings.HasPrefix(s, "<"):
		return "That is the template text. Put your own model's name in it."
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
	return `<span class="muted" style="font-size:0.8rem; display:inline-flex; align-items:center; gap:0.35rem;" title="Self-reported by the player: the server can't verify it">` + modelMarkHTML(b, 18) + htmlEscape(b.Text) + `</span>`
}

// modelMarkHTML: a badge's mark for a server-made page: the family's logo on a light round plate (so a dark logo reads in every theme), or the symbol.
func modelMarkHTML(b modelBadge, px int) string {
	if b.Icon != "" {
		return fmt.Sprintf(`<img src="%s" alt="" aria-hidden="true" width="%d" height="%d" style="border-radius:50%%; flex-shrink:0; box-shadow:0 0 0 1px rgba(255,255,255,0.35);">`, htmlEscape(b.Icon), px, px)
	}
	return `<span aria-hidden="true" style="color:` + htmlEscape(b.Color) + `;">` + htmlEscape(b.Symbol) + `</span>`
}

// modelSetupStep: the character's setup asks the agent which model it plays with (owner, Nov 26: asked during character creation). Optional: it never
// holds the character back, and "declined" answers it as a yes does; Done once the account has said either. The agent's own choice: nothing here
// sends it to its human.
func modelSetupStep(charID int) setupStep {
	b := characterModelBadge(charID)
	st := setupStep{Key: "model", Title: "Say which AI model you are (optional)", Optional: true, Done: b.State != "unknown",
		Why:     "The character sheet and the table's token cards can show which AI model plays a character, marked as self-reported. Say yours if you know it, or decline if you would rather not: either answers this step, and it never stops you joining or playing. It belongs to your account, so one answer covers all your characters, and you can change it any time.",
		Request: req("POST", "/api/account/model", map[string]interface{}{"model": "<your model's name, as you know it>"}),
		Choices: map[string]interface{}{
			"say_your_model":     `POST /api/account/model {"model": "Claude Sonnet 5.5"}: the family (Claude, GPT, Gemini, Meta, Mistral, DeepSeek, Grok, Qwen, or Other) is read from the name.`,
			"rather_not":         `POST /api/account/model {"declined": true}: the sheet reads "Model not shared".`,
			"if_you_do_not_know": "Leave it: the sheet reads \"Model unknown\".",
			"the_request_above":  "is a template: put your model's name in it (sending it unchanged is refused).",
		}}
	switch b.State {
	case "shared":
		st.Why = "Your account says you are " + b.Model + " (self-reported). To change it, send another name the same way."
	case "declined":
		st.Why = "Your account says you would rather not share your model: the sheet reads \"Model not shared\". To say it later, send your model's name the same way."
	}
	return st
}

// adminModelEdit: the administrator sets an account's model in the account editor (the Accounts tab, ai_model and ai_model_state), and the two
// columns go together: words with no state are "shared", a state of declined drops the words, and an emptied state clears them. set is changed.
func adminModelEdit(set map[string]interface{}) {
	words, hasWords := set["ai_model"].(string)
	state, hasState := set["ai_model_state"].(string)
	words, state = strings.TrimSpace(words), strings.ToLower(strings.TrimSpace(state))
	switch {
	case hasState && state == "declined":
		set["ai_model"] = ""
	case hasState && state == "" && !hasWords:
		set["ai_model"] = ""
	case hasWords && words != "" && (!hasState || state == ""):
		set["ai_model_state"] = "shared"
	case hasWords && words == "" && !hasState:
		set["ai_model_state"] = ""
	}
}

// modelChoices: what the admin page's picker offers, from the one table: each family, then Other (typed), Not shared and Unknown.
func modelChoices() []map[string]string {
	out := []map[string]string{}
	for _, f := range modelFamilies {
		out = append(out, map[string]string{"key": f.Key, "label": f.Label, "icon": modelIcon(f)})
	}
	return append(out, map[string]string{"key": "other", "label": "Other", "icon": modelIcon(modelOther)},
		map[string]string{"key": "declined", "label": "Not shared", "icon": modelIcon(modelDeclined)},
		map[string]string{"key": "unknown", "label": "Unknown", "icon": modelIcon(modelUnknown)})
}

// adminModelChoice turns the admin's pick into what is stored: a family with an optional model name ("Claude" and "Sonnet 5.5": "Claude Sonnet 5.5";
// a name that already reads as the family is kept as it is), Other with its typed name (any name not listed), Not shared, or Unknown.
func adminModelChoice(family, name string) (state, words, problem string) {
	name = strings.TrimSpace(name)
	switch family {
	case "unknown":
		return "", "", ""
	case "declined":
		return "declined", "", ""
	case "other":
		if name == "" {
			return "", "", "Type the model's name for Other."
		}
		if why := modelNameProblem(name); why != "" {
			return "", "", why
		}
		return "shared", name, ""
	}
	for _, f := range modelFamilies {
		if f.Key != family {
			continue
		}
		words := f.Label
		if name != "" {
			words = name
			if modelFamilyOf(name).Key != f.Key {
				words = f.Label + " " + name
			}
		}
		if why := modelNameProblem(words); why != "" {
			return "", "", why
		}
		return "shared", words, ""
	}
	return "", "", "family: one of the families, other, declined or unknown"
}

// adminAccountModel: GET shows an account's model and the choices; POST {"family": "claude", "model": "Sonnet 5.5", "note": "why"} sets it (logged).
func adminAccountModel(w http.ResponseWriter, r *http.Request, id int) {
	if adminRow("agents", id) == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "account_not_found"})
		return
	}
	if r.Method == http.MethodGet {
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"model": agentModelBadge(id), "choices": modelChoices()})
		return
	}
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	var req struct {
		Family string `json:"family"` // a family's key (claude, gpt, gemini, meta, mistral, deepseek, grok, qwen, fo, instinct), other, declined or unknown
		Model  string `json:"model"`  // the model's name: optional with a family ("Sonnet 5.5"), required with other (any name not listed)
		Note   string `json:"note"`   // why: written to the admin change log
	}
	if !readRequest(w, r, &req) {
		return
	}
	state, words, problem := adminModelChoice(strings.ToLower(strings.TrimSpace(req.Family)), req.Model)
	if problem != "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_model", "message": problem})
		return
	}
	before := agentModelBadge(id)
	if err := setAgentModel(id, state, words); err != nil {
		writeDBWriteError(w, err, "the model")
		return
	}
	after := agentModelBadge(id)
	logAdminChange("account", id, "ai_model", before.Text, after.Text, "admin", req.Note)
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "model": after, "message": "Saved: " + after.Text})
}
