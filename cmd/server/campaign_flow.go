package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func isMeaningfulActionType(actionType string) bool {
	switch strings.ToLower(strings.TrimSpace(actionType)) {
	case "", "poll", "joined", "following":
		return false
	default:
		return true
	}
}

// isFollowingParty reports whether the system's latest action for a character
// has already placed them on automatic follow. A later meaningful player
// action re-enables normal inactivity tracking.
func isFollowingParty(lastMeaningfulAction, lastFollowing sql.NullTime) bool {
	return lastFollowing.Valid && (!lastMeaningfulAction.Valid || !lastFollowing.Time.Before(lastMeaningfulAction.Time))
}

// allPartyUnableToAct detects encounters that cannot progress through player
// turns. Stable characters at zero HP remain unconscious; dead characters
// cannot take turns either. An empty roster is not a party defeat.
func allPartyUnableToAct(party []map[string]interface{}) bool {
	if len(party) == 0 {
		return false
	}
	for _, character := range party {
		status, ok := character["status"].(string)
		if !ok || (status != "stable" && status != "dead") {
			return false
		}
	}
	return true
}

func lastNarrationTime(lobbyID int) time.Time {
	var ts sql.NullTime
	db.QueryRow(`
		SELECT created_at
		FROM actions
		WHERE lobby_id = $1 AND action_type = 'narration'
		ORDER BY created_at DESC
		LIMIT 1
	`, lobbyID).Scan(&ts)
	if ts.Valid {
		return ts.Time
	}
	return time.Time{}
}

// latestMeaningfulPlayerActionTime deliberately ignores heartbeat polls, join
// records, and automatic following records. They are reads/administrative
// events, not story beats, and must not make a freshly written stateless-player
// recap look stale.
func latestMeaningfulPlayerActionTime(lobbyID int) sql.NullTime {
	var latest sql.NullTime
	db.QueryRow(`
		SELECT created_at
		FROM actions
		WHERE lobby_id = $1
		  AND character_id IS NOT NULL
		  AND action_type NOT IN ('poll', 'joined', 'following', 'drop', 'pick_up', 'species_ability')
		ORDER BY created_at DESC
		LIMIT 1
	`, lobbyID).Scan(&latest)
	return latest
}

func hasCharacterActedSinceLastNarration(charID, lobbyID int) (bool, string, string, time.Time) {
	narrationAt := lastNarrationTime(lobbyID)

	var actionType, description sql.NullString
	var createdAt sql.NullTime
	query := `
		SELECT action_type, COALESCE(description, ''), created_at
		FROM actions
		WHERE lobby_id = $1
		  AND character_id = $2
		  AND action_type NOT IN ('poll', 'joined', 'following', 'drop', 'pick_up', 'species_ability'` + beatFreeTypesSQL() + `)
		  AND COALESCE(result, '') NOT LIKE 'Cannot %' -- a refused action spent nothing: it doesn't use the exploration turn
	`
	args := []interface{}{lobbyID, charID}
	if !narrationAt.IsZero() {
		query += ` AND created_at > $3`
		args = append(args, narrationAt)
	}
	query += `
		ORDER BY created_at DESC
		LIMIT 1
	`
	err := db.QueryRow(query, args...).Scan(&actionType, &description, &createdAt)
	if err != nil {
		return false, "", "", time.Time{}
	}
	return true, actionType.String, description.String, createdAt.Time
}

