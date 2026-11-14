package main

// Pending-action combat turns (Nov 14, Matt's design "Pending-Action Combat Turns"). Players no longer have to be awake at the
// moment their slot comes up: during a fight a player submits the whole turn whenever they are there, the server holds it, and the
// turns resolve in initiative order once every earlier slot is done.
//
//	submit    POST /api/pending-turn {"steps": [...], "fallbacks": [[...], ...]}: each step is a POST /api/action body (or
//	          {"action": "map_move", "path": [...]} on a battle map). Checked at once (the action is one the character can take,
//	          the spell is prepared and a slot is left, the target exists, the range fits the map); nothing is rolled or spent.
//	          Sending again replaces it; DELETE withdraws it; GET shows the table's queue (pending turns are open to the table).
//	resolve   when the GM checks in (GET /api/gm/status, a turn the GM advances, POST .../combat/resolve) the server walks the
//	          initiative order from the current slot: a player's slot with a pending turn is checked again against the state as
//	          it is now and resolved (the steps run as the player's own actions, then the turn ends); the walk stops at the first
//	          slot it can't resolve: a monster or NPC (the GM's to play), a player with no pending turn, or a turn that no longer
//	          works and has no working fallback (paused for the GM: needs_gm).
//	fallbacks tried in order when the main turn no longer works. None works: the slot is paused for the GM, nothing is substituted.
//	deadline  the campaign's combat_turn_timeout table rule (4h by default). After it the slot is not skipped any more: the GM
//	          takes the turn as stand-in (POST .../combat/stand-in), from the character's established intent.
//	reactions unchanged: standing reaction orders (reaction_orders_2024.go) fire as before.
//
// A pending turn is kept per character, fight and round (pending_turns; cleared when a fight starts).

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"time"
)

var pendingTurnsOnce sync.Once

