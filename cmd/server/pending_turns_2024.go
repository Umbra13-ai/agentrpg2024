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
//	          slot it can't resolve: a monster or NPC (the GM's to play), a player with no pending turn (the GM takes it as stand-in), or a turn that no longer
//	          works and has no working fallback (paused for the GM: needs_gm).
//	fallbacks tried in order when the main turn no longer works. None works: the slot is paused for the GM, nothing is substituted.
//	missing   no waiting on a timer: a character with no turn in when its slot comes up has its turn taken by the GM as stand-in
//	          (POST .../combat/stand-in), from the character's established intent. Its slot is never skipped.
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
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------- the fight as the walk sees it

type ptEntry struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Initiative int    `json:"initiative"`
	IsMonster  bool   `json:"is_monster"`
	Second     bool   `json:"is_thiefs_reflexes_turn"`
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
	db.Exec(`UPDATE pending_turns SET status = 'closed', note = $4, resolved_at = NOW()
		WHERE lobby_id = $1 AND character_id = $2 AND round <= $3 AND status IN ('pending', 'needs_gm')`, lobbyID, charID, round, how)
}

// clearPendingTurns: a fight starts or ends: its pending turns go.
func clearPendingTurns(lobbyID int) {
	if db == nil {
		return
	}
	db.Exec(`DELETE FROM pending_turns WHERE lobby_id = $1`, lobbyID)
}

// ---------------------------------------------------------------- checking a turn

// ptStep: one step of a pending turn: an action, read with the action request's own fields (actionFields, actions.go), or a
// map_move with its path and altitude. character_id and campaign_id go at the top of the pending turn, not in a step.
type ptStep struct {
	actionFields
	Path     []mapSquare `json:"path"`
	Altitude *int        `json:"altitude"`
}

const ptMaxSteps, ptMaxFallbacks = 8, 3

