package main

// A monster's token move (PUT /battle-map by the GM) is checked against its Speed (monster_move_2024.go). The GM moves monster tokens,
// so this is a note, not a refusal: the server cannot know how much of its Speed the monster already used this turn, nor Dash,
// Difficult Terrain along the path or a Fly/Swim/Climb Speed. Per update it says the straight-line distance moved (diagonals counted
// as 5 feet, a lower bound of the path) against the walking Speed after Speed effects (Slowed, Grappled, Exhaustion ...). A monster
// whose Speed is 0 that moved, or an update beyond the Speed, is flagged; a Speed combination that needs a ruling is passed on.

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"
)

// attachPairQuerier: *sql.DB for the post-commit note passes, the handler's *sql.Tx for reads inside an
// open map transaction (the transaction guard flags bare db reads there; the row view must be the tx's).
type attachPairQuerier interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
}

// attachCarriedPairs: the live attaches in the campaign, source id -> target id, read on the given handle.
// The serialization boundary: every attach-row writer (onset, both detach routes, the voluntary detach,
// the death sweep) holds the encounter's combat_state row FOR UPDATE before writing (beginAttachWrite),
// and the map transaction holds the same row for its whole duration - so inside a map transaction this
// read sees a complete attach set: a writer either committed before the map transaction locked (visible
// here) or commits after it (blocked on the lock). A committed map therefore never misses a live attach
// for the carry or the refusal. The FOR UPDATE on this read is defense in depth against any FUTURE writer
// that skips beginAttachWrite: an existing row still cannot be deleted or replaced under the map
// transaction. Outside a transaction (the note passes) the read is a plain post-commit one. Dead rows
// never drive a carry: both sides must be alive. A read failure answers an empty map and logs: the carry
// checks fail open into the note path, never into a false refusal.
func attachCarriedPairs(q attachPairQuerier, lobbyID int) map[int]int {
	carried := map[int]int{}
	query := `SELECT source_id, target_id FROM combat_effects WHERE lobby_id=$1 AND kind='attached'`
	if _, inTx := q.(*sql.Tx); inTx {
		query += ` FOR UPDATE`
	}
	rows, err := q.Query(query, lobbyID)
	if err != nil {
		log.Printf("monster move: couldn't read the attaches in campaign %d: %v", lobbyID, err)
		return carried
	}
	defer rows.Close()
	for rows.Next() {
		var src, tgt int
		if rows.Scan(&src, &tgt) == nil && attachSideAlive(lobbyID, src) && attachSideAlive(lobbyID, tgt) {
			carried[src] = tgt
		}
	}
	return carried
}

// attachCarryProblem enforces the published carry ("it moves with the target") on the GM's map update: when
// the target's token moved and the attached monster's token did not move by the same displacement, the
// update is refused. Explicit GM overrides: move the pair together (a share-space pair or matching moves),
// detach it first, or send forced_movement (a push, a teleport, or the GM's ruling). Only attaches whose
// tokens are on the map before AND after are checked: a token the GM removed entirely is the GM's act.
// Reads the attach rows on the handler's open transaction. "" when no attach is violated.
func attachCarryProblem(tx attachPairQuerier, lobbyID int, before, after encounterMap) string {
	for src, tgt := range attachCarriedPairs(tx, lobbyID) {
		if src >= 0 { // only monsters attach in these rules
			continue
		}
		oldT, okT := before.token(tgt)
		newT, okT2 := after.token(tgt)
		oldS, okS := before.token(src)
		newS, okS2 := after.token(src)
		if !okT || !okT2 || !okS || !okS2 {
			continue
		}
		if oldT.Position == newT.Position {
			continue
		}
		if newS.Position.X-oldS.Position.X == newT.Position.X-oldT.Position.X &&
			newS.Position.Y-oldS.Position.Y == newT.Position.Y-oldT.Position.Y {
			continue
		}
		return fmt.Sprintf("%s is attached to %s and moves with it: move the pair together, detach it first, or send forced_movement for a forced move or a ruling.", newS.Name, newT.Name)
	}
	return ""
}

