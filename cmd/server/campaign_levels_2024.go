package main

// A campaign's level range and a character's start above level 1 (Matt, Nov 16): min_level is the level new characters start at,
// max_level the highest level that may join. A GM sets them when making the campaign and changes them later (PATCH
// /api/campaigns/{id}/levels), raising the starting level as the party levels up, so a newcomer joins a level 5 party at level 5
// with gear to match. A player whose character isn't in a campaign may start it higher before it joins (POST
// /api/characters/{id}/starting-level).
//
//   - higherLevelStart is the SRD 5.2.1 / PHB 2024 Starting Equipment at Higher Levels table (money by tier; the magic items are the
//     GM's to choose).
//   - startAtLevel is the one way a character starts above its level outside play: the level-up rules (applyLevelGain: Hit Points,
//     Ability Score Improvements, XP) and the table's money, paid once per tier (starting_money). The player's request, joining a
//     campaign that starts higher (startAtCampaignLevel) and the GM's POST /api/gm/starting-level call it.
//   - characterHasPlayed: a character that has played in a campaign is no longer new; only a new one may start higher by itself,
//     and only a new one may join a campaign that starts above its level (levelMayJoin).
//   - levelRangeView is how every reply describes the range; levelRangeAdvice is the GM's reminder in GET /api/gm/status when the
//     whole party has passed the starting level.

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/agentrpg/agentrpg/game"
)

// higherLevelStart: the money a character starting at level gets (rolled) and the magic items the table suggests for the GM to give.
func higherLevelStart(level int) (gold int, items string) {
	switch levelTier(level) {
	case 4:
		return 20000 + game.RollDie(10)*250, "2 Common, 4 Uncommon, 3 Rare and 1 Very Rare magic item"
	case 3:
		return 5000 + game.RollDie(10)*250, "2 Common, 3 Uncommon and 1 Rare magic item"
	case 2:
		return 500 + game.RollDie(10)*25, "1 Common and 1 Uncommon magic item"
	}
	if level >= 2 {
		return 0, "1 Common magic item"
	}
	return 0, ""
}

// levelTier: the table's rows (1: levels 1-4, 2: 5-10, 3: 11-16, 4: 17-20).
func levelTier(level int) int {
	switch {
	case level >= 17:
		return 4
	case level >= 11:
		return 3
	case level >= 5:
		return 2
	}
	return 1
}

// levelStart: what a start at a higher level gave.
type levelStart struct {
	CharID, From, To, HP, ASI, Gold int
	Items, Note                     string // Note: each level's gains (applyLevelGain); the level-up notices hold them too
}

// text: the start in words, for the player, the GM and the log.
func (s levelStart) text(name string) string {
	// a summary, not every level's gains (feedback log 50: a 4 KB text for levels 2 to 11); those are the level-up notices
	out := fmt.Sprintf("%s starts at level %d (from level %d): +%d Hit Points, %d Ability Score Improvement points to spend.", name, s.To, s.From, s.HP, s.ASI)
	if open := characterToFinish(s.CharID); len(open) > 0 {
		out += fmt.Sprintf(" %d choices these levels bring are open: GET /api/characters/%d/setup guides each (each level's gains: GET /api/characters/%d/level-up-notices).", len(open), s.CharID, s.CharID)
	}
	if s.Gold > 0 {
		out += fmt.Sprintf(" Starting money for level %d: +%d GP.", s.To, s.Gold)
	}
	if s.Items != "" {
		out += fmt.Sprintf(" The Starting Equipment at Higher Levels table also suggests %s: the campaign's GM chooses them and gives them (POST /api/gm/give-item).", s.Items)
	}
	return out
}

// startAtLevel raises a character from its level to level by the level-up rules, with the table's money when money is true. The
// money is paid once per tier: a character already given a tier's money (starting_money) gets only what a higher tier adds.
func startAtLevel(charID, from, to int, how string, money bool) levelStart {
	st := levelStart{CharID: charID, From: from, To: to}
	st.HP, st.ASI, st.Note = applyLevelGain(charID, from, to, how)
	gold, items := higherLevelStart(to)
	st.Items = items
	if !money {
		return st
	}
	var had int
	db.QueryRow(`SELECT COALESCE(starting_money, 0) FROM characters WHERE id = $1`, charID).Scan(&had)
	if had > 0 && levelTier(from) == levelTier(to) {
		gold = had // that tier's money was paid already
	}
	if st.Gold = gold - had; st.Gold > 0 {
		db.Exec(`UPDATE characters SET gold = COALESCE(gold, 0) + $1, starting_money = $2 WHERE id = $3`, st.Gold, gold, charID)
	} else {
		st.Gold = 0
	}
	return st
}

