package main

import (
	"fmt"
	"testing"
)

// A Concentration-row failure happens after the HP transaction committed. That
// partial command must stop at the failed branch, and a restart must still find
// a durable interruption to reconcile before dependent play continues.
func TestPostgresConcentrationWriteFailureStopsDependentTurn(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) { m.Resistances = "" })
	f.cast(t, f.char)
	pauseFailingConcentration(t, f)
	addSpellEffect(f.camp, f.caster, 0, "Command", "commanded", "grovel", 1, "", 0)
	startBurning(f.camp, f.caster, "invented storage-boundary probe")
	pauseCasterTurn(t, f, true)
	f.rejectStorage(t, "UPDATE")
	hp := monsterHPNow(f.camp, f.caster)
	code, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/next", f.camp), f.gm, map[string]interface{}{})
	if code != 500 || j["error"] != "database_error" {
		t.Fatalf("write failure must be explicit: %d %v", code, j)
	}
	if monsterHPNow(f.camp, f.caster) >= hp {
		t.Fatal("control: the triggering HP write did not commit")
	}
	if hasCondMarker(f.camp, f.caster, "prone") {
		t.Fatal("Command executed after the Concentration persistence failure")
	}
	var rounds int
	if err := db.QueryRow(`SELECT rounds_left FROM spell_effects WHERE lobby_id=$1 AND target_id=$2 AND kind='commanded'`, f.camp, f.caster).Scan(&rounds); err != nil || rounds != 1 {
		t.Fatalf("unresolved timer advanced: %d %v", rounds, err)
	}
	if paused, err := pendingCombatDecision(f.camp); err != nil || !paused {
		t.Fatalf("partial failed command did not leave a durable campaign boundary: %v %v", paused, err)
	}
}

func TestPostgresConcentrationIncapacitatedEndsImmediately(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	code, j := pgCall(t, "POST", "/api/gm/conditions", f.gm, map[string]interface{}{"campaign_id": f.camp, "target_id": f.caster, "condition": "stunned", "source": "invented incapacitation probe"})
	if code != 200 {
		t.Fatalf("condition: %d %v", code, j)
	}
	if !monsterIncapacitated(f.camp, f.caster) {
		t.Fatal("control: condition was not applied")
	}
	if monsterConcentrationOn(f.camp, f.caster) != "" || f.held(f.char) != 0 {
		t.Fatal("Incapacitated left the tracked cast and its target effect active")
	}
	if paused, err := pendingCombatDecision(f.camp); err != nil || paused {
		t.Fatalf("successful condition cleanup left work paused: %v %v", paused, err)
	}
}

