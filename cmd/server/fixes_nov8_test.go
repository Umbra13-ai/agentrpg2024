package main

// Unit tests for the pure helpers added on Nov 8, 2026 (level 20 battle tests): they guard the fixes F22-F31 and the features built
// after them against regressions without a database.

import (
	"fmt"
	"strings"
	"testing"
)

func TestAttackExtraDamage(t *testing.T) {
	got := attackExtraDamage("Melee Attack Roll: +11, reach 10 ft. Hit: 21 (4d6 + 7) Slashing damage plus 10 (3d6) Fire damage.")
	if len(got) != 1 || got[0].Dice != "3d6" || got[0].Type != "Fire" {
		t.Fatalf("Flame Sword extra damage: got %+v, want 3d6 Fire", got)
	}
	if got := attackExtraDamage("Melee Attack Roll: +9, reach 5 ft. Hit: 8 (1d8 + 4) Bludgeoning damage."); len(got) != 0 {
		t.Errorf("no extra damage: got %+v", got)
	}
	// "plus" before the Hit: line isn't damage of the hit
	got = attackExtraDamage("Ranged Attack Roll: +11, range 60/240 ft. Hit: 23 (3d10 + 7) Bludgeoning damage plus 4 (1d8) Fire damage, and the target is pushed")
	if len(got) != 1 || got[0].Dice != "1d8" {
		t.Errorf("Hammer Throw: got %+v, want 1d8 Fire", got)
	}
	got = attackExtraDamage("Hit: 13 (2d8 + 4) Piercing damage plus 7 (2d6 + 0) Poison damage.")
	if len(got) != 1 || got[0].Dice != "2d6+0" {
		t.Errorf("a bonus inside the parentheses: got %+v", got)
	}
}

func TestCastDealsNoDamage(t *testing.T) {
	for _, s := range []string{"wish", "meld-into-stone", "dimension-door", "teleport", "contact-other-plane", "geas", "dream", "hunters-mark", "hex"} {
		if !castDealsNoDamage(s) {
			t.Errorf("%s should deal no damage to a target when cast", s)
		}
	}
	for _, s := range []string{"fireball", "disintegrate", "power-word-kill", "magic-missile"} {
		if castDealsNoDamage(s) {
			t.Errorf("%s deals damage when cast", s)
		}
	}
}