// characterHasPlayed: the character has a record in a campaign's log besides joining, leaving, checking the game and starting
// at the campaign's level.
func characterHasPlayed(charID int) (bool, error) {
	rows, err := readActions(actionFilter{character: charID, inCampaign: true, hidePolls: true, skipTypes: notPlayTypes, limit: 1})
	return len(rows) > 0, err
}

// startAtCampaignLevel raises a new character to the campaign's starting level as it joins, with the starting money; it answers the
// note for the player and the GM ("" when nothing was done).
func startAtCampaignLevel(charID, lobby, minLevel int, played bool) string {
	var level int
	var name string
	if played || db.QueryRow(`SELECT level, name FROM characters WHERE id = $1`, charID).Scan(&level, &name) != nil || minLevel <= level {
		return ""
	}
	out := startAtLevel(charID, level, minLevel, fmt.Sprintf("starting at campaign %d's level", lobby), true).text(name) + " This is the campaign's starting level."
	writeAction(db, lobby, charID, "level_up", out, "")
	return out
}

// levelRangeView: the range as every reply describes it.
func levelRangeView(lobby, minLevel, maxLevel int) map[string]interface{} {
	v := map[string]interface{}{"min_level": minLevel, "max_level": maxLevel, "level_requirement": formatLevelRequirement(minLevel, maxLevel),
		"new_characters": fmt.Sprintf("A new character (one that hasn't played in a campaign) below level %d that joins is raised to it by the level-up rules, with the starting money for that level; its player then makes the level-up choices. A player can also start a character higher before joining: POST /api/characters/{id}/starting-level.", minLevel),
		"change":         fmt.Sprintf(`The GM changes the range with PATCH /api/campaigns/%d/levels {"min_level": N, "max_level": M} (1 to 20). Raise min_level as the party levels up, so newcomers join at the party's level.`, lobby)}
	if minLevel > 1 {
		_, items := higherLevelStart(minLevel)
		v["gear"] = fmt.Sprintf("At level %d the Starting Equipment at Higher Levels table also suggests %s, which the GM chooses and gives (POST /api/gm/give-item).", minLevel, items)
	}
	return v
}

// levelRangeAdvice: the GM's reminder when every character of the campaign is above its starting level ("" otherwise).
func levelRangeAdvice(lobby int) string {
	var minLevel, maxLevel, lowest, count int
	if db.QueryRow(`SELECT COALESCE(l.min_level, 1), COALESCE(l.max_level, 1), COALESCE(MIN(c.level), 0), COUNT(c.id)
		FROM lobbies l LEFT JOIN characters c ON c.lobby_id = l.id WHERE l.id = $1 GROUP BY l.id`, lobby).Scan(&minLevel, &maxLevel, &lowest, &count) != nil ||
		count == 0 || lowest <= minLevel {
		return ""
	}
	return fmt.Sprintf("Every character in this campaign is level %d or higher, but new characters still start at level %d: raise the starting level so newcomers join at the party's level: PATCH /api/campaigns/%d/levels {\"min_level\": %d, \"max_level\": %d}.",
		lowest, minLevel, lobby, lowest, max(maxLevel, lowest))
}

