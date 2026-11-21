package main

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// Recurring turn-start damage a stat block's hit sets up: "Until the grapple ends, the target takes 4 (1d8)
// Bludgeoning damage at the start of each of its turns" (Vine Blight Constricting Vine) and "While attached,
// ... the target takes 5 (2d4) Necrotic damage at the start of each of the stirge's turns" (Stirge Proboscis).
// The row lives in combat_effects (kind "recurring_damage", target_id the hurt creature, source_id the
// attacker) and ticks in the turn pipeline (runTurnEffects stage 7, condition_turns.go), which checkpoints
// each stage, so a re-read or retry never deals the damage twice. The tick checks the gate that keeps the
// damage running - the attacker's grapple on the target, or its attach - and deletes the row when the gate
// is gone (escape, expiry, death), so cleanup needs no extra paths.
//
// Anchors: "at the start of each of its turns" hurts on the TARGET's turns; "at the start of each of the
// <attacker>'s turns" on the SOURCE's. Gates: a sentence led by "Until the grapple ends," is grapple-gated
// (enforced: condition_records, the same live state the attack lockout reads); one led by "While attached,"
// is attach-gated (combat_effects kind "attached", the attach owner's convention). A recurring clause whose
// gate the server does not keep (attach, until that owner lands) is not written and stays in the GM residual.
var recurringDamageRe = regexp.MustCompile(`(?i)the target takes (\d+) \(([^)]+)\) (\w+) damage at the start of each of (its|the [\w() '’-]+?(?:'|’)s) turns`)

// recurringDamageSpec is one parsed recurring-damage clause.
type recurringDamageSpec struct {
	Average int
	Dice    string
	Type    string
	Source  bool // true: ticks at the start of the SOURCE's turns; false: the target's
	Attach  bool // true: gated on the attacker staying attached; false: on its grapple lasting
}

// readRecurringDamage reads the recurring-damage clause of a hit-rider sentence, if it declares one with a
// gate the sentence itself names ("Until the grapple ends, ..." / "While attached, ...").
func readRecurringDamage(sentence string) *recurringDamageSpec {
	m := recurringDamageRe.FindStringSubmatch(sentence)
	if m == nil {
		return nil
	}
	spec := &recurringDamageSpec{
		Dice:   strings.ReplaceAll(m[2], " ", ""),
		Type:   strings.ToLower(m[3]),
		Source: !strings.EqualFold(m[4], "its"),
	}
	fmt.Sscanf(m[1], "%d", &spec.Average)
	ls := strings.ToLower(sentence)
	switch {
	case strings.HasPrefix(strings.TrimSpace(ls), "until the grapple ends"):
		spec.Attach = false
	case strings.HasPrefix(strings.TrimSpace(ls), "while attached"):
		spec.Attach = true
	default:
		return nil // no gate the server can keep: the whole sentence stays the GM's
	}
	return spec
}

// recurringDamageText is the canonical stored text: human readable and parseable by recurringFromText.
func recurringDamageText(spec recurringDamageSpec) string {
	when := "its"
	if spec.Source {
		when = "the attacker's"
	}
	gate := "grapple"
	if spec.Attach {
		gate = "attach"
	}
	return fmt.Sprintf("%d (%s) %s damage at the start of each of %s turns, while the %s lasts", spec.Average, spec.Dice, spec.Type, when, gate)
}

var recurringFromTextRe = regexp.MustCompile(`^(\d+) \(([^)]+)\) (\w+) damage at the start of each of (its|the attacker's) turns, while the (grapple|attach) lasts$`)

func recurringFromText(text string) *recurringDamageSpec {
	m := recurringFromTextRe.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	spec := &recurringDamageSpec{
		Dice:   m[2],
		Type:   strings.ToLower(m[3]),
		Source: m[4] != "its",
		Attach: m[5] == "attach",
	}
	fmt.Sscanf(m[1], "%d", &spec.Average)
	return spec
}

