package main

import (
	"fmt"
	"strings"
	"testing"
)

// Declared-composition Multiattacks: the GM declares the chosen attacks ("attacks": [...]); the server validates
// the declaration against the Multiattack text and rolls exactly it. Reproduction-first: without the declaration
// path every choice text below refused with "one by one by name" (TestMultiattackAlternativesAreNeverCombined).
// Mutation-checked: dropping the options membership check, the uniform check or the substitute cap fails exactly
// that case's test.

func declaredRows() []monsterActionRow {
	return append(multiRows(),
		monsterActionRow{Name: "Roar", Desc: "Each creature within 30 feet makes a Wisdom Saving Throw: DC 11. Failure: the target has the Frightened condition until the end of its next turn."},
		monsterActionRow{Name: "Enthralling Panache", Desc: "Wisdom Saving Throw: DC 12, one target. Failure: the target has the Charmed condition until the end of its next turn."},
	)
}

func TestParseMultiattackChoiceShapes(t *testing.T) {
	cases := []struct {
		desc       string
		budget     int
		options    []string
		uniform    bool
		substitute string
		subCap     int
	}{
		{"The creature makes two attacks, using Bite or Claw in any combination.", 2, []string{"bite", "claw"}, false, "", 0},
		{"The creature makes two Slam attacks or two Bite attacks.", 2, []string{"slam", "bite"}, true, "", 0},
		{"The creature makes two Bite or Slam attacks.", 2, []string{"bite", "slam"}, false, "", 0},
		{"The creature makes one Bite or Claw attack, and it can use Roar.", 1, []string{"bite", "claw"}, false, "", 0},
		{"The creature makes two Slam attacks. It can replace one attack with a use of Roar.", 2, []string{"slam"}, false, "roar", 1},
		{"The creature makes two Bite attacks. It can replace one attack with a use of Enthralling Panache.", 2, []string{"bite"}, false, "enthralling panache", 1},
	}
	for _, c := range cases {
		spec, ok := parseMultiattackChoice(c.desc, declaredRows())
		if !ok {
			t.Errorf("%q: did not parse", c.desc)
			continue
		}
		if spec.budget != c.budget || spec.uniform != c.uniform || spec.substitute != c.substitute || spec.subCap != c.subCap {
			t.Errorf("%q: spec %+v", c.desc, spec)
		}
		if strings.Join(spec.options, ",") != strings.Join(c.options, ",") {
			t.Errorf("%q: options %v, want %v", c.desc, spec.options, c.options)
		}
	}
}

