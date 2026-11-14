package main

// Table rules: choices a GM makes for a campaign, shown on the campaign page and followed by the server. The defaults are the rules
// as written; the alternatives are common table choices (competent adventurers have food and water, ordinary ammunition isn't
// counted, weight is ignored unless someone exploits it).

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"
	"time"
)

type tableRuleOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Text  string `json:"text"`
}

type tableRuleDef struct {
	Key     string            `json:"key"`
	Title   string            `json:"title"`
	Default string            `json:"default"`
	Options []tableRuleOption `json:"options"`
}

var tableRuleDefs = []tableRuleDef{
	{Key: "food_and_water", Title: "Food and water", Default: "tracked", Options: []tableRuleOption{
		{"tracked", "Tracked", "Characters need food and water every day; going short brings Exhaustion (the Dehydration and Malnutrition rules)."},
		{"assumed", "Assumed", "The adventurers are competent: they carry enough food and drink and eat when they need to. Hunger and thirst matter only when the GM says supplies have run out."},
	}},
	{Key: "mundane_ammunition", Title: "Ammunition", Default: "counted", Options: []tableRuleOption{
		{"counted", "Counted", "Every arrow, bolt, sling bullet and blowgun needle fired is used up; half can be recovered after a fight."},
		{"unlimited", "Unlimited mundane ammunition", "Ordinary arrows, bolts, sling bullets and needles are never used up. Magic ammunition is still counted."},
	}},
	{Key: "visibility", Title: "Who can watch", Default: "public", Options: []tableRuleOption{
		{"public", "Public", "Anyone can watch the campaign page, its log and its feed."},
		{"private", "Private", "Only the players, the GM and people with the watch password can watch."},
	}},
	{Key: "dawn", Title: "Magic items at dawn", Default: "long_rest", Options: []tableRuleOption{
		{"long_rest", "A Long Rest is the next dawn", "Magic items regain the charges they get back at dawn when their owner finishes a Long Rest, as if dawn came during it. The GM can also call dawn (POST /api/gm/dawn)."},
		{"gm", "The GM calls dawn", "Magic items regain their charges only when the GM says dawn has come (POST /api/gm/dawn) or 24 hours pass (gm/pass-time)."},
	}},
	{Key: "encumbrance", Title: "Encumbrance", Default: "rules", Options: []tableRuleOption{
		{"rules", "Carrying Capacity rules", "A creature can carry Strength × 15 lb (Small or Medium; Tiny × 7.5, Large × 30, Huge × 60, Gargantuan × 120). Carrying more than that limits its Speed to 5 feet."},
		{"ignored", "Ignored", "Weight isn't tracked unless a player tries to exploit it; then the GM applies the Carrying Capacity rules."},
	}},
}

func init() {
	tableRuleDefs = append(tableRuleDefs, timeZoneTableRule(), multiclassingTableRule(),
		// F248: the auto-skip worker's timeouts (campaign_flow.go), per campaign; "off" while the GM holds the turn
		tableRuleDef{Key: "combat_turn_timeout", Title: "Combat turn timeout", Default: "4h", Options: []tableRuleOption{
			{"4h", "4 hours", "A monster's or NPC's combat turn the GM hasn't resolved for 4 hours is skipped (the server checks every 30 minutes, so 4 to 4.5 hours). A player's turn is never skipped and has no timer: a character with no turn in when its slot comes up has its turn taken by the GM as stand-in."},
			{"8h", "8 hours", "Skipped after 8 hours of inactivity."},
			{"12h", "12 hours", "Skipped after 12 hours of inactivity."},
			{"24h", "24 hours", "Skipped after 24 hours of inactivity."},
			{"off", "Off", "Turns are never skipped automatically; the GM advances them (use it to hold a turn)."},
		}},
		tableRuleDef{Key: "exploration_timeout", Title: "Exploration timeout", Default: "12h", Options: []tableRuleOption{
			{"12h", "12 hours", "Outside combat, a player inactive for 12 hours is skipped in the exploration order."},
			{"24h", "24 hours", "Skipped after 24 hours."},
			{"48h", "48 hours", "Skipped after 48 hours."},
			{"off", "Off", "Players are never skipped automatically outside combat."},
		}})
}

// timeoutRule: the campaign's timeout for a table rule ("4h" ...); ok is false when it is "off".
func timeoutRule(lobbyID int, key string, def time.Duration) (time.Duration, bool) {
	v := tableRule(lobbyID, key)
	if v == "off" {
		return 0, false
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d, true
	}
	return def, true
}

func tableRuleDefByKey(key string) (tableRuleDef, bool) {
	for _, d := range tableRuleDefs {
		if d.Key == key {
			return d, true
		}
	}
	return tableRuleDef{}, false
}

// campaignTableRules returns every table rule's value for a campaign (the default where the GM hasn't chosen).
func campaignTableRules(lobbyID int) map[string]string {
	out := map[string]string{}
	for _, d := range tableRuleDefs {
		out[d.Key] = d.Default
	}
	if db == nil || lobbyID == 0 {
		return out
	}
	var raw []byte
	if db.QueryRow(`SELECT COALESCE(table_rules, '{}') FROM lobbies WHERE id = $1`, lobbyID).Scan(&raw) != nil {
		return out
	}
	var set map[string]string
	json.Unmarshal(raw, &set)
	for k, v := range set {
		if d, ok := tableRuleDefByKey(k); ok {
			for _, o := range d.Options {
				if o.Value == v {
					out[k] = v
				}
			}
		}
	}
	return out
}

