package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The one owner of an ability check's Advantage and Disadvantage (check_2024.go), through every stage-1 route: the GM's skill check, the
// GM's tool check, the character's escape from a condition, the GM's grapple escape for a creature of the fight, and the Hide action.

func checkRollType(t *testing.T, gm string, camp, hero int, path string, body map[string]interface{}) string {
	t.Helper()
	body["campaign_id"], body["character_id"], body["dc"] = camp, hero, 10
	c, j := pgCall(t, "POST", path, gm, body)
	if c != 200 {
		t.Fatalf("%s: %d %v", path, c, j)
	}
	return fmt.Sprint(j["roll_type"])
}

func TestPostgresCheckSourcesOnEveryRoute(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, drake := earlyRulesFixture(t, "Test Check Drake", []srdMonsterEntry{
		{Name: "Cacophony", Text: "Wisdom Saving Throw: DC 30, one creature. Failure: The target has the Deafened condition until the start of the swarm’s next turn. While Deafened, the target also has Disadvantage on ability checks and attack rolls."},
		{Name: "Dazing Strike", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 9 (2d6 + 2) Bludgeoning damage, and the target has Disadvantage on attack rolls and ability checks until the end of its next turn."},
	})
	skill := func() string {
		return checkRollType(t, gm, camp, hero, "/api/gm/skill-check", map[string]interface{}{"skill": "athletics"})
	}
	tool := func() string {
		return checkRollType(t, gm, camp, hero, "/api/gm/tool-check", map[string]interface{}{"tool": "thieves' tools", "ability": "dex"})
	}
	both := func(label, want string) {
		t.Helper()
		if got := skill(); !strings.HasPrefix(got, want) {
			t.Errorf("%s: the skill check rolled %q, want %q", label, got, want)
		}
		if got := tool(); !strings.HasPrefix(got, want) {
			t.Errorf("%s: the tool check rolled %q, want %q", label, got, want)
		}
	}
	pgExec(t, `UPDATE characters SET species='Human' WHERE id=$1`, hero)
	both("no source", "normal")

	// Poisoned: both routes (the tool check had it already; the owner gives both the same list)
	addCondition(camp, hero, condAdd{Condition: "poisoned", Source: "test"})
	both("Poisoned", "disadvantage")
	endConditionRecords(recordsOf(camp, hero, "poisoned"))
	both("Poisoned ended", "normal")

	// Frightened: only while its source is in sight (the battle map); no map or no source: the GM's ruling, nothing assumed
	addCondition(camp, hero, condAdd{Condition: "frightened", Source: "test", SourceID: drake})
	both("Frightened with no map", "normal")
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	putMap := func(heroX int) {
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"........"}, Tokens: []mapToken{
			{ID: drake, Name: "drake", Position: mapSquare{0, 0}, Size: 1}, {ID: hero, Name: "hero", Position: mapSquare{heroX, 0}, Size: 1}}}, "forced_movement": true}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	putMap(3)
	both("Frightened with the source in sight", "disadvantage")
	endConditionRecords(recordsOf(camp, hero, "frightened"))
	both("Frightened ended", "normal")

	putMap(1) // next to the drake: within its reach
	// "Disadvantage on ... ability checks until ...": a hit's rider and a failed save's lasting effect, each ended with its time or its record
	for try := 0; try < 40 && !hasCondMarker(camp, hero, "disadv_checks"); try++ {
		narrateHit(t, gm, camp, drake, "Dazing Strike", fmt.Sprint(hero), "")
	}
	if !hasCondMarker(camp, hero, "disadv_checks") {
		t.Fatal("the hit gave no disadv_checks marker")
	}
	both("a hit's check rider", "disadvantage")
	removeConditionsWhere(camp, hero, func(r conditionRecord) bool { return r.Condition == "disadv_checks" || r.Condition == "disadv_attacks" })
	pgExec(t, `UPDATE characters SET wis=1 WHERE id=$1`, hero)
	for try := 0; try < 40 && !hasCondMarker(camp, hero, "disadv_checks"); try++ {
		narrateAs(t, gm, camp, drake, "Cacophony", fmt.Sprint(hero), "")
	}
	if !hasCondMarker(camp, hero, "disadv_checks") || !hasCondMarker(camp, hero, "disadv_attacks") {
		t.Fatalf("Cacophony: checks %v attacks %v", hasCondMarker(camp, hero, "disadv_checks"), hasCondMarker(camp, hero, "disadv_attacks"))
	}
	both("Cacophony's Deafened", "disadvantage")
	endConditionRecords(recordsOf(camp, hero, "deafened"))
	if hasCondMarker(camp, hero, "disadv_checks") || hasCondMarker(camp, hero, "disadv_attacks") {
		t.Error("the check and attack riders outlived the Deafened record")
	}
	both("Deafened ended", "normal")

	// the GM's grapple escape for a creature of the fight: Sunlight Sensitivity (the Kobold Warrior) and a holder's own words (the Mimic)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Check Kobold", "monster_key": "kobold-warrior", "hp": 1000, "ac": 1, "initiative": -700},
		{"name": "Check Mimic", "monster_key": "mimic", "hp": 1000, "ac": 1, "initiative": -701}}})
	kobold, mimic := monsterIDsByKeyOrName(camp, "Check Kobold")[0], monsterIDsByKeyOrName(camp, "Check Mimic")[0]
	escape := func(target int) string {
		c, j := pgCall(t, "POST", "/api/gm/escape-grapple", gm, map[string]interface{}{"campaign_id": camp, "target_id": target})
		if c != 200 {
			t.Fatalf("escape-grapple: %d %v", c, j)
		}
		return fmt.Sprint(j["result"])
	}
	holdEscape := []conditionEscape{{Label: "Strength (Athletics) DC 30", Ability: "Strength", Skill: "Athletics", DC: 30}}
	addCondition(camp, kobold, condAdd{Condition: "grappled", Source: "test", SourceID: drake, Escapes: holdEscape})
	pgCall(t, "POST", "/api/gm/set-lighting", gm, map[string]interface{}{"campaign_id": camp, "lighting": "bright"})
	if r := escape(kobold); strings.Contains(r, "Disadvantage") {
		t.Errorf("the Kobold in bright light: %s", r)
	}
	pgCall(t, "POST", "/api/gm/set-lighting", gm, map[string]interface{}{"campaign_id": camp, "lighting": "sunlight"})
	if r := escape(kobold); !strings.Contains(r, "Disadvantage (Sunlight Sensitivity)") {
		t.Errorf("the Kobold in sunlight should escape with Disadvantage: %s", r)
	}
	pgCall(t, "POST", "/api/gm/set-lighting", gm, map[string]interface{}{"campaign_id": camp, "lighting": "bright"})
	addCondition(camp, hero, condAdd{Condition: "grappled", Source: "Check Mimic's Pseudopod", SourceID: mimic, Escapes: holdEscape})
	if r := escape(hero); !strings.Contains(r, "Disadvantage (its holder's own words") {
		t.Errorf("escaping a Mimic's grapple should have Disadvantage: %s", r)
	}
	endConditionRecords(recordsOf(camp, hero, "grappled"))
	addCondition(camp, hero, condAdd{Condition: "grappled", Source: "test", SourceID: drake, Escapes: holdEscape})
	if r := escape(hero); strings.Contains(r, "Disadvantage") {
		t.Errorf("escaping a grapple whose holder says nothing: %s", r)
	}
	// Exhaustion's -2 per level, on a creature that is not a character (this route had none)
	addCondition(camp, kobold, condAdd{Condition: "exhaustion:3", Source: "test"})
	if checkExhaustion(camp, kobold) != 6 {
		t.Errorf("Exhaustion 3 on a monster should be -6, got -%d", checkExhaustion(camp, kobold))
	}
}

