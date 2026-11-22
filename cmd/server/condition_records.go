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
	Detail         string            `json:"-"` // what follows the condition's name in the list entry ("charmed:12": 12)
	SourceID       int               `json:"-"` // the creature that caused it (a monster's negative id): ownership by identity, not by the display text of Source
	CreatedBy      int               `json:"-"` // a Concentration spell's caster (notes "concentration"), or the GM who set it
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

// loadConditionRecords reads a creature's conditions (condition_owner_2024.go): a character's wherever it plays, a monster's in its
// campaign. Conditions a caster's ended Concentration held are dropped first.
func loadConditionRecords(lobbyID, targetID int) []conditionRecord {
	if db == nil {
		return nil
	}
	pruneConcentrationConditions(lobbyID, targetID)
	out, _ := readConditionRecords(db, lobbyID, targetID)
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
	// what this record does besides the condition ("Can't regain Hit Points while Poisoned", a poison's damage at the start of each of
	// its turns) and how it ends on its own: what the stat block already says, for the player and the GM (S-ALDEN-10)
	effects := lastingEffectsOf(rec)
	if s := rec.EndSave; s != nil && s.Ability != "" {
		when := "end"
		if s.When == "start_of_turn" {
			when = "start"
		}
		save := fmt.Sprintf("Repeats the %s saving throw at the %s of each of its turns; a success ends it", abilityNames[strings.ToLower(s.Ability)], when)
		if gm && s.DC > 0 {
			save = fmt.Sprintf("Repeats the DC %d %s saving throw at the %s of each of its turns; a success ends it", s.DC, abilityNames[strings.ToLower(s.Ability)], when)
		}
		effects = append(effects, save)
	}
	if len(effects) > 0 {
		v["effects"] = effects
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

// gmClearConditions removes a condition ("" = every condition) from a character or monster, by name (F230: the GM clears any
// condition without looking up record ids). For a character the list entries go too ("grappled:12" with "grappled"); "" also sets
// Exhaustion to 0 and leaves the markers that are not conditions (raging, dodging ...). It returns the conditions removed.
func gmClearConditions(campaignID, targetID int, cond string) []string {
	var removed []string
	seen := map[string]bool{}
	for _, rec := range removeConditionsWhere(campaignID, targetID, func(r conditionRecord) bool {
		return ruleConditions[r.Condition] && (cond == "" || r.Condition == cond)
	}) {
		if !seen[rec.Condition] {
			seen[rec.Condition] = true
			removed = append(removed, rec.Condition)
		}
	}
	if targetID > 0 && cond == "" && exhaustionLevelOf(targetID) > 0 {
		setExhaustionLevel(targetID, 0)
		removed = append(removed, "exhaustion")
	}
	return removed
}

// gmConditionRequest: the body of POST/DELETE /api/gm/conditions, and of /api/characters/{id}/conditions (target_id from the path).
type gmConditionRequest struct {
	TargetID         int               `json:"target_id"`
	Condition        string            `json:"condition"`          // one of the 15 conditions, or exhaustion (DELETE: a condition's name, or all)
	Source           string            `json:"source"`             // what caused it (GM only)
	SourceID         int               `json:"source_id"`          // the creature in this campaign that caused it
	EndsWithGrapple  bool              `json:"ends_with_grapple"`  // a Restrained record that ends with this source's existing Grappled record
	Escapes          []conditionEscape `json:"escapes"`            // the ways to end it, each {ability, skill, dc, label, uses_action}
	EscapeDC         int               `json:"escape_dc"`          // Grappled: the escape DC (the Strength (Athletics) or Dexterity (Acrobatics) escapes are made for you)
	DurationRounds   int               `json:"duration_rounds"`    // it counts down at the end of the affected creature's turns and ends at 0
	Notes            string            `json:"notes"`              // notes (GM only)
	RecordID         int               `json:"record_id"`          // DELETE: the record to remove (its id from GET /api/conditions/active)
	EndSave          *conditionSave    `json:"end_save"`           // a saving throw repeated at the end (or start) of each of its turns: {ability, dc, when}; a success ends it
	FromMagicalSleep bool              `json:"from_magical_sleep"` // true: magic puts the creature to sleep (Fey Ancestry and the like make it immune)
	SpeedFt          *int              `json:"speed_ft"`           // condition speed_ruling: the Speed in feet when effects meet that the rules do not combine
	Level            *int              `json:"level"`              // exhaustion: the level to set (0-6)
	CasterID         int               `json:"caster_id"`          // a spell's state (blessed): who cast it, when more than one has it in play
	All              bool              `json:"all"`                // DELETE: every condition of the target
}

// handleGMConditions godoc
// handleGMConditions: POST /api/gm/conditions applies a condition (exhaustion: one level, or "level" sets it); DELETE removes one by
// record_id, or by target_id with a condition name, or every condition with "all" (F230).
// The words of these routes are written in tools/build_swagger.py; the request and the reply come from the code.
// @Router /gm/conditions [post]
// @Router /gm/conditions [delete]
func handleGMConditions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST to apply a condition, DELETE to remove one"})
		return
	}
	var req gmConditionRequest
	// a character target's campaign, which must be an active one the GM runs (gm_campaign_2024.go)
	agentID, campaignID, ok := gmRead(w, r, &req, targetsIn(&req.TargetID))
	if !ok {
		return
	}
	if slug, isSpell := spellConditionWords[strings.ToLower(strings.TrimSpace(req.Condition))]; isSpell && req.RecordID == 0 {
		// Blessed, Baned, Hasted ...: the state a spell sets; it lives with the spell (and ends with its Concentration)
		body, _ := json.Marshal(map[string]interface{}{"caster_id": req.CasterID, "spell": slug, "target_ids": []int{req.TargetID}, "campaign_id": campaignID})
		r2 := r.Clone(r.Context())
		r2.Body = io.NopCloser(bytes.NewReader(body))
		handleGMSpellEffect(w, r2)
		return
	}
	if r.Method == http.MethodDelete && req.RecordID == 0 && strings.EqualFold(strings.TrimSpace(req.Condition), "speed_ruling") {
		gone := removeConditionsWhere(campaignID, req.TargetID, func(r conditionRecord) bool { return r.Condition == "speed_ruling" })
		writeJSONStatus(w, 200, map[string]interface{}{"success": true, "target_id": req.TargetID, "removed": len(gone)})
		return
	}
	if r.Method == http.MethodDelete && req.RecordID == 0 { // by name: target_id with a condition, or "all"
		gmDeleteByName(w, campaignID, req.TargetID, req.Condition, req.All)
		return
	}
	if r.Method == http.MethodDelete {
		found := findConditionRecords(`id = $1 AND lobby_id = $2`, req.RecordID, campaignID)
		gone := endConditionRecords(found)
		if len(gone) == 0 {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "record_not_found", "message": "Give the record_id from the condition list"})
			return
		}
		writeJSONStatus(w, 200, map[string]interface{}{"success": true, "removed": gone[0].Condition, "target_id": gone[0].TargetID})
		return
	}
	if strings.EqualFold(strings.TrimSpace(req.Condition), "speed_ruling") { // the GM's Speed when effects meet that the rules do not combine (speed_owner_2024.go)
		if req.TargetID == 0 || (req.TargetID < 0 && !monsterInCombat(campaignID, req.TargetID)) || req.SpeedFt == nil || *req.SpeedFt < 0 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "speed_ruling_needs_target_and_speed_ft", "message": "Send target_id and speed_ft (0 or more); duration_rounds counts down at the end of its turns (omit: until you remove it)"})
			return
		}
		res := addCondition(campaignID, req.TargetID, condAdd{Condition: fmt.Sprintf("speed_ruling:%d", *req.SpeedFt), Source: "GM ruling", Rounds: req.DurationRounds, Replace: true, CreatedBy: agentID})
		if res.Err != nil {
			writeJSONStatus(w, 500, map[string]interface{}{"error": "could_not_record_condition"})
			return
		}
		writeJSONStatus(w, 200, map[string]interface{}{"success": true, "target_id": req.TargetID, "speed_ft": *req.SpeedFt, "duration_rounds": req.DurationRounds})
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
	if req.TargetID == 0 || (req.TargetID < 0 && !monsterInCombat(campaignID, req.TargetID)) {
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
	if req.SourceID != 0 {
		if len(targetIDsFrom(campaignID, "", []int{req.SourceID})) != 1 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "source_not_found", "message": "Give the ID of the source creature in this campaign."})
			return
		}
		if strings.TrimSpace(req.Source) == "" {
			req.Source = creatureName(campaignID, req.SourceID)
		}
	}
	if req.EndsWithGrapple {
		linked := false
		if cond == "restrained" && req.Source != "" && req.SourceID != 0 {
			for _, record := range loadConditionRecords(campaignID, req.TargetID) {
				if record.Condition == "grappled" && record.SourceID == req.SourceID && record.Source == req.Source {
					linked = true
					break
				}
			}
		}
		if !linked {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "grapple_source_required", "message": "Link Restrained to an existing Grappled record from the same source and creature."})
			return
		}
		req.Notes = notesEndsWithGrapple
	}
	res := addCondition(campaignID, req.TargetID, condAdd{Condition: cond, Source: req.Source, Escapes: escapes, Rounds: req.DurationRounds,
		EndSave: req.EndSave, Notes: req.Notes, CreatedBy: agentID, SourceID: req.SourceID, MagicalSleep: req.FromMagicalSleep,
		Magical: strings.Contains(strings.ToLower(req.Source), "spell") || strings.Contains(strings.ToLower(req.Source), "magic")}) // the GM names the source
	if res.Immune != "" {
		writeJSONStatus(w, 200, map[string]interface{}{"success": true, "immune": true, "immunity_source": res.Immune, "target_id": req.TargetID,
			"condition": cond, "message": fmt.Sprintf("%s makes %s immune to the %s condition: it doesn't take hold.", res.Immune,
				creatureName(campaignID, req.TargetID), titleCase(cond))})
		return
	}
	if res.Err != nil {
		writeJSONStatus(w, 500, map[string]interface{}{"error": "could_not_record_condition"})
		return
	}
	out := map[string]interface{}{}
	for k, v := range res.Notes { // what the condition set off: Concentration and Wild Shape end, grapples released, Prone ...
		out[k] = v
	}
	rec := conditionRecord{ID: res.ID, TargetID: req.TargetID, Condition: cond, Source: req.Source, Escapes: escapes, DurationRounds: req.DurationRounds, EndSave: req.EndSave, Notes: req.Notes}
	if res.Already {
		out["message"] = "The creature already has this condition from this source."
	}
	if req.TargetID > 0 {
		out["conditions"] = getCharConditions(req.TargetID)
	}
	out["success"] = true
	out["condition"] = conditionView(rec, 0, true)
	writeJSONStatus(w, 200, out)
}

