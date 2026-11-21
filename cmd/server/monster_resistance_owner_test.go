package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestPostgresMonsterStateCannotRestoreSpentResistance(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	f.damage(t)
	old := loadMonsterState(f.camp, f.caster)
	if code, j := f.decide(t, "legendary_resistance"); code != 200 {
		t.Fatalf("decision: %d %v", code, j)
	}
	if legendaryResistanceLeft(f.camp, f.caster) != 2 {
		t.Fatal("control: decision did not spend one use")
	}
	// The general state writer may hold an older snapshot while a concentration
	// decision commits. Saving an unrelated aura setting must not restore a use.
	old.AurasOff = true
	saveMonsterState(f.camp, f.caster, old)
	if !loadMonsterState(f.camp, f.caster).AurasOff {
		t.Fatal("control: general state save did not execute")
	}
	if got := legendaryResistanceLeft(f.camp, f.caster); got != 2 {
		t.Fatalf("stale general state restored a spent use: left=%d, want 2", got)
	}
}

// The ordinary GM command and automatic save-effect route consume the same
// counter as Concentration. Vorpal's consumer is exercised at helper level here,
// not claimed as a complete equipped-item attack route.
func TestPostgresMonsterResistanceConsumers(t *testing.T) {
	f := reviewConcentrationSetup(t)
	code, j := pgCall(t, "POST", "/api/gm/legendary-resistance", f.gm, map[string]interface{}{"campaign_id": f.camp, "combatant_id": f.caster})
	if code != 200 || legendaryResistanceLeft(f.camp, f.caster) != 2 {
		t.Fatalf("manual use: %d %v", code, j)
	}
	m := compendiumFixture()
	m.Name = "Invented Save Source"
	m.Actions = []srdMonsterEntry{{Name: "Glare", Text: "Constitution Saving Throw: DC 99, one creature. Failure: The target has the Blinded condition until the end of its next turn. Success: No effect."}}
	code, j = pgCall(t, "POST", "/api/gm/compendium/monsters", f.gm, map[string]interface{}{"source_note": "invented counter consumer fixture", "monsters": []srdMonster2024{m}})
	if code != 201 {
		t.Fatalf("import: %d %v", code, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	code, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", f.camp), f.gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": m.Name, "monster_key": key}}})
	if code != 200 {
		t.Fatalf("add: %d %v", code, j)
	}
	code, j = pgCall(t, "POST", "/api/gm/narrate", f.gm, map[string]interface{}{"campaign_id": f.camp, "narration": "A save effect.", "monster_action": map[string]interface{}{"monster": m.Name, "action": "Glare", "target_ids": []int{f.caster}}})
	if code != 200 || legendaryResistanceLeft(f.camp, f.caster) != 1 || hasCondMarker(f.camp, f.caster, "blinded") {
		t.Fatalf("automatic save use: %d %v", code, j)
	}
	_, killed, text := vorpalBehead(f.camp, f.caster, f.key, "Review Focus Caster")
	if killed || legendaryResistanceLeft(f.camp, f.caster) != 0 || !strings.Contains(text, "spends a use") {
		t.Fatalf("item consumer helper did not spend the last use: %s", text)
	}
	code, j = pgCall(t, "POST", "/api/gm/legendary-resistance", f.gm, map[string]interface{}{"campaign_id": f.camp, "combatant_id": f.caster})
	if code != 400 || j["error"] != "no_uses_left" || legendaryResistanceLeft(f.camp, f.caster) != 0 {
		t.Fatalf("exhaustion: %d %v", code, j)
	}
}
