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

// The play-test reports of Nov 16 (Drive Issue Tracker), each through the server where it can be.

func pgCleric(name string) map[string]interface{} {
	return map[string]interface{}{"name": name, "class": "cleric", "background": "sage", "species": "dwarf", "ability_method": "standard_array",
		"str": 13, "dex": 10, "con": 14, "int": 12, "wis": 15, "cha": 8, "ability_increases": map[string]int{"wis": 2, "con": 1},
		"skill_proficiencies": []string{"insight", "medicine"}, "class_options": map[string]interface{}{"divine_order": "thaumaturge"},
		"background_feat_choices": map[string]interface{}{"list": "wizard", "cantrips": []string{"light", "mage-hand"}, "spell": "magic-missile", "spellcasting_ability": "int"},
		"background_equipment":    "a", "starting_equipment": "a", "extra_languages": []string{"Elvish", "Goblin"},
		"alignment": "Neutral Good", "backstory": "One.\n\nTwo.\n\nThree."}
}

func TestPostgresPlaytestReports(t *testing.T) {
	testPostgres(t)
	gm, gmTok := pgAgent(t, "pg pt gm")
	_, plTok := pgAgent(t, "pg pt player")
	c, j := pgCall(t, "POST", "/api/characters", plTok, pgCleric(fmt.Sprintf("Pg Thaum %d", gm)))
	if c != 200 {
		t.Fatalf("cleric: %d %v", c, j)
	}
	char := int(j["character_id"].(float64))
	// I-ROWAN-7: a spellcaster with a Shield keeps the hand free, and no weapon is left "held" (sheet, equipment and shop agree)
	var mainHand string
	db.QueryRow(`SELECT COALESCE(equipped_main_hand, '') FROM characters WHERE id = $1`, char).Scan(&mainHand)
	for _, it := range inventoryOf(char) {
		if _, _, isWeapon := findWeapon(itemStr(it, "name")); isWeapon && itemStr(it, "location") == "held" && mainHand == "" {
			t.Errorf("%s is held while the main hand is empty", itemStr(it, "name"))
		}
	}
	_, j = pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Playtest %d", gm), "max_players": 4})
	camp := int(j["campaign_id"].(float64))
	if c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), plTok, map[string]interface{}{"character_id": char}); c != 200 {
		t.Fatalf("join: %d %v", c, j)
	}

	// I-ROWAN-1: seated in a campaign that hasn't started
	c, j = pgCall(t, "GET", fmt.Sprintf("/api/my-turn?campaign_id=%d", camp), plTok, nil)
	if j["status"] != "campaign_not_started" || j["error"] != nil || j["how_to_join"] != nil {
		t.Errorf("my-turn in a recruiting campaign: %d %v", c, j)
	}
	// I-GM-2: the GM's status while recruiting
	c, j = pgCall(t, "GET", "/api/gm/status", gmTok, nil)
	if c != 200 || !strings.Contains(fmt.Sprint(j["gm_tasks"]), "recruiting") {
		t.Errorf("gm/status while recruiting: %d %v", c, j)
	}
	// I-GM-3: the GM starts a character higher before the start
	if c, j = pgCall(t, "POST", "/api/gm/starting-level", gmTok, map[string]interface{}{"character_id": char, "level": 11}); c != 200 {
		t.Fatalf("starting-level while recruiting: %d %v", c, j)
	}
	// I-GM-5/6: the GM sees what is left to choose
	if _, j = pgCall(t, "GET", "/api/gm/status", gmTok, nil); j["unfinished_characters"] == nil {
		t.Errorf("gm/status doesn't list the unfinished level 11 character: %v", j["gm_tasks"])
	}
	// I-ALDEN-4: the setup's spell ceiling is the character's slots
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/setup", char), plTok, nil)
	if !strings.Contains(fmt.Sprint(j["steps"]), "highest_spell_level_you_can_cast:6") {
		t.Errorf("a Cleric 11's setup doesn't offer level 6 spells")
	}
	if steps := fmt.Sprint(j["steps"]); !strings.Contains(steps, "heal") || !strings.Contains(steps, "harm") {
		t.Errorf("a Cleric 11's setup doesn't list Heal and Harm (level 6)")
	}
	if c, _ := pgCall(t, "POST", fmt.Sprintf("/api/characters/%d/prepare", char), plTok, map[string]interface{}{"spells": []string{"heal"}}); c != 200 {
		t.Errorf("a Cleric 11 can't prepare Heal (level 6): %d", c)
	}
	if c, _ := pgCall(t, "POST", fmt.Sprintf("/api/characters/%d/prepare", char), plTok, map[string]interface{}{"spells": []string{"regenerate"}}); c == 200 {
		t.Errorf("a Cleric 11 prepared Regenerate (level 7)")
	}
	// I-ROWAN-2: the portrait is optional (Matt, Nov 16)
	for _, st := range j["steps"].([]interface{}) {
		if m := st.(map[string]interface{}); m["key"] == "portrait" && m["optional"] != true {
			t.Errorf("the portrait step isn't optional: %v", m["optional"])
		}
	}
	// I-ROWAN-2: setup and the sheet agree on character_finished
	_, sheet := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d", char), plTok, nil)
	if fmt.Sprint(j["character_finished"]) != fmt.Sprint(sheet["character_finished"]) {
		t.Errorf("setup says finished %v, the sheet %v", j["character_finished"], sheet["character_finished"])
	}
	// I-ALDEN-3: the sheet's Arcana includes the Thaumaturge bonus (Int 12: +1, proficiency +4 at 11, Wisdom 17: +3)
	sb, _ := sheet["skill_bonuses"].(map[string]interface{})
	if sb["arcana"] != float64(1+4+3) || sb["religion"] != float64(1+3) {
		t.Errorf("skill_bonuses: arcana %v, religion %v", sb["arcana"], sb["religion"])
	}
	// I-ROWAN-3: a prepare reply has no slots_remaining
	_, j = pgCall(t, "POST", fmt.Sprintf("/api/characters/%d/prepare", char), plTok, map[string]interface{}{"spells": []string{"bless"}})
	if _, has := j["slots_remaining"]; has {
		t.Errorf("prepare still answers slots_remaining: %v", j)
	}
	// I-ROWAN-8: party chat with the campaign in the query string
	if c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/messages?campaign_id=%d", camp), plTok, map[string]interface{}{"message": "hello"}); c != 200 {
		t.Errorf("chat with ?campaign_id: %d %v", c, j)
	}
	if c, j = pgCall(t, "POST", "/api/campaigns/messages", plTok, map[string]interface{}{"message": "hello"}); c != 400 || j["error"] != "campaign_id_required" {
		t.Errorf("chat naming no campaign: %d %v", c, j)
	}
	// I-ROWAN-6: the exploration timeout isn't advertised while the schedule is on
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/campaigns/%d/table-rules", camp), "", nil)
	if strings.Contains(fmt.Sprint(j["table_rules"]), "inactive for 12 hours is skipped") || !strings.Contains(fmt.Sprint(j["table_rules"]), "stand-in") {
		t.Errorf("table rules still say a player is skipped after 12 hours under the schedule")
	}
}

