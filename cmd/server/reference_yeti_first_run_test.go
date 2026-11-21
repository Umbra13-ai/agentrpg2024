package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func suppliedFreshYeti(t *testing.T) srdMonster2024 {
	t.Helper()
	return suppliedFreshMonster(t, "abominable-yeti.json", "Abominable Yeti", "9")
}

func suppliedFreshMonster(t *testing.T, file, name, cr string) srdMonster2024 {
	t.Helper()
	folder := os.Getenv("FRESH_MONSTER_FIXTURES_DIR")
	if folder == "" {
		t.Skip("fresh private fixtures not provided")
	}
	raw, err := os.ReadFile(filepath.Join(folder, file))
	if err != nil {
		t.Fatal(err)
	}
	var batch struct {
		Monsters []srdMonster2024 `json:"monsters"`
	}
	if err = json.Unmarshal(raw, &batch); err != nil || len(batch.Monsters) != 1 {
		t.Fatalf("fresh fixture: %v", err)
	}
	monster := batch.Monsters[0]
	if monster.Name != name || monster.CR != cr {
		t.Fatal("incorrect fresh fixture")
	}
	for _, srd := range srdMonsters2024() {
		if strings.EqualFold(monster.Name, srd.Name) {
			t.Fatal("fresh case must be outside the SRD catalog")
		}
	}
	return monster
}

func referenceSaveTarget(t *testing.T, f reviewConcentrationCase, name string, conSave int, immune, resistant string, configure ...func(*srdMonster2024)) int {
	t.Helper()
	target := compendiumFixture()
	target.Name = name
	target.HP = 500
	target.Resistances = resistant
	target.DmgImmune = immune
	target.Traits = nil
	ability := target.Abilities["con"]
	ability.Save = conSave
	target.Abilities["con"] = ability
	for _, apply := range configure {
		apply(&target)
	}
	code, j := pgCall(t, "POST", "/api/gm/compendium/monsters", f.gm, map[string]interface{}{"source_note": "Invented save control", "monsters": []srdMonster2024{target}})
	if code != 201 {
		t.Fatalf("import control: %d %v", code, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key); err != nil {
			t.Errorf("remove control: %v", err)
		}
	})
	code, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", f.camp), f.gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": name, "monster_key": key, "initiative": -50}}})
	if code != 200 {
		t.Fatalf("add control: %d %v", code, j)
	}
	return monsterIDsByKeyOrName(f.camp, name)[0]
}

