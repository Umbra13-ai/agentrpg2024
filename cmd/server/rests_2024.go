package main

// Hit Point Dice and what a player is told about resting (Matt, Nov 16: a player didn't see how to use Hit Point Dice in a Short
// Rest). SRD 5.2.1 / PHB 2024 Short Rest: 1 hour; you need at least 1 Hit Point to start; when it finishes you can spend Hit Point
// Dice, each rolled and adding your Constitution modifier (at least 1 Hit Point each), "and you can decide to spend an additional Hit
// Point Die after each roll".
//
//   - rollHitDice is the one way Hit Point Dice are spent: the dice claimed in one guarded step (never more than are left), rolled,
//     and the healing given by the Hit Point owner. The Short Rest and POST /api/characters/{id}/hit-dice call it.
//   - POST /api/characters/{id}/hit-dice spends more right after a Short Rest, one at a time if you like, deciding after each roll:
//     only while the character's last entry in the log is that Short Rest (or a Hit Point Die spent after it) and no fight is on.
//   - restGuide is what every reply says about resting: Hit Points, the Hit Point Dice left, the die, what each heals on average, a
//     suggested number, what else comes back, and the requests. my-turn (your_options.rests), the after-battle reminder and the
//     Short Rest's replies show it.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// hitDiceRoll: what spending Hit Point Dice did.
type hitDiceRoll struct {
	Spent   int   `json:"hit_dice_spent"`
	Left    int   `json:"hit_dice_remaining"`
	Rolls   []int `json:"rolls"`
	Die     int   `json:"-"`
	ConMod  int   `json:"con_mod"`
	Healing int   `json:"total_healing"`
	Healed  int   `json:"actual_healing"`
	HP      int   `json:"hp"`
	MaxHP   int   `json:"max_hp"`
	Periapt bool  `json:"-"`
}

// errNotEnoughHitDice: fewer Hit Point Dice are left than were asked for (nothing was spent).
var errNotEnoughHitDice = errors.New("not enough Hit Point Dice left")

// rollHitDice spends n Hit Point Dice: claimed in one guarded statement, so two requests at once never spend more than are left;
// each is rolled and adds the Constitution modifier (at least 1; a Periapt of Wound Closure doubles it); the Hit Point owner heals.
// die picks a multiclass pool first (spendHitDiceByType). n of 0 spends nothing.
func rollHitDice(charID, n int, die string) (hitDiceRoll, error) {
	var out hitDiceRoll
	var class string
	var con, level, spent int
	if err := db.QueryRow(`SELECT class, con, level, COALESCE(hit_dice_spent, 0), hp, max_hp FROM characters WHERE id = $1`, charID).
		Scan(&class, &con, &level, &spent, &out.HP, &out.MaxHP); err != nil {
		return out, err
	}
	out.Die, out.ConMod, out.Left, out.Rolls = game.HitDie(class), game.Modifier(con), level-spent, []int{}
	if n <= 0 {
		return out, nil
	}
	err := db.QueryRow(`UPDATE characters SET hit_dice_spent = COALESCE(hit_dice_spent, 0) + $2
		WHERE id = $1 AND level - COALESCE(hit_dice_spent, 0) >= $2 RETURNING level - hit_dice_spent`, charID, n).Scan(&out.Left)
	if errors.Is(err, sql.ErrNoRows) {
		return out, errNotEnoughHitDice
	}
	if err != nil {
		return out, err
	}
	out.Spent = n
	sizes := spendHitDiceByType(charID, n, die)
	out.Periapt = periaptActive(charID)
	for i := 0; i < n; i++ {
		size := out.Die
		if i < len(sizes) {
			size = sizes[i]
		}
		roll := game.RollDie(size)
		heal := max(roll+out.ConMod, 1)
		if out.Periapt {
			heal *= 2 // Periapt of Wound Closure: a Hit Point Die heals double (F142)
		}
		out.Rolls = append(out.Rolls, roll)
		out.Healing += heal
	}
	h, err := healHP(charID, out.Healing)
	if err != nil {
		return out, err
	}
	out.HP, out.MaxHP, out.Healed = h.After.HP, h.After.Max, h.Healed
	return out, nil
}

// hitDiceSuggestion: about how many Hit Point Dice bring a character to its maximum (0 when it is there or has none left), and the
// average healing of one.
func hitDiceSuggestion(hp, maxHP, left, die, conMod int) (suggested int, average float64) {
	average = math.Max(float64(die)/2+0.5+float64(conMod), 1)
	if hp >= maxHP || left <= 0 {
		return 0, average
	}
	return min(int(math.Ceil(float64(maxHP-hp)/average)), left), average
}