// recurringDamageLive: the gate that keeps a recurring row running still holds: the grapple (or attach) the
// row rides on exists AND its source is still up - a dead creature's constrict or drain stops (the server's
// grapple records outlive a dead grappler: releaseAllGrapplesFrom runs on Incapacitated, not death).
func recurringDamageLive(lobbyID, rowID, targetID, sourceID int, spec recurringDamageSpec) bool {
	if sourceID < 0 && !monsterAlive(lobbyID, sourceID) {
		return false
	}
	var n int
	if spec.Attach {
		if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND source_id=$2 AND target_id=$3 AND kind='attached'`, lobbyID, sourceID, targetID).Scan(&n); err != nil {
			return true // unreadable: keep the row (fail loud elsewhere), never silently drop an effect
		}
		return n > 0
	}
	if err := db.QueryRow(`SELECT count(*) FROM condition_records WHERE lobby_id=$1 AND source_id=$2 AND condition='grappled' AND target_id=$3`, lobbyID, sourceID, targetID).Scan(&n); err != nil {
		return true
	}
	return n > 0
}

// addRecurringDamage writes the recurring row a hit sets up, when its gate holds after the hit (the grapple
// the hit just gave, or the attach). It answers the public line part. When the clause was recognized but its
// gate does not hold - the hit's grapple was not applied (a bigger target, an Immunity, a companion whose
// size the server does not know) - the whole sentence stays the GM's: it answers it as gmNote, because the
// residual was stripped at parse time (readRider) before the outcome was known.
func addRecurringDamage(lobbyID, attackerID, targetID int, desc, attackerName string) (applied, gmNote string) {
	hit := desc
	if i := strings.Index(desc, "Hit:"); i >= 0 {
		hit = desc[i+len("Hit:"):]
	}
	for _, s := range strings.Split(hit, ". ") {
		spec := readRecurringDamage(s)
		if spec == nil {
			continue
		}
		if !recurringDamageLive(lobbyID, 0, targetID, attackerID, *spec) {
			return "", strings.Trim(strings.TrimSpace(s), ".") // no gate holds (the grapple was not applied, the attach
			// row is missing): no recurring damage, the whole sentence is the GM's to apply
		}
		db.Exec(`DELETE FROM combat_effects WHERE lobby_id=$1 AND target_id=$2 AND source_id=$3 AND kind='recurring_damage'`, lobbyID, targetID, attackerID)
		if _, err := db.Exec(`INSERT INTO combat_effects (lobby_id, target_id, source_id, kind, text) VALUES ($1,$2,$3,'recurring_damage',$4)`, lobbyID, targetID, attackerID, recurringDamageText(*spec)); err != nil {
			log.Printf("recurring damage: couldn't record the row for target %d in campaign %d: %v", targetID, lobbyID, err)
			return "the recurring damage could not be recorded", ""
		}
		when := "its"
		if spec.Source {
			when = "the " + attackerName + "'s"
		}
		gate := "grapple"
		if spec.Attach {
			gate = "attach"
		}
		return fmt.Sprintf("%d (%s) %s damage at the start of each of %s turns while the %s lasts", spec.Average, spec.Dice, spec.Type, when, gate), ""
	}
	return "", ""
}

// recurringDamageTail: the rows still to tick when a pause interrupted the stage; the resume handler runs
// exactly them (each row's own damage committed or rolled back with the pause's transaction, never both).
type recurringDamageTail struct {
	Lobby int   `json:"lobby"`
	IDs   []int `json:"ids"`
}

func init() {
	combatResumeHandlers["recurring-damage-tail"] = func(lobby int, raw json.RawMessage) (map[string]interface{}, error) {
		var tail recurringDamageTail
		if err := json.Unmarshal(raw, &tail); err != nil {
			return nil, err
		}
		lines := recurringDamageRows(lobby, tail.IDs)
		for _, line := range lines {
			recordGMAction(db, lobby, "condition", line, line)
		}
		return map[string]interface{}{"turn_effects": lines}, nil
	}
}

// recurringApplyOne deals one row's damage through the target's own Hit Point owner (defenses included).
func recurringApplyOne(lobbyID, targetID int, spec recurringDamageSpec) string {
	dmg := game.RollDamage(spec.Dice, false)
	switch {
	case targetID < 0:
		text, _ := damageMonster(lobbyID, targetID, dmg, spec.Type)
		return text
	case isCompanionCombatID(targetID):
		if c, active := activeCompanionInCampaign(lobbyID, targetID); active {
			return damageCompanion(c, dmg, spec.Type)
		}
		return ""
	default:
		return damageCharacter(targetID, dmg, spec.Type)
	}
}

// recurringDamageRows ticks the given rows: gate gone (escape, expiry, the source down) ends the row with a
// line; gate live deals the damage. Row ids that no longer exist are skipped.
func recurringDamageRows(lobbyID int, ids []int) []string {
	var log []string
	for _, rowID := range ids {
		var target, source int
		var text string
		if err := db.QueryRow(`SELECT target_id, source_id, text FROM combat_effects WHERE id=$1 AND lobby_id=$2 AND kind='recurring_damage'`, rowID, lobbyID).Scan(&target, &source, &text); err != nil {
			continue // already ended
		}
		spec := recurringFromText(text)
		if spec == nil {
			continue
		}
		if !recurringDamageLive(lobbyID, rowID, target, source, *spec) {
			db.Exec(`DELETE FROM combat_effects WHERE id=$1`, rowID)
			log = append(log, fmt.Sprintf("The recurring damage on %s ends: the %s is over.", creatureName(lobbyID, target), map[bool]string{true: "attach", false: "grapple"}[spec.Attach]))
			continue
		}
		if applied := recurringApplyOne(lobbyID, target, *spec); applied != "" {
			log = append(log, fmt.Sprintf("%s %s (%s's recurring damage).", creatureName(lobbyID, target), applied, creatureName(lobbyID, source)))
		}
	}
	return log
}

// recurringDamageTurnStart deals the recurring damage that ticks at the start of creature id's turn and ends
// every row whose gate is gone. Stage 7 of runTurnEffects: each row's damage is armed with a tail frame of
// the rows after it, so a pause mid-stage (a Concentration review the damage called for) commits the damage
// and the exact remaining rows together - a resume deals them once, a crashed request rolls the whole turn
// change back and its retry starts the stage fresh. Nothing double-deals and nothing is silently lost.
func recurringDamageTurnStart(lobbyID, id int, name string) []string {
	anchors := []int{id}
	if characterTurn(id) {
		// A companion without its own turn entry takes its turn with its owner (steeds and summons share
		// the owner's Initiative; familiars roll their own - companions_2024.go): its turn-start rows tick
		// with the owner's turn. A companion with its own entry in the order ticks at its own turn.
		if f, err := readFight(db, lobbyID); err == nil {
			for _, c := range campaignCompanions(lobbyID) {
				if c.OwnerID != id || c.Status != "active" {
					continue
				}
				if i, _ := f.entry(c.CombatID); i >= 0 {
					continue
				}
				anchors = append(anchors, c.CombatID)
			}
		}
	}
	type row struct{ id, target, source, anchor int }
	var due []int
	var pending []row
	for _, anchor := range anchors {
		rows, err := db.Query(`SELECT id, target_id, source_id FROM combat_effects WHERE lobby_id=$1 AND kind='recurring_damage' AND (target_id=$2 OR source_id=$3) ORDER BY id`, lobbyID, anchor, anchor)
		if err != nil {
			log.Printf("recurring damage: couldn't read the rows for %s (id %d) in campaign %d: %v", name, id, lobbyID, err)
			return []string{fmt.Sprintf("recurring damage at the start of %s's turn could not be read", name)}
		}
		for rows.Next() {
			var r row
			if rows.Scan(&r.id, &r.target, &r.source) == nil {
				r.anchor = anchor
				pending = append(pending, r)
			}
		}
		rows.Close()
	}
	for _, r := range pending {
		var text string
		if db.QueryRow(`SELECT text FROM combat_effects WHERE id=$1`, r.id).Scan(&text) != nil {
			continue
		}
		spec := recurringFromText(text)
		if spec == nil {
			continue
		}
		if (spec.Source && r.source == r.anchor) || (!spec.Source && r.target == r.anchor) {
			due = append(due, r.id)
		}
	}
	var log []string
	for i, rowID := range due {
		restore, err := armCombatFrame(lobbyID, "recurring-damage-tail", recurringDamageTail{Lobby: lobbyID, IDs: due[i+1:]})
		if err != nil {
			return append(log, "Recurring damage could not save its continuation; the rows after this one did not run.")
		}
		lines := recurringDamageRows(lobbyID, []int{rowID})
		restore()
		log = append(log, lines...)
		if combatResolutionPaused(lobbyID) {
			return log // the pause captured the armed tail: the rows after this one resume with it
		}
	}
	return log
}

// recurringLockoutTailRe: the lockout clause that shares a recurring-damage sentence (", and the blight
// can't make Constricting Vine attacks"): enforced at the attack entry, so stripped with the recurring
// clause. grappleGatePrefixRe: the gate lead-in once both clauses are gone.
var recurringLockoutTailRe = regexp.MustCompile(`(?i),?\s*and the [\w() '’-]+can(?:'|’)t make [A-Za-z][A-Za-z ]*? attacks?\b`)
var grappleGatePrefixRe = regexp.MustCompile(`(?i)^\s*Until the grapple ends,\s*`)

// attachGatePrefixRe: the attach gate lead-in. attachLockoutLeadRe: the lockout clause when it LEADS the
// sentence ("While attached, the stirge can't make Proboscis attacks, and the target takes ..."): once the
// gate prefix and the recurring clause are stripped, it is what remains before the comma.
var attachGatePrefixRe = regexp.MustCompile(`(?i)^\s*While attached,\s*`)
var attachLockoutLeadRe = regexp.MustCompile(`(?i)^\s*the [\w() '’-]+can(?:'|’)t make [A-Za-z][A-Za-z ]*? attacks?\b`)