// actionReplaySinceLastNarration identifies an exact retry of a recorded
// exploration action. Clients can safely retry after a timeout without being
// told their already-recorded action was rejected.
func actionReplaySinceLastNarration(charID, lobbyID int, actionType, description string) (bool, string, time.Time) {
	narrationAt := lastNarrationTime(lobbyID)

	var result sql.NullString
	var createdAt sql.NullTime
	query := `
		SELECT COALESCE(result, ''), created_at
		FROM actions
		WHERE lobby_id = $1
		  AND character_id = $2
		  AND action_type = $3
		  AND COALESCE(description, '') = $4
		  AND COALESCE(result, '') NOT LIKE 'Cannot %' -- a refusal isn't a receipt to replay: the retry runs again
	`
	args := []interface{}{lobbyID, charID, actionType, description}
	if !narrationAt.IsZero() {
		query += ` AND created_at > $5`
		args = append(args, narrationAt)
	}
	query += ` ORDER BY created_at DESC LIMIT 1`
	if err := db.QueryRow(query, args...).Scan(&result, &createdAt); err != nil {
		return false, "", time.Time{}
	}
	return true, result.String, createdAt.Time
}

// beatFreeTypesSQL: the action types that don't use up the exploration beat (speaking, one object ...), as more entries of a SQL
// NOT IN list: they are the action table's own names, never a player's text.
func beatFreeTypesSQL() string {
	var b strings.Builder
	for _, t := range actionTypes {
		if t.Beat {
			b.WriteString(", '" + t.Name + "'")
		}
	}
	return b.String()
}

// shouldAllowExplorationFollowup: outside a fight, this action doesn't use up the character's beat between the GM's narrations
// (speaking, one object, dropping something, a Death Saving Throw): the action table's Beat (action_types_2024.go).
func shouldAllowExplorationFollowup(actionType string) bool {
	t, ok := lookupActionType(actionType)
	return ok && t.Beat
}

// openSeats is the rule of a campaign's player seats, for the campaign list and for joining: the seats left, or nil when
// the campaign has no limit (max_players 0 or less). A campaign is full when it has a limit and no seat left.
func openSeats(maxPlayers, playerCount int) interface{} {
	if maxPlayers <= 0 {
		return nil
	}
	if left := maxPlayers - playerCount; left > 0 {
		return left
	}
	return 0
}

func campaignFull(maxPlayers, playerCount int) bool { return openSeats(maxPlayers, playerCount) == 0 }

// campaignListFilter reads GET /api/campaigns' filters: open (only campaigns with a seat left) and level (only campaigns a
// character of that level may join). A bad value is refused, naming what is accepted.
type campaignFilter struct {
	open  bool
	level int
}

func campaignListFilter(r *http.Request) (campaignFilter, map[string]interface{}) {
	var f campaignFilter
	q := r.URL.Query()
	if v := strings.ToLower(strings.TrimSpace(q.Get("open"))); v != "" {
		switch v {
		case "true", "1", "yes":
			f.open = true
		case "false", "0", "no":
		default:
			return f, map[string]interface{}{"error": "invalid_query", "fields": []string{"open"},
				"message": "open is true or false (only campaigns with an open player seat)."}
		}
	}
	if v := strings.TrimSpace(q.Get("level")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 20 {
			return f, map[string]interface{}{"error": "invalid_query", "fields": []string{"level"},
				"message": "level is a character level, 1 to 20 (only campaigns a character of that level may join)."}
		}
		f.level = n
	}
	return f, nil
}

func (f campaignFilter) keeps(seats interface{}, minLevel, maxLevel int) bool {
	if f.open && seats == 0 {
		return false
	}
	return f.level == 0 || (f.level >= minLevel && f.level <= maxLevel)
}

func displayedMaxPlayers(maxPlayers, playerCount int) int {
	if playerCount > maxPlayers {
		return playerCount
	}
	return maxPlayers
}

// startCampaignAutoAdvanceWorker starts a background goroutine that auto-advances stalled campaigns
// Autonomous GM - campaigns run without human intervention
// Runs every 30 minutes, checking all active campaigns for timeout thresholds
func startCampaignAutoAdvanceWorker() {
	go func() {
		// Wait a bit before first run to let server fully initialize
		time.Sleep(1 * time.Minute)

		ticker := time.NewTicker(30 * time.Minute)
		for {
			autoAdvanceCampaigns()
			<-ticker.C
		}
	}()
	log.Println("Campaign auto-advance worker started (runs every 30min)")
}

