package main

import (
	"fmt"
	"testing"
	"time"
)

// Stabilizing (the Medicine action, Spare the Dying) and Survivor go through the Hit Point owner (HANDOFF item 28): a dying
// character becomes Stable with both counts reset and its 1d4 hours started, once; a character who isn't dying is left alone;
// Survivor heals only a character who is Bloodied when the row is locked.
func TestPostgresStabilizeAndSurvivor(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "pg stabilize")
	c, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Dying Two %d", time.Now().UnixNano()%1e9)))
	if c != 200 {
		t.Fatalf("create: %d %v", c, j)
	}
	id := int(j["character_id"].(float64))
	vitals := func() (hp, s, f, rounds int, stable bool) {
		db.QueryRow(`SELECT hp, death_save_successes, death_save_failures, COALESCE(stable_rounds_left, 0), is_stable FROM characters WHERE id = $1`, id).
			Scan(&hp, &s, &f, &rounds, &stable)
		return
	}

	pgExec(t, `UPDATE characters SET hp = 0, is_stable = false, is_dead = false, death_save_successes = 1, death_save_failures = 2, stable_rounds_left = 0 WHERE id = $1`, id)
	if changed, err := stabilizeCharacter(id); !changed || err != nil {
		t.Fatalf("a dying character is stabilized: %v %v", changed, err)
	}
	if hp, s, f, rounds, stable := vitals(); hp != 0 || s != 0 || f != 0 || rounds <= 0 || !stable {
		t.Errorf("after stabilizing: hp %d, saves %d/%d, rounds %d, stable %v", hp, s, f, rounds, stable)
	}
	if changed, err := stabilizeCharacter(id); changed || err != nil {
		t.Errorf("a Stable character is not stabilized again: %v %v", changed, err)
	}
	pgExec(t, `UPDATE characters SET hp = 5, is_stable = false WHERE id = $1`, id)
	if changed, _ := stabilizeCharacter(id); changed {
		t.Error("a character above 0 Hit Points is not stabilized")
	}

	pgExec(t, `UPDATE characters SET class = 'fighter', subclass = 'champion', level = 18, max_hp = 100, hp = 100, con = 14 WHERE id = $1`, id)
	if r := survivorRegen(id, "Pg"); r != nil {
		t.Errorf("Survivor heals a character who isn't Bloodied: %v", r)
	}
	pgExec(t, `UPDATE characters SET hp = 50 WHERE id = $1`, id)
	if r := survivorRegen(id, "Pg"); r == nil || r["new_hp"] != 57 {
		t.Errorf("Survivor at 50 of 100 Hit Points (5 + Constitution 2): %v", r)
	}
	pgExec(t, `UPDATE characters SET hp = 0, is_stable = true WHERE id = $1`, id)
	if r := survivorRegen(id, "Pg"); r != nil {
		t.Errorf("Survivor doesn't heal a character at 0 Hit Points: %v", r)
	}
}

// The Medicine action (moved from the SQLite test when stabilizing moved onto the Hit Point owner, whose row lock SQLite can't
// run): it costs the action; a success makes the dying patient Stable without healing and resets both tallies (SRD 5.2.1: "The
// number of both is reset to zero when you regain any Hit Points or become Stable", F210); a patient healed meanwhile is left alone.
func TestPostgresStabilizeAction(t *testing.T) {
	if got := getActionResourceType("stabilize"); got != "action" {
		t.Fatalf("stabilize resource = %q, want action", got)
	}
	testPostgres(t)
	_, tok := pgAgent(t, "pg medic")
	mk := func(name string) int {
		c, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("%s %d", name, time.Now().UnixNano()%1e9)))
		if c != 200 {
			t.Fatalf("create: %d %v", c, j)
		}
		return int(j["character_id"].(float64))
	}
	medic, patient := mk("Pg Medic"), mk("Pg Patient")
	pgExec(t, `UPDATE characters SET wis = 30, skill_proficiencies = 'medicine' WHERE id = $1`, medic) // +10 +2: never below DC 10
	pgExec(t, `UPDATE characters SET hp = 0, is_stable = false, is_dead = false, death_save_successes = 2, death_save_failures = 1 WHERE id = $1`, patient)
	if result, err := resolveStabilizeAction(medic, patient, "Patient"); err != nil || result == "" {
		t.Fatalf("stabilize result=%q err=%v", result, err)
	}
	var stable bool
	var hp, s, f int
	db.QueryRow(`SELECT is_stable, hp, death_save_successes, death_save_failures FROM characters WHERE id = $1`, patient).Scan(&stable, &hp, &s, &f)
	if !stable || hp != 0 || s != 0 || f != 0 {
		t.Fatalf("first aid must stabilize without healing and reset both tallies: stable=%v hp=%d saves=%d/%d", stable, hp, s, f)
	}
	pgExec(t, `UPDATE characters SET is_stable = false, hp = 5 WHERE id = $1`, patient)
	if _, err := resolveStabilizeAction(medic, patient, "Patient"); err != nil {
		t.Fatal(err)
	}
	if db.QueryRow(`SELECT is_stable FROM characters WHERE id = $1`, patient).Scan(&stable); stable {
		t.Fatal("a healed patient must not become Stable at positive Hit Points")
	}
}
