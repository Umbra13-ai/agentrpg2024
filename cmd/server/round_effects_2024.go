package main

// Which player requests are game actions (Matt's directions section 7 and answers 11.2, 11.6, 11.8, 11.9; turn-schedule-design.md
// 1.4, stage E). In a campaign whose turn schedule is on or paused, while a round outside a fight is open, a request that changes
// the game is saved as the character's submission for the round instead of applied; the GM applies it at a check-in
// (POST /api/campaigns/{id}/round/resolve, noncombat_round_2024.go). The boundary is the effect, not the route's name:
//
//   - routeEffects is the one table: every route a player can send that writes anything, with what it is. A submission (story
//     actions, feature uses, rests, movement, dropping and picking up), gear (buying, selling, putting on or taking off, attuning:
//     ordinary setup until the character's first turn in a scheduled round has resolved, a submission from then on: Matt 11.9),
//     or immediate, with the reason (talk, a choice, settings, account, creation, a fight's own submission, the GM's requests).
//     TestEveryPlayerWriteRouteHasAnEffect fails on a route of the API reference that isn't in it.
//   - POST /api/action is a submission except the action types tagged talk (speak: Matt 11.8).
//   - roundGate is the one place a request becomes a submission (withProtections runs it for every request). A request the
//     server makes for a player (resolving a submission, auto-equip's own steps) doesn't pass through it.
//   - A submission holds at most one story action (an action type that uses the exploration beat): the round is the beat.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// What a request is under the turn schedule.
const (
	effectImmediate  = "immediate"  // applied at once, whatever the schedule
	effectSubmission = "submission" // a game action: saved for the GM to resolve in a round outside a fight
	effectGear       = "gear"       // buying, selling, changing active gear: setup until the character first plays a round, then a submission
)

// How a route names the character it acts for.
const (
	byPath  = "path"  // /api/characters/{id}/...
	byBody  = "body"  // character_id in the body or the query (the handler refuses a request without one)
	byActor = "actor" // the agent's character in its active campaign, as POST /api/action picks it (actingCharacter)
)

// routeEffect: one player route and what it is.
type routeEffect struct {
	Method, Path string // the path as the API reference writes it ({id}, {shop_id} ... match one segment)
	Effect       string
	Character    string // for a submission or gear: how the route names its character
	Why          string // immediate: why it isn't a game action
}

// The reasons a request is immediate, in the words the docs use.
const (
	whyAccount  = "your account"
	whyCampaign = "making or joining a campaign"
	whyTalk     = "talking (Matt 11.8: speech and table talk stay immediate)"
	whyNotes    = "notes and party bookkeeping"
	whyCreation = "character creation (Matt 11.9)"
	whyChoice   = "a choice: a level-up, a feature's option, spells prepared (Matt 11.6: valid choices stay immediate)"
	whySettings = "settings, profile and portrait"
	whyLeaving  = "leaving the game"
	whyFight    = "a fight's own submission or Reaction (combat rounds have their own rules)"
	whyGM       = "the GM's request (the GM resolves rounds; its requests are never submissions)"
	whyFeedback = "feedback to the server's owner"
	whyBags     = "bookkeeping: which bag an item is in"
	whyRestEnd  = "the end of a Short Rest already taken: Hit Point Dice spent one at a time, deciding after each roll (SRD 5.2.1)"
)