// I-ROWAN-7: the equipped hands have one writer, which keeps the inventory's "held" marks in step.
func TestNoOtherHandWriter(t *testing.T) {
	re := regexp.MustCompile(`(?i)equipped_(main|off)_hand\s*=\s*(\$|NULL|COALESCE)`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, _ := os.ReadFile(f)
		for i, line := range strings.Split(string(src), "\n") {
			if re.MatchString(line) && !strings.Contains(line, "changeHands(") && !strings.Contains(strings.TrimSpace(line), "//") {
				// a statement continued from a changeHands( line above is fine
				prev := strings.Split(string(src), "\n")
				near := false
				for k := i - 1; k >= 0 && k >= i-3; k-- {
					near = near || strings.Contains(prev[k], "changeHands(")
				}
				if near {
					continue
				}
				t.Errorf("%s:%d writes a hand without changeHands: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// I-GM-9 and I-ALDEN-5: the whole submission text; a round turn is open only until it is sent.
func TestSubmissionTextAndRoundTurn(t *testing.T) {
	long := strings.Repeat("Alden studies the circle. ", 20)
	if w := submissionWhat("POST", "/api/action", []byte(`{"action":"declare","description":"`+long+`"}`)); !strings.Contains(w, strings.TrimSpace(long)) {
		t.Errorf("the submission text is cut: %q", w)
	}
	for status, open := range map[string]bool{statusNotSubmitted: true, statusStandIn: true, statusSubmitted: false, statusResolved: false, statusWaiting: false} {
		if roundTurnOpen(status) != open {
			t.Errorf("roundTurnOpen(%s) = %v", status, !open)
		}
	}
}

// I-ALDEN-7: a Weapon Mastery step's suggested request fills every open pick, and sending it closes the choice; I-ALDEN-6: the
// builder's note says a new character takes the seat in a campaign that hasn't started.
func TestPostgresMasterySuggestionFillsTheChoice(t *testing.T) {
	testPostgres(t)
	gm, gmTok := pgAgent(t, "pg mastery gm")
	_, tok := pgAgent(t, "pg mastery player")
	body := pgRogue(fmt.Sprintf("Pg Fighter %d", gm))
	body["class"], body["skill_proficiencies"], body["weapon_masteries"] = "fighter", []string{"acrobatics", "perception"}, []string{"greatsword", "javelin", "dagger"}
	c, j := pgCall(t, "POST", "/api/characters", tok, body)
	if c != 200 {
		t.Fatalf("fighter: %d %v", c, j)
	}
	id := int(j["character_id"].(float64))
	if c, j = pgCall(t, "POST", fmt.Sprintf("/api/characters/%d/starting-level", id), tok, map[string]interface{}{"level": 11}); c != 200 {
		t.Fatalf("starting level: %d %v", c, j)
	}
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/setup", id), tok, nil)
	var step map[string]interface{}
	for _, s := range j["steps"].([]interface{}) {
		if m := s.(map[string]interface{}); m["key"] == "weapon_mastery" {
			step = m
		}
	}
	if step == nil {
		t.Fatal("no Weapon Mastery step for a Fighter 11 with 3 masteries")
	}
	rq := step["request"].(map[string]interface{})
	b := rq["body"]
	if b == nil {
		b = rq["body_like"]
	}
	list := b.(map[string]interface{})["weapon_masteries"].([]interface{})
	if len(list) != 5 {
		t.Fatalf("the suggestion lists %d masteries, a Fighter 11 has 5: %v", len(list), list)
	}
	if c, j = pgCall(t, "POST", "/api/characters/weapon-masteries", tok, b); c != 200 {
		t.Fatalf("sending the suggestion: %d %v", c, j)
	}
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/setup", id), tok, nil)
	for _, s := range j["steps"].([]interface{}) {
		if m := s.(map[string]interface{}); m["key"] == "weapon_mastery" && m["done"] != true {
			t.Errorf("the suggestion didn't close the choice: %v", m["why"])
		}
	}

	// the builder's note before the start
	_, j = pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Note %d", gm), "max_players": 2, "min_level": 11, "max_level": 11})
	camp := int(j["campaign_id"].(float64))
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), tok, map[string]interface{}{"character_id": id}); c != 200 {
		t.Fatalf("join: %d %v", c, j)
	}
	if note := campaignsNote(func() int {
		var a int
		db.QueryRow(`SELECT agent_id FROM characters WHERE id = $1`, id).Scan(&a)
		return a
	}()); !strings.Contains(note, "takes") || strings.Contains(note, "GM removes") {
		t.Errorf("the builder's note before the start: %q", note)
	}
}

// I-ALDEN-8: the warning and the Ability Score Improvement suggestion read the class's every Primary Ability.
func TestPrimaryAbilityWarning(t *testing.T) {
	dexFighter := map[string]int{"str": 10, "dex": 17, "con": 15, "int": 12, "wis": 13, "cha": 8}
	if w := primaryAbilityWarning("fighter", dexFighter); w != "" {
		t.Errorf("a Dexterity Fighter is warned: %q", w)
	}
	if p := primaryByScore("fighter", dexFighter); p[0] != "dex" {
		t.Errorf("a Dexterity Fighter's suggestion raises %s first", p[0])
	}
	strWizard := map[string]int{"str": 17, "dex": 13, "con": 14, "int": 12, "wis": 10, "cha": 8}
	if w := primaryAbilityWarning("wizard", strWizard); !strings.Contains(w, "Intelligence") {
		t.Errorf("a Wizard whose best score is Strength isn't warned: %q", w)
	}
	for class := range classPrimary { // every class: its primary ability highest means no warning
		sc := map[string]int{"str": 10, "dex": 10, "con": 10, "int": 10, "wis": 10, "cha": 10}
		for _, p := range classPrimary[class] {
			sc[p] = 16
		}
		if w := primaryAbilityWarning(class, sc); w != "" {
			t.Errorf("%s with its primary abilities highest is warned: %q", class, w)
		}
	}
}

// I-ALDEN-9 and I-ROWAN-11: a portrait the human declined isn't described as a portrait; the join reply names the GM's ready too.
func TestPostgresPortraitSkipAndJoinWording(t *testing.T) {
	testPostgres(t)
	gm, gmTok := pgAgent(t, "pg word gm")
	_, tok := pgAgent(t, "pg word player")
	_, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Words %d", gm)))
	id := int(j["character_id"].(float64))
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/characters/%d/portrait", id), tok, map[string]interface{}{"my_human_declined": "no thanks, keep the placeholder"}); c != 200 {
		t.Fatalf("declined: %d %v", c, j)
	}
	if st := portraitSetupStep(id); !st.Done || strings.Contains(st.Why, "has a portrait") {
		t.Errorf("after the human's no: done %v, %q", st.Done, st.Why)
	}
	_, j = pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Words %d", gm), "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	_, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), tok, map[string]interface{}{"character_id": id})
	if !strings.Contains(fmt.Sprint(j["message"]), startCondition) || !strings.Contains(fmt.Sprint(j["next_steps"]), startCondition) {
		t.Errorf("the join reply doesn't say the GM must be ready too: %v / %v", j["message"], j["next_steps"])
	}
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/gm/status?campaign_id=%d", camp), gmTok, nil)
	if j["battle_recommended"] == true {
		t.Error("a battle is recommended while the campaign recruits")
	}
}

