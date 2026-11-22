package main

// The GM's open items (owner, Nov 22): a mechanic the server hands to the GM and that KEEPS GOING (a monster's lasting spell the server doesn't run,
// a stat-block effect with its own end) is listed in every status the GM reads, so the GM can't forget it, until the GM resolves it or the
// server sees it end. A resolved item is never listed (it is not put on the list, or it is removed). What the GM resolves in the same breath
// (a one-shot) is not an open item: it stays in the reply that said it.
//
//   - handOverToGM is the one writer of a new item (idempotent on creature, entry and targets: a repeat refreshes it); resolveGMItems and
//     snoozeGMItem are the other writers of gm_open_items. gmOpenItems is the one reader and a pure read (GET /api/gm/status only reads under
//     the turn schedule): an item is live while its end has not been seen, asked of the owners the game itself asks (monster Concentration, the
//     creature in the fight, the fight, the round). The end hooks also resolve it at the moment of the event, so the table stays tidy; the reader
//     never depends on them.
//   - Snooze, not intervals: the GM can hide an item until the next round or for N GM check-ins; it comes back by itself.
//   - Routes: GET, POST and DELETE /api/gm/open-items; status carries open_items and one task line, the heartbeat's GM block the count.

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The end kinds: how the server sees an item end.
const (
	endConcentration = "concentration" // the creature's Concentration on the spell (end_key: its slug)
	endCreature      = "creature"      // the creature is in the fight
	endFight         = "fight"         // the fight is on
	endRounds        = "rounds"        // the fight's round reaches ends_round
	endNever         = "never"         // only the GM ends it
)

// gmHandover is what a site that hands the GM a lasting mechanic says about it.
type gmHandover struct {
	Lobby   int
	Source  int    // the monster's combat id (0: none)
	Targets []int  // the creatures it is on
	Kind    string // "spell", "entry", "gm"
	Entry   string // the stat-block entry or the spell's name
	Title   string // one line: "Gaseous Form (Djinni) on Aldric"
	Detail  string // the rule text the GM needs
	EndKind string
	EndKey  string
	Rounds  int // for endRounds: how many rounds from now
}

func targetKey(ids []int) string {
	c := append([]int{}, ids...)
	sort.Ints(c)
	out := make([]string, len(c))
	for i, id := range c {
		out[i] = strconv.Itoa(id)
	}
	return strings.Join(out, ",")
}

