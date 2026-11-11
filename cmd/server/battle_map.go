package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type mapSquare struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type mapToken struct {
	ID          int          `json:"id"`
	Name        string       `json:"name"`
	Position    mapSquare    `json:"position"`
	Size        int          `json:"size"`
	Hidden      bool         `json:"hidden,omitempty"`
	Paused      bool         `json:"paused,omitempty"`
	Altitude    int          `json:"altitude,omitempty"` // feet above the ground: a flying creature (Fly, Gaseous Form; spells_more_2024.go)
	FreeTerrain bool         `json:"-"`                  // Freedom of Movement: Difficult Terrain costs nothing extra (the move handler sets it)
	Passable    map[int]bool `json:"-"`                  // tokens whose spaces it may move through (map_spaces_2024.go; the move handler sets it)
}

type encounterMap struct {
	Grid       []string    `json:"grid"`
	Tokens     []mapToken  `json:"tokens"`
	Interrupts []mapSquare `json:"interrupts,omitempty"`
	Revision   string      `json:"revision"`
}

type mapRequest struct {
	CharacterID    int          `json:"character_id"`
	Map            encounterMap `json:"map"`
	Path           []mapSquare  `json:"path"`
	Confirmation   string       `json:"confirmation"`
	ForcedMovement bool         `json:"forced_movement"` // GM map update: the moves were forced or teleports (no Opportunity Attacks)
	Disengaged     []int        `json:"disengaged"`      // GM map update: monsters that took the Disengage action this turn
	CompanionID    int          `json:"companion_id"`    // player move: one of the character's companions instead of the character
	Altitude       *int         `json:"altitude"`        // player move: the height (feet) to end at; a Fly Speed is needed above 0
}

func mapRevision() string {
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])
	return hex.EncodeToString(bytes[:])
}

func (m encounterMap) tile(p mapSquare) byte {
	if p.Y < 0 || p.Y >= len(m.Grid) || p.X < 0 || p.X >= len(m.Grid[p.Y]) {
		return '#'
	}
	return m.Grid[p.Y][p.X]
}

const battleMapStepFeet = 5

func blockedTile(tile byte) bool { return tile == '#' || tile == 'D' }

func (m encounterMap) validate() error {
	if len(m.Grid) == 0 || len(m.Grid) > 40 || len(m.Grid[0]) == 0 || len(m.Grid[0]) > 40 {
		return errors.New("grid must be between 1 and 40 squares on each axis")
	}
	for _, row := range m.Grid {
		if len(row) != len(m.Grid[0]) || strings.ContainsAny(row, "? ") {
			return errors.New("grid rows must have the same width and use the map legend")
		}
		for _, tile := range row {
			if !strings.ContainsRune(".#D/~", tile) {
				return errors.New("unknown terrain symbol")
			}
		}
	}
	occupied := map[mapSquare]bool{}
	ids := map[int]bool{}
	for _, token := range m.Tokens {
		if token.ID == 0 || ids[token.ID] || token.Name == "" || token.Size < 1 || token.Size > 4 {
			return errors.New("tokens need unique nonzero IDs, names, and footprints of 1–4 squares per side")
		}
		ids[token.ID] = true
		for _, square := range token.footprint(token.Position) {
			if blockedTile(m.tile(square)) || occupied[square] {
				return errors.New("token footprints must fit on open, unoccupied squares")
			}
			occupied[square] = true
		}
	}
	for _, p := range m.Interrupts {
		if blockedTile(m.tile(p)) {
			return errors.New("interruption squares must be on open terrain")
		}
	}
	return nil
}

func (t mapToken) footprint(p mapSquare) []mapSquare {
	squares := make([]mapSquare, 0, t.Size*t.Size)
	for y := 0; y < t.Size; y++ {
		for x := 0; x < t.Size; x++ {
			squares = append(squares, mapSquare{p.X + x, p.Y + y})
		}
	}
	return squares
}

