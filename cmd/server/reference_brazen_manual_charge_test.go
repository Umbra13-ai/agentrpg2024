package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestPostgresFreshBrazenGorgonManualChargeWorkflow(t *testing.T) {
	original := suppliedFreshMonster(t, "brazen-gorgon.json", "Brazen Gorgon", "9")
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
		*m = original
		m.Name = "Review Focus Caster"
	})
	const victimName = "Charge grapple control"
	victim := referenceSaveTarget(t, f, victimName, -10, "", "", func(m *srdMonster2024) {
		dex := m.Abilities["dex"]
		dex.Save = -10
		m.Abilities["dex"] = dex
	})
	call := func(method, path, token string, body interface{}) map[string]interface{} {
		t.Helper()
		code, reply := pgCall(t, method, path, token, body)
		if code != 200 {
			t.Fatalf("%s %s: %d %v", method, path, code, reply)
		}
		return reply
	}
	mapPath := fmt.Sprintf("/api/campaigns/%d/battle-map", f.camp)
	revision := ""
	terrain := []string{"............", "............", "............"}
	mapBody := func(gorgonX, victimX, shared int) map[string]interface{} {
		m := encounterMap{Grid: terrain, Tokens: []mapToken{
			{ID: f.caster, Name: "Review Focus Caster", Position: mapSquare{gorgonX, 0}, Size: 2},
			{ID: victim, Name: victimName, Position: mapSquare{victimX, 0}, Size: 1, ShareSpaceWith: shared},
			{ID: f.char, Name: "Hero observer", Position: mapSquare{8, 0}, Size: 1},
		}}
		return map[string]interface{}{"map": m, "confirmation": revision, "forced_movement": true}
	}
	publish := func(gorgonX, victimX, shared int) {
		t.Helper()
		out := call("PUT", mapPath, f.gm, mapBody(gorgonX, victimX, shared))
		revision, _ = out["revision"].(string)
		if revision == "" {
			t.Fatal("publish did not return a map revision")
		}
	}
	publish(0, 3, 0)

	// The GM has adjudicated the path. Moving the Large token ten feet
	// enters the victim's space. Roll against the supplied DC and record the
	// consequences through the existing game-state owners.
	dex, _, _ := saveModifier(f.camp, victim, "dex")
	if dex != -10 {
		t.Fatalf("the fixture must always fail its save, got %+d", dex)
	}
	roll := func(dice, label string) int {
		return asInt(call("POST", "/api/gm/roll", f.gm, map[string]interface{}{
			"campaign_id": f.camp, "dice": dice, "label": label, "secret": true,
		})["total"])
	}
	if total := roll(fmt.Sprintf("1d20%+d", dex), "Charge Dexterity save"); total >= 16 {
		t.Fatalf("fixture unexpectedly saved against DC 16: %d", total)
	}
	piercing := roll("2d8+4", "Charge piercing damage")
	fire := roll("3d8", "Charge fire damage")
	if piercing < 6 || piercing > 20 || fire < 3 || fire > 24 {
		t.Fatalf("damage roll controls: %d %d", piercing, fire)
	}
	for _, packet := range []struct {
		kind   string
		amount int
	}{{"piercing", piercing}, {"fire", fire}} {
		call("POST", "/api/gm/adjust-monster", f.gm, map[string]interface{}{
			"campaign_id": f.camp, "target_id": victim, "damage": packet.amount,
			"damage_type": packet.kind, "apply_resistances": true,
		})
	}
	if hp := monsterHPNow(f.camp, victim); hp != 500-piercing-fire {
		t.Fatalf("recorded damage result: %d HP", hp)
	}
	const effectSource = "Review Focus Caster (Smelting Charge)"
	condition := func(name string, linked bool) map[string]interface{} {
		return map[string]interface{}{"campaign_id": f.camp, "target_id": victim, "condition": name,
			"source": effectSource, "source_id": f.caster, "escape_dc": 14, "ends_with_grapple": linked}
	}
	if code, j := pgCall(t, "POST", "/api/gm/conditions", f.gm, condition("restrained", true)); code != 400 || j["error"] != "grapple_source_required" {
		t.Fatalf("linked restraint was accepted without a grapple: %d %v", code, j)
	}
	call("POST", "/api/gm/conditions", f.gm, condition("grappled", false))
	call("POST", "/api/gm/conditions", f.gm, condition("restrained", true))
	var grapple, restraint bool
	for _, record := range loadConditionRecords(f.camp, victim) {
		if record.Condition == "grappled" && record.SourceID == f.caster && len(record.Escapes) == 2 && record.Escapes[0].DC == 14 {
			grapple = true
		}
		if record.Condition == "restrained" && record.SourceID == f.caster && record.Notes == notesEndsWithGrapple {
			restraint = true
		}
	}
	if !grapple || !restraint {
		t.Fatal("manual workflow did not persist the sourced escape and linked restraint")
	}

	if code, reply := pgCall(t, "PUT", mapPath, f.gm, mapBody(2, 3, 0)); code != 400 || reply["error"] != "invalid_map" {
		t.Fatalf("ordinary overlap was accepted: %d %v", code, reply)
	}
	publish(2, 3, f.caster)
	publish(4, 5, f.caster) // GM moves the grappled token along with its grappler.
	battle, ok := activeEncounterMap(f.camp)
	got, placed := battle.token(victim)
	if !ok || !placed || got.Position != (mapSquare{5, 0}) || got.ShareSpaceWith != f.caster {
		t.Fatalf("shared position did not persist: %+v", got)
	}
	playerMap := call("GET", fmt.Sprintf("%s?character_id=%d", mapPath, f.char), f.player, nil)
	if strings.Contains(fmt.Sprint(playerMap), "share_space_with") {
		t.Fatal("the GM's token attachment appeared in a player map")
	}
	call("POST", "/api/gm/monster-features", f.gm, map[string]interface{}{
		"campaign_id": f.camp, "combatant_id": f.caster, "spend_recharge": "Smelting Charge",
	})
	if uses := loadMonsterState(f.camp, f.caster).RechargeSpent; len(uses) != 1 || uses[0] != "Smelting Charge" {
		t.Fatalf("charge did not consume exactly one Recharge use: %v", uses)
	}
	call("DELETE", "/api/gm/conditions", f.gm, map[string]interface{}{
		"campaign_id": f.camp, "target_id": victim, "condition": "grappled",
	})
	if hasCondMarker(f.camp, victim, "grappled") || hasCondMarker(f.camp, victim, "restrained") {
		t.Fatal("ending the charge's grapple did not release its linked restraint")
	}
	publish(4, 3, 0)
}