// Hide: the monster's Stealth check reads the same owner (Sunlight Sensitivity, a check rider, Exhaustion).
func TestPostgresMonsterHideReadsTheCheckOwner(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, _ := earlyRulesFixture(t, "Test Hide Drake", []srdMonsterEntry{{Name: "Claw", Text: "Melee Attack Roll: +5, reach 5 ft. Hit: 5 (1d6 + 2) Slashing damage."}})
	_ = hero
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Hide Kobold", "monster_key": "kobold-warrior", "hp": 1000, "ac": 1, "initiative": -710}}})
	kobold := monsterIDsByKeyOrName(camp, "Hide Kobold")[0]
	pgCall(t, "POST", "/api/gm/set-lighting", gm, map[string]interface{}{"campaign_id": camp, "lighting": "sunlight"})
	line := attemptMonsterHide(camp, kobold, "Hide Kobold", "Nimble Escape", "The kobold takes the Hide action.", true)
	if !strings.Contains(line, "Sunlight Sensitivity: Disadvantage") {
		t.Errorf("the Kobold's Hide in sunlight: %s", line)
	}
	pgCall(t, "POST", "/api/gm/set-lighting", gm, map[string]interface{}{"campaign_id": camp, "lighting": "bright"})
	addCondition(camp, kobold, condAdd{Condition: "disadv_checks", Source: "test", Rounds: 5})
	if line := attemptMonsterHide(camp, kobold, "Hide Kobold", "Nimble Escape", "The kobold takes the Hide action.", true); !strings.Contains(line, "Disadvantage") {
		t.Errorf("the Kobold's Hide with a check rider: %s", line)
	}
}

