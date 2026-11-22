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

// Every hit that pushes or pulls its target in the SRD catalog is read by hitForcedMove, and the GM's residual leaves it out.
func TestHitForcedMovesOverTheCatalog(t *testing.T) {
	var got []string
	for _, m := range srdMonsters2024() {
		for _, list := range [][]srdMonsterEntry{m.Actions, m.BonusActions, m.Reactions, m.Legendary} {
			for _, e := range list {
				i := strings.Index(e.Text, "Hit:")
				if i < 0 {
					continue
				}
				for _, f := range hitForcedMoveRe.FindAllStringSubmatch(e.Text[i:], -1) {
					got = append(got, fmt.Sprintf("%s|%s|%s %s %s|%s", m.Name, e.Name, f[2], f[3], f[4], f[1]))
					if strings.Contains(unappliedHitRider(e.Text), f[0]) {
						t.Errorf("%s %s: the applied movement is still the GM's: %q", m.Name, e.Name, unappliedHitRider(e.Text))
					}
				}
			}
		}
	}
	sort.Strings(got)
	joined := strings.Join(got, "\n")
	for _, want := range []string{"Satyr|Hooves|pushes 10 away from|Medium", "Merrow|Harpoon|pulls 15 toward|Large", "Shambling Mound|Charged Tendril|pulls 5 toward|Medium"} {
		if !strings.Contains(joined, want) {
			t.Errorf("not read: %s\n%s", want, joined)
		}
	}
	t.Logf("hit forced moves: %d\n%s", len(got), joined)
}

// The movement of the CR 0-1 entries that move their creature, read from their wording with the stat block's Speeds.
func TestSpecialMovesOfTheCatalog(t *testing.T) {
	speeds := map[string]map[string]int{}
	entries := map[string]string{}
	for _, m := range srdMonsters2024() {
		sp := map[string]int{}
		var walk int
		fmt.Sscanf(m.Speed, "%d", &walk)
		sp[""] = walk
		for _, k := range speedKindRe.FindAllStringSubmatch(m.Speed, -1) {
			var n int
			fmt.Sscanf(k[2], "%d", &n)
			sp[strings.ToLower(k[1])] = n
		}
		speeds[m.Name] = sp
		for _, list := range [][]srdMonsterEntry{m.Actions, m.BonusActions, m.Reactions} {
			for _, e := range list {
				entries[m.Name+"|"+stripUsage(e.Name)] = e.Text
			}
		}
	}
	for _, c := range []struct {
		key                      string
		feet                     int
		teleport, noOA, straight bool
	}{
		{"Blink Dog|Teleport", 40, true, true, false},
		{"Dryad|Tree Stride", 60, true, true, false},
		{"Sahuagin Warrior|Aquatic Charge", 40, false, false, true},
		{"Giant Seahorse|Bubble Dash", 20, false, true, false},
		{"Seahorse|Bubble Dash", 20, false, true, false},
		{"Gnoll Warrior|Rampage", 15, false, false, false},
		{"Giant Hyena|Rampage", 25, false, false, false},
		{"Octopus|Ink Cloud", 30, false, false, false},
	} {
		text, ok := entries[c.key]
		if !ok {
			t.Errorf("%s: not in the catalog", c.key)
			continue
		}
		name := strings.SplitN(c.key, "|", 2)[0]
		mv, ok := readSpecialMove(text, func(kind string) int { return speeds[name][kind] })
		if !ok || mv.Feet != c.feet || mv.Teleport != c.teleport || mv.NoOA != c.noOA || mv.Straight != c.straight {
			t.Errorf("%s: read %+v (ok %v), want %d ft teleport=%v noOA=%v straight=%v; speeds %v", c.key, mv, ok, c.feet, c.teleport, c.noOA, c.straight, speeds[name])
		}
	}
	for _, key := range []string{"Gnoll Warrior|Rampage", "Giant Hyena|Rampage"} {
		if !afterBloodiedHitRe.MatchString(entries[key]) {
			t.Errorf("%s: its Bloodied gate isn't read", key)
		}
	}
}

// Shape-Shift's forms and their Speeds, a jump that spends less movement than its distance (Leap, invented wording with the
// published pattern), Abduct's wording and a fall's dice.
func TestShapeFormsLeapAbductAndFallDice(t *testing.T) {
	texts := map[string]string{}
	for _, m := range srdMonsters2024() {
		for _, list := range [][]srdMonsterEntry{m.Actions, m.BonusActions, m.Traits} {
			for _, e := range list {
				texts[m.Name+"|"+stripUsage(e.Name)] = e.Text
			}
		}
	}
	imp := formSpeeds(texts["Imp|Shape-Shift"])
	if imp["rat"] != "20 ft." || imp["raven"] != "20 ft., Fly 60 ft." || imp["spider"] != "20 ft., Climb 20 ft." || len(imp) != 3 {
		t.Errorf("Imp's forms: %v", imp)
	}
	quasit := formSpeeds(texts["Quasit|Shape-Shift"])
	if quasit["bat"] != "Speed 10 ft., Fly 40 ft." && quasit["bat"] != "10 ft., Fly 40 ft." || len(quasit) != 3 {
		t.Errorf("Quasit's forms: %v", quasit)
	}
	mv, ok := readSpecialMove("The frog-knight jumps up to 30 feet by spending 10 feet of movement.", func(string) int { return 30 })
	if !ok || mv.Feet != 30 || mv.SpeedCost != 10 || mv.NoOA || mv.Teleport {
		t.Errorf("a jump: %+v %v", mv, ok)
	}
	if !noDragCostRe.MatchString(texts["Bugbear Warrior|Abduct"]) {
		t.Errorf("Abduct's wording isn't read: %q", texts["Bugbear Warrior|Abduct"])
	}
	for feet, want := range map[int]int{5: 1, 10: 1, 25: 2, 200: 20, 400: 20} {
		if got := fallDamageDice(feet); got != want {
			t.Errorf("a fall of %d ft: %dd6, want %dd6", feet, got, want)
		}
	}
}
