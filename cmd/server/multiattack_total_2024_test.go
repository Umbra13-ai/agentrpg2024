package main

// A Multiattack never rolls fewer attacks than its text states (statedAttackTotal, I-ALDENQA-7): over every SRD stat block a plan is the
// stated total or is refused, and an invented "makes two attacks with its beak" rolls two Beaks.

import (
	"strings"
	"testing"
)

func TestMultiattackRollsTheStatedTotal(t *testing.T) {
	checked := 0
	for _, m := range srdMonsters2024() {
		rows := actionRowsOf(m)
		for _, e := range m.Actions {
			if !strings.HasPrefix(e.Name, "Multiattack") {
				continue
			}
			stated := statedAttackTotal(strings.ToLower(withoutReplaceSentence(e.Text)))
			plan, problem := multiattackPlan(e.Text, rows)
			if stated > 0 && problem == "" && len(plan) != stated {
				t.Errorf("%s: %q states %d attacks, the plan rolls %d", m.Name, e.Text, stated, len(plan))
			}
			checked++
		}
	}
	if checked < 100 {
		t.Errorf("only %d SRD Multiattacks checked", checked)
	}
	swan := inventedMonster("Test Pond Swan", "Beast", nil, []srdMonsterEntry{
		{Name: "Multiattack", Text: "The swan makes two attacks with its beak."},
		{Name: "Beak", Text: "Melee Attack Roll: +4, reach 5 ft. Hit: 5 (1d6 + 2) Piercing damage."}})
	rows := actionRowsOf(swan)
	if plan, problem := multiattackPlan(swan.Actions[0].Text, rows); problem != "" || len(plan) != 2 || plan[1].Name != "Beak" {
		t.Errorf("two attacks with its beak: %d %q", len(plan), problem)
	}
}

// actionRowsOf: a stat block's actions as the plan reads them (entryActionRow, as the import's Multiattack check builds them).
func actionRowsOf(m srdMonster2024) []monsterActionRow {
	var rows []monsterActionRow
	for _, e := range m.Actions {
		rows = append(rows, entryActionRow(e))
	}
	return rows
}