// The one ability check d20 (check_2024.go rollCheckD20): no route rolls a check's d20 itself. Every d20 rolled in the server outside
// game.AttackRoll, game.SavingThrow and rollCheckD20 must be in a function listed here with what it is for: a check not yet moved onto
// checkCtx (the plan's stage 2 and 3), a reroll of a check the owner rolled (Heroic Inspiration, the Luck Blade), or not a check at all
// (initiative, an item's last charge, the dice roller). A listed function that no longer rolls fails too: a moved route leaves the list.
func TestNoOtherCheckRoll(t *testing.T) {
	notACheck := map[string]string{
		// rerolls of a check rolled by the owner
		"gm_checks.go:handleGMSkillCheck": "Heroic Inspiration's reroll of a check rolled by the owner",
		"gm_checks.go:handleGMToolCheck":  "Heroic Inspiration's reroll of a check rolled by the owner",
		"hide_2024.go:attemptHide":        "the Luck Blade's reroll of a check rolled by the owner",
		// checks not yet on the owner (stage 2: other routes, stage 3: Dispel Magic)
		"actions.go:resolveActionWithBonusDash":        "stage 2: Dexterity (Sleight of Hand) check to pick a lock",
		"actions.go:resolveStabilizeAction":            "stage 2: Wisdom (Medicine) check",
		"actions_checks_2024.go:resolveCheckAction":    "stage 2: Search, Study and Influence checks",
		"buff_spells_2024.go:buffCheckRoll":            "stage 2: Hide and Search read it until they move",
		"buff_spells_2024.go:dispelSpellEffects":       "stage 3: Dispel Magic's ability check",
		"gm_checks.go:handleGMContestedCheck":          "stage 2: contested checks",
		"gm_group_check_2024.go:handleGMGroupCheck":    "stage 2: group checks",
		"gm_spells.go:handleGMDispelMagic":             "stage 3: Dispel Magic's ability check",
		"legendary_items_2024.go:sphereOfAnnihilation": "stage 2: an Intelligence (Arcana) check",
		"magic_item_use_2024.go:planItemCast":          "stage 2: a Spell Scroll's ability check",
		"toolbox_more_2024.go:jumpAction":              "stage 2: a Strength (Athletics) check",
		// not checks
		"attack_target.go:resolveAttackOnMonster":    "the Luck Blade's reroll of an attack roll, judged by game.AttackRoll",
		"combat_handlers.go:handleCombatAdd":         "Initiative",
		"combat_handlers.go:handleCombatStart":       "Initiative",
		"combat_handlers.go:handleRoll":              "the public dice roller",
		"legendary_items_2024.go:addMonstersToFight": "Initiative",
		"magic_item_use_2024.go:spendCharges":        "the d20 for an item's last charge",
		"spell_resolution.go:resolveSpellOnMonster":  "Seeking Spell's reroll of an attack roll, judged by game.AttackRoll",
		"time_conditions_2024.go:handleGMRoll":       "the GM's dice roller",
		"check_2024.go:rollCheckD20":                 "the one check d20",
	}
	d20 := regexp.MustCompile(`RollDie\(20\)|RollWith(Dis)?[Aa]dvantage\(`)
	funcName := regexp.MustCompile(`^func (?:\([^)]*\) )?(\w+)`)
	files, _ := filepath.Glob("*.go")
	rolls := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		fn := ""
		for n, line := range strings.Split(string(b), "\n") {
			if m := funcName.FindStringSubmatch(line); m != nil {
				fn = m[1]
			}
			if d20.MatchString(line) && !strings.HasPrefix(strings.TrimSpace(line), "//") {
				rolls[f+":"+fn] = true
				if _, ok := notACheck[f+":"+fn]; !ok {
					t.Errorf("%s:%d (%s) rolls a d20 itself: an ability check is a checkCtx (check_2024.go) rolled by rollCheckD20; anything else goes in this test's list with what it is", f, n+1, fn)
				}
			}
		}
	}
	for k := range notACheck {
		if !rolls[k] {
			t.Errorf("%s is listed as rolling a d20 and no longer does: remove it from the list", k)
		}
	}
}