// carryAttachedTokens moves the tokens of creatures attached to the actor by the actor's own displacement
// ("it moves with the target") and answers the public carry lines. Called on the player and companion move
// routes, where the target moves its own token and cannot move the monster's.
func carryAttachedTokens(tx attachPairQuerier, lobbyID int, m *encounterMap, actorID int, actorName string, from, to mapSquare, altitude int) []string {
	dx, dy := to.X-from.X, to.Y-from.Y
	if dx == 0 && dy == 0 {
		return nil
	}
	var notes []string
	for src, tgt := range attachCarriedPairs(tx, lobbyID) {
		if tgt != actorID || src >= 0 {
			continue
		}
		for i, tk := range m.Tokens {
			if tk.ID != src {
				continue
			}
			m.Tokens[i].Position = mapSquare{X: tk.Position.X + dx, Y: tk.Position.Y + dy}
			m.Tokens[i].Altitude = altitude // it is on the target: it ends where the target ends
			notes = append(notes, fmt.Sprintf("%s moves with %s, attached.", tk.Name, actorName))
		}
	}
	return notes
}

// monsterMoved: the feet a monster has moved on the current combat turn (its own turn only), by the turn marker. Kept in memory: a restart
// of the server forgets it, which can only under-count (a missed warning, never a false one).
type monsterMovedKey struct{ lobby, id int }
type monsterMovedTurn struct {
	Marker string
	Feet   int
}

var monsterMoved sync.Map

// addMonsterMoved adds a move to the monster's total for the current combat turn and returns the total.
func addMonsterMoved(lobbyID, id, feet int) int {
	marker := combatTurnMarker(lobbyID)
	k := monsterMovedKey{lobbyID, id}
	cur := monsterMovedTurn{}
	if v, ok := monsterMoved.Load(k); ok {
		cur = v.(monsterMovedTurn)
	}
	if cur.Marker != marker {
		cur = monsterMovedTurn{Marker: marker}
	}
	cur.Feet += feet
	monsterMoved.Store(k, cur)
	return cur.Feet
}

// monsterSpeedList: the stat block's whole Speed line when it lists more than the walking Speed ("40 ft., Fly 80 ft., Swim 40 ft."), else "".
func monsterSpeedList(id, lobbyID int) string {
	for _, e := range combatEntries(lobbyID) {
		if asInt(e["id"]) != id {
			continue
		}
		var text string
		if monsterQueryRow(`SELECT COALESCE(speed_text,'') FROM monsters WHERE slug = $1`, sprintAny(e["monster_key"])).Scan(&text) == nil &&
			strings.ContainsAny(text, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") && strings.Count(text, "ft") > 1 {
			return text
		}
	}
	return ""
}

func monsterWalkSpeed(id int, lobbyID int) (int, bool) {
	for _, e := range combatEntries(lobbyID) {
		if asInt(e["id"]) != id {
			continue
		}
		key := sprintAny(e["monster_key"])
		if key == "" {
			return 0, false
		}
		var speed int
		if monsterQueryRow(`SELECT COALESCE(speed,0) FROM monsters WHERE slug = $1`, key).Scan(&speed) != nil {
			return 0, false
		}
		return speed, true
	}
	return 0, false
}

