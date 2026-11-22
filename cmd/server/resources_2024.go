package main

// The one owner of what a character has expended (audit AUD-24, findings B-4 and E-13): its spell slots (spell_slots_used), its
// Pact Magic slots (pact_slots_used), its class resources and other counted uses (class_resources_used: Rage, Focus Points,
// Sorcery Points, Channel Divinity, once-per-rest features, the rounds left of a timed feature ...) and Indomitable
// (indomitable_used). Each column holds counts of what is used: a slot level or a resource key -> how many are expended (a
// negative count of a slot level is an extra slot, such as one Font of Magic creates, that vanishes on a Long Rest).
//
// Every change goes through changeSpent (or changeSpentTx inside a caller's transaction): the row is locked (SELECT ... FOR
// UPDATE), the change checks what is left on the locked values and changes them, and they are written in the same transaction,
// so two spends at once can't both take the last use and no write overwrites another's. Before Nov 15 2026 about 60 places read
// a whole column, checked and changed it in Go and wrote the whole map back without a lock (18 statements wrote
// class_resources_used, 14 spell_slots_used): a spend that overlapped another was lost, or both took the last use. The maximum a
// change is checked against (classResourceMax, characterSpellSlots ...) reads other rows and tables, so it is worked out before
// the change and handed to it: the transaction itself only reads and writes through tx (CLAUDE.md).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
)

// spent is what a character has expended, read with its row locked.
type spent struct {
	Slots       map[string]int // spell slot level -> expended (negative: extra slots)
	Pact        map[string]int // Pact Magic slot level -> expended
	Uses        map[string]int // class resource or counted use -> expended
	Indomitable int            // uses of Indomitable
}

const spentColumns = `COALESCE(spell_slots_used,'{}'), COALESCE(pact_slots_used,'{}'), COALESCE(class_resources_used,'{}'),
	COALESCE(indomitable_used,0)`

func scanSpent(row *sql.Row) (spent, error) {
	var slots, pact, uses []byte
	var s spent
	if err := row.Scan(&slots, &pact, &uses, &s.Indomitable); err != nil {
		return s, err
	}
	s.Slots, s.Pact, s.Uses = usedMap(slots), usedMap(pact), usedMap(uses)
	return s, nil
}

// usedMap reads one column's counts (a value that isn't a whole number counts as 0).
func usedMap(raw []byte) map[string]int {
	out := map[string]int{}
	var m map[string]interface{}
	json.Unmarshal(raw, &m)
	for k, v := range m {
		if f, ok := v.(float64); ok {
			out[k] = int(f)
		}
	}
	return out
}

func (s spent) copy() spent {
	c := spent{Slots: map[string]int{}, Pact: map[string]int{}, Uses: map[string]int{}, Indomitable: s.Indomitable}
	for k, v := range s.Slots {
		c.Slots[k] = v
	}
	for k, v := range s.Pact {
		c.Pact[k] = v
	}
	for k, v := range s.Uses {
		c.Uses[k] = v
	}
	return c
}

