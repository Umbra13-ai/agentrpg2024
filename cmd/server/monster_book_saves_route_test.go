package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPostgresBookSaveRoute(t *testing.T) {
	testPostgres(t)
	dir := os.Getenv("MM_FIXTURES_DIR")
	if dir == "" {
		t.Skip("MM_FIXTURES_DIR not set")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(files)
	_, gm := pgAgent(t, "book save gm")
	_, player := pgAgent(t, "book save player")
	call := func(method, path, token string, body interface{}) map[string]interface{} {
		t.Helper()
		c, j := pgCall(t, method, path, token, body)
		if c != 200 && c != 201 {
			t.Fatalf("%s %s %d %v", method, path, c, j)
		}
		return j
	}
	campaign := int(call("POST", "/api/campaigns", gm, map[string]interface{}{"name": "Book save route", "max_players": 2})["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", campaign)
	call("PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	character := int(call("POST", "/api/characters", player, pgRogue(fmt.Sprintf("Save Hero %d", time.Now().UnixNano()%1e9)))["character_id"].(float64))
	call("POST", cp+"/join", player, map[string]interface{}{"character_id": character})
	pgFinish(t, player, character)
	call("POST", cp+"/ready", player, nil)
	call("POST", cp+"/ready", gm, nil)
	call("POST", cp+"/start", gm, nil)
	var rows []string
	var played, failedSaves, passedSaves, rechargeChecked, conditionsChecked, stateChecked, gmFlagged, optionsRefused int
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			SourceNote string           `json:"source_note"`
			Monsters   []srdMonster2024 `json:"monsters"`
		}
		if json.Unmarshal(raw, &body) != nil || len(body.Monsters) != 1 {
			t.Fatalf("%s: unreadable", f)
		}
		m := body.Monsters[0]
		imported := call("POST", "/api/gm/compendium/monsters", gm, body)
		call("POST", cp+"/combat/start", gm, map[string]interface{}{})
		key := imported["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
		call("POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": m.Name, "monster_key": key, "initiative": 20}}})
		type playItem struct {
			e           srdMonsterEntry
			action, use string
		}
		var items []playItem
		for _, a := range m.Actions {
			if opts := splitSaveOptions(a.Text); len(opts) >= 2 { // "one of the following rays": refused until the GM names one, then each is played
				refused := fmt.Sprint(call("POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": campaign, "narration": "The creature acts.",
					"monster_action": map[string]interface{}{"monster": m.Name, "action": a.Name, "target": fmt.Sprint(character)}})["monster_action_result"])
				if !strings.Contains(refused, "Nothing was rolled") || !strings.Contains(refused, opts[0].Name) {
					t.Errorf("%s / %s: an action with %d effects was not refused before rolling: %s", m.Name, a.Name, len(opts), refused)
				}
				optionsRefused++
				for _, o := range opts {
					items = append(items, playItem{srdMonsterEntry{Name: a.Name + " / " + o.Name, Text: o.Text}, a.Name, o.Name})
				}
				continue
			}
			items = append(items, playItem{a, a.Name, ""})
		}
		for _, it := range items {
			e := it.e
			eff, ok := parseSaveEffect(e.Text)
			if !ok || len(eff.Damage) == 0 || strings.Contains(e.Text, "Attack Roll:") || e.Text == "" {
				continue
			}
			lo, hi := 0, 0
			for _, d := range eff.Damage {
				l, h := diceBounds(d.Dice)
				if strings.EqualFold(d.Type, "poison") { // the sweep character is a Dwarf (Dwarven Resilience): Resistance to Poison halves each total, rounded down
					l /= 2
				}
				lo, hi = lo+l, hi+h
			}
			var failSeen, passSeen bool
			for i := 0; i < 40 && !(failSeen && passSeen); i++ {
				pgExec(t, `UPDATE characters SET hp=1000,ac=10 WHERE id=$1`, character)
				pgExec(t, `DELETE FROM spell_effects WHERE lobby_id=$1 AND target_id=$2`, campaign, character) // 24-hour immunity to an effect
				changeConditions(0, character, func(c *conditionChange) error { return c.setExhaustion(0) })
				removeConditionsWhere(campaign, character, func(conditionRecord) bool { return true }) // earlier tries' Stunned, Paralyzed ... would fail the save automatically
				for _, id := range monsterIDsByKeyOrName(campaign, m.Name) {                           // a fresh use each time: nothing is spent
					st := loadMonsterState(campaign, id)
					st.RechargeSpent, st.LAOnce = nil, nil
					saveMonsterState(campaign, id, st)
					saveDayUses(campaign, id, nil)
				}
				// This sweep supplies the legal target and tests save resolution
				// without a map. Dedicated route tests exercise target eligibility.
				action := map[string]interface{}{"monster": m.Name, "action": it.action, "use": it.use,
					"target": fmt.Sprint(character), "confirm_targeting": true}
				r := call("POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": campaign, "narration": "The creature acts.",
					"monster_action": action})
				result := fmt.Sprint(r["monster_action_result"])
				var hp int
				db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, character).Scan(&hp)
				dealt := 1000 - hp
				if !strings.Contains(result, fmt.Sprintf("vs DC %d", eff.DC)) || !strings.Contains(result, eff.AbilityName+" saving throw") {
					t.Fatalf("%s / %s: reply does not show a %s save against DC %d: %s", m.Name, e.Name, eff.AbilityName, eff.DC, result)
				}
				if rechargeRe.MatchString(e.Name) { // the use spends it: the next try, before the monster's turn, must be refused with nothing rolled
					again := fmt.Sprint(call("POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": campaign, "narration": "The creature acts again.",
						"monster_action": action})["monster_action_result"])
					var hp2 int
					db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, character).Scan(&hp2)
					if !strings.Contains(again, "hasn't recharged yet") || hp2 != hp {
						t.Errorf("%s / %s: a spent Recharge action was used again: %s (HP %d -> %d)", m.Name, e.Name, again, hp, hp2)
					}
					rechargeChecked++
				}
				failed := strings.Contains(result, " fails the ")
				if eff.Extra != "" && (failed || strings.HasPrefix(eff.Extra, "Failure or Success")) { // text the shared rules do not automate must reach the GM, whole
					if !strings.Contains(result, "the GM") || !strings.Contains(result, eff.Extra) {
						t.Errorf("%s / %s: text left for the GM is missing from the reply (%q): %s", m.Name, e.Name, eff.Extra, result)
					}
					gmFlagged++
				}
				if failed {
					failSeen = true
					for _, c := range eff.Conditions { // a failed save names each condition it gave, or why the target did not get it
						if !strings.Contains(result, titleCase(c)) && !strings.Contains(result, "not "+c) {
							t.Errorf("%s / %s: failed save does not show the %s condition: %s", m.Name, e.Name, c, result)
						}
						conditionsChecked++
						if strings.Contains(result, "not "+c) {
							continue // the target was immune: no record to check
						}
						checkConditionState(t, m.Name, e.Name, c, eff, campaign, character)
						stateChecked++
					}
					checkConditionExpiry(t, m.Name, e.Name, eff, campaign, character)
					if dealt < lo || dealt > hi {
						t.Errorf("%s / %s: failed save dealt %d, the damage is %d-%d: %s", m.Name, e.Name, dealt, lo, hi, result)
					}
				} else {
					passSeen = true
					if !eff.HalfOnSuccess && dealt != 0 {
						t.Errorf("%s / %s: successful save with no half damage still dealt %d: %s", m.Name, e.Name, dealt, result)
					}
					if dealt > hi/2+len(eff.Damage) {
						t.Errorf("%s / %s: successful save dealt %d, more than half of at most %d: %s", m.Name, e.Name, dealt, hi, result)
					}
				}
			}
			if !failSeen {
				t.Errorf("%s / %s: no failed save in 40 tries", m.Name, e.Name)
			}
			played++
			if failSeen {
				failedSaves++
			}
			if passSeen {
				passedSaves++
			}
			rows = append(rows, fmt.Sprintf("| %s | %s | DC %d %s | damage %d-%d | fail seen %v, success seen %v |", m.Name, e.Name, eff.DC, eff.AbilityName, lo, hi, failSeen, passSeen))
		}
		call("POST", cp+"/combat/end", gm, nil)
	}
	summary := fmt.Sprintf("%d save actions with damage played through POST /api/gm/narrate against a character: %d with a failed save seen, %d with a successful save seen; a spent Recharge action was refused on reuse %d times; %d failed-save condition lines checked by name, %d stored condition records checked (source, duration, repeat save, expiry); %d replies carried leftover rules text to the GM in full; %d actions that offer several effects were refused before rolling until one was named.", played, failedSaves, passedSaves, rechargeChecked, conditionsChecked, stateChecked, gmFlagged, optionsRefused)
	t.Log(summary)
	if out := os.Getenv("MM_SAVE_ROUTE_OUT"); out != "" {
		body := "# Save route report\n\n" + summary + "\n\nChecks: the reply names the stat block's save ability and DC; a failed save deals an amount inside the parsed dice range; a success deals none or at most half. Conditions given on a failure are checked by name and in storage (source, duration, repeat save, expiry). Also checked: Recharge refusal on reuse, leftover rules text reaching the GM, multi-effect actions refused until one is named. Not checked here: the repeat saves actually being rolled at end of turn (condition_records_test.go), riders (pushes, Speed, Disadvantage: GM-applied), character-side Evasion and resistances (damageHP), monster targets (monster_save_targets_test.go).\n\n| Monster | Action | Save | Damage | Outcomes seen |\n|---|---|---|---|---|\n" + strings.Join(rows, "\n") + "\n"
		if err := os.WriteFile(out, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// diceBounds: the smallest and largest total of "4d10+6".
func diceBounds(d string) (int, int) {
	m := regexp.MustCompile(`^(\d+)d(\d+)(?:([+-])(\d+))?$`).FindStringSubmatch(d)
	if m == nil {
		return 0, 1 << 30
	}
	n, _ := strconv.Atoi(m[1])
	sides, _ := strconv.Atoi(m[2])
	flat, _ := strconv.Atoi(m[4])
	if m[3] == "-" {
		flat = -flat
	}
	return n + flat, n*sides + flat
}

// checkConditionState: what a failed save left in the character's stored conditions. The record must exist with the stat block's
// duration ("until the end of its next turn": 1 round; "for 1 minute": 10), the repeated save of "repeats the save at the end of each of
// its turns" with the stat block's ability and DC, and the "until the start of its next turn" note. A timed record must end when its
// rounds pass; an untimed one must stay.
func checkConditionState(t *testing.T, monster, action, cond string, eff saveEffect, campaign, character int) {
	t.Helper()
	recs, err := readConditionRecords(db, campaign, character)
	if err != nil {
		t.Fatal(err)
	}
	var rec *conditionRecord
	for i := range recs {
		if recs[i].Condition == cond {
			rec = &recs[i]
		}
	}
	if rec == nil {
		t.Errorf("%s / %s: the reply gave %s but no condition record is stored (stored: %+v)", monster, action, cond, recs)
		return
	}
	if rec.DurationRounds != eff.Rounds {
		t.Errorf("%s / %s: %s stored for %d rounds, the stat block says %d", monster, action, cond, rec.DurationRounds, eff.Rounds)
	}
	if eff.EndSave != (rec.EndSave != nil) || (rec.EndSave != nil && (rec.EndSave.DC != eff.DC || !strings.EqualFold(abilityNames[rec.EndSave.Ability], eff.AbilityName))) {
		t.Errorf("%s / %s: %s repeat save stored as %+v, the stat block says repeat=%v DC %d %s", monster, action, cond, rec.EndSave, eff.EndSave, eff.DC, eff.AbilityName)
	}
	if eff.UntilStart != (rec.Notes == "until_start_of_next_turn") {
		t.Errorf("%s / %s: %s notes %q, until-start-of-next-turn=%v", monster, action, cond, rec.Notes, eff.UntilStart)
	}
	if cond == "grappled" && eff.EscapeDC > 0 {
		ok := len(rec.Escapes) > 0
		for _, esc := range rec.Escapes {
			ok = ok && esc.DC == eff.EscapeDC
		}
		if !ok {
			t.Errorf("%s / %s: Grappled stored with escapes %+v, the stat block says escape DC %d", monster, action, rec.Escapes, eff.EscapeDC)
		}
	}
	if !strings.Contains(rec.Source, monster) {
		t.Errorf("%s / %s: %s source %q does not name the monster", monster, action, cond, rec.Source)
	}
}

// checkConditionExpiry: a timed record ends when its rounds pass (all of the action's conditions at once).
func checkConditionExpiry(t *testing.T, monster, action string, eff saveEffect, campaign, character int) {
	t.Helper()
	if eff.Rounds <= 0 || len(eff.Conditions) == 0 {
		return
	}
	passConditionTime(campaign, eff.Rounds, character)
	after, _ := readConditionRecords(db, campaign, character)
	for _, r := range after {
		for _, c := range eff.Conditions {
			if r.Condition == c {
				t.Errorf("%s / %s: %s still stored after its %d rounds passed", monster, action, c, eff.Rounds)
			}
		}
	}
}