func mapAbs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// mapVisible traces the grid from cell center to cell center. Two blocked
// orthogonal neighbors close a diagonal corner.
func (m encounterMap) mapVisible(from, to mapSquare) bool {
	dx, dy := mapAbs(to.X-from.X), mapAbs(to.Y-from.Y)
	sx, sy := 1, 1
	if to.X < from.X {
		sx = -1
	}
	if to.Y < from.Y {
		sy = -1
	}
	p := from
	for ix, iy := 0, 0; ix < dx || iy < dy; {
		a, b := (1+2*ix)*dy, (1+2*iy)*dx
		switch {
		case a == b:
			if blockedTile(m.tile(mapSquare{p.X + sx, p.Y})) && blockedTile(m.tile(mapSquare{p.X, p.Y + sy})) {
				return false
			}
			p.X += sx
			p.Y += sy
			ix++
			iy++
		case a < b:
			p.X += sx
			ix++
		default:
			p.Y += sy
			iy++
		}
		if p != to && blockedTile(m.tile(p)) {
			return false
		}
	}
	return true
}

func (m encounterMap) playerView(viewer mapToken) encounterMap {
	view := encounterMap{Grid: make([]string, len(m.Grid)), Tokens: []mapToken{}} // no revision: it is the GM's edit token
	for y, row := range m.Grid {
		visible := []byte(row)
		for x := range visible {
			if !m.mapVisible(viewer.Position, mapSquare{x, y}) {
				visible[x] = '?'
			}
		}
		view.Grid[y] = string(visible)
	}
	for _, t := range m.Tokens {
		if t.ID == viewer.ID {
			t.Hidden = false
			view.Tokens = append(view.Tokens, t)
			continue
		}
		if t.Hidden {
			continue
		}
		// An exposed corner must not reveal the coordinates of a concealed footprint.
		allVisible := true
		for _, p := range t.footprint(t.Position) {
			if view.tile(p) == '?' {
				allVisible = false
				break
			}
		}
		if allVisible {
			t.Paused = false
			view.Tokens = append(view.Tokens, t)
		}
	}
	return view
}

func (m encounterMap) stepCost(actor mapToken, from, to mapSquare, prone bool) (int, bool) {
	// Bounds first, so arbitrary client integers cannot overflow deltas.
	if blockedTile(m.tile(to)) || (from == to) || mapAbs(to.X-from.X) > 1 || mapAbs(to.Y-from.Y) > 1 {
		return 0, false
	}
	cost := battleMapStepFeet
	for _, p := range actor.footprint(to) {
		if blockedTile(m.tile(p)) {
			return 0, false
		}
		if from.X != to.X && from.Y != to.Y {
			if blockedTile(m.tile(mapSquare{p.X, p.Y + from.Y - to.Y})) || blockedTile(m.tile(mapSquare{p.X + from.X - to.X, p.Y})) {
				return 0, false
			}
		}
		if m.tile(p) == '~' && !actor.FreeTerrain {
			cost = 2 * battleMapStepFeet
		}
		for _, t := range m.Tokens {
			if t.ID != actor.ID && p.X >= t.Position.X && p.X < t.Position.X+t.Size && p.Y >= t.Position.Y && p.Y < t.Position.Y+t.Size {
				if !actor.Passable[t.ID] {
					return 0, false
				}
				if !actor.FreeTerrain {
					cost = 2 * battleMapStepFeet // another creature's space is Difficult Terrain (F115)
				}
			}
		}
	}
	if prone {
		cost += battleMapStepFeet
	}
	return cost, true
}