func TestMultiattackDeclaredPlanValidation(t *testing.T) {
	spec := func(desc string) multiattackChoiceSpec {
		s, ok := parseMultiattackChoice(desc, declaredRows())
		if !ok {
			t.Fatalf("%q did not parse", desc)
		}
		return s
	}
	// happy paths
	if plan, _, problem := multiattackDeclaredPlan(spec("The creature makes two attacks, using Bite or Claw in any combination."), declaredRows(), []string{"Claw", "Bite"}); problem != "" || len(plan) != 2 || plan[0].Name != "Claw" || plan[1].Name != "Bite" {
		t.Errorf("any-combination declaration: %d %q", len(plan), problem)
	}
	if plan, _, problem := multiattackDeclaredPlan(spec("The creature makes two Slam attacks or two Bite attacks."), declaredRows(), []string{"Bite", "Bite"}); problem != "" || len(plan) != 2 {
		t.Errorf("uniform declaration: %d %q", len(plan), problem)
	}
	plan, subs, problem := multiattackDeclaredPlan(spec("The creature makes two Slam attacks. It can replace one attack with a use of Roar."), declaredRows(), []string{"Slam", "Roar"})
	if problem != "" || len(plan) != 1 || len(subs) != 1 || subs[0] != "roar" {
		t.Errorf("substitution declaration: plan %d subs %v %q", len(plan), subs, problem)
	}
	plan, subs, problem = multiattackDeclaredPlan(spec("The creature makes two Bite attacks. It can replace one attack with a use of Enthralling Panache."), declaredRows(), []string{"Bite", "Enthralling Panache"})
	if problem != "" || len(plan) != 1 || len(subs) != 1 || subs[0] != "enthralling panache" {
		t.Errorf("multi-word substitution declaration: plan %d subs %v %q", len(plan), subs, problem)
	}
	if _, _, problem = multiattackDeclaredPlan(spec("The creature makes two Bite attacks. It can replace one attack with a use of Enthralling Panache."), declaredRows(), []string{"Enthralling Panache", "Enthralling Panache"}); problem == "" {
		t.Errorf("an over-cap multi-word substitution was accepted")
	}
	// refusals, each before anything is rolled
	refusals := []struct {
		desc     string
		declared []string
	}{
		{"The creature makes two attacks, using Bite or Claw in any combination.", []string{"Bite"}},                                                                                                                               // wrong count
		{"The creature makes two attacks, using Bite or Claw in any combination.", []string{"Bite", "Slam"}},                                                                                                                       // Slam is not an option
		{"The creature makes two attacks, using Bite or Claw in any combination.", []string{"Bite", "Roar"}},                                                                                                                       // no substitute in the text
		{"The creature makes two Slam attacks or two Bite attacks.", []string{"Slam", "Bite"}},                                                                                                                                     // mixed when uniform
		{"The creature makes two Slam attacks. It can replace one attack with a use of Roar.", []string{"Slam", "Bite"}},                                                                                                           // Bite not in the fixed plan
		{"The creature makes two Slam attacks. It can replace one attack with a use of Roar.", []string{"Roar", "Roar"}},                                                                                                           // over the replacement cap
		{"The creature makes two Slam attacks.", []string{"Slam", "Slam", "Slam", "Slam", "Slam", "Slam", "Slam", "Slam", "Slam", "Slam", "Slam", "Slam", "Slam", "Slam", "Slam", "Slam", "Slam", "Slam", "Slam", "Slam", "Slam"}}, // over the plan cap
	}
	for _, c := range refusals {
		if _, _, problem := multiattackDeclaredPlan(spec(c.desc), declaredRows(), c.declared); problem == "" {
			t.Errorf("%q %v: accepted, want a refusal", c.desc, c.declared)
		} else if !strings.Contains(problem, "Nothing was rolled") && !strings.HasPrefix(problem, multiattackOverCapProblem) {
			t.Errorf("%q %v: refusal does not say nothing was rolled: %s", c.desc, c.declared, problem)
		}
	}
}

func declaredUserSetup(t *testing.T, multi string) (reviewConcentrationCase, int) {
	t.Helper()
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
		m.Actions = append(m.Actions,
			srdMonsterEntry{Name: "Slam", Text: "Melee Attack Roll: Automatic hit, reach 5 ft. Hit: 4 Force damage."},
			srdMonsterEntry{Name: "Bite", Text: "Melee Attack Roll: Automatic hit, reach 5 ft. Hit: 3 Piercing damage."},
			srdMonsterEntry{Name: "Shriek", Text: "The creature shrieks. Dexterity Saving Throw: DC 11, each target. Failure: 2 (1d4) Thunder damage."},
			srdMonsterEntry{Name: "Enthralling Shriek", Text: "The creature shrieks beguilingly. Wisdom Saving Throw: DC 11, one target. Failure: the target has the Charmed condition until the end of its next turn."},
			srdMonsterEntry{Name: "Multiattack", Text: multi})
	})
	code, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", f.camp), f.gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Declared User", "monster_key": f.key, "initiative": -200}}})
	if code != 200 {
		t.Fatalf("attacker: %d %v", code, j)
	}
	attacker := monsterIDsByKeyOrName(f.camp, "Declared User")
	if len(attacker) != 1 {
		t.Fatalf("attacker IDs: %v", attacker)
	}
	cp := fmt.Sprintf("/api/campaigns/%d", f.camp)
	_, state := pgCall(t, "GET", cp+"/battle-map", f.gm, nil)
	put := map[string]interface{}{"map": encounterMap{Grid: []string{"............"}, Tokens: []mapToken{
		{ID: attacker[0], Name: "Declared User", Position: mapSquare{0, 0}, Size: 1},
		{ID: f.char, Name: "Target", Position: mapSquare{1, 0}, Size: 1},
		{ID: f.caster, Name: "Review Focus Caster", Position: mapSquare{2, 0}, Size: 1}}}, "forced_movement": true}
	if rev, ok := state["revision"].(string); ok {
		put["confirmation"] = rev
	}
	if c, r := pgCall(t, "PUT", cp+"/battle-map", f.gm, put); c != 200 {
		t.Fatalf("put map: %d %v", c, r)
	}
	return f, attacker[0]
}

