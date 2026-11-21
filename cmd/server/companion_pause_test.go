package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestPostgresCompanionMultiattackPausesAtDamage(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(fmt.Sprintf("attacks_%d", count), func(t *testing.T) {
			f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
				m.AC = 1
				m.Actions = append(m.Actions,
					srdMonsterEntry{Name: "Slam", Text: "Melee Attack Roll: Automatic hit, reach 5 ft. Hit: 4 Force damage plus 2 Radiant damage, and the target has the Frightened condition for 1 minute."},
					srdMonsterEntry{Name: "Multiattack", Text: "The creature makes two Slam attacks."})
			})
			held := pauseAuraSource(t, f, "Companion held target")
			pgExec(t, `UPDATE characters SET species='Human' WHERE id=$1`, held)
			f.cast(t, held)
			pauseFailingConcentration(t, f)
			code, j := pgCall(t, "POST", "/api/gm/companion", f.gm, map[string]interface{}{"campaign_id": f.camp, "character_id": f.char, "monster_key": f.key, "name": "Sequence Pet"})
			if code != 200 {
				t.Fatalf("pet: %d %v", code, j)
			}
			petID := int(j["companion"].(map[string]interface{})["id"].(float64))
			if count == 2 {
				// GM pets deliberately have one attack. This component fixture models an
				// existing summon plan with Count=2; it does not change pet creation policy.
				pgExec(t, `UPDATE companions SET kind='summon',attacks=jsonb_set(attacks,'{0,count}',to_jsonb(2)) WHERE id=$1`, petID)
			}

			_, err := changeFight(f.camp, func(fight *fight) error {
				for i, e := range fight.Order {
					if asInt(e["id"]) == f.char {
						fight.Index = i
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			hp := monsterHPNow(f.camp, f.caster)
			for i := 0; i < 40; i++ {
				pgExec(t, `UPDATE companions SET last_acted_round=0,last_acted_lobby=0 WHERE id=$1`, petID)
				code, j = pgCall(t, "POST", "/api/action", f.player, map[string]interface{}{"companion_id": petID, "action": "attack", "target_id": f.caster})
				if code != 200 {
					t.Fatalf("pet attack: %d %v", code, j)
				}
				if monsterConcentrationPending(f.camp, f.caster) != "" {
					break
				}
			}
			if monsterConcentrationPending(f.camp, f.caster) == "" {
				t.Fatal("no companion hit in 40 attempts")
			}
			if monsterHPNow(f.camp, f.caster) != hp-4 || hasCondMarker(f.camp, f.caster, "frightened") {
				t.Fatalf("companion continued beyond pending damage: hp=%d %v", monsterHPNow(f.camp, f.caster), j)
			}
			work := pauseWaitingWork(t, f.camp)
			pet, _ := loadCompanion(petID)
			if pet.lastRound != currentRound(f.camp) || pet.lastLobby != f.camp {
				t.Fatal("initial companion command did not spend its turn")
			}
			if code, j = f.decide(t, "end"); code != 200 {
				t.Fatalf("end: %d %v", code, j)
			}
			pauseResume(t, f, work)
			if monsterHPNow(f.camp, f.caster) != hp-count*6 || !hasCondMarker(f.camp, f.caster, "frightened") {
				t.Fatalf("remaining companion hit/rider was lost or repeated: hp=%d", monsterHPNow(f.camp, f.caster))
			}
			pauseResume(t, f, work)
			again, _ := loadCompanion(petID)
			if monsterHPNow(f.camp, f.caster) != hp-count*6 || again.lastRound != pet.lastRound || again.lastLobby != pet.lastLobby {
				t.Fatal("duplicate companion resume changed damage or its spent turn")
			}

		})
	}
}

func TestPostgresWildShapePlanResumesWithoutExtraAttacks(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) { m.AC = 1 })
	held := pauseAuraSource(t, f, "Form held target")
	pgExec(t, `UPDATE characters SET species='Human' WHERE id=$1`, held)
	f.cast(t, held)
	pauseFailingConcentration(t, f)
	// Component fixture: the transformation route is covered separately. This
	// drives a real attack request while the character is in the SRD bear form.
	pgExec(t, `UPDATE characters SET class='druid',level=20,wild_shape_form='brown-bear',subclass_choices='{}',action_used=false,attacks_remaining=NULL WHERE id=$1`, f.char)
	_, err := changeFight(f.camp, func(fight *fight) error {
		for i, e := range fight.Order {
			if asInt(e["id"]) == f.char {
				fight.Index = i
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var initial string
	for i := 0; i < 40; i++ {
		pgExec(t, `UPDATE characters SET action_used=false,attacks_remaining=NULL WHERE id=$1`, f.char)
		code, j := pgCall(t, "POST", "/api/action", f.player, map[string]interface{}{"character_id": f.char, "action": "attack", "target_id": f.caster, "description": "I attack in my bear form"})
		if code != 200 {
			t.Fatalf("form attack: %d %v", code, j)
		}
		initial = fmt.Sprint(j["result"])
		if monsterConcentrationPending(f.camp, f.caster) != "" {
			break
		}
	}
	if monsterConcentrationPending(f.camp, f.caster) == "" {
		t.Fatal("no form hit in 40 attempts")
	}
	work := pauseWaitingWork(t, f.camp)
	if code, j := f.decide(t, "end"); code != 200 {
		t.Fatalf("end: %d %v", code, j)
	}
	remaining := fmt.Sprint(pauseResume(t, f, work))
	all := initial + remaining
	// SRD 5.2.1 Brown Bear: one Bite and one Claw, regardless of misses.
	if strings.Count(all, "Bite:") != 1 || strings.Count(all, "Claw:") != 1 {
		t.Fatalf("the resumed form plan duplicated or dropped an attack: %s", all)
	}
	hp := monsterHPNow(f.camp, f.caster)
	pauseResume(t, f, work)
	if monsterHPNow(f.camp, f.caster) != hp {
		t.Fatal("completed form plan replayed")
	}
}
