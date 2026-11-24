package main

// The GM compendium in the existing database: monsters a GM imports for its campaigns, private to the GM until it shares them, and
// immutable (an edit saves a new version, custom_monster_edit_2024.go). The SRD table and public catalog are never written.
import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
	"github.com/lib/pq"
)

// monsterSQL is the runtime stat-block boundary. Only explicit custom keys select private data;
// all ordinary SRD queries, including SQLite tests, retain their original query and behavior.
// GM requests check custom keys exist; internal combat reads use those saved keys.
func monsterSQL(query string, args []interface{}) string {
	for i, a := range args {
		if key, ok := a.(string); ok && strings.HasPrefix(key, "custom:") {
			relation := fmt.Sprintf(`FROM (SELECT (json_populate_record(NULL::monsters, stats::json)).* FROM custom_monsters WHERE key = $%d) AS monsters`, i+1)
			return strings.ReplaceAll(query, "FROM monsters", relation)
		}
	}
	return query
}
func monsterQueryRow(query string, args ...interface{}) *sql.Row {
	return db.QueryRow(monsterSQL(query, args), args...)
}
func monsterExec(query string, args ...interface{}) (sql.Result, error) {
	return db.Exec(monsterSQL(query, args), args...)
}

// compendiumVisible is the one rule of who may list and use a compendium entry (owner, Nov 22): its owner, or anyone while it is shared. An
// entry from before ownership has no owner and stays shared. A hidden entry is off the list but still works by its key, so a fight
// that started with it, and a campaign that knows it, keep it. $N is the caller's account.
func compendiumVisible(arg int) string {
	return fmt.Sprintf("(owner_id IS NULL OR shared OR owner_id = $%d)", arg)
}

