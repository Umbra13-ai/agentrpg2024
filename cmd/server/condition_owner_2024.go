package main

// One owner for a creature's conditions (audit AUD-08, AUD-34, AUD-38; Nov 15 2026).
//
// The store is condition_records: one row per entry of a creature's condition list, for characters and monsters alike. A rules
// condition (Prone, Grappled, Stunned ...) carries its source, duration, repeated save and ways to escape; the markers the server
// keeps beside the conditions (raging, dodging, reckless, hidden, sanctuary:DC, sacred_weapon:BONUS:ROUNDS ...) are rows too.
// condition is the name before the first colon, detail what follows ("charmed:12" is charmed with detail 12). A character's rows
// follow the character (they are read by its id, and carry its current campaign); a monster's rows are its campaign's (the
// campaign and the monster's negative id).
//
// characters.conditions is not a store. It is the character's rows as a list, in the order they were added, plus "exhaustion:N"
// from exhaustion_level, and only this file writes it: in the same transaction as every change of the rows, so the sheet, my-turn,
// the battle map, the attack and save rules and every other reader of the column see exactly the records. Before Nov 15 2026 the
// list and the records were two stores, kept in step by 42 unlocked sync passes after 23 whole-list writes in 11 files and repaired
// at every start; concurrent changes lost conditions in both (six parallel GM conditions kept three).
//
// Every change runs in changeConditions: one transaction that holds the character's row (SELECT ... FOR UPDATE) or, for a monster,
// a transaction lock on (campaign, id), and writes one row at a time. addCondition is the one way a condition is applied: Immunity
// (conditionImmunities: the stat block, spells and items, auras, species and class features), no second copy of a condition from
// the same source (SRD 5.2.1: each instance of a condition has its own duration, but its effects don't get worse), and what the
// condition sets off (conditionTakesHold: an Incapacitating condition ends Concentration, Wild Shape and the grapples the creature
// holds; Unconscious brings Prone and drops what the creature holds). TestNoOtherConditionWriter fails on any other statement that
// writes condition_records or characters.conditions.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

var ruleConditions = map[string]bool{
	"blinded": true, "charmed": true, "deafened": true, "frightened": true, "grappled": true, "incapacitated": true, "invisible": true,
	"paralyzed": true, "petrified": true, "poisoned": true, "prone": true, "restrained": true, "stunned": true, "unconscious": true,
}

// conditionBase is the condition without its parameter ("charmed:12" -> "charmed").
func conditionBase(c string) string {
	base, _ := splitConditionEntry(c)
	return base
}

func isRuleCondition(c string) bool { return ruleConditions[conditionBase(c)] }

// splitConditionEntry reads a list entry: the condition (lower case, before the first colon) and its detail ("sanctuary:14" ->
// "sanctuary", "14"). Stray brackets and quotes from old list writes are dropped.
func splitConditionEntry(s string) (cond, detail string) {
	s = strings.TrimSpace(strings.Trim(strings.TrimSpace(s), "[]\""))
	if i := strings.Index(s, ":"); i >= 0 {
		return strings.ToLower(strings.TrimSpace(s[:i])), strings.TrimSpace(s[i+1:])
	}
	return strings.ToLower(s), ""
}

// entry is the record as an entry of the character's list ("charmed:12", "prone").
func (r conditionRecord) entry() string {
	if r.Detail == "" {
		return r.Condition
	}
	return r.Condition + ":" + r.Detail
}

// bare: the record says only that the condition is there (no source, duration, save, escape or note).
func (r conditionRecord) bare() bool {
	return r.Source == "" && r.DurationRounds == 0 && r.EndSave == nil && len(r.Escapes) == 0 && r.Notes == "" && !r.lasting()
}

// conditionList is a character's conditions list made from its records: each entry once, in the order added, then its
// Exhaustion level.
func conditionList(recs []conditionRecord, exhaustion int) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, r := range recs {
		e := r.entry()
		if k := strings.ToLower(e); e != "" && !seen[k] {
			seen[k] = true
			out = append(out, e)
		}
	}
	if exhaustion > 0 {
		out = append(out, fmt.Sprintf("exhaustion:%d", min(exhaustion, 6)))
	}
	return out
}

// ---------------------------------------------------------------- the store

type rowsQuerier interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
}