// restGuide: what resting gives this character now, and how to ask for it (one source for my-turn, the reminders and the rest replies).
func restGuide(charID int) map[string]interface{} {
	r, err := rollHitDice(charID, 0, "")
	if err != nil {
		return nil
	}
	suggested, avg := hitDiceSuggestion(r.HP, r.MaxHP, r.Left, r.Die, r.ConMod)
	dice := map[string]interface{}{"left": r.Left, "die": fmt.Sprintf("d%d", r.Die),
		"each_heals": fmt.Sprintf("1d%d %+d (about %.1f; at least 1)", r.Die, r.ConMod, avg), "suggested": suggested}
	if byType := hitDiceLeftByType(charID); len(byType) > 1 {
		dice["by_type"] = byType
	}
	short := map[string]interface{}{
		"takes":    "1 hour of light activity (you need at least 1 Hit Point to start; rolling Initiative, casting a spell other than a cantrip or taking damage interrupts it)",
		"send":     fmt.Sprintf("POST /api/characters/%d/short-rest", charID),
		"example":  map[string]interface{}{"hit_dice": suggested},
		"hit_dice": dice,
		"how": fmt.Sprintf("When the rest finishes you spend Hit Point Dice: send hit_dice with the rest (each is rolled and adds your Constitution modifier), "+
			"or 0 and then spend them one at a time with POST /api/characters/%d/hit-dice {\"count\": 1}, deciding after each roll (only right after that Short Rest).", charID),
	}
	if back := shortRestRestores(charID); len(back) > 0 {
		short["restores"] = back
	}
	long := map[string]interface{}{
		"takes":    "8 hours, when the GM allows it (the GM keeps time)",
		"send":     fmt.Sprintf("POST /api/characters/%d/rest", charID),
		"restores": "all Hit Points, spell slots and class resources, and half your Hit Point Dice",
	}
	out := map[string]interface{}{"hp": fmt.Sprintf("%d/%d", r.HP, r.MaxHP), "short_rest": short, "long_rest": long}
	if usesScheduleFor(charID) {
		out["turn_schedule"] = "Outside a fight a rest is a game action: it is saved as your submission for the round and the GM resolves it (its duration, interruptions and benefits). Send hit_dice with it."
	}
	return out
}

// usesScheduleFor: the character's campaign uses the turn schedule.
func usesScheduleFor(charID int) bool {
	lobby := lobbyOfChar(charID)
	if lobby == 0 {
		return false
	}
	state, err := scheduleStateOf(db, lobby)
	return err == nil && usesSchedule(state)
}

// rightAfterShortRest: the character's last entry in the log is a Short Rest, or a Hit Point Die spent after one, and no fight is on.
func rightAfterShortRest(charID int) (bool, error) {
	if lobby := lobbyOfChar(charID); lobby != 0 && inActiveCombat(lobby) {
		return false, nil
	}
	recs, err := readActions(actionFilter{character: charID, skipTypes: []string{"poll", "following"}, newestFirst: true, limit: 1})
	if err != nil || len(recs) == 0 {
		return false, err
	}
	t := recs[0].view()["type"]
	return t == "short_rest" || t == "hit_dice", nil
}

