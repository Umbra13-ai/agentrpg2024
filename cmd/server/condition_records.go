package main

// Conditions as the SRD 5.2.1 Rules Glossary prints them, plus a record of how each one came about
// and how it can end. Everyone sees the condition, its verbatim text and the ways to end it (like a
// DM telling a player "you can try a Strength or Dexterity check"). Only the GM sees the DCs, the
// source and the notes; a player who tries to end a condition gets success or failure.

import (
	"bytes"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

//go:embed data/srd521_glossary.json
var srd521GlossaryJSON []byte

type glossaryEntry struct {
	Name string   `json:"name"`
	Kind string   `json:"kind"`
	Text []string `json:"text"`
}

func srdGlossary() []glossaryEntry {
	var out []glossaryEntry
	json.Unmarshal(srd521GlossaryJSON, &out)
	return out
}

// srdConditions are the 15 conditions of the glossary, in the SRD's order.
func srdConditions() []glossaryEntry {
	var out []glossaryEntry
	for _, e := range srdGlossary() {
		if e.Kind == "Condition" {
			out = append(out, e)
		}
	}
	return out
}

func srdCondition(name string) (glossaryEntry, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, e := range srdConditions() {
		if strings.ToLower(e.Name) == name {
			return e, true
		}
	}
	return glossaryEntry{}, false
}

// conditionEscape is one way to end a condition. The label and the check are shown to players; the DC is not.
type conditionEscape struct {
	Label      string `json:"label"`
	Ability    string `json:"ability,omitempty"`
	Skill      string `json:"skill,omitempty"`
	DC         int    `json:"dc,omitempty"`
	UsesAction *bool  `json:"uses_action,omitempty"` // default true: the attempt takes the creature's action
}

type conditionRecord struct {
	ID             int               `json:"id"`
	LobbyID        int               `json:"-"`
	TargetID       int               `json:"target_id"`
	Condition      string            `json:"condition"`
	Source         string            `json:"source,omitempty"`
	Escapes        []conditionEscape `json:"escapes"`
	DurationRounds int               `json:"duration_rounds,omitempty"`
	EndSave        *conditionSave    `json:"end_save,omitempty"`
	Notes          string            `json:"notes,omitempty"`
}

func ensureConditionRecordsTable() {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS condition_records (
		id SERIAL PRIMARY KEY,
		lobby_id INTEGER NOT NULL,
		target_id INTEGER NOT NULL,
		condition TEXT NOT NULL,
		source TEXT NOT NULL DEFAULT '',
		escapes JSONB NOT NULL DEFAULT '[]',
		duration_rounds INTEGER NOT NULL DEFAULT 0,
		end_save JSONB,
		notes TEXT NOT NULL DEFAULT '',
		created_by INTEGER,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`)
	db.Exec(`ALTER TABLE condition_records ADD COLUMN IF NOT EXISTS end_save JSONB`)
	if err == nil {
		db.Exec(`CREATE INDEX IF NOT EXISTS condition_records_target ON condition_records (lobby_id, target_id)`)
	}
}

var abilityNames = map[string]string{"str": "Strength", "strength": "Strength", "dex": "Dexterity", "dexterity": "Dexterity",
	"con": "Constitution", "constitution": "Constitution", "int": "Intelligence", "intelligence": "Intelligence",
	"wis": "Wisdom", "wisdom": "Wisdom", "cha": "Charisma", "charisma": "Charisma"}

func (e conditionEscape) usesAction() bool { return e.UsesAction == nil || *e.UsesAction }

// escapeLabel describes a check the way a DM would: "Strength (Athletics) check".
func escapeLabel(e conditionEscape) string {
	if e.Label != "" {
		return e.Label
	}
	s := e.Ability + " check"
	if e.Skill != "" {
		s = e.Ability + " (" + e.Skill + ") check"
	}
	return s
}

// defaultEscapes: a Grappled creature can use its action for a Strength (Athletics) or Dexterity
// (Acrobatics) check against the grapple's escape DC (SRD 5.2.1, "Grappling").
func defaultEscapes(condition string, dc int) []conditionEscape {
	if condition == "grappled" && dc > 0 {
		return []conditionEscape{
			{Ability: "Strength", Skill: "Athletics", DC: dc},
			{Ability: "Dexterity", Skill: "Acrobatics", DC: dc},
		}
	}
	return nil
}

// normalizeEscapes checks what the GM sent and fills in labels.
func normalizeEscapes(in []conditionEscape) ([]conditionEscape, string) {
	var out []conditionEscape
	for i, e := range in {
		ab, ok := abilityNames[strings.ToLower(strings.TrimSpace(e.Ability))]
		if !ok {
			return nil, fmt.Sprintf("escapes[%d].ability must be one of Strength, Dexterity, Constitution, Intelligence, Wisdom, Charisma", i)
		}
		if e.DC < 1 || e.DC > 30 {
			return nil, fmt.Sprintf("escapes[%d].dc must be between 1 and 30", i)
		}
		e.Ability = ab
		e.Skill = strings.TrimSpace(e.Skill)
		e.Label = escapeLabel(e)
		out = append(out, e)
	}
	return out, ""
}

func loadConditionRecords(lobbyID, targetID int) []conditionRecord {
	pruneConcentrationConditions(lobbyID, targetID)
	rows, err := db.Query(`SELECT id, target_id, condition, source, escapes, duration_rounds, COALESCE(end_save, 'null'::jsonb), notes
		FROM condition_records WHERE lobby_id = $1 AND target_id = $2 ORDER BY id`, lobbyID, targetID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []conditionRecord
	for rows.Next() {
		var r conditionRecord
		var esc, es []byte
		if rows.Scan(&r.ID, &r.TargetID, &r.Condition, &r.Source, &esc, &r.DurationRounds, &es, &r.Notes) == nil {
			json.Unmarshal(esc, &r.Escapes)
			json.Unmarshal(es, &r.EndSave)
			r.LobbyID = lobbyID
			out = append(out, r)
		}
	}
	return out
}

// conditionView is what a viewer sees. gm adds the DCs, the source and the notes.
func conditionView(rec conditionRecord, speed int, gm bool) map[string]interface{} {
	v := map[string]interface{}{"id": rec.ID, "condition": rec.Condition}
	if def, ok := srdCondition(rec.Condition); ok {
		v["description"] = def.Text
	}
	var ways []map[string]interface{}
	for i, e := range rec.Escapes {
		w := map[string]interface{}{"escape": i, "label": escapeLabel(e), "ability": e.Ability, "uses_action": e.usesAction()}
		if e.Skill != "" {
			w["skill"] = e.Skill
		}
		if gm {
			w["dc"] = e.DC
		}
		ways = append(ways, w)
	}
	if rec.Condition == "prone" && speed > 0 {
		ways = append(ways, map[string]interface{}{"label": fmt.Sprintf("Stand up: spend half your Speed (%d feet of movement)", speed/2), "movement_cost_ft": speed / 2})
	}
	if len(ways) > 0 {
		v["ways_to_end"] = ways
	}
	if rec.DurationRounds > 0 {
		v["duration_rounds"] = rec.DurationRounds
	}
	if gm {
		if rec.Source != "" {
			v["source"] = rec.Source
		}
		if rec.Notes != "" {
			v["notes"] = rec.Notes
		}
		v["target_id"] = rec.TargetID
	}
	return v
}

// conditionsDetail lists every condition of a character: recorded ones with their ways to end, and
// conditions applied without a record with only the verbatim text.
func conditionsDetail(lobbyID, charID, speed int, gm bool) []map[string]interface{} {
	recs := loadConditionRecords(lobbyID, charID)
	seen := map[string]bool{}
	out := []map[string]interface{}{}
	for _, r := range recs {
		seen[r.Condition] = true
		out = append(out, conditionView(r, speed, gm))
	}
	for _, c := range getCharConditions(charID) {
		base := strings.SplitN(strings.ToLower(c), ":", 2)[0]
		if seen[base] {
			continue
		}
		seen[base] = true
		out = append(out, conditionView(conditionRecord{Condition: base, TargetID: charID}, speed, gm))
	}
	return out
}

func gmCampaignOf(w http.ResponseWriter, r *http.Request) (agentID, campaignID int, ok bool) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		writeAuthError(w, err)
		return 0, 0, false
	}
	id, code, msg := gmCampaignFor(agentID, r) // ?campaign_id=N when the GM runs several campaigns
	if code != "" {
		status := http.StatusForbidden
		if code == "campaign_id_required" {
			status = http.StatusBadRequest
		}
		writeJSONStatus(w, status, map[string]interface{}{"error": code, "message": msg})
		return 0, 0, false
	}
	campaignID = id
	return agentID, campaignID, true
}

// monsterInCombat says whether a negative target id is a combatant of the lobby's combat.
func monsterInCombat(lobbyID, id int) bool {
	var raw []byte
	if db.QueryRow(`SELECT turn_order FROM combat_state WHERE lobby_id = $1`, lobbyID).Scan(&raw) != nil {
		return false
	}
	var entries []map[string]interface{}
	json.Unmarshal(raw, &entries)
	for _, e := range entries {
		if m, _ := e["is_monster"].(bool); m && asInt(e["id"]) == id {
			return true
		}
	}
	return false
}

// setExhaustionLevel sets a character's Exhaustion level (0-6): the exhaustion_level column and the exhaustion:N marker in its list.
func setExhaustionLevel(charID, level int) int {
	if level < 0 {
		level = 0
	}
	if level > 6 {
		level = 6
	}
	db.Exec(`UPDATE characters SET exhaustion_level = $1 WHERE id = $2`, level, charID)
	var list []string
	for _, c := range getCharConditions(charID) {
		if !strings.HasPrefix(strings.ToLower(c), "exhaustion:") {
			list = append(list, c)
		}
	}
	if level > 0 {
		list = append(list, fmt.Sprintf("exhaustion:%d", level))
	}
	writeCharList(charID, list)
	return level
}

// gmClearConditions removes a condition ("" = every condition) from a character or monster, by name (F230: the GM clears any
// condition without looking up record ids). For a character the list entries go too ("grappled:12" with "grappled"); "" also sets
// Exhaustion to 0 and leaves the markers that are not conditions (raging, dodging ...). It returns the conditions removed.
func gmClearConditions(campaignID, targetID int, cond string) []string {
	var removed []string
	seen := map[string]bool{}
	for _, rec := range loadConditionRecords(campaignID, targetID) {
		base := conditionBase(rec.Condition)
		if cond != "" && base != cond {
			continue
		}
		db.Exec(`DELETE FROM condition_records WHERE id = $1`, rec.ID)
		if !seen[base] {
			seen[base] = true
			removed = append(removed, base)
		}
	}
	if targetID > 0 {
		var list []string
		for _, c := range getCharConditions(targetID) {
			base := conditionBase(c)
			if isRuleCondition(c) && (cond == "" || base == cond) {
				if !seen[base] {
					seen[base] = true
					removed = append(removed, base)
				}
				continue
			}
			list = append(list, c)
		}
		writeCharList(targetID, list)
		syncListFromRecords(targetID)
		if cond == "" && exhaustionLevelOf(targetID) > 0 {
			setExhaustionLevel(targetID, 0)
			removed = append(removed, "exhaustion")
		}
	}
	return removed
}

// handleGMConditions: POST /api/gm/conditions applies a condition (exhaustion: one level, or "level" sets it); DELETE removes one by
// record_id, or by target_id with a condition name, or every condition with "all" (F230).
func handleGMConditions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST to apply a condition, DELETE to remove one"})
		return
	}
	agentID, campaignID, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		TargetID         int               `json:"target_id"`
		Condition        string            `json:"condition"`
		Source           string            `json:"source"`
		Escapes          []conditionEscape `json:"escapes"`
		EscapeDC         int               `json:"escape_dc"`
		DurationRounds   int               `json:"duration_rounds"`
		Notes            string            `json:"notes"`
		RecordID         int               `json:"record_id"`
		EndSave          *conditionSave    `json:"end_save"`
		FromMagicalSleep bool              `json:"from_magical_sleep"`
		FromElemental    bool              `json:"from_elemental"`
		FromFey          bool              `json:"from_fey"`
		Level            *int              `json:"level"` // exhaustion: the level to set (0-6)
		CasterID         int               `json:"caster_id"` // a spell's state (blessed): who cast it, when more than one has it in play
		All              bool              `json:"all"`   // DELETE: every condition of the target
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
		return
	}
	if slug, isSpell := spellConditionWords[strings.ToLower(strings.TrimSpace(req.Condition))]; isSpell && req.RecordID == 0 {
		// Blessed, Baned, Hasted ...: the state a spell sets; it lives with the spell (and ends with its Concentration)
		body, _ := json.Marshal(map[string]interface{}{"caster_id": req.CasterID, "spell": slug, "target_ids": []int{req.TargetID}})
		r2 := r.Clone(r.Context())
		r2.Body = io.NopCloser(bytes.NewReader(body))
		handleGMSpellEffect(w, r2)
		return
	}
	if r.Method == http.MethodDelete && req.RecordID == 0 { // by name: target_id with a condition, or "all"
		gmDeleteByName(w, campaignID, req.TargetID, req.Condition, req.All)
		return
	}
	if r.Method == http.MethodDelete {
		var target int
		var cond string
		if db.QueryRow(`DELETE FROM condition_records WHERE id = $1 AND lobby_id = $2 RETURNING target_id, condition`, req.RecordID, campaignID).Scan(&target, &cond) != nil {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "record_not_found", "message": "Give the record_id from the condition list"})
			return
		}
		if target > 0 {
			syncListFromRecords(target)
		}
		writeJSONStatus(w, 200, map[string]interface{}{"success": true, "removed": cond, "target_id": target})
		return
	}
	def, valid := srdCondition(req.Condition)
	if !valid {
		var names []string
		for _, c := range srdConditions() {
			names = append(names, strings.ToLower(c.Name))
		}
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_condition", "valid": names,
			"spell_states": "blessed, baned, hasted, slowed, shield of faith, heroism, faerie fire, guidance, resistance (added to the spell in play)"})
		return
	}
	cond := strings.ToLower(def.Name)
	if req.TargetID > 0 {
		var lobby int
		if db.QueryRow(`SELECT lobby_id FROM characters WHERE id = $1`, req.TargetID).Scan(&lobby) != nil || lobby != campaignID {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "target_not_in_campaign"})
			return
		}
	} else if req.TargetID == 0 || !monsterInCombat(campaignID, req.TargetID) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "target_not_found", "message": "target_id is a character id, or the negative id of a monster in the current combat"})
		return
	}
	if cond == "exhaustion" {
		if req.TargetID < 0 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "monsters_have_no_exhaustion_levels"})
			return
		}
		level := exhaustionLevelOf(req.TargetID) + 1
		if req.Level != nil {
			level = *req.Level // F230: the GM sets the level
		}
		level = setExhaustionLevel(req.TargetID, level)
		writeJSONStatus(w, 200, map[string]interface{}{"success": true, "condition": "exhaustion", "exhaustion_level": level,
			"effects": game.ExhaustionEffects(level), "description": def.Text})
		return
	}
	escapes := req.Escapes
	if len(escapes) == 0 {
		escapes = defaultEscapes(cond, req.EscapeDC)
	}
	escapes, msg := normalizeEscapes(escapes)
	if msg != "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_escape", "message": msg})
		return
	}
	if msg := validateEndSave(req.EndSave); msg != "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_end_save", "message": msg})
		return
	}
	extra := map[string]interface{}{}
	if req.TargetID > 0 {
		magical := strings.Contains(strings.ToLower(req.Source), "spell") || strings.Contains(strings.ToLower(req.Source), "magic") // the GM names the source
		if imm, why := buffConditionImmune(campaignID, req.TargetID, cond, 0, magical); imm {                                       // a spell's Immunity: Mind Blank, Heroes' Feast, Gaseous Form ... (F80)
			writeJSONStatus(w, 200, map[string]interface{}{"success": true, "immune": true, "immunity_source": why,
				"message": fmt.Sprintf("%s makes the creature immune to the %s condition: it doesn't take hold.", why, titleCase(conditionBase(cond)))})
			return
		}
		// The established rules for a character (Paladin and Barbarian immunities, Fey Ancestry, releasing
		// grapples, dropping items and falling Prone when Unconscious) are applied by the original handler.
		oldBody, _ := json.Marshal(map[string]interface{}{"condition": cond, "from_magical_sleep": req.FromMagicalSleep,
			"from_elemental": req.FromElemental, "from_fey": req.FromFey})
		rec := httptest.NewRecorder()
		handleAddCondition(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(oldBody)), req.TargetID)
		json.Unmarshal(rec.Body.Bytes(), &extra)
		if _, bad := extra["error"]; bad {
			writeJSONStatus(w, http.StatusBadRequest, extra)
			return
		}
		if immune, _ := extra["immune"].(bool); immune {
			writeJSONStatus(w, 200, extra) // no record: the condition did not take hold
			return
		}
		delete(extra, "condition")
		delete(extra, "effect")
	}
	if heroismImmune(campaignID, req.TargetID, cond) {
		writeJSONStatus(w, 200, map[string]interface{}{"success": true, "immune": true, "immunity_source": "Heroism", "message": "Heroism makes the creature immune to the Frightened condition."})
		return
	}
	esc, _ := json.Marshal(escapes)
	if req.TargetID > 0 { // handleAddCondition left a bare record; this one carries the source, duration and saves
		db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND condition = $3 AND source = '' AND duration_rounds = 0 AND end_save IS NULL`, campaignID, req.TargetID, conditionBase(cond))
	}
	var id int
	err := db.QueryRow(`INSERT INTO condition_records (lobby_id, target_id, condition, source, escapes, duration_rounds, end_save, notes, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, campaignID, req.TargetID, cond, req.Source, esc, req.DurationRounds, endSaveJSON(req.EndSave), req.Notes, agentID).Scan(&id)
	if err != nil {
		writeJSONStatus(w, 500, map[string]interface{}{"error": "could_not_record_condition"})
		return
	}
	if cond == "incapacitated" || cond == "paralyzed" || cond == "petrified" || cond == "stunned" || cond == "unconscious" {
		if req.TargetID > 0 {
			if n, _ := db.Exec(`UPDATE characters SET concentrating_on = NULL WHERE id = $1 AND concentrating_on IS NOT NULL AND concentrating_on <> ''`, req.TargetID); n != nil {
				if c, _ := n.RowsAffected(); c > 0 {
					extra["concentration_broken"] = "Incapacitated: Concentration is broken"
				}
			}
			if isInWildShape(req.TargetID) { // the Incapacitated condition ends Wild Shape (wild_shape_2024.go)
				extra["wild_shape_ended"] = leaveWildShape(req.TargetID, "Incapacitated")
			}
		}
	}
	rec := conditionRecord{ID: id, TargetID: req.TargetID, Condition: cond, Source: req.Source, Escapes: escapes, DurationRounds: req.DurationRounds, EndSave: req.EndSave, Notes: req.Notes}
	extra["success"] = true
	extra["condition"] = conditionView(rec, 0, true)
	writeJSONStatus(w, 200, extra)
}

// gmDeleteByName is DELETE /api/gm/conditions without a record_id: {"target_id": N, "condition": "poisoned"} ends that condition
// (every record of it), {"target_id": N, "condition": "exhaustion"} removes one level (with "level", sets it), {"target_id": N,
// "all": true} (or condition "all") ends every condition and Exhaustion.
func gmDeleteByName(w http.ResponseWriter, campaignID, targetID int, condition string, all bool) {
	if targetID > 0 {
		var lobby int
		if db.QueryRow(`SELECT COALESCE(lobby_id,0) FROM characters WHERE id = $1`, targetID).Scan(&lobby) != nil || lobby != campaignID {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "target_not_in_campaign"})
			return
		}
	} else if targetID == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "target_required",
			"message": "Send record_id, or target_id (a character id, or a monster's negative id) with a condition name or \"all\": true"})
		return
	}
	cond := strings.ToLower(strings.TrimSpace(condition))
	if cond == "all" {
		all = true
	}
	if !all {
		def, valid := srdCondition(cond)
		if !valid {
			var names []string
			for _, c := range srdConditions() {
				names = append(names, strings.ToLower(c.Name))
			}
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_condition", "valid": append(names, "all")})
			return
		}
		cond = strings.ToLower(def.Name)
	}
	if cond == "exhaustion" && !all {
		if targetID < 0 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "monsters_have_no_exhaustion_levels"})
			return
		}
		level := setExhaustionLevel(targetID, exhaustionLevelOf(targetID)-1)
		writeJSONStatus(w, 200, map[string]interface{}{"success": true, "condition": "exhaustion", "exhaustion_level": level,
			"effects": game.ExhaustionEffects(level)})
		return
	}
	if all {
		cond = ""
	}
	removed := gmClearConditions(campaignID, targetID, cond)
	out := map[string]interface{}{"success": true, "target_id": targetID, "removed": removed}
	if removed == nil {
		out["removed"] = []string{}
		out["message"] = "The creature had no such condition."
	}
	if targetID > 0 {
		out["conditions"] = getCharConditions(targetID)
		if charAtZeroHP(targetID) {
			out["note"] = "At 0 Hit Points the character is still Unconscious (with Incapacitated and Prone): that follows from the Hit Points, not a record. Healing ends it."
		}
	}
	writeJSONStatus(w, 200, out)
}

// handleConditionsActive: GET /api/conditions/active?character_id=N lists a character's conditions.
// The character's player gets the player view; the GM of the campaign gets the GM view.
func handleConditionsActive(w http.ResponseWriter, r *http.Request) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		writeAuthError(w, err)
		return
	}
	charID, _ := strconv.Atoi(r.URL.Query().Get("character_id"))
	var owner, lobby, dm int
	var species string
	if db.QueryRow(`SELECT c.agent_id, c.lobby_id, COALESCE(l.dm_id,0), COALESCE(c.species,'') FROM characters c LEFT JOIN lobbies l ON l.id = c.lobby_id WHERE c.id = $1`, charID).Scan(&owner, &lobby, &dm, &species) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	gm := dm == agentID
	if owner != agentID && !gm {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_your_character"})
		return
	}
	speed := characterSpeed(charID, species)
	writeJSONStatus(w, 200, map[string]interface{}{"character_id": charID, "conditions": conditionsDetail(lobby, charID, speed, gm), "exhaustion_level": exhaustionLevelOf(charID)})
}

// handleConditionEscape: POST /api/conditions/escape {character_id, record_id, escape} makes the check.
func handleConditionEscape(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		writeAuthError(w, err)
		return
	}
	var req struct {
		CharacterID int `json:"character_id"`
		RecordID    int `json:"record_id"`
		Escape      int `json:"escape"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
		return
	}
	var owner, lobby, level, str, dex, con, intl, wis, cha int
	var name, skillRaw, expRaw string
	if db.QueryRow(`SELECT agent_id, lobby_id, level, str, dex, con, intl, wis, cha, name, COALESCE(skill_proficiencies,''), COALESCE(expertise,'') FROM characters WHERE id = $1`,
		req.CharacterID).Scan(&owner, &lobby, &level, &str, &dex, &con, &intl, &wis, &cha, &name, &skillRaw, &expRaw) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	if owner != agentID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_your_character"})
		return
	}
	var recs []conditionRecord
	for _, rec := range loadConditionRecords(lobby, req.CharacterID) {
		if rec.ID == req.RecordID {
			recs = append(recs, rec)
		}
	}
	if len(recs) == 0 || req.Escape < 0 || req.Escape >= len(recs[0].Escapes) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_such_way_to_end", "message": "Use record_id and escape (0, 1, ...) from GET /api/conditions/active"})
		return
	}
	rec, esc := recs[0], recs[0].Escapes[req.Escape]
	if esc.usesAction() {
		var used bool
		db.QueryRow(`SELECT COALESCE(action_used,false) FROM characters WHERE id = $1`, req.CharacterID).Scan(&used)
		if used {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "action_already_used", "message": "This attempt takes your action, and you have used it this turn."})
			return
		}
		db.Exec(`UPDATE characters SET action_used = true WHERE id = $1`, req.CharacterID)
	}
	scores := map[string]int{"Strength": str, "Dexterity": dex, "Constitution": con, "Intelligence": intl, "Wisdom": wis, "Charisma": cha}
	mod := game.Modifier(scores[esc.Ability]) - game.ExhaustionD20Penalty(exhaustionLevelOf(req.CharacterID))
	skillKey := strings.ToLower(strings.ReplaceAll(esc.Skill, " ", "_"))
	has := func(list string) bool {
		for _, f := range strings.FieldsFunc(strings.ToLower(list), func(r rune) bool { return r == ',' || r == ' ' || r == ';' || r == '"' || r == '[' || r == ']' }) {
			if strings.ReplaceAll(f, "-", "_") == skillKey {
				return true
			}
		}
		return false
	}
	if skillKey != "" && has(skillRaw) {
		mod += game.ProficiencyBonus(level)
		if has(expRaw) {
			mod += game.ProficiencyBonus(level)
		}
	}
	conditions := getCharConditions(req.CharacterID)
	disadv := game.GetAbilityCheckDisadvantage(conditions, 0, false)
	adv := largeFormAdvantage(req.CharacterID, esc.Ability) // Goliath Large Form: Advantage on Strength checks
	roll, r1, r2 := 0, 0, 0
	switch {
	case disadv && !adv:
		roll, r1, r2 = game.RollWithDisadvantage()
	case adv && !disadv:
		roll, r1, r2 = game.RollWithAdvantage()
	default:
		roll = game.RollDie(20)
		r1 = roll
	}
	total := roll + mod
	success := total >= esc.DC
	resp := map[string]interface{}{"character": name, "condition": rec.Condition, "attempt": escapeLabel(esc), "roll": roll, "modifier": mod, "total": total, "success": success}
	if disadv && !adv {
		resp["rolls"] = []int{r1, r2}
		resp["disadvantage"] = true
	} else if adv && !disadv {
		resp["rolls"] = []int{r1, r2}
		resp["advantage"] = "Large Form"
	}
	if success {
		deleteConditionRecord(rec.ID)
		resp["message"] = fmt.Sprintf("%s total %d: success. The %s condition ends.", name, total, rec.Condition)
	} else {
		resp["message"] = fmt.Sprintf("%s total %d: failure. The %s condition continues.", name, total, rec.Condition)
	}
	db.Exec(`INSERT INTO actions (lobby_id, character_id, action_type, description, result) VALUES ($1,$2,'escape_condition',$3,$4)`,
		lobby, req.CharacterID, name+" tries to end "+rec.Condition+": "+escapeLabel(esc), fmt.Sprint(resp["message"]))
	writeJSONStatus(w, 200, resp)
}

