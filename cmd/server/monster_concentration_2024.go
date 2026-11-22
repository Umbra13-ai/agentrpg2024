package main

// The lifecycle owner for monster Concentration. Cast identity, damage-time save
// outcomes, pending decisions, resistance spending and effect cleanup live here.
// SRD 5.2.1 p.179: another Concentration cast ends the old effect when casting
// starts; damage calls for a Constitution save; Incapacitated/dead ends it.
// Spell/condition consumers remain their existing owners. Cleanup calls the
// condition owner inside the lifecycle transaction, never a second transaction.
import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

type monsterConcentration struct {
	Spell, Pending, Queue, CastToken, DecisionToken, PendingSave, WorkID string
}

func readMonsterConcentration(q rowQuerier, lobby, id int) (monsterConcentration, error) {
	var s monsterConcentration
	err := q.QueryRow(`SELECT spell,pending,queue,cast_token,decision_token,pending_save,work_id FROM monster_concentration WHERE lobby_id=$1 AND monster_id=$2`, lobby, id).Scan(&s.Spell, &s.Pending, &s.Queue, &s.CastToken, &s.DecisionToken, &s.PendingSave, &s.WorkID)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	return s, err
}
func monsterConcentrationOn(lobby, id int) string {
	if db == nil {
		return ""
	}
	s, err := readMonsterConcentration(db, lobby, id)
	if err != nil {
		log.Printf("read monster concentration: %v", err)
	}
	return s.Spell
}
func monsterConcentrationPending(lobby, id int) string {
	if db == nil {
		return ""
	}
	s, err := readMonsterConcentration(db, lobby, id)
	if err != nil {
		log.Printf("read monster concentration decision: %v", err)
	}
	return s.Pending
}
func newConcentrationToken() (string, error) {
	var b [16]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}

// The advisory lock also serializes first insert/recast when no row exists.
// The one-bigint namespace is distinct from condition-owner pair locks.
func changeMonsterConcentration(lobby, id int, change func(*sql.Tx, *monsterConcentration) error) error {
	return inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("monster-concentration:%d:%d", lobby, id)); err != nil {
			return err
		}
		s, err := readMonsterConcentration(tx, lobby, id)
		if err != nil {
			return err
		}
		return change(tx, &s)
	})
}
func saveMonsterConcentrationIn(tx *sql.Tx, lobby, id int, s monsterConcentration) error {
	if s.PendingSave == "" {
		s.PendingSave = "null"
	}
	_, err := tx.Exec(`INSERT INTO monster_concentration(lobby_id,monster_id,spell,pending,queue,cast_token,decision_token,pending_save,work_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9)
 ON CONFLICT(lobby_id,monster_id) DO UPDATE SET spell=EXCLUDED.spell,pending=EXCLUDED.pending,queue=EXCLUDED.queue,cast_token=EXCLUDED.cast_token,decision_token=EXCLUDED.decision_token,pending_save=EXCLUDED.pending_save,work_id=EXCLUDED.work_id`, lobby, id, s.Spell, s.Pending, s.Queue, s.CastToken, s.DecisionToken, s.PendingSave, s.WorkID)
	return err
}
func concentrationFailure(lobby int, name string, err error) {
	markConcentrationJournalFault(lobby)
	if !requestHasDBFailure() {
		noteDBError(err, "monster Concentration state")
	}
	log.Printf("monster Concentration for campaign %d: %v", lobby, err)
	addGMNotice(lobby, name+"'s Concentration could not be updated. Resolve the pending game state before continuing; check the server log.")
}