// handleHitDice godoc
// @Summary Spend Hit Point Dice right after a Short Rest
// @Description SRD 5.2.1 Short Rest: when it finishes you can spend Hit Point Dice, each rolled and adding your Constitution modifier (at least 1 Hit Point each), and you can decide to spend another after each roll. Send {"count": 1} (the default) to roll one, read the result, and send again while you want more. Only right after a Short Rest (POST /api/characters/{id}/short-rest): the character's last action is that rest or a Hit Point Die spent after it, and no fight is on (409 not_after_short_rest otherwise). GET shows the Hit Point Dice left and the rest guide. Only the character's owner or the campaign's GM.
// @Tags Characters
// @Param id path int true "Character ID"
// @Body how many Hit Point Dice to spend now
// @Success 200 {object} map[string]interface{} "the rolls, the healing, hp and the Hit Point Dice left"
// @Failure 400 {object} map[string]interface{} "not_enough_hit_dice"
// @Failure 409 {object} map[string]interface{} "not_after_short_rest"
// @Router /characters/{id}/hit-dice [get]
// @Router /characters/{id}/hit-dice [post]
func handleHitDice(w http.ResponseWriter, r *http.Request, charID int) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(map[string]interface{}{"character_id": charID, "rest": restGuide(charID)})
		return
	}
	var req struct {
		Count int    `json:"count"` // Hit Point Dice to spend now (default 1): roll one, read it, send again for another
		Die   string `json:"die"`   // which Hit Point Die to spend first ("d10"), when the character has more than one kind
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}
	if req.Count == 0 {
		req.Count = 1
	}
	if req.Count < 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_count", "message": "count is how many Hit Point Dice to spend, 1 or more. Nothing was spent."})
		return
	}
	ok, err := rightAfterShortRest(charID)
	if answerDBFailure(w, err, "the character's log") {
		return
	}
	if !ok {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "not_after_short_rest",
			"message": fmt.Sprintf("Hit Point Dice are spent when a Short Rest finishes: take one with POST /api/characters/%d/short-rest (send hit_dice with it, or 0 and then spend them here one at a time). This works only right after that rest, outside a fight. Nothing was spent.", charID)})
		return
	}
	res, err := rollHitDice(charID, req.Count, req.Die)
	if errors.Is(err, errNotEnoughHitDice) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_enough_hit_dice", "hit_dice_remaining": res.Left,
			"message": fmt.Sprintf("You have %d Hit Point Dice left. Nothing was spent.", res.Left)})
		return
	}
	if answerDBFailure(w, err, "the character") {
		return
	}
	lobby := lobbyOfChar(charID)
	writeAction(db, lobby, charID, "hit_dice", fmt.Sprintf("%s spends %d Hit Point Dice after the Short Rest", getCharacterName(charID), res.Spent), fmt.Sprintf("rolled %v, regained %d Hit Points", res.Rolls, res.Healed))
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "hit_dice_spent": res.Spent, "hit_dice_remaining": res.Left,
		"rolls": res.Rolls, "con_mod": res.ConMod, "total_healing": res.Healing, "actual_healing": res.Healed, "hp": res.HP, "max_hp": res.MaxHP,
		"message": hitDiceMessage(charID, res)})
}

// hitDiceMessage: what the dice did, and whether another may help.
func hitDiceMessage(charID int, res hitDiceRoll) string {
	msg := fmt.Sprintf("Rolled %s: regained %d Hit Points (%d/%d).", strings.Trim(fmt.Sprint(res.Rolls), "[]"), res.Healed, res.HP, res.MaxHP)
	if res.HP < res.MaxHP && res.Left > 0 {
		msg += fmt.Sprintf(" %d Hit Point Dice left: spend another with POST /api/characters/%d/hit-dice {\"count\": 1}, or stop here.", res.Left, charID)
	}
	return msg
}

// logRest: a rest a character took goes into the campaign's log, so the GM and the party see it (playtest I-CALLA-23: a Long Rest
// restored everything with no record the GM could read). Both rest routes call it once the rest is done.
func logRest(charID int, kind, summary string) {
	lobby := lobbyOfChar(charID) // 0 (in no campaign) is written too: the Hit Point Dice after a Short Rest read the record
	what := map[string]string{"long": "finishes a Long Rest", "short": "finishes a Short Rest"}[kind]
	writeAction(db, lobby, charID, kind+"_rest", getCharacterName(charID)+" "+what, summary)
}

// longRestTooSoon: the refusal of a Long Rest started less than 24 hours of story time after the character's last one began (SRD
// 5.2.1), nil when it may rest. The story clock is the campaign's (lobbies.story_rounds: the GM's POST /api/gm/pass-time and travel
// move it); a character's own rest isn't on it, so the gap needed is 24 hours less the rest's own length. A character in no
// campaign, or with no Long Rest on the story clock yet, may rest.
func longRestTooSoon(charID, restRounds int) map[string]interface{} {
	var last sql.NullInt64
	var now int64
	var lobby sql.NullInt64
	if db.QueryRow(`SELECT c.last_long_rest_story, c.lobby_id, COALESCE(l.story_rounds, 0) FROM characters c LEFT JOIN lobbies l ON l.id = c.lobby_id
		WHERE c.id = $1`, charID).Scan(&last, &lobby, &now) != nil || !lobby.Valid || !last.Valid {
		return nil
	}
	need := int64(14400 - restRounds) // 24 hours of 10-round minutes, less the rest's own 8 (or a Trance's 4)
	if gone := now - last.Int64; gone < need {
		left := (need - gone + 599) / 600
		return map[string]interface{}{"success": false, "error": "long_rest_too_soon", "story_hours_left": left,
			"message": fmt.Sprintf("A creature can't benefit from more than one Long Rest in 24 hours, and the story clock shows %d more hours to go since this character's last one. "+
				"If the story has moved on, the GM passes the time (POST /api/gm/pass-time {\"minutes\": %d}) or sends the rest itself (the GM's own request is its ruling; a submission it resolves is checked like the player's). Nothing was changed.", left, left*60)}
	}
	return nil
}
