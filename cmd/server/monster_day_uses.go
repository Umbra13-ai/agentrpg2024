package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
)

// Per-day uses of a stat block ("Smoke Bomb (1/Day)", Spellcasting's "2/Day Each: Fly, Lightning Bolt", "1/Day: ..."). The count is kept
// per creature in a ledger (table monster_day_uses), so it lasts from one fight to the next and through a restart. A creature's identity
// is its creature_id: combat/add gives every monster a fresh one (two creatures with one name never share uses, a rename changes
// nothing), and the GM links a returning creature by giving its creature_id again (same stat block) when adding it. A creature added
// without one is a new creature. Monsters the fight makes by itself (no creature_id) are counted for that fight only. The server does not
// know when a day ends: the GM calls new_day. Documented in docs/MONSTER_SYSTEM.md.

var (
	spellListHeadRe = regexp.MustCompile(`(?i)(At Will|(\d+)/Day( Each)?):`) // any case: an imported entry writes "1/day each"
	// restRechargeRe: "(Recharge after a Short or Long Rest)" (Cloaker Phantasms; an import writes "Recharges after a Long Rest", "Recharges at the Next
	// Dawn"): one use until the rest it names. Group 1 is what it waits for.
	restRechargeRe   = regexp.MustCompile(`(?i)\(Recharges? [a-z]+ (?:the |a )?([^)]+)\)`)
	spellParenRe     = regexp.MustCompile(`\s*\([^)]*\)`)
	spellLevelNoteRe = regexp.MustCompile(`^level \d version$`)
)

// spellTier is one list of a Spellcasting entry: "At Will" (Limit 0), "2/Day Each" (Limit 2, Each) or "1/Day" (Limit 1, shared).
type spellTier struct {
	Head   string
	Limit  int
	Each   bool
	Spells []string
	Notes  map[string]string // what the stat block adds to a spell in its list ("Charm Monster (lasts 24 hours; ...)"): the GM's
}

// spellTiersOf reads the lists of a Spellcasting entry in order.
func spellTiersOf(text string) []spellTier {
	locs := spellListHeadRe.FindAllStringSubmatchIndex(text, -1)
	var out []spellTier
	for i, l := range locs {
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		t := spellTier{Head: text[l[2]:l[3]]}
		if l[4] >= 0 {
			t.Limit, _ = strconv.Atoi(text[l[4]:l[5]])
		}
		t.Each = l[6] >= 0
		// a note may hold commas ("Invisibility (self only, and the hag leaves no tracks while Invisible)"): the notes come out first
		list := text[l[1]:end]
		notes := map[int]string{}
		list = spellParenRe.ReplaceAllStringFunc(list, func(p string) string {
			notes[len(notes)] = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(p), "("), ")")
			return fmt.Sprintf("\x00%d", len(notes)-1)
		})
		for _, n := range strings.Split(list, ",") {
			note := ""
			if i := strings.Index(n, "\x00"); i >= 0 {
				k, _ := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(n[i+1:]), ".")))
				note, n = notes[k], n[:i]
			}
			n = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(n), "."))
			if n != "" && len(n) < 40 {
				t.Spells = append(t.Spells, n)
				if note != "" && !spellLevelNoteRe.MatchString(note) { // "(level 3 version)" is the cast's own (monsterSpellFromUse)
					if t.Notes == nil {
						t.Notes = map[string]string{}
					}
					t.Notes[n] = note
				}
			}
		}
		out = append(out, t)
	}
	return out
}

// spellListNote: what a Spellcasting entry's list adds to one of its spells ("lasts 24 hours; ends early if the dryad casts the spell
// again", "24-hour duration", "self only, and the hag leaves no tracks while Invisible"), "" when nothing: the server casts the spell
// as the SRD gives it, so this is shown to the GM with the cast.
func spellListNote(text, spell string) string {
	for _, t := range spellTiersOf(text) {
		for n, note := range t.Notes {
			if strings.EqualFold(n, spell) {
				return note
			}
		}
	}
	return ""
}

