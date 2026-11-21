package main

import (
	"fmt"
	"testing"
)

func TestPostgresMonsterEndAuraPausesLaterTargetsAndAuras(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
		m.Resistances, m.DmgImmune = "", ""
	})
	f.cast(t, f.char)
	pauseFailingConcentration(t, f)
	later := referenceSaveTarget(t, f, "Later aura target", -10, "", "")
	source := compendiumFixture()
	source.Name = "Invented two-aura source"
	source.Traits = []srdMonsterEntry{
		{Name: "First Aura", Text: "At the end of each of the creature's turns, each creature in a 5-foot Emanation originating from the creature takes 4 (1d1 + 3) Force damage."},
		{Name: "Later Aura", Text: "At the end of each of the creature's turns, each creature in a 5-foot Emanation originating from the creature takes 2 (1d1 + 1) Radiant damage."},
	}
	code, j := pgCall(t, "POST", "/api/gm/compendium/monsters", f.gm, map[string]interface{}{
		"source_note": "Invented aura continuation control", "monsters": []srdMonster2024{source},
	})
	if code != 201 {
		t.Fatalf("import aura source: %d %v", code, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key); err != nil {
			t.Errorf("remove aura source: %v", err)
		}
	})
	path := fmt.Sprintf("/api/campaigns/%d/combat", f.camp)
	code, j = pgCall(t, "POST", path+"/add", f.gm, map[string]interface{}{
		"combatants": []map[string]interface{}{{"name": source.Name, "monster_key": key, "initiative": 40}},
	})
	if code != 200 {
		t.Fatalf("add aura source: %d %v", code, j)
	}
	ids := monsterIDsByKeyOrName(f.camp, source.Name)
	if len(ids) != 1 {
		t.Fatalf("aura source identity: %v", ids)
	}
	if _, err := changeFight(f.camp, func(combat *fight) error {
		i, _ := combat.entry(ids[0])
		if i < 0 {
			return fmt.Errorf("aura source has no turn")
		}
		combat.Index = i
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m := encounterMap{Grid: []string{"......", "......"}, Tokens: []mapToken{
		{ID: ids[0], Name: source.Name, Position: mapSquare{0, 0}, Size: 1},
		{ID: f.caster, Name: "Concentrating monster", Position: mapSquare{1, 0}, Size: 1},
		{ID: later, Name: "Later aura target", Position: mapSquare{0, 1}, Size: 1},
		{ID: f.char, Name: "Player target out of reach", Position: mapSquare{5, 1}, Size: 1},
	}}
	code, j = pgCall(t, "PUT", fmt.Sprintf("/api/campaigns/%d/battle-map", f.camp), f.gm, map[string]interface{}{"map": m, "forced_movement": true})
	if code != 200 {
		t.Fatalf("publish aura controls: %d %v", code, j)
	}
	firstHP, laterHP := monsterHPNow(f.camp, f.caster), monsterHPNow(f.camp, later)
	code, j = pgCall(t, "POST", path+"/next", f.gm, map[string]interface{}{})
	if code != 409 || j["error"] != "combat_adjudication_pending" {
		t.Fatalf("expected turn pause at first aura target: %d %v", code, j)
	}
	if first, second := monsterHPNow(f.camp, f.caster), monsterHPNow(f.camp, later); first != firstHP-4 || second != laterHP {
		t.Fatalf("later target or aura processed before adjudication: %d %d", first, second)
	}
	work := pauseWaitingWork(t, f.camp)
	if code, j = f.decide(t, "end"); code != 200 {
		t.Fatalf("end cast: %d %v", code, j)
	}
	pauseResume(t, f, work)
	pauseResume(t, f, work)
	if first, second := monsterHPNow(f.camp, f.caster), monsterHPNow(f.camp, later); first != firstHP-6 || second != laterHP-6 {
		t.Fatalf("resume omitted or repeated targets or aura effects: %d %d", first, second)
	}
}