// autoAdvanceCampaigns checks all active campaigns and auto-skips stalled turns
// Combat: auto-skip after 4h of inactivity
// Exploration: auto-skip after 12h of inactivity
func autoAdvanceCampaigns() {
	log.Println("Auto-advance worker: checking campaigns...")

	// Get all active campaigns
	rows, err := db.Query(`
		SELECT id, name, dm_id FROM lobbies 
		WHERE status = 'active'
	`)
	if err != nil {
		log.Printf("Auto-advance error querying campaigns: %v", err)
		return
	}
	defer rows.Close()

	var campaigns []struct {
		ID   int
		Name string
		DMID int
	}
	for rows.Next() {
		var c struct {
			ID   int
			Name string
			DMID int
		}
		rows.Scan(&c.ID, &c.Name, &c.DMID)
		campaigns = append(campaigns, c)
	}

	skippedTotal := 0
	for _, campaign := range campaigns {
		skipped := autoAdvanceCampaign(campaign.ID, campaign.Name)
		skippedTotal += skipped
	}

	if skippedTotal > 0 {
		log.Printf("Auto-advance worker: skipped %d inactive turns across %d campaigns", skippedTotal, len(campaigns))
	}
}

// autoAdvanceCampaign handles auto-skip for a single campaign
// Returns number of turns/players skipped
func autoAdvanceCampaign(campaignID int, campaignName string) int {
	if f, err := readFight(db, campaignID); err == nil {
		return autoAdvanceCombat(campaignID, campaignName, f) // combat mode: the combat turn timeout
	}
	// Exploration mode - check for 12h+ inactive players
	return autoAdvanceExploration(campaignID, campaignName)
}

// autoAdvanceCombat skips a monster's or NPC's turn once the campaign's combat turn timeout has passed (table rule
// combat_turn_timeout, 4 hours by default; off: never). The turn change is advanceTurn's, only while the turn read here is still
// the current one (a GM's combat/next at the same moment changes it once).
func autoAdvanceCombat(campaignID int, campaignName string, f *fight) int {
	cur := f.current()
	if cur == nil || f.StartedAt.IsZero() {
		return 0
	}
	elapsed := time.Since(f.StartedAt)
	limit, on := combatTurnTimeout(campaignID) // table rule (F248); off: the GM holds turns
	if !on || elapsed < limit {
		return 0 // Not timed out yet
	}
	skippedName, skippedID := sprintAny(cur["name"]), asInt(cur["id"])
	// A player's slot is never skipped (Matt, Nov 14, pending-action turns): a character with no turn in when its slot comes up
	// has its turn taken by the GM as stand-in (GET /api/gm/status says so; POST .../combat/stand-in). A monster's or NPC's slot
	// is still skipped as before.
	if _, isPC := ptOwner(ptEntry{ID: skippedID, Name: skippedName}); isPC {
		return 0
	}
	t, code := advanceTurn(campaignID, turnHow{by: "the combat turn timeout passed", result: howSkipped, expect: &turnRef{Round: f.Round, Index: f.Index, StartedAt: f.StartedAt}})
	if code != "" {
		return 0
	}
	elapsedMinutes := int(elapsed.Minutes())
	log.Printf("Auto-advance: Skipped %s's turn in %s (inactive %d min)", skippedName, campaignName, elapsedMinutes)
	writeAction(db, campaignID, skippedID, "turn_auto_skipped", "Turn automatically skipped: the combat turn timeout passed (table rule combat_turn_timeout; system)", fmt.Sprintf("Inactive for %d minutes. Auto-skipped by system.", elapsedMinutes))
	if t.NewRound {
		log.Printf("Auto-advance: %s combat advanced to round %d", campaignName, t.Round)
	}
	return 1
}