var _ = sql.ErrNoRows

// conditionsPage prints the 15 conditions of the SRD Rules Glossary as written.
func conditionsPage() string {
	var b strings.Builder
	b.WriteString(`<h1>🩸 Conditions</h1>`)
	for _, c := range srdConditions() {
		b.WriteString(`<h2 id="` + e(strings.ToLower(c.Name)) + `">` + e(c.Name) + `</h2>`)
		for _, p := range c.Text {
			b.WriteString("<p>" + e(p) + "</p>")
		}
	}
	return b.String()
}

// handleUniverseConditions: GET /api/universe/conditions returns the verbatim SRD text of each condition.
func handleUniverseConditions(w http.ResponseWriter, r *http.Request) {
	type cond struct {
		Name        string   `json:"name"`
		Description []string `json:"description"`
	}
	var out []cond
	for _, c := range srdConditions() {
		out = append(out, cond{Name: c.Name, Description: c.Text})
	}
	writeJSONStatus(w, 200, map[string]interface{}{"ruleset": "2024", "source": "SRD 5.2.1", "count": len(out), "conditions": out})
}

// conditionSave is a saving throw repeated to end a condition ("repeat the save at the end of each of its turns").
type conditionSave struct {
	Ability string `json:"ability"`
	DC      int    `json:"dc"`
	When    string `json:"when,omitempty"` // "end_of_turn" (default) or "start_of_turn"
}