// endMonsterConcentrationIn removes only this caster's old spell effects, with
// character condition lists synchronized by their existing owner in the same tx.
func endMonsterConcentrationIn(tx *sql.Tx, lobby, id int, s monsterConcentration) (int, error) {
	if s.Spell == "" {
		return 0, nil
	}
	if err := readyCombatWorkIn(tx, lobby, s.WorkID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM monster_concentration WHERE lobby_id=$1 AND monster_id=$2`, lobby, id); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM combat_effects WHERE lobby_id=$1 AND source_id=$2 AND (kind='magical_darkness'
		OR (kind='obscured_area' AND text::jsonb->>'concentration' = 'true'))`, lobby, id); err != nil { // its areas (obscured_areas_2024.go)
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM spell_effects WHERE lobby_id=$1 AND caster_id=$2 AND LOWER(spell)=LOWER($3)`, lobby, id, s.Spell); err != nil {
		return 0, err
	}
	rows, err := tx.Query(`SELECT DISTINCT target_id FROM condition_records WHERE lobby_id=$1 AND source_id=$2 ORDER BY target_id`, lobby, id)
	if err != nil {
		return 0, err
	}
	var targets []int
	for rows.Next() {
		var tid int
		if err = rows.Scan(&tid); err != nil {
			rows.Close()
			return 0, err
		}
		targets = append(targets, tid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	ended := 0
	for _, tid := range targets {
		err = changeConditionsIn(tx, lobby, tid, func(c *conditionChange) error {
			gone, err := c.remove(func(r conditionRecord) bool {
				return r.SourceID == id && (strings.HasSuffix(r.Source, "("+s.Spell+")") || strings.EqualFold(r.Source, s.Spell))
			})
			ended += len(gone)
			return err
		})
		if err != nil {
			return 0, err
		}
	}
	return ended, nil
}
func concentrationEndNotice(name, spell, why string, ended int) string {
	return fmt.Sprintf("%s's Concentration on %s ended (%s). The server ended %d condition(s) that spell put on others. Anything else it did that the server did not record is yours to end.", name, spell, why, ended)
}

// Called BEFORE the new spell's effects are resolved, including same-name casts.
func startMonsterConcentration(lobby, id int, spell string) error {
	if db == nil || lobby == 0 || id >= 0 {
		return nil
	}
	token, err := newConcentrationToken()
	if err != nil {
		return err
	}
	name := creatureName(lobby, id)
	notice := ""
	err = changeMonsterConcentration(lobby, id, func(tx *sql.Tx, s *monsterConcentration) error {
		if s.Spell != "" {
			n, err := endMonsterConcentrationIn(tx, lobby, id, *s)
			if err != nil {
				return err
			}
			notice = concentrationEndNotice(name, s.Spell, "starts concentrating on "+spell, n)
		}
		return saveMonsterConcentrationIn(tx, lobby, id, monsterConcentration{Spell: spell, CastToken: token})
	})
	if err != nil {
		concentrationFailure(lobby, name, err)
		return err
	}
	if notice != "" {
		addGMNotice(lobby, notice)
	}
	return nil
}
func endMonsterConcentration(lobby, id int, name, why string) {
	notice := ""
	err := changeMonsterConcentration(lobby, id, func(tx *sql.Tx, s *monsterConcentration) error {
		if s.Spell == "" {
			return nil
		}
		n, err := endMonsterConcentrationIn(tx, lobby, id, *s)
		if err != nil {
			return err
		}
		notice = concentrationEndNotice(name, s.Spell, why, n)
		return nil
	})
	if err != nil {
		concentrationFailure(lobby, name, err)
		return
	}
	if notice != "" {
		addGMNotice(lobby, notice)
		resolveGMItemsEnding(lobby, endConcentration, id, "") // the open items that waited on it (gm_open_items_2024.go)
	}
}

// The complete rolled saving throw retains gathered modifiers, Advantage and
// Disadvantage, dice and outcome. Legacy DC-only queues cannot recover that state:
// they require explicit GM adjudication, rather than a roll using later state.
func concentrationRolls(raw string) ([]game.SaveResult, error) {
	if raw == "" {
		return nil, nil
	}
	var out []game.SaveResult
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return nil, fmt.Errorf("legacy or malformed Concentration queue requires GM adjudication")
	}
	for _, sv := range out {
		expected := sv.Save.Judge(sv.Natural)
		if sv.Save.Ability != "con" || sv.Save.DC < 10 || sv.Save.DC > 30 || (sv.Rolled() && (sv.Natural < 1 || sv.Natural > 20)) || sv.Total != expected.Total || sv.Success != expected.Success {
			return nil, fmt.Errorf("invalid saved Concentration outcome requires GM adjudication")
		}
	}
	return out, nil
}
func setConcentrationRolls(s *monsterConcentration, rolls []game.SaveResult) error {
	s.Queue = ""
	if len(rolls) == 0 {
		return nil
	}
	b, err := json.Marshal(rolls)
	s.Queue = string(b)
	return err
}

func settleConcentrationIn(tx *sql.Tx, lobby, id int, name string, s *monsterConcentration, sv game.SaveResult, meta *monsterMeta) (string, error) {
	if sv.Success {
		return fmt.Sprintf("%s keeps Concentration on %s: %s.", name, s.Spell, sv.Line(name)), nil
	}
	left, err := monsterResistanceIn(tx, lobby, id, meta, false)
	if err != nil {
		return "", err
	}
	if left > 0 {
		token, err := newConcentrationToken()
		if err != nil {
			return "", err
		}
		s.Pending = fmt.Sprintf("%s; %d Legendary Resistance use(s) left", sv.Clause(), left)
		s.DecisionToken = token
		saved, err := json.Marshal(sv)
		if err != nil {
			return "", err
		}
		s.PendingSave = string(saved)
		work, err := captureCombatWorkIn(tx, lobby)
		if err != nil {
			return "", err
		}
		if work != "" {
			s.WorkID = work
		}
		notice := fmt.Sprintf("%s %s. Its Concentration on %s and what that spell put on others are KEPT until you decide: POST /api/gm/monster-concentration with decision \"legendary_resistance\" or \"end\", and decision_token \"%s\". Resolve this before dependent combat progression.", name, s.Pending, s.Spell, token)
		if s.WorkID != "" {
			notice += fmt.Sprintf(" After adjudication, resume saved work %s with POST /api/gm/combat-resume and resume_id.", s.WorkID)
		}
		return notice, nil
	}
	n, err := endMonsterConcentrationIn(tx, lobby, id, *s)
	if err != nil {
		return "", err
	}
	note := concentrationEndNotice(name, s.Spell, sv.Clause()+", with no Legendary Resistance left", n)
	*s = monsterConcentration{}
	return note, nil
}
func monsterConcentrationDamage(lobby, id int, name string, dealt int) bool {
	before, err := readMonsterConcentration(db, lobby, id)
	if err != nil {
		concentrationFailure(lobby, name, err)
		return true
	}
	if before.Spell == "" {
		return false
	}
	if monsterIncapacitated(lobby, id) {
		endMonsterConcentration(lobby, id, name, "it is Incapacitated")
		return true
	}
	if _, err = concentrationRolls(before.Queue); err != nil {
		markConcentrationJournalFault(lobby)
		addGMNotice(lobby, name+": "+err.Error()+"; no further save was rolled.")
		return true
	}
	// The shared owner must know this is a Concentration save. In particular,
	// Concentration-only Disadvantage is distinct from an ordinary Constitution save.
	sv := (&saveCtx{lobby: lobby, saver: id, ability: "con", dc: game.ConcentrationDC(dealt), concentration: true}).roll()
	if err := rememberJournalConcentrationSave(lobby, sv); err != nil {
		concentrationFailure(lobby, name, err)
		return true
	}
	_, meta, _ := monsterCombatant(lobby, id)
	notice := ""
	err = changeMonsterConcentration(lobby, id, func(tx *sql.Tx, s *monsterConcentration) error {
		if s.Spell == "" || s.CastToken != before.CastToken {
			return nil
		} // damage belonged to the previous cast
		if s.Pending != "" {
			rolls, err := concentrationRolls(s.Queue)
			if err != nil {
				return err
			}
			if err = setConcentrationRolls(s, append(rolls, sv)); err != nil {
				return err
			}
			notice = fmt.Sprintf("%s took more damage while the decision on its Concentration on %s is pending. The Constitution save was rolled now: %s. Its outcome is stored until you decide.", name, s.Spell, sv.Line(name))
		} else {
			var err error
			notice, err = settleConcentrationIn(tx, lobby, id, name, s, sv, meta)
			if err != nil {
				return err
			}
		}
		if s.Spell == "" {
			return nil
		}
		return saveMonsterConcentrationIn(tx, lobby, id, *s)
	})
	if err != nil {
		concentrationFailure(lobby, name, err)
		return true
	}
	if notice != "" {
		addGMNotice(lobby, notice)
	}
	return true
}
func legendaryResistanceLeft(lobby, id int) int {
	_, meta, ok := monsterCombatant(lobby, id)
	if !ok || meta == nil || meta.LR == 0 {
		return 0
	}
	s := loadMonsterState(lobby, id)
	_, total := legendaryUseTotals(meta, s)
	return max(0, total-s.LRUsed)
}

// The observed token is checked under the lifecycle lock. A concurrent request
// cannot answer the next queued failure by accidentally reusing the first decision.
func resolveMonsterConcentration(lobby, id int, decision string, tokens ...string) (msg, code string) {
	if decision != "end" && decision != "legendary_resistance" {
		return "decision must be \"end\" or \"legendary_resistance\".", "bad_decision"
	}
	seen, err := readMonsterConcentration(db, lobby, id)
	if err != nil {
		return "Could not read the Concentration decision.", dbErrorCode
	}
	if seen.Spell == "" || seen.Pending == "" {
		return "No decision is pending for this monster's Concentration.", "nothing_pending"
	}
	token := ""
	if len(tokens) > 0 {
		token = tokens[0]
	}
	if seen.DecisionToken != "" && token == "" {
		return "Send decision_token from this failure's private GM notice. Nothing was spent.", "decision_token_required"
	}
	name := creatureName(lobby, id)
	_, meta, _ := monsterCombatant(lobby, id)
	if decision == "legendary_resistance" && meta == nil {
		return "The monster's stat block is unavailable. End or adjudicate the pending cast; nothing was spent.", "combatant_not_found"
	}
	var notices []string
	err = changeMonsterConcentration(lobby, id, func(tx *sql.Tx, s *monsterConcentration) error {
		if s.Spell == "" || s.Pending == "" || s.CastToken != seen.CastToken || s.DecisionToken != token {
			code = "nothing_pending"
			msg = "That Concentration decision is no longer pending."
			return nil
		}
		if decision == "end" {
			n, err := endMonsterConcentrationIn(tx, lobby, id, *s)
			if err != nil {
				return err
			}
			notices = append(notices, concentrationEndNotice(name, s.Spell, "the GM let the failed save stand: "+s.Pending, n))
			msg = name + "'s Concentration on " + s.Spell + " ended."
			return nil
		}
		rolls, err := concentrationRolls(s.Queue)
		if err != nil {
			code = "queue_needs_adjudication"
			msg = "The stored queue predates save-time outcomes or is malformed. Resolve it with the GM; decision end remains available. Nothing was spent."
			return nil
		}
		left, err := monsterResistanceIn(tx, lobby, id, meta, false)
		if err != nil {
			return err
		}
		if left == 0 {
			code = "no_uses_left"
			msg = "No Legendary Resistance use is left."
			return nil
		}
		left, err = monsterResistanceIn(tx, lobby, id, meta, true)
		if err != nil {
			return err
		}
		s.Pending, s.DecisionToken, s.PendingSave = "", "", ""
		msg = fmt.Sprintf("%s used Legendary Resistance and keeps Concentration on %s (%d uses left).", name, s.Spell, left)
		if err = writeAction(tx, lobby, 0, "legendary_resistance", name+" chooses to succeed instead (Legendary Resistance).", fmt.Sprintf("%d uses left.", left)); err != nil {
			return err
		}
		for len(rolls) > 0 {
			sv := rolls[0]
			rolls = rolls[1:]
			note, err := settleConcentrationIn(tx, lobby, id, name, s, sv, meta)
			if err != nil {
				return err
			}
			notices = append(notices, note)
			if s.Spell == "" {
				return nil
			}
			if s.Pending != "" {
				break
			}
		}
		if s.Pending == "" {
			if err := readyCombatWorkIn(tx, lobby, s.WorkID); err != nil {
				return err
			}
		}
		if err = setConcentrationRolls(s, rolls); err != nil {
			return err
		}
		return saveMonsterConcentrationIn(tx, lobby, id, *s)
	})
	if err != nil {
		log.Printf("monster Concentration decision: %v", err)
		return "The Concentration decision was not saved. Nothing in this decision was committed.", dbErrorCode
	}
	for _, note := range notices {
		addGMNotice(lobby, note)
	}
	return msg, code
}

// endObservedMonsterCast permits voluntary ending under SRD 5.2.1 p.179.
// It also lets the GM settle a tracked cast whose damage write failed before a
// pending decision existed. The cast identity is checked under its owner lock.
func endObservedMonsterCast(lobby, id int, token string) (string, error) {
	if token == "" {
		return "cast_token_required", nil
	}
	code := ""
	err := changeMonsterConcentration(lobby, id, func(tx *sql.Tx, s *monsterConcentration) error {
		if s.Spell == "" || s.CastToken != token {
			code = "stale_cast_token"
			return nil
		}
		_, err := endMonsterConcentrationIn(tx, lobby, id, *s)
		return err
	})
	if err == nil && code == "" {
		addGMNotice(lobby, "The GM ended the observed monster Concentration cast. Its recorded effects were removed; no save was rerolled.")
	}
	return code, err
}

// handleGMMonsterConcentration godoc
// @Summary Inspect or settle a monster's Concentration
// @Description GET privately lists active casts, cast_token and pending decisions for this campaign. POST legendary_resistance or end answers a pending failed save using decision_token. POST end_cast voluntarily ends the observed cast using cast_token, including recovery when a write failed before a pending decision was stored. Cleanup is atomic; no stored save is rerolled.
// @Tags GM
// @Body combatant_id, decision, decision_token or cast_token
// @Router /gm/monster-concentration [get]
// @Router /gm/monster-concentration [post]
func handleGMMonsterConcentration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		writeJSONStatus(w, 405, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	var req struct {
		CombatantID   int    `json:"combatant_id"`   // the concentrating monster's negative combat id
		Decision      string `json:"decision"`       // legendary_resistance, end (pending save), or end_cast (observed active cast)
		DecisionToken string `json:"decision_token"` // pending failure identity from its private notice or GET
		CastToken     string `json:"cast_token"`     // required for end_cast: the observed cast identity from GET
	}
	_, lobby, ok := gmRead(w, r, &req, optionalBody())
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		rows, err := db.Query(`SELECT monster_id,spell,cast_token,pending,decision_token,pending_save,work_id FROM monster_concentration WHERE lobby_id=$1 ORDER BY monster_id`, lobby)
		if err != nil {
			writeDBError(w, err, "monster Concentration")
			return
		}
		defer rows.Close()
		list := []map[string]interface{}{}
		for rows.Next() {
			var id int
			var spell, cast, pending, decision, work string
			var save json.RawMessage
			if err = rows.Scan(&id, &spell, &cast, &pending, &decision, &save, &work); err != nil {
				writeDBError(w, err, "monster Concentration")
				return
			}
			list = append(list, map[string]interface{}{"combatant_id": id, "spell": spell, "cast_token": cast, "pending": pending, "decision_token": decision, "pending_save": save, "resume_id": work})
		}
		if err = rows.Err(); err != nil {
			writeDBError(w, err, "monster Concentration")
			return
		}
		writeJSONStatus(w, 200, map[string]interface{}{"casts": list})
		return
	}
	if req.Decision == "end_cast" {
		code, err := endObservedMonsterCast(lobby, req.CombatantID, req.CastToken)
		if err != nil {
			writeDBWriteError(w, err, "ending the observed Concentration cast")
			return
		}
		if code != "" {
			writeJSONStatus(w, 409, map[string]interface{}{"error": code, "message": "Read the current cast and use its cast_token; no cast was changed."})
			return
		}
		writeJSONStatus(w, 200, map[string]interface{}{"success": true})
		return
	}
	msg, code := resolveMonsterConcentration(lobby, req.CombatantID, req.Decision, req.DecisionToken)
	if code == dbErrorCode {
		writeDBWriteError(w, nil, "the Concentration decision")
		return
	}
	if code != "" {
		writeJSONStatus(w, 400, map[string]interface{}{"error": code, "message": msg})
		return
	}
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "message": msg})
}

func monsterConcentrationEnds(lobby, id int, name, how string) bool {
	if monsterConcentrationOn(lobby, id) == "" {
		return false
	}
	endMonsterConcentration(lobby, id, name, how+": Concentration ends with no saving throw")
	return true
}
