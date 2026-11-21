package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPostgresTurnPausesAndResumesAfterBurning(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) { m.Resistances = "" })
	f.cast(t, f.char)
	// A GM-granted Bane makes even natural 20 fail this fixture's DC 10
	// Concentration save: 20 - 10 - at least 1 < 10. No probabilistic retry.
	pauseFailingConcentration(t, f)
	addSpellEffect(f.camp, f.caster, 0, "Command", "commanded", "grovel", 1, "", 0)
	res := addCondition(f.camp, f.caster, condAdd{Condition: "blinded", Source: "invented start save", Rounds: 5, EndSave: &conditionSave{Ability: "wis", DC: 1, When: "start_of_turn"}})
	if res.Err != nil || !res.Added {
		t.Fatalf("start save: %+v", res)
	}
	startBurning(f.camp, f.caster, "invented pause probe")
	_, err := changeFight(f.camp, func(fight *fight) error {
		for i, e := range fight.Order {
			if asInt(e["id"]) == f.caster {
				if i == 0 {
					t.Fatal("fixture needs a preceding turn")
				}
				fight.Index = i - 1
				return nil
			}
		}
		t.Fatal("caster missing")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	hp := monsterHPNow(f.camp, f.caster)
	path := fmt.Sprintf("/api/campaigns/%d/combat/next", f.camp)
	code, j := pgCall(t, "POST", path, f.gm, map[string]interface{}{})
	if code != 409 || j["error"] != "combat_adjudication_pending" || monsterConcentrationPending(f.camp, f.caster) == "" {
		t.Fatalf("Burning must stop the turn at its pending save: %d %v", code, j)
	}
	after := monsterHPNow(f.camp, f.caster)
	if after >= hp || after < hp-4 {
		t.Fatalf("Burning damage was not applied exactly once: %d -> %d", hp, after)
	}
	if hasCondMarker(f.camp, f.caster, "prone") || !hasCondMarker(f.camp, f.caster, "blinded") {
		t.Fatal("Command or a later repeated save executed after Concentration became pending")
	}
	var rounds int
	db.QueryRow(`SELECT rounds_left FROM spell_effects WHERE lobby_id=$1 AND target_id=$2 AND kind='commanded'`, f.camp, f.caster).Scan(&rounds)
	if rounds != 1 {
		t.Fatalf("Command timer changed while pending: %d", rounds)
	}
	var work, status string
	if err = db.QueryRow(`SELECT id,status FROM combat_paused_work WHERE lobby_id=$1 AND status='waiting'`, f.camp).Scan(&work, &status); err != nil {
		t.Fatal(err)
	}
	saved, _ := readFight(db, f.camp)
	if code, j = f.decide(t, "end"); code != 200 {
		t.Fatalf("decision: %d %v", code, j)
	}
	if f.held(f.char) != 0 {
		t.Fatal("letting the failed save stand did not end its condition")
	}
	if code, j = pgCall(t, "POST", path, f.gm, map[string]interface{}{}); code != 409 {
		t.Fatalf("ready work must be resumed before another turn starts: %d %v", code, j)
	}
	body := map[string]interface{}{"campaign_id": f.camp, "resume_id": work}
	code, j = pgCall(t, "POST", "/api/gm/combat-resume", f.gm, body)
	if code != 200 {
		t.Fatalf("resume: %d %v", code, j)
	}
	resumed, _ := readFight(db, f.camp)
	if !hasCondMarker(f.camp, f.caster, "prone") || hasCondMarker(f.camp, f.caster, "blinded") || monsterHPNow(f.camp, f.caster) != after || resumed.Index != saved.Index || resumed.Round != saved.Round || !resumed.StartedAt.Equal(saved.StartedAt) {
		t.Fatal("resume repeated Burning, advanced again, or skipped Command")
	}
	code, j = pgCall(t, "POST", "/api/gm/combat-resume", f.gm, body)
	if code != 200 || monsterHPNow(f.camp, f.caster) != after {
		t.Fatalf("retry repeated completed work: %d %v", code, j)
	}
}

// These are independent aura sources, not the character held by the monster.
// Character creation/setup use the public routes; the high-level class fields
// are component-fixture data so this test exercises combat rather than leveling.
func pauseAuraSource(t *testing.T, f reviewConcentrationCase, name string) int {
	t.Helper()
	_, token := pgAgent(t, name)
	code, j := pgCall(t, "POST", "/api/characters", token, pgRogue(fmt.Sprintf("%s %d", name, f.camp)))
	if code != 200 {
		t.Fatalf("aura source: %d %v", code, j)
	}
	id := int(j["character_id"].(float64))
	pgFinish(t, token, id)
	pgExec(t, `UPDATE characters SET lobby_id=$1,class='Paladin',level=20,cha=20,hp=500,max_hp=500 WHERE id=$2`, f.camp, id)
	_, err := changeFight(f.camp, func(fight *fight) error {
		fight.Order = append(fight.Order, map[string]interface{}{"id": id, "name": name, "initiative": -200})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func pauseCasterTurn(t *testing.T, f reviewConcentrationCase, atStart bool) {
	t.Helper()
	_, err := changeFight(f.camp, func(fight *fight) error {
		for i, e := range fight.Order {
			if asInt(e["id"]) == f.caster {
				fight.Index = i
				if atStart {
					if i == 0 {
						t.Fatal("fixture needs preceding turn")
					}
					fight.Index--
				}
				return nil
			}
		}
		t.Fatal("caster missing")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func pauseWaitingWork(t *testing.T, lobby int) string {
	t.Helper()
	var id string
	if err := db.QueryRow(`SELECT id FROM combat_paused_work WHERE lobby_id=$1 AND status='waiting'`, lobby).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
func pauseResume(t *testing.T, f reviewConcentrationCase, id string) map[string]interface{} {
	t.Helper()
	code, j := pgCall(t, "POST", "/api/gm/combat-resume", f.gm, map[string]interface{}{"campaign_id": f.camp, "resume_id": id})
	if code != 200 {
		t.Fatalf("resume: %d %v", code, j)
	}
	return j
}
func TestPostgresTurnPausesAgainBetweenAuraSources(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	pauseFailingConcentration(t, f)
	addSpellEffect(f.camp, f.caster, 0, "Command", "commanded", "grovel", 1, "", 0)
	first := pauseAuraSource(t, f, "First aura")
	second := pauseAuraSource(t, f, "Second aura")
	mp := encounterMap{Grid: []string{"............"}, Tokens: []mapToken{
		{ID: f.caster, Name: "Review Focus Caster", Position: mapSquare{0, 0}, Size: 1},
		{ID: first, Name: "First aura", Position: mapSquare{1, 0}, Size: 1},
		{ID: second, Name: "Second aura", Position: mapSquare{2, 0}, Size: 1},
	}}
	code, j := pgCall(t, "PUT", fmt.Sprintf("/api/campaigns/%d/battle-map", f.camp), f.gm, map[string]interface{}{"map": mp, "forced_movement": true})
	if code != 200 {
		t.Fatalf("map: %d %v", code, j)
	}
	addSpellEffect(f.camp, first, first, "Holy Nimbus", "holy_nimbus", "", 100, "", 0)
	addSpellEffect(f.camp, second, second, "Holy Nimbus", "holy_nimbus", "", 100, "", 0)
	pauseCasterTurn(t, f, true)
	hp := monsterHPNow(f.camp, f.caster)
	code, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/next", f.camp), f.gm, map[string]interface{}{})
	if code != 409 || monsterHPNow(f.camp, f.caster) != hp-11 || hasCondMarker(f.camp, f.caster, "prone") {
		t.Fatalf("first aura did not pause before the second and Command: %d %v hp=%d", code, j, monsterHPNow(f.camp, f.caster))
	}
	work := pauseWaitingWork(t, f.camp)
	turn, _ := readFight(db, f.camp)
	if code, j = f.decide(t, "legendary_resistance"); code != 200 {
		t.Fatalf("keep: %d %v", code, j)
	}
	j = pauseResume(t, f, work)
	if j["combat_paused"] != true || pauseWaitingWork(t, f.camp) != work || monsterHPNow(f.camp, f.caster) != hp-22 || hasCondMarker(f.camp, f.caster, "prone") || legendaryResistanceLeft(f.camp, f.caster) != 2 {
		t.Fatalf("second aura did not pause exactly once on the same work: %v hp=%d", j, monsterHPNow(f.camp, f.caster))
	}
	if code, j = f.decide(t, "end"); code != 200 {
		t.Fatalf("end: %d %v", code, j)
	}
	pauseResume(t, f, work)
	now, _ := readFight(db, f.camp)
	if !hasCondMarker(f.camp, f.caster, "prone") || f.held(f.char) != 0 || monsterHPNow(f.camp, f.caster) != hp-22 || now.Index != turn.Index || now.Round != turn.Round || !now.StartedAt.Equal(turn.StartedAt) {
		t.Fatal("final resume skipped Command, repeated aura damage or advanced the turn twice")
	}
	pauseResume(t, f, work)
	if monsterHPNow(f.camp, f.caster) != hp-22 {
		t.Fatal("completed retry repeated damage")
	}
}

func TestPostgresEndTurnPausesBeforeConditionCountdown(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	pauseFailingConcentration(t, f)
	cleric := pauseAuraSource(t, f, "Spirit source")
	pgExec(t, `UPDATE characters SET concentrating_on='Spirit Guardians' WHERE id=$1`, cleric)
	raw, err := json.Marshal(sgDetail{Dice: "1d1+1", Type: "radiant", DC: 1, Targets: []int{f.caster}})
	if err != nil {
		t.Fatal(err)
	}
	addSpellEffect(f.camp, cleric, cleric, "Spirit Guardians", "spirit_guardians", string(raw), 100, "", 0)
	res := addCondition(f.camp, f.caster, condAdd{Condition: "frightened", Source: "invented countdown", Rounds: 2})
	if res.Err != nil || !res.Added {
		t.Fatalf("condition: %+v", res)
	}
	duration := func() int {
		for _, r := range loadConditionRecords(f.camp, f.caster) {
			if r.Source == "invented countdown" {
				return r.DurationRounds
			}
		}
		return 0
	}
	pauseCasterTurn(t, f, false)
	old, _ := readFight(db, f.camp)
	hp := monsterHPNow(f.camp, f.caster)
	code, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/next", f.camp), f.gm, map[string]interface{}{})
	atPause, _ := readFight(db, f.camp)
	// Flat 2 damage is 1 on a successful Wisdom save and 2 on a failure.
	if code != 409 || duration() != 2 || atPause.Index != old.Index || monsterHPNow(f.camp, f.caster) >= hp {
		t.Fatalf("end-turn pause: %d %v duration=%d", code, j, duration())
	}
	after := monsterHPNow(f.camp, f.caster)
	work := pauseWaitingWork(t, f.camp)
	if code, j = f.decide(t, "end"); code != 200 {
		t.Fatalf("end: %d %v", code, j)
	}
	pauseResume(t, f, work)
	next, _ := readFight(db, f.camp)
	if duration() != 1 || monsterHPNow(f.camp, f.caster) != after || next.Index == old.Index {
		t.Fatal("resume did not finish countdown and advance exactly once")
	}
	pauseResume(t, f, work)
	if duration() != 1 {
		t.Fatal("duplicate resume counted a condition twice")
	}
}

type combatRestartFixture struct {
	GM                              string
	Lobby, Caster, HP, Index, Round int
	StartedAt                       time.Time
	Work, Decision                  string
}

func TestPostgresCombatContinuationFreshProcess(t *testing.T) {
	testPostgres(t)
	if path := os.Getenv("COMBAT_RESTART_FIXTURE"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var f combatRestartFixture
		if err = json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		code, j := pgCall(t, "POST", "/api/gm/monster-concentration", f.GM, map[string]interface{}{"campaign_id": f.Lobby, "combatant_id": f.Caster, "decision": "end", "decision_token": f.Decision})
		if code != 200 {
			t.Fatalf("cold decision: %d %v", code, j)
		}
		body := map[string]interface{}{"campaign_id": f.Lobby, "resume_id": f.Work}
		for i := 0; i < 2; i++ {
			code, j = pgCall(t, "POST", "/api/gm/combat-resume", f.GM, body)
			if code != 200 {
				t.Fatalf("cold resume %d: %d %v", i, code, j)
			}
		}
		now, err := readFight(db, f.Lobby)
		if err != nil {
			t.Fatal(err)
		}
		if monsterHPNow(f.Lobby, f.Caster) != f.HP || !hasCondMarker(f.Lobby, f.Caster, "prone") || now.Index != f.Index || now.Round != f.Round || !now.StartedAt.Equal(f.StartedAt) {
			t.Fatal("cold resume changed completed damage/turn or lost Command")
		}
		return
	}
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) { m.Resistances = "" })
	f.cast(t, f.char)
	pauseFailingConcentration(t, f)
	addSpellEffect(f.camp, f.caster, 0, "Command", "commanded", "grovel", 1, "", 0)
	startBurning(f.camp, f.caster, "invented cold pause")
	pauseCasterTurn(t, f, true)
	code, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/next", f.camp), f.gm, map[string]interface{}{})
	if code != 409 {
		t.Fatalf("pause: %d %v", code, j)
	}
	now, _ := readFight(db, f.camp)
	fixture := combatRestartFixture{f.gm, f.camp, f.caster, monsterHPNow(f.camp, f.caster), now.Index, now.Round, now.StartedAt, pauseWaitingWork(t, f.camp), concentrationDecisionToken(t, f)}
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "combat-restart.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestPostgresCombatContinuationFreshProcess$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), "COMBAT_RESTART_FIXTURE="+path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cold child: %v\n%s", err, output)
	}
	var status string
	if err = db.QueryRow(`SELECT status FROM combat_paused_work WHERE id=$1 AND lobby_id=$2`, fixture.Work, f.camp).Scan(&status); err != nil || status != "done" {
		t.Fatalf("cold completion not persisted: %s %v", status, err)
	}
}

// The stored kind is "bane"; "baned" is only its displayed label. Assert the
// fixture reaches the shared saving-throw owner and fails even at natural 20.
func pauseFailingConcentration(t *testing.T, f reviewConcentrationCase) {
	t.Helper()
	addSpellEffect(f.camp, f.caster, 0, "Bane (GM)", "bane", "", 10, "", 0)
	save := (&saveCtx{lobby: f.camp, saver: f.caster, ability: "con", dc: 10, concentration: true}).gather()
	if save.Judge(20).Success {
		t.Fatalf("fixture must fail the minimum Concentration DC even at natural 20: %+v", save)
	}
	found := false
	for _, adj := range save.Adjust {
		if strings.Contains(adj.Why, "Bane") && adj.Amount <= -1 && adj.Amount >= -4 {
			found = true
		}
	}
	if !found {
		t.Fatalf("fixture did not reach Bane's saving-throw adjustment: %+v", save)
	}
}

func TestPostgresResumedTurnKeepsPrivateGMNotice(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) { m.Resistances = "" })
	f.cast(t, f.char)
	pauseFailingConcentration(t, f)
	enemy := monsterIDsByKeyOrName(f.camp, "Opponent")[0]
	// Keep the source's own end-of-turn hook out of this transition.
	_, err := changeFight(f.camp, func(fight *fight) error {
		ci, ei := -1, -1
		for i, e := range fight.Order {
			if asInt(e["id"]) == f.caster {
				ci = i
			}
			if asInt(e["id"]) == enemy {
				ei = i
			}
		}
		if ei < ci {
			fight.Order[ci], fight.Order[ei] = fight.Order[ei], fight.Order[ci]
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	secret := fmt.Sprintf("private-source-%d", f.camp)
	r := addCondition(f.camp, f.caster, condAdd{Condition: "frightened", Source: secret, SourceID: enemy, Notes: notesUntilEndOfSourceTurn})
	if r.Err != nil || !r.Added {
		t.Fatalf("source condition: %+v", r)
	}
	startBurning(f.camp, f.caster, "invented private continuation")
	pauseCasterTurn(t, f, true)
	code, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/next", f.camp), f.gm, map[string]interface{}{})
	if code != 409 {
		t.Fatalf("pause: %d %v", code, j)
	}
	work := pauseWaitingWork(t, f.camp)
	code, j = pgCall(t, "POST", "/api/gm/adjust-monster", f.gm, map[string]interface{}{"campaign_id": f.camp, "target_id": enemy, "hp": 0})
	if code != 200 {
		t.Fatalf("down source: %d %v", code, j)
	}
	if !hasCondMarker(f.camp, f.caster, "frightened") {
		t.Fatal("fixture's source-anchored condition ended before the resumed stage")
	}
	if code, j = f.decide(t, "end"); code != 200 {
		t.Fatalf("end: %d %v", code, j)
	}
	result := pauseResume(t, f, work)
	notice := fmt.Sprint(result)
	for _, n := range noticesOf(t, f.camp) {
		notice += " " + n.Text
	}
	if !strings.Contains(notice, secret) || !strings.Contains(notice, "still has the frightened condition") {
		t.Fatalf("resumed turn lost the GM's source-ended adjudication notice: %v", result)
	}
	var public string
	if err := db.QueryRow(`SELECT COALESCE(string_agg(COALESCE(result,''),' '),'') FROM actions WHERE lobby_id=$1`, f.camp).Scan(&public); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(public, secret) || strings.Contains(public, "still has the frightened condition") {
		t.Fatal("private resumed-turn notice entered the shared log")
	}
	if !strings.Contains(public, "burning") {
		t.Fatal("public log control is empty")
	}
	cp := fmt.Sprintf("/api/campaigns/%d", f.camp)
	for _, path := range []string{"/api/my-turn", cp + "/log", cp} {
		code, j = pgCall(t, "GET", path, f.player, nil)
		if code != 200 {
			t.Fatalf("player read %s: %d %v", path, code, j)
		}
		if strings.Contains(fmt.Sprint(j), secret) || strings.Contains(fmt.Sprint(j), "still has the frightened condition") {
			t.Fatalf("private continuation notice leaked through %s", path)
		}
	}
}