// ptDecodeStep reads a step by the rule every request is read by (request_body_2024.go): a field that isn't an action's is
// refused when the turn is sent, not first when it resolves.
func ptDecodeStep(m map[string]interface{}) (ptStep, *bodyProblem) {
	var s ptStep
	raw, _ := json.Marshal(m)
	p := parseRequest(raw, &s, bodySpec{hint: "character_id and campaign_id go at the top of the pending turn, not in a step."})
	s.Action = canonicalAction(s.Action)
	return s, p
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

// ptEnemiesDown: the fight has monsters in its turn order and none of them is still up.
func ptEnemiesDown(lobbyID int) bool {
	monsters, up := 0, 0
	for _, e := range combatEntries(lobbyID) {
		if m, _ := e["is_monster"].(bool); m {
			monsters++
			if asInt(e["hp"]) > 0 {
				up++
			}
		}
	}
	return monsters > 0 && up == 0
}

// spellHarms: the spell deals damage or forces a saving throw, so its target has to be a creature still in the fight.
func spellHarms(sp SRDSpell) bool {
	return sp.DamageDice != "" || sp.SavingThrow != "" || len(sp.DamageParts) > 0
}

// ptHandsProblem: the spell's Somatic or Material component needs a hand the character doesn't have free (a weapon and a Shield).
// No step of a pending turn can sheathe a weapon, so this is refused when the turn is sent, with what to do (live test, Nov 14:
// a Cleric's Bless was refused at resolution and the fallback ran instead).
func ptHandsProblem(lobbyID, charID int, sp SRDSpell, slug string, s ptStep) string {
	if s.Item != "" || s.Charges > 0 || db == nil {
		return "" // cast from an item: no components
	}
	var inventoryJSON, featsJSON []byte
	var class string
	if db.QueryRow(`SELECT COALESCE(inventory, '[]'), COALESCE(feats, '[]'), LOWER(COALESCE(class,'')) FROM characters WHERE id = $1`, charID).Scan(&inventoryJSON, &featsJSON, &class) != nil {
		return ""
	}
	var inventory []map[string]interface{}
	var feats []string
	json.Unmarshal(inventoryJSON, &inventory)
	json.Unmarshal(featsJSON, &feats)
	focusClass := class
	if _, isOrigin := originSpellsOf(charID)[slug]; isOrigin && !containsStr(getClassSpellList(class), slug) {
		focusClass = ""
	}
	var metamagic []string
	if hasWord(strings.ToLower(s.Description), "subtle") {
		metamagic = []string{"subtle"}
	}
	why := castHandsProblem(lobbyID, charID, sp.Components, sp.MaterialCost == 0 && !sp.MaterialConsumed, usableInventory(inventory), focusClass, feats, metamagic)
	if why == "" {
		return ""
	}
	return strings.TrimPrefix(why, "Cannot cast - ") + " Do that now, before your slot, then send the turn again: a pending turn can't change what you hold"
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
		s, perr := ptDecodeStep(m)
		if perr != nil {
			problems = append(problems, at+": "+strings.TrimSuffix(perr.Message, " Nothing was changed."))
			continue
		}
		if s.Action == "" {
			problems = append(problems, at+": action is missing (attack, cast, dodge, dash, move, map_move ...)")
			continue
		}
		at += " (" + s.Action + ")"
		if code, msg := actionWordProblem(s.Action, s.CompanionID); code == "unknown_action" {
			problems = append(problems, fmt.Sprintf("%s: unknown_action: %q is not an action (the actions you can send are listed under actions)", at, s.Action))
			continue
		} else if code != "" && code != "pending_turn_step" {
			problems = append(problems, at+": "+code+": "+strings.TrimSuffix(msg, " Nothing was spent."))
			continue
		}
		if s.Action == "death_save" { // the server rolls it at a dying character's slot (needsDeathSave): a held turn is what you do when up
			problems = append(problems, at+": no need: if you are at 0 Hit Points when your slot comes, the server rolls your Death Saving Throw itself. A pending turn holds what you do when you are up")
			continue
		}
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
		if s.Action == "cast" { // a spell takes what its casting time says: Healing Word is a Bonus Action, Shield a Reaction
			if slug, _, selErr := resolveCastSpellSlug(s.SpellSlug, actionNarrative(s.Description, s.Result)); selErr == "" {
				switch ct := strings.ToLower(srdSpellsMemory[normalizeSpellSlug(slug)].CastingTime); {
				case strings.Contains(ct, "bonus"):
					kind = "bonus_action"
				case strings.Contains(ct, "reaction"):
					kind = "reaction"
				}
			}
		}
		if kind == "reaction" {
			problems = append(problems, at+": this is a Reaction, taken on another creature's turn, so it isn't part of your turn. Standing reaction orders (Shield, Opportunity Attack ...) are set with POST /api/characters/reaction-orders")
			continue
		}
		if (kind == "action" || kind == "bonus_action") && s.Action != "use_item" { // an item's use may be a Bonus Action (a potion): not counted
			uses[kind] = append(uses[kind], s.Action)
		}
		if kind == "movement" {
			moved = true
		}
		if s.MovementCost < 0 || s.MovementCost > 10000 {
			problems = append(problems, at+": movement_cost is the feet of movement you use, 0 or more")
		}
		if t, _ := lookupActionType(s.Action); t.GM && strings.TrimSpace(actionNarrative(s.Description, s.Result)) == "" {
			problems = append(problems, at+": "+t.Title+" is described for the GM: say what your character does or says in description")
		}
		if s.Action == "ready" {
			if _, _, _, ok := readiedAction(actionNarrative(s.Description, s.Result)); !ok {
				problems = append(problems, at+": unknown_action: a Ready holds one action, \"trigger: <what happens>; action: <an action type> ...\"")
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
		case "attack", "offhand_attack":
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
			if why := ptHandsProblem(lobbyID, charID, sp, normalizeSpellSlug(slug), s); why != "" {
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
				if !missing && spellHarms(sp) { // a spell that harms needs a living enemy, as an attack does (live test, Nov 14)
					for _, id := range targets {
						if id < 0 {
							if ok, why := validateAttackTarget(lobbyID, id); !ok {
								problems = append(problems, at+": "+why)
								missing = true
								break
							}
						}
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

// withActionOptions adds what the character can send (actions) to a refused turn whose problems include a word that is no action.
func withActionOptions(charID int, problems []string, reply map[string]interface{}) map[string]interface{} {
	for _, p := range problems {
		if strings.Contains(p, "unknown_action") {
			reply["actions"] = actionOptionsFor(charID)
			break
		}
	}
	return reply
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
				if res, isText := out["result"].(string); isText { // a cast that can't be made answers 200 with success false and says why in result
					why = strings.TrimSpace(res)
				}
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

// ptAdvance ends the turn of slot c (the one the walk or the stand-in just took) as combat/next would: the one turn change
// (turn_advance_2024.go), only while that slot's turn is still the current one. how is how the turn ended, for the turn
// schedule's record (schedule_walk_2024.go). The change is answered too (nil when it was refused).
func ptAdvance(lobbyID int, c ptCombat, how string) (*turnChange, bool, string) {
	expect := &turnRef{Round: c.Round, Index: c.Index}
	if c.StartedAt.Valid {
		expect.StartedAt = c.StartedAt.Time
	}
	t, code := advanceTurn(lobbyID, turnHow{by: "the pending turn was taken", expect: expect, result: how})
	if code == "" {
		return t, true, ""
	}
	if t != nil && t.Message != "" {
		return nil, false, t.Message
	}
	return nil, false, code
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
// at the first slot it can't resolve and says why (stopped_at). nil when there is no fight. Only the GM's requests run it
// (combat/resolve, combat/stand-in, combat/next, combat/skip; GET /api/gm/status only for a campaign without the turn schedule).
// In a campaign with the turn schedule (schedule_walk_2024.go) a turn that has ended is never run again, an automatic turn
// (turnNeeds) is processed at its place, and a character with no submission is stood in for only once standInAllowed says so;
// before that the walk stops there with awaiting_submission.
func resolvePendingTurns(lobbyID int) map[string]interface{} {
	if db == nil {
		return nil
	}
	defer lockPendingWalk(lobbyID)()
	if _, ok := ptLoadCombat(lobbyID); !ok {
		return nil
	}
	state, serr := scheduleStateOf(db, lobbyID)
	scheduled := usesSchedule(state)
	resolved := []map[string]interface{}{}
	var roundsDone []string
	var stop map[string]interface{}
	// advance ends the walked slot's turn; a round that completed with it is told in the report
	advance := func(c ptCombat, how string, slot map[string]interface{}, done string) bool {
		t, ok, why := ptAdvance(lobbyID, c, how)
		if !ok {
			slot["reason"] = "advance_failed"
			slot["message"] = fmt.Sprintf("%s, but the turn order didn't advance (%s): POST /api/campaigns/%d/combat/next.", done, why, lobbyID)
			stop = slot
			return false
		}
		if t.RoundOpened != nil {
			roundsDone = append(roundsDone, roundCompleteText(t.EndedRound, t.RoundOpened))
		}
		return true
	}
	if serr != nil {
		stop = map[string]interface{}{"reason": "database_error", "message": "The campaign's turn schedule couldn't be read; nothing was resolved."}
	}
	for guard := 0; guard < 80 && stop == nil; guard++ {
		c, ok := ptLoadCombat(lobbyID)
		if !ok {
			stop = map[string]interface{}{"reason": "fight_over", "message": "The fight is over."}
			break
		}
		e := c.Entries[c.Index]
		slot := map[string]interface{}{"slot": c.Index + 1, "name": e.Name, "initiative": e.Initiative, "round": c.Round}
		if ptEnemiesDown(lobbyID) {
			// every monster in the turn order is down: nothing is left to fight, so nobody's turn is asked for (live test, Nov 14:
			// the walk ran on into the next round and asked the GM to stand in for a player after the last goblin fell)
			stop = map[string]interface{}{"reason": "enemies_down", "round": c.Round,
				"message": fmt.Sprintf("Every enemy in this fight is down. End the fight: POST /api/campaigns/%d/combat/end (or bring in more with POST /api/campaigns/%d/combat/add). The turns still held are dropped when the fight ends.", lobbyID, lobbyID)}
			break
		}
		owner, isPC := ptOwner(e)
		if !isPC {
			// monster turns are never queued: the GM chooses the action when its turn comes
			slot["reason"] = "gm_turn"
			slot["message"] = fmt.Sprintf("%s is a monster or NPC: its turn is yours to resolve. Then POST /api/campaigns/%d/combat/next; the pending turns after it resolve in order.", e.Name, lobbyID)
			stop = slot
			break
		}
		slot["character_id"] = e.ID
		var need turnNeed
		if scheduled {
			// a turn that has already ended this round is never run again (a second resolve re-runs nothing)
			how, err := slotRecorded(lobbyID, c.Round, e.ID, e.Second)
			if err != nil {
				slot["reason"], slot["message"] = "database_error", "The round's record couldn't be read; nothing more was resolved."
				stop = slot
				break
			}
			if how != "" {
				if !advance(c, how, slot, fmt.Sprintf("%s's turn had already ended (%s)", e.Name, turnResultHows[how])) {
					break
				}
				continue
			}
			if need, err = turnNeeds(e.ID); err != nil {
				slot["reason"], slot["message"] = "database_error", "The character's Hit Points couldn't be read; nothing more was resolved."
				stop = slot
				break
			}
			if need.Kind == needNothing {
				// stable or unconscious: nothing to choose (Matt, 11.3): the turn passes once, at its place, and never waits
				closePendingTurns(lobbyID, e.ID, c.Round, "The character couldn't act when the slot came ("+need.Why+"): the turn passed automatically.")
				resolved = append(resolved, map[string]interface{}{"slot": c.Index + 1, "name": e.Name, "character_id": e.ID, "round": c.Round,
					"used": "automatic (" + need.Why + ": no action to choose)", "steps": []map[string]interface{}{}})
				recordGMAction(db, lobbyID, "pending_turn", fmt.Sprintf("%s is %s: the turn passed automatically", e.Name, need.Why), "")
				if !advance(c, howAutomatic, slot, fmt.Sprintf("%s's turn passed automatically", e.Name)) {
					break
				}
				continue
			}
		} else {
			need = turnNeed{Kind: needChoose}
			if needsDeathSave(e.ID) {
				need = turnNeed{Kind: needDeathSave}
			}
		}
		if need.Kind == needDeathSave {
			// at 0 Hit Points and not stable: the rules give this turn no choice ("whenever you start your turn with 0 Hit Points,
			// you must make a Death Saving Throw"), so the server rolls it and the fight goes on. A held turn can't be taken.
			out, ok, why := ptRunStep(lobbyID, e.ID, owner, map[string]interface{}{"action": "death_save"})
			if ok {
				res := []map[string]interface{}{{"step": 1, "action": "death_save", "result": out["result"]}}
				raw, _ := json.Marshal(map[string]interface{}{"used": "death saving throw", "steps": res})
				db.Exec(`UPDATE pending_turns SET status = 'closed', note = 'At 0 Hit Points when the slot came: the server rolled the Death Saving Throw instead.', result = $4, resolved_at = NOW()
					WHERE lobby_id = $1 AND character_id = $2 AND round = $3 AND status IN ('pending', 'needs_gm')`, lobbyID, e.ID, c.Round, raw)
				resolved = append(resolved, map[string]interface{}{"slot": c.Index + 1, "name": e.Name, "character_id": e.ID, "round": c.Round,
					"used": "death saving throw (at 0 Hit Points: rolled by the server)", "steps": res})
				recordGMAction(db, lobbyID, "pending_turn", fmt.Sprintf("%s is at 0 Hit Points: the Death Saving Throw was rolled", e.Name), "")
				if !advance(c, howDeathSave, slot, fmt.Sprintf("%s's Death Saving Throw was rolled", e.Name)) {
					break
				}
				continue
			}
			_ = why // the roll was refused (the state changed under us): fall through to the usual handling
		}
		row, has := ptOpenRow(lobbyID, e.ID, c.Round)
		if !has {
			// a character with no turn in when its slot comes up has its turn taken by the GM as stand-in: at once without the turn
			// schedule (Matt, Nov 14: a fight must not stall for hours on an agent whose scheduler is broken), once the round's final
			// check-in has come with it (Matt, Nov 15). A player who is acting directly in this slot (POST /api/action) is left to it.
			var acted bool
			db.QueryRow(`SELECT COALESCE(action_used, false) OR COALESCE(bonus_action_used, false) FROM characters WHERE id = $1`, e.ID).Scan(&acted)
			sc, err := standInNow(lobbyID, false)
			switch {
			case err != nil:
				slot["reason"], slot["message"] = "database_error", "The turn schedule couldn't be read; nothing more was resolved."
			case acted:
				slot["reason"] = "player_acted"
				slot["message"] = fmt.Sprintf("%s acted directly in this slot (POST /api/action) and sent no pending turn. End the turn when it is done: POST /api/campaigns/%d/combat/next; the pending turns after it resolve in order.", e.Name, lobbyID)
			case !sc.Allowed:
				slot["reason"] = "awaiting_submission"
				slot["message"] = fmt.Sprintf("%s hasn't submitted a turn for round %d. %s Resolve again at a later check-in (POST /api/campaigns/%d/combat/resolve).", e.Name, c.Round, sc.tooEarly(e.Name), lobbyID)
				if sc.Deadline != nil {
					slot["final_check_in"] = sc.Deadline.UTC().Format(time.RFC3339)
				}
			case scheduled:
				slot["reason"] = "stand_in"
				slot["final_check_in"] = sc.Deadline.UTC().Format(time.RFC3339)
				slot["message"] = fmt.Sprintf("The round's final check-in (%s) has come and %s has no turn in for round %d: take the turn as stand-in now, a meaningful in-character action from what the character knows and its situation, not a default Dodge and not a skip: POST /api/campaigns/%d/combat/stand-in {\"steps\": [...]} (each step is a POST /api/action body). The pending turns after it then resolve in order.", clockText(*sc.Deadline), e.Name, c.Round, lobbyID)
			default:
				slot["reason"] = "stand_in"
				slot["message"] = fmt.Sprintf("%s has no turn in for this round, and the fight doesn't wait: take the turn as stand-in now, from the character's established intent and situation, not a passive default: POST /api/campaigns/%d/combat/stand-in {\"steps\": [...]} (each step is a POST /api/action body). The pending turns after it then resolve in order.", e.Name, lobbyID)
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
			how := howOwn
			if ai > 0 {
				how = howFallback
			}
			advance(c, how, slot, fmt.Sprintf("%s's turn resolved", e.Name))
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
	if len(roundsDone) > 0 {
		report["round_complete"] = roundsDone
	}
	if c, ok := ptLoadCombat(lobbyID); ok {
		report["queue"] = pendingQueue(lobbyID, c)
	}
	if scheduled { // where the round stands now: the GM's next check-in and the round's final check-in (check_in_2024.go)
		if in, now, err := loadCheckIn(lobbyID); err == nil {
			report["check_in"] = checkInAt(in, now)
		}
	}
	return report
}

// pendingQueue: the slots still to come this round, in initiative order, each with its pending turn if one is in; then the turns
// already sent for the next round. Open to everyone at the table (Matt, Nov 14: pending turns are not hidden).
func pendingQueue(lobbyID int, c ptCombat) []map[string]interface{} {
	var out []map[string]interface{}
	dead := combatDeadFn(lobbyID)
	add := func(i, round int) {
		e := c.Entries[i]
		if e.IsMonster && dead(e.ID) {
			return // a defeated monster has no turn
		}
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
// @Description During a fight, send your whole turn whenever you are there: the server holds it and resolves it in initiative order once every slot before yours is done (when the GM checks in). POST {"steps": [...], "fallbacks": [[...], ...]}: each step is a POST /api/action body (attack, cast, dodge, move ...), or {"action": "map_move", "path": [{"x": 3, "y": 4}, ...]} on a battle map; at most 8 steps and 3 fallbacks. It is checked at once (the action, the spell and its slot, the target, the range on the map) and refused with the reasons if it can't work; nothing is rolled or spent until it resolves. Sending again replaces it. Fallbacks are tried in order if the main turn no longer works when your slot comes; if none works the slot is paused for the GM. GET shows the table's queue (pending turns are open to the table); DELETE withdraws yours. If no turn is in when your slot comes up, the fight doesn't wait: the GM takes your turn as stand-in. Reactions are not part of a turn: set standing orders with POST /api/characters/reaction-orders. In a campaign that uses the turn schedule (GET /api/campaigns/{id}/schedule) a turn is only for the round now open: sending it only saves it (only the GM resolves turns, at its check-ins); a character whose turn in the open round has resolved is done for that round (409 turn_already_resolved: submit when the next round begins), and the optional "round" names the round you mean (409 round_not_open when it isn't the open one); nothing is saved on a refusal. If nothing is in by the round's final check-in, the GM takes your turn as stand-in. Without the schedule a turn sent after your slot has passed is held for the next round.
// @Tags Combat
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Body steps (list of action bodies), fallbacks (list of step lists), round (optional: the round the turn is for), character_id and campaign_id when you play several
// @Success 200 {object} map[string]interface{} "The stored turn, its round, who is before you, warnings"
// @Failure 400 {object} map[string]interface{} "invalid_pending_turn with problems; nothing was saved"
// @Failure 409 {object} map[string]interface{} "no_active_combat, not_in_combat, round_not_open or turn_already_resolved (with open_round)"
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
	raw := map[string]json.RawMessage{}
	switch r.Method {
	case http.MethodPost:
		if !readRequest(w, r, &raw, onlyFields(
			"steps",        // your turn: action bodies in order, each with the fields of POST /api/action
			"fallbacks",    // lists of steps to try in order when a step no longer works when the turn resolves
			"round",        // optional: the round the turn is for; only the round now open is accepted in a scheduled campaign
			"character_id", // the character, when you play more than one
			"campaign_id",  // the campaign, when you play in more than one
		), bodyHint("An action goes inside steps.")) {
			return
		}
	case http.MethodDelete:
		if !readRequest(w, r, &raw, onlyFields(
			"round",        // optional: the round of the turn to withdraw (the round now open when left out)
			"character_id", // the character, when you play more than one
			"campaign_id",  // the campaign, when you play in more than one
		), optionalBody()) {
			return
		}
	}
	var bodyChar, bodyCampaign, askedRound int
	if v, ok := raw["round"]; ok && (json.Unmarshal(v, &askedRound) != nil || askedRound < 1) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_round", "message": "round is the number of the round the turn is for (1 or more); leave it out for the round now open. Nothing was saved."})
		return
	}
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
		if answerDBFailure(w, err, "your character") {
			return
		}
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
	if r.Method != http.MethodGet { // a change waits for a walk under way, so it never lands on a turn being resolved
		defer lockPendingWalk(lobbyID)()
	}
	state, serr := scheduleStateOf(db, lobbyID)
	if answerDBFailure(w, serr, "the campaign's turn schedule") {
		return
	}
	scheduled := usesSchedule(state)
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
	round, refusal, why := pendingRoundFor(c, slot, scheduled, askedRound) // schedule_walk_2024.go
	if refusal != "" && r.Method != http.MethodGet {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": refusal, "message": why, "open_round": round, "character_id": charID, "campaign_id": lobbyID})
		return
	}
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
		if refusal == codeTurnAlreadyResolve {
			base["your_pending_turn"], base["done_this_round"], base["before_you"] = nil, why, []string{}
			writeJSONStatus(w, http.StatusOK, base)
			return
		}
		if row, has := ptOpenRow(lobbyID, charID, round); has {
			base["your_pending_turn"] = row.view()
		} else {
			base["your_pending_turn"] = nil
			base["how"] = "POST /api/pending-turn {\"steps\": [{\"action\": \"attack\", \"target_id\": -1}], \"fallbacks\": [[{\"action\": \"dodge\"}]]}: each step is a POST /api/action body. It resolves in initiative order when your slot comes."
		}
		writeJSONStatus(w, http.StatusOK, base)
	case http.MethodDelete:
		res, xerr := db.Exec(`UPDATE pending_turns SET status = 'withdrawn', resolved_at = NOW() WHERE lobby_id = $1 AND character_id = $2 AND round = $3 AND status IN ('pending', 'needs_gm')`, lobbyID, charID, round)
		if xerr != nil {
			writeDBWriteError(w, xerr, "the withdrawal")
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "no_pending_turn", "message": fmt.Sprintf("%s has no pending turn for round %d.", charName, round)})
			return
		}
		base["success"], base["message"] = true, fmt.Sprintf("%s's pending turn for round %d is withdrawn. Send a new one with POST /api/pending-turn.", charName, round)
		writeJSONStatus(w, http.StatusOK, base)
	case http.MethodPost:
		var steps []map[string]interface{}
		if v, ok := raw["steps"]; !ok || json.Unmarshal(v, &steps) != nil { // your turn: action bodies in order, each with the fields of POST /api/action (map_move also path and altitude)
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "steps_required", "message": "Send steps: a list of action bodies in the order you take them, like [{\"action\": \"attack\", \"target_id\": -1}]. Nothing was saved."})
			return
		}
		var fallbacks [][]map[string]interface{}
		var readAs []string
		if v, ok := raw["fallbacks"]; ok && string(v) != "null" {
			var items []json.RawMessage
			if json.Unmarshal(v, &items) != nil { // other turns to take in order when a step of steps can't be taken: a list of step lists
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
			writeJSONStatus(w, http.StatusBadRequest, withActionOptions(charID, problems, map[string]interface{}{"error": "invalid_pending_turn", "problems": problems, "message": msg + "."}))
			return
		}
		var hp int
		db.QueryRow(`SELECT COALESCE(hp, 0) FROM characters WHERE id = $1`, charID).Scan(&hp)
		if hp <= 0 {
			warnings = append(warnings, "you are at 0 Hit Points now: this turn resolves only if you are up when your slot comes; if you are still down then, the server rolls your Death Saving Throw instead")
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
		if scheduled {
			base["message"] = fmt.Sprintf("Your submission is saved for round %d (nothing is rolled or spent yet). Only the GM resolves turns, at its check-ins, in initiative order: %s. Send again to replace it, DELETE to withdraw it, until it resolves.", round, strings.TrimPrefix(when, "when the GM next checks in"))
			if len(before) == 0 {
				base["message"] = fmt.Sprintf("Your submission is saved for round %d (nothing is rolled or spent yet). Only the GM resolves turns, at its check-ins, in initiative order; yours is next. Send again to replace it, DELETE to withdraw it, until it resolves.", round)
			}
		}
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
// @Description Walks the initiative order from the current slot and resolves every pending player turn it can, in order; stops at the first slot it can't resolve and says why (stopped_at: gm_turn, stand_in, player_acted, needs_gm, enemies_down, awaiting_submission). A character at 0 Hit Points has its Death Saving Throw rolled by the server. POST .../combat/next, .../combat/skip and .../combat/stand-in do the same walk after their change; GET /api/gm/status does it only in a campaign without the turn schedule. With the turn schedule: a turn that has ended is never run again (a second resolve re-runs nothing); a stable or unconscious character's turn passes automatically at its place; a monster's turn stops the walk for the GM; a player with no submission stops it with awaiting_submission until the round's final check-in (final_check_in), then with stand_in. When the round's last turn ends the next round opens and round_complete says "Round N is complete; round N+1 has begun"; check_in is where the round stands now (the GM's next check-in, the final check-in, each character's status).
// @Tags Combat
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer token"
// @Success 200 {object} map[string]interface{} "resolved_now, stopped_at, queue"
// @Failure 403 {object} map[string]interface{} "Only the GM"
// @Failure 409 {object} map[string]interface{} "no_active_combat"
// @Router /campaigns/{id}/combat/resolve [post]
func handleCombatResolve(w http.ResponseWriter, r *http.Request, campaignID int) {
	if !ptRequireGM(w, r, campaignID, "resolve the pending turns") {
		return
	}
	if !noFields(w, r) { // this request takes no fields
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

// ptRequireGM: the GM's POST (the access rule is requireCampaignGM's).
func ptRequireGM(w http.ResponseWriter, r *http.Request, campaignID int, what string) bool {
	if _, ok := requireCampaignGM(w, r, campaignID, what); !ok {
		return false
	}
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "POST."})
		return false
	}
	return true
}

// handleCombatStandIn godoc
// @Summary Take a player's turn as stand-in (GM only)
// @Description The current slot is a player's character with no turn in for this round, or whose pending turn is paused for you (needs_gm). Send the turn the character would take, from its established intent and situation, not a passive default: {"steps": [...]}, each step a POST /api/action body run as that character. When the steps have run the turn ends and the pending turns after it resolve in order. Without the turn schedule the fight doesn't wait: a stand-in may be taken at once. With it, a stand-in for a character with no submission waits for the round's final check-in (409 stand_in_too_early with final_check_in; none while the schedule is paused); a turn paused for you (needs_gm) may be finished at any check-in; an automatic turn (dying, stable, unconscious) is the server's (409 automatic_turn) and a turn that has already ended is never taken again (409 turn_already_resolved).
// @Tags Combat
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer token"
// @Body steps: list of action bodies
// @Success 200 {object} map[string]interface{} "The steps' results, then the walk's report"
// @Failure 400 {object} map[string]interface{} "A step was refused"
// @Failure 409 {object} map[string]interface{} "gm_turn (a monster's slot), pending_turn_waiting, stand_in_too_early, automatic_turn, turn_already_resolved or no_active_combat"
// @Router /campaigns/{id}/combat/stand-in [post]
func handleCombatStandIn(w http.ResponseWriter, r *http.Request, campaignID int) {
	if !ptRequireGM(w, r, campaignID, "take a turn as stand-in") {
		return
	}
	var req struct {
		Steps []map[string]interface{} `json:"steps"` // the turn the character would take: action bodies in order, each with the fields of POST /api/action
	}
	if !readRequest(w, r, &req) {
		return
	}
	if len(req.Steps) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "steps_required", "message": "Send steps: the character's turn as a list of action bodies, like [{\"action\": \"attack\", \"target_id\": -1}]. Nothing was changed."})
		return
	}
	out, ok := standInTurn(w, campaignID, req.Steps) // the walk's lock is held while the turn is taken, and let go before the walk
	if !ok {
		return
	}
	if rep := resolvePendingTurns(campaignID); rep != nil && out["turn_not_advanced"] == nil {
		out["pending_turns"] = rep
	}
	writeJSONStatus(w, http.StatusOK, out)
}

// standInTurn takes the current slot's turn as stand-in (handleCombatStandIn), with the walk's lock held: a stand-in and a walk,
// or two stand-ins, never take the same turn twice. It writes any refusal itself.
func standInTurn(w http.ResponseWriter, campaignID int, steps []map[string]interface{}) (map[string]interface{}, bool) {
	defer lockPendingWalk(campaignID)()
	c, ok := ptLoadCombat(campaignID)
	if !ok {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no_active_combat"})
		return nil, false
	}
	e := c.Entries[c.Index]
	owner, isPC := ptOwner(e)
	if !isPC {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "gm_turn", "message": fmt.Sprintf("The current slot is %s, a monster or NPC: resolve its turn with your GM tools, then POST /api/campaigns/%d/combat/next.", e.Name, campaignID)})
		return nil, false
	}
	state, err := scheduleStateOf(db, campaignID)
	if answerDBFailure(w, err, "the campaign's turn schedule") {
		return nil, false
	}
	row, has := ptOpenRow(campaignID, e.ID, c.Round)
	paused := has && row.Status == "needs_gm"
	if !paused {
		if has {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "pending_turn_waiting", "message": fmt.Sprintf("%s has a pending turn that hasn't been tried yet: POST /api/campaigns/%d/combat/resolve resolves it.", e.Name, campaignID)})
			return nil, false
		}
	}
	if usesSchedule(state) {
		// a turn that has ended is never taken again, and an automatic turn is the server's (schedule_walk_2024.go)
		how, err := slotRecorded(campaignID, c.Round, e.ID, e.Second)
		if answerDBFailure(w, err, "the round's record") {
			return nil, false
		}
		if how != "" {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": codeTurnAlreadyResolve, "message": fmt.Sprintf("%s's turn in round %d has already ended (%s): a character never takes a second turn in a round. POST /api/campaigns/%d/combat/resolve moves on.", e.Name, c.Round, turnResultHows[how], campaignID)})
			return nil, false
		}
		need, err := turnNeeds(e.ID)
		if answerDBFailure(w, err, "the character's Hit Points") {
			return nil, false
		}
		if need.Kind != needChoose {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "automatic_turn", "message": fmt.Sprintf("%s's turn is automatic (%s): no submission or stand-in; POST /api/campaigns/%d/combat/resolve processes it. Nothing was changed.", e.Name, need.Why, campaignID)})
			return nil, false
		}
	}
	sc, err := standInNow(campaignID, paused)
	if answerDBFailure(w, err, "the campaign's turn schedule") {
		return nil, false
	}
	if !sc.Allowed {
		writeJSONStatus(w, http.StatusConflict, sc.refusal(e.Name))
		return nil, false
	}
	if problems, _ := validatePendingSteps(campaignID, e.ID, steps, true); len(problems) > 0 {
		writeJSONStatus(w, http.StatusBadRequest, withActionOptions(e.ID, problems, map[string]interface{}{"error": "invalid_pending_turn", "problems": problems, "message": "This turn can't work as things stand: " + strings.Join(problems, "; ") + ". Nothing was changed."}))
		return nil, false
	}
	results, failedAt, why := ptRunSteps(campaignID, e.ID, owner, steps)
	if failedAt >= 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "step_refused", "steps_done": results, "failed_step": failedAt + 1,
			"message": fmt.Sprintf("Step %d was refused: %s. The steps before it happened; the turn has not ended. Send the rest again, or end the turn with POST /api/campaigns/%d/combat/next.", failedAt+1, strings.TrimRight(why, "."), campaignID)})
		return nil, false
	}
	closePendingTurns(campaignID, e.ID, c.Round, "the GM took the turn as stand-in")
	recordGMAction(db, campaignID, "stand_in", fmt.Sprintf("The GM took %s's turn as stand-in (%d step(s))", e.Name, len(results)), "")
	out := map[string]interface{}{"success": true, "stand_in_for": e.Name, "steps": results}
	if t, ok, why := ptAdvance(campaignID, c, howStandIn); !ok {
		out["turn_not_advanced"] = fmt.Sprintf("%s: POST /api/campaigns/%d/combat/next", why, campaignID)
	} else if t.RoundOpened != nil {
		out["round_complete"] = roundCompleteText(t.EndedRound, t.RoundOpened)
	}
	return out, true
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
	state, _ := scheduleStateOf(db, lobbyID)
	round, refusal, why := pendingRoundFor(c, slot, usesSchedule(state), 0) // schedule_walk_2024.go
	if refusal == codeTurnAlreadyResolve {
		return map[string]interface{}{"round": round, "yours": nil, "done_this_round": why}
	}
	out := map[string]interface{}{"round": round,
		"how": "You don't have to wait for your slot: POST /api/pending-turn {\"steps\": [{\"action\": \"attack\", \"target_id\": -1}], \"fallbacks\": [[{\"action\": \"dodge\"}]]} holds your whole turn (each step is a POST /api/action body) and it resolves in initiative order. GET /api/pending-turn shows the table's queue."}
	if needsDeathSave(charID) {
		out["at_0_hit_points"] = "You are at 0 Hit Points. When your slot comes the server rolls your Death Saving Throw for you; a turn you send is held and is taken only if you are back up by then."
	}
	if row, has := ptOpenRow(lobbyID, charID, round); has {
		out["yours"] = row.view()
	} else {
		out["yours"] = nil
		out["if_you_send_nothing"] = "The fight doesn't wait: if no turn is in when your slot comes up, the GM takes your turn as stand-in. Send your turn for the round as soon as you can."
		if usesSchedule(state) {
			out["if_you_send_nothing"] = "The GM resolves submitted turns at its check-ins, in initiative order; if you have sent nothing by the round's final check-in, the GM takes your turn as stand-in. GET /api/campaigns/{id}/schedule shows your check-in and the round's final check-in."
		}
		if slot == c.Index {
			out["your_slot_is_now"] = true
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
