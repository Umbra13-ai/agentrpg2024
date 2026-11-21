package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestPostgresFreshBrazenGorgonNamedAbilities(t *testing.T) {
	original := suppliedFreshMonster(t, "brazen-gorgon.json", "Brazen Gorgon", "9")
	fixture := func(t *testing.T) reviewConcentrationCase {
		t.Helper()
		return reviewConcentrationSetup(t, func(m *srdMonster2024) {
			*m = original
			m.Name = "Review Focus Caster"
		})
	}
	gmTurn := func(t *testing.T, f reviewConcentrationCase, id int) {
		t.Helper()
		if _, err := changeFight(f.camp, func(combat *fight) error {
			i, _ := combat.entry(id)
			if i < 0 {
				return fmt.Errorf("fixture initiative missing for %d", id)
			}
			combat.Index = i
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("flame aura at source turn end", func(t *testing.T) {
		f := fixture(t)
		near := referenceSaveTarget(t, f, "Adjacent monster", -10, "", "")
		far := referenceSaveTarget(t, f, "Distant monster", -10, "", "")
		companions := campaignCompanions(f.camp)
		if len(companions) != 1 {
			t.Fatalf("fixture needs one companion: %v", companions)
		}
		pet := companions[0]
		m := encounterMap{Grid: []string{"......", "......"}, Tokens: []mapToken{
			{ID: f.caster, Name: "Review Focus Caster", Position: mapSquare{0, 0}, Size: 1},
			{ID: near, Name: "Adjacent monster", Position: mapSquare{1, 0}, Size: 1},
			{ID: far, Name: "Distant monster", Position: mapSquare{4, 0}, Size: 1},
			{ID: f.char, Name: "Hero", Position: mapSquare{0, 1}, Size: 1},
			{ID: pet.CombatID, Name: "Adjacent companion", Position: mapSquare{1, 1}, Size: 1},
		}}
		path := fmt.Sprintf("/api/campaigns/%d/battle-map", f.camp)
		if code, reply := pgCall(t, "PUT", path, f.gm, map[string]interface{}{"map": m, "forced_movement": true}); code != 200 {
			t.Fatalf("place aura controls: %d %v", code, reply)
		}
		if c := applyDamageResistance(f.char, 50, "fire").FinalDamage; c != 50 {
			t.Fatalf("unmitigated fire control is %d", c)
		}
		hp := ironGolemHeroHP(t, f)
		gmTurn(t, f, f.caster)
		if monsterHPNow(f.camp, near) != 500 {
			t.Fatal("aura applied before the source turn ended")
		}
		path = fmt.Sprintf("/api/campaigns/%d/combat/next", f.camp)
		code, reply := pgCall(t, "POST", path, f.gm, map[string]interface{}{})
		if code != 200 {
			t.Fatalf("end source turn: %d %v", code, reply)
		}
		loss := hp - ironGolemHeroHP(t, f)
		if loss < 3 || loss > 24 {
			t.Errorf("adjacent character took %d Fire damage, expected an actual 3d8 result", loss)
		}
		if got := 500 - monsterHPNow(f.camp, near); got != loss {
			t.Errorf("adjacent monster took %d Fire damage, want the shared roll %d", got, loss)
		}
		after, ok := loadCompanion(pet.ID)
		wantPetDamage, how := adjustMonsterDamage(pet.Form, "fire", loss)
		if wantPetDamage <= 0 || !ok || pet.HP-after.HP != min(wantPetDamage, pet.HP) {
			t.Errorf("adjacent companion took %d Fire damage, want %d after %q, capped at its HP", pet.HP-after.HP, wantPetDamage, how)
		}
		if monsterHPNow(f.camp, far) != 500 {
			t.Error("aura affected a monster outside its range")
		}
	})
	t.Run("illumination changes attack visibility locally", func(t *testing.T) {
		f := fixture(t)
		near := referenceSaveTarget(t, f, "Illuminated target", -10, "", "")
		far := referenceSaveTarget(t, f, "Darkness target", -10, "", "")
		ironGolemMap(t, f, map[int]int{near: 2, f.char: 4, far: 8})
		pgExec(t, `UPDATE characters SET darkvision_range=0,blindsight_range=0,truesight_range=0 WHERE id=$1`, f.char)
		code, reply := pgCall(t, "POST", "/api/gm/set-lighting", f.gm, map[string]interface{}{"campaign_id": f.camp, "lighting": "darkness"})
		if code != 200 || canSeeInLighting(f.char, getCampaignLighting(f.camp)) != "blind" {
			t.Fatalf("baseline darkness control: %d %v", code, reply)
		}
		gmTurn(t, f, f.char)
		var held string
		if err := db.QueryRow(`SELECT COALESCE(equipped_main_hand,'') FROM characters WHERE id=$1`, f.char).Scan(&held); err != nil {
			t.Fatal(err)
		}
		if !strings.EqualFold(held, "shortbow") {
			if held != "" {
				drop := fmt.Sprintf("/api/characters/%d/equipment/drop", f.char)
				if code, j := pgCall(t, "POST", drop, f.player, map[string]interface{}{"item": held}); code != 200 {
					t.Fatalf("free the fixture's main hand: %d %v", code, j)
				}
			}
			code, reply = pgCall(t, "POST", "/api/characters/equip-weapon", f.player, map[string]interface{}{
				"character_id": f.char, "weapon": "shortbow", "slot": "main_hand",
			})
			if code != 200 {
				t.Fatalf("equip the fixture shortbow: %d %v", code, reply)
			}
		}
		attack := func(target int) string {
			t.Helper()
			pgExec(t, `UPDATE characters SET action_used=false,attacks_remaining=NULL WHERE id=$1`, f.char)
			code, j := pgCall(t, "POST", "/api/action", f.player, map[string]interface{}{"character_id": f.char,
				"action": "attack", "target_id": target, "description": "I attack with my shortbow",
			})
			if code != 200 {
				t.Fatalf("player attack: %d %v", code, j)
			}
			return fmt.Sprint(j)
		}
		if result := attack(far); !strings.Contains(result, "can't see in this light") {
			t.Errorf("unlit target did not show the darkness penalty: %s", result)
		}
		if result := attack(near); strings.Contains(result, "can't see in this light") {
			t.Errorf("Gorgon's bright area did not remove the darkness penalty: %s", result)
		}
	})
	t.Run("gore and two-attack multiattack", func(t *testing.T) {
		f := fixture(t)
		ironGolemMap(t, f, map[int]int{f.char: 1})
		pgExec(t, `UPDATE characters SET hp=5000,max_hp=5000,ac=1 WHERE id=$1`, f.char)
		for _, rule := range []struct {
			action string
			rolls  int
		}{{"Gore", 1}, {"Multiattack", 2}} {
			witnessedHit := false
			for attempt := 0; attempt < 6 && !witnessedHit; attempt++ {
				hp := ironGolemHeroHP(t, f)
				j := ironGolemAction(t, f, rule.action, map[string]interface{}{"target": fmt.Sprint(f.char)})
				text := fmt.Sprint(j["monster_action_result"])
				receipt, ok := j["monster_attack"].(map[string]interface{})
				if !ok || asInt(receipt["attacks"]) != rule.rolls || strings.Count(text, "Gore:") != rule.rolls {
					t.Fatalf("%s: expected %d Gore rolls: %v", rule.action, rule.rolls, j)
				}
				if hits := asInt(receipt["hits"]); hits > 0 {
					witnessedHit = true
					loss := hp - ironGolemHeroHP(t, f)
					if !strings.Contains(strings.ToLower(text), "piercing damage") || !strings.Contains(strings.ToLower(text), "fire damage") ||
						loss != asInt(receipt["damage"]) || loss < 9*hits || loss > 64*hits {
						t.Fatalf("%s: damage components or actual HP mismatch: %s (%d)", rule.action, text, loss)
					}
				}
			}
			if !witnessedHit {
				t.Errorf("%s: no hit within six independent route uses", rule.action)
			}
		}
	})
	t.Run("smelting charge requires movement eligibility", func(t *testing.T) {
		f := fixture(t)
		target := referenceSaveTarget(t, f, "No shared movement path", -10, "", "")
		ironGolemMap(t, f, map[int]int{target: 12})
		for _, confirm := range []bool{false, true} {
			j := ironGolemAction(t, f, "Smelting Charge (Recharge 5–6)", map[string]interface{}{
				"target_ids": []int{target}, "confirm_targeting": confirm,
			})
			text := fmt.Sprint(j["monster_action_result"])
			if !strings.Contains(text, "Nothing was rolled") || !strings.Contains(text, "required movement through creature spaces") ||
				strings.Contains(text, "Dexterity saving throw") || monsterHPNow(f.camp, target) != 500 ||
				hasCondMarker(f.camp, target, "grappled") || len(loadMonsterState(f.camp, f.caster).RechargeSpent) != 0 {
				t.Errorf("charge did not preserve damage, conditions and Recharge pending path adjudication (confirm %t): %s", confirm, text)
			}
		}
	})
}