// This first-run suite probes all six named entries of one fresh fixture. Its
// individual assertions have their own scope. It is not the complete ability
// and interaction acceptance gate.
func TestPostgresFreshAbominableYetiNamedAbilities(t *testing.T) {
	original := suppliedFreshYeti(t)
	encounter := func(t *testing.T) reviewConcentrationCase {
		return reviewConcentrationSetup(t, func(m *srdMonster2024) { *m = original; m.Name = "Review Focus Caster" })
	}
	t.Run("fear of fire and cold immunity", func(t *testing.T) {
		f := encounter(t)
		ironGolemMap(t, f, map[int]int{f.char: 1})
		hp := monsterHPNow(f.camp, f.caster)
		damage := func(kind string, mitigate bool) {
			t.Helper()
			code, j := pgCall(t, "POST", "/api/gm/adjust-monster", f.gm, map[string]interface{}{"campaign_id": f.camp, "target_id": f.caster, "damage": 12, "damage_type": kind, "apply_resistances": mitigate})
			if code != 200 {
				t.Fatalf("damage: %d %v", code, j)
			}
		}
		claw := func() string {
			return fmt.Sprint(ironGolemAction(t, f, "Claw", map[string]interface{}{"target": fmt.Sprint(f.char), "apply_damage": false})["monster_action_result"])
		}
		damage("cold", true)
		if monsterHPNow(f.camp, f.caster) != hp || strings.Contains(claw(), "Fear of Fire") {
			t.Error("cold immunity or negative Fear of Fire control failed")
		}
		damage("fire", true)
		if monsterHPNow(f.camp, f.caster) != hp-12 {
			t.Fatal("control fire did not deal damage")
		}
		if text := claw(); !strings.Contains(text, "Disadvantage") || !strings.Contains(text, "Fear of Fire") {
			t.Errorf("fire did not cause Fear of Fire Disadvantage: %s", text)
		}
		notices := fmt.Sprintf("/api/gm/notices?campaign_id=%d", f.camp)
		code, reply := pgCall(t, "GET", notices, f.gm, nil)
		if code != 200 || !strings.Contains(fmt.Sprint(reply), "Disadvantage on ability checks") {
			t.Fatalf("GM could not see the check penalty: %d %v", code, reply)
		}
		if code, reply = pgCall(t, "GET", notices, f.player, nil); code == 200 {
			t.Fatalf("player saw private adjudication: %v", reply)
		}
		mod := (original.Abilities["str"].Score - 10) / 2
		code, reply = pgCall(t, "POST", "/api/gm/roll", f.gm, map[string]interface{}{
			"campaign_id": f.camp, "dice": fmt.Sprintf("1d20%+d", mod), "label": "NPC Strength check", "disadvantage": true, "secret": true,
		})
		dice, ok := reply["rolls"].([]interface{})
		if code != 200 || !ok || len(dice) != 2 || asInt(reply["total"]) != min(asInt(dice[0]), asInt(dice[1]))+mod {
			t.Fatalf("GM's disadvantaged check did not use the lower die: %d %v", code, reply)
		}
		path := fmt.Sprintf("/api/campaigns/%d/combat/next", f.camp)
		next := func() {
			t.Helper()
			if code, j := pgCall(t, "POST", path, f.gm, map[string]interface{}{}); code != 200 {
				t.Fatalf("next: %d %v", code, j)
			}
		}
		reachYeti := func() {
			t.Helper()
			for i := 0; currentTurnID(f.camp) != f.caster; i++ {
				if i >= 10 {
					t.Fatal("yeti's turn was not reached")
				}
				next()
			}
		}
		reachYeti()
		if text := claw(); !strings.Contains(text, "Fear of Fire") {
			t.Errorf("penalty ended on an earlier turn: %s", text)
		}
		if err := setMonsterTempHP(f.camp, f.caster, 12); err != nil {
			t.Fatal(err)
		}
		damage("fire", false)
		if monsterHPNow(f.camp, f.caster) != hp-12 {
			t.Fatal("Temporary Hit Points control did not absorb the refresh packet")
		}
		next()
		if text := claw(); !strings.Contains(text, "Fear of Fire") {
			t.Errorf("new Fire damage on the yeti's turn did not refresh the penalty: %s", text)
		}
		reachYeti()
		next()
		if text := claw(); strings.Contains(text, "Fear of Fire") {
			t.Errorf("the next Yeti turn end did not remove the refreshed penalty: %s", text)
		}
	})
	t.Run("claw ice throw and multiattack choice", func(t *testing.T) {
		f := encounter(t)
		ironGolemMap(t, f, map[int]int{f.char: 1})
		target := fmt.Sprint(f.char)
		hp := ironGolemHeroHP(t, f)
		choice := fmt.Sprint(ironGolemAction(t, f, "Multiattack", map[string]interface{}{"target": target})["monster_action_result"])
		if !strings.Contains(choice, "choice between attacks") || !strings.Contains(choice, "Chilling Gaze") || ironGolemHeroHP(t, f) != hp {
			t.Errorf("Multiattack did not make its optional gaze and choice clear before rolling: %s", choice)
		}
		for _, action := range []string{"Claw", "Ice Throw"} {
			hp := ironGolemHeroHP(t, f)
			j := ironGolemAction(t, f, action, map[string]interface{}{"target": target})
			receipt, ok := j["monster_attack"].(map[string]interface{})
			text := fmt.Sprint(j["monster_action_result"])
			loss := hp - ironGolemHeroHP(t, f)
			if !ok || asInt(receipt["attacks"]) != 1 || asInt(receipt["damage"]) != loss {
				t.Fatalf("wrong attack or HP change: %v", j)
			}
			_, roll, found := strings.Cut(text, action+": ")
			totalText, armorText, hasArmor := strings.Cut(roll, " vs AC ")
			var total, armor int
			n, _ := fmt.Sscan(totalText, &total)
			m, _ := fmt.Sscan(armorText, &armor)
			if !found || !hasArmor || n != 1 || m != 1 || total < 12 || total > 31 {
				t.Fatalf("incorrect +11 attack roll receipt: %s", text)
			}
			hit := total == 31 || (total != 12 && total >= armor)
			if (asInt(receipt["hits"]) == 1) != hit {
				t.Fatalf("attack outcome disagrees with its Armor Class and die: %s", text)
			}
			if hit && (!strings.Contains(strings.ToLower(text), "cold damage") || loss < 11 || loss > 55) {
				t.Fatalf("missing or invalid hit damage: %s, %d", text, loss)
			}
		}
	})
	t.Run("chilling gaze failure success and immunity", func(t *testing.T) {
		f := encounter(t)
		normal := referenceSaveTarget(t, f, "Fresh gaze failure", -10, "", "")
		cold := referenceSaveTarget(t, f, "Fresh gaze cold immunity", -10, "Cold", "")
		success := referenceSaveTarget(t, f, "Fresh gaze success", 30, "", "")
		resistant := referenceSaveTarget(t, f, "Fresh gaze cold resistance", -10, "", "Cold")
		ironGolemMap(t, f, map[int]int{normal: 2, cold: 3, success: 4, resistant: 5})
		play := func(target int) string {
			return fmt.Sprint(ironGolemAction(t, f, "Chilling Gaze", map[string]interface{}{"target_ids": []int{target}, "confirm_targeting": true})["monster_action_result"])
		}
		if text := play(normal); !strings.Contains(text, "fails the Constitution saving throw") || !hasCondMarker(f.camp, normal, "paralyzed") || monsterHPNow(f.camp, normal) > 494 || monsterHPNow(f.camp, normal) < 464 {
			t.Errorf("ordinary failed gaze did not deal damage and paralyze: %s", text)
		}
		if text := play(cold); monsterHPNow(f.camp, cold) != 500 || hasCondMarker(f.camp, cold, "paralyzed") {
			t.Errorf("cold immunity failed to exclude gaze damage and paralysis: %s", text)
		}
		if text := play(resistant); monsterHPNow(f.camp, resistant) >= 500 || !hasCondMarker(f.camp, resistant, "paralyzed") {
			t.Errorf("cold resistance shouldn't exclude paralysis: %s", text)
		}
		if text := play(success); !strings.Contains(text, "succeeds") || monsterHPNow(f.camp, success) != 500 || hasCondMarker(f.camp, success, "paralyzed") {
			t.Errorf("ordinary successful gaze changed harm state: %s", text)
		}
		spent := false
		for _, effect := range spellEffectsOn(f.camp, success) {
			if effect.Kind == "effect_immune" && effect.CasterID == f.caster {
				spent = true
				if effect.RoundsLeft != 600 {
					t.Errorf("gaze immunity lasted %d rounds, want 600", effect.RoundsLeft)
				}
			}
		}
		if !spent {
			t.Error("successful gaze did not grant immunity")
		}
		if text := play(success); strings.Contains(text, "saving throw") || !strings.Contains(text, "immune") {
			t.Errorf("repeated gaze did not respect source immunity: %s", text)
		}
	})
	t.Run("cold breath full half and spent", func(t *testing.T) {
		f := encounter(t)
		failed := referenceSaveTarget(t, f, "Fresh breath failure", -10, "", "")
		success := referenceSaveTarget(t, f, "Fresh breath success", 30, "", "")
		outside := referenceSaveTarget(t, f, "Fresh breath exclusion", -10, "", "")
		ironGolemMap(t, f, map[int]int{failed: 2, success: 3, outside: 8})
		play := func() string {
			return fmt.Sprint(ironGolemAction(t, f, "Cold Breath (Recharge 6)", map[string]interface{}{"point": []int{6, 0}, "target_ids": []int{failed, success, outside}})["monster_action_result"])
		}
		text := play()
		loss := 500 - monsterHPNow(f.camp, failed)
		if loss < 10 || loss > 80 || 500-monsterHPNow(f.camp, success) != loss/2 || monsterHPNow(f.camp, outside) != 500 {
			t.Errorf("cold breath failed cone or damage checks: %s", text)
		}
		hp := monsterHPNow(f.camp, failed)
		if text = play(); !strings.Contains(text, "hasn't recharged yet") || monsterHPNow(f.camp, failed) != hp {
			t.Errorf("spent breath applied again: %s", text)
		}
	})
}