func noteMonsterMoves(lobbyID int, before, after encounterMap) []string {
	var notes []string
	alive := livingMonsterIDs(lobbyID)
	carried := attachCarriedPairs(db, lobbyID) // post-commit note pass: no open transaction
	for _, old := range before.Tokens {
		if old.ID >= 0 || !alive[old.ID] {
			continue
		}
		now, ok := after.token(old.ID)
		if !ok || now.Position == old.Position {
			continue
		}
		dx, dy := abs(now.Position.X-old.Position.X), abs(now.Position.Y-old.Position.Y)
		if tgt, ok := carried[old.ID]; ok {
			oldT, okT := before.token(tgt)
			newT, okT2 := after.token(tgt)
			if okT && okT2 && newT.Position.X-oldT.Position.X == now.Position.X-old.Position.X &&
				newT.Position.Y-oldT.Position.Y == now.Position.Y-old.Position.Y && oldT.Position != newT.Position {
				continue // carried by the creature it is attached to
			}
		}
		feet := 5 * max(dx, dy)
		moved, onTurn := feet, currentTurnID(lobbyID) == old.ID
		if onTurn { // on its own turn the moves add up: the Speed is a budget for the whole turn
			moved = addMonsterMoved(lobbyID, old.ID, feet)
		}
		tail := ""
		if onTurn && moved != feet {
			tail = fmt.Sprintf(" (%d ft this turn so far)", moved)
		}
		base, known := monsterWalkSpeed(old.ID, lobbyID)
		if !known {
			if n := monsterSpeedNote(lobbyID, old.ID); n != "" {
				notes = append(notes, fmt.Sprintf("%s moved %d ft; its Speed effects: %s.", old.Name, feet, n))
			}
			continue
		}
		eff, why, ruling := effectiveSpeed(lobbyID, old.ID, base)
		switch {
		case ruling:
			notes = append(notes, fmt.Sprintf("%s moved %d ft%s; its Speed (%d ft) is changed by a combination that needs the GM's ruling: %s.", old.Name, feet, tail, base, monsterSpeedNote(lobbyID, old.ID)))
		case eff == 0 && base > 0:
			notes = append(notes, fmt.Sprintf("%s moved %d ft%s but its Speed is 0 (%s).", old.Name, feet, tail, moveWhy(why)))
		case moved > eff && len(why) > 0:
			notes = append(notes, fmt.Sprintf("%s moved %d ft%s; its Speed is %d ft (%d ft, %s). Over its Speed unless it Dashed or used another Speed%s; the GM checks how much it had left.", old.Name, feet, tail, eff, base, moveWhy(why), speedList(lobbyID, old.ID)))
		case moved > base:
			notes = append(notes, fmt.Sprintf("%s moved %d ft%s; its Speed is %d ft. Over its Speed unless it Dashed or used another Speed%s; the GM checks how much it had left.", old.Name, feet, tail, base, speedList(lobbyID, old.ID)))
		}
	}
	return notes
}

// attachSeparationNotes flags a separated attach pair: the target's token moved, the attached monster's
// token stayed, and the attach row is still live. Runs on every accepted GM map update, forced or not:
// the non-forced route refuses the separation (attachCarryProblem), so the note is exactly the record of
// a forced move or a ruling that parted them, which the GM may need to end the attach for.
func attachSeparationNotes(lobbyID int, before, after encounterMap) []string {
	var notes []string
	for src, tgt := range attachCarriedPairs(db, lobbyID) { // post-commit: no open transaction

		if src >= 0 { // only monsters attach in these rules
			continue
		}
		oldT, okT := before.token(tgt)
		newT, okT2 := after.token(tgt)
		oldS, okS := before.token(src)
		newS, okS2 := after.token(src)
		if !okT || !okT2 || !okS || !okS2 {
			continue
		}
		if oldT.Position != newT.Position && oldS.Position == newS.Position {
			notes = append(notes, fmt.Sprintf("%s is attached to %s, which moved, but did not move with it (attached, it moves with the target).", newS.Name, newT.Name))
		}
	}
	return notes
}

func moveWhy(why []string) string {
	out := ""
	for i, w := range why {
		if i > 0 {
			out += "; "
		}
		out += w
	}
	return out
}

// speedList: ", its stat block lists: <Speed line>" when the monster has Speeds besides walking (so the GM sees which other one could apply).
func speedList(lobbyID, id int) string {
	if l := monsterSpeedList(id, lobbyID); l != "" {
		return fmt.Sprintf(" (its stat block lists: %s)", l)
	}
	return ""
}
