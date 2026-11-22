package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestPostgresGMRecordsManuallyResolvedRecharge(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
		m.Actions = []srdMonsterEntry{
			{Name: "Rush Pulse (Recharge 6)", Text: "Constitution Saving Throw: DC 15, each creature in a 15-foot Cone. Failure: 7 (2d6) Force damage. Success: Half damage."},
			{Name: "Ordinary Pulse", Text: "Constitution Saving Throw: DC 15, one target within 30 feet. Failure: 7 (2d6) Force damage."},
		}
	})
	target := reviewSaveTarget(t, f, "Manual recharge control", -10, "", "")
	reviewMap(t, f, map[int]int{target: 2})
	spend := func(token, action string) (int, map[string]interface{}) {
		return pgCall(t, "POST", "/api/gm/monster-features", token, map[string]interface{}{
			"campaign_id": f.camp, "combatant_id": f.caster, "spend_recharge": action,
		})
	}
	if code, j := spend(f.player, "Rush Pulse"); code < 400 {
		t.Fatalf("player recorded a GM Recharge use: %d %v", code, j)
	}
	if code, j := spend(f.gm, "Ordinary Pulse"); code != 400 || j["error"] != "invalid_recharge_action" {
		t.Fatalf("ordinary action accepted as rechargeable: %d %v", code, j)
	}
	if used := loadMonsterState(f.camp, f.caster).RechargeSpent; len(used) != 0 {
		t.Fatalf("invalid request consumed a use: %v", used)
	}

	// GM has adjudicated the effect here. Record the use independently of
	// resolving damage or conditions so the server cannot replay the effect.
	if code, j := spend(f.gm, "Rush Pulse (Recharge 6)"); code != 200 {
		t.Fatalf("record named Recharge use: %d %v", code, j)
	}
	if monsterHPNow(f.camp, target) != 500 {
		t.Fatal("recording the use applied damage again")
	}
	if code, j := spend(f.gm, "rush pulse"); code != 409 || j["error"] != "recharge_spent" {
		t.Fatalf("duplicate use was not refused: %d %v", code, j)
	}
	reply := reviewMonsterAction(t, f, "Rush Pulse", map[string]interface{}{"target_ids": []int{target}, "confirm_targeting": true})
	if result := fmt.Sprint(reply["monster_action_result"]); !strings.Contains(result, "hasn't recharged yet") {
		t.Fatalf("ordinary action path did not see the recorded use: %s", result)
	}
	if got := loadMonsterState(f.camp, f.caster).RechargeSpent; len(got) != 1 || got[0] != "Rush Pulse" {
		t.Fatalf("expected the same shared Recharge record: %v", got)
	}
}