func TestPostgresMonsterSaveImmunityScopeAndDuration(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
		m.Actions = []srdMonsterEntry{
			{Name: "First Signal", Text: "Constitution Saving Throw: DC 15, one target within 30 feet. Failure: The target has the Prone condition. Success: The target is immune to this signal for 1 hour."},
			{Name: "Second Signal", Text: "Constitution Saving Throw: DC 15, one target within 30 feet. Failure: The target has the Prone condition. Success: The target is immune to this signal for 24 hours."},
			{Name: "Unspecified Signal", Text: "Constitution Saving Throw: DC 15, one target within 30 feet. Failure: The target has the Prone condition. Success: The target is immune to this signal for an unknown duration."},
		}
	})
	target := referenceSaveTarget(t, f, "Invented successful saver", 30, "", "")
	ironGolemMap(t, f, map[int]int{target: 2})
	play := func(action string) string {
		t.Helper()
		return fmt.Sprint(ironGolemAction(t, f, action, map[string]interface{}{"target_ids": []int{target}})["monster_action_result"])
	}
	wantSave := func(action string) {
		t.Helper()
		if result := play(action); !strings.Contains(result, "succeeds") {
			t.Fatalf("%s did not call for a save: %s", action, result)
		}
	}
	wantImmune := func(action string) {
		t.Helper()
		if result := play(action); strings.Contains(result, "saving throw") || !strings.Contains(result, "Immune to") {
			t.Fatalf("%s did not honor its stored immunity: %s", action, result)
		}
	}

	wantSave("First Signal")
	wantSave("Second Signal")
	wantImmune("First Signal")
	wantImmune("Second Signal")
	// Advance the real timer owner, then recheck through GM narration. The
	// public pass-time route refuses active combat.
	passSpellTime(f.camp, 599)
	wantImmune("First Signal")
	passSpellTime(f.camp, 1)
	wantSave("First Signal")
	wantImmune("Second Signal")
	wantSave("Unspecified Signal")
	wantSave("Unspecified Signal")
	if effectImmune(f.camp, target, f.caster, "Unspecified Signal") {
		t.Fatal("recorded a duration for an unspecified immunity")
	}
}