func ensurePendingTurns() {
	pendingTurnsOnce.Do(func() {
		db.Exec(`CREATE TABLE IF NOT EXISTS pending_turns (
			id SERIAL PRIMARY KEY,
			lobby_id INTEGER NOT NULL,
			character_id INTEGER NOT NULL,
			round INTEGER NOT NULL,
			steps JSONB NOT NULL,
			fallbacks JSONB NOT NULL DEFAULT '[]',
			status TEXT NOT NULL DEFAULT 'pending',
			note TEXT NOT NULL DEFAULT '',
			result JSONB,
			submitted_at TIMESTAMP NOT NULL DEFAULT NOW(),
			resolved_at TIMESTAMP)`)
		db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS pending_turns_open ON pending_turns (lobby_id, character_id, round) WHERE status IN ('pending', 'needs_gm')`)
	})
}

// ---------------------------------------------------------------- the fight as the walk sees it

type ptEntry struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Initiative int    `json:"initiative"`
	IsMonster  bool   `json:"is_monster"`
}

type ptCombat struct {
	Round, Index int
	Entries      []ptEntry
	StartedAt    sql.NullTime
}

// ptLoadCombat: the active fight of a campaign; ok is false when there is none (or its turn order is broken).
func ptLoadCombat(lobbyID int) (ptCombat, bool) {
	var c ptCombat
	var raw []byte
	var active bool
	if db.QueryRow(`SELECT round_number, current_turn_index, turn_order, active, turn_started_at FROM combat_state WHERE lobby_id = $1`, lobbyID).
		Scan(&c.Round, &c.Index, &raw, &active, &c.StartedAt) != nil || !active {
		return c, false
	}
	json.Unmarshal(raw, &c.Entries)
	if len(c.Entries) == 0 || c.Index < 0 || c.Index >= len(c.Entries) {
		return c, false
	}
	return c, true
}

// slotOf: the character's place in the initiative order (-1: not in this fight).
func (c ptCombat) slotOf(charID int) int {
	for i, e := range c.Entries {
		if e.ID == charID && !e.IsMonster {
			return i
		}
	}
	return -1
}

// roundFor: the round a turn submitted now is for: this round while the character's slot is still to come (or is the current
// one), the next round once it has passed.
func (c ptCombat) roundFor(slot int) int {
	if slot < c.Index {
		return c.Round + 1
	}
	return c.Round
}

// ptOwner: the player who owns the character in a slot; ok is false for a monster or NPC (the GM's slot).
func ptOwner(e ptEntry) (int, bool) {
	if e.IsMonster || e.ID <= 0 {
		return 0, false
	}
	var owner int
	if db.QueryRow(`SELECT COALESCE(agent_id, 0) FROM characters WHERE id = $1`, e.ID).Scan(&owner) != nil || owner == 0 {
		return 0, false
	}
	return owner, true
}

// ptDeadline: when the missing-player deadline of the current slot passes (the combat_turn_timeout table rule); ok is false when
// the rule is off.
func ptDeadline(lobbyID int, c ptCombat) (time.Time, bool) {
	limit, on := timeoutRule(lobbyID, "combat_turn_timeout", 4*time.Hour)
	if !on || !c.StartedAt.Valid {
		return time.Time{}, false
	}
	return c.StartedAt.Time.Add(limit), true
}

// ---------------------------------------------------------------- stored turns

type ptRow struct {
	ID, CharacterID, Round int
	Steps                  []map[string]interface{}
	Fallbacks              [][]map[string]interface{}
	Status, Note           string
	Result                 json.RawMessage
	SubmittedAt            time.Time
}

// ptOpenRow: the character's turn for the round that is still open (pending, or paused for the GM).
func ptOpenRow(lobbyID, charID, round int) (ptRow, bool) {
	var r ptRow
	var steps, fallbacks []byte
	var result []byte
	if db.QueryRow(`SELECT id, character_id, round, steps, fallbacks, status, note, COALESCE(result, 'null'), submitted_at FROM pending_turns
		WHERE lobby_id = $1 AND character_id = $2 AND round = $3 AND status IN ('pending', 'needs_gm')`, lobbyID, charID, round).
		Scan(&r.ID, &r.CharacterID, &r.Round, &steps, &fallbacks, &r.Status, &r.Note, &result, &r.SubmittedAt) != nil {
		return r, false
	}
	json.Unmarshal(steps, &r.Steps)
	json.Unmarshal(fallbacks, &r.Fallbacks)
	r.Result = result
	return r, true
}

func (r ptRow) view() map[string]interface{} {
	v := map[string]interface{}{"character_id": r.CharacterID, "round": r.Round, "steps": r.Steps, "status": r.Status,
		"submitted_at": r.SubmittedAt.UTC().Format(time.RFC3339)}
	if len(r.Fallbacks) > 0 {
		v["fallbacks"] = r.Fallbacks
	}
	if r.Note != "" {
		v["note"] = r.Note
	}
	return v
}

// closePendingTurns: a turn ended without its pending turn being resolved by the walk (the GM advanced or skipped it, or took it
// as stand-in): the open turn of that character and round is closed.
func closePendingTurns(lobbyID, charID, round int, how string) {
	if db == nil {
		return
	}
	ensurePendingTurns()
	db.Exec(`UPDATE pending_turns SET status = 'closed', note = $4, resolved_at = NOW()
		WHERE lobby_id = $1 AND character_id = $2 AND round <= $3 AND status IN ('pending', 'needs_gm')`, lobbyID, charID, round, how)
}

// clearPendingTurns: a fight starts or ends: its pending turns go.
func clearPendingTurns(lobbyID int) {
	if db == nil {
		return
	}
	ensurePendingTurns()
	db.Exec(`DELETE FROM pending_turns WHERE lobby_id = $1`, lobbyID)
}

// ---------------------------------------------------------------- checking a turn

type ptStep struct {
	Action       string      `json:"action"`
	Description  string      `json:"description"`
	Result       string      `json:"result"`
	SpellSlug    string      `json:"spell_slug"`
	Target       string      `json:"target"`
	TargetID     int         `json:"target_id"`
	TargetIDs    []int       `json:"target_ids"`
	SlotLevel    int         `json:"slot_level"`
	MovementCost int         `json:"movement_cost"`
	CompanionID  int         `json:"companion_id"`
	Item         string      `json:"item"`
	Charges      int         `json:"charges"`
	Path         []mapSquare `json:"path"`
	Altitude     *int        `json:"altitude"`
}

// ptStepFields: what a step can hold: the fields of POST /api/action, and a map_move's path and altitude.
var ptStepFields = map[string]bool{"action": true, "description": true, "result": true, "spell_slug": true, "target": true,
	"target_character_id": true, "movement_cost": true, "toward_frightened_source": true, "close_range": true, "slot_level": true,
	"object_id": true, "cleave_target_id": true, "target_ids": true, "item": true, "charges": true, "target_id": true,
	"companion_id": true, "features": true, "path": true, "altitude": true}

const ptMaxSteps, ptMaxFallbacks = 8, 3

func ptDecodeStep(m map[string]interface{}) (ptStep, error) {
	var s ptStep
	raw, _ := json.Marshal(m)
	err := json.Unmarshal(raw, &s)
	s.Action = canonicalAction(s.Action)
	return s, err
}

// ptMapToken: the fight has a battle map with this character's token on it.
func ptMapToken(lobbyID, charID int) bool {
	var raw []byte
	var m encounterMap
	if db.QueryRow(`SELECT battle_map FROM combat_state WHERE lobby_id = $1 AND active`, lobbyID).Scan(&raw) != nil || len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return false
	}
	for _, t := range m.Tokens {
		if t.ID == charID {
			return true
		}
	}
	return false
}

// ptSlotProblem: a levelled spell needs a free slot of its level, unless it is cast another way (an item, a free cast, an origin
// spell, a Warlock's Mystic Arcanum, a Wizard's Spell Mastery or Signature Spells, which the cast itself checks).
func ptSlotProblem(charID int, sp SRDSpell, slug string, s ptStep) string {
	need := sp.Level
	if s.SlotLevel > need {
		need = s.SlotLevel
	}
	if need < 1 || s.Item != "" || s.Charges > 0 || hasWord(strings.ToLower(s.Description), "free") || slotFreeAtOrAbove(charID, need) {
		return ""
	}
	if _, origin := originSpellsOf(charID)[slug]; origin {
		return ""
	}
	var class string
	var level int
	db.QueryRow(`SELECT LOWER(COALESCE(class,'')), COALESCE(level,1) FROM characters WHERE id = $1`, charID).Scan(&class, &level)
	if (class == "warlock" && level >= 11) || (class == "wizard" && level >= 18) || class == "warlock" && len(invocationSpellsView(charID)) > 0 {
		return ""
	}
	return fmt.Sprintf("%s needs a spell slot of level %d or higher, and you have none left", sp.Name, need)
}

// validatePendingSteps checks one attempt (the main turn or a fallback) against the state as it is now: what can be known without
// rolling or spending anything. strict is false for a fallback at submission: it is meant for a changed situation, so its range is
// only a warning then. Problems refuse the turn; warnings are told and the turn is kept.
func validatePendingSteps(lobbyID, charID int, steps []map[string]interface{}, strict bool) (problems, warnings []string) {
	if len(steps) == 0 {
		return []string{"a turn needs at least one step (an action body, like {\"action\": \"attack\", \"target_id\": -1})"}, nil
	}
	if len(steps) > ptMaxSteps {
		return []string{fmt.Sprintf("a turn has at most %d steps (you sent %d)", ptMaxSteps, len(steps))}, nil
	}
	hasMap := ptMapToken(lobbyID, charID)
	moved := false // a step that moves the character came earlier: a later range can't be judged from where it stands now
	uses := map[string][]string{}
	for i, m := range steps {
		at := fmt.Sprintf("step %d", i+1)
		var unknown []string
		for k := range m {
			if !ptStepFields[k] {
				unknown = append(unknown, k)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			problems = append(problems, fmt.Sprintf("%s: %s is not a field of a step (a step is a POST /api/action body; character_id and campaign_id go at the top of the request)", at, strings.Join(unknown, ", ")))
			continue
		}
		s, err := ptDecodeStep(m)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: a field has the wrong type (%s)", at, publicError(err)))
			continue
		}
		if s.Action == "" {
			problems = append(problems, at+": action is missing (attack, cast, dodge, dash, move, map_move ...)")
			continue
		}
		at += " (" + s.Action + ")"
		if s.Action == "map_move" {
			if !hasMap {
				problems = append(problems, at+": this fight has no battle map with your token on it; use {\"action\": \"move\", \"movement_cost\": feet} instead")
			} else if (len(s.Path) == 0 && s.Altitude == nil) || len(s.Path) > 120 {
				problems = append(problems, at+": send path, the squares you move through in order ([{\"x\": 3, \"y\": 4}, ...], 1 to 120 of them)")
			}
			moved = true
			continue
		}
		kind := getActionResourceType(s.Action)
		if kind == "reaction" {
			problems = append(problems, at+": this is a Reaction, taken on another creature's turn, so it isn't part of your turn. Standing reaction orders (Shield, Opportunity Attack ...) are set with POST /api/characters/reaction-orders")
			continue
		}
		if kind == "action" || kind == "bonus_action" {
			uses[kind] = append(uses[kind], s.Action)
		}
		if kind == "movement" {
			moved = true
		}
		if s.MovementCost < 0 || s.MovementCost > 10000 {
			problems = append(problems, at+": movement_cost is the feet of movement you use, 0 or more")
		}
		if actionRequiresNarrative(s.Action) && strings.TrimSpace(actionNarrative(s.Description, s.Result)) == "" {
			problems = append(problems, at+": an \"other\" action needs a description")
		}
		if slug := normalizeSpellSlug(strings.ReplaceAll(s.Action, "_", "-")); s.Action != "cast" && kind == "action" && !knownActionWord(s.Action) {
			if sp, isSpell := srdSpellsMemory[slug]; isSpell {
				problems = append(problems, fmt.Sprintf("%s: %s is a spell: cast it with {\"action\": \"cast\", \"spell_slug\": \"%s\"}", at, sp.Name, slug))
				continue
			}
		}
		if s.CompanionID != 0 {
			continue // a companion's step is checked when it runs
		}
		targetID := s.TargetID
		if t := strings.TrimSpace(s.Target); t != "" && targetID == 0 && len(s.TargetIDs) == 0 {
			id, names := resolveTargetName(lobbyID, charID, t)
			if id == 0 {
				problems = append(problems, fmt.Sprintf("%s: no creature called %q in this fight (%s)", at, t, strings.Join(names, ", ")))
				continue
			}
			targetID = id
		}
		rangeNote := func(why string) {
			if why == "" {
				return
			}
			if strict && !moved {
				problems = append(problems, at+": "+why)
			} else {
				warnings = append(warnings, at+": "+why+" (as things stand now; it is checked again when the turn resolves)")
			}
		}
		switch s.Action {
		case "attack", "offhand_attack", "frenzy_attack":
			if targetID != 0 {
				if ok, why := validateAttackTarget(lobbyID, targetID); !ok {
					problems = append(problems, at+": "+why)
					continue
				}
				rangeNote(attackRangeProblem(lobbyID, charID, targetID, s.Description))
			}
		case "cast":
			slug, candidates, selErr := resolveCastSpellSlug(s.SpellSlug, actionNarrative(s.Description, s.Result))
			if selErr != "" {
				msg := "name the spell with spell_slug (an exact slug from GET /api/universe/spells)"
				if len(candidates) > 0 {
					msg += ": " + strings.Join(candidates, ", ") + "?"
				}
				problems = append(problems, at+": "+msg)
				continue
			}
			sp := srdSpellsMemory[normalizeSpellSlug(slug)]
			if ok, why := spellAvailable(charID, slug, s.Description); !ok {
				problems = append(problems, at+": "+why)
				continue
			}
			if why := ptSlotProblem(charID, sp, normalizeSpellSlug(slug), s); why != "" {
				problems = append(problems, at+": "+why)
				continue
			}
			targets := append([]int{}, s.TargetIDs...)
			if targetID != 0 {
				targets = append(targets, targetID)
			}
			if len(targets) > 0 {
				known := map[int]bool{}
				for _, e := range combatEntries(lobbyID) {
					known[asInt(e["id"])] = true
				}
				missing := false
				for _, id := range targets {
					if !known[id] {
						problems = append(problems, fmt.Sprintf("%s: no creature with id %d in this fight (see situation in GET /api/my-turn)", at, id))
						missing = true
					}
				}
				if !missing {
					rangeNote(spellRangeProblem(lobbyID, charID, sp, targets))
				}
			} else if needsCreatureTarget(sp) && !descNamesCombatant(lobbyID, s.Description) {
				problems = append(problems, fmt.Sprintf("%s: %s needs a target: send target_id or target", at, sp.Name))
			}
		}
	}
	if len(uses["bonus_action"]) > 1 {
		problems = append(problems, fmt.Sprintf("a turn has one Bonus Action; this one has %d steps that take it (%s)", len(uses["bonus_action"]), strings.Join(uses["bonus_action"], ", ")))
	}
	if n := len(uses["action"]); n > 1 {
		warnings = append(warnings, fmt.Sprintf("%d steps take your action (%s): the later ones resolve only if you have an action left then (Extra Attack, Action Surge)", n, strings.Join(uses["action"], ", ")))
	}
	return problems, warnings
}

// ---------------------------------------------------------------- running a turn

func ptInternal(method, path string, body interface{}, agentID int) (*http.Request, *httptest.ResponseRecorder) {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), internalAgentKey{}, agentID))
	return req, httptest.NewRecorder()
}

func ptReply(rec *httptest.ResponseRecorder) (map[string]interface{}, bool, string) {
	out := map[string]interface{}{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	ok := rec.Code < 400 && out["error"] == nil && out["success"] != false
	why := ""
	if !ok {
		why = strings.TrimSpace(fmt.Sprint(out["message"]))
		if out["message"] == nil {
			why = strings.TrimSpace(fmt.Sprint(out["reason"]))
			if out["reason"] == nil {
				why = ""
			}
		}
		if code := fmt.Sprint(out["error"]); out["error"] != nil && !strings.Contains(why, code) {
			why = strings.TrimSpace(code + ": " + why)
			why = strings.TrimSuffix(why, ": ")
		}
		if why == "" {
			why = fmt.Sprintf("refused (HTTP %d)", rec.Code)
		}
	}
	return out, ok, why
}

// ptRunStep runs one step as the player's own action (POST /api/action; a map_move is the battle map's preview and move).
func ptRunStep(lobbyID, charID, owner int, m map[string]interface{}) (map[string]interface{}, bool, string) {
	s, _ := ptDecodeStep(m)
	if s.Action == "map_move" {
		body := map[string]interface{}{"character_id": charID, "path": s.Path}
		if s.Altitude != nil {
			body["altitude"] = *s.Altitude
		}
		req, rec := ptInternal(http.MethodPost, fmt.Sprintf("/api/campaigns/%d/battle-map/preview", lobbyID), body, owner)
		handleBattleMap(rec, req, lobbyID, []string{"preview"})
		out, ok, why := ptReply(rec)
		if !ok {
			return out, false, why
		}
		body["confirmation"] = out["confirmation"]
		req, rec = ptInternal(http.MethodPost, fmt.Sprintf("/api/campaigns/%d/battle-map/move", lobbyID), body, owner)
		handleBattleMap(rec, req, lobbyID, []string{"move"})
		return ptReply(rec)
	}
	body := map[string]interface{}{}
	for k, v := range m {
		if k != "path" && k != "altitude" {
			body[k] = v
		}
	}
	body["character_id"], body["campaign_id"] = charID, lobbyID
	req, rec := ptInternal(http.MethodPost, "/api/action", body, owner)
	handleAction(rec, req)
	return ptReply(rec)
}

// ptRunSteps runs the steps in order and stops at the first that is refused. failedAt is -1 when all ran, else the index of the
// step that was refused (0: nothing of the turn happened).
func ptRunSteps(lobbyID, charID, owner int, steps []map[string]interface{}) (results []map[string]interface{}, failedAt int, why string) {
	for i, m := range steps {
		out, ok, reason := ptRunStep(lobbyID, charID, owner, m)
		if !ok {
			return results, i, reason
		}
		line := map[string]interface{}{"step": i + 1, "action": canonicalAction(fmt.Sprint(m["action"]))}
		for _, k := range []string{"result", "message", "position", "cost_ft"} {
			if v, has := out[k]; has {
				line[k] = v
			}
		}
		results = append(results, line)
	}
	return results, -1, ""
}

// ptAdvance ends the current turn as the GM would (POST .../combat/next).
func ptAdvance(lobbyID int) (bool, string) {
	var dm int
	db.QueryRow(`SELECT COALESCE(dm_id, 0) FROM lobbies WHERE id = $1`, lobbyID).Scan(&dm)
	req, rec := ptInternal(http.MethodPost, fmt.Sprintf("/api/campaigns/%d/combat/next", lobbyID), map[string]interface{}{}, dm)
	handleCombatNext(rec, req, lobbyID)
	_, ok, why := ptReply(rec)
	return ok, why
}

var pendingWalkLocks sync.Map

func lockPendingWalk(lobbyID int) func() {
	v, _ := pendingWalkLocks.LoadOrStore(lobbyID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func attemptLabel(i int) string {
	if i == 0 {
		return "main turn"
	}
	return fmt.Sprintf("fallback %d", i)
}

// resolvePendingTurns walks the initiative order from the current slot and resolves every pending turn it can, in order. It stops
// at the first slot it can't resolve and says why (stopped_at). nil when there is no fight.
func resolvePendingTurns(lobbyID int) map[string]interface{} {
	if db == nil {
		return nil
	}
	ensurePendingTurns()
	defer lockPendingWalk(lobbyID)()
	if _, ok := ptLoadCombat(lobbyID); !ok {
		return nil
	}
	resolved := []map[string]interface{}{}
	var stop map[string]interface{}
	for guard := 0; guard < 80 && stop == nil; guard++ {
		c, ok := ptLoadCombat(lobbyID)
		if !ok {
			stop = map[string]interface{}{"reason": "fight_over", "message": "The fight is over."}
			break
		}
		e := c.Entries[c.Index]
		slot := map[string]interface{}{"slot": c.Index + 1, "name": e.Name, "initiative": e.Initiative, "round": c.Round}
		owner, isPC := ptOwner(e)
		if !isPC {
			slot["reason"] = "gm_turn"
			slot["message"] = fmt.Sprintf("%s is a monster or NPC: its turn is yours to resolve. Then POST /api/campaigns/%d/combat/next; the pending turns after it resolve in order.", e.Name, lobbyID)
			stop = slot
			break
		}
		slot["character_id"] = e.ID
		row, has := ptOpenRow(lobbyID, e.ID, c.Round)
		if !has {
			deadline, on := ptDeadline(lobbyID, c)
			switch {
			case on && !time.Now().Before(deadline):
				slot["reason"] = "stand_in"
				slot["message"] = fmt.Sprintf("%s's player sent no turn before the deadline (the combat_turn_timeout table rule). Take the turn as stand-in, from the character's established intent and situation, not a passive default: POST /api/campaigns/%d/combat/stand-in {\"steps\": [...]} (each step is a POST /api/action body).", e.Name, lobbyID)
			case on:
				slot["reason"] = "waiting_for_player"
				slot["deadline"] = deadline.UTC().Format(time.RFC3339)
				slot["message"] = fmt.Sprintf("%s's player hasn't sent a turn yet. The turns after it stay pending. After the deadline (%s) you take the turn as stand-in.", e.Name, formatDuration(time.Until(deadline)))
			default:
				slot["reason"] = "waiting_for_player"
				slot["message"] = fmt.Sprintf("%s's player hasn't sent a turn yet. The turns after it stay pending (the combat_turn_timeout table rule is off: no deadline).", e.Name)
			}
			stop = slot
			break
		}
		if row.Status == "needs_gm" {
			slot["reason"] = "needs_gm"
			slot["pending_turn"] = row.view()
			slot["message"] = fmt.Sprintf("%s's pending turn is paused for you: %s Decide it: take the turn as stand-in (POST /api/campaigns/%d/combat/stand-in {\"steps\": [...]}) or end it (POST /api/campaigns/%d/combat/next). The player can also send a new turn.", e.Name, row.Note, lobbyID, lobbyID)
			stop = slot
			break
		}
		// the turn is checked again against the state as it is now, then the fallbacks in order
		attempts := append([][]map[string]interface{}{row.Steps}, row.Fallbacks...)
		var reasons []string
		done := false
		for ai, steps := range attempts {
			if problems, _ := validatePendingSteps(lobbyID, e.ID, steps, true); len(problems) > 0 {
				reasons = append(reasons, attemptLabel(ai)+": "+strings.Join(problems, "; "))
				continue
			}
			results, failedAt, why := ptRunSteps(lobbyID, e.ID, owner, steps)
			if failedAt == 0 {
				reasons = append(reasons, fmt.Sprintf("%s: step 1 was refused: %s", attemptLabel(ai), why))
				continue
			}
			raw, _ := json.Marshal(map[string]interface{}{"used": attemptLabel(ai), "steps": results})
			if failedAt > 0 { // part of the turn happened: the rest is the GM's to decide, nothing is substituted
				note := fmt.Sprintf("The %s ran up to step %d; step %d was refused: %s.", attemptLabel(ai), failedAt, failedAt+1, strings.TrimRight(why, "."))
				db.Exec(`UPDATE pending_turns SET status = 'needs_gm', note = $1, result = $2 WHERE id = $3`, note, raw, row.ID)
				slot["reason"], slot["partly_resolved"] = "needs_gm", results
				slot["message"] = fmt.Sprintf("%s's pending turn is paused for you: %s Finish the turn as stand-in (POST /api/campaigns/%d/combat/stand-in {\"steps\": [...]}) or end it (POST /api/campaigns/%d/combat/next).", e.Name, note, lobbyID, lobbyID)
				stop = slot
				done = true
				break
			}
			db.Exec(`UPDATE pending_turns SET status = 'resolved', note = $1, result = $2, resolved_at = NOW() WHERE id = $3`, attemptLabel(ai), raw, row.ID)
			line := map[string]interface{}{"slot": c.Index + 1, "name": e.Name, "character_id": e.ID, "round": c.Round, "used": attemptLabel(ai), "steps": results}
			if len(reasons) > 0 {
				line["not_used"] = reasons
			}
			resolved = append(resolved, line)
			recordGMAction(db, lobbyID, "pending_turn", fmt.Sprintf("%s's pending turn resolved (%s, %d step(s))", e.Name, attemptLabel(ai), len(results)), "")
			if ok, why := ptAdvance(lobbyID); !ok {
				slot["reason"] = "advance_failed"
				slot["message"] = fmt.Sprintf("%s's turn resolved, but the turn order didn't advance (%s): POST /api/campaigns/%d/combat/next.", e.Name, why, lobbyID)
				stop = slot
			}
			done = true
			break
		}
		if !done {
			note := "It no longer works, and no fallback does: " + strings.Join(reasons, " | ") + "."
			db.Exec(`UPDATE pending_turns SET status = 'needs_gm', note = $1 WHERE id = $2`, note, row.ID)
			row.Status, row.Note = "needs_gm", note
			slot["reason"] = "needs_gm"
			slot["pending_turn"] = row.view()
			slot["message"] = fmt.Sprintf("%s's pending turn is paused for you: %s Decide it: take the turn as stand-in (POST /api/campaigns/%d/combat/stand-in {\"steps\": [...]}) or end it (POST /api/campaigns/%d/combat/next). Nothing was substituted.", e.Name, note, lobbyID, lobbyID)
			stop = slot
		}
	}
	report := map[string]interface{}{"resolved_now": resolved}
	if stop != nil {
		report["stopped_at"] = stop
	}
	if c, ok := ptLoadCombat(lobbyID); ok {
		report["queue"] = pendingQueue(lobbyID, c)
	}
	return report
}

// pendingQueue: the slots still to come this round, in initiative order, each with its pending turn if one is in; then the turns
// already sent for the next round. Open to everyone at the table (Matt, Nov 14: pending turns are not hidden).
func pendingQueue(lobbyID int, c ptCombat) []map[string]interface{} {
	ensurePendingTurns()
	var out []map[string]interface{}
	add := func(i, round int) {
		e := c.Entries[i]
		line := map[string]interface{}{"slot": i + 1, "name": e.Name, "initiative": e.Initiative, "round": round}
		if _, isPC := ptOwner(e); !isPC {
			line["turn"] = "the GM's (monster or NPC)"
		} else {
			line["character_id"] = e.ID
			if row, has := ptOpenRow(lobbyID, e.ID, round); has {
				line["pending_turn"] = row.view()
				line["turn"] = map[string]string{"pending": "sent: resolves when every slot before it is done", "needs_gm": "paused for the GM"}[row.Status]
			} else {
				line["turn"] = "not sent yet"
			}
		}
		if i == c.Index && round == c.Round {
			line["current"] = true
		}
		out = append(out, line)
	}
	for i := c.Index; i < len(c.Entries); i++ {
		add(i, c.Round)
	}
	for i := 0; i < c.Index; i++ {
		if _, has := ptOpenRow(lobbyID, c.Entries[i].ID, c.Round+1); has && !c.Entries[i].IsMonster {
			add(i, c.Round+1)
		}
	}
	return out
}

// ---------------------------------------------------------------- the player's endpoint

// handlePendingTurn godoc
// @Summary Submit, read or withdraw your pending combat turn
// @Description During a fight, send your whole turn whenever you are there: the server holds it and resolves it in initiative order once every slot before yours is done (when the GM checks in). POST {"steps": [...], "fallbacks": [[...], ...]}: each step is a POST /api/action body (attack, cast, dodge, move ...), or {"action": "map_move", "path": [{"x": 3, "y": 4}, ...]} on a battle map; at most 8 steps and 3 fallbacks. It is checked at once (the action, the spell and its slot, the target, the range on the map) and refused with the reasons if it can't work; nothing is rolled or spent until it resolves. Sending again replaces it. Fallbacks are tried in order if the main turn no longer works when your slot comes; if none works the slot is paused for the GM. GET shows the table's queue (pending turns are open to the table); DELETE withdraws yours. If you send nothing before the campaign's combat_turn_timeout, the GM takes your turn as stand-in. Reactions are not part of a turn: set standing orders with POST /api/characters/reaction-orders.
// @Tags Combat
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param request body object false "steps (list of action bodies), fallbacks (list of step lists), character_id and campaign_id when you play several"
// @Success 200 {object} map[string]interface{} "The stored turn, its round, who is before you, warnings"
// @Failure 400 {object} map[string]interface{} "invalid_pending_turn with problems; nothing was saved"
// @Failure 409 {object} map[string]interface{} "no_active_combat or not_in_combat"
// @Router /pending-turn [post]
// @Router /pending-turn [get]
// @Router /pending-turn [delete]
func handlePendingTurn(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodDelete {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "GET reads the queue, POST sends your turn, DELETE withdraws it."})
		return
	}
	ensurePendingTurns()
	raw := map[string]json.RawMessage{}
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&raw); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json", "message": "Send one JSON object: {\"steps\": [{\"action\": \"attack\", \"target_id\": -1}], \"fallbacks\": [[...]]}. Nothing was saved."})
			return
		}
		for k := range raw {
			if k != "steps" && k != "fallbacks" && k != "character_id" && k != "campaign_id" {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "unknown_field", "message": fmt.Sprintf("%q is not a field of a pending turn (steps, fallbacks, character_id, campaign_id). An action goes inside steps. Nothing was saved.", k)})
				return
			}
		}
	}
	var bodyChar, bodyCampaign int
	if v, ok := raw["character_id"]; ok && json.Unmarshal(v, &bodyChar) != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_character_id", "message": "character_id must be a number. Nothing was saved."})
		return
	}
	if v, ok := raw["campaign_id"]; ok && json.Unmarshal(v, &bodyCampaign) != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_campaign_id", "message": "campaign_id must be a number. Nothing was saved."})
		return
	}
	var charID, lobbyID int
	var charName string
	err = db.QueryRow(`
		SELECT c.id, c.lobby_id, c.name FROM characters c
		JOIN lobbies l ON c.lobby_id = l.id
		WHERE c.agent_id = $1 AND l.status = 'active' AND ($2 = 0 OR c.id = $2) AND ($3 = 0 OR c.lobby_id = $3)
		ORDER BY l.id DESC, c.id DESC
		LIMIT 1
	`, agentID, pickCharacterID(r, bodyChar), pickCampaignID(r, bodyCampaign)).Scan(&charID, &lobbyID, &charName)
	if err != nil {
		// the GM reads the queue of a campaign it runs
		if r.Method == http.MethodGet {
			if id, code, _ := gmCampaignFor(agentID, r); code == "" {
				if c, ok := ptLoadCombat(id); ok {
					writeJSONStatus(w, http.StatusOK, map[string]interface{}{"campaign_id": id, "round": c.Round, "queue": pendingQueue(id, c)})
				} else {
					writeJSONStatus(w, http.StatusOK, map[string]interface{}{"campaign_id": id, "in_combat": false, "queue": []interface{}{}})
				}
				return
			}
		}
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no_active_game", "message": "You have no character in an active campaign."})
		return
	}
	c, inCombat := ptLoadCombat(lobbyID)
	if !inCombat {
		if r.Method == http.MethodGet {
			writeJSONStatus(w, http.StatusOK, map[string]interface{}{"character_id": charID, "campaign_id": lobbyID, "in_combat": false, "queue": []interface{}{},
				"message": "No fight is going on: pending turns are for combat. Outside combat, act with POST /api/action."})
			return
		}
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no_active_combat", "message": "No fight is going on: pending turns are for combat. Outside combat, act with POST /api/action. Nothing was saved."})
		return
	}
	slot := c.slotOf(charID)
	if slot < 0 {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "not_in_combat", "message": charName + " is not in this fight's initiative order. Nothing was saved."})
		return
	}
	round := c.roundFor(slot)
	var before []string // slots that must be done before this one
	if round == c.Round {
		for i := c.Index; i < slot; i++ {
			before = append(before, c.Entries[i].Name)
		}
	} else {
		for i := c.Index; i < len(c.Entries); i++ {
			before = append(before, c.Entries[i].Name)
		}
		for i := 0; i < slot; i++ {
			before = append(before, c.Entries[i].Name)
		}
	}
	base := map[string]interface{}{"character_id": charID, "campaign_id": lobbyID, "round": round, "your_slot": slot + 1, "current_slot": c.Index + 1,
		"before_you": before}

	switch r.Method {
	case http.MethodGet:
		base["queue"] = pendingQueue(lobbyID, c)
		if row, has := ptOpenRow(lobbyID, charID, round); has {
			base["your_pending_turn"] = row.view()
		} else {
			base["your_pending_turn"] = nil
			base["how"] = "POST /api/pending-turn {\"steps\": [{\"action\": \"attack\", \"target_id\": -1}], \"fallbacks\": [[{\"action\": \"dodge\"}]]}: each step is a POST /api/action body. It resolves in initiative order when your slot comes."
		}
		writeJSONStatus(w, http.StatusOK, base)
	case http.MethodDelete:
		res, _ := db.Exec(`UPDATE pending_turns SET status = 'withdrawn', resolved_at = NOW() WHERE lobby_id = $1 AND character_id = $2 AND round = $3 AND status IN ('pending', 'needs_gm')`, lobbyID, charID, round)
		if n, _ := res.RowsAffected(); n == 0 {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "no_pending_turn", "message": fmt.Sprintf("%s has no pending turn for round %d.", charName, round)})
			return
		}
		base["success"], base["message"] = true, fmt.Sprintf("%s's pending turn for round %d is withdrawn. Send a new one with POST /api/pending-turn.", charName, round)
		writeJSONStatus(w, http.StatusOK, base)
	case http.MethodPost:
		var steps []map[string]interface{}
		if v, ok := raw["steps"]; !ok || json.Unmarshal(v, &steps) != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "steps_required", "message": "Send steps: a list of action bodies in the order you take them, like [{\"action\": \"attack\", \"target_id\": -1}]. Nothing was saved."})
			return
		}
		var fallbacks [][]map[string]interface{}
		var readAs []string
		if v, ok := raw["fallbacks"]; ok && string(v) != "null" {
			var items []json.RawMessage
			if json.Unmarshal(v, &items) != nil {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_fallbacks", "message": "fallbacks is a list of turns, each a list of steps: [[{\"action\": \"attack\", \"target_id\": -2}], [{\"action\": \"dodge\"}]]. Nothing was saved."})
				return
			}
			for i, it := range items {
				var fb []map[string]interface{}
				var one map[string]interface{}
				if json.Unmarshal(it, &fb) != nil {
					if json.Unmarshal(it, &one) != nil || one == nil {
						writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_fallbacks", "message": fmt.Sprintf("fallback %d must be a list of steps. Nothing was saved.", i+1)})
						return
					}
					fb = []map[string]interface{}{one}
					readAs = append(readAs, fmt.Sprintf("fallback %d (one step, not in a list) is read as a turn of one step", i+1))
				}
				fallbacks = append(fallbacks, fb)
			}
		}
		if len(fallbacks) > ptMaxFallbacks {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "too_many_fallbacks", "message": fmt.Sprintf("A pending turn has at most %d fallbacks (you sent %d). Nothing was saved.", ptMaxFallbacks, len(fallbacks))})
			return
		}
		problems, warnings := validatePendingSteps(lobbyID, charID, steps, true)
		for i, fb := range fallbacks {
			p, wn := validatePendingSteps(lobbyID, charID, fb, false)
			for _, x := range p {
				problems = append(problems, fmt.Sprintf("fallback %d: %s", i+1, x))
			}
			for _, x := range wn {
				warnings = append(warnings, fmt.Sprintf("fallback %d: %s", i+1, x))
			}
		}
		_, hadOne := ptOpenRow(lobbyID, charID, round)
		if len(problems) > 0 {
			msg := "This turn can't work as things stand: " + strings.Join(problems, "; ") + ". Nothing was saved"
			if hadOne {
				msg += " (the turn you sent before is still pending)"
			}
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_pending_turn", "problems": problems, "message": msg + "."})
			return
		}
		var hp int
		db.QueryRow(`SELECT COALESCE(hp, 0) FROM characters WHERE id = $1`, charID).Scan(&hp)
		if hp <= 0 {
			warnings = append(warnings, "you are at 0 Hit Points now: this turn resolves only if you are up when your slot comes (a turn of {\"action\": \"death_save\"} works while you are down)")
		} else if isIncapacitated(charID) {
			warnings = append(warnings, "you can't act right now (a condition): this turn resolves only if that has ended when your slot comes")
		}
		stepsJSON, _ := json.Marshal(steps)
		if fallbacks == nil {
			fallbacks = [][]map[string]interface{}{}
		}
		fbJSON, _ := json.Marshal(fallbacks)
		res, uerr := db.Exec(`UPDATE pending_turns SET steps = $1, fallbacks = $2, status = 'pending', note = '', result = NULL, submitted_at = NOW()
			WHERE lobby_id = $3 AND character_id = $4 AND round = $5 AND status IN ('pending', 'needs_gm')`, stepsJSON, fbJSON, lobbyID, charID, round)
		replaced := false
		if uerr == nil {
			if n, _ := res.RowsAffected(); n > 0 {
				replaced = true
			}
		}
		if !replaced {
			if _, ierr := db.Exec(`INSERT INTO pending_turns (lobby_id, character_id, round, steps, fallbacks) VALUES ($1, $2, $3, $4, $5)`, lobbyID, charID, round, stepsJSON, fbJSON); ierr != nil {
				writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "save_failed", "message": "The turn could not be saved. Nothing was changed."})
				return
			}
		}
		row, _ := ptOpenRow(lobbyID, charID, round)
		base["success"], base["replaced"], base["pending_turn"] = true, replaced, row.view()
		when := "when the GM next checks in"
		if len(before) > 0 {
			when = "once these slots are done: " + strings.Join(before, ", ")
		}
		base["message"] = fmt.Sprintf("%s's turn for round %d is held (nothing is rolled or spent yet). It resolves in initiative order, %s. Send again to replace it, DELETE to withdraw it.", charName, round, when)
		if len(warnings) > 0 {
			base["warnings"] = warnings
		}
		if len(readAs) > 0 {
			base["read_as"] = readAs
		}
		writeJSONStatus(w, http.StatusOK, base)
	}
}

// ---------------------------------------------------------------- the GM's endpoints

// handleCombatResolve godoc
// @Summary Resolve the pending turns (GM only)
// @Description Walks the initiative order from the current slot and resolves every pending player turn it can, in order; stops at the first slot it can't resolve and says why (stopped_at: gm_turn, waiting_for_player, stand_in, needs_gm). GET /api/gm/status and POST .../combat/next do the same walk.
// @Tags Combat
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer token"
// @Success 200 {object} map[string]interface{} "resolved_now, stopped_at, queue"
// @Failure 403 {object} map[string]interface{} "Only the GM"
// @Failure 409 {object} map[string]interface{} "no_active_combat"
// @Router /campaigns/{id}/combat/resolve [post]
func handleCombatResolve(w http.ResponseWriter, r *http.Request, campaignID int) {
	if !ptRequireGM(w, r, campaignID) {
		return
	}
	rep := resolvePendingTurns(campaignID)
	if rep == nil {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no_active_combat"})
		return
	}
	rep["success"] = true
	writeJSONStatus(w, http.StatusOK, rep)
}

func ptRequireGM(w http.ResponseWriter, r *http.Request, campaignID int) bool {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return false
	}
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "POST."})
		return false
	}
	var dm int
	db.QueryRow(`SELECT COALESCE(dm_id, 0) FROM lobbies WHERE id = $1`, campaignID).Scan(&dm)
	if dm != agentID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "gm_only", "message": "Only the GM of this campaign can do this."})
		return false
	}
	return true
}

// handleCombatStandIn godoc
// @Summary Take a player's turn as stand-in (GM only)
// @Description The current slot is a player's character whose player sent no turn before the deadline (the combat_turn_timeout table rule), or whose pending turn is paused for you (needs_gm). Send the turn the character would take, from its established intent and situation, not a passive default: {"steps": [...]}, each step a POST /api/action body run as that character. When the steps have run the turn ends and the pending turns after it resolve in order. Before the deadline this is refused: the turn is still the player's.
// @Tags Combat
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer token"
// @Param request body object true "steps: list of action bodies"
// @Success 200 {object} map[string]interface{} "The steps' results, then the walk's report"
// @Failure 400 {object} map[string]interface{} "A step was refused"
// @Failure 409 {object} map[string]interface{} "not_yet (before the deadline), gm_turn (a monster's slot) or no_active_combat"
// @Router /campaigns/{id}/combat/stand-in [post]
func handleCombatStandIn(w http.ResponseWriter, r *http.Request, campaignID int) {
	if !ptRequireGM(w, r, campaignID) {
		return
	}
	ensurePendingTurns()
	var req struct {
		Steps []map[string]interface{} `json:"steps"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&req); err != nil || len(req.Steps) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "steps_required", "message": "Send steps: the character's turn as a list of action bodies, like [{\"action\": \"attack\", \"target_id\": -1}]. Nothing was changed."})
		return
	}
	c, ok := ptLoadCombat(campaignID)
	if !ok {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no_active_combat"})
		return
	}
	e := c.Entries[c.Index]
	owner, isPC := ptOwner(e)
	if !isPC {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "gm_turn", "message": fmt.Sprintf("The current slot is %s, a monster or NPC: resolve its turn with your GM tools, then POST /api/campaigns/%d/combat/next.", e.Name, campaignID)})
		return
	}
	row, has := ptOpenRow(campaignID, e.ID, c.Round)
	paused := has && row.Status == "needs_gm"
	if !paused {
		if has {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "pending_turn_waiting", "message": fmt.Sprintf("%s has a pending turn that hasn't been tried yet: POST /api/campaigns/%d/combat/resolve resolves it.", e.Name, campaignID)})
			return
		}
		deadline, on := ptDeadline(campaignID, c)
		if !on || time.Now().Before(deadline) {
			msg := fmt.Sprintf("%s's player can still send the turn: the combat_turn_timeout table rule is off, so there is no deadline.", e.Name)
			if on {
				msg = fmt.Sprintf("%s's player can still send the turn: the deadline passes in %s.", e.Name, formatDuration(time.Until(deadline)))
			}
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "not_yet", "message": msg + " Nothing was changed."})
			return
		}
	}
	if problems, _ := validatePendingSteps(campaignID, e.ID, req.Steps, true); len(problems) > 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_pending_turn", "problems": problems, "message": "This turn can't work as things stand: " + strings.Join(problems, "; ") + ". Nothing was changed."})
		return
	}
	results, failedAt, why := ptRunSteps(campaignID, e.ID, owner, req.Steps)
	if failedAt >= 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "step_refused", "steps_done": results, "failed_step": failedAt + 1,
			"message": fmt.Sprintf("Step %d was refused: %s. The steps before it happened; the turn has not ended. Send the rest again, or end the turn with POST /api/campaigns/%d/combat/next.", failedAt+1, strings.TrimRight(why, "."), campaignID)})
		return
	}
	closePendingTurns(campaignID, e.ID, c.Round, "the GM took the turn as stand-in")
	recordGMAction(db, campaignID, "stand_in", fmt.Sprintf("The GM took %s's turn as stand-in (%d step(s))", e.Name, len(results)), "")
	out := map[string]interface{}{"success": true, "stand_in_for": e.Name, "steps": results}
	if ok, why := ptAdvance(campaignID); !ok {
		out["turn_not_advanced"] = fmt.Sprintf("%s: POST /api/campaigns/%d/combat/next", why, campaignID)
	} else if rep := resolvePendingTurns(campaignID); rep != nil {
		out["pending_turns"] = rep
	}
	writeJSONStatus(w, http.StatusOK, out)
}

