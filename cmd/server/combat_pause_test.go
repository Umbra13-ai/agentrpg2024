package main

import (
	"fmt"
	"sync"
	"testing"
)

func TestPostgresPendingConcentrationBlocksNewWork(t *testing.T) {
	f := reviewConcentrationSetup(t)
	held := pauseAuraSource(t, f, "Separate held target")
	pgExec(t, `UPDATE characters SET species='Human' WHERE id=$1`, held)
	_, err := changeFight(f.camp, func(fight *fight) error {
		for i, e := range fight.Order {
			if asInt(e["id"]) == f.char {
				fight.Index = i
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var equipped string
	if err := db.QueryRow(`SELECT COALESCE(equipped_main_hand,'') FROM characters WHERE id=$1`, f.char).Scan(&equipped); err != nil {
		t.Fatal(err)
	}
	if equipped == "" {
		t.Fatal("fixture has no equipped weapon")
	}
	control := map[string]interface{}{"character_id": f.char, "action": "attack", "target_id": f.caster, "description": "I attack with my " + equipped}
	if code, j := pgCall(t, "POST", "/api/action", f.player, control); code != 200 {
		t.Fatalf("unheld attack control: %d %v", code, j)
	}
	f.cast(t, held)
	f.damage(t)
	token := concentrationDecisionToken(t, f)
	before, _ := readFight(db, f.camp)
	pgExec(t, `UPDATE characters SET action_used=false,attacks_remaining=NULL WHERE id=$1`, f.char)
	hp := monsterHPNow(f.camp, f.caster)
	calls := []struct {
		name, method, path, auth string
		body                     interface{}
	}{
		{"turn", "POST", fmt.Sprintf("/api/campaigns/%d/combat/next", f.camp), f.gm, map[string]interface{}{}},
		{"player attack", "POST", "/api/action", f.player, control},
		{"monster attack", "POST", "/api/gm/narrate", f.gm, map[string]interface{}{"campaign_id": f.camp, "narration": "A later attack.", "monster_action": map[string]interface{}{"monster": "Opponent", "action": "Bite", "target_ids": []int{f.char}}}},
		{"rest", "POST", fmt.Sprintf("/api/characters/%d/rest", f.char), f.player, map[string]interface{}{"type": "short"}},
	}
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			code, j := pgCall(t, c.method, c.path, c.auth, c.body)
			if code != 409 || j["error"] != "combat_adjudication_pending" {
				t.Errorf("pending work must refuse before effects: %d %v", code, j)
			}
		})
	}
	after, _ := readFight(db, f.camp)
	if after.Round != before.Round || after.Index != before.Index || monsterHPNow(f.camp, f.caster) != hp || concentrationDecisionToken(t, f) != token || f.held(held) == 0 {
		t.Fatal("refused work changed the pending combat")
	}
	var used bool
	db.QueryRow(`SELECT action_used FROM characters WHERE id=$1`, f.char).Scan(&used)
	if used {
		t.Fatal("refused work spent an action")
	}
	// A GM can still read and decide; those are the way out of the pause.
	if code, j := pgCall(t, "GET", fmt.Sprintf("/api/gm/notices?campaign_id=%d", f.camp), f.gm, nil); code != 200 {
		t.Fatalf("notices: %d %v", code, j)
	}
	if code, j := f.decide(t, "end"); code != 200 {
		t.Fatalf("decision: %d %v", code, j)
	}
	code, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/next", f.camp), f.gm, map[string]interface{}{})
	if code != 200 {
		t.Fatalf("resolved combat did not resume: %d %v", code, j)
	}
}

func TestPostgresCampaignCommandLeaseIsolation(t *testing.T) {
	f := reviewConcentrationSetup(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	saved := turnStartHooks
	turnStartHooks = append(append([]turnHook(nil), saved...), turnHook{name: "controlled boundary probe", run: func(change *turnChange) {
		if change.Lobby == f.camp {
			close(started)
			<-release
		}
	}})
	t.Cleanup(func() { turnStartHooks = saved })
	done := make(chan int, 1)
	go func() {
		code, _ := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/next", f.camp), f.gm, map[string]interface{}{})
		done <- code
	}()
	<-started
	before := monsterHPNow(f.camp, f.caster)
	code, j := pgCall(t, "POST", "/api/gm/adjust-monster", f.gm, map[string]interface{}{"campaign_id": f.camp, "target_id": f.caster, "damage": 1})
	if code != 409 || j["error"] != "combat_operation_in_progress" || monsterHPNow(f.camp, f.caster) != before {
		t.Errorf("overlapping command was applied: %d %v", code, j)
	}
	if code, j = pgCall(t, "GET", fmt.Sprintf("/api/gm/notices?campaign_id=%d", f.camp), f.gm, nil); code != 200 {
		t.Errorf("read blocked: %d %v", code, j)
	}
	// A separate campaign remains usable while this one is busy.
	other := reviewConcentrationSetup(t)
	if code, j = pgCall(t, "POST", "/api/gm/adjust-monster", other.gm, map[string]interface{}{"campaign_id": other.camp, "target_id": other.caster, "damage": 1}); code != 200 {
		t.Errorf("another campaign was blocked: %d %v", code, j)
	}
	unblock()
	if code := <-done; code != 200 {
		t.Fatalf("first command: %d", code)
	}
	if code, j = pgCall(t, "POST", "/api/gm/adjust-monster", f.gm, map[string]interface{}{"campaign_id": f.camp, "target_id": f.caster, "damage": 1}); code != 200 {
		t.Fatalf("lease was not released: %d %v", code, j)
	}
}

func TestPostgresPendingCombatBlocksClockOwners(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	addSpellEffect(f.camp, f.char, 0, "Bless (GM)", "bless", "", 10, "", 0)
	res := addCondition(f.camp, f.char, condAdd{Condition: "poisoned", Source: "invented clock", Rounds: 10})
	if res.Err != nil || !res.Added {
		t.Fatalf("condition: %+v", res)
	}
	f.damage(t)
	duration := func() (int, int) {
		var spell int
		if err := db.QueryRow(`SELECT rounds_left FROM spell_effects WHERE lobby_id=$1 AND target_id=$2 AND kind='bless'`, f.camp, f.char).Scan(&spell); err != nil {
			t.Fatal(err)
		}
		for _, r := range loadConditionRecords(f.camp, f.char) {
			if r.Source == "invented clock" {
				return spell, r.DurationRounds
			}
		}
		return spell, 0
	}
	passSpellTime(f.camp, 1)
	passConditionTime(f.camp, 1, 0)
	if a, b := duration(); a != 10 || b != 10 {
		t.Fatalf("pending clock advanced: %d/%d", a, b)
	}
	if code, j := f.decide(t, "end"); code != 200 {
		t.Fatalf("end: %d %v", code, j)
	}
	passSpellTime(f.camp, 1)
	if a, b := duration(); a != 9 || b != 9 {
		t.Fatalf("unpaused clock control: %d/%d", a, b)
	}
}