// handleCampaignLevels godoc
// @Summary A campaign's level range: the level new characters start at, and the highest that may join
// @Description GET (anyone who may see the campaign): min_level (the level a new character starts at: one that hasn't played in a campaign and is below it is raised to it as it joins, by the level-up rules, with the Starting Equipment at Higher Levels money), max_level (the highest level that may join), and what they mean. PATCH (the GM): {"min_level": N, "max_level": M}, 1 to 20, min_level at most max_level; either may be sent alone. Characters already in the campaign are not changed. Raise min_level as the party levels up, so newcomers join at the party's level (GET /api/gm/status reminds you when the whole party has passed it). The change is written to the campaign's log.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string false "Bearer <token> (PATCH)"
// @Body PATCH: the new range
// @Success 200 {object} map[string]interface{} "the range"
// @Failure 400 {object} map[string]interface{} "invalid_level_range"
// @Failure 403 {object} map[string]interface{} "not_gm"
// @Router /campaigns/{id}/levels [get]
// @Router /campaigns/{id}/levels [patch]
func handleCampaignLevels(w http.ResponseWriter, r *http.Request, lobby int) {
	w.Header().Set("Content-Type", "application/json")
	read := func() (int, int, bool) {
		var minLevel, maxLevel int
		err := db.QueryRow(`SELECT COALESCE(min_level, 1), COALESCE(max_level, 1) FROM lobbies WHERE id = $1`, lobby).Scan(&minLevel, &maxLevel)
		return minLevel, maxLevel, !answerDBFailure(w, err, "the campaign")
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if !noFields(w, r) {
			return
		}
		minLevel, maxLevel, ok := read()
		if !ok {
			return
		}
		v := levelRangeView(lobby, minLevel, maxLevel)
		v["campaign_id"] = lobby
		json.NewEncoder(w).Encode(v)
	case http.MethodPatch:
		if _, ok := requireCampaignGM(w, r, lobby, "change its level range"); !ok {
			return
		}
		var req struct {
			MinLevel *int `json:"min_level"` // the level new characters start at (1 to 20)
			MaxLevel *int `json:"max_level"` // the highest character level that may join (1 to 20)
		}
		if !readRequest(w, r, &req) {
			return
		}
		minLevel, maxLevel, ok := read()
		if !ok {
			return
		}
		if req.MinLevel == nil && req.MaxLevel == nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_level_range", "message": `Send "min_level" and/or "max_level" (1 to 20). Nothing was changed.`})
			return
		}
		var problem string
		if minLevel, maxLevel, problem = newLevelRange(req.MinLevel, req.MaxLevel, minLevel, maxLevel); problem != "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_level_range", "message": problem})
			return
		}
		if _, err := db.Exec(`UPDATE lobbies SET min_level = $1, max_level = $2 WHERE id = $3`, minLevel, maxLevel, lobby); err != nil {
			writeDBWriteError(w, err, "the campaign's level range")
			return
		}
		writeAction(db, lobby, 0, "level_range", fmt.Sprintf("The campaign's level range is now %s: new characters start at level %d.", formatLevelRequirement(minLevel, maxLevel), minLevel), "")
		v := levelRangeView(lobby, minLevel, maxLevel)
		v["success"], v["campaign_id"] = true, lobby
		json.NewEncoder(w).Encode(v)
	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "GET or PATCH."})
	}
}

// newLevelRange is the one rule of a range a GM sets (creation, from a template's range or 1-1, and PATCH .../levels, from the
// current one): what was sent replaces it, a min_level raised above the maximum lifts it when max_level isn't sent, levels are 1
// to 20 and min_level is at most max_level. It answers the new range, or why it is refused.
func newLevelRange(minIn, maxIn *int, minLevel, maxLevel int) (int, int, string) {
	if minIn != nil {
		minLevel = *minIn
	}
	if maxIn != nil {
		maxLevel = *maxIn
	} else if maxLevel < minLevel {
		maxLevel = minLevel
	}
	if minLevel < 1 || maxLevel > 20 || minLevel > maxLevel {
		return minLevel, maxLevel, fmt.Sprintf("Levels are 1 to 20 and min_level can't be above max_level (asked: %d to %d). Nothing was changed.", minLevel, maxLevel)
	}
	return minLevel, maxLevel, ""
}

// levelMayJoin is the one rule of who may join a campaign by level: a character within the range, or a new one (it hasn't played
// in a campaign) below it, which is raised to min_level as it joins. Joining and GET /api/campaigns?level=N read it.
func levelMayJoin(level int, played bool, minLevel, maxLevel int) bool {
	return (level >= minLevel || !played) && level <= maxLevel
}

// joinLevelRefusal: why a character outside the range can't join, and what to do instead.
func joinLevelRefusal(level, minLevel, maxLevel int) string {
	req := formatLevelRequirement(minLevel, maxLevel)
	if level < minLevel {
		return fmt.Sprintf("Your character is level %d and has played in a campaign already; this campaign requires %s. A new character can join: it is raised to level %d as it joins.", level, req, minLevel)
	}
	return fmt.Sprintf("Your character is level %d; this campaign requires %s.", level, req)
}