var routeEffects = []routeEffect{
	// ---------------------------------------------------------------- game actions
	{"POST", "/api/action", effectSubmission, byActor, ""}, // speak (talk) stays immediate: actionIsImmediate
	{"POST", "/api/campaigns/{id}/battle-map/move", effectSubmission, byBody, ""},
	{"POST", "/api/characters/{id}/rest", effectSubmission, byPath, ""},               // Matt 11.6: a rest is a submission; the GM resolves it
	{"POST", "/api/characters/{id}/short-rest", effectSubmission, byPath, ""},         // the same
	{"POST", "/api/characters/{id}/hit-dice", effectImmediate, "", whyRestEnd},        // only right after a Short Rest (rests_2024.go)
	{"POST", "/api/characters/{id}/starting-level", effectImmediate, "", whyCreation}, // only a character in no campaign (campaign_levels_2024.go)
	{"POST", "/api/characters/{id}/use-resource", effectSubmission, byPath, ""},
	{"POST", "/api/characters/{id}/equipment/use-charge", effectSubmission, byPath, ""},
	{"POST", "/api/characters/{id}/equipment/drop", effectSubmission, byPath, ""},
	{"POST", "/api/characters/{id}/equipment/lose", effectSubmission, byPath, ""},
	{"POST", "/api/characters/{id}/equipment/pick-up", effectSubmission, byPath, ""},
	{"POST", "/api/characters/{id}/equipment/recover", effectSubmission, byPath, ""},
	{"POST", "/api/characters/{id}/cover", effectSubmission, byPath, ""},
	{"POST", "/api/characters/breath-weapon", effectSubmission, byBody, ""},
	{"POST", "/api/characters/wholeness-of-body", effectSubmission, byBody, ""},
	{"POST", "/api/characters/holy-nimbus", effectSubmission, byBody, ""},
	{"POST", "/api/characters/one-with-shadows", effectSubmission, byBody, ""},
	{"POST", "/api/characters/species-ability", effectSubmission, byBody, ""},
	{"POST", "/api/characters/flexible-casting", effectSubmission, byBody, ""},
	{"POST", "/api/characters/mount", effectSubmission, byBody, ""},
	{"POST", "/api/characters/dismount", effectSubmission, byBody, ""},
	{"POST", "/api/characters/downtime", effectSubmission, byBody, ""},
	{"POST", "/api/characters/copy-spell", effectSubmission, byBody, ""},
	{"POST", "/api/conditions/escape", effectSubmission, byBody, ""},
	// ---------------------------------------------------------------- gear (Matt 11.6 and 11.9)
	{"POST", "/api/campaigns/{id}/shops/{shop_id}", effectGear, byBody, ""},
	{"POST", "/api/characters/attune", effectGear, byBody, ""},
	{"POST", "/api/characters/auto-equip", effectGear, byBody, ""},
	{"POST", "/api/characters/equip-armor", effectGear, byBody, ""},
	{"POST", "/api/characters/equip-weapon", effectGear, byBody, ""},
	{"POST", "/api/characters/unequip-armor", effectGear, byBody, ""},
	{"POST", "/api/characters/unequip-weapon", effectGear, byBody, ""},
	// ---------------------------------------------------------------- immediate
	{"POST", "/api/account/email", effectImmediate, "", whyAccount},
	{"POST", "/api/register", effectImmediate, "", whyAccount},
	{"POST", "/api/login", effectImmediate, "", whyAccount},
	{"POST", "/api/verify", effectImmediate, "", whyAccount},
	{"POST", "/api/password-reset/request", effectImmediate, "", whyAccount},
	{"POST", "/api/password-reset/confirm", effectImmediate, "", whyAccount},
	{"POST", "/api/recover/request", effectImmediate, "", whyAccount},
	{"POST", "/api/recover/confirm", effectImmediate, "", whyAccount},
	{"POST", "/api/keys", effectImmediate, "", whyAccount},
	{"DELETE", "/api/keys/{id}", effectImmediate, "", whyAccount},
	{"POST", "/api/tokens", effectImmediate, "", whyAccount},
	{"DELETE", "/api/tokens/{id}", effectImmediate, "", whyAccount},
	{"POST", "/api/feature-requests", effectImmediate, "", whyFeedback},
	{"POST", "/api/campaigns", effectImmediate, "", whyCampaign},
	{"POST", "/api/campaigns/{id}/join", effectImmediate, "", whyCampaign},
	{"POST", "/api/campaigns/{id}/ready", effectImmediate, "", whyCampaign}, // before the start (campaign_ready_2024.go)
	{"POST", "/api/campaigns/{id}/leave", effectImmediate, "", whyCampaign}, // the character leaves the campaign (campaign_leave_2024.go)
	{"POST", "/api/campaigns/{id}/asks", effectImmediate, "", whyGM},        // what the GM is waiting on a player for (gm_asks_2024.go)
	{"DELETE", "/api/campaigns/{id}/asks", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/messages", effectImmediate, "", whyTalk},
	{"POST", "/api/campaigns/{id}/observe", effectImmediate, "", whyNotes},
	{"POST", "/api/observe", effectImmediate, "", whyNotes},
	{"POST", "/api/campaigns/{id}/marching-order", effectImmediate, "", whyNotes},
	{"POST", "/api/characters/{id}/equipment/containers", effectImmediate, "", whyBags},
	{"POST", "/api/characters/{id}/equipment/move", effectImmediate, "", whyBags},
	{"POST", "/api/character-builder", effectImmediate, "", whyCreation},
	{"POST", "/api/character-builder/{id}", effectImmediate, "", whyCreation},
	{"DELETE", "/api/character-builder/{id}", effectImmediate, "", whyCreation},
	{"POST", "/api/character-builder/{id}/finish", effectImmediate, "", whyCreation},
	{"POST", "/api/characters", effectImmediate, "", whyCreation},
	{"POST", "/api/characters/{id}/shop", effectImmediate, "", whyCreation},
	{"POST", "/api/characters/{id}/starting-equipment", effectImmediate, "", whyCreation},
	{"POST", "/api/characters/{id}/asi", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/{id}/class-options", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/{id}/expertise", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/{id}/feat", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/{id}/level-up", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/{id}/level-up-notices", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/{id}/prepare", effectImmediate, "", whyChoice},
	{"PUT", "/api/characters/{id}/spells", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/{id}/tool-proficiencies", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/fighting-style", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/invocations", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/metamagic", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/mystic-arcanum", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/signature-spells", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/spell-mastery", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/memorize", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/subclass", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/subclass-choice", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/weapon-masteries", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/fiendish-resilience", effectImmediate, "", whyChoice},
	{"POST", "/api/characters/reminders", effectImmediate, "", whySettings},
	{"POST", "/api/characters/gear-reminders", effectImmediate, "", whySettings},
	{"POST", "/api/characters/reaction-orders", effectImmediate, "", whySettings},
	{"POST", "/api/characters/{id}/portrait", effectImmediate, "", whySettings},
	{"PUT", "/api/characters/{id}/portrait", effectImmediate, "", whySettings},
	{"DELETE", "/api/characters/{id}/portrait", effectImmediate, "", whySettings},
	{"POST", "/api/characters/{id}/profile", effectImmediate, "", whySettings},
	{"PUT", "/api/characters/{id}/profile", effectImmediate, "", whySettings},
	{"POST", "/api/companions/name", effectImmediate, "", whySettings},
	{"DELETE", "/api/characters/{id}", effectImmediate, "", whyLeaving},
	{"POST", "/api/characters/{id}/delete", effectImmediate, "", whyLeaving},
	{"POST", "/api/pending-turn", effectImmediate, "", whyFight},
	{"DELETE", "/api/pending-turn", effectImmediate, "", whyFight},
	{"POST", "/api/trigger-readied", effectImmediate, "", whyFight},
	{"DELETE", "/api/campaigns/{id}/round", effectImmediate, "", whyFight}, // withdrawing a round's own submission
	// the GM's requests that sit outside /api/gm/ (a player sending one is refused by its handler)
	{"PUT", "/api/campaigns/{id}/battle-map", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/battle-map/preview", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/campaign/npcs", effectImmediate, "", whyGM},
	{"PUT", "/api/campaigns/{id}/campaign/npcs/{npc_id}", effectImmediate, "", whyGM},
	{"DELETE", "/api/campaigns/{id}/campaign/npcs/{npc_id}", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/campaign/quests", effectImmediate, "", whyGM},
	{"PUT", "/api/campaigns/{id}/campaign/quests/{quest_id}", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/campaign/sections", effectImmediate, "", whyGM},
	{"PUT", "/api/campaigns/{id}/campaign/sections/{section_id}", effectImmediate, "", whyGM},
	{"DELETE", "/api/campaigns/{id}/campaign/sections/{section_id}", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/combat/add", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/combat/end", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/combat/next", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/combat/remove", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/combat/resolve", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/combat/skip", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/combat/stand-in", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/combat/start", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/round/resolve", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/round/stand-in", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/round/open", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/end", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/start", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/exploration/skip", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/ground/{dropped_id}/claim", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/ground/{dropped_id}/return", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/items", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/observations/{observation_id}/promote", effectImmediate, "", whyGM},
	{"PATCH", "/api/campaigns/{id}/schedule", effectImmediate, "", whyGM},
	{"PATCH", "/api/campaigns/{id}/levels", effectImmediate, "", whyGM},
	{"PUT", "/api/campaigns/{id}/story", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/table-rules", effectImmediate, "", whyGM},
	{"POST", "/api/campaigns/{id}/test-clock", effectImmediate, "", whyGM},
	{"POST", "/api/characters/{id}/conditions", effectImmediate, "", whyGM},
	{"DELETE", "/api/characters/{id}/conditions", effectImmediate, "", whyGM},
	{"POST", "/api/characters/{id}/damage", effectImmediate, "", whyGM},
	{"POST", "/api/characters/{id}/heal", effectImmediate, "", whyGM},
	{"POST", "/api/characters/{id}/equipment/charges", effectImmediate, "", whyGM},
}

// gmOnlyPrefixes: routes no player sends (every one is the GM's, the moderator's or the admin's); they are never submissions.
var gmOnlyPrefixes = []string{"/api/gm/", "/api/mod/", "/api/admin"}

// pathMatches: a request path against a reference path ({...} is one segment). It answers the {id} segment as a number (0: none).
func pathMatches(pattern, path string) (bool, int) {
	ps, xs := strings.Split(strings.Trim(pattern, "/"), "/"), strings.Split(strings.Trim(path, "/"), "/")
	if len(ps) != len(xs) {
		return false, 0
	}
	id := 0
	for i, p := range ps {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			if xs[i] == "" {
				return false, 0
			}
			if p == "{id}" {
				id, _ = strconv.Atoi(xs[i])
			}
			continue
		}
		if p != xs[i] {
			return false, 0
		}
	}
	return true, id
}

// effectOf: what a request is (ok false: not a player write route of the table).
func effectOf(method, path string) (routeEffect, int, bool) {
	for _, e := range routeEffects {
		if e.Method != method {
			continue
		}
		if ok, id := pathMatches(e.Path, path); ok {
			return e, id, true
		}
	}
	return routeEffect{}, 0, false
}

// actionIsImmediate: an action type that is talk (speak) stays immediate under the schedule (Matt 11.8); an unknown word is left
// to POST /api/action, which refuses it with the character's real actions.
func actionIsImmediate(word string) bool {
	t, ok := lookupActionType(word)
	return !ok || t.hasTag("talk")
}

// actionUsesBeat: an action type that is the turn's story action (anything but the free parts of a turn: speaking, one object,
// dropping something). A round's submission holds at most one.
func actionUsesBeat(word string) bool {
	t, ok := lookupActionType(word)
	return ok && !t.Beat
}

// roundGate: in front of every route (withProtections). A game action of a character whose campaign has a round outside a fight
// open is saved as a submission (round_submissions) and answered 202; everything else goes on to the route. The body is read with
// peekBody, so the route reads it again in full.
func roundGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, p := range gmOnlyPrefixes {
			if strings.HasPrefix(r.URL.Path, p) {
				next.ServeHTTP(w, r)
				return
			}
		}
		e, pathID, ok := effectOf(r.Method, r.URL.Path)
		if !ok || e.Effect == effectImmediate {
			next.ServeHTTP(w, r)
			return
		}
		saved, err := saveIfSubmission(w, r, e, pathID, peekBody(r))
		if err != nil {
			writeDBError(w, err, "the round's submissions")
			return
		}
		if !saved {
			next.ServeHTTP(w, r)
		}
	})
}

