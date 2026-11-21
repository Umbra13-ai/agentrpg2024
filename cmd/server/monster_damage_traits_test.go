package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestPostgresDamageTriggeredTraitUsesDamageTypeAndImmunity(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
		m.DmgImmune = "Thunder"
		m.Resistances = ""
		m.Traits = []srdMonsterEntry{{Name: "Thunder Daze", Text: "If the creature takes Thunder damage, it has Disadvantage on attack rolls until the end of its next turn."}}
	})
	adjust := func(kind string, mitigate bool) {
		t.Helper()
		code, payload := pgCall(t, "POST", "/api/gm/adjust-monster", f.gm, map[string]interface{}{
			"campaign_id": f.camp, "target_id": f.caster, "damage": 12, "damage_type": kind, "apply_resistances": mitigate,
		})
		if code != 200 {
			t.Fatalf("adjust: %d %v", code, payload)
		}
	}
	penalty := func() string { return damageDisadvantageForAttack(f.camp, f.caster) }
	before := monsterHPNow(f.camp, f.caster)
	adjust("force", true)
	if hp := monsterHPNow(f.camp, f.caster); hp != before-12 || penalty() != "" {
		t.Fatalf("unrelated damage: hp %d, penalty %q", hp, penalty())
	}
	adjust("thunder", true)
	if hp := monsterHPNow(f.camp, f.caster); hp != before-12 || penalty() != "" {
		t.Fatalf("immune damage: hp %d, penalty %q", hp, penalty())
	}
	adjust("thunder", false)
	if hp := monsterHPNow(f.camp, f.caster); hp != before-24 || penalty() != "Thunder Daze" {
		t.Fatalf("exact typed damage: hp %d, penalty %q", hp, penalty())
	}
}

func TestPostgresImportedCompanionDamageTriggeredTrait(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
		m.Resistances = ""
		m.DmgImmune = ""
		m.Traits = []srdMonsterEntry{{Name: "Thunder Daze", Text: "If the creature takes Thunder damage, it has Disadvantage on attack rolls until the end of its next turn."}}
	})
	code, payload := pgCall(t, "POST", "/api/gm/companion", f.gm, map[string]interface{}{
		"campaign_id": f.camp, "monster_key": f.key, "character_id": f.char, "name": "Invented Damage Trait Pet",
	})
	if code != 200 {
		t.Fatalf("create pet: %d %v", code, payload)
	}
	id := asInt(payload["companion"].(map[string]interface{})["id"])
	c, ok := loadCompanion(id)
	if !ok || c.Form != f.key {
		t.Fatalf("pet form: %+v", c)
	}
	code, payload = pgCall(t, "POST", "/api/gm/companion", f.gm, map[string]interface{}{
		"campaign_id": f.camp, "companion_id": id, "damage": 10, "damage_type": "thunder",
	})
	if code != 200 || !strings.Contains(fmt.Sprint(payload["result"]), "takes 10 thunder") {
		t.Fatalf("companion damage: %d %v", code, payload)
	}
	after, ok := loadCompanion(id)
	if !ok || after.HP != c.HP-10 || damageDisadvantageForAttack(f.camp, c.CombatID) != "Thunder Daze" {
		t.Fatalf("companion did not persist damage and its penalty: %+v", after)
	}
	code, payload = pgCall(t, "GET", fmt.Sprintf("/api/gm/notices?campaign_id=%d", f.camp), f.gm, nil)
	if code != 200 || !strings.Contains(fmt.Sprint(payload), "individual turn") {
		t.Fatalf("companion without a turn did not get an explicit timing ruling: %d %v", code, payload)
	}
}
