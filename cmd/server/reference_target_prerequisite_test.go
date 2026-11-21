package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestPostgresFreshGazeTargetPrerequisites(t *testing.T) {
	original := suppliedFreshYeti(t)
	for _, probe := range []string{"out of range", "more than one target", "blinded source", "unconfirmed sight"} {
		t.Run(probe, func(t *testing.T) {
			f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
				*m = original
				m.Name = "Review Focus Caster"
			})
			near := referenceSaveTarget(t, f, "Visible target control", -10, "", "")
			second := referenceSaveTarget(t, f, "Second target control", -10, "", "")
			far := referenceSaveTarget(t, f, "Out-of-range target control", -10, "", "")
			ironGolemMap(t, f, map[int]int{near: 2, second: 3, far: 7})
			targets := []int{far}
			confirmed := true
			if probe == "unconfirmed sight" {
				targets = []int{near}
				confirmed = false
			}
			if probe == "more than one target" {
				targets = []int{near, second}
			}
			if probe == "blinded source" {
				targets = []int{near}
				if add := addCondition(f.camp, f.caster, condAdd{Condition: "blinded", Source: "Fixture"}); !add.Added || add.Err != nil {
					t.Fatalf("blindness control: %+v", add)
				}
			}
			text := fmt.Sprint(ironGolemAction(t, f, "Chilling Gaze", map[string]interface{}{"target_ids": targets, "confirm_targeting": confirmed})["monster_action_result"])
			if !strings.Contains(text, "Nothing was rolled") || !strings.Contains(text, "[GM:") {
				t.Errorf("missing explicit private targeting refusal: %s", text)
			}
			for _, target := range targets {
				if strings.Contains(text, "fails the Constitution saving throw") || monsterHPNow(f.camp, target) != 500 || hasCondMarker(f.camp, target, "paralyzed") {
					t.Errorf("ineligible target %d was rolled and affected: %s", target, text)
				}
			}
		})
	}
}