// gmDeleteByName is DELETE /api/gm/conditions without a record_id: {"target_id": N, "condition": "poisoned"} ends that condition
// (every record of it), {"target_id": N, "condition": "exhaustion"} removes one level (with "level", sets it), {"target_id": N,
// "all": true} (or condition "all") ends every condition and Exhaustion.
// Its callers have checked a character target is in the campaign (gmReadAs).
func gmDeleteByName(w http.ResponseWriter, campaignID, targetID int, condition string, all bool) {
	if targetID == 0 {
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

// handleConditionsActive godoc
// handleConditionsActive: GET /api/conditions/active?character_id=N lists a character's conditions.
// The character's player gets the player view; the GM of the campaign gets the GM view.
// The words of these routes are written in tools/build_swagger.py; the request and the reply come from the code.
// @Router /conditions/active [get]
func handleConditionsActive(w http.ResponseWriter, r *http.Request) {
	charID, _ := strconv.Atoi(r.URL.Query().Get("character_id"))
	agentID, ok := requireOwnerOrGM(w, r, charID, "see its conditions")
	if !ok {
		return
	}
	gm := isCharacterGM(agentID, charID)
	var lobby int
	var species string
	db.QueryRow(`SELECT COALESCE(lobby_id, 0), COALESCE(species,'') FROM characters WHERE id = $1`, charID).Scan(&lobby, &species)
	speed := characterSpeed(charID, species)
	writeJSONStatus(w, 200, map[string]interface{}{"character_id": charID, "conditions": conditionsDetail(lobby, charID, speed, gm), "exhaustion_level": exhaustionLevelOf(charID)})
}

// handleConditionEscape godoc
// handleConditionEscape: POST /api/conditions/escape {character_id, record_id, escape} makes the check.
// The words of these routes are written in tools/build_swagger.py; the request and the reply come from the code.
// @Router /conditions/escape [post]
func handleConditionEscape(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	if _, err := getAgentFromAuth(r); err != nil { // signed in before the body is read
		w.Header().Set("Content-Type", "application/json")
		writeAuthError(w, err)
		return
	}
	var req struct {
		CharacterID int `json:"character_id"`
		RecordID    int `json:"record_id"` // the condition's record (its record_id from GET /api/conditions/active)
		Escape      int `json:"escape"`    // which way to end it: its index in ways_to_end
	}
	if !readRequest(w, r, &req) {
		return
	}
	if _, ok := requireCharacterOwner(w, r, req.CharacterID, "make its escape checks"); !ok {
		return
	}
	var lobby, level, str, dex, con, intl, wis, cha int
	var name string
	if db.QueryRow(`SELECT lobby_id, level, str, dex, con, intl, wis, cha, name FROM characters WHERE id = $1`,
		req.CharacterID).Scan(&lobby, &level, &str, &dex, &con, &intl, &wis, &cha, &name) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
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
	if esc.Skill != "" {
		if proficient, expert := characterSkillProficiency(req.CharacterID, esc.Skill); proficient { // the one proficiency reader
			mod += game.ProficiencyBonus(level)
			if expert {
				mod += game.ProficiencyBonus(level)
			}
		}
	}
	// What gives the check Advantage or Disadvantage has one owner (check_2024.go): Poisoned, Frightened, riders, Sunlight Sensitivity, the
	// holder's own words, Large Form, Powerful Build, Help
	why := checkCtx{lobby: lobby, who: req.CharacterID, ability: esc.Ability, skill: esc.Skill, escape: true, holder: rec.SourceID}.sources()
	disadv, adv := why.Disadvantage(), why.Advantage()
	roll, r1, r2, _ := rollCheckD20(adv, disadv)
	total := roll + mod
	success := total >= esc.DC
	resp := map[string]interface{}{"character": name, "condition": rec.Condition, "attempt": escapeLabel(esc), "roll": roll, "modifier": mod, "total": total, "success": success}
	if disadv && !adv {
		resp["rolls"] = []int{r1, r2}
		resp["disadvantage"] = true
	} else if adv && !disadv {
		resp["rolls"] = []int{r1, r2}
		resp["advantage"] = strings.Join(why.Adv, ", ")
	}
	if success {
		deleteConditionRecord(lobby, req.CharacterID, rec.ID)
		resp["message"] = fmt.Sprintf("%s total %d: success. The %s condition ends.", name, total, rec.Condition)
	} else {
		resp["message"] = fmt.Sprintf("%s total %d: failure. The %s condition continues.", name, total, rec.Condition)
	}
	writeAction(db, lobby, req.CharacterID, "escape_condition", name+" tries to end "+rec.Condition+": "+escapeLabel(esc), fmt.Sprint(resp["message"]))
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

// handleUniverseConditions godoc
// handleUniverseConditions: GET /api/universe/conditions returns the verbatim SRD text of each condition.
// The words of these routes are written in tools/build_swagger.py; the request and the reply come from the code.
// @Router /universe/conditions [get]
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
	// Then: the condition a failed repeated save turns the record into (Sleep: Unconscious), for ThenRounds; that one isn't repeated
	Then             string `json:"then,omitempty"`
	ThenRounds       int    `json:"then_rounds,omitempty"`
	ThenEndsOnDamage bool   `json:"then_ends_on_damage,omitempty"`
	ThenMagicalSleep bool   `json:"then_magical_sleep,omitempty"` // magic puts it to sleep: a creature that can't be is immune
	ThenRepeats      bool   `json:"then_repeats,omitempty"`       // the second condition keeps this repeated save (no further stage)
}

func endSaveJSON(s *conditionSave) interface{} {
	if s == nil {
		return nil
	}
	b, _ := json.Marshal(s)
	return string(b)
}

// handleCharacterConditions godoc
// @Summary Apply or remove a condition on a character (GM)
// @Description The same request as POST and DELETE /api/gm/conditions, with the character taken from the path (leave target_id out). Only the GM of the character's campaign; a player ends a condition with POST /api/conditions/escape.
// @Tags GM
// @Param id path int true "Character ID"
// @Body As POST /api/gm/conditions (apply) or DELETE /api/gm/conditions (remove: record_id, condition, or all)
// @Router /characters/{id}/conditions [post]
// @Router /characters/{id}/conditions [delete]
//
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
	// the same request as POST /api/gm/conditions, for this character (target_id is the path's). Only the GM of the
	// character's campaign may send it (gm_campaign_2024.go); a player ends a condition with POST /api/conditions/escape.
	var req gmConditionRequest
	lobby, ok := gmReadAs(w, r, agentID, &req, optionalBody(), campaignFromTarget(&charID),
		notGMHint("Only the GM of the character's campaign applies or removes conditions. To end one, use POST /api/conditions/escape (see GET /api/conditions/active)."))
	if !ok {
		return
	}
	if req.TargetID != 0 && req.TargetID != charID {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "id_mismatch",
			"message": fmt.Sprintf("target_id %d is not the character in the path (%d): leave target_id out. Nothing was changed.", req.TargetID, charID)})
		return
	}
	if r.Method == http.MethodDelete && req.RecordID == 0 {
		gmDeleteByName(w, lobby, charID, req.Condition, req.All) // F230: by name, exhaustion one level, or "all"
		return
	}
	req.TargetID = charID
	b, _ := json.Marshal(struct {
		gmConditionRequest
		CampaignID int `json:"campaign_id"`
	}{req, lobby})
	r2 := r.Clone(r.Context())
	r2.Body = io.NopCloser(bytes.NewReader(b))
	if r.Method != http.MethodDelete {
		r2.Method = http.MethodPost
	}
	handleGMConditions(w, r2)
}

// clearMonsterConditions drops the condition records of monsters (negative ids) when a fight starts or ends.
func clearMonsterConditions(campaignID int) {
	clearVexMarks(campaignID)
	clearCombatEffects(campaignID)
	clearSpellEffects(campaignID)
	clearMonsterStates(campaignID) // Legendary Action and Resistance uses, lair, auras (monster_features_2024.go)
	clearMonsterReactions(campaignID)
	clearBurning(campaignID, true) // F69: a monster's fire doesn't pass to the monster with its id in the next fight
	clearMonsterConditionRecords(campaignID)
	if db != nil {
		cancelCombatWork(campaignID)
		db.Exec(`DELETE FROM monster_concentration WHERE lobby_id = $1`, campaignID) // monster ids are not kept between fights (monster_concentration_2024.go)
	}
}