func TestPostgresAuraSaveImmunityUsesSharedDuration(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
		m.Traits = []srdMonsterEntry{
			{Name: "Invented Short Aura", Text: "Wisdom Saving Throw: DC 1, any enemy that starts its turn in a 10-foot Emanation. Failure: The target has the Prone condition. Success: The target is immune to this aura for 1 minute."},
			{Name: "Invented Long Aura", Text: "Wisdom Saving Throw: DC 1, any enemy that starts its turn in a 10-foot Emanation. Failure: The target has the Prone condition. Success: The target is immune to this aura for 24 hours."},
			{Name: "Invented Unknown Aura", Text: "Wisdom Saving Throw: DC 1, any enemy that starts its turn in a 10-foot Emanation. Failure: The target has the Prone condition. Success: The target is immune to this aura for an unknown duration."},
		}
	})
	if modifier, _, _ := saveModifier(f.camp, f.char, "wis"); modifier < 0 {
		t.Fatalf("fixture must always pass its Wisdom save, has %+d", modifier)
	}
	ironGolemMap(t, f, map[int]int{f.char: 1})
	heroTurn := func() {
		t.Helper()
		_, err := changeFight(f.camp, func(combat *fight) error {
			i, _ := combat.entry(f.char)
			if i < 0 || len(combat.Order) < 2 {
				return fmt.Errorf("fixture needs the hero and at least one other turn")
			}
			combat.Index = (i - 1 + len(combat.Order)) % len(combat.Order)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		path := fmt.Sprintf("/api/campaigns/%d/combat/next", f.camp)
		code, j := pgCall(t, "POST", path, f.gm, map[string]interface{}{})
		if code != 200 || currentTurnID(f.camp) != f.char {
			t.Fatalf("advance to hero: %d %v", code, j)
		}
		const instruction = "Confirm its duration"
		if !strings.Contains(fmt.Sprint(j["gm_notices"]), instruction) {
			t.Fatalf("GM did not receive the unknown-duration instruction: %v", j)
		}
		for _, route := range []string{"/api/my-turn", fmt.Sprintf("/api/campaigns/%d/combat/log", f.camp)} {
			if code, body := pgCall(t, "GET", route, f.player, nil); code != 200 || strings.Contains(fmt.Sprint(body), instruction) {
				t.Fatalf("player surface %s: %d %v", route, code, body)
			}
		}
	}
	rounds := func() map[string]int {
		result := map[string]int{}
		for _, e := range spellEffectsOn(f.camp, f.char) {
			if e.Kind == "effect_immune" && e.CasterID == f.caster {
				result[e.Detail] = e.RoundsLeft
			}
		}
		return result
	}
	heroTurn()
	if active := rounds(); active["Invented Short Aura"] != 10 || active["Invented Long Aura"] != 14400 {
		t.Fatalf("timed aura records: %v", active)
	}
	passSpellTime(f.camp, 10)
	if active := rounds(); active["Invented Short Aura"] != 0 || active["Invented Long Aura"] != 14390 {
		t.Fatalf("elapsed timer: %v", active)
	}
	heroTurn()
	if active := rounds(); active["Invented Short Aura"] != 10 || active["Invented Long Aura"] < 14389 || active["Invented Long Aura"] > 14390 {
		t.Fatalf("only the expired aura should have asked again: %v", active)
	}
}