func TestPostgresConcentrationJournalFailureRollsBackHP(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	name := fmt.Sprintf("journal_insert_%d", f.camp)
	pgExec(t, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected journal failure'; END $$`, name))
	pgExec(t, fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON combat_paused_work FOR EACH ROW WHEN(NEW.lobby_id=%d) EXECUTE FUNCTION %s()`, name, f.camp, name))
	t.Cleanup(func() {
		pgExec(t, fmt.Sprintf(`DROP TRIGGER %s ON combat_paused_work`, name))
		pgExec(t, fmt.Sprintf(`DROP FUNCTION %s()`, name))
	})
	hp := monsterHPNow(f.camp, f.caster)
	code, j := pgCall(t, "POST", "/api/gm/adjust-monster", f.gm, map[string]interface{}{"campaign_id": f.camp, "target_id": f.caster, "damage": 100})
	if code != 500 || j["error"] != "database_error" {
		t.Fatalf("failed journal write: %d %v", code, j)
	}
	if monsterHPNow(f.camp, f.caster) != hp || f.held(f.char) == 0 {
		t.Fatal("HP/effect changed without durable damage obligation")
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM combat_paused_work WHERE lobby_id=$1`, f.camp).Scan(&n); err != nil || n != 0 {
		t.Fatalf("journal rollback: %d %v", n, err)
	}
}

func TestPostgresConcentrationEndFailureLeavesDurableBoundary(t *testing.T) {
	for _, kind := range []string{"death", "incapacitated"} {
		t.Run(kind, func(t *testing.T) {
			f := reviewConcentrationSetup(t)
			f.cast(t, f.char)
			f.rejectStorage(t, "DELETE")
			var code int
			var j map[string]interface{}
			if kind == "death" {
				code, j = pgCall(t, "POST", "/api/gm/adjust-monster", f.gm, map[string]interface{}{"campaign_id": f.camp, "target_id": f.caster, "hp": 0})
			} else {
				code, j = pgCall(t, "POST", "/api/gm/conditions", f.gm, map[string]interface{}{"campaign_id": f.camp, "target_id": f.caster, "condition": "stunned", "source": "invented storage probe"})
			}
			if code != 500 || j["error"] != "database_error" {
				t.Fatalf("cleanup failure: %d %v", code, j)
			}
			if kind == "death" && monsterHPNow(f.camp, f.caster) != 0 {
				t.Fatal("control: death did not commit")
			}
			if kind == "incapacitated" && !monsterIncapacitated(f.camp, f.caster) {
				t.Fatal("control: condition did not commit")
			}
			if f.held(f.char) == 0 || monsterConcentrationOn(f.camp, f.caster) == "" {
				t.Fatal("failed cleanup partially removed cast/effect")
			}
			if paused, err := pendingCombatDecision(f.camp); err != nil || !paused {
				t.Fatalf("lost durable boundary: %v %v", paused, err)
			}
		})
	}
}

func TestPostgresConcentrationJournalFailureRollsBackCondition(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	name := fmt.Sprintf("journal_condition_%d", f.camp)
	pgExec(t, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected journal failure'; END $$`, name))
	pgExec(t, fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON combat_paused_work FOR EACH ROW WHEN(NEW.lobby_id=%d) EXECUTE FUNCTION %s()`, name, f.camp, name))
	t.Cleanup(func() {
		pgExec(t, fmt.Sprintf(`DROP TRIGGER %s ON combat_paused_work`, name))
		pgExec(t, fmt.Sprintf(`DROP FUNCTION %s()`, name))
	})
	code, j := pgCall(t, "POST", "/api/gm/conditions", f.gm, map[string]interface{}{"campaign_id": f.camp, "target_id": f.caster, "condition": "stunned", "source": "invented atomicity probe"})
	if code != 500 || j["error"] != "database_error" {
		t.Fatalf("journal insert failure: %d %v", code, j)
	}
	if monsterIncapacitated(f.camp, f.caster) || f.held(f.char) == 0 || monsterConcentrationOn(f.camp, f.caster) == "" {
		t.Fatal("condition or cast changed without a durable obligation")
	}
}

func TestPostgresConcentrationHealthyJournalClosesOnlyOwnWork(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) { a := m.Abilities["con"]; a.Save = 30; m.Abilities["con"] = a })
	f.cast(t, f.char)
	unrelated := pauseStoreWork(t, f.camp, "capturing", []combatFrame{})
	hp := monsterHPNow(f.camp, f.caster)
	// Owner control deliberately bypasses route gating to verify that normal
	// completion never changes a different interrupted operation in this campaign.
	if _, err := loseMonsterHP(f.camp, f.caster, 4, ""); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM combat_paused_work WHERE id=$1`, unrelated).Scan(&status); err != nil || status != "capturing" {
		t.Fatalf("unrelated interruption finalized: %s %v", status, err)
	}
	var done int
	if err := db.QueryRow(`SELECT count(*) FROM combat_paused_work WHERE lobby_id=$1 AND status='done'`, f.camp).Scan(&done); err != nil || done != 1 {
		t.Fatalf("healthy journal did not close: %d %v", done, err)
	}
	if monsterHPNow(f.camp, f.caster) != hp-4 || monsterConcentrationOn(f.camp, f.caster) == "" {
		t.Fatal("passed save changed cast or lost damage")
	}
}
