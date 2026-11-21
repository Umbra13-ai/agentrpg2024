package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

type concentrationRestartFixture struct {
	GM                    string
	Lobby, Caster, Target int
	Cast, Decision        string
}

// The child starts the real server's database/schema/rules and router in a new
// process. No package globals or connection pool are inherited. The parent is
// only the fixture/test harness; no concurrent gameplay runs while it waits.
func TestPostgresConcentrationFreshProcess(t *testing.T) {
	testPostgres(t)
	if path := os.Getenv("CONCENTRATION_RESTART_FIXTURE"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var fixture concentrationRestartFixture
		if err = json.Unmarshal(raw, &fixture); err != nil {
			t.Fatal(err)
		}
		s, err := readMonsterConcentration(db, fixture.Lobby, fixture.Caster)
		if err != nil {
			t.Fatal(err)
		}
		if s.CastToken != fixture.Cast || s.DecisionToken != fixture.Decision || s.Pending == "" {
			t.Fatal("cold start lost cast or pending identity")
		}
		var first game.SaveResult
		if err = json.Unmarshal([]byte(s.PendingSave), &first); err != nil {
			t.Fatal(err)
		}
		queued, err := concentrationRolls(s.Queue)
		if err != nil {
			t.Fatal(err)
		}
		if len(queued) != 1 || first.Save.Bonus != -10 || first.Save.Mode() != "disadvantage" || first.Success || queued[0].Save.Bonus != -10 || queued[0].Save.Mode() != "disadvantage" || queued[0].Success {
			t.Fatal("cold start lost save-time rules/outcomes")
		}
		code, j := pgCall(t, "POST", "/api/gm/monster-concentration", fixture.GM, map[string]interface{}{"campaign_id": fixture.Lobby, "combatant_id": fixture.Caster, "decision": "legendary_resistance", "decision_token": fixture.Decision})
		if code != 200 {
			t.Fatalf("cold-start decision: %d %v", code, j)
		}
		s, err = readMonsterConcentration(db, fixture.Lobby, fixture.Caster)
		if err != nil {
			t.Fatal(err)
		}
		if s.Pending == "" || s.DecisionToken == fixture.Decision || s.CastToken != fixture.Cast || legendaryResistanceLeft(fixture.Lobby, fixture.Caster) != 2 {
			t.Fatal("cold-start decision did not advance exactly one queued failure")
		}
		return
	}
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	r := addCondition(f.camp, f.caster, condAdd{Condition: "disadv_concentration", Source: "invented restart rider", Rounds: 2})
	if r.Err != nil || !r.Added {
		t.Fatalf("rider: %+v", r)
	}
	f.damage(t)
	f.damage(t)
	s, err := readMonsterConcentration(db, f.camp, f.caster)
	if err != nil {
		t.Fatal(err)
	}
	fixture := concentrationRestartFixture{f.gm, f.camp, f.caster, f.char, s.CastToken, s.DecisionToken}
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "restart-fixture.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestPostgresConcentrationFreshProcess$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), "CONCENTRATION_RESTART_FIXTURE="+path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cold-start child: %v\n%s", err, output)
	}
	after, err := readMonsterConcentration(db, f.camp, f.caster)
	if err != nil {
		t.Fatal(err)
	}
	if after.CastToken != s.CastToken || after.DecisionToken == s.DecisionToken || after.Pending == "" || f.held(f.char) == 0 {
		t.Fatal("persisted game state after the fresh process is inconsistent")
	}
}
