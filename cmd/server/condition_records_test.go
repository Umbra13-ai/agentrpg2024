package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSRDConditionsVerbatim(t *testing.T) {
	if n := len(srdConditions()); n != 15 {
		t.Fatalf("want 15 conditions, got %d", n)
	}
	r, ok := srdCondition("Restrained")
	if !ok || !strings.Contains(strings.Join(r.Text, " "), "Attack rolls against you have Advantage, and your attack rolls have Disadvantage.") {
		t.Fatalf("Restrained text is not the SRD text: %v", r.Text)
	}
	p, _ := srdCondition("prone")
	if !strings.Contains(strings.Join(p.Text, " "), "half your Speed (round down)") {
		t.Fatalf("Prone text: %v", p.Text)
	}
}

func TestPlayersDoNotSeeDCs(t *testing.T) {
	rec := conditionRecord{ID: 1, TargetID: 5, Condition: "restrained", Source: "chain around the legs", Notes: "secret",
		Escapes: []conditionEscape{{Ability: "Strength", Skill: "Athletics", DC: 14}}}
	player, _ := json.Marshal(conditionView(rec, 30, false))
	gm, _ := json.Marshal(conditionView(rec, 30, true))
	for _, leak := range []string{`"dc"`, "chain around the legs", "secret"} {
		if strings.Contains(string(player), leak) {
			t.Errorf("player view leaks %s: %s", leak, player)
		}
	}
	if !strings.Contains(string(player), "Strength (Athletics) check") {
		t.Errorf("player view should say how the condition can end: %s", player)
	}
	for _, need := range []string{`"dc":14`, "chain around the legs", "secret"} {
		if !strings.Contains(string(gm), need) {
			t.Errorf("GM view lacks %s: %s", need, gm)
		}
	}
}

func TestProneStandUpCostAndGrappleDefaults(t *testing.T) {
	v := conditionView(conditionRecord{Condition: "prone"}, 35, false)
	if !strings.Contains(v["ways_to_end"].([]map[string]interface{})[0]["label"].(string), "17 feet") {
		t.Errorf("half of 35 rounded down is 17: %v", v["ways_to_end"])
	}
	if _, has := conditionView(conditionRecord{Condition: "prone"}, 0, false)["ways_to_end"]; has {
		t.Error("a creature with Speed 0 cannot right itself")
	}
	esc := defaultEscapes("grappled", 13)
	if len(esc) != 2 || esc[0].Skill != "Athletics" || esc[1].Skill != "Acrobatics" || esc[0].DC != 13 {
		t.Errorf("grapple escapes: %+v", esc)
	}
	if _, msg := normalizeEscapes([]conditionEscape{{Ability: "luck", DC: 10}}); msg == "" {
		t.Error("an unknown ability must be refused")
	}
	if _, msg := normalizeEscapes([]conditionEscape{{Ability: "str", DC: 0}}); msg == "" {
		t.Error("a missing DC must be refused")
	}
}

func TestValidateEndSave(t *testing.T) {
	if validateEndSave(nil) != "" {
		t.Error("no end_save is fine")
	}
	ok := &conditionSave{Ability: "wis", DC: 14}
	if msg := validateEndSave(ok); msg != "" || ok.Ability != "Wisdom" || ok.When != "end_of_turn" {
		t.Errorf("wis DC 14: %q %+v", msg, ok)
	}
	for _, bad := range []*conditionSave{{Ability: "luck", DC: 10}, {Ability: "str", DC: 0}, {Ability: "str", DC: 12, When: "sometime"}} {
		if validateEndSave(bad) == "" {
			t.Errorf("should refuse %+v", bad)
		}
	}
}

func TestStartOfTurnSaveAndAdjacency(t *testing.T) {
	s := &conditionSave{Ability: "str", DC: 12, When: "start_of_turn"}
	if validateEndSave(s) != "" || s.When != "start_of_turn" {
		t.Errorf("start_of_turn is a valid timing: %+v", s)
	}
	one := mapToken{Size: 1}
	big := mapToken{Size: 2}
	if !tokensAdjacent(mapToken{Size: 1, Position: mapSquare{2, 2}}, mapToken{Size: 1, Position: mapSquare{3, 3}}) {
		t.Error("diagonal neighbours are within 5 feet")
	}
	if tokensAdjacent(mapToken{Size: 1, Position: mapSquare{2, 2}}, mapToken{Size: 1, Position: mapSquare{4, 2}}) {
		t.Error("two squares apart is 10 feet")
	}
	if !tokensAdjacent(mapToken{Size: 1, Position: mapSquare{0, 0}}, mapToken{Size: 2, Position: mapSquare{1, 1}}) || one.Size != 1 || big.Size != 2 {
		t.Error("a Large token is adjacent when any of its squares is")
	}
}