// readConditionRecords reads a creature's rows: a character's by its id, a monster's by its campaign and id.
func readConditionRecords(q rowsQuerier, lobbyID, targetID int) ([]conditionRecord, error) {
	query := `SELECT id, lobby_id, target_id, condition, detail, source, escapes, duration_rounds, COALESCE(end_save, 'null'::jsonb), notes,
		COALESCE(created_by, 0), COALESCE(turn_damage, 'null'::jsonb), no_healing FROM condition_records WHERE target_id = $1 ORDER BY id`
	args := []interface{}{targetID}
	if targetID < 0 {
		query = `SELECT id, lobby_id, target_id, condition, detail, source, escapes, duration_rounds, COALESCE(end_save, 'null'::jsonb), notes,
		COALESCE(created_by, 0), COALESCE(turn_damage, 'null'::jsonb), no_healing FROM condition_records WHERE target_id = $1 AND lobby_id = $2 ORDER BY id`
		args = append(args, lobbyID)
	}
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []conditionRecord
	for rows.Next() {
		var r conditionRecord
		var esc, es, td []byte
		if err := rows.Scan(&r.ID, &r.LobbyID, &r.TargetID, &r.Condition, &r.Detail, &r.Source, &esc, &r.DurationRounds, &es, &r.Notes, &r.CreatedBy,
			&td, &r.NoHealing); err != nil {
			return nil, err
		}
		json.Unmarshal(esc, &r.Escapes)
		json.Unmarshal(es, &r.EndSave)
		json.Unmarshal(td, &r.TurnDamage)
		out = append(out, r)
	}
	return out, rows.Err()
}

// conditionChange is one change to one creature's conditions, inside its transaction: recs are the creature's rows as locked.
type conditionChange struct {
	tx         *sql.Tx
	lobby      int // a character's current campaign; a monster's campaign
	target     int
	exhaustion int
	recs       []conditionRecord
	changed    bool
}

var errNoConditionTarget = errors.New("conditions: no creature named")