func mapPathConfirmation(revision string, started time.Time, actor, movement int, conditions []byte, path []mapSquare) string {
	payload, _ := json.Marshal([]any{revision, started, actor, movement, string(conditions), path})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// stepProblem says why a step is not allowed, using only what the mover can see.
func (m encounterMap) stepProblem(actor mapToken, from, to mapSquare) string {
	switch {
	case from == to:
		return "that is the square you are already on"
	case mapAbs(to.X-from.X) > 1 || mapAbs(to.Y-from.Y) > 1:
		return "steps must go to an adjacent square (list every square of the path, without the starting square)"
	case m.tile(to) == '#':
		return "there is a wall in that square"
	case m.tile(to) == 'D':
		return "there is a closed door in that square (the GM opens it)"
	case m.tile(to) == '?':
		return "you cannot see that square"
	}
	for _, p := range actor.footprint(to) {
		if blockedTile(m.tile(p)) {
			return "your size does not fit through there"
		}
		for _, t := range m.Tokens {
			if t.ID != actor.ID && p.X >= t.Position.X && p.X < t.Position.X+t.Size && p.Y >= t.Position.Y && p.Y < t.Position.Y+t.Size {
				return t.Name + " is standing there (you can move through the space of an ally, an Incapacitated or Tiny creature, or a creature two sizes larger or smaller than you; a Halfling, any creature larger than it)"
			}
		}
	}
	if from.X != to.X && from.Y != to.Y {
		return "a diagonal step cannot cut past a wall or closed door"
	}
	return "that step is not allowed"
}

func battleMapReply(w http.ResponseWriter, status int, body any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func battleMapError(w http.ResponseWriter, status int, code string) {
	battleMapReply(w, status, map[string]string{"error": code})
}

// handleBattleMap godoc
// @Summary The battle map of a fight
// @Description During combat. GET shows the map: the GM sees everything, a player adds ?character_id=N for that character's view. GM: POST /battle-map/preview with a map object checks it, PUT /battle-map publishes or updates it (forced_movement: the moves were forced or teleports, so no Opportunity Attacks; disengaged: monster ids that took Disengage; moving a monster into Spirit Guardians triggers its save; a creature a Magic Circle keeps out stays where it was; a token's altitude is its height in feet). Player: POST /battle-map/preview with character_id and the ordered path of adjacent squares returns the cost and a confirmation; POST /battle-map/move with the same path and that confirmation moves the token and spends the movement (Opportunity Attacks happen as the path leaves a monster's reach). altitude: the height in feet to end at, for a creature with a Fly Speed (Fly, Gaseous Form); the climb costs movement and the height counts in distances.
// @Tags Campaigns
// @Param id path int true "Campaign ID"
// @Param character_id query int false "A player's character (GET)"
// @Param request body object{map=object,character_id=int,path=[]object,confirmation=string,forced_movement=bool,disengaged=[]int,altitude=int} false "Map (GM) or path (player)"
// @Router /campaigns/{id}/battle-map [get]
// @Router /campaigns/{id}/battle-map [put]
// @Router /campaigns/{id}/battle-map/preview [post]
// @Router /campaigns/{id}/battle-map/move [post]
func handleBattleMap(w http.ResponseWriter, r *http.Request, campaignID int, route []string) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	operation := ""
	if len(route) == 1 {
		operation = route[0]
	} else if len(route) > 1 {
		http.NotFound(w, r)
		return
	}
	pruneDeadTokens(campaignID) // defeated monsters leave the map
	var req mapRequest
	if r.Method == http.MethodGet {
		req.CharacterID, _ = strconv.Atoi(r.URL.Query().Get("character_id"))
	} else {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&req); err != nil {
			battleMapError(w, 400, "invalid_map_request")
			return
		}
	}
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		battleMapError(w, 500, "map_unavailable")
		return
	}
	defer tx.Rollback()
	var gm, turn int
	var started time.Time
	var raw, order []byte
	err = tx.QueryRow(`
  SELECT l.dm_id, c.current_turn_index, c.turn_started_at, c.battle_map, c.turn_order
  FROM combat_state c JOIN lobbies l ON l.id=c.lobby_id
  WHERE c.lobby_id=$1 AND c.active AND l.status='active' FOR UPDATE OF c
 `, campaignID).Scan(&gm, &turn, &started, &raw, &order)
	if errors.Is(err, sql.ErrNoRows) {
		battleMapError(w, 404, "no_active_encounter")
		return
	}
	if err != nil {
		battleMapError(w, 500, "map_unavailable")
		return
	}
	var m encounterMap
	if len(raw) > 0 && json.Unmarshal(raw, &m) != nil {
		battleMapError(w, 500, "invalid_stored_map")
		return
	}
	var initiative []struct {
		ID int `json:"id"`
	}
	if json.Unmarshal(order, &initiative) != nil {
		battleMapError(w, 500, "invalid_initiative")
		return
	}

	if agentID == gm {
		switch {
		case r.Method == http.MethodGet && operation == "":
			if len(raw) == 0 {
				battleMapError(w, 404, "no_battle_map")
				return
			}
			battleMapReply(w, 200, m)
		case (r.Method == http.MethodPut && operation == "") || (r.Method == http.MethodPost && operation == "preview"):
			if err := req.Map.validate(); err != nil {
				battleMapReply(w, 400, map[string]string{"error": "invalid_map", "message": publicError(err)})
				return
			}
			participants := map[int]bool{}
			for _, entry := range initiative {
				participants[entry.ID] = true
			}
			for _, id := range activeCompanionCombatIDs(campaignID) { // companions act on their owner's turn but can stand on the map
				participants[id] = true
			}
			for _, token := range req.Map.Tokens {
				if !participants[token.ID] {
					battleMapError(w, 400, "token_not_in_initiative")
					return
				}
			}
			if operation == "preview" {
				battleMapReply(w, 200, req.Map)
				return
			}
			if req.Confirmation != m.Revision {
				battleMapError(w, 409, "stale_map")
				return
			}
			var circleNotes []string
			if !req.ForcedMovement {
				circleNotes = magicCircleHolds(campaignID, m, &req.Map) // the chosen creatures can't cross a Magic Circle by moving (spells_more_2024.go)
			}
			req.Map.Revision = mapRevision()
			updated, _ := json.Marshal(req.Map)
			if _, err := tx.Exec("UPDATE combat_state SET battle_map=$1 WHERE lobby_id=$2", updated, campaignID); err != nil {
				battleMapError(w, 500, "map_write_failed")
				return
			}
			// GM map changes are recorded without publishing coordinates or hidden features.
			if _, err := tx.Exec("INSERT INTO actions (lobby_id, action_type, description, result) VALUES ($1,'map_update','GM updated the battle map','Map updated')", campaignID); err != nil || tx.Commit() != nil {
				battleMapError(w, 500, "map_write_failed")
				return
			}
			// a monster that moved out of a character's reach by its own movement lets that character make an Opportunity Attack;
			// forced movement (pushed, knocked back), teleporting and Disengage don't provoke (character_oa_2024.go)
			if !req.ForcedMovement {
				for _, note := range noteMonstersLeavingReach(campaignID, m, req.Map, req.Disengaged) {
					recordGMAction(db, campaignID, "opportunity_attack_available", note, note)
				}
				for _, note := range autoOpportunityAttacks(campaignID) { // standing reaction orders (F220)
					recordGMAction(db, campaignID, "opportunity_attack", note, note)
				}
			}
			for _, note := range spiritGuardiansEnter(campaignID, m, req.Map) { // entering Spirit Guardians (any movement)
				recordGMAction(db, campaignID, "condition", note, note)
			}
			for _, note := range circleNotes {
				recordGMAction(db, campaignID, "spell_effect", note, note)
			}
			battleMapReply(w, 200, req.Map)
		default:
			battleMapError(w, 405, "invalid_map_operation")
		}
		return
	}

	if r.Method == http.MethodPut || len(req.Map.Grid) > 0 {
		battleMapReply(w, 403, map[string]string{"error": "not_gm", "message": "Only the GM of this campaign can publish or preview a map. Players read their view with GET and move with preview and move."})
		return
	}
	if req.CharacterID == 0 {
		battleMapReply(w, 400, map[string]string{"error": "character_id_required", "message": "Send character_id (your character in this campaign)."})
		return
	}
	if r.Method == http.MethodPost && db != nil {
		if why := turnLimitProblem(req.CharacterID, "move"); why != "" { // Command (Halt, Grovel, Drop), Haste's lethargy
			battleMapReply(w, 409, map[string]string{"error": "spell_effect_limit", "message": why})
			return
		}
	}
	var hp, movement, exhaustion int
	var dead bool
	var conditions []byte
	err = tx.QueryRow(`SELECT hp, COALESCE(is_dead,false), COALESCE(movement_remaining,30),
 COALESCE(exhaustion_level,0), COALESCE(conditions,'[]') FROM characters
 WHERE id=$1 AND lobby_id=$2 AND agent_id=$3 FOR UPDATE`, req.CharacterID, campaignID, agentID).
		Scan(&hp, &dead, &movement, &exhaustion, &conditions)
	if errors.Is(err, sql.ErrNoRows) {
		battleMapError(w, 403, "character_not_in_campaign")
		return
	}
	if err != nil {
		battleMapError(w, 500, "map_unavailable")
		return
	}
	if req.CompanionID != 0 {
		order := make([]int, len(initiative))
		for i, e := range initiative {
			order[i] = e.ID
		}
		companionMapMove(w, r, tx, campaignID, m, order, turn, started, req, operation)
		return
	}
	actorIndex := -1
	for i, t := range m.Tokens {
		if t.ID == req.CharacterID {
			actorIndex = i
			break
		}
	}
	if actorIndex < 0 {
		battleMapError(w, 404, "no_character_token")
		return
	}
	actor := m.Tokens[actorIndex]
	view := m.playerView(actor)
	if r.Method == http.MethodGet && operation == "" {
		battleMapReply(w, 200, map[string]any{"map": view, "position": actor.Position, "movement_remaining_ft": movement})
		return
	}
	if r.Method != http.MethodPost || (operation != "preview" && operation != "move") {
		battleMapError(w, 405, "invalid_map_operation")
		return
	}
	if turn < 0 || turn >= len(initiative) || initiative[turn].ID != actor.ID {
		battleMapError(w, 409, "not_your_turn")
		return
	}
	if actor.Paused {
		battleMapError(w, 409, "awaiting_gm_map_ruling")
		return
	}
	prone := false
	remainingConditions := []string{}
	for _, condition := range parseConditionsJSON(conditions) {
		switch strings.SplitN(strings.ToLower(condition), ":", 2)[0] {
		case "grappled", "restrained", "paralyzed", "unconscious", "petrified":
			battleMapError(w, 409, "cannot_move")
			return
		case "prone":
			prone = true
		}
		if condition != "invisible:one_with_shadows" {
			remainingConditions = append(remainingConditions, condition)
		}
	}
	if hp <= 0 || dead || exhaustion >= 6 {
		battleMapError(w, 409, "cannot_move")
		return
	}
	if (len(req.Path) == 0 && req.Altitude == nil) || len(req.Path) > 120 {
		battleMapError(w, 400, "invalid_path_length")
		return
	}
	actor.FreeTerrain = buffIgnoresDifficult(actor.ID)    // Freedom of Movement (buff_spells_2024.go)
	actor.Passable = passableTokens(campaignID, m, actor) // F115 (map_spaces_2024.go)
	confirmation := mapPathConfirmation(m.Revision, started, actor.ID, movement, conditions, req.Path)
	if operation == "move" && req.Confirmation != confirmation {
		battleMapError(w, 409, "stale_path_preview")
		return
	}

	cost := 0
	from := actor.Position
	for i, to := range req.Path {
		step, ok := view.stepCost(actor, from, to, prone)
		if !ok || cost+step > movement {
			reason := "not enough movement: this step costs " + strconv.Itoa(step) + " ft and you have " + strconv.Itoa(movement-cost) + " ft left"
			if !ok {
				reason = view.stepProblem(actor, from, to)
			}
			battleMapReply(w, 400, map[string]any{"error": "invalid_path_step", "reason": reason, "step": i + 1, "movement_remaining_ft": movement - cost})
			return
		}
		cost += step
		from = to
	}
	if len(req.Path) > 0 {
		if who := view.endsInCreature(actor, from); who != "" {
			battleMapReply(w, 400, map[string]any{"error": "invalid_path_step", "reason": "you can't end your move in " + who + "'s space", "step": len(req.Path), "movement_remaining_ft": movement - cost})
			return
		}
	}
	climb := 0
	if req.Altitude != nil { // rising or descending costs a foot of movement per foot (spells_more_2024.go)
		if why := altitudeProblem(campaignID, actor.ID, *req.Altitude); why != "" {
			battleMapReply(w, 400, map[string]any{"error": "cannot_fly", "reason": why})
			return
		}
		climb = mapAbs(*req.Altitude - actor.Altitude)
		if cost+climb > movement {
			battleMapReply(w, 400, map[string]any{"error": "invalid_path_step", "reason": fmt.Sprintf("not enough movement: the path costs %d ft and the climb %d ft, and you have %d ft", cost, climb, movement), "step": len(req.Path), "movement_remaining_ft": movement - cost})
			return
		}
		cost += climb
	}
	if operation == "preview" {
		battleMapReply(w, 200, map[string]any{"confirmation": confirmation, "start": actor.Position, "path": req.Path, "distance_ft": len(req.Path) * battleMapStepFeet, "cost_ft": cost, "movement_remaining_ft": movement - cost})
		return
	}

	traversed := []mapSquare{}
	cost = 0
	for _, to := range req.Path {
		step, ok := m.stepCost(actor, actor.Position, to, prone)
		if !ok || cost+step > movement {
			actor.Paused = true
			break
		}
		actor.Position = to
		traversed = append(traversed, to)
		cost += step
		for _, p := range m.Interrupts {
			for _, occupied := range actor.footprint(to) {
				if p == occupied {
					actor.Paused = true
				}
			}
		}
		if actor.Paused {
			break
		}
	}
	if req.Altitude != nil && !actor.Paused {
		actor.Altitude = *req.Altitude
		cost += climb
	}
	startToken := m.Tokens[actorIndex]
	opportunity := opportunityTriggers(campaignID, m, startToken, traversed) // monsters whose reach the character leaves
	m.Tokens[actorIndex] = actor
	m.Revision = mapRevision()
	updated, _ := json.Marshal(m)
	if _, err := tx.Exec("UPDATE combat_state SET battle_map=$1 WHERE lobby_id=$2", updated, campaignID); err != nil {
		battleMapError(w, 500, "map_write_failed")
		return
	}
	if cost > 0 {
		remainingJSON, _ := json.Marshal(remainingConditions)
		if _, err := tx.Exec("UPDATE characters SET movement_remaining=$1, conditions=$2 WHERE id=$3", movement-cost, remainingJSON, actor.ID); err != nil {
			battleMapError(w, 500, "map_write_failed")
			return
		}
	}
	if _, err := tx.Exec("INSERT INTO actions (lobby_id,character_id,action_type,description,result) VALUES ($1,$2,'map_move','Moved on the battle map',$3)", campaignID, actor.ID, fmt.Sprintf("%d ft of movement spent", cost)); err != nil || tx.Commit() != nil {
		battleMapError(w, 500, "map_write_failed")
		return
	}
	if cost > 0 {
		syncRecordsFromList(actor.ID)
	}
	reply := map[string]any{"position": actor.Position, "traversed": traversed, "interrupted": actor.Paused, "distance_ft": len(traversed) * battleMapStepFeet, "cost_ft": cost, "movement_remaining_ft": movement - cost, "map": m.playerView(actor)}
	if len(opportunity) > 0 {
		reply["opportunity_attacks"] = resolveOpportunityAttacks(r, campaignID, actor.ID, opportunity)
	}
	battleMapReply(w, 200, reply)
}
