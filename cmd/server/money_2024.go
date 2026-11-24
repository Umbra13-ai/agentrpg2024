package main

// A character's purse (SRD 5.2.1 Coins: 1 PP = 10 GP, 1 GP = 10 SP = 2 EP, 1 SP = 10 CP) and the one rule of a payment from it: a price is
// paid from the whole purse, its value in copper, never from one coin column alone (I-ALDEN-1: the GM's deduction of 8 SP from a purse of
// 23 GP and no silver answered success and changed nothing; the spellbook copy and downtime's materials read only the gold). When the
// coin named holds the price it alone is spent; otherwise the purse makes change and what is left is written as GP, SP and CP (the shop's
// way since its start). A purse that can't pay changes nothing. The writers are readPurseIn / writePurseIn, inside the caller's
// transaction that has the character's row locked (the inventory change, or lockPurse).

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// purse: a character's coins.
type purse struct{ CP, SP, EP, GP, PP int }

// coinCP: the value of one coin in copper.
var coinCP = map[string]int{"cp": 1, "sp": 10, "ep": 50, "gp": 100, "pp": 1000}

func (p purse) total() int { return p.PP*1000 + p.GP*100 + p.EP*50 + p.SP*10 + p.CP }

func (p *purse) coin(abbrev string) *int {
	switch abbrev {
	case "cp":
		return &p.CP
	case "sp":
		return &p.SP
	case "ep":
		return &p.EP
	case "pp":
		return &p.PP
	}
	return &p.GP
}

// purseOfCP: an amount in copper as GP, SP and CP.
func purseOfCP(cp int) purse { return purse{GP: cp / 100, SP: (cp % 100) / 10, CP: cp % 10} }

// pay: the purse after paying n of the coin abbrev ("" for a price in copper: n is copper), and whether it could.
func (p purse) pay(n int, abbrev string) (purse, bool) {
	cost := n
	if abbrev != "" {
		if c := p.coin(abbrev); *c >= n { // the coin named holds the price: it alone is spent
			*c -= n
			return p, true
		}
		cost = n * coinCP[abbrev]
	}
	if p.total() < cost {
		return p, false
	}
	return purseOfCP(p.total() - cost), true
}

// text: the purse in words ("23 GP, 2 SP"; "nothing").
func (p purse) text() string {
	var parts []string
	for _, c := range []struct {
		n    int
		name string
	}{{p.PP, "PP"}, {p.GP, "GP"}, {p.EP, "EP"}, {p.SP, "SP"}, {p.CP, "CP"}} {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c.n, c.name))
		}
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, ", ")
}

// view: the purse as replies give it.
func (p purse) view() map[string]interface{} {
	return map[string]interface{}{"cp": p.CP, "sp": p.SP, "ep": p.EP, "gp": p.GP, "pp": p.PP, "total_in_gp": float64(p.total()) / 100}
}

// errPurseShort: a payment the purse couldn't make; its transaction changes nothing.
var errPurseShort = errors.New("not enough money")

// readPurseIn reads the purse through q (inside a transaction that has locked the character: the purse it is about to change).
func readPurseIn(q rowQuerier, charID int) (purse, error) {
	var p purse
	err := q.QueryRow(`SELECT COALESCE(copper,0), COALESCE(silver,0), COALESCE(electrum,0), COALESCE(gold,0), COALESCE(platinum,0) FROM characters WHERE id = $1`, charID).
		Scan(&p.CP, &p.SP, &p.EP, &p.GP, &p.PP)
	return p, err
}

// writePurseIn writes the purse in the caller's transaction.
func writePurseIn(tx execer, charID int, p purse) error {
	_, err := tx.Exec(`UPDATE characters SET copper = $1, silver = $2, electrum = $3, gold = $4, platinum = $5 WHERE id = $6`, p.CP, p.SP, p.EP, p.GP, p.PP, charID)
	return err
}

// lockPurse locks the character's row in tx and reads its purse (a payment outside an inventory change).
func lockPurse(tx *sql.Tx, charID int) (purse, error) {
	if _, err := tx.Exec(`SELECT id FROM characters WHERE id = $1 FOR UPDATE`, charID); err != nil {
		return purse{}, err
	}
	return readPurseIn(tx, charID)
}
