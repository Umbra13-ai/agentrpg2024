package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestPostgresMonsterSavePreviewPreservesState(t *testing.T) {
	entry := srdMonsterEntry{Name: "Preview Wave (Recharge 6)", Text: "Wisdom Saving Throw: DC 99, each target. Failure: The target has the Frightened condition for 1 minute."}
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) { m.Actions = append(m.Actions, entry) })
	code, j := pgCall(t, "POST", "/api/gm/companion", f.gm, map[string]interface{}{"campaign_id": f.camp, "character_id": f.char, "monster_key": f.key, "name": "Preview Target"})
	if code != 200 {
		t.Fatalf("test companion: %d %v", code, j)
	}
	pet := companionIDBase + asInt(j["companion"].(map[string]interface{})["id"])
	targets := []int{f.char, pet, f.caster}
	turnUses := legendaryResistanceLeft(f.camp, f.caster)
	if turnUses == 0 {
		t.Fatal("control: monster needs available Legendary Resistance")
	}
	play := func(apply bool) string {
		t.Helper()
		code, j := pgCall(t, "POST", "/api/gm/narrate", f.gm, map[string]interface{}{"campaign_id": f.camp, "narration": "An invented save effect.", "monster_action": map[string]interface{}{"monster": "Review Focus Caster", "action": entry.Name, "apply_damage": apply, "target_ids": targets}})
		if code != 200 {
			t.Fatalf("save effect: %d %v", code, j)
		}
		return fmt.Sprint(j["monster_action_result"])
	}
	if text := play(false); !strings.Contains(text, "saving throw") {
		t.Fatalf("control: preview did not roll its saves: %s", text)
	}
	for _, target := range targets {
		if hasCondMarker(f.camp, target, "frightened") {
			t.Errorf("preview applied condition to %d", target)
		}
	}
	if got := legendaryResistanceLeft(f.camp, f.caster); got != turnUses {
		t.Errorf("preview spent Legendary Resistance: %d -> %d", turnUses, got)
	}
	if containsStr(loadMonsterState(f.camp, f.caster).RechargeSpent, stripUsage(entry.Name)) {
		t.Errorf("preview spent recharge")
	}
	if t.Failed() {
		return
	}
	text := play(true)
	if !hasCondMarker(f.camp, f.char, "frightened") || !hasCondMarker(f.camp, pet, "frightened") {
		t.Errorf("actual effect missed targets: %s", text)
	}
	if legendaryResistanceLeft(f.camp, f.caster) != turnUses-1 || hasCondMarker(f.camp, f.caster, "frightened") {
		t.Errorf("actual failed save did not use Legendary Resistance: %s", text)
	}
	if !containsStr(loadMonsterState(f.camp, f.caster).RechargeSpent, stripUsage(entry.Name)) {
		t.Error("actual effect did not spend recharge")
	}
}

func TestPostgresLegendaryPreviewKeepsUseAndWindow(t *testing.T) {
	entry := srdMonsterEntry{Name: "Legendary Force (1/Day)", Text: "The target takes 2 (1d4) Force damage."}
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) { m.Legendary = append(m.Legendary, entry) })
	setLastTurnEnded(f.camp, "1:1", f.char)
	target := monsterIDsByKeyOrName(f.camp, "Opponent")[0]
	hp := monsterHPNow(f.camp, target)
	key, _ := monsterDayUse(entry, "")
	play := func(apply bool) (int, map[string]interface{}) {
		t.Helper()
		return pgCall(t, "POST", "/api/gm/legendary-action", f.gm, map[string]interface{}{"campaign_id": f.camp, "combatant_id": f.caster, "action": entry.Name, "target_ids": []int{target}, "apply_damage": apply})
	}
	if code, j := play(false); code != 200 {
		t.Fatalf("legendary preview: %d %v", code, j)
	}
	if state := loadMonsterState(f.camp, f.caster); state.LAUsed != 0 || state.LAWindow != "" || len(state.LAOnce) != 0 {
		t.Errorf("preview used Legendary Action or its window: %+v", state)
	}
	if monsterHPNow(f.camp, target) != hp || loadDayUses(f.camp, f.caster)[key] != 0 {
		t.Error("preview changed HP or daily use")
	}
	if t.Failed() {
		return
	}
	if code, j := play(true); code != 200 || hp-monsterHPNow(f.camp, target) < 1 || hp-monsterHPNow(f.camp, target) > 4 {
		t.Fatalf("actual action in previewed window: %d %v", code, j)
	}
	if state := loadMonsterState(f.camp, f.caster); state.LAUsed != 1 || state.LAWindow != "1:1" || loadDayUses(f.camp, f.caster)[key] != 1 {
		t.Fatalf("actual action did not count uses: %+v", state)
	}
	if code, j := play(true); code != 400 || j["error"] != "one_at_a_time" {
		t.Fatalf("repeat in same window: %d %v", code, j)
	}
}