func TestTitleCase(t *testing.T) {
	cases := map[string]string{"fell glare": "Fell Glare", "grovel": "Grovel", "": "", "acid-splash": "Acid-Splash", "already Up": "Already Up", "x": "X"}
	for in, want := range cases {
		if got := titleCase(in); got != want {
			t.Errorf("titleCase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompanionSpeedFeet(t *testing.T) {
	cases := map[string]int{"60 ft., Fly 60 ft.": 60, "5 ft., Fly 60 ft.": 60, "30 ft., Fly 60 ft., Swim 30 ft.": 60, "40 ft., Climb 40 ft.": 40, "": 30}
	for speed, want := range cases {
		if got := companionSpeedFeet(companion{Speed: speed}); got != want {
			t.Errorf("speed %q: got %d, want %d", speed, got, want)
		}
	}
}

func TestAnimatedObjectSize(t *testing.T) {
	cases := []struct {
		desc       string
		slot, mod  int
		size       string
		counts, hp int
		slam       string
	}{
		{"animate the chairs", 5, 5, "Medium", 1, 10, "1d4+3"},
		{"a large statue", 5, 5, "Large", 2, 20, "2d6+8"},
		{"huge statues", 5, 5, "Huge", 3, 40, "2d12+8"},
		{"huge statues", 7, 5, "Huge", 3, 40, "4d12+8"},
		{"brooms", 6, 4, "Medium", 1, 10, "2d4+3"},
	}
	for _, c := range cases {
		size, counts, hp, slam := animatedObjectSize(c.desc, c.slot, c.mod)
		if size != c.size || counts != c.counts || hp != c.hp || slam != c.slam {
			t.Errorf("%q slot %d mod %d: got %s %d %d %s, want %s %d %d %s", c.desc, c.slot, c.mod, size, counts, hp, slam, c.size, c.counts, c.hp, c.slam)
		}
	}
}

func TestStripUsageAndMultiattack(t *testing.T) {
	if got := stripUsage("Grave Strike (Vampire Form Only)"); got != "Grave Strike" {
		t.Errorf("stripUsage: got %q", got)
	}
	actions := []monsterActionRow{
		{Name: "Multiattack (Vampire Form Only)", Desc: "The vampire makes two Grave Strike attacks and uses Bite."},
		{Name: "Grave Strike (Vampire Form Only)", AttackBonus: 9, DamageDice: "1d8+4", DamageType: "Bludgeoning"},
		{Name: "Bite (Bat or Vampire Form Only)"},
	}
	plan, problem := multiattackPlan(actions[0].Desc, actions)
	if problem != "" || len(plan) != 2 || plan[0].DamageDice != "1d8+4" {
		t.Errorf("Vampire Multiattack: got %d attacks %+v, want two Grave Strikes", len(plan), plan)
	}
}

func TestMultiattackUseCount(t *testing.T) {
	// "makes one Bite attack and uses Antennae twice" must resolve Antennae twice; the second use was silently
	// dropped (found by the CR 0-1 ordered harness: the Rust Monster reply showed one Antennae use).
	cases := []struct {
		multi, name string
		want        int
		problem     bool
	}{
		{"the rust monster makes one bite attack and uses antennae twice.", "antennae", 2, false},
		{"the rust monster makes one bite attack and uses antennae three times.", "antennae", 3, false},
		{"the rust monster makes one bite attack and uses antennae 2 times.", "antennae", 2, false},
		{"the vampire makes two grave strike attacks and uses bite.", "bite", 1, false},
		{"the toad makes one bite attack and swallow.", "swallow", 1, false},
		{"the ape makes two fist attacks.", "bite", 0, false},
		// the count binds to its own action, not another's
		{"the ettin uses antennae twice and uses bite.", "bite", 1, false},
		{"the ettin uses antennae twice and uses bite.", "antennae", 2, false},
		// unreadable, ambiguous or absurd counts refuse: nothing is rolled for the unresolved use
		{"the flail snail uses flail tentacle forty times.", "flail tentacle", 0, true},
		{"the flail snail uses flail tentacle 40 times.", "flail tentacle", 0, true},
		{"the flail snail uses flail tentacle many times.", "flail tentacle", 0, true},
		{"the flail snail uses flail tentacle 3.", "flail tentacle", 0, true},
	}
	for _, c := range cases {
		got, problem := multiattackUseCount(c.multi, c.name)
		if got != c.want || (problem != "") != c.problem {
			t.Errorf("multiattackUseCount(%q, %q): got %d, %q; want %d, problem=%t", c.multi, c.name, got, problem, c.want, c.problem)
		}
	}
}

func twiceUserSetup(t *testing.T, multi string) (reviewConcentrationCase, int) {
	t.Helper()
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
		m.Actions = append(m.Actions,
			srdMonsterEntry{Name: "Slam", Text: "Melee Attack Roll: Automatic hit, reach 5 ft. Hit: 4 Force damage."},
			srdMonsterEntry{Name: "Shriek", Text: "The creature shrieks. Dexterity Saving Throw: DC 11, each target. Failure: 2 (1d4) Thunder damage."},
			srdMonsterEntry{Name: "Multiattack", Text: multi})
	})
	code, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", f.camp), f.gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Twice User", "monster_key": f.key, "initiative": -200}}})
	if code != 200 {
		t.Fatalf("attacker: %d %v", code, j)
	}
	attacker := monsterIDsByKeyOrName(f.camp, "Twice User")
	if len(attacker) != 1 {
		t.Fatalf("attacker IDs: %v", attacker)
	}
	cp := fmt.Sprintf("/api/campaigns/%d", f.camp)
	_, state := pgCall(t, "GET", cp+"/battle-map", f.gm, nil)
	put := map[string]interface{}{"map": encounterMap{Grid: []string{"............"}, Tokens: []mapToken{
		{ID: attacker[0], Name: "Twice User", Position: mapSquare{0, 0}, Size: 1},
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

func narrateMultiattack(t *testing.T, f reviewConcentrationCase, attacker int, target string) map[string]interface{} {
	t.Helper()
	code, j := pgCall(t, "POST", "/api/gm/narrate", f.gm, map[string]interface{}{"campaign_id": f.camp, "narration": "An invented sequence.", "monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", attacker), "action": "Multiattack", "target": target}})
	if code != 200 {
		t.Fatalf("multiattack: %d %v", code, j)
	}
	return j
}

func TestPostgresMultiattackUsesTwice(t *testing.T) {
	// Route reproduction for the dropped second use: "uses Shriek twice" must resolve Shriek twice
	// in the narrate reply. Mutation-checked: with the count ignored the reply shows one Shriek.
	f, attacker := twiceUserSetup(t, "The creature makes one Slam attack and uses Shriek twice.")
	j := narrateMultiattack(t, f, attacker, fmt.Sprintf("%d", f.char))
	result := fmt.Sprint(j["monster_action_result"])
	if n := strings.Count(result, "Shriek:"); n != 2 {
		t.Fatalf("uses Shriek twice resolved %d times: %s", n, result)
	}
	if !strings.Contains(result, "Slam:") {
		t.Fatalf("the listed attack did not resolve: %s", result)
	}
}

func TestPostgresMultiattackUsesTwiceMonsterTarget(t *testing.T) {
	// The same Multiattack against another monster of the fight: the listed uses were silently dropped
	// on this path (monsterAttacksMonster returned before the uses ran).
	f, attacker := twiceUserSetup(t, "The creature makes one Slam attack and uses Shriek twice.")
	j := narrateMultiattack(t, f, attacker, fmt.Sprintf("#%d", f.caster))
	result := fmt.Sprint(j["monster_action_result"])
	if n := strings.Count(result, "Shriek:"); n != 2 {
		t.Fatalf("monster target: uses Shriek twice resolved %d times: %s", n, result)
	}
	if !strings.Contains(result, "Slam:") {
		t.Fatalf("monster target: the listed attack did not resolve: %s", result)
	}
}

func TestPostgresMultiattackUsesTwiceCompanionTarget(t *testing.T) {
	// The same Multiattack against a companion: the listed uses were silently dropped on this path
	// (monsterAttacksCompanion returned before the uses ran).
	f, attacker := twiceUserSetup(t, "The creature makes one Slam attack and uses Shriek twice.")
	if _, ok := companionByNameOrID(f.camp, "React Pet"); !ok {
		t.Fatalf("the fixture companion is missing")
	}
	j := narrateMultiattack(t, f, attacker, "React Pet")
	result := fmt.Sprint(j["monster_action_result"])
	if n := strings.Count(result, "Shriek:"); n != 2 {
		t.Fatalf("companion target: uses Shriek twice resolved %d times: %s", n, result)
	}
	if !strings.Contains(result, "Slam:") {
		t.Fatalf("companion target: the listed attack did not resolve: %s", result)
	}
}

func TestPostgresMultiattackInvalidCountRefusesPlan(t *testing.T) {
	// Plan prevalidation: an unreadable count refuses the WHOLE Multiattack before anything is rolled. The listed
	// attack does not execute either, and the reply and the persisted state agree, so the GM adjudicates the plan
	// instead of retrying a partially executed one.
	f, attacker := twiceUserSetup(t, "The creature makes one Slam attack and uses Shriek forty times.")
	var hpBefore int
	if err := db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, f.char).Scan(&hpBefore); err != nil {
		t.Fatal(err)
	}
	j := narrateMultiattack(t, f, attacker, fmt.Sprintf("%d", f.char))
	result := fmt.Sprint(j["monster_action_result"])
	atk, _ := j["monster_attack"].(map[string]interface{})
	if atk["refused"] != true {
		t.Fatalf("the plan was not refused: %v", j)
	}
	if strings.Contains(result, "Slam:") {
		t.Fatalf("the listed attack executed despite the unreadable count: %s", result)
	}
	if !strings.Contains(result, "refused before anything is rolled") {
		t.Fatalf("the refusal does not say nothing was rolled: %s", result)
	}
	var hpAfter int
	if err := db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, f.char).Scan(&hpAfter); err != nil {
		t.Fatal(err)
	}
	if hpAfter != hpBefore {
		t.Fatalf("the refused plan still dealt damage: HP %d -> %d", hpBefore, hpAfter)
	}
}