func parseTargetKey(s string) []int {
	var out []int
	for _, p := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// handOverToGM lists a lasting mechanic for the GM. A repeat of the same creature, entry and targets refreshes the open item. A failed
// write is logged and the reply still carries the text: nothing is held in memory.
func handOverToGM(h gmHandover) int {
	if db == nil || h.Lobby == 0 || strings.TrimSpace(h.Title) == "" {
		return 0
	}
	round := currentRound(h.Lobby)
	endsRound := 0
	if h.EndKind == endRounds {
		endsRound = round + h.Rounds
	}
	key := targetKey(h.Targets)
	var id int
	err := inTx(func(tx *sql.Tx) error {
		err := tx.QueryRow(`UPDATE gm_open_items SET title=$1, detail=$2, end_kind=$3, end_key=$4, ends_round=$5, created_round=$6, created_at=NOW(), snoozed_until=NULL, snooze_round=0
			WHERE lobby_id=$7 AND source_id=$8 AND entry=$9 AND target_ids=$10 AND resolved_at IS NULL RETURNING id`,
			h.Title, h.Detail, h.EndKind, h.EndKey, endsRound, round, h.Lobby, h.Source, h.Entry, key).Scan(&id)
		if err == nil {
			return nil
		}
		return tx.QueryRow(`INSERT INTO gm_open_items (lobby_id, kind, source_id, target_ids, entry, title, detail, end_kind, end_key, ends_round, created_round)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
			h.Lobby, h.Kind, h.Source, key, h.Entry, h.Title, h.Detail, h.EndKind, h.EndKey, endsRound, round).Scan(&id)
	})
	if err != nil {
		log.Printf("gm open items: couldn't list %q for campaign %d: %v", h.Title, h.Lobby, err)
		return 0
	}
	return id
}

// gmItem is one open item as stored.
type gmItem struct {
	ID           int
	Kind, Entry  string
	Title        string
	Detail       string
	EndKind      string
	EndKey       string
	Source       int
	Targets      []int
	EndsRound    int
	CreatedRound int
	CreatedAt    time.Time
	SnoozedUntil *time.Time
	SnoozeRound  int
}

// gmItemView is what the GM reads.
type gmItemView struct {
	ID         int      `json:"id"`
	Creature   string   `json:"creature,omitempty"`
	Targets    []string `json:"targets,omitempty"`
	Entry      string   `json:"entry"`
	Title      string   `json:"title"`
	Rule       string   `json:"rule"`
	SinceRound int      `json:"since_round,omitempty"`
	EndsWhen   string   `json:"ends_when"`
	Snoozed    bool     `json:"snoozed,omitempty"`
}

func loadOpenItems(lobby int) ([]gmItem, error) {
	rows, err := db.Query(`SELECT id, kind, source_id, target_ids, entry, title, detail, end_kind, end_key, ends_round, created_round, created_at, snoozed_until, snooze_round
		FROM gm_open_items WHERE lobby_id = $1 AND resolved_at IS NULL ORDER BY id`, lobby)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []gmItem
	for rows.Next() {
		var it gmItem
		var tk string
		var su *time.Time
		if err := rows.Scan(&it.ID, &it.Kind, &it.Source, &tk, &it.Entry, &it.Title, &it.Detail, &it.EndKind, &it.EndKey, &it.EndsRound, &it.CreatedRound, &it.CreatedAt, &su, &it.SnoozeRound); err != nil {
			return nil, err
		}
		it.Targets, it.SnoozedUntil = parseTargetKey(tk), su
		out = append(out, it)
	}
	return out, rows.Err()
}

// gmItemLive: the server has not seen the item end. It asks the owners the game asks.
func gmItemLive(lobby int, it gmItem, round int) bool {
	if it.Source < 0 && it.Kind != "gm" && it.EndKind != endNever && !monsterInFight(lobby, it.Source) { // what it holds ends with it (it left the fight or died); a lasting effect on others (Geas) stays
		return false
	}
	switch it.EndKind {
	case endConcentration:
		return it.Source != 0 && normalizeSpellSlug(monsterConcentrationOn(lobby, it.Source)) == normalizeSpellSlug(it.EndKey)
	case endCreature:
		return monsterInFight(lobby, it.Source)
	case endFight:
		return round > 0
	case endRounds:
		return round > 0 && round < it.EndsRound
	}
	return true
}

// gmItemEndsWhen: the end in words.
func gmItemEndsWhen(it gmItem) string {
	switch it.EndKind {
	case endConcentration:
		return "when its caster's Concentration on " + titleCase(strings.ReplaceAll(it.EndKey, "-", " ")) + " ends (the server tells you)"
	case endCreature:
		return "when its creature leaves the fight"
	case endFight:
		return "when the fight ends"
	case endRounds:
		return fmt.Sprintf("at round %d", it.EndsRound)
	}
	return "when you resolve it (DELETE /api/gm/open-items)"
}

// gmOpenItems is the one reader: the live items of the campaign, snoozed ones apart. A pure read.
func gmOpenItems(lobby int, withSnoozed bool) (list []gmItemView, snoozed int, err error) {
	items, err := loadOpenItems(lobby)
	if err != nil {
		return nil, 0, err
	}
	round, now := currentRound(lobby), time.Now().UTC()
	for _, it := range items {
		if !gmItemLive(lobby, it, round) {
			continue
		}
		hidden := (it.SnoozedUntil != nil && now.Before(*it.SnoozedUntil)) || (it.SnoozeRound > 0 && round < it.SnoozeRound)
		if hidden {
			snoozed++
			if !withSnoozed {
				continue
			}
		}
		v := gmItemView{ID: it.ID, Entry: it.Entry, Title: it.Title, Rule: it.Detail, SinceRound: it.CreatedRound, EndsWhen: gmItemEndsWhen(it), Snoozed: hidden}
		if it.Source != 0 {
			v.Creature = creatureName(lobby, it.Source)
		}
		for _, t := range it.Targets {
			v.Targets = append(v.Targets, creatureName(lobby, t))
		}
		list = append(list, v)
	}
	return list, snoozed, nil
}

// openItemsTask is the status's section: the task line and the items (nothing when none are open).
func openItemsTask(lobby int) (task string, items []gmItemView, snoozed int, err error) {
	items, snoozed, err = gmOpenItems(lobby, false)
	if err != nil || len(items) == 0 {
		return "", items, snoozed, err
	}
	names := []string{}
	for i, it := range items {
		if i == 4 {
			names = append(names, fmt.Sprintf("and %d more", len(items)-4))
			break
		}
		names = append(names, it.Title)
	}
	return fmt.Sprintf("📌 %d open item(s) the server can't run for you: %s. Read open_items for each rule; resolve one with DELETE /api/gm/open-items {\"item_id\": N}, or snooze it with POST (next_round, or a number of check-ins).", len(items), strings.Join(names, "; ")), items, snoozed, nil
}

// resolveGMItems resolves open items: all of the campaign's (item 0, creature 0), one, or a creature's. why says by whom ("gm", or "server: ...").
func resolveGMItems(lobby, item, creature int, all bool, why string) int {
	q := `UPDATE gm_open_items SET resolved_at = NOW(), resolved_by = $2 WHERE lobby_id = $1 AND resolved_at IS NULL`
	args := []interface{}{lobby, why}
	switch {
	case item != 0:
		q += fmt.Sprintf(` AND id = $%d`, len(args)+1)
		args = append(args, item)
	case creature != 0:
		q += fmt.Sprintf(` AND (source_id = $%d OR target_ids = $%d OR target_ids LIKE $%d OR target_ids LIKE $%d OR target_ids LIKE $%d)`, len(args)+1, len(args)+2, len(args)+3, len(args)+4, len(args)+5)
		c := strconv.Itoa(creature)
		args = append(args, creature, c, c+",%", "%,"+c, "%,"+c+",%")
	case !all:
		return 0
	}
	res, err := db.Exec(q, args...)
	if err != nil {
		log.Printf("gm open items: couldn't resolve in campaign %d: %v", lobby, err)
		return 0
	}
	n, _ := res.RowsAffected()
	return int(n)
}

// resolveGMItemsEnding: the server saw the end of what these items wait for (a creature left the fight, a Concentration ended, the fight
// ended): they are resolved at the moment of the event.
func resolveGMItemsEnding(lobby int, endKind string, source int, key string) int {
	if db == nil {
		return 0
	}
	// endKind "" is the creature leaving or the fight ending: everything but what only the GM ends, and never the GM's own items
	q := `UPDATE gm_open_items SET resolved_at = NOW(), resolved_by = $2 WHERE lobby_id = $1 AND resolved_at IS NULL AND end_kind = $3`
	args := []interface{}{lobby, "server: it ended (" + endKind + ")", endKind}
	if endKind == "" {
		q = `UPDATE gm_open_items SET resolved_at = NOW(), resolved_by = $2 WHERE lobby_id = $1 AND resolved_at IS NULL AND end_kind <> 'never' AND kind <> 'gm' AND $3 = ''`
		args[1] = "server: its creature left the fight or the fight ended"
	}
	if source != 0 {
		q += ` AND source_id = $4`
		args = append(args, source)
		if key != "" {
			q += ` AND end_key = $5`
			args = append(args, key)
		}
	}
	res, err := db.Exec(q, args...)
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return int(n)
}

// snoozeGMItem hides an item until the next round, or for n GM check-ins; it comes back by itself. A time is stored, so a read stays a read.
func snoozeGMItem(lobby, item int, nextRound bool, checkins int) (bool, error) {
	if nextRound {
		res, err := db.Exec(`UPDATE gm_open_items SET snooze_round = $3, snoozed_until = NULL WHERE lobby_id = $1 AND id = $2 AND resolved_at IS NULL`, lobby, item, currentRound(lobby)+1)
		if err != nil {
			return false, err
		}
		n, _ := res.RowsAffected()
		return n > 0, nil
	}
	until := nthGMCheckIn(lobby, time.Now().UTC(), checkins+1) // the check-ins to skip, then the one it returns at
	res, err := db.Exec(`UPDATE gm_open_items SET snoozed_until = $3, snooze_round = 0 WHERE lobby_id = $1 AND id = $2 AND resolved_at IS NULL`, lobby, item, until)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// nthGMCheckIn: the nth GM check-in after now by the campaign's schedule (a quarter of an hour apart when the schedule is off).
func nthGMCheckIn(lobby int, now time.Time, n int) time.Time {
	if n < 1 {
		n = 1
	}
	in, _, err := loadCheckIn(lobby)
	if err != nil || in.State == scheduleOff || len(in.Sched.GM) == 0 {
		return now.Add(time.Duration(n) * 15 * time.Minute)
	}
	count := 0
	for h := now.Truncate(time.Hour); count < n && h.Before(now.Add(48*time.Hour)); h = h.Add(time.Hour) {
		for _, m := range in.Sched.GM {
			if t := h.Add(time.Duration(m) * time.Minute); t.After(now) {
				if count++; count == n {
					return t
				}
			}
		}
	}
	return now.Add(time.Duration(n) * 15 * time.Minute)
}

// gmOpenItemsRequest is what the GM sends to /api/gm/open-items.
type gmOpenItemsRequest struct {
	ItemID     int    `json:"item_id"`     // the item (DELETE: resolve it; POST with snooze: hide it)
	CreatureID int    `json:"creature_id"` // DELETE: resolve every item of this creature; POST with text: the creature your own item is about
	All        bool   `json:"all"`         // DELETE: resolve every open item of the campaign
	Snooze     string `json:"snooze"`      // POST with item_id: "next_round" (back at the next round) or "checkins" (back after checkins GM check-ins)
	Checkins   int    `json:"checkins"`    // with snooze "checkins": how many GM check-ins to skip, 1 to 48
	Text       string `json:"text"`        // POST: your own item, the rule or fact to keep in mind
	Title      string `json:"title"`       // POST with text: its one-line title (default: the start of the text)
}

// handleGMOpenItems godoc
// @Summary The GM's open items: lasting mechanics the server can't run
// @Description GET lists the open items of the campaign (?all=true also the snoozed ones): each is a mechanic the server handed to the GM that keeps going (a monster's lasting spell the server doesn't run, a stat-block effect with its own end), with the creature, the rule and when it ends. They are also in GET /api/gm/status as open_items, with one task line, at every check-in, until you resolve them or the server sees them end. DELETE resolves one ({"item_id": N}), a creature's ({"creature_id": -3}) or all ({"all": true}); a resolved item is never listed again. POST {"item_id": N, "snooze": "next_round"} (or {"snooze": "checkins", "checkins": 2}) hides an item until then; POST {"text": "...", "creature_id": -3} adds an item of your own.
// @Tags GM
// @Produce json
// @Param Authorization header string true "Bearer <token>"
// @Param campaign_id query int false "The campaign (default: the GM's only active campaign)"
// @Param all query bool false "GET: include the snoozed items"
// @Body Open items (POST and DELETE)
// @Success 200 {object} map[string]interface{} "open_items"
// @Router /gm/open-items [get]
// @Router /gm/open-items [post]
// @Router /gm/open-items [delete]
func handleGMOpenItems(w http.ResponseWriter, r *http.Request) {
	var req gmOpenItemsRequest
	_, lobby, ok := gmRead(w, r, &req, optionalBody())
	if !ok {
		return
	}
	reply := func(extra map[string]interface{}) {
		list, snoozed, err := gmOpenItems(lobby, r.URL.Query().Get("all") == "true")
		out := map[string]interface{}{"campaign_id": lobby, "open_items": list, "snoozed": snoozed}
		if err != nil {
			log.Printf("gm open items: couldn't read campaign %d: %v", lobby, err)
			handledDBError(err)
			out["open_items"] = []gmItemView{}
			out["open_items_error"] = "The open items could not be read just now: try again."
		}
		for k, v := range extra {
			out[k] = v
		}
		writeJSONStatus(w, http.StatusOK, out)
	}
	switch r.Method {
	case http.MethodGet:
		reply(nil)
	case http.MethodDelete:
		if req.ItemID == 0 && req.CreatureID == 0 && !req.All {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "item_creature_or_all_required", "message": `Send {"item_id": N}, {"creature_id": -3} or {"all": true}.`})
			return
		}
		n := resolveGMItems(lobby, req.ItemID, req.CreatureID, req.All, "gm")
		if n == 0 && req.ItemID != 0 {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "no_such_open_item", "message": "That item is not open in this campaign: it is resolved already, ended, or never existed. GET /api/gm/open-items lists the open ones."})
			return
		}
		reply(map[string]interface{}{"resolved": n})
	case http.MethodPost:
		switch {
		case req.ItemID != 0 && req.Snooze != "":
			var done bool
			var err error
			switch req.Snooze {
			case "next_round":
				done, err = snoozeGMItem(lobby, req.ItemID, true, 0)
			case "checkins":
				if req.Checkins < 1 || req.Checkins > 48 {
					writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "checkins_required", "message": `With "snooze": "checkins", send checkins: how many GM check-ins to skip, 1 to 48.`})
					return
				}
				done, err = snoozeGMItem(lobby, req.ItemID, false, req.Checkins)
			default:
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "unknown_snooze", "message": `snooze is "next_round" or "checkins".`})
				return
			}
			if answerDBFailure(w, err, "the open item") {
				return
			}
			if !done {
				writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "no_such_open_item", "message": "That item is not open in this campaign."})
				return
			}
			reply(map[string]interface{}{"snoozed_item": req.ItemID})
		case strings.TrimSpace(req.Text) != "":
			title := strings.TrimSpace(req.Title)
			if title == "" {
				title = strings.TrimSpace(req.Text)
				if len(title) > 80 {
					title = title[:80] + "..."
				}
			}
			id := handOverToGM(gmHandover{Lobby: lobby, Source: req.CreatureID, Kind: "gm", Entry: "GM's own: " + title, Title: title, Detail: strings.TrimSpace(req.Text), EndKind: endNever})
			if id == 0 {
				writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "database_error", "message": "The item could not be saved."})
				return
			}
			reply(map[string]interface{}{"item_id": id})
		default:
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "snooze_or_text_required", "message": `Send {"item_id": N, "snooze": "next_round"}, {"item_id": N, "snooze": "checkins", "checkins": 2} or {"text": "...", "creature_id": -3}.`})
		}
	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
	}
}
