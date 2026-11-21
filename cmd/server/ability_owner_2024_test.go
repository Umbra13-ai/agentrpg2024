package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestNoOtherAbilityScoreWriter: a character's six score columns (and item_base_scores, the scores an attuned item gives back) are
// written only by the ability score owner (ability_owner_2024.go): a SQL statement that assigns one, a SET clause put together in Go
// ("str = $%d") or a column spliced into a SET (`SET `+col+` = `) anywhere else fails, unless that splice is one of the listed
// writers whose column is never a score.
func TestNoOtherAbilityScoreWriter(t *testing.T) {
	update := regexp.MustCompile(`(?is)UPDATE\s+characters\b(.*?)\bWHERE\b`)
	assign := regexp.MustCompile(`(?i)(\bSET\s+|,\s*)(str|dex|con|intl|wis|cha|item_base_scores)\s*=`)
	built := regexp.MustCompile(`"(str|dex|con|intl|wis|cha|item_base_scores) = `)
	splice := regexp.MustCompile("UPDATE characters SET [`\"]\\s*\\+\\s*([A-Za-z_.]+(?:\\([^)]*\\))?)\\s*\\+\\s*[`\"]\\s*=")
	notAScore := map[string]bool{ // file:expression of a spliced column that is never an ability score
		"resources_2024.go:col":          true, // class_resources_used and its kin
		"feature_picks_2024.go:p.column": true, // a feature pick's own column (divine_order ...)
		"actions.go:c.column":            true, // a once-per-turn flag
	}
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "ability_owner_2024.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		src := string(b)
		for _, m := range update.FindAllStringSubmatchIndex(src, -1) {
			if assign.MatchString(src[m[2]:m[3]]) {
				t.Errorf("%s:%d writes a character's ability score itself: use the ability score owner (ability_owner_2024.go)", f, strings.Count(src[:m[0]], "\n")+1)
			}
		}
		for n, l := range strings.Split(src, "\n") {
			if built.MatchString(l) {
				t.Errorf("%s:%d builds an ability score SET clause: use the ability score owner (ability_owner_2024.go)", f, n+1)
			}
			if m := splice.FindStringSubmatch(l); m != nil && !notAScore[f+":"+m[1]] {
				t.Errorf("%s:%d splices a column into a character SET (%s): if it can be a score, use the ability score owner; otherwise list it in notAScore", f, n+1, m[1])
			}
		}
	}
}

// The Shadow's Draining Swipe (SRD 5.2.1: "the target's Strength score decreases by 1d4. The target dies if this reduces that score
// to 0"): every hit route lowers the target's Strength through the ability score owner, a character's sheet and saves read the
// lowered score, Strength 0 kills, Greater Restoration gives the score back; a monster's and a companion's saves read their reduced
// score too.
func TestPostgresAbilityDrainOnEveryTarget(t *testing.T) {
	testPostgres(t)
	gm, player, camp, charID, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	m := compendiumFixture()
	m.Name, m.Type, m.Traits, m.Legendary, m.LegendaryIn = "Gloom Wisp", "Undead", nil, nil, ""
	m.Actions = []srdMonsterEntry{{Name: "Sapping Touch", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 1 (1d1) Necrotic damage, and the target’s Strength score decreases by 1d4. The target dies if this reduces that score to 0."}}
	victim := compendiumFixture()
	victim.Name, victim.Type, victim.Traits, victim.Legendary, victim.LegendaryIn, victim.DmgImmune, victim.Resistances = "Drained Ox", "Beast", nil, nil, "", "", ""
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"campaign_id": camp, "source_note": "invented drain fixture", "monsters": []srdMonster2024{m, victim}})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	keys := map[string]string{}
	for _, x := range imported["monsters"].([]interface{}) {
		mm := x.(map[string]interface{})
		key := fmt.Sprint(mm["monster_key"])
		keys[fmt.Sprint(mm["name"])] = key
		t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	}
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Gloom Wisp", "monster_key": keys["Gloom Wisp"], "hp": 100, "ac": 1, "initiative": -800},
		{"name": "Drained Ox", "monster_key": keys["Drained Ox"], "hp": 500, "ac": 1, "initiative": -801}}})
	pgExec(t, `UPDATE characters SET hp=500, max_hp=500, ac=1 WHERE id=$1`, charID)
	pgExec(t, `UPDATE companions SET hp=500, max_hp=500, ac=1 WHERE id=$1`, petID)
	db.Exec(`DELETE FROM ability_reductions WHERE creature_id = ANY($1)`, []int{charID, pet})
	setAbilityScores(charID, map[string]int{"str": 14})
	ox := monsterIDsByKeyOrName(camp, "Drained Ox")[0]
	hit := func(target string) string {
		t.Helper()
		for n := 0; n < 20; n++ {
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": "Gloom Wisp", "action": "Sapping Touch", "target": target}})
			if res := fmt.Sprint(r["monster_action_result"]); strings.Contains(res, "HIT") {
				return res
			}
		}
		t.Fatalf("no hit on %s", target)
		return ""
	}

	// a character: the column falls, the reply says so, the sheet and a Strength save read it
	res := hit(fmt.Sprint(charID))
	str := abilityScoreOf(charID, "str")
	if str < 10 || str > 13 || !strings.Contains(res, "Strength") || strings.Contains(res, "decreases by 1d4") {
		t.Fatalf("the drain on a character: Strength %d: %s", str, res)
	}
	if rs := openAbilityReductions(camp, charID, "str"); len(rs) != 1 || rs[0].Amount != 14-str {
		t.Errorf("the reduction record: %+v", rs)
	}
	if mod, _, _ := saveModifier(camp, charID, "str"); mod != (str-10)/2 && mod != (str-11)/2 {
		t.Errorf("the Strength save reads %d for Strength %d", mod, str)
	}
	_, sheet := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d", charID), player, nil)
	if !strings.Contains(fmt.Sprint(sheet), fmt.Sprintf("str:%d", str)) {
		t.Errorf("the sheet doesn't show Strength %d", str)
	}
	// Greater Restoration ends it: the score is back
	if ab, by, err := endAbilityReductions(camp, charID, ""); err != nil || ab != "str" || by != 14-str || abilityScoreOf(charID, "str") != 14 {
		t.Errorf("restoration: %s %d %v, Strength %d", ab, by, err, abilityScoreOf(charID, "str"))
	}
	// Strength 0 kills
	setAbilityScores(charID, map[string]int{"str": 1})
	hit(fmt.Sprint(charID))
	var dead bool
	db.QueryRow(`SELECT COALESCE(is_dead,false) FROM characters WHERE id=$1`, charID).Scan(&dead)
	if abilityScoreOf(charID, "str") != 0 || !dead {
		t.Errorf("Strength 0 didn't kill: Strength %d, dead %v", abilityScoreOf(charID, "str"), dead)
	}

	// a monster and a companion: their saves read the reduced score
	for _, tg := range []struct {
		name string
		id   int
	}{{"Drained Ox", ox}, {"React Pet", pet}} {
		before := creatureAbilityScore(camp, tg.id, "str")
		modBefore, _, _ := saveModifier(camp, tg.id, "str")
		hit(tg.name)
		after := creatureAbilityScore(camp, tg.id, "str")
		modAfter, _, _ := saveModifier(camp, tg.id, "str")
		if after >= before || after < before-4 || modAfter > modBefore || (before-after >= 2 && modAfter == modBefore && before%2 == 0) {
			t.Errorf("%s: Strength %d -> %d, save %d -> %d", tg.name, before, after, modBefore, modAfter)
		}
	}
	_ = time.Now
}
