package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Runs user-provided private reference fixtures; not part of the distributable patch.
func TestPostgresReferenceCombat(t *testing.T) {
	testPostgres(t)
	dir := os.Getenv("REFERENCE_FIXTURES_DIR")
	if dir == "" {
		t.Skip("REFERENCE_FIXTURES_DIR not set")
	}
	_, gm := pgAgent(t, "reference simulation gm")
	_, player := pgAgent(t, "reference simulation player")
	call := func(method, path, token string, body interface{}) map[string]interface{} {
		t.Helper()
		c, j := pgCall(t, method, path, token, body)
		if c != 200 && c != 201 {
			t.Fatalf("%s %s %d %v", method, path, c, j)
		}
		return j
	}
	campaign := int(call("POST", "/api/campaigns", gm, map[string]interface{}{"name": "High CR combat simulation", "max_players": 2})["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", campaign)
	call("PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	character := int(call("POST", "/api/characters", player, pgRogue(fmt.Sprintf("Fixture Hero %d", time.Now().UnixNano()%1e9)))["character_id"].(float64))
	call("POST", cp+"/join", player, map[string]interface{}{"character_id": character})
	pgFinish(t, player, character)
	call("POST", cp+"/ready", player, nil)
	call("POST", cp+"/ready", gm, nil)
	call("POST", cp+"/start", gm, nil)
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,ac=1 WHERE id=$1`, character)
	cases := []struct {
		file, action, damageType string
		damageWant, legendary    int
	}{
		{"arch-hag.json", "Spectral Claw", "fire", 20, 4},
		{"dracolich.json", "Rend", "poison", 0, 3},
		{"elemental-cataclysm.json", "Elemental Burst", "fire", 0, 4},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				SourceNote string           `json:"source_note"`
				Monsters   []srdMonster2024 `json:"monsters"`
			}
			if err = json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			m := body.Monsters[0]
			imported := call("POST", "/api/gm/compendium/monsters", gm, body)
			key := imported["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
			call("POST", cp+"/combat/start", gm, map[string]interface{}{})
			call("POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": m.Name, "monster_key": key, "initiative": 20}}})
			f, err := readFight(db, campaign)
			if err != nil {
				t.Fatal(err)
			}
			id := 0
			for _, e := range f.Order {
				if e["monster_key"] == key {
					id = asInt(e["id"])
					if asInt(e["hp"]) != m.HP || asInt(e["ac"]) != m.AC {
						t.Fatalf("incorrect stats %v", e)
					}
				}
			}
			if id == 0 {
				t.Fatal("monster missing")
			}
			meta := monsterMetaFor(key)
			if meta == nil || meta.LR != tc.legendary || meta.LAUses != 3 {
				t.Fatalf("legendary uses %+v", meta)
			}
			call("POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": campaign, "target_id": id, "damage": 40, "damage_type": tc.damageType, "apply_resistances": true})
			f, err = readFight(db, campaign)
			if err != nil {
				t.Fatal(err)
			}
			_, e := f.entry(id)
			if got := m.HP - asInt(e["hp"]); got != tc.damageWant {
				t.Fatalf("%s damage got %d want %d", tc.damageType, got, tc.damageWant)
			}
			t.Logf("%s CR%s import, AC %d HP %d, %s damage 40 -> %d; LR %d LA %d", m.Name, m.CR, m.AC, m.HP, tc.damageType, tc.damageWant, meta.LR, meta.LAUses)
			dealt := false
			for i := 0; i < 15; i++ {
				pgExec(t, `UPDATE characters SET hp=1000,ac=1 WHERE id=$1`, character)
				r := call("POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": campaign, "narration": "The creature attacks.", "monster_action": map[string]interface{}{"monster": m.Name, "action": tc.action, "target": fmt.Sprint(character), "use": "fire"}})
				var hp int
				db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, character).Scan(&hp)
				text := fmt.Sprint(r["monster_action_result"])
				if strings.Contains(text, "HIT") {
					t.Logf("%s result %s; damage=%d", tc.action, text, 1000-hp)
					dealt = hp < 1000
					if tc.action == "Elemental Burst" {
						damage := 1000 - hp
						if damage < 13 || !strings.Contains(strings.ToLower(text), "acid damage") && !strings.Contains(strings.ToLower(text), "cold damage") && !strings.Contains(strings.ToLower(text), "fire damage") && !strings.Contains(strings.ToLower(text), "lightning damage") && !strings.Contains(strings.ToLower(text), "thunder damage") {
							t.Errorf("Elemental Burst damage is incorrect: noncritical minimum 13 (5d6+8), and a chosen damage type is required; got %d, %s", damage, text)
						}
					}
					break
				}
			}
			if !dealt {
				t.Errorf("%s failed to apply attack damage", tc.action)
			}
			for i := 0; i < 2; i++ {
				call("POST", cp+"/combat/next", gm, map[string]interface{}{})
			}
			call("POST", cp+"/combat/end", gm, nil)
		})
	}
}