// spellNamesOf: every spell name a Spellcasting entry lists.
func spellNamesOf(text string) []string {
	var out []string
	for _, t := range spellTiersOf(text) {
		out = append(out, t.Spells...)
	}
	return out
}

// monsterDayUse names the per-day counter the use of an entry spends and its limit; key is "" when the use is unlimited (At Will, no
// "/Day"). use is the spell the GM named for a Spellcasting entry.
func monsterDayUse(entry srdMonsterEntry, use string) (key string, limit int) {
	if period := restRechargePeriod(entry.Name); period != "" { // one use until the rest (a Recharge that rolls is rechargeRe's)
		return "rest:" + period + ":" + strings.ToLower(stripUsage(entry.Name)), 1
	}
	if m := perDayRe.FindStringSubmatch(entry.Name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return "action:" + strings.ToLower(stripUsage(entry.Name)), n
	}
	u := strings.ToLower(strings.TrimSpace(use))
	if u == "" {
		return "", 0
	}
	for _, t := range spellTiersOf(entry.Text) {
		for _, sp := range t.Spells {
			if strings.ToLower(sp) == u && t.Limit > 0 {
				if t.Each {
					return "spell:" + u, t.Limit
				}
				return "tier:" + strings.ToLower(t.Head), t.Limit
			}
		}
	}
	return "", 0
}

// restRechargePeriod: "short" for an entry that recharges after a Short Rest (alone or "Short or Long Rest"), "long" for one that waits for a
// Long Rest or the next dawn, "" for an entry with no rest recharge.
func restRechargePeriod(entryName string) string {
	m := restRechargeRe.FindStringSubmatch(entryName)
	if m == nil {
		return ""
	}
	if strings.Contains(strings.ToLower(m[1]), "short") {
		return "short"
	}
	return "long"
}

// restWords: what the entry waits for, as its name says it ("a Short or Long Rest", "the Next Dawn").
func restWords(entryName string) string {
	m := restRechargeRe.FindStringSubmatch(entryName)
	if m == nil {
		return "a rest"
	}
	if strings.HasPrefix(strings.ToLower(m[1]), "next dawn") {
		return "the " + m[1]
	}
	return "a " + m[1]
}

// endRest gives back the rest-recharged uses of every creature in the campaign: a Short Rest those that recharge after one, a Long Rest all of them
// (the GM says the creatures rested, as it says a new day began).
func endRest(campaignID int, long bool) {
	if db == nil {
		return
	}
	rows, err := db.Query(`SELECT creature_id, uses FROM monster_day_uses WHERE campaign_id = $1`, campaignID)
	if err != nil {
		return
	}
	type held struct {
		id   string
		uses map[string]int
	}
	var all []held
	for rows.Next() {
		var id string
		var raw []byte
		uses := map[string]int{}
		if rows.Scan(&id, &raw) == nil && json.Unmarshal(raw, &uses) == nil {
			all = append(all, held{id, uses})
		}
	}
	rows.Close()
	for _, h := range all {
		changed := false
		for k := range h.uses {
			if strings.HasPrefix(k, "rest:short:") || (long && strings.HasPrefix(k, "rest:long:")) {
				delete(h.uses, k)
				changed = true
			}
		}
		if changed {
			out, _ := json.Marshal(h.uses)
			db.Exec(`UPDATE monster_day_uses SET uses = $3 WHERE campaign_id = $1 AND creature_id = $2`, campaignID, h.id, string(out))
		}
	}
}