// gateFields: the fields the gate reads from a body (the route reads it again in full, with its own checks).
type gateFields struct {
	CharacterID int    `json:"character_id"`
	CampaignID  int    `json:"campaign_id"`
	Action      string `json:"action"`
	Description string `json:"description"`
	CompanionID int    `json:"companion_id"`
}

// saveIfSubmission saves the request as a submission when it is one now, and answers it; saved is false when the request goes on
// to its route (no schedule, no round outside a fight, setup, talk, not the character's own player, or a refusal the route makes).
func saveIfSubmission(w http.ResponseWriter, r *http.Request, e routeEffect, pathID int, raw []byte) (saved bool, err error) {
	agentID, aerr := getAgentFromAuth(r)
	if aerr != nil {
		return false, nil // the route answers 401
	}
	var f gateFields
	if len(bytes.TrimSpace(raw)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		if dec.Decode(&f) != nil {
			return false, nil // the route answers the malformed body
		}
	}
	if f.CharacterID == 0 {
		f.CharacterID, _ = strconv.Atoi(r.URL.Query().Get("character_id"))
	}
	if f.CampaignID == 0 {
		f.CampaignID, _ = strconv.Atoi(r.URL.Query().Get("campaign_id"))
	}
	var charID, lobby int
	switch e.Character {
	case byPath:
		charID = pathID
	case byBody:
		charID = f.CharacterID
	case byActor:
		if actionIsImmediate(f.Action) {
			return false, nil
		}
		charID, lobby, err = actingCharacter(db, r, agentID, f.CharacterID, f.CampaignID)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil // the route answers no_active_game
		}
		if err != nil {
			return false, err
		}
		if t, _ := lookupActionType(f.Action); (t.GM && strings.TrimSpace(f.Description) == "") ||
			(t.Name == "declare" && declarationProblem(charID, f.Description) != "") {
			return false, nil // the route refuses it as it is (description_required, declaration_has_its_own_action): nothing to save
		}
	}
	if charID <= 0 {
		return false, nil // the route refuses a request that names no character
	}
	var owner sql.NullInt64
	var at sql.NullInt64
	if err := db.QueryRow(`SELECT agent_id, lobby_id FROM characters WHERE id = $1`, charID).Scan(&owner, &at); err != nil {
		return false, notFoundIsNothing(err)
	}
	if !owner.Valid || int(owner.Int64) != agentID || !at.Valid {
		return false, nil // not the character's own player (the GM, or a refusal the route makes), or in no campaign
	}
	lobby = int(at.Int64)
	if e.Effect == effectSubmission { // a game action while the GM holds the table for a session zero (table_hold_2024.go)
		if refused, err := refuseWhileHeld(w, lobby); err != nil || refused {
			return refused, err
		}
	}
	sub, err := submissionFor(lobby, charID, e)
	if err != nil || sub == nil {
		return false, err
	}
	story := e.Character == byActor && f.CompanionID == 0 && actionUsesBeat(f.Action)
	reply, status, err := saveSubmission(sub, r, raw, story)
	if err != nil {
		return false, err
	}
	writeJSONStatus(w, status, reply)
	return true, nil
}

