package main

// A creature's ability scores have one owner (this file). Before it, eleven places wrote a character's score columns each in its
// own way (Ability Score Improvements, feats, the Barbarian's and Monk's capstones, potions, tomes, attunement items, the Deck of
// Many Things, Wild Shape, the GM's and the admin's editors), and nothing could lower a score and give it back.
//
//   - changeAbilities (changeAbilitiesIn inside a caller's transaction) is the one writer of the six score columns (str, dex, con,
//     intl, wis, cha) and of item_base_scores: the row is locked, the change gets the scores as they are now, and they are written in
//     the same transaction. A new Constitution modifier changes the Hit Point maximum by the level for each point (the Hit Point
//     owner), unless the change says the Hit Points stay (Wild Shape keeps them: SRD 5.2.1).
//   - A reduction (SRD 5.2.1 Shadow Draining Swipe: "the target's Strength score decreases by 1d4. The target dies if this reduces
//     that score to 0") is reduceAbility: one ability_reductions row per reduction, with its source; a character's column is the
//     score as it is now (lowered), so every reader of the column reads the reduced score; a monster's or a companion's score is its
//     stat block's less its open reductions (creatureAbilityScore, read by saveModifier and the monster checks). The 2024 text gives
//     the reduction no end of its own: it lasts until an effect removes it (Greater Restoration: "Any reduction to one of the
//     target's ability scores", endAbilityReductions).
//   - TestNoOtherAbilityScoreWriter fails on a statement that writes a score column anywhere else.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/agentrpg/agentrpg/game"
)

// abilityColumn: the column of an ability ("int" and "intl" are Intelligence).
var abilityColumn = map[string]string{"str": "str", "dex": "dex", "con": "con", "int": "intl", "intl": "intl", "wis": "wis", "cha": "cha"}

// abilityKeyOf: the short name of an ability as a player, a column or a stat block names it ("Strength", "intl", "STR"), "" for none.
func abilityKeyOf(word string) string {
	if k, ok := game.AbilityKey(word); ok {
		return k
	}
	return ""
}

// abilityRow is a character's scores as the owner hands them to a change: Scores by short name, Base the scores an attuned item
// keeps to give back (item_base_scores, by column: "str", "con", "intl"), Level for the Hit Points of a new Constitution modifier.
type abilityRow struct {
	Scores map[string]int
	Base   map[string]int
	Level  int
}

// abilityHow: how a change is written. KeepHP: a new Constitution modifier leaves the Hit Point maximum as it is (Wild Shape).
type abilityHow struct {
	KeepHP bool
}

// changeAbilities runs change on the character's scores, the row locked, and writes them in one transaction.
func changeAbilities(charID int, how abilityHow, change func(tx *sql.Tx, a *abilityRow) error) (before, after abilityRow, err error) {
	err = inTx(func(tx *sql.Tx) error {
		var e error
		before, after, e = changeAbilitiesIn(tx, charID, change)
		return e
	})
	if err != nil {
		return before, after, err
	}
	abilitiesChanged(charID, how, before, after)
	return before, after, nil
}

// changeAbilitiesIn is changeAbilities inside a caller's transaction; the caller then calls abilitiesChanged once it has committed.
func changeAbilitiesIn(tx *sql.Tx, charID int, change func(tx *sql.Tx, a *abilityRow) error) (before, after abilityRow, err error) {
	var s [6]int
	var base []byte
	if err = tx.QueryRow(`SELECT COALESCE(str,10), COALESCE(dex,10), COALESCE(con,10), COALESCE(intl,10), COALESCE(wis,10), COALESCE(cha,10),
		COALESCE(item_base_scores,'{}'), COALESCE(level,1) FROM characters WHERE id = $1 FOR UPDATE`, charID).
		Scan(&s[0], &s[1], &s[2], &s[3], &s[4], &s[5], &base, &before.Level); err != nil {
		return before, after, err
	}
	before.Scores = map[string]int{}
	for i, k := range abilityOrder {
		before.Scores[k] = s[i]
	}
	before.Base = map[string]int{}
	json.Unmarshal(base, &before.Base)
	after = abilityRow{Scores: map[string]int{}, Base: map[string]int{}, Level: before.Level}
	for k, v := range before.Scores {
		after.Scores[k] = v
	}
	for k, v := range before.Base {
		after.Base[k] = v
	}
	if err = change(tx, &after); err != nil {
		return before, after, err
	}
	for _, k := range abilityOrder {
		after.Scores[k] = max(0, min(30, after.Scores[k])) // a score is 0 to 30 (SRD 5.2.1 Ability Scores)
	}
	baseJSON, _ := json.Marshal(after.Base)
	_, err = tx.Exec(`UPDATE characters SET str = $1, dex = $2, con = $3, intl = $4, wis = $5, cha = $6, item_base_scores = $7 WHERE id = $8`,
		after.Scores["str"], after.Scores["dex"], after.Scores["con"], after.Scores["int"], after.Scores["wis"], after.Scores["cha"], baseJSON, charID)
	return before, after, err
}