// dayUseRefusal is the refusal of a use past its limit, or "".
func dayUseRefusal(s monsterState, name string, entry srdMonsterEntry, use, key string, limit int) string {
	if key == "" || s.DayUses[key] < limit {
		return ""
	}
	what := stripUsage(entry.Name)
	if strings.HasPrefix(key, "spell:") || strings.HasPrefix(key, "tier:") {
		what = strings.TrimSpace(use) + " (Spellcasting)"
	}
	safe := fmt.Sprintf("%s has already used %s. Nothing was rolled.", name, what)
	if strings.HasPrefix(key, "rest:") {
		return refusalAudiences(safe, fmt.Sprintf("%s has already used %s: it recharges after %s (counted per creature across fights; the GM says the creatures rested with monster-features rest, or ends the day with new_day)", name, what, restWords(entry.Name)))
	}
	return refusalAudiences(safe, fmt.Sprintf("%s has used %s %d time(s) already: the stat block allows %d/Day (counted per creature across fights; the GM ends the day with monster-features new_day, or corrects the count with day_uses)", name, what, s.DayUses[key], limit))
}

// newCreatureID is the identity a monster gets when it is added to a fight.
func newCreatureID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return "cr-" + hex.EncodeToString(b)
}

// dayCreatureID is the ledger identity of a monster in the fight: its creature_id, or (for a monster the fight made by itself) a
// fight-only one that goes with the fight.
func dayCreatureID(lobbyID, id int) string {
	if id >= 0 {
		return ""
	}
	for _, e := range combatEntries(lobbyID) {
		if asInt(e["id"]) == id {
			if c := sprintAny(e["creature_id"]); c != "" && c != "<nil>" {
				return c
			}
			return fmt.Sprintf("fight:%d", id)
		}
	}
	return ""
}

// registerCreature records a creature in the ledger when it joins a fight (its name is a label, updated each time).
func registerCreature(campaignID int, creatureID, name, monsterKey string) {
	if db == nil || creatureID == "" {
		return
	}
	db.Exec(`INSERT INTO monster_day_uses (campaign_id, creature_id, name, monster_key) VALUES ($1,$2,$3,$4)
		ON CONFLICT (campaign_id, creature_id) DO UPDATE SET name = EXCLUDED.name`, campaignID, creatureID, name, monsterKey)
}

// creatureStatBlock is the stat block key of a creature known to the ledger ("" when the campaign has no such creature).
func creatureStatBlock(campaignID int, creatureID string) string {
	var key string
	if db != nil {
		db.QueryRow(`SELECT monster_key FROM monster_day_uses WHERE campaign_id = $1 AND creature_id = $2 AND creature_id NOT LIKE 'fight:%'`, campaignID, creatureID).Scan(&key)
	}
	return key
}

// creatureLedger lists the creatures the campaign knows (for the GM to link a returning one).
func creatureLedger(campaignID int) []map[string]interface{} {
	out := []map[string]interface{}{}
	if db == nil {
		return out
	}
	rows, err := db.Query(`SELECT creature_id, name, monster_key, uses FROM monster_day_uses WHERE campaign_id = $1 AND creature_id NOT LIKE 'fight:%' ORDER BY creature_id`, campaignID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, key string
		var raw []byte
		rows.Scan(&id, &name, &key, &raw)
		uses := map[string]int{}
		json.Unmarshal(raw, &uses)
		out = append(out, map[string]interface{}{"creature_id": id, "name": name, "monster_key": key, "day_uses": uses})
	}
	return out
}

func loadDayUses(lobbyID, id int) map[string]int {
	out := map[string]int{}
	cid := dayCreatureID(lobbyID, id)
	if db == nil || cid == "" {
		return out
	}
	var raw []byte
	db.QueryRow(`SELECT uses FROM monster_day_uses WHERE campaign_id = $1 AND creature_id = $2`, lobbyID, cid).Scan(&raw)
	json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]int{}
	}
	return out
}