func sameCounts(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// changeSpentTx runs change on the counts with the row locked in tx and writes the columns it changed. A change that returns an
// error writes nothing.
func changeSpentTx(tx *sql.Tx, charID int, change func(s *spent) error) (before, after spent, err error) {
	if before, err = scanSpent(tx.QueryRow(`SELECT `+spentColumns+` FROM characters WHERE id = $1 FOR UPDATE`, charID)); err != nil {
		return before, before, err
	}
	after = before.copy()
	if err = change(&after); err != nil {
		return before, before, err
	}
	write := func(col string, a, b map[string]int) error {
		if sameCounts(a, b) {
			return nil
		}
		raw, _ := json.Marshal(b)
		_, err := tx.Exec(`UPDATE characters SET `+col+` = $1 WHERE id = $2`, string(raw), charID)
		return err
	}
	if err = write("spell_slots_used", before.Slots, after.Slots); err == nil {
		if err = write("pact_slots_used", before.Pact, after.Pact); err == nil {
			err = write("class_resources_used", before.Uses, after.Uses)
		}
	}
	if err == nil && after.Indomitable != before.Indomitable {
		_, err = tx.Exec(`UPDATE characters SET indomitable_used = $1 WHERE id = $2`, after.Indomitable, charID)
	}
	if err != nil {
		return before, before, err
	}
	return before, after, nil
}

// changeSpent is changeSpentTx in a transaction of its own.
func changeSpent(charID int, change func(s *spent) error) (before, after spent, err error) {
	tx, err := db.Begin()
	if err != nil {
		return before, after, err
	}
	defer tx.Rollback()
	if before, after, err = changeSpentTx(tx, charID, change); err != nil {
		return before, before, err
	}
	if err = tx.Commit(); err != nil {
		return before, before, err
	}
	return before, after, nil
}

// spendWithTurn runs change on the locked counts and uses cost's part of the turn (featureTurnCost, actions.go) in the same
// transaction: a feature route's use and its Bonus Action (or action) are spent together or not at all.
func spendWithTurn(charID int, cost turnCost, change func(s *spent) error) (before, after spent, err error) {
	tx, err := db.Begin()
	if err != nil {
		return before, after, err
	}
	defer tx.Rollback()
	if before, after, err = changeSpentTx(tx, charID, change); err != nil {
		return before, before, err
	}
	if err = cost.spendIn(tx, charID); err != nil {
		return before, before, err
	}
	if err = tx.Commit(); err != nil {
		return before, before, err
	}
	return before, after, nil
}

// notEnough is a change refused because too little is left; Left is what was left. Its text is the reply's.
type notEnough struct {
	Msg  string
	Left int
}

func (e *notEnough) Error() string { return e.Msg }

// refusedSpend says whether err is a refusal (not enough left) and returns it.
func refusedSpend(err error) (*notEnough, bool) {
	var r *notEnough
	ok := errors.As(err, &r)
	return r, ok
}

// spendText is the reply when a spend failed: the refusal's text in the refusal form (featureRefusal: "Cannot use <what>: you have no expended level 3
// spell slot."), or a database problem. A refusal made elsewhere in that form is kept as it is.
func spendText(err error, what string) string {
	if r, ok := refusedSpend(err); ok {
		if isRefusalResult(r.Msg) {
			return r.Msg
		}
		if r.Msg == "" {
			return featureRefusal(what, "there is nothing left to spend.")
		}
		return featureRefusal(what, strings.ToLower(r.Msg[:1])+r.Msg[1:])
	}
	log.Printf("spending %s: %v", what, err)
	return notFoundOrDBError(err, "no such character")
}

// ---------------------------------------------------------------- class resources and counted uses

// spendUse expends n of key when at least n of max are left, and returns what is left after.
func spendUse(charID int, key string, n, max int) (left int, err error) {
	_, after, err := changeSpent(charID, func(s *spent) error { return s.spend(key, n, max) })
	return max - after.Uses[key], err
}

// spendUseOK is spendUse for a caller that only needs to know whether it was spent.
func spendUseOK(charID int, key string, n, max int) bool {
	_, err := spendUse(charID, key, n, max)
	return err == nil
}

// spend expends n of key on the locked counts when at least n of max are left.
func (s *spent) spend(key string, n, max int) error {
	if left := max - s.Uses[key]; left < n {
		if left < 0 {
			left = 0
		}
		return &notEnough{fmt.Sprintf("Not enough %s (have %d, need %d)", key, left, n), left}
	}
	s.Uses[key] += n
	return nil
}

// regain gives back up to n expended uses of key (never below none expended).
func (s *spent) regain(key string, n int) {
	if s.Uses[key] > 0 {
		s.Uses[key] = max(0, s.Uses[key]-n)
	}
}

// regainUse gives back up to n expended uses of key.
func regainUse(charID int, key string, n int) error {
	_, _, err := changeSpent(charID, func(s *spent) error { s.regain(key, n); return nil })
	return err
}

// setUse sets the count of key (a timer, or a record such as the feats taken with Lessons of the First Ones).
func setUse(charID int, key string, v int) error {
	_, _, err := changeSpent(charID, func(s *spent) error { s.Uses[key] = v; return nil })
	return err
}

// countUse adds one to the count of key (a record, such as the feats taken with Lessons of the First Ones).
func countUse(charID int, key string) error {
	_, _, err := changeSpent(charID, func(s *spent) error { s.Uses[key]++; return nil })
	return err
}

// useOnce marks a once-per-rest use of key; it is refused (with msg) when it is already used.
func useOnce(charID int, key, msg string) error {
	_, _, err := changeSpent(charID, func(s *spent) error {
		if s.Uses[key] > 0 {
			return &notEnough{msg, 0}
		}
		s.Uses[key] = 1
		return nil
	})
	return err
}

// clearUses gives back every listed use (the keys are removed).
func clearUses(charID int, keys ...string) error {
	_, _, err := changeSpent(charID, func(s *spent) error {
		for _, k := range keys {
			delete(s.Uses, k)
		}
		return nil
	})
	return err
}

// ---------------------------------------------------------------- spell slots

// spendSlotIn expends one slot of the level of the counts (s.Slots or s.Pact) when one of total is left.
func spendSlotIn(counts map[string]int, level, total int, msg string) error {
	k := strconv.Itoa(level)
	if counts[k] >= total {
		return &notEnough{msg, max(0, total-counts[k])}
	}
	counts[k]++
	return nil
}

// regainSlotIn gives back one expended slot of the level; false when none of that level is expended.
func regainSlotIn(counts map[string]int, level int) bool {
	k := strconv.Itoa(level)
	if counts[k] <= 0 {
		return false
	}
	counts[k]--
	return true
}

// spendSlot uses one spell slot of the level (a Warlock's Pact Magic slots count when they are at least that level; they
// are used at their own level), and says whether there was one.
func spendSlot(charID int, class string, level, slot int) bool {
	total := characterSpellSlots(charID, class, level)[slot]
	pact := false
	if strings.EqualFold(class, "warlock") {
		if pl := pactSlotLevel(level); pl >= slot {
			total, slot, pact = getWarlockPactSlots(level), pl, true
		}
	}
	_, _, err := changeSpent(charID, func(s *spent) error {
		counts := s.Slots
		if pact {
			counts = s.Pact
		}
		return spendSlotIn(counts, slot, total, "no slot left")
	})
	return err == nil
}

// addSlot gives the character one more slot of the level: an expended one comes back, or an extra one that vanishes on a
// Long Rest (Font of Magic, the Rod of Absorption).
func (s *spent) addSlot(level int) { s.Slots[strconv.Itoa(level)]-- }

// ---------------------------------------------------------------- rests

// restoreSpent gives back, in one locked step, what a rest restores: every spell and Pact Magic slot and Indomitable on a Long
// Rest (extra slots vanish), and the class resources (recoverClassResources' rule). It returns the resources recovered.
func restoreSpent(q *sql.Tx, charID int, long bool, recover func(used map[string]int) map[string]int) (map[string]int, error) {
	var recovered map[string]int
	_, _, err := changeSpentTx(q, charID, func(s *spent) error {
		if long {
			s.Slots, s.Pact, s.Indomitable = map[string]int{}, map[string]int{}, 0
		}
		recovered = recover(s.Uses)
		return nil
	})
	return recovered, err
}

// ---------------------------------------------------------------- a Dash as a Bonus Action

// spendBonusDash: a Dash as a Bonus Action (Cunning Action, Step of the Wind ...), in one guarded statement: the Bonus Action, the
// movement and, with kiLimit, one Focus Point while fewer than kiLimit are used; sql.ErrNoRows when any is unavailable.
// fleet: Fleet Step, after another Bonus Action (the Bonus Action is already spent).
func spendBonusDash(charID, speed, kiLimit int, fleet ...bool) error {
	var remaining int
	ignoreBonus := len(fleet) > 0 && fleet[0]
	return db.QueryRow(`
		UPDATE characters
		SET bonus_action_used = true,
		    movement_remaining = COALESCE(movement_remaining, 30) + $1,
		    class_resources_used = CASE WHEN $3 > 0 THEN
		        jsonb_set(COALESCE(class_resources_used, '{}'), '{focus}',
		            to_jsonb(COALESCE((class_resources_used->>'focus')::int, 0) + 1))
		        ELSE class_resources_used END
		WHERE id = $2 AND ($4 OR NOT COALESCE(bonus_action_used, false))
		  AND ($3 = 0 OR COALESCE((class_resources_used->>'focus')::int, 0) < $3)
		RETURNING movement_remaining
	`, speed, charID, kiLimit, ignoreBonus).Scan(&remaining)
}