// abilitiesChanged: what follows a committed change of scores: the Hit Point maximum of a new Constitution modifier (the Hit Point
// owner, by the level for each point).
func abilitiesChanged(charID int, how abilityHow, before, after abilityRow) {
	if how.KeepHP {
		return
	}
	if d := (game.Modifier(after.Scores["con"]) - game.Modifier(before.Scores["con"])) * max(1, after.Level); d != 0 {
		changeMaxHP(charID, d, true) // hit_points_2024.go
	}
}

// errNoAbilityChange: a change that decided to write nothing (its refusal is the caller's); changeAbilities rolls it back.
var errNoAbilityChange = errors.New("no ability change")

// addAbilityScore raises (or lowers) one score by delta, no higher than hi (no lower than 0), and answers it before and after.
func addAbilityScore(charID int, ability string, delta, hi int) (from, to int) {
	short := abilityKeyOf(ability)
	changeAbilities(charID, abilityHow{}, func(_ *sql.Tx, a *abilityRow) error {
		from = a.Scores[short]
		to = max(0, min(hi, from+delta))
		if delta > 0 && from > hi { // a score already above the cap stays as it is
			to = from
		}
		a.Scores[short] = to
		return nil
	})
	return from, to
}

// setAbilityScores sets scores (by short name or column) through the owner; the editors' and the GM's way.
func setAbilityScores(charID int, set map[string]int) (abilityRow, error) {
	_, after, err := changeAbilities(charID, abilityHow{}, func(_ *sql.Tx, a *abilityRow) error {
		for k, v := range set {
			short := abilityKeyOf(k)
			if short == "" {
				return fmt.Errorf("%q is not an ability", k)
			}
			a.Scores[short] = v
		}
		return nil
	})
	return after, err
}

// abilityReduction is one open reduction of a creature's score.
type abilityReduction struct {
	ID      int
	Ability string
	Amount  int
	Source  string
}

// reduceAbility lowers a creature's score by amount (a character's column; a monster's or a companion's through its open
// reductions), records the reduction with its source, and answers the score now. lobby is the creature's campaign (a monster's id is
// one of its fight).
func reduceAbility(lobby, id int, ability string, amount int, source string) (now int, err error) {
	short := abilityKeyOf(ability)
	if short == "" || amount <= 0 {
		return 0, fmt.Errorf("no reduction of %q by %d", ability, amount)
	}
	if id > 0 && !isCompanionCombatID(id) {
		var dealt int
		_, after, err := changeAbilities(id, abilityHow{}, func(tx *sql.Tx, a *abilityRow) error {
			dealt = min(amount, a.Scores[short])
			a.Scores[short] -= dealt
			if col := abilityColumn[short]; a.Base[col] > 0 { // an attuned item's kept score falls too, so taking the item off gives back the reduced score
				a.Base[col] = max(0, a.Base[col]-dealt)
			}
			_, e := tx.Exec(`INSERT INTO ability_reductions (lobby_id, creature_id, ability, amount, source) VALUES ($1, $2, $3, $4, $5)`, lobby, id, short, dealt, source)
			return e
		})
		return after.Scores[short], err
	}
	before := creatureAbilityScore(lobby, id, short)
	dealt := min(amount, before)
	if _, err := db.Exec(`INSERT INTO ability_reductions (lobby_id, creature_id, ability, amount, source) VALUES ($1, $2, $3, $4, $5)`, lobby, id, short, dealt, source); err != nil {
		return before, err
	}
	return before - dealt, nil
}