// changeConditions runs one change to a creature's conditions in its own transaction.
func changeConditions(lobbyID, targetID int, change func(c *conditionChange) error) error {
	if db == nil || targetID == 0 {
		return errNoConditionTarget
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := changeConditionsIn(tx, lobbyID, targetID, change); err != nil {
		return err
	}
	return tx.Commit()
}

// changeConditionsIn runs the change inside a caller's transaction (the battle map's move, the admin page's edit): the character's
// row is locked, the rows read and written through tx, and the character's list rewritten before the caller commits.
func changeConditionsIn(tx *sql.Tx, lobbyID, targetID int, change func(c *conditionChange) error) error {
	c := &conditionChange{tx: tx, lobby: lobbyID, target: targetID}
	switch {
	case targetID > 0:
		if err := tx.QueryRow(`SELECT COALESCE(lobby_id, 0), COALESCE(exhaustion_level, 0) FROM characters WHERE id = $1 FOR UPDATE`, targetID).
			Scan(&c.lobby, &c.exhaustion); err != nil {
			return err
		}
	case targetID < 0:
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1, $2)`, lobbyID, targetID); err != nil {
			return err
		}
	default:
		return errNoConditionTarget
	}
	recs, err := readConditionRecords(tx, c.lobby, targetID)
	if err != nil {
		return err
	}
	c.recs = recs
	if err := change(c); err != nil {
		return err
	}
	if targetID < 0 || !c.changed {
		return nil
	}
	for _, r := range c.recs {
		if r.LobbyID != c.lobby { // the character changed campaigns: its conditions go with it
			if _, err := tx.Exec(`UPDATE condition_records SET lobby_id = $1 WHERE target_id = $2 AND lobby_id <> $1`, c.lobby, targetID); err != nil {
				return err
			}
			break
		}
	}
	list, _ := json.Marshal(conditionList(c.recs, c.exhaustion))
	_, err = tx.Exec(`UPDATE characters SET conditions = $1 WHERE id = $2`, string(list), targetID)
	return err
}

// insert writes one row.
func (c *conditionChange) insert(r conditionRecord) (int, error) {
	r.Condition, r.Detail = strings.ToLower(strings.TrimSpace(r.Condition)), strings.TrimSpace(r.Detail)
	if r.Escapes == nil {
		r.Escapes = []conditionEscape{}
	}
	esc, _ := json.Marshal(r.Escapes)
	var createdBy interface{}
	if r.CreatedBy != 0 {
		createdBy = r.CreatedBy
	}
	var turnDamage interface{}
	if len(r.TurnDamage) > 0 {
		b, _ := json.Marshal(r.TurnDamage)
		turnDamage = string(b)
	}
	err := c.tx.QueryRow(`INSERT INTO condition_records (lobby_id, target_id, condition, detail, source, escapes, duration_rounds, end_save, notes, created_by,
		turn_damage, no_healing) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`, c.lobby, c.target, r.Condition, r.Detail, r.Source,
		string(esc), r.DurationRounds, endSaveJSON(r.EndSave), r.Notes, createdBy, turnDamage, r.NoHealing).Scan(&r.ID)
	if err != nil {
		return 0, err
	}
	r.LobbyID, r.TargetID = c.lobby, c.target
	c.recs = append(c.recs, r)
	c.changed = true
	return r.ID, nil
}

// remove deletes the rows that match and returns them.
func (c *conditionChange) remove(match func(conditionRecord) bool) ([]conditionRecord, error) {
	var gone, kept []conditionRecord
	for _, r := range c.recs {
		if !match(r) {
			kept = append(kept, r)
			continue
		}
		if _, err := c.tx.Exec(`DELETE FROM condition_records WHERE id = $1`, r.ID); err != nil {
			return nil, err
		}
		gone = append(gone, r)
	}
	if len(gone) > 0 {
		c.recs, c.changed = kept, true
	}
	return gone, nil
}

// set changes one row's remaining rounds and detail (a countdown).
func (c *conditionChange) set(id, rounds int, detail string) error {
	for i, r := range c.recs {
		if r.ID != id {
			continue
		}
		if _, err := c.tx.Exec(`UPDATE condition_records SET duration_rounds = $1, detail = $2 WHERE id = $3`, rounds, detail, id); err != nil {
			return err
		}
		c.recs[i].DurationRounds, c.recs[i].Detail = rounds, detail
		c.changed = true
	}
	return nil
}

// setExhaustion sets a character's Exhaustion level (0-6) with its list.
func (c *conditionChange) setExhaustion(level int) error {
	level = max(0, min(6, level))
	if c.target <= 0 {
		return nil
	}
	if _, err := c.tx.Exec(`UPDATE characters SET exhaustion_level = $1 WHERE id = $2`, level, c.target); err != nil {
		return err
	}
	c.exhaustion, c.changed = level, true
	return nil
}

// logConditionFailure: a change the caller has no reply for. The request's database guard (db_guard_2024.go) answers it.
func logConditionFailure(what string, targetID int, err error) {
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, errNoConditionTarget) {
		log.Printf("conditions: %s on %d: %v", what, targetID, err)
	}
}

// ---------------------------------------------------------------- applying a condition

// condAdd is a condition to apply.
type condAdd struct {
	Condition    string // "prone", "charmed:12", or a marker ("raging", "sanctuary:14")
	Source       string
	Rounds       int
	EndSave      *conditionSave
	Escapes      []conditionEscape
	Notes        string // "concentration" (it lasts while CreatedBy concentrates), "until_start_of_next_turn"
	CreatedBy    int
	SourceID     int  // the creature that imposes it (Protection from Evil and Good)
	Magical      bool // from a spell or a magical effect (Freedom of Movement, the Ring of Free Action)
	MagicalSleep bool // magic putting the creature to sleep (Fey Ancestry)
	Replace      bool // the GM's word: the creature's other records of this condition go
	Force        bool // the GM's word: set even on a creature immune to it
	// what lasts with the condition (a monster's effect: monster_effects_2024.go)
	TurnDamage []conditionTurnDamage // damage at the start or the end of each of its turns
	NoHealing  bool                  // it can't regain Hit Points
}

func (a condAdd) bare() bool {
	return a.Source == "" && a.Rounds == 0 && a.EndSave == nil && len(a.Escapes) == 0 && a.Notes == "" && len(a.TurnDamage) == 0 && !a.NoHealing
}

// condResult is what applying a condition did.
type condResult struct {
	ID      int
	Added   bool
	Already bool                   // the creature has it from that source already
	Immune  string                 // what made the creature immune ("" when it isn't)
	Notes   map[string]interface{} // what the condition set off (concentration_broken, wild_shape_ended, auto_prone ...)
	Err     error
}

// conditionImmunity is one source of Immunity to conditions; it answers what makes the creature immune, or "".
type conditionImmunity func(lobbyID, targetID int, cond string, a condAdd) string

// conditionImmunities: every source of condition Immunity the server knows (SRD 5.2.1: Immunity to a condition means the condition
// isn't applied). One table, asked by addCondition for every path that applies a condition. Before Nov 15 2026 the GM's tool asked
// six of these, the condition owner three, and Topple and Grapple none (audit AUD-38). (Filled in init: its rows reach back to
// addCondition through the spell effects they read.)
var conditionImmunities []conditionImmunity

func init() { conditionImmunities = conditionImmunityRows() }

func conditionImmunityRows() []conditionImmunity {
	return []conditionImmunity{
		func(lobbyID, targetID int, cond string, _ condAdd) string { // the monster's stat block
			if targetID < 0 && monsterConditionImmune(lobbyID, targetID, cond) {
				return "its stat block"
			}
			return ""
		},
		func(lobbyID, targetID int, cond string, a condAdd) string { // Mind Blank, Heroes' Feast, Gaseous Form, Ring of Free Action ...
			if imm, why := buffConditionImmune(lobbyID, targetID, cond, a.SourceID, a.Magical); imm {
				return why
			}
			return ""
		},
		func(lobbyID, targetID int, cond string, _ condAdd) string { // Heroism
			if cond == "frightened" && hasSpellEffect(lobbyID, targetID, "heroism") {
				return "Heroism"
			}
			return ""
		},
		func(lobbyID, targetID int, cond string, _ condAdd) string { // Aura of Courage, Aura of Devotion
			_, why := auraImmune(lobbyID, targetID, cond)
			return why
		},
		func(_, targetID int, cond string, a condAdd) string { // Fey Ancestry: magic can't put you to sleep
			if targetID > 0 && a.MagicalSleep && (cond == "unconscious" || cond == "incapacitated") && isImmuneToMagicalSleep(targetID) {
				return "Fey Ancestry"
			}
			return ""
		},
		func(_, targetID int, cond string, _ condAdd) string { // Nature's Ward (Circle of the Land, level 10)
			if targetID > 0 && cond == "poisoned" && natureWardImmune(targetID) {
				return "Nature's Ward"
			}
			return ""
		},
		func(_, targetID int, cond string, _ condAdd) string { // Mindless Rage (Path of the Berserker, level 6), while raging
			if targetID > 0 && (cond == "charmed" || cond == "frightened") && mindlessRage(targetID) {
				return "Mindless Rage"
			}
			return ""
		},
	}
}

// mindlessRage: a Berserker Barbarian of level 6 or more who is raging (Immunity to Charmed and Frightened).
func mindlessRage(charID int) bool {
	class, sub, level := subclassOf(charID)
	return class == "barbarian" && strings.EqualFold(sub, "berserker") && level >= 6 && hasCondition(charID, "raging")
}

// immuneToCondition answers what makes the creature immune to a rules condition, or "" (markers have no Immunity).
func immuneToCondition(lobbyID, targetID int, a condAdd) string {
	cond, _ := splitConditionEntry(a.Condition)
	if !ruleConditions[cond] || a.Force {
		return ""
	}
	for _, imm := range conditionImmunities {
		if why := imm(lobbyID, targetID, cond, a); why != "" {
			return why
		}
	}
	return ""
}

// addCondition applies a condition to a character or a monster (negative id): the one way a condition is applied.
func addCondition(lobbyID, targetID int, a condAdd) condResult {
	var res condResult
	cond, detail := splitConditionEntry(a.Condition)
	if cond == "" || targetID == 0 {
		res.Err = errNoConditionTarget
		return res
	}
	if res.Immune = immuneToCondition(lobbyID, targetID, a); res.Immune != "" {
		return res
	}
	rule := ruleConditions[cond]
	res.Err = changeConditions(lobbyID, targetID, func(c *conditionChange) error {
		if a.Replace {
			if _, err := c.remove(func(r conditionRecord) bool { return r.Condition == cond }); err != nil {
				return err
			}
		}
		var bare []int
		for _, r := range c.recs {
			if r.Condition != cond || !strings.EqualFold(r.Detail, detail) {
				continue
			}
			if !rule || r.Source == a.Source || a.bare() { // one copy per source; a marker once
				res.ID, res.Already = r.ID, true
				return nil
			}
			if r.bare() {
				bare = append(bare, r.ID) // what was known only as "the condition is there" gets its source, duration and save
			}
		}
		if _, err := c.remove(func(r conditionRecord) bool { return containsInt(bare, r.ID) }); err != nil {
			return err
		}
		id, err := c.insert(conditionRecord{Condition: cond, Detail: detail, Source: a.Source, Escapes: a.Escapes, DurationRounds: a.Rounds,
			EndSave: a.EndSave, Notes: a.Notes, CreatedBy: a.CreatedBy, TurnDamage: a.TurnDamage, NoHealing: a.NoHealing})
		res.ID, res.Added = id, err == nil
		return err
	})
	logConditionFailure("apply "+cond, targetID, res.Err)
	if res.Added && rule {
		res.Notes = conditionTakesHold(lobbyID, targetID, cond, a.Source)
	}
	return res
}

// conditionTakesHold: what a condition sets off once it is applied (after its transaction).
//   - SRD 5.2.1 Incapacitated: Concentration ends; a druid's Wild Shape ends; a Barbarian's Rage ends; a grapple ends when the grappler is Incapacitated
//     ("Ending a Grapple"). Paralyzed, Petrified, Stunned and Unconscious include it. Dodge's benefit is lost (dodgeActive asks).
//   - Unconscious: the creature has the Prone condition and drops whatever it's holding.
func conditionTakesHold(lobbyID, targetID int, cond, source string) map[string]interface{} {
	notes := map[string]interface{}{}
	if game.IsIncapacitatingCondition(cond) {
		if targetID > 0 {
			if n, err := db.Exec(`UPDATE characters SET concentrating_on = NULL WHERE id = $1 AND COALESCE(concentrating_on, '') <> ''`, targetID); err == nil {
				if k, _ := n.RowsAffected(); k > 0 {
					notes["concentration_broken"] = "Incapacitated: Concentration is broken"
				}
			}
			if note := endWildShapeIfDown(targetID); note != "" {
				notes["wild_shape_ended"] = note
			}
			if removeCondition(targetID, "raging") { // SRD 5.2.1 Rage: it ends early if you have the Incapacitated condition
				notes["rage_ended"] = "Incapacitated: the Rage ends"
			}
		}
		if released := releaseAllGrapplesFrom(lobbyID, targetID); len(released) > 0 {
			notes["grapples_released"] = released
			notes["grapple_note"] = fmt.Sprintf("Grapple(s) ended because %s became incapacitated", creatureName(lobbyID, targetID))
		}
	}
	if cond == "unconscious" {
		if r := addCondition(lobbyID, targetID, condAdd{Condition: "prone", Source: source}); r.Added {
			notes["auto_prone"] = true
			notes["prone_note"] = fmt.Sprintf("%s falls prone (unconscious creatures automatically fall prone)", creatureName(lobbyID, targetID))
		}
		if targetID > 0 {
			if dropped := dropHeldItems(targetID); len(dropped) > 0 {
				notes["dropped_items"] = dropped
				notes["drop_note"] = fmt.Sprintf("%s drops held items (unconscious creatures drop whatever they're holding)", creatureName(lobbyID, targetID))
			}
		}
	}
	if len(notes) == 0 {
		return nil
	}
	return notes
}

// applyCondition puts a condition on a monster or a character, with a duration in rounds (0 = until removed) and an optional
// repeated save at the end of each of its turns.
func applyCondition(lobbyID, id int, cond, source string, rounds int, saveAbility string, dc int) condResult {
	a := condAdd{Condition: cond, Source: source, Rounds: rounds}
	if saveAbility != "" {
		a.EndSave = &conditionSave{Ability: saveAbility, DC: dc}
	}
	return addCondition(lobbyID, id, a)
}

// addCharacterCondition puts a condition or a marker on a character, with no source.
func addCharacterCondition(charID int, cond string) condResult {
	return addCondition(lobbyOfChar(charID), charID, condAdd{Condition: cond})
}

// setMarker gives a character one entry of a marker: "sacred_weapon" with "2:100" replaces any sacred_weapon:... it had.
func setMarker(charID int, marker, detail string) error {
	marker = strings.ToLower(marker)
	err := changeConditions(0, charID, func(c *conditionChange) error {
		if _, err := c.remove(func(r conditionRecord) bool { return r.Condition == marker }); err != nil {
			return err
		}
		_, err := c.insert(conditionRecord{Condition: marker, Detail: detail})
		return err
	})
	logConditionFailure("set "+marker, charID, err)
	return err
}

// countDownMarker takes one round off a marker whose detail ends in its rounds left ("holy_nimbus:10", "sacred_weapon:2:100");
// at 0 the marker ends. It says whether the marker ended.
func countDownMarker(charID int, marker string) bool {
	if charID <= 0 || !hasMarker(charID, marker) {
		return false // nothing to count (most turns): no transaction
	}
	ended := false
	err := changeConditions(0, charID, func(c *conditionChange) error {
		for _, r := range c.recs {
			if r.Condition != marker {
				continue
			}
			head, last := "", r.Detail
			if i := strings.LastIndex(r.Detail, ":"); i >= 0 {
				head, last = r.Detail[:i+1], r.Detail[i+1:]
			}
			n, err := strconv.Atoi(last)
			if err != nil {
				continue
			}
			if n-1 > 0 {
				if err := c.set(r.ID, r.DurationRounds, head+strconv.Itoa(n-1)); err != nil {
					return err
				}
				continue
			}
			id := r.ID
			if _, err := c.remove(func(x conditionRecord) bool { return x.ID == id }); err != nil {
				return err
			}
			ended = true
		}
		return nil
	})
	logConditionFailure("count down "+marker, charID, err)
	return ended
}

// hasMarker: the character's list has an entry of the marker ("sacred_weapon" for "sacred_weapon:2:100").
func hasMarker(charID int, marker string) bool {
	for _, e := range getCharConditions(charID) {
		if conditionBase(e) == marker {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- ending conditions

// removeConditionsWhere ends a creature's records that match, in one transaction, and returns them.
func removeConditionsWhere(lobbyID, targetID int, match func(conditionRecord) bool) []conditionRecord {
	var gone []conditionRecord
	err := changeConditions(lobbyID, targetID, func(c *conditionChange) error {
		var err error
		gone, err = c.remove(match)
		return err
	})
	logConditionFailure("end conditions", targetID, err)
	return gone
}

// endCondition ends a condition on a creature: every record of it ("grappled" ends "grappled:12" too), only those from source when
// source is given. It returns the records ended.
func endCondition(lobbyID, targetID int, cond, source string) []conditionRecord {
	cond = conditionBase(cond)
	return removeConditionsWhere(lobbyID, targetID, func(r conditionRecord) bool {
		return r.Condition == cond && (source == "" || r.Source == source)
	})
}

// removeCondition ends one entry of a character's list ("sanctuary:14", "raging"), or every record of a rules condition named
// without a detail ("grappled" ends "grappled:12" too). Used for standing up from Prone, breaking a grapple, ending a marker ...
func removeCondition(charID int, condition string) bool {
	cond, detail := splitConditionEntry(condition)
	whole := ruleConditions[cond] && !strings.Contains(condition, ":")
	return len(removeConditionsWhere(0, charID, func(r conditionRecord) bool {
		return r.Condition == cond && (whole || strings.EqualFold(r.Detail, detail))
	})) > 0
}

// deleteConditionRecord ends one record by its id.
func deleteConditionRecord(lobbyID, targetID, recordID int) bool {
	return len(removeConditionsWhere(lobbyID, targetID, func(r conditionRecord) bool { return r.ID == recordID })) > 0
}

// endConditionRecords ends records found across creatures (each creature's in its own transaction); the ones gone already are
// skipped. It returns the records ended.
func endConditionRecords(recs []conditionRecord) []conditionRecord {
	type who struct{ lobby, target int }
	ids := map[who][]int{}
	var order []who
	for _, r := range recs {
		k := who{r.LobbyID, r.TargetID}
		if r.TargetID > 0 {
			k.lobby = 0
		}
		if _, ok := ids[k]; !ok {
			order = append(order, k)
		}
		ids[k] = append(ids[k], r.ID)
	}
	var gone []conditionRecord
	for _, k := range order {
		list := ids[k]
		gone = append(gone, removeConditionsWhere(k.lobby, k.target, func(r conditionRecord) bool { return containsInt(list, r.ID) })...)
	}
	return gone
}

// findConditionRecords reads the records across creatures that a filter picks (where is SQL over condition_records' columns).
func findConditionRecords(where string, args ...interface{}) []conditionRecord {
	if db == nil {
		return nil
	}
	rows, err := db.Query(`SELECT id, lobby_id, target_id, condition, detail, source, duration_rounds, notes, COALESCE(created_by, 0)
		FROM condition_records WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []conditionRecord
	for rows.Next() {
		var r conditionRecord
		if rows.Scan(&r.ID, &r.LobbyID, &r.TargetID, &r.Condition, &r.Detail, &r.Source, &r.DurationRounds, &r.Notes, &r.CreatedBy) == nil {
			out = append(out, r)
		}
	}
	return out
}

// inCampaignSQL picks the records of a campaign's creatures ($1 is the campaign): its characters' (wherever their records were
// written) and its monsters'.
const inCampaignSQL = `((target_id > 0 AND target_id IN (SELECT id FROM characters WHERE lobby_id = $1)) OR (target_id < 0 AND lobby_id = $1))`

// endConditionsFromSource ends the conditions one source put on the campaign's creatures (Stunning Strike at the start of the
// Monk's next turn).
func endConditionsFromSource(lobbyID int, source string) []conditionRecord {
	return endConditionRecords(findConditionRecords(inCampaignSQL+` AND source = $2`, lobbyID, source))
}

// clearMonsterConditionRecords drops the records of a campaign's monsters (negative ids) when a fight starts or ends: their ids
// are the fight's. Monsters have no list, so one statement is the whole change.
func clearMonsterConditionRecords(lobbyID int) {
	if db != nil {
		db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id < 0`, lobbyID)
	}
}

// countDownCondition takes one round off a record's duration at the end of the creature's turn; at the last round the condition
// runs out. It says whether it ran out.
func countDownCondition(lobbyID, targetID, recordID int) bool {
	ended := false
	err := changeConditions(lobbyID, targetID, func(c *conditionChange) error {
		for _, r := range c.recs {
			if r.ID != recordID || r.DurationRounds <= 0 {
				continue
			}
			if r.DurationRounds > 1 {
				return c.set(r.ID, r.DurationRounds-1, r.Detail)
			}
			ended = true
			_, err := c.remove(func(x conditionRecord) bool { return x.ID == recordID })
			return err
		}
		return nil
	})
	logConditionFailure("count down", targetID, err)
	return ended
}

// passConditionTime: rounds of time pass for the campaign's conditions (onlyTarget > 0: that character's): durations run out.
func passConditionTime(lobbyID, rounds, onlyTarget int) {
	if rounds <= 0 {
		return
	}
	targets := map[int]bool{}
	for _, r := range findConditionRecords(inCampaignSQL+` AND duration_rounds > 0 AND ($2 = 0 OR target_id = $2)`, lobbyID, onlyTarget) {
		targets[r.TargetID] = true
	}
	for t := range targets {
		err := changeConditions(lobbyID, t, func(c *conditionChange) error {
			if _, err := c.remove(func(r conditionRecord) bool { return r.DurationRounds > 0 && r.DurationRounds <= rounds }); err != nil {
				return err
			}
			for _, r := range c.recs {
				if r.DurationRounds > 0 {
					if err := c.set(r.ID, r.DurationRounds-rounds, r.Detail); err != nil {
						return err
					}
				}
			}
			return nil
		})
		logConditionFailure("pass time", t, err)
	}
}

// dropMarkersKeepConditions removes the markers (dodging, reckless, hidden, raging ...) from a character's conditions and keeps the
// rules' conditions and diseases: starting a fight, or a Long Rest, must not cure Poisoned or stand a Prone character up.
func dropMarkersKeepConditions(charID int) {
	removeConditionsWhere(0, charID, func(r conditionRecord) bool { return !ruleConditions[r.Condition] && r.Condition != "disease" })
}

// longRestConditions: a Long Rest ends the markers and removes one level of Exhaustion (SRD 5.2.1 Long Rest); the rules'
// conditions keep their own durations, which the rest's 8 hours have already passed.
func longRestConditions(charID int) error {
	err := changeConditions(0, charID, func(c *conditionChange) error {
		if _, err := c.remove(func(r conditionRecord) bool { return !ruleConditions[r.Condition] && r.Condition != "disease" }); err != nil {
			return err
		}
		if c.exhaustion > 0 {
			return c.setExhaustion(c.exhaustion - 1)
		}
		return nil
	})
	logConditionFailure("long rest", charID, err)
	return err
}

// ---------------------------------------------------------------- Exhaustion

// setExhaustionLevel sets a character's Exhaustion level (0-6) and returns it.
func setExhaustionLevel(charID, level int) int {
	level = max(0, min(6, level))
	err := changeConditions(0, charID, func(c *conditionChange) error { return c.setExhaustion(level) })
	logConditionFailure("set exhaustion", charID, err)
	return level
}

// addExhaustion changes a character's Exhaustion level by n (2024: a creature dies at level 6).
func addExhaustion(charID, n int) string {
	level := 0
	err := changeConditions(0, charID, func(c *conditionChange) error {
		level = max(0, min(6, c.exhaustion+n))
		return c.setExhaustion(level)
	})
	logConditionFailure("add exhaustion", charID, err)
	if level >= 6 {
		killCharacter(charID) // the Hit Point owner (hit_points_2024.go)
		return "Exhaustion level 6: the creature dies."
	}
	return fmt.Sprintf("Exhaustion level %d.", level)
}

// ---------------------------------------------------------------- the editors

// conditionColumns: the character columns this owner writes. The editors (the admin page, the GM's "set") send them here.
var conditionColumns = map[string]bool{"conditions": true, "exhaustion_level": true}

// setConditionColumnIn writes an editor's value of a condition column inside the editor's transaction. conditions takes the whole
// list: entries that stay keep their records (source, duration, save), new ones get a bare record, the rest end; "exhaustion:N"
// in it sets the Exhaustion level. exhaustion_level takes the level.
func setConditionColumnIn(tx *sql.Tx, charID int, column string, value interface{}) error {
	return changeConditionsIn(tx, 0, charID, func(c *conditionChange) error {
		if column == "exhaustion_level" {
			n, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(value)))
			if value == nil {
				n, err = 0, nil
			}
			if err != nil {
				return fmt.Errorf("exhaustion_level takes a level from 0 to 6")
			}
			return c.setExhaustion(n)
		}
		var list []string
		switch v := value.(type) {
		case nil:
		case string:
			list = parseConditionsString(v)
		case []string:
			list = v
		case []interface{}:
			for _, x := range v {
				list = append(list, fmt.Sprint(x))
			}
		default:
			return fmt.Errorf("conditions takes a list of conditions")
		}
		keep := map[int]bool{}
		var add []conditionRecord
		for _, e := range normalizeConditionList(list) {
			cond, detail := splitConditionEntry(e)
			if cond == "exhaustion" {
				n, _ := strconv.Atoi(detail)
				if err := c.setExhaustion(n); err != nil {
					return err
				}
				continue
			}
			found := false
			for _, r := range c.recs {
				if !keep[r.ID] && r.Condition == cond && strings.EqualFold(r.Detail, detail) {
					keep[r.ID], found = true, true
					break
				}
			}
			if !found {
				add = append(add, conditionRecord{Condition: cond, Detail: detail})
			}
		}
		if _, err := c.remove(func(r conditionRecord) bool { return !keep[r.ID] }); err != nil {
			return err
		}
		for _, r := range add {
			if _, err := c.insert(r); err != nil {
				return err
			}
		}
		c.changed = true
		return nil
	})
}

// setConditionColumn is the same in its own transaction (the GM's "set").
func setConditionColumn(charID int, column string, value interface{}) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := setConditionColumnIn(tx, charID, column, value); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------------------------------------------------------------- at startup

// migrateConditionLists makes condition_records the one store, once: a record whose condition still carries its detail
// ("grappled:12") is split; an entry of a character's list without a record (a marker, or a condition written before Nov 15 2026
// by a whole-list writer) gets one; then the list is rewritten from the records. A character whose list already is its records
// is left alone.
func migrateConditionLists() {
	if db == nil {
		return
	}
	db.Exec(`UPDATE condition_records SET detail = substr(condition, strpos(condition, ':') + 1), condition = lower(split_part(condition, ':', 1))
		WHERE strpos(condition, ':') > 0`)
	rows, err := db.Query(`SELECT id, COALESCE(conditions::text, '[]'), COALESCE(exhaustion_level, 0) FROM characters`)
	if err != nil {
		log.Printf("conditions: migration skipped: %v", err)
		return
	}
	type char struct {
		id, exhaustion int
		list           []string
	}
	var chars []char
	for rows.Next() {
		var c char
		var raw string
		if rows.Scan(&c.id, &raw, &c.exhaustion) == nil {
			c.list = normalizeConditionList(parseConditionsString(raw))
			chars = append(chars, c)
		}
	}
	rows.Close()
	withRecords := map[int]bool{}
	for _, r := range findConditionRecords(`target_id > 0`) {
		withRecords[r.TargetID] = true
	}
	moved := 0
	for _, ch := range chars {
		if len(ch.list) == 0 && !withRecords[ch.id] {
			continue
		}
		err := changeConditions(0, ch.id, func(c *conditionChange) error {
			before, _ := json.Marshal(ch.list)
			for _, e := range ch.list {
				cond, detail := splitConditionEntry(e)
				if cond == "" || cond == "exhaustion" { // the level is exhaustion_level's
					continue
				}
				have := false
				for _, r := range c.recs {
					have = have || (r.Condition == cond && (strings.EqualFold(r.Detail, detail) || (ruleConditions[cond] && detail == "")))
				}
				if !have {
					if _, err := c.insert(conditionRecord{Condition: cond, Detail: detail}); err != nil {
						return err
					}
				}
			}
			after, _ := json.Marshal(conditionList(c.recs, c.exhaustion))
			c.changed = c.changed || string(before) != string(after)
			for _, r := range c.recs {
				c.changed = c.changed || r.LobbyID != c.lobby // records written in an earlier campaign
			}
			if c.changed {
				moved++
			}
			return nil
		})
		logConditionFailure("migrate", ch.id, err)
	}
	if moved > 0 {
		log.Printf("conditions: %d character list(s) now made from their condition records", moved)
	}
}