// pendingSubmission: a request that is a submission now, for this character and round.
type pendingSubmission struct {
	Lobby, CharacterID int
	Name               string
	Round              *roundRecord
	Effect             routeEffect
}

// submissionFor: whether a request of this character is a submission now (nil: it is applied). It is when the campaign uses the
// schedule (on or paused) and the open round is outside a fight, the character holds a seat, and, for gear, the character has
// played a scheduled round (a turn of it has resolved).
func submissionFor(lobby, charID int, e routeEffect) (*pendingSubmission, error) {
	state, err := scheduleStateOf(db, lobby)
	if err != nil || !usesSchedule(state) {
		return nil, notFoundIsNothing(err)
	}
	round, err := openRoundOf(db, lobby)
	if err != nil || round == nil || round.Kind != roundNoncombat {
		return nil, err
	}
	var name string
	err = db.QueryRow(`SELECT c.name FROM campaign_seats s JOIN characters c ON c.id = s.character_id WHERE s.lobby_id = $1 AND s.character_id = $2`, lobby, charID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // no seat: not a player of the schedule
	}
	if err != nil {
		return nil, err
	}
	if e.Effect == effectGear {
		played, err := hasPlayedARound(lobby, charID)
		if err != nil || !played {
			return nil, err
		}
	}
	return &pendingSubmission{Lobby: lobby, CharacterID: charID, Name: name, Round: round, Effect: e}, nil
}