// handleStartingLevel godoc
// @Summary Start a new character above level 1, before it joins a campaign
// @Description Every character is made at level 1. Before it joins a campaign, its player can start it at a higher level, for a campaign whose new characters start higher (min_level; joining also raises a new character to it). Only a character in no campaign that hasn't played in one, and only upward: the level-up rules (Hit Points, Ability Score Improvements, experience set to the level's minimum) and the Starting Equipment at Higher Levels money (levels 5-10: 500 GP + 1d10 x 25 GP; 11-16: 5,000 GP + 1d10 x 250 GP; 17-20: 20,000 GP + 1d10 x 250 GP), paid once per tier. The magic items the table suggests are the GM's to give once you join. Then GET /api/characters/{id}/setup guides the level-up choices. Refusals spend nothing: invalid_level, in_campaign, already_played.
// @Tags Characters
// @Accept json
// @Produce json
// @Param id path int true "Character ID"
// @Param Authorization header string true "Bearer <token>"
// @Body The level to start at
// @Success 200 {object} map[string]interface{} "what the start gave"
// @Failure 400 {object} map[string]interface{} "invalid_level"
// @Failure 409 {object} map[string]interface{} "in_campaign or already_played"
// @Router /characters/{id}/starting-level [post]
func handleStartingLevel(w http.ResponseWriter, r *http.Request, charID int) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": `POST /api/characters/{id}/starting-level {"level": N}.`})
		return
	}
	if _, ok := requireCharacterOwner(w, r, charID, "start it at a higher level"); !ok {
		return
	}
	var req struct {
		Level int `json:"level"` // the level to start at: above the character's level, at most 20
	}
	if !readRequest(w, r, &req) {
		return
	}
	var level, lobby int
	var name string
	if answerDBFailure(w, db.QueryRow(`SELECT level, COALESCE(lobby_id, 0), name FROM characters WHERE id = $1`, charID).Scan(&level, &lobby, &name), "the character") {
		return
	}
	if req.Level <= level || req.Level > 20 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_level",
			"message": fmt.Sprintf("level is the level %s starts at: above its level (%d) and at most 20. A character can't start lower than it is: make a new one for that. Nothing was changed.", name, level)})
		return
	}
	if lobby != 0 {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "in_campaign",
			"message": fmt.Sprintf("%s is in a campaign: there it gains levels through play (its GM can start it higher with POST /api/gm/starting-level). A character starts higher only before it joins. Nothing was changed.", name)})
		return
	}
	played, err := characterHasPlayed(charID)
	if answerDBFailure(w, err, "the character's log") {
		return
	}
	if played {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "already_played",
			"message": fmt.Sprintf("%s has played in a campaign: it gains levels through play, not by starting again. A new character can start higher. Nothing was changed.", name)})
		return
	}
	st := startAtLevel(charID, level, req.Level, "starting at a higher level", true)
	out := st.text(name)
	writeAction(db, 0, charID, "level_up", out, "")
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "character_id": charID, "level": st.To, "hp_gained": st.HP, "asi_points": st.ASI,
		"gold_added": st.Gold, "suggested_magic_items": st.Items, "result": out,
		"next": fmt.Sprintf("Make the level-up choices: GET /api/characters/%d/setup. Then find a campaign: GET /api/campaigns?open=true&level=%d.", charID, st.To)})
}

// startingLevelSetupStep: the optional setup step for a new character in no campaign (nil otherwise): starting it higher for a
// campaign whose new characters start above its level.
func startingLevelSetupStep(charID int) *setupStep {
	var level, lobby int
	if db.QueryRow(`SELECT level, COALESCE(lobby_id, 0) FROM characters WHERE id = $1`, charID).Scan(&level, &lobby) != nil || lobby != 0 || level >= 20 {
		return nil
	}
	if played, err := characterHasPlayed(charID); err != nil || played {
		return nil
	}
	return &setupStep{Key: "starting_level", Title: "Start at a higher level (only for a campaign that starts higher)", Optional: true,
		Why: fmt.Sprintf("Your character is level %d. A campaign shows the level its new characters start at (new_characters_start_at_level in GET /api/campaigns?open=true). "+
			"Joining one that starts higher raises your character to that level as it joins, so you need not do anything; or start it higher now, before joining, "+
			"by the level-up rules with the Starting Equipment at Higher Levels money. Only before it joins a campaign.", level),
		Request: req("POST", fmt.Sprintf("/api/characters/%d/starting-level", charID), map[string]interface{}{"level": "<the level to start at, above your level, at most 20>"})}
}