func saveDayUses(lobbyID, id int, uses map[string]int) {
	cid := dayCreatureID(lobbyID, id)
	if db == nil || cid == "" {
		return
	}
	if uses == nil {
		uses = map[string]int{}
	}
	raw, _ := json.Marshal(uses)
	db.Exec(`INSERT INTO monster_day_uses (campaign_id, creature_id, name, uses) VALUES ($1,$2,$3,$4)
		ON CONFLICT (campaign_id, creature_id) DO UPDATE SET uses = EXCLUDED.uses`, lobbyID, cid, monsterName(lobbyID, id), string(raw))
}

// forgetFightCreatures drops the fight-only counts (monster ids are reused by the next fight); linked creatures stay.
func forgetFightCreatures(campaignID int) {
	if db != nil {
		db.Exec(`DELETE FROM monster_day_uses WHERE campaign_id = $1 AND creature_id LIKE 'fight:%'`, campaignID)
	}
}

// creatureIDFor: the identity of a monster added to a fight: the one the GM linked it to, or a new one.
func creatureIDFor(linked string) string {
	if linked != "" {
		return linked
	}
	return newCreatureID()
}

// Execution record: what the server actually carried out for one monster action, said by the code that did it (an attack rolled against
// a target, a saving throw effect applied to its targets, a reaction that took effect), never read back from the reply text. A miss, a
// save that succeeds, immunity and a condition-only effect are all executed; a hand-off to the GM, a preview and a refusal are not.
// Executed or not, a day use is spent when the request ran the entry (usedSince): a hand-off is the GM using it (owner, Nov 23, I-WL-2).
type execKey struct{}

type execRecord struct {
	executed         int32
	confirmTargeting bool   // the GM confirmed any range or sight the server cannot determine
	targetBlocked    bool   // an explicit prerequisite blocked the action before it rolled (set only by blockedBeforeRoll)
	dayHandled       bool   // monsterAction reserves its own entry's use; inner entries do not take it twice
	fromCaller       bool   // the record came with the request (the narrate route), so it will settle a pending attack
	pending          func() // the day use an attack settles after it was rolled (set by monsterAction, run by the narrate route)
}

func withExecution(r *http.Request) (*http.Request, *execRecord) {
	rec := &execRecord{}
	if r == nil {
		return r, rec
	}
	return r.WithContext(context.WithValue(r.Context(), execKey{}, rec)), rec
}

func execIn(r *http.Request) *execRecord {
	if r == nil {
		return nil
	}
	rec, _ := r.Context().Value(execKey{}).(*execRecord)
	return rec
}

// markExecuted: the server carried the action out (called by the code that rolled or applied it).
func markExecuted(r *http.Request) {
	if rec := execIn(r); rec != nil {
		atomic.AddInt32(&rec.executed, 1)
	}
}

// blockedBeforeRoll: the action can't run until the GM names something it needs (a target, a spell or effect it offers, a damage type)
// or an explicit prerequisite fails; nothing was rolled or spent. The callers answer it as a refusal (gm/narrate: 409, nothing posted;
// a Legendary Action: target_ineligible), never as a result. Every effect resolver that stops this way says so here.
func blockedBeforeRoll(r *http.Request, lines ...string) []string {
	if rec := execIn(r); rec != nil {
		rec.targetBlocked = true
	}
	return lines
}

func (e *execRecord) wasExecuted() bool { return e != nil && atomic.LoadInt32(&e.executed) > 0 }