func narrateDeclaredMultiattack(t *testing.T, f reviewConcentrationCase, attacker int, target string, attacks []string) map[string]interface{} {
	t.Helper()
	body := map[string]interface{}{"campaign_id": f.camp, "narration": "An invented sequence.", "monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", attacker), "action": "Multiattack", "target": target}}
	if attacks != nil {
		body["monster_action"].(map[string]interface{})["attacks"] = attacks
	}
	code, j := pgCall(t, "POST", "/api/gm/narrate", f.gm, body)
	if code != 200 {
		t.Fatalf("declared multiattack: %d %v", code, j)
	}
	return j
}

func targetHP(t *testing.T, charID int) int {
	t.Helper()
	var hp int
	if err := db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, charID).Scan(&hp); err != nil {
		t.Fatal(err)
	}
	return hp
}

func TestPostgresMultiattackDeclaredCombination(t *testing.T) {
	f, attacker := declaredUserSetup(t, "The creature makes two attacks, using Slam or Bite in any combination.")
	// Control: undeclared, the choice keeps its handoff refusal.
	j := narrateDeclaredMultiattack(t, f, attacker, fmt.Sprintf("%d", f.char), nil)
	result := fmt.Sprint(j["monster_action_result"])
	if !strings.Contains(result, "one by one by name") {
		t.Fatalf("undeclared choice lost its handoff: %s", result)
	}
	// Declared: exactly the declaration rolls, through the automated pipeline.
	j = narrateDeclaredMultiattack(t, f, attacker, fmt.Sprintf("%d", f.char), []string{"Bite", "Slam"})
	result = fmt.Sprint(j["monster_action_result"])
	if !strings.Contains(result, "Bite:") || !strings.Contains(result, "Slam:") {
		t.Fatalf("the declared composition did not resolve: %s", result)
	}
	if strings.Contains(result, "one by one by name") {
		t.Fatalf("a validated declaration was still handed off: %s", result)
	}
}

func TestPostgresMultiattackDeclaredUniform(t *testing.T) {
	f, attacker := declaredUserSetup(t, "The creature makes two Slam attacks or two Bite attacks.")
	before := targetHP(t, f.char)
	// Mixed: the text offers all-one-way or all-the-other; mixing refuses before anything is rolled.
	j := narrateDeclaredMultiattack(t, f, attacker, fmt.Sprintf("%d", f.char), []string{"Slam", "Bite"})
	if refusal := fmt.Sprint(j["monster_action_refused"]); !strings.Contains(refusal, "Nothing was rolled") {
		t.Fatalf("a mixed uniform declaration was not refused: %v", j)
	}
	if after := targetHP(t, f.char); after != before {
		t.Fatalf("a refused declaration still dealt damage: %d -> %d", before, after)
	}
	// Uniform: rolls twice.
	j = narrateDeclaredMultiattack(t, f, attacker, fmt.Sprintf("%d", f.char), []string{"Bite", "Bite"})
	result := fmt.Sprint(j["monster_action_result"])
	if n := strings.Count(result, "Bite:"); n != 2 {
		t.Fatalf("the uniform declaration resolved %d Bites: %s", n, result)
	}
}

