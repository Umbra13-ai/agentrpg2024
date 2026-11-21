package main

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Every charge wording of the SRD catalog is read by the movement owner: a charge sentence (extra damage or a condition) or an
// alternative amount judged as condCharged. None is left to the GM.
func TestChargeWordingsOverTheCatalog(t *testing.T) {
	var got []string
	for _, m := range srdMonsters2024() {
		for _, list := range [][]srdMonsterEntry{m.Actions, m.BonusActions, m.Reactions} {
			for _, e := range list {
				if !chargeAnyRe.MatchString(e.Text) {
					continue
				}
				kinds := []string{}
				for _, s := range chargeSentenceRe.FindAllStringSubmatch(e.Text, -1) {
					if s[5] != "" {
						kinds = append(kinds, "extra "+s[5])
					}
					if c := s[6] + s[7]; c != "" {
						kinds = append(kinds, "condition "+c)
					}
					if s[8] != "" {
						kinds = append(kinds, "then GM")
					}
				}
				for _, alt := range parseMonsterAttack(e.Name, e.Text).Alts {
					if alt.Cond == condCharged {
						kinds = append(kinds, "alternative")
					}
				}
				if len(kinds) == 0 {
					kinds = []string{"NOT READ"}
				}
				got = append(got, fmt.Sprintf("%s|%s|%s", m.Name, e.Name, strings.Join(kinds, ",")))
			}
		}
	}
	sort.Strings(got)
	for _, g := range got {
		if strings.HasSuffix(g, "NOT READ") {
			t.Errorf("a charge wording isn't read: %s", g)
		}
	}
	want := map[string]string{
		"Boar|Gore":                "extra Piercing,condition Prone",
		"Elk|Ram":                  "extra Bludgeoning,condition Prone",
		"Giant Goat|Ram":           "extra Bludgeoning,condition Prone",
		"Goat|Ram":                 "alternative",
		"Giant Seahorse|Ram":       "alternative",
		"Warhorse|Hooves":          "extra Bludgeoning,condition Prone",
		"Allosaurus|Claws":         "condition Prone,then GM",
		"Warhorse Skeleton|Hooves": "condition Prone",
	}
	for k, v := range want {
		found := false
		for _, g := range got {
			if strings.HasPrefix(g, k+"|") {
				found = true
				if !strings.HasSuffix(g, "|"+v) {
					t.Errorf("%s read as %s, want %s", k, g, v)
				}
			}
		}
		if !found {
			t.Errorf("%s has no charge wording in the catalog", k)
		}
	}
	t.Logf("charge wordings: %d\n%s", len(got), strings.Join(got, "\n"))
}

// The text one hit reads: charged, the extra damage joins the Hit sentence and the condition is a plain rider; not charged (or too
// big), the sentence is gone; no map and nothing said, the GM is told.
func TestChargeRewrite(t *testing.T) {
	gore := "Melee Attack Roll: +3, reach 5 ft. Hit: 4 (1d6 + 1) Piercing damage. If the target is a Medium or smaller creature and the boar moved 20+ feet straight toward it immediately before the hit, the target takes an extra 3 (1d6) Piercing damage and has the Prone condition."
	hooves := "Melee Attack Roll: +6, reach 5 ft. Hit: 7 (1d6 + 4) Bludgeoning damage. If the target is a Large or smaller creature and the skeleton moved 20+ feet straight toward it immediately before the hit, the target has the Prone condition."
	fits := func(string) bool { return true }
	big := func(string) bool { return false }
	if d, note := chargeRewrite(gore, 25, true, fits); !strings.Contains(d, "Piercing damage plus 3 (1d6) Piercing damage.") || !strings.Contains(d, "The target has the Prone condition.") || strings.Contains(d, "moved") || note != "" {
		t.Errorf("charged Gore: %q %q", d, note)
	}
	if x := parseMonsterAttack("Gore", func() string { d, _ := chargeRewrite(gore, 25, true, fits); return d }()).Extras; len(x) != 1 || x[0].Dice != "1d6" || x[0].Type != "Piercing" {
		t.Errorf("the charged extra isn't one amount of the hit: %+v", x)
	}
	if d, note := chargeRewrite(gore, 15, true, fits); strings.Contains(d, "extra") || strings.Contains(d, "plus") || note != "" {
		t.Errorf("15 feet is no charge: %q %q", d, note)
	}
	if d, _ := chargeRewrite(gore, 25, true, big); strings.Contains(d, "plus") {
		t.Errorf("a target too big got the charge: %q", d)
	}
	if d, note := chargeRewrite(gore, 0, false, fits); strings.Contains(d, "plus") || !strings.Contains(note, `"charged": true`) {
		t.Errorf("no map, nothing said: %q %q", d, note)
	}
	d, _ := chargeRewrite(hooves, 20, true, fits)
	if e, ok := parseHitRider(d); !ok || len(e.Conditions) != 1 || e.Conditions[0] != "prone" {
		t.Errorf("the charged Prone isn't a rider: %q", d)
	}
	if e, ok := parseHitRider(func() string { d, _ := chargeRewrite(hooves, 5, true, fits); return d }()); ok && len(e.Conditions) > 0 {
		t.Errorf("an uncharged hit gave Prone: %+v", e)
	}
	// the alternative amount (condCharged): picked by the damage owner from the attack's approach
	ram := parseMonsterAttack("Ram", "Melee Attack Roll: +4, reach 5 ft. Hit: 9 (2d6 + 2) Bludgeoning damage, or 11 (2d8 + 2) Bludgeoning damage if the seahorse moved 20+ feet straight toward the target immediately before the hit.")
	if a := applyDamageAlternatives(ram.monsterActionRow, hitCondition{ChargedFeet: 20}); a.DamageDice != "2d8+2" {
		t.Errorf("a charge's alternative: %+v", a)
	}
	if a := applyDamageAlternatives(ram.monsterActionRow, hitCondition{ChargedFeet: 10}); a.DamageDice != "2d6+2" {
		t.Errorf("no charge kept the primary: %+v", a)
	}
	if gmJudgedAlternatives(ram.monsterActionRow) != "" {
		t.Errorf("a charge's alternative still goes to the GM: %q", gmJudgedAlternatives(ram.monsterActionRow))
	}
}