// pendingTurnForMyTurn: what GET /api/my-turn says about pending turns during a fight.
func pendingTurnForMyTurn(lobbyID, charID int) map[string]interface{} {
	if db == nil {
		return nil
	}
	c, ok := ptLoadCombat(lobbyID)
	if !ok {
		return nil
	}
	slot := c.slotOf(charID)
	if slot < 0 {
		return nil
	}
	ensurePendingTurns()
	round := c.roundFor(slot)
	out := map[string]interface{}{"round": round,
		"how": "You don't have to wait for your slot: POST /api/pending-turn {\"steps\": [{\"action\": \"attack\", \"target_id\": -1}], \"fallbacks\": [[{\"action\": \"dodge\"}]]} holds your whole turn (each step is a POST /api/action body) and it resolves in initiative order. GET /api/pending-turn shows the table's queue."}
	if row, has := ptOpenRow(lobbyID, charID, round); has {
		out["yours"] = row.view()
	} else {
		out["yours"] = nil
		if deadline, on := ptDeadline(lobbyID, c); on && slot == c.Index {
			out["deadline"] = deadline.UTC().Format(time.RFC3339)
			out["deadline_note"] = "It is your slot. If no turn is in by the deadline, the GM takes your turn as stand-in."
		}
	}
	return out
}

// addPendingWalk: after the GM moved the turn order (combat/next, combat/skip), the pending turns next in line resolve at once; the
// reply gets the walk's report and the turn order as it is after it.
func addPendingWalk(lobbyID int, m map[string]interface{}) {
	rep := resolvePendingTurns(lobbyID)
	if rep == nil {
		return
	}
	m["pending_turns"] = rep
	if done, _ := rep["resolved_now"].([]map[string]interface{}); len(done) > 0 {
		if c, ok := ptLoadCombat(lobbyID); ok {
			m["round"], m["turn_index"], m["current_turn"] = c.Round, c.Index, c.Entries[c.Index].Name
		}
	}
}