// autoAdvanceExploration marks 12h+ inactive players as "following the party"
func autoAdvanceExploration(campaignID int, campaignName string) int {
	// Find players inactive 12h+
	rows, err := db.Query(`
		SELECT c.id, c.name,
			COALESCE(
				(SELECT MAX(a.created_at) FROM actions a WHERE a.character_id = c.id AND a.action_type NOT IN ('poll', 'joined', 'turn_auto_skipped', 'following')),
				c.created_at
			) as last_action
		FROM characters c
		WHERE c.lobby_id = $1
	`, campaignID)
	if err != nil {
		return 0
	}
	defer rows.Close()

	skipped := 0
	exLimit, exOn := timeoutRule(campaignID, "exploration_timeout", 12*time.Hour) // table rule (F248)
	for rows.Next() {
		var charID int
		var charName string
		var lastAction time.Time
		rows.Scan(&charID, &charName, &lastAction)

		elapsed := time.Since(lastAction)
		if !exOn || elapsed < exLimit {
			continue // Not timed out
		}

		// A following record remains effective until the player takes another
		// meaningful action. Expiring it after 12 hours re-emits the same system
		// event forever for abandoned characters, which pollutes campaign feeds.
		var lastFollowing sql.NullTime
		db.QueryRow(`
			SELECT MAX(created_at) FROM actions
			WHERE character_id = $1 AND action_type = 'following'
		`, charID).Scan(&lastFollowing)
		if isFollowingParty(sql.NullTime{Time: lastAction, Valid: true}, lastFollowing) {
			continue // Already following; a later player action will re-enable tracking.
		}

		elapsedHours := int(elapsed.Hours())
		log.Printf("Auto-advance: Marking %s as following in %s (inactive %dh)", charName, campaignName, elapsedHours)

		// Record as following
		writeAction(db, campaignID, charID, "following", "Automatically marked as following the party (12h+ inactive)", fmt.Sprintf("Inactive for %d hours. Auto-marked by system.", elapsedHours))

		skipped++
	}

	return skipped
}

// getRecentCampaignMessages returns messages from last N hours
func getRecentCampaignMessages(lobbyID int, hours int) []map[string]interface{} {
	messages := []map[string]interface{}{}
	if db == nil {
		return messages
	}
	rows, err := db.Query(`
		SELECT id, agent_id, agent_name, message, created_at
		FROM campaign_messages
		WHERE lobby_id = $1 AND created_at > NOW() - INTERVAL '1 hour' * $2
		ORDER BY created_at DESC
		LIMIT 50
	`, lobbyID, hours)
	if err != nil {
		return messages
	}
	defer rows.Close()
	for rows.Next() {
		var id, agentID int
		var agentName, message string
		var createdAt time.Time
		rows.Scan(&id, &agentID, &agentName, &message, &createdAt)
		messages = append(messages, map[string]interface{}{
			"id":         id,
			"agent_id":   agentID,
			"agent_name": agentName,
			"message":    message,
			"created_at": createdAt.Format(time.RFC3339),
		})
	}
	return messages
}

// shouldAdvanceAbandonedPlayer: an abandoned player's turn may be skipped now (out of combat, or its combat turn has passed
// skipAfter, the campaign's combat turn timeout; skipAfter 0: the rule is off and turns are never skipped).
func shouldAdvanceAbandonedPlayer(inCombat bool, currentCombatCharacterID, characterID int, turnStartedAt sql.NullTime, now time.Time, skipAfter time.Duration) bool {
	if !inCombat {
		return true
	}
	return skipAfter > 0 && currentCombatCharacterID == characterID && turnStartedAt.Valid &&
		now.Sub(turnStartedAt.Time) >= skipAfter
}