// tableRule is one table rule's value for a campaign.
func tableRule(lobbyID int, key string) string {
	return campaignTableRules(lobbyID)[key]
}

// tableRulesView is the readable form: each rule with the chosen option's label and text.
func tableRulesView(lobbyID int) []map[string]interface{} {
	vals := campaignTableRules(lobbyID)
	var out []map[string]interface{}
	for _, d := range tableRuleDefs {
		for _, o := range d.Options {
			if o.Value == vals[d.Key] {
				out = append(out, map[string]interface{}{"key": d.Key, "title": d.Title, "value": o.Value, "label": o.Label, "text": o.Text, "is_default": o.Value == d.Default})
			}
		}
	}
	return out
}

// tableRulesHTML is the Table Rules section of the campaign page.
func tableRulesHTML(lobbyID int) string {
	var b strings.Builder
	b.WriteString(`<ul class="table-rules">`)
	for _, r := range tableRulesView(lobbyID) {
		note := ""
		if r["is_default"] == true {
			note = ` <span class="muted">(rules as written)</span>`
		}
		b.WriteString(fmt.Sprintf(`<li><strong>%s:</strong> %s%s<br><span class="muted">%s</span></li>`,
			html.EscapeString(fmt.Sprint(r["title"])), html.EscapeString(fmt.Sprint(r["label"])), note, html.EscapeString(fmt.Sprint(r["text"]))))
	}
	b.WriteString(`</ul>`)
	return b.String()
}

// handleCampaignTableRules godoc
// @Summary Campaign table rules
// @Description GET (anyone): the GM's table rules for the campaign, each with its meaning and the choices. POST (GM only): change some of them, for example {"food_and_water":"assumed","mundane_ammunition":"unlimited","encumbrance":"ignored"}. food_and_water: tracked (default) or assumed (competent adventurers carry enough). mundane_ammunition: counted (default) or unlimited (ordinary arrows, bolts, sling bullets and needles are not used up; magic ammunition is). encumbrance: rules (default; Carrying Capacity) or ignored (unless someone exploits it). spell_hands: required (default; a spell's Somatic component needs a hand free of weapons and a Shield, or the focus hand when its Material component has no cost; War Caster waives it) or ignored. The server follows them: unlimited ammunition skips using up ordinary ammunition, the encumbrance check reports "ignored", and the hunger and thirst hazards remind the GM that food and water are assumed.
// @Tags Campaigns
// @Param id path int true "Campaign ID"
// @Param request body object{food_and_water=string,mundane_ammunition=string,encumbrance=string} false "Rules to change (POST)"
// @Router /campaigns/{id}/table-rules [get]
// @Router /campaigns/{id}/table-rules [post]
func handleCampaignTableRules(w http.ResponseWriter, r *http.Request, campaignID int) {
	var dmID int
	if db.QueryRow(`SELECT COALESCE(dm_id,0) FROM lobbies WHERE id = $1`, campaignID).Scan(&dmID) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "campaign_not_found"})
		return
	}
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		agentID, err := getAgentFromAuth(r)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		if agentID != dmID {
			writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "only_gm_can_set_table_rules"})
			return
		}
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req) == 0 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "rules_required", "rules": tableRuleDefs})
			return
		}
		// the watch password of a private campaign (stored as a hash; private_campaigns.go)
		if pw, has := req["watch_password"]; has {
			delete(req, "watch_password")
			if err := setWatchPassword(campaignID, strings.TrimSpace(pw)); err != nil {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_watch_password", "message": publicError(err)})
				return
			}
		}
		if strings.ToLower(strings.TrimSpace(req["visibility"])) == "private" && watchHash(campaignID) == "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "watch_password_required", "message": "A private campaign needs a watch_password (at least 4 characters)."})
			return
		}
		cur := campaignTableRules(campaignID)
		var keys []string
		for k := range req {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := strings.ToLower(strings.TrimSpace(req[k]))
			d, ok := tableRuleDefByKey(k)
			if !ok {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "unknown_rule", "message": fmt.Sprintf("%q is not a table rule", k), "rules": tableRuleDefs})
				return
			}
			valid := false
			var vals []string
			for _, o := range d.Options {
				vals = append(vals, o.Value)
				if o.Value == v {
					valid = true
				}
			}
			if !valid {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_value", "message": fmt.Sprintf("%s must be one of: %s", k, strings.Join(vals, ", "))})
				return
			}
			cur[k] = v
		}
		b, _ := json.Marshal(cur)
		if _, err := db.Exec(`UPDATE lobbies SET table_rules = $1 WHERE id = $2`, b, campaignID); err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"})
			return
		}
		var summary []string
		for _, v := range tableRulesView(campaignID) {
			summary = append(summary, fmt.Sprintf("%s: %s", v["title"], v["label"]))
		}
		recordGMAction(db, campaignID, "table_rules", "Table rules: "+strings.Join(summary, "; "), "")
	}
	writeJSONStatus(w, 200, map[string]interface{}{"campaign_id": campaignID, "table_rules": tableRulesView(campaignID), "choices": tableRuleDefs})
}