// compendiumUsable: whether this account may use the entry named by key.
func compendiumUsable(agentID int, key string) (bool, error) {
	var ok bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM custom_monsters WHERE key=$1 AND `+compendiumVisible(2)+`)`, key, agentID).Scan(&ok)
	return ok, err
}

// Check references before any mutation. Also covers nested GM requests through gmReadAs. An entry the caller may not use answers as an
// unknown one does, so a private entry's key is not confirmed to another GM.
func checkCustomReferences(w http.ResponseWriter, r *http.Request, request interface{}) bool {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return false
	}
	var keys []string
	referenceFields := map[string]bool{"monster_key": true, "attacker_monster_key": true, "monster": true, "monster_name": true, "attacker_name": true, "form": true}
	var walk func(reflect.Value, string)
	walk = func(v reflect.Value, field string) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Ptr:
			if !v.IsNil() {
				walk(v.Elem(), field)
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i), strings.Split(v.Type().Field(i).Tag.Get("json"), ",")[0])
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), field)
			}
		case reflect.Map:
			for _, k := range v.MapKeys() {
				if k.Kind() == reflect.String {
					walk(v.MapIndex(k), k.String())
				}
			}
		case reflect.String:
			if referenceFields[field] && strings.HasPrefix(strings.ToLower(strings.TrimSpace(v.String())), "custom:") {
				keys = append(keys, v.String())
			}
		}
	}
	walk(reflect.ValueOf(request), "")
	for _, key := range keys {
		exists, err := compendiumUsable(agentID, key)
		if answerDBFailure(w, err, "the compendium") {
			return false
		}
		if !exists {
			writeJSONStatus(w, 404, map[string]interface{}{"error": "monster_not_found", "message": "That custom monster is not in your compendium or shared with you. GET /api/gm/compendium/monsters lists yours. Nothing was changed."})
			return false
		}
	}
	return true
}

func validateCustomMonster(m srdMonster2024) string {
	if strings.TrimSpace(m.Name) == "" || len(m.Name) > 100 {
		return "name must contain 1–100 bytes"
	}
	if m.HP < 1 || m.HP > 100000 || m.AC < 1 || m.AC > 50 {
		return "hp must be 1–100000 and ac 1–50"
	}
	if m.PB < 2 || m.PB > 9 || m.XP < 0 || m.XPLair < 0 || m.XP > 2147483647 || m.XPLair > 2147483647 {
		return "proficiency_bonus must be 2–9 and XP must fit a nonnegative 32-bit integer"
	}
	sizes := map[string]bool{"tiny": true, "small": true, "medium": true, "large": true, "huge": true, "gargantuan": true}
	if !sizes[strings.ToLower(m.Size)] {
		return "size must be Tiny, Small, Medium, Large, Huge or Gargantuan"
	}
	if strings.TrimSpace(m.Type) == "" || len(m.Type) > 50 || len(m.Speed) > 200 {
		return "type is required (up to 50 bytes); speed is limited to 200 bytes"
	}
	cr := m.CR
	if cr != "1/8" && cr != "1/4" && cr != "1/2" {
		n, e := strconv.Atoi(cr)
		if e != nil || n < 0 || n > 30 {
			return "cr must be 0–30, 1/8, 1/4 or 1/2"
		}
	}
	if len(m.Abilities) != 6 {
		return "abilities must contain exactly str, dex, con, int, wis and cha"
	}
	for _, k := range []string{"str", "dex", "con", "int", "wis", "cha"} {
		a, ok := m.Abilities[k]
		if !ok || a.Score < 1 || a.Score > 30 || a.Save < -10 || a.Save > 30 {
			return "each ability requires score 1–30 and save -10–30"
		}
	}
	for _, entries := range [][]srdMonsterEntry{m.Traits, m.Actions, m.BonusActions, m.Reactions, m.Legendary} {
		if len(entries) > 50 {
			return "each action or trait section is limited to 50 entries"
		}
		for _, e := range entries {
			if strings.TrimSpace(e.Name) == "" || len(e.Name) > 200 || strings.TrimSpace(e.Text) == "" || len(e.Text) > 10000 {
				return "entries require name (up to 200 bytes) and text (up to 10000 bytes)"
			}
		}
	}
	for _, g := range []struct {
		kind    string
		entries []srdMonsterEntry
	}{{"trait", m.Traits}, {"action", m.Actions}, {"bonus", m.BonusActions}, {"reaction", m.Reactions}, {"legendary", m.Legendary}} {
		for _, e := range g.entries {
			if p := mechanicProblem(g.kind, e); p != "" { // the attached mechanics (monster_mechanics_2024.go)
				return p
			}
		}
	}
	// An imported Multiattack is checked as it will be played, so a definition cannot ask for an unbounded number of attacks. Other
	// text the engine cannot resolve is not an import error: the attack is refused when it is made and the GM resolves it.
	rows := make([]monsterActionRow, 0, len(m.Actions))
	for _, e := range m.Actions {
		rows = append(rows, entryActionRow(e))
	}
	for _, e := range m.Actions {
		if strings.EqualFold(stripUsage(e.Name), "Multiattack") {
			if _, problem := multiattackPlan(e.Text, rows); strings.HasPrefix(problem, multiattackOverCapProblem) {
				return "Multiattack: " + problem
			}
		}
	}
	return ""
}

// customMonsterStats adapts the same structured 2024 input to the legacy database stat-block columns.
// Complex action text remains subject to the existing engine's parsers and GM adjudication.
func customMonsterStats(key string, m srdMonster2024) map[string]interface{} {
	speed, _ := strconv.Atoi(speedNumRe.FindString(m.Speed))
	hitDice := ""
	if parts := strings.FieldsFunc(m.HPFormula, func(r rune) bool { return r == '+' || r == '-' }); len(parts) > 0 {
		hitDice = strings.TrimSpace(parts[0])
	}
	saves := map[string]int{}
	s := map[string]interface{}{"slug": key,
		"name":                   m.Name,
		"size":                   strings.ToLower(m.Size),
		"type":                   strings.ToLower(strings.TrimSpace(strings.SplitN(m.Type, "(", 2)[0])),
		"ac":                     m.AC,
		"hp":                     m.HP,
		"speed":                  speed,
		"speed_text":             m.Speed,
		"cr":                     m.CR,
		"xp":                     m.XP,
		"xp_lair":                m.XPLair,
		"proficiency_bonus":      m.PB,
		"hp_formula":             m.HPFormula,
		"hit_dice":               hitDice,
		"alignment":              m.Alignment,
		"skills":                 m.Skills,
		"senses":                 m.Senses,
		"languages":              m.Languages,
		"gear":                   m.Gear,
		"group_name":             m.Group,
		"source":                 "custom",
		"ruleset":                "2024",
		"actions":                oldStyleActions(m.Actions),
		"action_entries":         m.Actions,
		"traits":                 m.Traits,
		"bonus_actions":          m.BonusActions,
		"reactions":              m.Reactions,
		"legendary_intro":        m.LegendaryIn,
		"damage_resistances":     strings.ToLower(m.Resistances),
		"damage_immunities":      strings.ToLower(m.DmgImmune),
		"damage_vulnerabilities": strings.ToLower(m.Vulnerable),
		"condition_immunities":   strings.ToLower(m.CondImmune)}
	if m.Initiative.Mod != 0 || m.Initiative.Score != 0 {
		s["initiative_mod"] = m.Initiative.Mod
	} else {
		s["initiative_mod"] = game.Modifier(m.Abilities["dex"].Score)
	}
	for k, a := range m.Abilities {
		saves[k] = a.Save
		column := k
		if k == "int" {
			column = "intl"
		}
		s[column] = a.Score
	}
	s["saves"] = saves
	legendary := []map[string]interface{}{}
	for _, e := range m.Legendary {
		legendary = append(legendary, map[string]interface{}{"name": e.Name, "desc": e.Text, "cost": 1})
	}
	s["legendary_actions"] = legendary
	meta := metaFromMonster(key, m)
	s["legendary_action_count"] = meta.LAUses
	s["legendary_resistances"] = meta.LR
	return s
}

// insertCompendiumMonster is the one writer of a compendium row (import and edit): a new key, the definition and its stats, in the caller's
// transaction, owned by agentID and private unless shared. replaces is the key of the version an edit supersedes. It answers a refusal
// itself and returns false.
func insertCompendiumMonster(w http.ResponseWriter, tx *sql.Tx, agentID int, m srdMonster2024, note, replaces string, shared bool) (string, bool) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		writeJSONStatus(w, 500, map[string]interface{}{"error": "id_failed", "message": "Could not create monster IDs. Nothing was changed."})
		return "", false
	}
	key := "custom:" + hex.EncodeToString(id[:])
	definition, err := json.Marshal(m)
	if err != nil {
		writeDBWriteError(w, err, "the compendium")
		return "", false
	}
	stats, err := json.Marshal(customMonsterStats(key, m))
	if err != nil {
		writeDBWriteError(w, err, "the compendium")
		return "", false
	}
	if _, err := tx.Exec(`INSERT INTO custom_monsters(key,definition,stats,source_note,owner_id,shared,replaces) VALUES($1,$2,to_jsonb(json_populate_record(NULL::monsters,$3::json)),$4,$5,$6,NULLIF($7,''))`,
		key, string(definition), string(stats), note, agentID, shared, replaces); err != nil {
		if pg, ok := err.(*pq.Error); ok && pg.Code.Class() == "22" {
			handledDBError(err)
			writeJSONStatus(w, 400, map[string]interface{}{"error": "invalid_monster", "message": "A monster contains text or numbers outside the stat-block storage limits. Nothing was changed."})
			return "", false
		}
		writeDBWriteError(w, err, "the compendium")
		return "", false
	}
	return key, true
}

// handleGMCompendiumMonsters godoc
// @Summary GM monster compendium
// @Description How to write a monster the server runs: the guide at /gm-monsters.md. GET lists the monsters you imported and the ones other GMs shared (limit 100; after is a pagination cursor; mine=1 lists only yours; hidden ones are left out). POST imports 1–25 structured monster definitions atomically; an import is private to you until you share it (POST /api/gm/compendium/monsters/share). Only a GM uses it: campaign_id names a campaign you run (needed when you run more than one). A definition never changes: edit one with POST /api/gm/compendium/monsters/edit, which saves a new version. Existing mechanics apply; the GM adjudicates other abilities. SRD data is separate.
// @Tags GM
// @Param after query string false "GET pagination cursor from next_after"
// @Param mine query boolean false "GET: only the monsters you imported"
// @Param campaign_id query int false "GET: a campaign you run (needed when you run more than one)"
// @Router /gm/compendium/monsters [get]
// @Router /gm/compendium/monsters [post]
func handleGMCompendiumMonsters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeJSONStatus(w, 405, map[string]interface{}{"error": "method_not_allowed", "message": "Use GET or POST."})
		return
	}
	agent, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	// who may use the shared compendium is a GM, as every GM command decides it (gm_campaign_2024.go): the campaign named with
	// campaign_id, else the GM's only campaign, started or not
	if r.Method == http.MethodGet {
		if _, code, msg := gmCampaignFor(agent, r, campaignBeforeStart()); code != "" {
			writeGMCampaignProblem(w, code, msg)
			return
		}
		onlyMine := ""
		if q := r.URL.Query().Get("mine"); q == "1" || q == "true" {
			onlyMine = " AND owner_id = $2"
		}
		rows, err := db.Query(`SELECT key,definition,source_note,COALESCE(owner_id,0)=$2,shared,COALESCE(replaces,''),CASE WHEN token IS NULL THEN NULL ELSE token_updated_at END FROM custom_monsters
			WHERE key>$1 AND hidden_at IS NULL AND `+compendiumVisible(2)+onlyMine+` ORDER BY key LIMIT 101`, r.URL.Query().Get("after"), agent)
		if err != nil {
			writeDBError(w, err, "the compendium")
			return
		}
		defer rows.Close()
		out := []map[string]interface{}{}
		next := ""
		for rows.Next() {
			var key, note, replaces string
			var raw []byte
			var mine, shared bool
			var tokenAt sql.NullTime
			if err := rows.Scan(&key, &raw, &note, &mine, &shared, &replaces, &tokenAt); err != nil {
				writeDBError(w, err, "the compendium")
				return
			}
			if len(out) == 100 {
				next = out[99]["monster_key"].(string)
				break
			}
			entry := map[string]interface{}{"monster_key": key, "monster": json.RawMessage(raw), "source_note": note, "mine": mine, "shared": shared, "replaces": replaces,
				"stat_block_url": statBlockLink(key)} // its page for 24 hours (statblock_view_2024.go)
			if u := customTokenURLAt(key, tokenAt); u != "" {
				entry["token_url"] = u // its token (monster_token_2024.go)
			}
			out = append(out, entry)
		}
		if err := rows.Err(); err != nil {
			writeDBError(w, err, "the compendium")
			return
		}
		writeJSONStatus(w, 200, map[string]interface{}{"monsters": out, "next_after": next, "token_rule": monsterTokenRuleText})
		return
	}
	var req struct {
		SourceNote string           `json:"source_note"` // Required provenance for the shared import, up to 500 bytes.
		Monsters   []srdMonster2024 `json:"monsters"`    // 1–25 structured 2024 stat blocks; name, size, type, ac, hp, abilities, cr and proficiency_bonus are required.
	}
	if _, ok := gmReadAs(w, r, agent, &req, campaignBeforeStart()); !ok {
		return
	}
	problem := ""
	if len(req.Monsters) < 1 || len(req.Monsters) > 25 {
		problem = "Import 1–25 monsters at a time."
	}
	if strings.TrimSpace(req.SourceNote) == "" || len(req.SourceNote) > 500 {
		problem = "source_note is required, up to 500 bytes."
	}
	notes := make([][]string, len(req.Monsters))
	for i := range req.Monsters {
		notes[i] = normalizeCustomMonster(&req.Monsters[i])
		if msg := validateCustomMonster(req.Monsters[i]); msg != "" {
			problem = fmt.Sprintf("monsters[%d]: %s", i, msg)
			break
		}
	}
	if problem != "" {
		writeJSONStatus(w, 400, map[string]interface{}{"error": "invalid_monster", "message": problem})
		return
	}
	tx, err := db.Begin()
	if err != nil {
		writeDBWriteError(w, err, "the compendium")
		return
	}
	defer tx.Rollback()
	out := []map[string]interface{}{}
	for i, m := range req.Monsters {
		key, ok := insertCompendiumMonster(w, tx, agent, m, req.SourceNote, "", false)
		if !ok {
			return
		}
		out = append(out, map[string]interface{}{"monster_key": key, "name": m.Name, "report": customMonsterReport(m, notes[i], false), "stat_block_url": statBlockLink(key)})
	}
	if err := tx.Commit(); err != nil {
		writeDBWriteError(w, err, "the compendium")
		return
	}
	writeJSONStatus(w, 201, map[string]interface{}{"monsters": out, "token_rule": monsterTokenRuleText, "message": "Imported as yours alone (share one with POST /api/gm/compendium/monsters/share). Use monster_key in combat/add. Each report says what the server filled in, what it will count as 0, and how many sentences a rule reads and how many you resolve; POST /api/gm/compendium/monsters/preview lists them sentence by sentence. Give each a token for the encounter stage (token_rule). stat_block_url opens a monster's stat block as a page for 24 hours (send it to your human to look at). How to word a monster so the server runs it: " + publicBaseURL() + "/gm-monsters.md"})
}