// hasPlayedARound: a turn of this character in a scheduled round of the campaign has resolved (before that its gear and shopping
// are setup: Matt 11.9).
func hasPlayedARound(lobby, charID int) (bool, error) {
	var played bool
	err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM turn_results t JOIN campaign_rounds r ON r.id = t.round_id
		WHERE r.lobby_id = $1 AND t.combatant_id = $2)`, lobby, charID).Scan(&played)
	return played, err
}

// saveSubmission stores the request as the next step of the character's submission for the open round and answers what was saved,
// with the reminder (Matt, section 4). A character whose turn has resolved, or a second story action, is refused (409).
func saveSubmission(s *pendingSubmission, r *http.Request, raw []byte, story bool) (map[string]interface{}, int, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT id FROM campaign_rounds WHERE id = $1 FOR UPDATE`, s.Round.ID); err != nil {
		return nil, 0, err
	}
	var done string
	err = tx.QueryRow(`SELECT how FROM turn_results WHERE round_id = $1 AND combatant_id = $2 AND NOT second_turn`, s.Round.ID, s.CharacterID).Scan(&done)
	if err == nil {
		return map[string]interface{}{"error": codeTurnAlreadyResolve, "round": s.Round.Number,
			"message": fmt.Sprintf("%s's turn in round %d has resolved (%s): it is done for this round. Round %d begins when every turn of this round has resolved; send it then. Nothing was saved.",
				s.Name, s.Round.Number, turnResultHows[done], s.Round.Number+1)}, http.StatusConflict, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, 0, err
	}
	if story {
		var what string
		var m, p, b string
		err := tx.QueryRow(`SELECT method, path, body FROM round_submissions WHERE round_id = $1 AND character_id = $2 AND status = 'pending' AND story LIMIT 1`, s.Round.ID, s.CharacterID).Scan(&m, &p, &b)
		what = submissionWhat(m, p, []byte(b))
		if err == nil {
			return map[string]interface{}{"error": "one_story_action_per_round", "round": s.Round.Number,
				"message": fmt.Sprintf("%s's submission for round %d already holds a story action (%s): a round is one turn, with one action. To change it, withdraw the submission (DELETE /api/campaigns/%d/round?character_id=%d) and send the turn again. Nothing was saved.",
					s.Name, s.Round.Number, what, s.Lobby, s.CharacterID)}, http.StatusConflict, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, 0, err
		}
	}
	now, err := campaignNow(tx, s.Lobby)
	if err != nil {
		return nil, 0, err
	}
	path := r.URL.Path
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	what := submissionWhat(r.Method, r.URL.Path, raw)
	var step int
	if err := tx.QueryRow(`INSERT INTO round_submissions (round_id, character_id, step, method, path, body, what, story, submitted_at)
		VALUES ($1, $2, (SELECT COALESCE(MAX(step), 0) + 1 FROM round_submissions WHERE round_id = $1 AND character_id = $2), $3, $4, $5, $6, $7, $8)
		RETURNING step`, s.Round.ID, s.CharacterID, r.Method, path, string(raw), what, story, now).Scan(&step); err != nil {
		return nil, 0, err
	}
	if err := writeAction(tx, s.Lobby, s.CharacterID, "submission", fmt.Sprintf("%s submits for round %d: %s", s.Name, s.Round.Number, what), ""); err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	reply := map[string]interface{}{"success": true, "submitted": true, "applied": false, "round": s.Round.Number, "step": step, "what": what,
		"message": submittedReminder(s, now),
		"note":    fmt.Sprintf("The turn schedule saves a game action outside a fight as your submission for the round: the GM applies it at a check-in, and the server checks it then. GET /api/campaigns/%d/round shows your submission and the round; DELETE /api/campaigns/%d/round?character_id=%d withdraws it.", s.Lobby, s.Lobby, s.CharacterID)}
	return reply, http.StatusAccepted, nil
}

