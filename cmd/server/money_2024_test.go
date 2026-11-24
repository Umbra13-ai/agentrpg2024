package main

// A payment comes from the whole purse (money_2024.go, I-ALDEN-1): the coin named alone when it holds the price, else the purse makes
// change; a purse that can't pay changes nothing, and replies never claim a change that didn't happen.

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestPurseRule(t *testing.T) {
	cases := []struct {
		have   purse
		n      int
		coin   string
		want   purse
		paidOK bool
	}{
		{purse{GP: 23}, 8, "sp", purse{GP: 22, SP: 2}, true},        // the report: change made from a gold piece
		{purse{GP: 23, SP: 9}, 8, "sp", purse{GP: 23, SP: 1}, true}, // the coin holds it: only that coin changes
		{purse{PP: 1, CP: 5}, 3, "gp", purse{GP: 7, CP: 5}, true},   // change from platinum
		{purse{SP: 5}, 1, "gp", purse{SP: 5}, false},                // can't pay: unchanged
		{purse{GP: 1, SP: 2, CP: 3}, 123, "", purse{}, true},        // a price in copper, exactly the purse
		{purse{EP: 2}, 1, "gp", purse{}, true},                      // two electrum pieces are one gold piece
		{purse{GP: 50, EP: 1}, 50, "gp", purse{EP: 1}, true},        // the gold holds it
		{purse{SP: 30}, 2, "gp", purse{GP: 1}, true},                // silver pays a gold price, change as GP
	}
	for _, c := range cases {
		got, ok := c.have.pay(c.n, c.coin)
		if ok != c.paidOK || (ok && got != c.want) || (!ok && got != c.have) {
			t.Errorf("%+v pays %d %s: %+v %v, want %+v %v", c.have, c.n, c.coin, got, ok, c.want, c.paidOK)
		}
	}
	if (purse{GP: 22, SP: 2}).text() != "22 GP, 2 SP" || (purse{}).text() != "nothing" {
		t.Error("purse text")
	}
}

// Only the purse owner writes a payment: the other writers of a coin column are an award (the starting money of a higher level) and an
// item that takes all the gold (the Deck of Many Things); a new one is listed here with its reason.
func TestNoOtherPurseWriter(t *testing.T) {
	allowed := map[string]string{
		"campaign_levels_2024.go": "the Starting Equipment money of a higher level: an award",
		"legendary_items_2024.go": "a card that takes all the gold",
		"money_2024.go":           "the owner",
	}
	re := regexp.MustCompile(`(SET|,)\s*(gold|silver|copper|electrum|platinum)\s*=`)
	files, _ := os.ReadDir(".")
	for _, f := range files {
		n := f.Name()
		if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") || allowed[n] != "" {
			continue
		}
		b, _ := os.ReadFile(n)
		if loc := re.FindIndex(b); loc != nil {
			t.Errorf("%s writes a coin column itself (%q): pay through the purse owner (money_2024.go)", n, b[loc[0]:loc[1]])
		}
	}
}

func TestPostgresPaymentsFromTheWholePurse(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, _ := earlyRulesFixture(t, "Test Purse Watcher", []srdMonsterEntry{{Name: "Poke", Text: "Melee Attack Roll: +4, reach 5 ft. Hit: 1 Bludgeoning damage."}})
	pgExec(t, `UPDATE characters SET gold = 23, silver = 0, copper = 0, electrum = 0, platinum = 0 WHERE id = $1`, hero)
	purseNow := func() purse { p, _ := readPurseIn(db, hero); return p }
	// the report: 8 SP from 23 GP and no silver
	c, j := pgCall(t, "POST", "/api/gm/gold", gm, map[string]interface{}{"campaign_id": camp, "character_id": hero, "amount": -8, "currency": "sp", "reason": "a carnival ticket"})
	if c != 200 || (purseNow() != purse{GP: 22, SP: 2}) || !strings.Contains(fmt.Sprint(j), "purse_after:22 GP, 2 SP") {
		t.Errorf("8 SP from 23 GP: %d %v / %+v", c, j, purseNow())
	}
	// more than the purse holds: refused, nothing changed
	if c, j = pgCall(t, "POST", "/api/gm/gold", gm, map[string]interface{}{"campaign_id": camp, "character_id": hero, "amount": -3, "currency": "pp"}); c != 409 || j["error"] != "not_enough_money" || (purseNow() != purse{GP: 22, SP: 2}) {
		t.Errorf("3 PP from 22 GP 2 SP: %d %v / %+v", c, j, purseNow())
	}
	// an award adds the coin named
	if c, _ = pgCall(t, "POST", "/api/gm/gold", gm, map[string]interface{}{"campaign_id": camp, "character_id": hero, "amount": 2, "currency": "pp"}); c != 200 || (purseNow() != purse{GP: 22, SP: 2, PP: 2}) {
		t.Errorf("award 2 PP: %d / %+v", c, purseNow())
	}
	// the coin named holds it: only that coin changes
	if c, _ = pgCall(t, "POST", "/api/gm/gold", gm, map[string]interface{}{"campaign_id": camp, "character_id": hero, "amount": -1, "currency": "pp"}); c != 200 || (purseNow() != purse{GP: 22, SP: 2, PP: 1}) {
		t.Errorf("1 PP from 1 PP: %d / %+v", c, purseNow())
	}
}
