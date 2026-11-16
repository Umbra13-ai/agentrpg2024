package main

// A campaign's level range (Matt, Nov 16): min_level is the level new characters start at, max_level the highest level that may
// join. A GM sets them when making the campaign and changes them later (PATCH /api/campaigns/{id}/levels), raising the starting
// level as the party levels up, so a newcomer joins a level 5 party at level 5 with gear to match.
//
//   - higherLevelStart is the SRD 5.2.1 / PHB 2024 Starting Equipment at Higher Levels table (money by tier; the magic items are the
//     GM's to choose): POST /api/gm/starting-level and joining read it.
//   - startAtCampaignLevel: a new character (level 1, no XP) joining a campaign that starts higher is raised to its starting level
//     as it joins, by the level-up rules (applyLevelGain: Hit Points, Ability Score Improvements, XP), with the starting money; the
//     GM is told which magic items the table suggests. Any other character must already be within the range.
//   - levelRangeView is how every reply describes the range (creation, the campaign, the change); levelRangeAdvice is the GM's
//     reminder in GET /api/gm/status when the whole party has passed the starting level.

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/agentrpg/agentrpg/game"
)

// higherLevelStart: the money a character starting at level gets (rolled) and the magic items the table suggests for the GM to give.
func higherLevelStart(level int) (gold int, items string) {
	switch {
	case level >= 17:
		return 20000 + game.RollDie(10)*250, "2 Common, 4 Uncommon, 3 Rare and 1 Very Rare magic item"
	case level >= 11:
		return 5000 + game.RollDie(10)*250, "2 Common, 3 Uncommon and 1 Rare magic item"
	case level >= 5:
		return 500 + game.RollDie(10)*25, "1 Common and 1 Uncommon magic item"
	case level >= 2:
		return 0, "1 Common magic item"
	}
	return 0, ""
}

// levelRangeView: the range as every reply describes it.
func levelRangeView(lobby, minLevel, maxLevel int) map[string]interface{} {
	v := map[string]interface{}{"min_level": minLevel, "max_level": maxLevel, "level_requirement": formatLevelRequirement(minLevel, maxLevel),
		"new_characters": fmt.Sprintf("A new character (level 1, no experience) that joins starts at level %d: joining raises it by the level-up rules, with the starting money for that level; its player then makes the level-up choices.", minLevel),
		"change":         fmt.Sprintf(`The GM changes the range with PATCH /api/campaigns/%d/levels {"min_level": N, "max_level": M} (1 to 20). Raise min_level as the party levels up, so newcomers join at the party's level.`, lobby)}
	if minLevel > 1 {
		_, items := higherLevelStart(minLevel)
		v["gear"] = fmt.Sprintf("At level %d the Starting Equipment at Higher Levels table also suggests %s, which the GM chooses and gives (POST /api/gm/give-item).", minLevel, items)
	}
	return v
}

// freshCharacter: a character that has not played yet (level 1, no experience).
func freshCharacter(level, xp int) bool { return level == 1 && xp == 0 }

// startAtCampaignLevel raises a new character to the campaign's starting level as it joins, with the starting money; it answers the
// note for the player and the GM ("" when nothing was done).
func startAtCampaignLevel(charID, lobby, minLevel int) string {
	var level, xp int
	var name string
	if db.QueryRow(`SELECT level, COALESCE(xp, 0), name FROM characters WHERE id = $1`, charID).Scan(&level, &xp, &name) != nil ||
		minLevel <= level || !freshCharacter(level, xp) {
		return ""
	}
	_, _, note := applyLevelGain(charID, level, minLevel, fmt.Sprintf("starting at campaign %d's level", lobby))
	gold, items := higherLevelStart(minLevel)
	if gold > 0 {
		db.Exec(`UPDATE characters SET gold = COALESCE(gold, 0) + $1 WHERE id = $2`, gold, charID)
	}
	out := fmt.Sprintf("%s starts at level %d, the campaign's starting level (from level %d). %s", name, minLevel, level, note)
	if gold > 0 {
		out += fmt.Sprintf(" Starting money for level %d: +%d GP.", minLevel, gold)
	}
	if items != "" {
		out += fmt.Sprintf(" The GM chooses the magic items the table suggests (%s) and gives them with POST /api/gm/give-item.", items)
	}
	writeAction(db, lobby, charID, "level_up", out, "")
	return out
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
// @Description GET (anyone who may see the campaign): min_level (the level a new character starts at when it joins: a level 1 character with no experience is raised to it by the level-up rules, with the Starting Equipment at Higher Levels money), max_level (the highest level that may join), and what they mean. PATCH (the GM): {"min_level": N, "max_level": M}, 1 to 20, min_level at most max_level; either may be sent alone. Characters already in the campaign are not changed. Raise min_level as the party levels up, so newcomers join at the party's level (GET /api/gm/status reminds you when the whole party has passed it). The change is written to the campaign's log.
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

// levelMayJoin is the one rule of who may join a campaign by level: a character within the range, or a new one (level 1, no
// experience), which is raised to min_level as it joins. Joining and GET /api/campaigns?level=N read it.
func levelMayJoin(level, xp, minLevel, maxLevel int) bool {
	return (level >= minLevel || freshCharacter(level, xp)) && level <= maxLevel
}

// joinLevelRefusal: why a character outside the range can't join, and what to do instead.
func joinLevelRefusal(level, minLevel, maxLevel int) string {
	req := formatLevelRequirement(minLevel, maxLevel)
	if level < minLevel {
		return fmt.Sprintf("Your character is level %d and has played already; this campaign requires %s. A new character (level 1, no experience) can join: it starts at level %d when it joins.", level, req, minLevel)
	}
	return fmt.Sprintf("Your character is level %d; this campaign requires %s.", level, req)
}
