package main

// A Multiattack's replacement option replaces one of its attacks and is never one more (GM doc A2, Issue Tracker I-ALDEN-PARTY-01):
// the attacks are read outside the replacement sentence (withoutReplaceSentence), "one of the attacks" is any attack, an option's name
// leaves out "if available" however it is written, and an attack's name is read whole ("Claw" is not read in "Sickening Claw").
// Every monster here is invented.

import (
	"fmt"
	"strings"
	"testing"
)

func TestMultiattackPlanReadsTheReplacementApart(t *testing.T) {
	rows := []monsterActionRow{
		{Name: "Claw", DamageDice: "1d6", DamageType: "slashing", Desc: "Melee Attack Roll: +5, reach 5 ft. Hit: 4 (1d6 + 1) Slashing damage."},
		{Name: "Sickening Claw", DamageDice: "1d8", DamageType: "slashing", Desc: "Melee Attack Roll: +5, reach 5 ft. Hit: 5 (1d8 + 1) Slashing damage."},
		{Name: "Sting", DamageDice: "1d4", DamageType: "piercing", Desc: "Melee Attack Roll: +5, reach 5 ft. Hit: 3 (1d4 + 1) Piercing damage."},
	}
	cases := []struct{ text, plan, replaced, options string }{
		{"The fiend makes three Claw attacks. It can replace one of the attacks with a Sting attack.", "Claw,Claw,Claw", "", "Sting"},
		{"The fiend makes three Claw attacks. It can replace one of its attacks with a Sting attack.", "Claw,Claw,Claw", "", "Sting"},
		{"The fiend makes three Claw attacks. It can replace one Claw attack with a use of Gnaw, if available.", "Claw,Claw,Claw", "Claw", "Gnaw"},
		{"The fiend makes four Claw attacks. It can replace one Claw attack with Insect Gorge (if available).", "Claw,Claw,Claw,Claw", "Claw", "Insect Gorge"},
		{"The fiend makes two Sickening Claw attacks.", "Sickening Claw,Sickening Claw", "", ""},
		{"The fiend makes two attacks: one Claw attack and one Sickening Claw attack.", "Claw,Sickening Claw", "", ""},
		{"The fiend makes two Sickening Claw attacks and one Claw attack.", "Claw,Sickening Claw,Sickening Claw", "", ""},
	}
	for _, c := range cases {
		p, problem := multiattackPlan(c.text, rows)
		var names []string
		for _, a := range p {
			names = append(names, a.Name)
		}
		r, _ := replaceRuleOf(c.text)
		if c.options != "" {
			if _, ok := r.option(c.options + " (if available)"); !ok {
				t.Errorf("%q: the option named with (if available) is not found", c.text)
			}
		}
		if problem != "" || strings.Join(names, ",") != c.plan || r.Replaced != c.replaced || strings.Join(r.names(), ",") != c.options {
			t.Errorf("%q: plan %v (%q), replaced %q, options %v", c.text, names, problem, r.Replaced, r.names())
		}
	}
}

func TestPostgresMultiattackReplacementNeverAddsAnAttack(t *testing.T) {
	testPostgres(t)
	claw := srdMonsterEntry{Name: "Claw", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 1 (1d1) Slashing damage."}
	sting := srdMonsterEntry{Name: "Sting", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 1 (1d1) Piercing damage."}
	multi := srdMonsterEntry{Name: "Multiattack", Text: "The fiend makes three Claw attacks. It can replace one of the attacks with a Sting attack."}
	gm, _, camp, hero, fiend := earlyRulesFixture(t, "Test Sting Fiend", []srdMonsterEntry{multi, claw, sting})
	count := func(body map[string]interface{}) (int, string) {
		t.Helper()
		ma := map[string]interface{}{"monster": fmt.Sprintf("#%d", fiend), "action": "Multiattack", "target": fmt.Sprint(hero)}
		for k, v := range body {
			ma[k] = v
		}
		_, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": ma})
		res := fmt.Sprint(j["monster_action_result"])
		return strings.Count(res, " vs AC "), res
	}
	if n, res := count(nil); n != 3 || strings.Contains(res, "Sting:") {
		t.Errorf("the Multiattack as written: %d attacks: %s", n, res)
	}
	if n, res := count(map[string]interface{}{"attacks": []string{"Claw", "Claw", "Claw"}}); n != 3 || strings.Contains(res, "Sting:") {
		t.Errorf("three declared Claws: %d attacks: %s", n, res)
	}
	if n, res := count(map[string]interface{}{"use": "Sting"}); n != 3 || strings.Count(res, "Claw:") != 2 || strings.Count(res, "Sting:") != 1 {
		t.Errorf("Sting in place of one Claw: %d attacks: %s", n, res)
	}
}
