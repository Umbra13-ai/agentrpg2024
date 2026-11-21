package main

import (
	"fmt"
	"strings"
	"testing"
)

var directEffectProbe = srdMonsterEntry{
	Name: "Force Test (1/Day)",
	Text: "Each target takes 2 (1d4) Force damage.",
}

func TestPostgresMonsterDirectDamagePreviewAndCreatureTargets(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) { m.Actions = append(m.Actions, directEffectProbe) })
	m := monsterIDsByKeyOrName(f.camp, "Opponent")[0]
	code, j := pgCall(t, "POST", "/api/gm/companion", f.gm, map[string]interface{}{"campaign_id": f.camp, "character_id": f.char, "monster_key": f.key, "name": "Force Test Pet"})
	if code != 200 {
		t.Fatalf("create companion: %d %v", code, j)
	}
	petID := asInt(j["companion"].(map[string]interface{})["id"])
	hp := func() (int, int, int) {
		var hero int
		if err := db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, f.char).Scan(&hero); err != nil {
			t.Fatal(err)
		}
		pet, ok := loadCompanion(petID)
		if !ok {
			t.Fatal("missing test companion")
		}
		return hero, monsterHPNow(f.camp, m), pet.HP
	}
	key, _ := monsterDayUse(directEffectProbe, "")
	if key == "" {
		t.Fatal("control: day limit did not parse")
	}
	play := func(apply bool) string {
		t.Helper()
		code, j := pgCall(t, "POST", "/api/gm/narrate", f.gm, map[string]interface{}{
			"campaign_id": f.camp, "narration": "An invented damage-only effect.",
			"monster_action": map[string]interface{}{"monster": "Review Focus Caster", "action": directEffectProbe.Name, "target_ids": []int{m, f.char, companionIDBase + petID}, "apply_damage": apply},
		})
		if code != 200 {
			t.Fatalf("direct damage: %d %v", code, j)
		}
		return fmt.Sprint(j["monster_action_result"])
	}
	beforeHero, beforeMonster, beforePet := hp()
	if text := play(false); !strings.Contains(text, "Force Test") {
		t.Fatalf("preview did not reach the action: %s", text)
	}
	afterHero, afterMonster, afterPet := hp()
	if afterHero != beforeHero || afterMonster != beforeMonster || afterPet != beforePet || loadDayUses(f.camp, f.caster)[key] != 0 {
		t.Errorf("preview mutated state: HP (%d,%d,%d) -> (%d,%d,%d)", beforeHero, beforeMonster, beforePet, afterHero, afterMonster, afterPet)
	}
	text := play(true)
	hero, monster, pet := hp()
	damage := afterHero - hero
	if damage < 1 || damage > 4 || afterMonster-monster != damage || afterPet-pet != damage {
		t.Errorf("one shared roll must damage each target: %d %d %d. %s", damage, afterMonster-monster, afterPet-pet, text)
	}
	if loadDayUses(f.camp, f.caster)[key] != 1 {
		t.Errorf("executed action did not spend a use: %s", text)
	}
}

func TestPostgresMonsterDirectDamagePausesBeforeNextTarget(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) { m.Actions = append(m.Actions, directEffectProbe) })
	f.cast(t, f.char)
	pauseFailingConcentration(t, f)
	other := monsterIDsByKeyOrName(f.camp, "Opponent")[0]
	beforeCaster, beforeOther := monsterHPNow(f.camp, f.caster), monsterHPNow(f.camp, other)
	code, j := pgCall(t, "POST", "/api/gm/narrate", f.gm, map[string]interface{}{"campaign_id": f.camp, "narration": "An invented damage-only effect.", "monster_action": map[string]interface{}{"monster": "Review Focus Caster", "action": directEffectProbe.Name, "target_ids": []int{f.caster, other}}})
	if code != 200 {
		t.Fatalf("damage effect: %d %v", code, j)
	}
	after := monsterHPNow(f.camp, f.caster)
	damage := beforeCaster - after
	if damage < 1 || damage > 4 || monsterConcentrationPending(f.camp, f.caster) == "" {
		t.Fatalf("control: effect did not create a pending save: %v", j)
	}
	if monsterHPNow(f.camp, other) != beforeOther {
		t.Fatal("second target changed before the pending Concentration decision")
	}
	work := pauseWaitingWork(t, f.camp)
	if code, j = f.decide(t, "end"); code != 200 {
		t.Fatalf("end cast: %d %v", code, j)
	}
	pauseResume(t, f, work)
	pauseResume(t, f, work)
	if monsterHPNow(f.camp, f.caster) != after || monsterHPNow(f.camp, other) != beforeOther-damage {
		t.Fatal("resuming or retrying rerolled damage, replayed a target, or lost the second target")
	}
	key, _ := monsterDayUse(directEffectProbe, "")
	if loadDayUses(f.camp, f.caster)[key] != 1 {
		t.Fatal("the resumed effect must spend one use total")
	}
}

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