func TestPostgresMonsterTargetRefusalPreservesRechargeAndLegendaryUse(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
		save := "Constitution Saving Throw: DC 15, one creature it can see within 30 feet. Failure: 7 (2d6) Force damage."
		m.Actions = []srdMonsterEntry{{Name: "Distant Call (Recharge 6)", Text: save}}
		m.Legendary = []srdMonsterEntry{{Name: "Night Call", Text: save}}
		m.LegendaryIn = "Legendary Action Uses: 2"
	})
	target := referenceSaveTarget(t, f, "Single Target Control", -10, "", "")
	// No map or sight information is available. Confirmation must be explicit.
	call := func(confirm bool) string {
		return fmt.Sprint(ironGolemAction(t, f, "Distant Call", map[string]interface{}{"target_ids": []int{target}, "confirm_targeting": confirm})["monster_action_result"])
	}
	if result := call(false); !strings.Contains(result, "can see the target and that it is within 30 feet") ||
		monsterHPNow(f.camp, target) != 500 || len(loadMonsterState(f.camp, f.caster).RechargeSpent) != 0 {
		t.Fatalf("unknown distance and sight: %s", result)
	}
	if result := call(true); !strings.Contains(result, "fails the Constitution saving throw") ||
		monsterHPNow(f.camp, target) >= 500 || len(loadMonsterState(f.camp, f.caster).RechargeSpent) != 1 {
		t.Fatalf("confirmed target did not consume the recharge action: %s", result)
	}

	if _, err := changeFight(f.camp, func(combat *fight) error {
		i, _ := combat.entry(f.char)
		if i < 0 {
			return fmt.Errorf("the hero needs a turn")
		}
		combat.Index = i
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/campaigns/%d/combat/next", f.camp)
	if code, reply := pgCall(t, "POST", path, f.gm, map[string]interface{}{}); code != 200 {
		t.Fatalf("open window: %d %v", code, reply)
	}
	legendary := func(confirm bool) (int, map[string]interface{}) {
		return pgCall(t, "POST", "/api/gm/legendary-action", f.gm, map[string]interface{}{
			"campaign_id": f.camp, "combatant_id": f.caster, "action": "Night Call", "target_ids": []int{target}, "confirm_targeting": confirm,
		})
	}
	hp := monsterHPNow(f.camp, target)
	code, j := legendary(false)
	if code != 400 || j["error"] != "target_ineligible" || monsterHPNow(f.camp, target) != hp ||
		loadMonsterState(f.camp, f.caster).LAUsed != 0 {
		t.Fatalf("unknown target spent a Legendary Action: %d %v", code, j)
	}
	code, j = legendary(true)
	if code != 200 || asInt(j["uses_left"]) != 1 || monsterHPNow(f.camp, target) >= hp {
		t.Fatalf("available window did not accept confirmed target: %d %v", code, j)
	}
}

func TestPostgresFreshGazeParalysisExpiresAtSourceStart(t *testing.T) {
	original := suppliedFreshYeti(t)
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
		*m = original
		m.Name = "Review Focus Caster"
	})
	target := referenceSaveTarget(t, f, "Gaze end control", -10, "", "")
	ironGolemMap(t, f, map[int]int{target: 2})
	// Set the fixture to a known earlier slot. Use the real turn route to
	// observe every intervening initiative and the source's next start.
	if _, err := changeFight(f.camp, func(combat *fight) error {
		i, _ := combat.entry(f.char)
		if i < 0 {
			return fmt.Errorf("hero turn missing")
		}
		combat.Index = i
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ironGolemAction(t, f, "Chilling Gaze", map[string]interface{}{
		"target_ids": []int{target}, "confirm_targeting": true,
	})
	if !hasCondMarker(f.camp, target, "paralyzed") {
		t.Fatal("the ordinary failed gaze did not establish the timing control")
	}
	path := fmt.Sprintf("/api/campaigns/%d/combat/next", f.camp)
	sawTarget := false
	for turn := 0; turn < 10; turn++ {
		code, j := pgCall(t, "POST", path, f.gm, map[string]interface{}{})
		if code != 200 {
			t.Fatalf("advance: %d %v", code, j)
		}
		id := currentTurnID(f.camp)
		if id == target {
			sawTarget = true
		}
		if id == f.caster {
			if !sawTarget {
				t.Fatal("fixture did not drive the target's turn before the source's next turn")
			}
			if hasCondMarker(f.camp, target, "paralyzed") {
				t.Fatal("Gaze paralysis survived the source's next turn start")
			}
			return
		}
		if !hasCondMarker(f.camp, target, "paralyzed") {
			t.Fatalf("Gaze paralysis ended before its source's turn, at %d", id)
		}
	}
	t.Fatal("the advance route did not reach the source's next turn")
}

func TestPostgresAttachedLegendaryTargetConfirmationIsPerAction(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
		m.Legendary = []srdMonsterEntry{{
			Name: "Invented Night Call", Text: "Constitution Saving Throw: DC 15, one creature it can see within 30 feet. Failure: 7 (2d6) Force damage.",
		}}
		m.LegendaryIn = "Legendary Action Uses: 2"
	})
	target := referenceSaveTarget(t, f, "Attached target control", -10, "", "")
	if _, err := changeFight(f.camp, func(combat *fight) error {
		i, _ := combat.entry(f.char)
		if i < 0 {
			return fmt.Errorf("hero turn missing")
		}
		combat.Index = i
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	actions := []legendaryRequest{
		{CombatantID: f.caster, Action: "Invented Night Call", TargetIDs: []int{target}},
		{CombatantID: f.caster, Action: "Invented Night Call", TargetIDs: []int{target}, ConfirmTargeting: true},
	}
	path := fmt.Sprintf("/api/campaigns/%d/combat/next", f.camp)
	code, j := pgCall(t, "POST", path, f.gm, map[string]interface{}{"legendary_actions": actions})
	if code != 200 {
		t.Fatalf("advance with attached actions: %d %v", code, j)
	}
	replies, ok := j["legendary_actions_taken"].([]interface{})
	if !ok || len(replies) != 2 {
		t.Fatalf("expected two independent action replies: %v", j)
	}
	first := replies[0].(map[string]interface{})
	second := replies[1].(map[string]interface{})
	if first["error"] != "target_ineligible" || second["error"] != nil || asInt(second["uses_left"]) != 1 {
		t.Fatalf("attached rejection consumed the confirmation candidate's window: %v", replies)
	}
	if hp := monsterHPNow(f.camp, target); hp > 498 || hp < 488 {
		t.Fatalf("expected one application of 2d6 damage, got %d HP", hp)
	}
}

func TestPostgresMonsterSightEffectRespectsMapCover(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
		m.Actions = []srdMonsterEntry{{
			Name: "Invented Sight Effect",
			Text: "Constitution Saving Throw: DC 15, one creature it can see within 30 feet. Failure: 7 (2d6) Force damage.",
		}}
	})
	target := referenceSaveTarget(t, f, "Sight cover control", -10, "", "")
	path := fmt.Sprintf("/api/campaigns/%d/battle-map", f.camp)
	revision := ""
	publish := func(row string) {
		t.Helper()
		mapState := encounterMap{
			Grid: []string{row},
			Tokens: []mapToken{
				{ID: f.caster, Name: "Review Focus Caster", Position: mapSquare{0, 0}, Size: 1},
				{ID: target, Name: "Sight cover control", Position: mapSquare{2, 0}, Size: 1},
			},
		}
		code, reply := pgCall(t, "PUT", path, f.gm, map[string]interface{}{"map": mapState, "confirmation": revision, "forced_movement": true})
		if code != 200 {
			t.Fatalf("cover map: %d %v", code, reply)
		}
		revision, _ = reply["revision"].(string)
		if revision == "" {
			t.Fatalf("cover map revision: %v", reply)
		}
	}
	play := func() string {
		return fmt.Sprint(ironGolemAction(t, f, "Invented Sight Effect", map[string]interface{}{
			"target_ids": []int{target}, "confirm_targeting": true,
		})["monster_action_result"])
	}
	publish(".#.")
	if sees, known := monsterSeesTarget(f.camp, f.caster, target); sees || !known {
		t.Fatal("fixture did not establish a wall between the tokens")
	}
	if text := play(); !strings.Contains(text, "Nothing was rolled") || monsterHPNow(f.camp, target) != 500 {
		t.Errorf("a target behind map cover still rolled and took damage: %s", text)
	}
	publish("...")
	if text := play(); !strings.Contains(text, "Constitution saving throw") || monsterHPNow(f.camp, target) >= 500 {
		t.Errorf("target in confirmed clear sight did not take damage: %s", text)
	}
}
