package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// The attach owner: "..., and the stirge attaches to the target." (Stirge Proboscis; Darkmantle Crush). The
// attach is a combat effect (kind "attached", combat_effects.go) the hit writes; it lives while both sides
// are alive and gates what the stat block hangs on it: the recurring damage "While attached, ... the target
// takes 5 (2d4) Necrotic damage at the start of each of the stirge's turns" (the recurring owner,
// monster_recurring_damage_2024.go, reads the row as its gate) and the lockout "While attached, the stirge
// can't make Proboscis attacks." (the lockout owner, monster_attack_lockout_2024.go, refuses the attack at
// its entry).
//
// Ending it: the published detach actions are the detach action owner's (POST /api/attach/detach: the
// stirge's - the target or a creature within 5 feet of it, an action, no check; the darkmantle's - the
// attached creature's own action, DC 13 Strength (Athletics)). The voluntary detach ("can detach itself by
// spending 5 feet of its movement") is the attacher's own move: the GM adjudicates it with the movement
// rules (/api/gm/detach). The attach also ends when either side dies (attachSweep, read at every turn
// start, because a dead creature has no turn of its own to end it on), and the GM can end it whenever the
// table rules it (an ally pulls the creature off, a spell forces it). Whether the target can move and
// carry the attacher along: the darkmantle's text says it moves with the target; the stirge's is silent -
// the GM's, like every movement question the blocks leave open.

// attachOnHitRe: the onset sentence of an attach ("and the stirge attaches to the target").
var attachOnHitRe = regexp.MustCompile(`(?i)\bthe [\w() '’-]+ attaches? to the target\b`)

// whileAttachedOnlyAdvRe: the while-attached rider that restricts the attacher's attacks and rewards the
// one it may still make ("While attached to a target, the darkmantle can attack only the target but has
// Advantage on its attack rolls."). Parsed when the hit writes the row and stored on it (" | only-target-adv"),
// so the attack entry's refusal and the roll's Advantage read the rule of the attach that holds. The next
// sentence of the same paragraph - Speed 0 and moving with the target - has no store to land in (the
// server moves no monsters on a map) and stays the GM's.
var whileAttachedOnlyAdvRe = regexp.MustCompile(`(?i)While attached to a target, the [\w() '’-]+ can attack only the target but has Advantage on its attack rolls\.?`)

// attachOnlyTarget: the live attach the attacker holds whose stat block restricts its attacks; the
// attached creature's id. ok is false when no such attach holds (none, a dead side, or no rider).
func attachOnlyTarget(lobbyID, attackerID int) (tid int, ok bool) {
	if !attachSideAlive(lobbyID, attackerID) {
		return 0, false
	}
	rows, err := db.Query(`SELECT target_id, text FROM combat_effects WHERE lobby_id=$1 AND source_id=$2 AND kind='attached'`, lobbyID, attackerID)
	if err != nil {
		return 0, false
	}
	defer rows.Close()
	for rows.Next() {
		var t int
		var text string
		if rows.Scan(&t, &text) == nil && strings.Contains(text, "only-target-adv") && attachSideAlive(lobbyID, t) {
			return t, true
		}
	}
	return 0, false
}

// attachOnlyTargetRefusal: the stat block's "it can attack only the target" refuses an attack aimed at
// anything but the attached creature, at the attack entry, before a use is reserved or anything is
// rolled; "" when the attack is allowed.
func attachOnlyTargetRefusal(lobbyID, attackerID, intendedID int) string {
	tid, ok := attachOnlyTarget(lobbyID, attackerID)
	if !ok || intendedID == tid {
		return ""
	}
	name := creatureName(lobbyID, attackerID)
	safe := fmt.Sprintf("%s can attack only the creature it is attached to. Nothing was rolled.", name)
	return refusalAudiences(safe, fmt.Sprintf("%s can attack only %s while attached to it (its stat block's rule)", name, creatureName(lobbyID, tid)))
}

// attachSideAlive: an attach lasts while the attacher and its target are both alive (a companion's "alive"
// is the companion owner's companionGone; a character's is combatCharacterIsDead; a monster's is
// monsterAlive).
func attachSideAlive(lobbyID, id int) bool {
	switch {
	case isCompanionCombatID(id):
		return !companionGone(id)
	case id < 0:
		return monsterAlive(lobbyID, id)
	default:
		return !combatCharacterIsDead(id)
	}
}

