package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestPostgresMonsterEmbeddedDamageNeedsGMAdjudication(t *testing.T) {
	effect := srdMonsterEntry{Name: "Invented Crossing (1/Day)", Text: "When a creature crosses the boundary, the target takes 2 (1d4) Force damage. This can happen only once on a turn."}
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) { m.Actions = append(m.Actions, effect) })
	target := monsterIDsByKeyOrName(f.camp, "Opponent")[0]
	before := monsterHPNow(f.camp, target)
	code, j := pgCall(t, "POST", "/api/gm/narrate", f.gm, map[string]interface{}{"campaign_id": f.camp, "narration": "An invented event-driven effect.", "monster_action": map[string]interface{}{"monster": "Review Focus Caster", "action": effect.Name, "target_ids": []int{target}}})
	text := fmt.Sprint(j["monster_action_result"])
	if code != 200 || !strings.Contains(text, "GM adjudication") || !strings.Contains(text, "Nothing was rolled") {
		t.Fatalf("missing handoff: %d %v", code, j)
	}
	key, _ := monsterDayUse(effect, "")
	if monsterHPNow(f.camp, target) != before || loadDayUses(f.camp, f.caster)[key] != 0 {
		t.Fatal("unresolved trigger dealt damage or used a charge")
	}
	code, j = pgCall(t, "GET", fmt.Sprintf("/api/gm/notices?campaign_id=%d", f.camp), f.gm, nil)
	if code != 200 || !strings.Contains(fmt.Sprint(j), effect.Name) {
		t.Fatalf("missing private GM notice: %d %v", code, j)
	}
}