// getCampaignPlayers returns all players in a campaign with last_active
func getCampaignPlayers(lobbyID int) []map[string]interface{} {
	players := []map[string]interface{}{}
	rows, err := db.Query(`
		SELECT c.id, c.name, c.class, c.species, c.level, c.hp, c.max_hp, c.last_active,
			a.id, a.name
		FROM characters c
		JOIN agents a ON c.agent_id = a.id
		WHERE c.lobby_id = $1
	`, lobbyID)
	if err != nil {
		return players
	}
	defer rows.Close()
	for rows.Next() {
		var charID, level, hp, maxHP, agentID int
		var charName, class, species, agentName string
		var lastActive sql.NullTime
		rows.Scan(&charID, &charName, &class, &species, &level, &hp, &maxHP, &lastActive, &agentID, &agentName)
		player := map[string]interface{}{
			"character_id":   charID,
			"character_name": charName,
			"class":          class,
			"species":        species,
			"level":          level,
			"hp":             hp,
			"max_hp":         maxHP,
			"agent_id":       agentID,
			"agent_name":     agentName,
		}
		if lastActive.Valid {
			player["last_active"] = lastActive.Time.Format(time.RFC3339)
		}
		players = append(players, player)
	}
	return players
}

// getTurnStatus returns turn information for a campaign
func getTurnStatus(lobbyID int, characterID int) map[string]interface{} {
	result := map[string]interface{}{
		"in_combat":  false,
		"your_turn":  false,
		"waiting_on": []string{},
	}

	var combatRound, turnIndex int
	var turnOrderJSON []byte
	var combatActive bool
	err := db.QueryRow(`
		SELECT round_number, current_turn_index, turn_order, active 
		FROM combat_state WHERE lobby_id = $1
	`, lobbyID).Scan(&combatRound, &turnIndex, &turnOrderJSON, &combatActive)

	if err != nil || !combatActive {
		return result
	}

	result["in_combat"] = true
	result["round"] = combatRound

	type InitEntry struct {
		ID         int    `json:"id"`
		Name       string `json:"name"`
		Initiative int    `json:"initiative"`
	}
	var entries []InitEntry
	json.Unmarshal(turnOrderJSON, &entries)

	if len(entries) == 0 {
		return result
	}

	currentTurnID := 0
	currentTurnName := ""
	if len(entries) > turnIndex {
		currentTurnID = entries[turnIndex].ID
		currentTurnName = entries[turnIndex].Name
	}

	result["current_turn"] = currentTurnName
	result["your_turn"] = currentTurnID == characterID

	// Build waiting_on list (characters between current and this character)
	waitingOn := []string{}
	if characterID > 0 && currentTurnID != characterID {
		// Find positions
		currentPos := turnIndex
		myPos := -1
		for i, e := range entries {
			if e.ID == characterID {
				myPos = i
				break
			}
		}

		if myPos >= 0 {
			// Count characters between current and me (wrapping around)
			for i := currentPos; i != myPos; i = (i + 1) % len(entries) {
				waitingOn = append(waitingOn, entries[i].Name)
				if len(waitingOn) > 10 {
					break // Safety limit
				}
			}
		}
	}
	result["waiting_on"] = waitingOn

	return result
}

// getRecentCampaignActions returns recent actions from a campaign
func getRecentCampaignActions(lobbyID int, hours int) []map[string]interface{} {
	actions := []map[string]interface{}{}
	recs, err := readActions(actionFilter{lobby: lobbyID, within: time.Duration(hours) * time.Hour, newestFirst: true, limit: 50}) // action_log_2024.go
	if err != nil {
		return actions
	}
	for _, a := range recs {
		actions = append(actions, a.view())
	}
	return dedupeRecentCampaignActions(actions)
}

func dedupeRecentCampaignActions(actions []map[string]interface{}) []map[string]interface{} {
	if len(actions) == 0 {
		return actions
	}

	deduped := make([]map[string]interface{}, 0, len(actions))
	lastJoinKey := ""
	for _, action := range actions {
		actionType, _ := action["type"].(string)
		if actionType == "" {
			actionType, _ = action["action_type"].(string)
		}
		actor, _ := action["actor"].(string)
		description, _ := action["description"].(string)

		if actionType == "joined" {
			joinKey := actor + "|" + description
			if joinKey == lastJoinKey {
				continue
			}
			lastJoinKey = joinKey
		} else {
			lastJoinKey = ""
		}

		deduped = append(deduped, action)
	}

	return deduped
}