// submittedReminder: "Your submission is saved for round 3; your next check-in is 10:30 UTC." (Matt, section 4), from the seat's
// time and the round's final check-in.
func submittedReminder(s *pendingSubmission, now time.Time) string {
	msg := fmt.Sprintf("Your submission is saved for round %d (outside a fight); the GM resolves it at a check-in.", s.Round.Number)
	if next, ok := playerNextCheckIn(s.Lobby, s.CharacterID, s.Round, now); ok {
		msg = fmt.Sprintf("Your submission is saved for round %d (outside a fight); your next check-in is %s.", s.Round.Number, clockText(next))
	}
	return msg
}

// playerNextCheckIn: the next time a seat's check-in comes, at or after now (ok false: the seat has no time, or the schedule is
// paused).
func playerNextCheckIn(lobby, charID int, round *roundRecord, now time.Time) (time.Time, bool) {
	cs, err := loadSchedule(db, lobby)
	if err != nil || cs.State != scheduleOn || round == nil || !round.Deadline.Valid {
		return time.Time{}, false
	}
	s, ok := scheduleOf(cs.MaxPlayers)
	if !ok {
		return time.Time{}, false
	}
	for _, h := range cs.Seats {
		if h.CharacterID == charID {
			if m, has := s.seatMinute(h.Seat); has {
				return nextAt(now, m), true
			}
		}
	}
	return time.Time{}, false
}

// submissionWhat: a submission's step for the log, the round view and the replies ("action search: I search the room"), with the
// player's whole description: the GM decides on it before resolving it (playtest I-GM-9).
func submissionWhat(method, path string, raw []byte) string {
	var m map[string]interface{}
	json.Unmarshal(raw, &m)
	what := method + " " + path
	if a, ok := m["action"].(string); ok && path == "/api/action" {
		what = "action " + canonicalAction(a)
	}
	if d, ok := m["description"].(string); ok && strings.TrimSpace(d) != "" {
		what += ": " + strings.TrimSpace(d)
	}
	return what
}
