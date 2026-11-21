package main

import (
	"fmt"
	"testing"
)

func TestPostgresSaveEffectPausesRidersAndLaterTargets(t *testing.T) {
	for _, entry := range []string{"owner", "GM route"} {
		t.Run(entry, func(t *testing.T) {
			text := "Constitution Saving Throw: DC 99, each creature. Failure: 4 (1d1 + 3) Force damage plus 2 (1d1 + 1) Radiant damage, and the target has the Frightened condition for 1 minute."
			f := reviewConcentrationSetup(t, func(m *srdMonster2024) { m.Actions = append(m.Actions, srdMonsterEntry{Name: "Pulse", Text: text}) })
			f.cast(t, f.char)
			pauseFailingConcentration(t, f)
			s := loadMonsterState(f.camp, f.caster)
			s.LRPolicy = "manual"
			saveMonsterState(f.camp, f.caster, s)
			enemy := monsterIDsByKeyOrName(f.camp, "Opponent")[0]
			s = loadMonsterState(f.camp, enemy)
			s.LRPolicy = "manual"
			saveMonsterState(f.camp, enemy, s)
			// Fixed invented damage dice make save outcomes and both packets deterministic.
			e, ok := parseSaveEffect(text)
			if !ok || len(e.Damage) != 2 {
				t.Fatalf("fixture parse: %+v", e)
			}
			e.SourceID = enemy
			hp, other := monsterHPNow(f.camp, f.caster), monsterHPNow(f.camp, enemy)
			var lines []string
			if entry == "owner" {
				lines, _ = applySaveEffect(f.camp, "Invented pulse", e, []int{f.caster, enemy}, true)
			} else {
				code, j := pgCall(t, "POST", "/api/gm/narrate", f.gm, map[string]interface{}{"campaign_id": f.camp, "narration": "An invented pause probe.", "monster_action": map[string]interface{}{"monster": "Review Focus Caster", "action": "Pulse", "target_ids": []int{f.caster, enemy}}})
				if code != 200 {
					t.Fatalf("GM effect: %d %v", code, j)
				}
				lines = []string{fmt.Sprint(j["monster_action_result"])}
			}
			if monsterHPNow(f.camp, f.caster) != hp-4 || monsterHPNow(f.camp, enemy) != other || hasCondMarker(f.camp, f.caster, "frightened") || monsterConcentrationPending(f.camp, f.caster) == "" {
				t.Fatalf("save effect ran beyond pending damage: hp=%d other=%d lines=%v", monsterHPNow(f.camp, f.caster), monsterHPNow(f.camp, enemy), lines)
			}
			work := pauseWaitingWork(t, f.camp)
			// A later state change cannot turn the earlier failed effect save
			// into a success when the remaining damage/rider resumes.
			pgExec(t, `UPDATE custom_monsters SET definition=jsonb_set(definition,'{abilities,con,save}',to_jsonb(200)),stats=jsonb_set(stats,'{saves,con}',to_jsonb(200)) WHERE key=$1`, f.key)
			if bonus, _, _ := saveModifier(f.camp, f.caster, "con"); bonus != 200 {
				t.Fatalf("changed-state control: %d", bonus)
			}
			if code, j := f.decide(t, "end"); code != 200 {
				t.Fatalf("end: %d %v", code, j)
			}
			pauseResume(t, f, work)
			if monsterHPNow(f.camp, f.caster) != hp-6 || monsterHPNow(f.camp, enemy) != other-6 || !hasCondMarker(f.camp, f.caster, "frightened") || !hasCondMarker(f.camp, enemy, "frightened") {
				t.Fatalf("resume lost or repeated a packet, rider, or target: %d/%d %v", monsterHPNow(f.camp, f.caster), monsterHPNow(f.camp, enemy), fmt.Sprint(lines))
			}
			pauseResume(t, f, work)
			if monsterHPNow(f.camp, f.caster) != hp-6 || monsterHPNow(f.camp, enemy) != other-6 {
				t.Fatal("completed effect replayed")
			}

		})
	}
}