// I-ROWAN-7 (retested still broken): every view that says what is in hand (the equipment list, the sheet, the shop) agrees with the
// hands through creation, auto-equip with an empty hand, equipping and unequipping; and a character saved before the inventory writer
// synced the hands is brought in line at the server's start (repairHeldMarks).
func TestPostgresHeldWeaponViewsAgree(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "pg held views")
	_, j := pgCall(t, "POST", "/api/characters", tok, pgCleric(fmt.Sprintf("Pg Held %d", time.Now().UnixNano()%1e9)))
	id := int(j["character_id"].(float64))
	check := func(step string, wantHeld bool) {
		t.Helper()
		_, e := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/equipment", id), tok, nil)
		_, s := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d", id), tok, nil)
		_, sh := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/shop?category=gear", id), tok, nil)
		listed := strings.Contains(fmt.Sprint(e["held"]), "Mace")
		inHand := s["equipment"].(map[string]interface{})["main_hand"] != nil
		shopSaysHeld := strings.Contains(fmt.Sprint(sh["offers_for_your_items"]), "You hold Mace")
		if listed != wantHeld || inHand != wantHeld || shopSaysHeld != wantHeld {
			t.Errorf("%s: equipment lists it held %v, sheet has it in hand %v, shop says held %v (want %v)", step, listed, inHand, shopSaysHeld, wantHeld)
		}
	}
	check("after creation", false)
	pgCall(t, "POST", "/api/characters/auto-equip", tok, map[string]interface{}{"answer": "yes", "character_id": id, "choices": map[string]interface{}{"main_hand": "none"}})
	check("after auto-equip with the hand empty", false)
	pgCall(t, "POST", "/api/characters/equip-weapon", tok, map[string]interface{}{"character_id": id, "weapon": "mace", "slot": "main_hand"})
	check("after equipping the Mace", true)
	pgCall(t, "POST", "/api/characters/unequip-weapon", tok, map[string]interface{}{"character_id": id, "slot": "main_hand"})
	check("after unequipping it", false)

	// a character saved before the sync: the Mace stored "held", the hand empty (written as the old server left it)
	pgExec(t, `UPDATE characters SET inventory = (SELECT jsonb_agg(CASE WHEN e->>'name' = 'Mace' THEN jsonb_set(e, '{location}', '"held"') ELSE e END)
		FROM jsonb_array_elements(inventory::jsonb) e), equipped_main_hand = NULL WHERE id = $1`, id)
	repairHeldMarks()
	check("after the start-up repair of a stale character", false)
}