func TestWildShapeLimits(t *testing.T) {
	cases := []struct {
		level int
		known int
		cr    float64
		fly   bool
	}{{2, 4, 0.25, false}, {4, 6, 0.5, false}, {7, 6, 0.5, false}, {8, 8, 1, true}, {20, 8, 1, true}}
	for _, c := range cases {
		k, cr, fly := wildShapeLimits(c.level)
		if k != c.known || cr != c.cr || fly != c.fly {
			t.Errorf("level %d: got %d %v %v", c.level, k, cr, fly)
		}
	}
	if crValue("1/4") != 0.25 || crValue("1/2") != 0.5 || crValue("1") != 1 || crValue("1/8") != 0.125 {
		t.Error("crValue fractions")
	}
}

func TestBeastAttackPlan(t *testing.T) {
	attacks := []compAttack{{Name: "Bite", Dice: "1d8+4"}, {Name: "Claw", Dice: "2d6+4"}}
	acts := []map[string]interface{}{{"name": "Multiattack", "desc": "The bear makes one Bite attack and one Claw attack."}}
	plan := beastAttackPlan(attacks, acts, "")
	if len(plan) != 2 || plan[0].Name != "Bite" || plan[1].Name != "Claw" {
		t.Errorf("Brown Bear Multiattack: got %+v", plan)
	}
	if plan := beastAttackPlan(attacks, nil, "claw it"); len(plan) != 1 || plan[0].Name != "Claw" {
		t.Errorf("a named attack without Multiattack: got %+v", plan)
	}
}