// reserveDayUse takes one use of the creature's day counter if one is left, in one database transaction (the row is locked), so two
// requests cannot both take the last use. False: none left (or no database: the use is not limited then).
func reserveDayUse(lobbyID, id int, key string, limit int) bool {
	cid := dayCreatureID(lobbyID, id)
	if db == nil || cid == "" {
		return true
	}
	name := monsterName(lobbyID, id) // read BEFORE the transaction: the counter's tx must not hold a
	// pool connection while the name lookup asks the pool for another (db_guard_2024.go, AUD-02).
	tx, err := db.Begin()
	if err != nil {
		return true
	}
	defer tx.Rollback()
	tx.Exec(`INSERT INTO monster_day_uses (campaign_id, creature_id, name) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, lobbyID, cid, name)
	var raw []byte
	if tx.QueryRow(`SELECT uses FROM monster_day_uses WHERE campaign_id = $1 AND creature_id = $2 FOR UPDATE`, lobbyID, cid).Scan(&raw) != nil {
		return true
	}
	uses := map[string]int{}
	json.Unmarshal(raw, &uses)
	if uses[key] >= limit {
		return false
	}
	uses[key]++
	out, _ := json.Marshal(uses)
	tx.Exec(`UPDATE monster_day_uses SET uses = $3 WHERE campaign_id = $1 AND creature_id = $2`, lobbyID, cid, string(out))
	return tx.Commit() == nil
}

// releaseDayUse gives a reserved use back (the use was not executed).
func releaseDayUse(lobbyID, id int, key string) {
	cid := dayCreatureID(lobbyID, id)
	if db == nil || cid == "" {
		return
	}
	tx, err := db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	var raw []byte
	if tx.QueryRow(`SELECT uses FROM monster_day_uses WHERE campaign_id = $1 AND creature_id = $2 FOR UPDATE`, lobbyID, cid).Scan(&raw) != nil {
		return
	}
	uses := map[string]int{}
	json.Unmarshal(raw, &uses)
	if uses[key] > 1 {
		uses[key]--
	} else {
		delete(uses, key)
	}
	out, _ := json.Marshal(uses)
	tx.Exec(`UPDATE monster_day_uses SET uses = $3 WHERE campaign_id = $1 AND creature_id = $2`, lobbyID, cid, string(out))
	tx.Commit()
}

// ensureExecution gives monsterAction the request's record, or a new one for an entry point that has none.
func ensureExecution(r *http.Request) (*http.Request, *execRecord) {
	if rec := execIn(r); rec != nil {
		return r, rec
	}
	return withExecution(r)
}

// newDay gives back every creature's per-day uses in the campaign (the GM says a new day has begun).
func newDay(campaignID int) {
	if db != nil {
		db.Exec(`UPDATE monster_day_uses SET uses = '{}' WHERE campaign_id = $1`, campaignID)
	}
}

// dayUseCounted is the line that says a use was counted (a confirmed use, never a refusal).
func dayUseCounted(name, key string, used, limit int) string {
	return fmt.Sprintf("Day use counted for %s: %d of %d/Day (%s).", name, used, limit, strings.TrimPrefix(strings.TrimPrefix(key, "action:"), "spell:"))
}

// usedSince: a reserved day use stays spent when the request ran the entry since the count was before: the server carried something out
// or handed the entry to the GM, who uses it (owner, Nov 23: a 1/Day the GM resolves was never counted, I-WL-2). Only a request that ran
// nothing gives the use back: one blocked before anything was rolled (blockedBeforeRoll), a refusal, a preview (which reserves nothing).
func (e *execRecord) usedSince(before int32) bool {
	return e != nil && (e.count() > before || !e.targetBlocked)
}

func (e *execRecord) count() int32 {
	if e == nil {
		return 0
	}
	return atomic.LoadInt32(&e.executed)
}

// dayListHeadLooseRe: anything that reads like a per-day list heading of a Spellcasting entry, in any wording ("3e/day each:").
var dayListHeadLooseRe = regexp.MustCompile(`(?i)\b\d+\w*\s*/\s*day\b[^:.;]{0,12}:`)

// unreadDayLists: the per-day list headings of a Spellcasting entry the server does not read (spellListHeadRe), so a spell under one is
// not counted: the import's warning names them.
func unreadDayLists(text string) []string {
	var out []string
	for _, h := range dayListHeadLooseRe.FindAllString(text, -1) {
		if !spellListHeadRe.MatchString(h) {
			out = append(out, strings.TrimSpace(h))
		}
	}
	return out
}
