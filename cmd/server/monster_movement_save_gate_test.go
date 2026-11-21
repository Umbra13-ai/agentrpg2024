package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestPostgresMovementSaveGatePreservesUses(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
		m.Actions = []srdMonsterEntry{
			{Name: "Rush Pulse (Recharge 6)", Text: "The creature moves up to its Speed. Each time it enters a creature's space during this move, that target is subject to the following effect. Constitution Saving Throw: DC 15. Failure: 7 (2d6) Force damage."},
			{Name: "Ordinary Pulse", Text: "Constitution Saving Throw: DC 15, one target within 30 feet. Failure: 7 (2d6) Force damage."},
		}
	})
	target := referenceSaveTarget(t, f, "Movement prerequisite control", -10, "", "")
	ironGolemMap(t, f, map[int]int{target: 2})
	play := func(action string, apply bool) string {
		return fmt.Sprint(ironGolemAction(t, f, action, map[string]interface{}{
			"target_ids": []int{target}, "confirm_targeting": true, "apply_damage": apply,
		})["monster_action_result"])
	}
	for _, apply := range []bool{false, true} {
		result := play("Rush Pulse", apply)
		if !strings.Contains(result, "Nothing was rolled") || !strings.Contains(result, "[GM: Resolve the required movement") ||
			strings.Contains(result, "saving throw") || monsterHPNow(f.camp, target) != 500 ||
			len(loadMonsterState(f.camp, f.caster).RechargeSpent) != 0 {
			t.Errorf("unverified path affected the encounter (apply %t): %s", apply, result)
		}
	}
	if result := play("Ordinary Pulse", true); !strings.Contains(result, "fails the Constitution saving throw") || monsterHPNow(f.camp, target) >= 500 {
		t.Errorf("unrelated save did not resolve against its legal target: %s", result)
	}
}