func TestPostgresMultiattackDeclaredSubstitution(t *testing.T) {
	f, attacker := declaredUserSetup(t, "The creature makes two Slam attacks. It can replace one attack with a use of Shriek.")
	before := targetHP(t, f.char)
	// Over the replacement cap: refused, nothing rolled.
	j := narrateDeclaredMultiattack(t, f, attacker, fmt.Sprintf("%d", f.char), []string{"Shriek", "Shriek"})
	if refusal := fmt.Sprint(j["monster_action_refused"]); !strings.Contains(refusal, "Nothing was rolled") {
		t.Fatalf("an over-cap substitution was not refused: %v", j)
	}
	if after := targetHP(t, f.char); after != before {
		t.Fatalf("a refused substitution still dealt damage: %d -> %d", before, after)
	}
	// One replacement: one Slam plus the resolved substitute; the exercised option leaves the GM handoff.
	j = narrateDeclaredMultiattack(t, f, attacker, fmt.Sprintf("%d", f.char), []string{"Slam", "Shriek"})
	result := fmt.Sprint(j["monster_action_result"])
	if !strings.Contains(result, "Slam:") || !strings.Contains(result, "Shriek:") {
		t.Fatalf("the substitution did not resolve: %s", result)
	}
	if strings.Contains(result, "not automated") {
		t.Fatalf("the exercised substitute was still handed to the GM: %s", result)
	}
	// Undeclared: the fixed sequence rolls and the option stays a handoff (unchanged behavior).
	j = narrateDeclaredMultiattack(t, f, attacker, fmt.Sprintf("%d", f.char), nil)
	result = fmt.Sprint(j["monster_action_result"])
	if n := strings.Count(result, "Slam:"); n != 2 || !strings.Contains(result, "not automated") {
		t.Fatalf("the undeclared fixed sequence changed: %s", result)
	}
}

// A substitute name of several words ("a use of Enthralling Shriek"): the text contract reads the whole name,
// so the declared replacement validates and resolves instead of silently falling back to the fixed sequence.
func TestPostgresMultiattackDeclaredMultiWordSubstitution(t *testing.T) {
	f, attacker := declaredUserSetup(t, "The creature makes two Slam attacks. It can replace one attack with a use of Enthralling Shriek.")
	before := targetHP(t, f.char)
	j := narrateDeclaredMultiattack(t, f, attacker, fmt.Sprintf("%d", f.char), []string{"Enthralling Shriek", "Enthralling Shriek"})
	if refusal := fmt.Sprint(j["monster_action_refused"]); !strings.Contains(refusal, "Nothing was rolled") {
		t.Fatalf("an over-cap multi-word substitution was not refused: %v", j)
	}
	if after := targetHP(t, f.char); after != before {
		t.Fatalf("a refused substitution still dealt damage: %d -> %d", before, after)
	}
	j = narrateDeclaredMultiattack(t, f, attacker, fmt.Sprintf("%d", f.char), []string{"Slam", "Enthralling Shriek"})
	result := fmt.Sprint(j["monster_action_result"])
	if n := strings.Count(result, "Slam:"); n != 1 {
		t.Fatalf("the declared composition did not roll exactly one Slam: %s", result)
	}
	if !strings.Contains(result, "Enthralling Shriek:") {
		t.Fatalf("the declared substitute did not resolve as a use (the fixed sequence took over): %s", result)
	}
	if strings.Contains(result, "not automated") {
		t.Fatalf("the exercised substitute was still handed to the GM: %s", result)
	}
}

func TestPostgresMultiattackDeclaredRefusalRollsNothing(t *testing.T) {
	f, attacker := declaredUserSetup(t, "The creature makes two attacks, using Slam or Bite in any combination.")
	before := targetHP(t, f.char)
	for _, declared := range [][]string{{"Bite"}, {"Bite", "Shriek"}, {"Bite", "Nonsense"}} {
		j := narrateDeclaredMultiattack(t, f, attacker, fmt.Sprintf("%d", f.char), declared)
		if refusal := fmt.Sprint(j["monster_action_refused"]); !strings.Contains(refusal, "Nothing was rolled") {
			t.Fatalf("%v: not refused with a nothing-rolled refusal: %v", declared, j)
		}
		if strings.Contains(fmt.Sprint(j["monster_action_result"]), "Bite:") {
			t.Fatalf("%v: part of the declaration executed: %v", declared, j)
		}
	}
	if after := targetHP(t, f.char); after != before {
		t.Fatalf("refused declarations changed Hit Points: %d -> %d", before, after)
	}
}