// beginAttachWrite starts a transaction that serializes attach-row writes with battle-map updates. The
// map routes hold the encounter's combat_state row FOR UPDATE for their whole transaction; taking the
// same lock FIRST here means an attach change is either visible to a map transaction's pair read (it
// committed before the map transaction locked) or commits after the map transaction (it blocked on the
// lock), so a committed map always reflects a complete attach set - a new onset can no longer slip a
// separated pair past the carry/refusal checks. The lock precedes every other statement in the
// transaction, so a deadlock abort finds nothing else held and rolls nothing back partially. No active
// combat: no map update can be in flight and there is nothing to serialize with.
func beginAttachWrite(lobbyID int) (*sql.Tx, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	var one int
	if err := tx.QueryRow(`SELECT 1 FROM combat_state WHERE lobby_id=$1 AND active FOR UPDATE`, lobbyID).Scan(&one); err != nil && err != sql.ErrNoRows {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// addAttach records the attach a hit sets up. One attach per attacher (its lockout stops it attacking
// while attached, so a live replacement is unreachable; the replace keeps a retried write idempotent). It
// answers the public line part; "" when the hit has no attach onset.
// attachDetachRule: the published way to end an attach as an action (SRD 5.2.1): the stirge's is
// automatic for the target or a creature within 5 feet of it; the darkmantle's is the attached creature's
// own DC 13 Strength (Athletics) check. Read from the action's full text when the hit writes the row and
// stored on it (" | detach: ..."), so the detach action applies the rule of the attach that holds.
type attachDetachRule struct {
	DC       int    // 0: the action detaches it without a check
	Ability  string // e.g. "Strength"
	Skill    string // e.g. "Athletics"
	Adjacent bool   // a creature within 5 feet of the target may take the action too; false: the target only
}

var detachAutoRe = regexp.MustCompile(`(?i)the target or a creature within 5 feet of it can detach the [\w() '’-]+ as an action`)
var detachCheckRe = regexp.MustCompile(`(?i)a creature can take an action to try to detach the [\w() '’-]+ from itself, doing so with a successful DC (\d+) ([A-Za-z]+) \(([A-Za-z]+)\) check`)

// readAttachDetach reads the detach-as-an-action rule of an attach action's full text; nil when the text
// has none (then the attach ends on a death, or by the GM: /api/gm/detach).
func readAttachDetach(desc string) *attachDetachRule {
	if detachAutoRe.MatchString(desc) {
		return &attachDetachRule{Adjacent: true}
	}
	if m := detachCheckRe.FindStringSubmatch(desc); m != nil {
		dc, _ := strconv.Atoi(m[1])
		return &attachDetachRule{DC: dc, Ability: m[2], Skill: m[3]}
	}
	return nil
}

func (r attachDetachRule) text() string {
	if r.DC > 0 {
		return fmt.Sprintf("detach: an action, DC %d %s (%s) check, the attached creature only", r.DC, r.Ability, r.Skill)
	}
	return "detach: an action, no check, the attached creature or one within 5 feet of it"
}

// attachDetachFromRow reads the stored rule back off an attach row.
func attachDetachFromRow(text string) *attachDetachRule {
	if strings.Contains(text, "detach: an action, no check") {
		return &attachDetachRule{Adjacent: true}
	}
	if m := regexp.MustCompile(`detach: an action, DC (\d+) ([A-Za-z]+) \(([A-Za-z]+)\)`).FindStringSubmatch(text); m != nil {
		dc, _ := strconv.Atoi(m[1])
		return &attachDetachRule{DC: dc, Ability: m[2], Skill: m[3]}
	}
	return nil
}

// attachTieRe: the sentence that ties an effect to the attach itself ("while the darkmantle is attached in
// this way", "While attached to a target, ...").
var attachTieRe = regexp.MustCompile(`(?i)\bwhile [\w() '’-]*\battached\b`)

// attachOwnedConditions: the conditions the action's text ties to the attach ("... the Blinded condition ...
// while the darkmantle is attached in this way"): they end when the attach ends, and only them. source is
// the condition records' Source label the hit gave them under ("Darkmantle's Crush"), so an independent
// source of the same condition - another creature, or another action of the same creature - survives a
// detach.
func attachOwnedConditions(desc, source string) []string {
	var owned []string
	for _, s := range sentenceSplitRe.Split(desc, -1) {
		if !attachTieRe.MatchString(s) {
			continue
		}
		c := mfxCondRe.FindStringSubmatch(s)
		if c == nil {
			continue
		}
		for _, name := range regexp.MustCompile(`, | and | or `).Split(c[1], -1) {
			if n := strings.ToLower(strings.TrimSpace(name)); mfxConditions[n] && !slices.Contains(owned, n) {
				owned = append(owned, n)
			}
		}
	}
	return owned
}

var attachOwnedFromRowRe = regexp.MustCompile(`owns: ([a-z, ]+) @ (.+?)(?: \| |$)`)

// attachOwnedFromRow reads back the conditions an attach owns and the source label that gave them.
func attachOwnedFromRow(text string) ([]string, string) {
	m := attachOwnedFromRowRe.FindStringSubmatch(text)
	if m == nil {
		return nil, ""
	}
	var conds []string
	for _, c := range strings.Split(m[1], ",") {
		if c = strings.TrimSpace(c); c != "" {
			conds = append(conds, c)
		}
	}
	return conds, m[2]
}

// endAttachOwnedConditions ends the conditions an attach owns on its target: exactly the records the
// attach-tied clause created, matched on the target, the attacher's id and the stored source label, so an
// independent source of the same condition (another creature, another action of the same creature, a
// spell) survives. Called wherever an attach ends: the death sweep, the GM route and the detach action.
func endAttachOwnedConditions(lobbyID, attacherID, targetID int, rowText string) []conditionRecord {
	conds, source := attachOwnedFromRow(rowText)
	if len(conds) == 0 {
		return nil
	}
	where := `target_id = $1 AND source_id = $2 AND source = $3`
	args := []interface{}{targetID, attacherID, source}
	if targetID < 0 || isCompanionCombatID(targetID) { // a monster's or companion's combat id is the fight's
		where += ` AND lobby_id = $4`
		args = append(args, lobbyID)
	}
	var recs []conditionRecord
	for _, r := range findConditionRecords(where, args...) {
		if slices.Contains(conds, r.Condition) {
			recs = append(recs, r)
		}
	}
	return endConditionRecords(recs)
}

// attachEndedNote: the public line part for the conditions an ended attach owned ("the Blinded condition
// ends").
func attachEndedNote(lobbyID, targetID int, ended []conditionRecord) string {
	if len(ended) == 0 {
		return ""
	}
	var names []string
	for _, r := range ended {
		if !slices.Contains(names, r.Condition) {
			names = append(names, r.Condition)
		}
	}
	return fmt.Sprintf(" %s's %s condition ends.", creatureName(lobbyID, targetID), titleCase(strings.Join(names, " and ")))
}

// addAttach records the attach a hit sets up. coverOK says the hit's cover clause (the one its conditions
// and suffocating hang on) actually fired - its Advantage and size gates held. Only then does an
// attach-tied suffocating start (monster_suffocation_2024.go): a cover that never applied suffocates no
// one, and its sentence stays the GM's.
func addAttach(lobbyID, attackerID, targetID int, desc, source string, coverOK bool) string {
	hit := desc
	if i := strings.Index(desc, "Hit:"); i >= 0 {
		hit = desc[i+len("Hit:"):]
	}
	m := attachOnHitRe.FindString(hit)
	if m == "" {
		return ""
	}
	text := strings.TrimSpace(m)
	if rule := readAttachDetach(desc); rule != nil {
		text += " | " + rule.text()
	}
	if owned := attachOwnedConditions(desc, source); len(owned) > 0 {
		text += " | owns: " + strings.Join(owned, ", ") + " @ " + source
	}
	suffocates := attachSuffocates(desc)
	if suffocates {
		text += " | suffocates"
	}
	if whileAttachedOnlyAdvRe.MatchString(desc) {
		text += " | only-target-adv"
	}
	tx, err := beginAttachWrite(lobbyID)
	if err != nil {
		log.Printf("attach: couldn't begin the write for source %d in campaign %d: %v", attackerID, lobbyID, err)
		return "the attach could not be recorded"
	}
	defer tx.Rollback()
	tx.Exec(`DELETE FROM combat_effects WHERE lobby_id=$1 AND source_id=$2 AND kind='attached'`, lobbyID, attackerID)
	if _, err := tx.Exec(`INSERT INTO combat_effects (lobby_id, target_id, source_id, kind, text) VALUES ($1,$2,$3,'attached',$4)`, lobbyID, targetID, attackerID, text); err != nil {
		log.Printf("attach: couldn't record the row for source %d in campaign %d: %v", attackerID, lobbyID, err)
		return "the attach could not be recorded"
	}
	if err := tx.Commit(); err != nil {
		log.Printf("attach: couldn't commit the row for source %d in campaign %d: %v", attackerID, lobbyID, err)
		return "the attach could not be recorded"
	}
	if suffocates && coverOK {
		startSuffocation(lobbyID, attackerID, targetID, source)
	}
	return m
}

// attachSweep ends every attach whose sides are not both alive. Read at every turn start
// (condition_turns.go, stage 0): a dead creature has no turn of its own, so the first turn start after a
// death ends its attaches before anyone acts on them; the lockout and the recurring gate also read the
// rows live, so a stale row never takes effect between turns. It answers the public notes of what the
// endings took with them (the attach's own conditions, its suffocation).
func attachSweep(lobbyID int) (notes []string) {
	if db == nil {
		return nil
	}
	rows, err := db.Query(`SELECT id, source_id, target_id, text FROM combat_effects WHERE lobby_id=$1 AND kind='attached'`, lobbyID)
	if err != nil {
		log.Printf("attach sweep: couldn't read campaign %d: %v", lobbyID, err)
		return
	}
	type ended struct {
		rowID, src, tgt int
		text            string
	}
	var stale []ended
	for rows.Next() {
		var e ended
		if rows.Scan(&e.rowID, &e.src, &e.tgt, &e.text) == nil && (!attachSideAlive(lobbyID, e.src) || !attachSideAlive(lobbyID, e.tgt)) {
			stale = append(stale, e)
		}
	}
	rows.Close()
	if len(stale) == 0 {
		return nil
	}
	tx, err := beginAttachWrite(lobbyID)
	if err != nil {
		log.Printf("attach sweep: couldn't begin the write in campaign %d: %v", lobbyID, err)
		return nil // the next turn start retries
	}
	for _, e := range stale {
		if _, err := tx.Exec(`DELETE FROM combat_effects WHERE id=$1 AND lobby_id=$2`, e.rowID, lobbyID); err != nil {
			log.Printf("attach sweep: couldn't end a row in campaign %d: %v", lobbyID, err)
			tx.Rollback()
			return nil // atomic: every stale row ends together or none do; the next turn start retries
		}
		if err := pendAttachEnding(tx, lobbyID, e.src, e.tgt, e.text); err != nil {
			log.Printf("attach sweep: couldn't record a pending ending in campaign %d: %v", lobbyID, err)
			tx.Rollback()
			return nil
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("attach sweep: couldn't commit in campaign %d: %v", lobbyID, err)
		return nil
	}
	for _, e := range stale { // the endings the rows gated, drained (crash-durable) after the rows are gone
		note := fmt.Sprintf("%s is no longer attached to %s.", creatureName(lobbyID, e.src), creatureName(lobbyID, e.tgt)) +
			drainAttachEnding(lobbyID, e.src, e.tgt, e.text)
		notes = append(notes, note)
	}
	return notes
}

// endAttachRecurring ends the recurring damage an attach gated: a source-anchored row ("the stirge's
// turns") is never considered at a dead source's turn start, so without this it would outlive the attach.
func endAttachRecurring(lobbyID, sourceID, targetID int) {
	db.Exec(`DELETE FROM combat_effects WHERE lobby_id=$1 AND kind='recurring_damage' AND source_id=$2 AND target_id=$3 AND text LIKE '%while the attach lasts'`, lobbyID, sourceID, targetID)
}

// The ending effects an attach gated (recurring damage, owned conditions, suffocation) must not be lost
// to a crash: the attach row and a pending_attach_endings row are written in the SAME serialized
// transaction (beginAttachWrite), so a committed detach always has its cleanup recorded, including the
// suffocation's gained count (read while the suffocating row is still live). The drain runs the endings
// and removes the pending row; every ending helper is idempotent (a missing condition, recurring row or
// suffocating row is a no-op), and the suffocation's row delete and Exhaustion removal commit in one
// transaction, so a repeat drain after a crash anywhere is safe and a crash that already took the
// suffocating row still sheds exactly the recorded levels. The fast path drains right after the
// commit; the turn start (stage 0, condition_turns.go) drains anything left.
func drainAttachEnding(lobbyID, attacherID, targetID int, rowText string) string {
	endAttachRecurring(lobbyID, attacherID, targetID)
	note := attachEndedNote(lobbyID, targetID, endAttachOwnedConditions(lobbyID, attacherID, targetID, rowText))
	suffNote, err := endAttachSuffocation(lobbyID, attacherID, targetID, rowText)
	if err != nil { // the suffocation ending is left PENDING: the next drain retries it
		log.Printf("attach endings: the suffocation ending for %d on %d in campaign %d stays pending: %v", attacherID, targetID, lobbyID, err)
		return note
	}
	// A suffocating ending acknowledged itself inside its own transaction; this delete covers the
	// endings that never suffocated (and is a no-op otherwise).
	db.Exec(`DELETE FROM pending_attach_endings WHERE lobby_id=$1 AND attacher_id=$2 AND target_id=$3`, lobbyID, attacherID, targetID)
	return note + suffNote
}

// drainAttachEndings is the crash backstop: every recorded ending that has not run yet, oldest first.
func drainAttachEndings(lobbyID int) (notes []string) {
	rows, err := db.Query(`SELECT attacher_id, target_id, row_text FROM pending_attach_endings WHERE lobby_id=$1 ORDER BY id`, lobbyID)
	if err != nil {
		log.Printf("attach endings: couldn't read the pending rows in campaign %d: %v", lobbyID, err)
		return nil
	}
	type pending struct {
		att, tgt int
		text     string
	}
	var list []pending
	for rows.Next() {
		var p pending
		if rows.Scan(&p.att, &p.tgt, &p.text) == nil {
			list = append(list, p)
		}
	}
	rows.Close()
	for _, p := range list {
		if note := drainAttachEnding(lobbyID, p.att, p.tgt, p.text); note != "" {
			notes = append(notes, fmt.Sprintf("The ended attach's effects catch up:%s", note))
		}
	}
	return notes
}

// pendAttachEnding records the cleanup the deleting transaction must not lose. Call inside the
// beginAttachWrite transaction, after the attach row's DELETE. The suffocation's gained count is
// read HERE, while the suffocating row is still live: the drain's crash recovery (the row deleted,
// the Exhaustion not yet removed) relies on it.
func pendAttachEnding(tx *sql.Tx, lobbyID, attacherID, targetID int, rowText string) error {
	gained := 0
	if strings.Contains(rowText, "suffocates") {
		var text string
		if err := tx.QueryRow(`SELECT text FROM combat_effects WHERE lobby_id=$1 AND kind='suffocating' AND source_id=$2 AND target_id=$3`, lobbyID, attacherID, targetID).Scan(&text); err == nil {
			if m := suffocationRowRe.FindStringSubmatch(text); m != nil {
				gained, _ = strconv.Atoi(m[2])
			}
		}
	}
	_, err := tx.Exec(`INSERT INTO pending_attach_endings (lobby_id, attacher_id, target_id, row_text, suffocation_gained) VALUES ($1,$2,$3,$4,$5)`, lobbyID, attacherID, targetID, rowText, gained)
	return err
}

// @Summary Detach (GM)
// @Description The GM ends one creature's attach on another (a stirge's Proboscis, a darkmantle's Crush) by the table's ruling: an ally pulls the creature off, a spell forces it, or the attacher detaches itself spending 5 feet of its movement. The published detach ACTIONS a creature takes (Stirge: the target or a creature within 5 feet of it, no check; Darkmantle: the attached creature's own DC 13 Strength (Athletics) check) are POST /api/attach/detach. The recurring damage and the attack lockout the attach gated end with it.
// @Tags GM
// @Router /gm/detach [post]
func handleGMDetach2024(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	var req struct {
		AttacherID int `json:"attacher_id"` // the attached creature
		TargetID   int `json:"target_id"`
	}
	_, lobby, ok := gmRead(w, r, &req, optionalBody())
	if !ok {
		return
	}
	if req.AttacherID == 0 || req.TargetID == 0 {
		writeJSONStatus(w, 400, map[string]interface{}{"error": "attacher_id and target_id required"})
		return
	}
	tx, err := beginAttachWrite(lobby)
	if err != nil {
		log.Printf("detach: couldn't begin the write of %d on %d in campaign %d: %v", req.AttacherID, req.TargetID, lobby, err)
		writeJSONStatus(w, 500, map[string]interface{}{"error": "db", "message": "The attach could not be ended."})
		return
	}
	var rowText string
	err = tx.QueryRow(`DELETE FROM combat_effects WHERE lobby_id=$1 AND kind='attached' AND source_id=$2 AND target_id=$3 RETURNING text`, lobby, req.AttacherID, req.TargetID).Scan(&rowText)
	if err != nil {
		tx.Rollback()
		if err == sql.ErrNoRows {
			writeJSONStatus(w, 404, map[string]interface{}{"error": "no_such_attach", "message": "That creature is not attached to the target."})
			return
		}
		log.Printf("detach: couldn't end the attach of %d on %d in campaign %d: %v", req.AttacherID, req.TargetID, lobby, err)
		writeJSONStatus(w, 500, map[string]interface{}{"error": "db", "message": "The attach could not be ended."})
		return
	}
	if err := pendAttachEnding(tx, lobby, req.AttacherID, req.TargetID, rowText); err != nil {
		tx.Rollback()
		log.Printf("detach: couldn't record the pending ending of %d on %d in campaign %d: %v", req.AttacherID, req.TargetID, lobby, err)
		writeJSONStatus(w, 500, map[string]interface{}{"error": "db", "message": "The attach could not be ended."})
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("detach: couldn't commit the end of %d on %d in campaign %d: %v", req.AttacherID, req.TargetID, lobby, err)
		writeJSONStatus(w, 500, map[string]interface{}{"error": "db", "message": "The attach could not be ended."})
		return
	}
	result := fmt.Sprintf("%s is no longer attached to %s.", creatureName(lobby, req.AttacherID), creatureName(lobby, req.TargetID)) +
		drainAttachEnding(lobby, req.AttacherID, req.TargetID, rowText)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "result": result})
}

// @Summary The attached monster detaches itself (costed)
// @Description The attached monster's own voluntary detach ("On its turn, the darkmantle can detach itself by using 5 feet of movement"), with {"attacher_id", "target_id"}. It must be the attacher's turn (409 not_its_turn otherwise, nothing written) and the 5 feet land on its movement for the turn; the Speed-0-while-attached rule does not block it, the text grants exactly this 5 feet. A missing attach is 404 no_such_attach. The GM's ruling route (/api/gm/detach) carries no cost. The recurring damage, the attach-owned conditions and the suffocating the attach started end with it.
// @Tags GM
// @Router /gm/detach/voluntary [post]
// handleGMVoluntaryDetach2024: the attached monster's OWN voluntary detach ("On its turn, the darkmantle
// can detach itself by using 5 feet of movement"). A costed action, separate from the GM's ruling route
// (/api/gm/detach, which carries no cost): it must be the attacher's turn and the 5 feet land on its
// movement for the turn. The Speed-0-while-attached rule does not block it: the text grants exactly this
// 5 feet. Validated before anything is written; a refused attempt changes nothing.
func handleGMVoluntaryDetach2024(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	var req struct {
		AttacherID int `json:"attacher_id"` // the attached creature, detaching itself
		TargetID   int `json:"target_id"`   // the creature it is attached to
	}
	_, lobby, ok := gmRead(w, r, &req, optionalBody())
	if !ok {
		return
	}
	if req.AttacherID == 0 || req.TargetID == 0 {
		writeJSONStatus(w, 400, map[string]interface{}{"error": "attacher_id and target_id required"})
		return
	}
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM combat_effects WHERE lobby_id=$1 AND kind='attached' AND source_id=$2 AND target_id=$3)`, lobby, req.AttacherID, req.TargetID).Scan(&exists); err != nil {
		log.Printf("voluntary detach: couldn't check the attach of %d on %d in campaign %d: %v", req.AttacherID, req.TargetID, lobby, err)
		writeJSONStatus(w, 500, map[string]interface{}{"error": "db", "message": "The attach could not be read."})
		return
	}
	if !exists {
		writeJSONStatus(w, 404, map[string]interface{}{"error": "no_such_attach", "message": "That creature is not attached to the target."})
		return
	}
	if tid := currentTurnID(lobby); tid != req.AttacherID {
		writeJSONStatus(w, 409, map[string]interface{}{"error": "not_its_turn", "message": fmt.Sprintf("The voluntary detach happens on the attached creature's own turn; the current turn is %s. The GM's ruling route (/api/gm/detach) carries no cost.", creatureName(lobby, tid))})
		return
	}
	tx, err := beginAttachWrite(lobby)
	if err != nil {
		log.Printf("voluntary detach: couldn't begin the write of %d on %d in campaign %d: %v", req.AttacherID, req.TargetID, lobby, err)
		writeJSONStatus(w, 500, map[string]interface{}{"error": "db", "message": "The attach could not be ended."})
		return
	}
	var rowText string
	if err := tx.QueryRow(`DELETE FROM combat_effects WHERE lobby_id=$1 AND kind='attached' AND source_id=$2 AND target_id=$3 RETURNING text`, lobby, req.AttacherID, req.TargetID).Scan(&rowText); err != nil {
		tx.Rollback()
		log.Printf("voluntary detach: couldn't end the attach of %d on %d in campaign %d: %v", req.AttacherID, req.TargetID, lobby, err)
		writeJSONStatus(w, 500, map[string]interface{}{"error": "db", "message": "The attach could not be ended."})
		return
	}
	if err := pendAttachEnding(tx, lobby, req.AttacherID, req.TargetID, rowText); err != nil {
		tx.Rollback()
		log.Printf("voluntary detach: couldn't record the pending ending of %d on %d in campaign %d: %v", req.AttacherID, req.TargetID, lobby, err)
		writeJSONStatus(w, 500, map[string]interface{}{"error": "db", "message": "The attach could not be ended."})
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("voluntary detach: couldn't commit the end of %d on %d in campaign %d: %v", req.AttacherID, req.TargetID, lobby, err)
		writeJSONStatus(w, 500, map[string]interface{}{"error": "db", "message": "The attach could not be ended."})
		return
	}
	moved := addMonsterMoved(lobby, req.AttacherID, 5) // 5 feet of movement, on its turn
	result := fmt.Sprintf("%s detaches itself from %s, using 5 feet of movement (%d ft this turn).", creatureName(lobby, req.AttacherID), creatureName(lobby, req.TargetID), moved) +
		drainAttachEnding(lobby, req.AttacherID, req.TargetID, rowText)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "result": result})
}

// @Summary Detach an attached creature
// @Description The character takes the published detach action against an attach (Stirge: the target or a creature within 5 feet of it, no check; Darkmantle: the attached creature's own DC 13 Strength (Athletics) check), with {"character_id", "attacher_id", "target_id"}. The attempt costs the character's action, spent before the check, so a failed check spends it and the attach holds. When the actor is not the attached creature, the attach's rule must allow an adjacent creature and the battle map must place them within 5 feet; where the map does not place them the answer is 409 distance_unknown and the GM rules it (/api/gm/detach), exactly like a reach question without a map. An attach whose action text names no detach action is 400 no_detach_action. The recurring damage, the attach-owned conditions and the suffocating the attach started end with it.
// @Tags Characters
// @Router /attach/detach [post]
func handleAttachDetach2024(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	if _, err := getAgentFromAuth(r); err != nil { // signed in before the body is read
		w.Header().Set("Content-Type", "application/json")
		writeAuthError(w, err)
		return
	}
	var req struct {
		CharacterID int `json:"character_id"` // the actor taking the action
		AttacherID  int `json:"attacher_id"`  // the attached creature (a monster's negative id, a companion id, a character)
		TargetID    int `json:"target_id"`    // the creature it is attached to
	}
	if !readRequest(w, r, &req) {
		return
	}
	if _, ok := requireCharacterOwner(w, r, req.CharacterID, "take the detach action"); !ok {
		return
	}
	var lobby, level, str int
	var name string
	if db.QueryRow(`SELECT lobby_id, level, str, name FROM characters WHERE id = $1`,
		req.CharacterID).Scan(&lobby, &level, &str, &name) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	var rowID int
	var text string
	if db.QueryRow(`SELECT id, text FROM combat_effects WHERE lobby_id=$1 AND kind='attached' AND source_id=$2 AND target_id=$3`,
		lobby, req.AttacherID, req.TargetID).Scan(&rowID, &text) != nil {
		writeJSONStatus(w, 404, map[string]interface{}{"error": "no_such_attach", "message": "That creature is not attached to the target."})
		return
	}
	rule := attachDetachFromRow(text)
	if rule == nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_detach_action", "message": "Its stat block gives no detach action: the GM ends it."})
		return
	}
	if req.CharacterID != req.TargetID {
		if !rule.Adjacent {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "self_only", "message": "Its stat block lets only the attached creature try to detach it."})
			return
		}
		dist, known := charDistanceFeet(lobby, req.CharacterID, req.TargetID)
		if !known {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "distance_unknown", "message": "The battle map does not place both creatures: the GM rules whether they are within 5 feet."})
			return
		}
		if dist > 5 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "too_far", "message": fmt.Sprintf("The target is %d ft away: the detach action reaches a creature within 5 feet of it.", dist)})
			return
		}
	}
	var used bool
	db.QueryRow(`SELECT COALESCE(action_used,false) FROM characters WHERE id = $1`, req.CharacterID).Scan(&used)
	if used {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "action_already_used", "message": "This attempt takes your action, and you have used it this turn."})
		return
	}
	attacherName := creatureName(lobby, req.AttacherID)
	resp := map[string]interface{}{"character": name, "attacher": attacherName, "attempt": rule.text()}
	finish := func(success bool, message string) {
		if success {
			// The action spend and the attach-row end are one serialized transaction: a deadlock or
			// failure rolls BOTH back, so an aborted attempt never spends the action while the attach
			// holds - the player can simply try again.
			tx, err := beginAttachWrite(lobby)
			if err == nil {
				if _, err = tx.Exec(`UPDATE characters SET action_used = true WHERE id = $1`, req.CharacterID); err == nil {
					_, err = tx.Exec(`DELETE FROM combat_effects WHERE id=$1 AND lobby_id=$2`, rowID, lobby)
				}
				if err == nil {
					err = pendAttachEnding(tx, lobby, req.AttacherID, req.TargetID, text)
				}
				if err == nil {
					err = tx.Commit()
				}
			}
			if err != nil {
				tx.Rollback()
				log.Printf("detach action: couldn't finish the detach of %d on %d in campaign %d: %v", req.AttacherID, req.TargetID, lobby, err)
				writeJSONStatus(w, 500, map[string]interface{}{"error": "db", "message": "The detach could not be finished; nothing was spent. Try again."})
				return
			}
			message += drainAttachEnding(lobby, req.AttacherID, req.TargetID, text)
		} else {
			db.Exec(`UPDATE characters SET action_used = true WHERE id = $1`, req.CharacterID) // a failed attempt costs the action
		}
		resp["success"] = success
		resp["message"] = message
		writeAction(db, lobby, req.CharacterID, "detach_attempt", name+" tries to detach "+attacherName, message)
		writeJSONStatus(w, 200, resp)
	}
	if rule.DC == 0 {
		finish(true, fmt.Sprintf("%s detaches %s.", name, attacherName))
		return
	}
	mod := game.Modifier(str)
	if rule.Skill != "" {
		if proficient, expert := characterSkillProficiency(req.CharacterID, rule.Skill); proficient { // the one proficiency reader
			mod += game.ProficiencyBonus(level)
			if expert {
				mod += game.ProficiencyBonus(level)
			}
		}
	}
	// What gives the check Advantage or Disadvantage has one owner (check_2024.go), as for the condition escape
	why := checkCtx{lobby: lobby, who: req.CharacterID, ability: rule.Ability, skill: rule.Skill}.sources()
	mod -= why.Penalty // Exhaustion and a die subtracted from the check (check_2024.go)
	disadv, adv := why.Disadvantage(), why.Advantage()
	roll, r1, r2, _ := rollCheckD20(adv, disadv)
	switch {
	case disadv && !adv:
		resp["rolls"] = []int{r1, r2}
		resp["disadvantage"] = true
	case adv && !disadv:
		resp["rolls"] = []int{r1, r2}
		resp["advantage"] = strings.Join(why.Adv, ", ")
	}
	total := roll + mod
	resp["roll"], resp["modifier"], resp["total"], resp["dc"] = roll, mod, total, rule.DC
	if total >= rule.DC {
		finish(true, fmt.Sprintf("%s total %d: success. %s is detached.", name, total, attacherName))
	} else {
		finish(false, fmt.Sprintf("%s total %d: failure. %s stays attached.", name, total, attacherName))
	}
}
