package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Every spell a CR 10+ Spellcasting entry lists is played through POST /api/gm/narrate (monster_action Spellcasting, use: the spell)
// against a character. Each reply is sorted: resolved by the shared spell rules (a save, an attack roll, a named effect), handed to the
// GM with the spell named, or silent. Silent (no reply naming the spell) fails the test.
func TestPostgresBookSpellcastingRoute(t *testing.T) {
	testPostgres(t)
	dir := os.Getenv("MM_FIXTURES_DIR")
	if dir == "" {
		t.Skip("MM_FIXTURES_DIR not set")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(files)
	_, gm := pgAgent(t, "book spell gm")
	_, player := pgAgent(t, "book spell player")
	call := func(method, path, token string, body interface{}) map[string]interface{} {
		t.Helper()
		c, j := pgCall(t, method, path, token, body)
		if c != 200 && c != 201 {
			t.Fatalf("%s %s %d %v", method, path, c, j)
		}
		return j
	}
	campaign := int(call("POST", "/api/campaigns", gm, map[string]interface{}{"name": "Book spell route", "max_players": 2})["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", campaign)
	call("PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	character := int(call("POST", "/api/characters", player, pgRogue(fmt.Sprintf("Spell Hero %d", time.Now().UnixNano()%1e9)))["character_id"].(float64))
	call("POST", cp+"/join", player, map[string]interface{}{"character_id": character})
	pgFinish(t, player, character)
	call("POST", cp+"/ready", player, nil)
	call("POST", cp+"/ready", gm, nil)
	call("POST", cp+"/start", gm, nil)
	counts := map[string]int{}
	var rows []string
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		var body struct {
			SourceNote string           `json:"source_note"`
			Monsters   []srdMonster2024 `json:"monsters"`
		}
		if json.Unmarshal(raw, &body) != nil || len(body.Monsters) != 1 {
			t.Fatalf("%s: unreadable", f)
		}
		m := body.Monsters[0]
		var entry srdMonsterEntry
		for _, a := range m.Actions {
			if a.Name == "Spellcasting" {
				entry = a
			}
		}
		spells := spellNamesOf(entry.Text)
		if len(spells) == 0 {
			continue
		}
		body.SourceNote = "supplied reconstruction text, spell route test"
		imported := call("POST", "/api/gm/compendium/monsters", gm, body)
		call("POST", cp+"/combat/start", gm, map[string]interface{}{})
		key := imported["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
		call("POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": m.Name, "monster_key": key, "initiative": 20}}})
		var notes []string
		for _, sp := range spells {
			pgExec(t, `UPDATE characters SET hp=1000,ac=10 WHERE id=$1`, character)
			removeConditionsWhere(campaign, character, func(conditionRecord) bool { return true })
			r := call("POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": campaign, "narration": "The creature casts.",
				"monster_action": map[string]interface{}{"monster": m.Name, "action": "Spellcasting", "use": sp, "target": fmt.Sprint(character)}})
			res := fmt.Sprint(r["monster_action_result"])
			kind := "GM"
			switch {
			case !strings.Contains(strings.ToLower(res), strings.ToLower(sp)):
				kind = "SILENT"
				t.Errorf("%s / %s: the reply does not name the spell: %s", m.Name, sp, res)
			case strings.Contains(res, "Nothing was rolled"), strings.Contains(res, " isn't a ") && strings.Contains(res, " has no effect on "): // an ineligible creature type: nothing rolled (published spell text)
				kind = "refused"
			case strings.Contains(res, "saving throw") || strings.Contains(res, " HIT") || strings.Contains(res, " MISS") || strings.Contains(res, "attack roll"):
				kind = "rolled"
			}
			if kind == "GM" && !strings.Contains(res, "GM") {
				t.Errorf("%s / %s: neither rolled nor handed to the GM in words: %s", m.Name, sp, res)
			}
			counts[kind]++
			notes = append(notes, sp+": "+kind)
		}
		rows = append(rows, fmt.Sprintf("| %s | %s |", m.Name, strings.Join(notes, "; ")))
		call("POST", cp+"/combat/end", gm, nil)
	}
	summary := fmt.Sprintf("Spellcasting spells played through POST /api/gm/narrate: %d rolled by the shared rules (save or attack), %d handed to the GM with the spell named, %d refused with nothing rolled, %d silent.", counts["rolled"], counts["GM"], counts["refused"], counts["SILENT"])
	t.Log(summary)
	if out := os.Getenv("MM_SPELL_OUT"); out != "" {
		os.WriteFile(out, []byte("# Spellcasting route report\n\n"+summary+"\n\n| Monster | Spells (outcome) |\n|---|---|\n"+strings.Join(rows, "\n")+"\n"), 0o644)
	}
}
