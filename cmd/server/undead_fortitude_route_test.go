package main

import (
	"fmt"
	"strings"
	"testing"
)

// SRD 5.2.1 Zombie, Undead Fortitude: "If damage reduces the zombie to 0 Hit Points, it makes a Constitution saving throw (DC 5 plus
// the damage taken) unless the damage is Radiant or from a Critical Hit. On a successful save, the zombie drops to 1 Hit Point instead."
// Through a character's weapon attack (POST /api/action) and a monster's attack on it (POST /api/gm/narrate: the damage owner every
// spell and companion attack also uses): damage that drops it to 0 calls for the save with its DC, a success leaves it at 1, a failure
// at 0; Radiant damage and a Critical Hit call for none; damage that leaves it above 0 calls for none.
func TestPostgresUndeadFortitudeRoutes(t *testing.T) {
	testPostgres(t)
	gm, player, camp, charID, _, _ := companionReactionFixture(t)
	cp := fmt.Sprintf("/api/campaigns/%d/combat", camp)
	if c, j := pgCall(t, "POST", cp+"/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Shambler", "monster_key": "zombie", "initiative": -900, "ac": 1}}}); c != 200 {
		t.Fatalf("add: %d %v", c, j)
	}
	z := monsterIDsByKeyOrName(camp, "Shambler")[0]

	// a monster's hit (narrate at the zombie: hurtMonster), at 1 Hit Point: the save is called with DC 5 + the damage; the result fits
	seenSave, seenSuccess := false, false
	for n := 0; n < 30 && !seenSuccess; n++ {
		setMonsterHP(camp, z, 1)
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Opponent", "action": "Bite", "target": "Shambler"}})
		res := fmt.Sprint(r["monster_action_result"])
		if !strings.Contains(res, "HIT") || strings.Contains(res, "CRITICAL") {
			continue
		}
		hp := monsterEntryVitals(camp, z).HP
		if !strings.Contains(res, "Undead Fortitude") {
			t.Fatalf("a hit that dropped the zombie to 0 called for no save (HP %d): %s", hp, res)
		}
		seenSave = true
		if strings.Contains(res, "drops to 1 Hit Point instead") {
			seenSuccess = true
			if hp != 1 {
				t.Errorf("a successful save left %d Hit Points: %s", hp, res)
			}
		} else if hp != 0 {
			t.Errorf("a failed save left %d Hit Points: %s", hp, res)
		}
	}
	if !seenSave || !seenSuccess {
		t.Errorf("no successful Undead Fortitude save in 30 tries (save seen %v)", seenSave)
	}

	// the damage owner directly: Radiant and a Critical Hit call for no save; damage that leaves it standing calls for none
	setMonsterHP(camp, z, 1)
	if res, _ := damageMonster(camp, z, 5, "radiant"); strings.Contains(res, "Undead Fortitude") || monsterEntryVitals(camp, z).HP != 0 {
		t.Errorf("Radiant: HP %d: %s", monsterEntryVitals(camp, z).HP, res)
	}
	setMonsterHP(camp, z, 1)
	if res, _ := damageMonster(camp, z, 5, "slashing", true); strings.Contains(res, "Undead Fortitude") || monsterEntryVitals(camp, z).HP != 0 {
		t.Errorf("a Critical Hit: HP %d: %s", monsterEntryVitals(camp, z).HP, res)
	}
	setMonsterHP(camp, z, 10) // its maximum is 15
	if res, _ := damageMonster(camp, z, 5, "slashing"); strings.Contains(res, "Undead Fortitude") || monsterEntryVitals(camp, z).HP != 5 {
		t.Errorf("damage that leaves it standing: HP %d: %s", monsterEntryVitals(camp, z).HP, res)
	}

	// a character's weapon attack (attack_target.go) that drops it to 0 calls for the save
	var equipped string
	db.QueryRow(`SELECT COALESCE(equipped_main_hand,'') FROM characters WHERE id=$1`, charID).Scan(&equipped)
	if equipped == "" {
		pgCall(t, "POST", "/api/characters/equip-weapon", player, map[string]interface{}{"character_id": charID, "weapon": "dagger", "slot": "main_hand"})
		equipped = "dagger"
	}
	called := false
	for n := 0; n < 40 && !called; n++ {
		setMonsterHP(camp, z, 1)
		pgExec(t, `UPDATE characters SET action_used=false,attacks_remaining=NULL,conditions='[]',wild_shape_form='',weapon_interaction_used=false WHERE id=$1`, charID)
		_, r := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"action": "attack", "target_id": z, "description": "I attack with my " + equipped})
		am, _ := r["attack"].(map[string]interface{})
		if am == nil || am["hit"] != true || strings.Contains(strings.ToUpper(fmt.Sprint(r)), "CRITICAL") {
			continue
		}
		if !strings.Contains(fmt.Sprint(r), "Undead Fortitude") {
			t.Fatalf("a character's hit that dropped the zombie to 0 called for no save: %v", r)
		}
		called = true
	}
	if !called {
		t.Error("the character's attack never hit the zombie")
	}
}