func endSaveJSON(s *conditionSave) interface{} {
	if s == nil {
		return nil
	}
	b, _ := json.Marshal(s)
	return string(b)
}

// handleCharacterConditions serves POST and DELETE /api/characters/{id}/conditions. Only the GM of the
// character's campaign may apply or remove conditions; players end them with POST /api/conditions/escape.
// The request is the same as POST /api/gm/conditions with target_id taken from the path.
func handleCharacterConditions(w http.ResponseWriter, r *http.Request, charID int) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		writeAuthError(w, err)
		return
	}
	var lobby, dm int
	if db.QueryRow(`SELECT COALESCE(c.lobby_id,0), COALESCE(l.dm_id,0) FROM characters c LEFT JOIN lobbies l ON l.id = c.lobby_id WHERE c.id = $1`, charID).Scan(&lobby, &dm) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	if dm != agentID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_gm",
			"message": "Only the GM of this character's campaign applies or removes conditions. To end one, use POST /api/conditions/escape (see GET /api/conditions/active)."})
		return
	}
	body := map[string]interface{}{}
	json.NewDecoder(r.Body).Decode(&body)
	if r.Method == http.MethodDelete {
		cond, _ := body["condition"].(string)
		all, _ := body["all"].(bool)
		gmDeleteByName(w, lobby, charID, cond, all) // F230: by name, exhaustion one level, or "all"
		return
	}
	body["target_id"] = charID
	b, _ := json.Marshal(body)
	r.Body = io.NopCloser(bytes.NewReader(b))
	r.Method = http.MethodPost
	handleGMConditions(w, r)
}

// clearMonsterConditions drops the condition records of monsters (negative ids) when a fight starts or ends.
func clearMonsterConditions(campaignID int) {
	clearVexMarks(campaignID)
	clearCombatEffects(campaignID)
	clearSpellEffects(campaignID)
	clearMonsterStates(campaignID) // Legendary Action and Resistance uses, lair, auras (monster_features_2024.go)
	clearMonsterReactions(campaignID)
	clearBurning(campaignID, true) // F69: a monster's fire doesn't pass to the monster with its id in the next fight
	if db != nil {
		db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id < 0`, campaignID)
	}
}
