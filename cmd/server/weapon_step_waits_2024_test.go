package main

import (
	"fmt"
	"testing"
	"time"
)

// Playtest S-GM-16: auto-equip and "hold a weapon" are two setup steps for one thing. While the auto-equip question is open the weapon
// step waits for it (waits_for, no request to send); once it is answered the step is done, or, with the hands still empty, gives its request.
func TestPostgresWeaponStepWaitsForAutoEquip(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "pg weapon step")
	_, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Weapon %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	// a fresh character: auto-equip not yet answered, the hands empty
	pgExec(t, `UPDATE characters SET auto_equip_answered = false, equipped_main_hand = NULL, equipped_off_hand = NULL WHERE id = $1`, char)
	step := func(key string) map[string]interface{} {
		t.Helper()
		_, setup := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/setup", char), tok, nil)
		steps, _ := setup["steps"].([]interface{})
		for _, s := range steps {
			if m, ok := s.(map[string]interface{}); ok && m["key"] == key {
				return m
			}
		}
		return nil
	}
	auto, weapon := step("auto_equip"), step("weapon_in_hand")
	if auto == nil || auto["done"] == true || auto["request"] == nil {
		t.Skipf("this character has nothing for auto-equip to do: %v", auto)
	}
	if weapon == nil || weapon["waits_for"] != "auto_equip" || weapon["request"] != nil {
		t.Fatalf("while auto-equip is open the weapon step waits for it, with no request: %v", weapon)
	}
	// answer auto-equip with its own suggested request
	rq := auto["request"].(map[string]interface{})
	if c, j := pgCall(t, "POST", "/api/characters/auto-equip", tok, rq["body"]); c != 200 {
		t.Fatalf("auto-equip: %d %v", c, j)
	}
	weapon = step("weapon_in_hand")
	if weapon != nil && weapon["waits_for"] != nil {
		t.Errorf("the weapon step still waits after auto-equip was answered: %v", weapon)
	}
	if weapon != nil && weapon["done"] != true && weapon["request"] != nil {
		var main string
		db.QueryRow(`SELECT COALESCE(equipped_main_hand,'') FROM characters WHERE id = $1`, char).Scan(&main)
		if main != "" {
			t.Errorf("the weapon step asks for a draw with %q already in the main hand: %v", main, weapon)
		}
	}
}