// openAbilityReductions: a creature's reductions that haven't ended ("" ability: all of them).
func openAbilityReductions(lobby, id int, ability string) []abilityReduction {
	q := `SELECT id, ability, amount, source FROM ability_reductions WHERE creature_id = $1 AND ended_at IS NULL`
	args := []interface{}{id}
	if id <= 0 || isCompanionCombatID(id) { // a monster's id is one of its fight
		q += ` AND lobby_id = $2`
		args = append(args, lobby)
	}
	if short := abilityKeyOf(ability); short != "" {
		q += fmt.Sprintf(` AND ability = $%d`, len(args)+1)
		args = append(args, short)
	}
	rows, err := db.Query(q+` ORDER BY id`, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []abilityReduction
	for rows.Next() {
		var r abilityReduction
		if rows.Scan(&r.ID, &r.Ability, &r.Amount, &r.Source) == nil {
			out = append(out, r)
		}
	}
	return out
}

// endAbilityReductions ends a creature's open reductions of one ability ("" ability: the first reduced one, as Greater Restoration
// removes "any reduction to one of the target's ability scores") and gives the score back. It answers the ability restored and by
// how much (0: nothing was reduced).
func endAbilityReductions(lobby, id int, ability string) (string, int, error) {
	open := openAbilityReductions(lobby, id, ability)
	if len(open) == 0 {
		return "", 0, nil
	}
	short := open[0].Ability
	total := 0
	var ids []int
	for _, r := range open {
		if r.Ability == short {
			total += r.Amount
			ids = append(ids, r.ID)
		}
	}
	end := func(q execer) error {
		for _, rid := range ids {
			if _, err := q.Exec(`UPDATE ability_reductions SET ended_at = NOW() WHERE id = $1 AND ended_at IS NULL`, rid); err != nil {
				return err
			}
		}
		return nil
	}
	if id > 0 && !isCompanionCombatID(id) {
		_, _, err := changeAbilities(id, abilityHow{}, func(tx *sql.Tx, a *abilityRow) error {
			a.Scores[short] += total
			if col := abilityColumn[short]; a.Base[col] > 0 {
				a.Base[col] += total
			}
			return end(tx)
		})
		return short, total, err
	}
	return short, total, end(db)
}

// creatureAbilityScore: a creature's score as it is now: a character's column (already reduced); a monster's or a companion's stat
// block score less its open reductions.
func creatureAbilityScore(lobby, id int, ability string) int {
	short := abilityKeyOf(ability)
	if short == "" {
		return 10
	}
	if id > 0 && !isCompanionCombatID(id) {
		return abilityScoreOf(id, short)
	}
	key := monsterKeyOfCombatant(lobby, id)
	if isCompanionCombatID(id) {
		if c, ok := loadCompanion(id - companionIDBase); ok {
			key = c.Form
		}
	}
	score := 10
	if sb := monsterStatBlock(key); sb != nil {
		score = asInt(sb[short])
	}
	for _, r := range openAbilityReductions(lobby, id, short) {
		score -= r.Amount
	}
	return max(0, score)
}

// monsterAbilityShift: how much a monster's or a companion's open reductions lower the modifier of an ability (0 or less): added to
// its stat block's save and skill bonuses, which already hold the unreduced modifier.
func monsterAbilityShift(lobby, id int, ability string) int {
	short := abilityKeyOf(ability)
	if short == "" || (id > 0 && !isCompanionCombatID(id)) || len(openAbilityReductions(lobby, id, short)) == 0 {
		return 0
	}
	key := monsterKeyOfCombatant(lobby, id)
	if isCompanionCombatID(id) {
		if c, ok := loadCompanion(id - companionIDBase); ok {
			key = c.Form
		}
	}
	sb := monsterStatBlock(key)
	if sb == nil {
		return 0
	}
	return game.Modifier(creatureAbilityScore(lobby, id, short)) - game.Modifier(asInt(sb[short]))
}
