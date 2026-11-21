package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Sixteen different SRD definitions are imported into the custom table. Private
// reference fixtures add four different non-SRD monsters when explicitly supplied.
// REQUIRE_TWENTY_MONSTERS=1 makes missing private fixtures a failure, not a skip.
// This legacy test name covers selected attacks/defenses only. It does NOT
// establish every-ability acceptance or first-run unique replacement passes.
func TestPostgresTwentyMonsterAcceptance(t *testing.T) {
	testPostgres(t)
	names := []string{"Balor", "Ancient Red Dragon", "Ancient Gold Dragon", "Ancient Blue Dragon", "Ancient Silver Dragon", "Dragon Turtle", "Ice Devil", "Iron Golem", "Kraken", "Lich", "Marilith", "Mummy Lord", "Pit Fiend", "Planetar", "Solar", "Tarrasque"}
	fixtures := []srdMonster2024{}
	for _, name := range names {
		found := false
		for _, m := range srdMonsters2024() {
			if m.Name == name {
				m.Size = strings.Split(m.Size, " or ")[0]
				fixtures = append(fixtures, m)
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing SRD fixture %s", name)
		}
	}
	if dir := os.Getenv("TWENTY_MONSTER_FIXTURES"); dir != "" {
		for _, file := range []string{"death-knight-import.json", "arch-hag.json", "dracolich.json", "elemental-cataclysm.json"} {
			raw, err := os.ReadFile(filepath.Join(dir, file))
			if err != nil {
				t.Fatal(err)
			}
			var batch struct {
				Monsters []srdMonster2024 `json:"monsters"`
			}
			if err = json.Unmarshal(raw, &batch); err != nil || len(batch.Monsters) != 1 {
				t.Fatalf("fixture %s: %v", file, err)
			}
			fixtures = append(fixtures, batch.Monsters[0])
		}
	}
	if os.Getenv("REQUIRE_TWENTY_MONSTERS") != "" && len(fixtures) < 20 {
		t.Fatal("20-monster gate requires TWENTY_MONSTER_FIXTURES with four reference fixtures")
	}
	seen := map[string]bool{}
	for _, m := range fixtures {
		if seen[m.Name] {
			t.Fatalf("duplicate %s", m.Name)
		}
		seen[m.Name] = true
	}
	_, gm := pgAgent(t, "acceptance GM")
	_, player := pgAgent(t, "acceptance player")
	doCall := func(t *testing.T, method, path, token string, body interface{}) map[string]interface{} {
		t.Helper()
		status, j := pgCall(t, method, path, token, body)
		if status != 200 && status != 201 {
			t.Fatalf("%s %s: %d %v", method, path, status, j)
		}
		return j
	}
	call := func(method, path, token string, body interface{}) map[string]interface{} {
		return doCall(t, method, path, token, body)
	}
	camp := int(call("POST", "/api/campaigns", gm, map[string]interface{}{"name": "20-monster acceptance", "max_players": 2})["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	call("PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	character := int(call("POST", "/api/characters", player, pgRogue(fmt.Sprintf("Acceptance hero %d", time.Now().UnixNano()%1e9)))["character_id"].(float64))
	call("POST", cp+"/join", player, map[string]interface{}{"character_id": character})
	pgFinish(t, player, character)
	call("POST", cp+"/ready", player, nil)
	call("POST", cp+"/ready", gm, nil)
	call("POST", cp+"/start", gm, nil)
	passed := 0
	for _, m := range fixtures {
		m := m
		t.Run(monsterSlug(m.Name), func(t *testing.T) {
			call := func(method, path, token string, body interface{}) map[string]interface{} {
				return doCall(t, method, path, token, body)
			}
			// Each subtest must clean up even if an assertion fails, so later monsters get their own encounter.
			hp := func() int {
				var n int
				if err := db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, character).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n
			}
			var srdBefore int
			db.QueryRow(`SELECT COUNT(*) FROM monsters`).Scan(&srdBefore)
			j := call("POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "Acceptance fixture for " + m.Name, "monsters": []srdMonster2024{m}})
			key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
			defer func() {
				pgCall(t, "POST", cp+"/combat/end", gm, nil)
				if _, err := db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key); err != nil {
					t.Error(err)
				}
			}()
			call("POST", cp+"/combat/start", gm, map[string]interface{}{})
			call("POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": m.Name, "monster_key": key, "initiative": 20}}})
			f, err := readFight(db, camp)
			if err != nil {
				t.Fatal(err)
			}
			id := 0
			for _, e := range f.Order {
				if e["monster_key"] == key {
					id = asInt(e["id"])
					if asInt(e["hp"]) != m.HP || asInt(e["max_hp"]) != m.HP || asInt(e["ac"]) != m.AC {
						t.Fatalf("incorrect combat stats: %v", e)
					}
				}
			}
			if id == 0 {
				t.Fatal("custom monster missing")
			}
			block := monsterStatBlock(key)
			if block == nil || block["hp"] != m.HP {
				t.Fatal("stat block missing")
			}
			meta := monsterMetaFor(key)
			expected := metaFromMonster(key, m)
			if meta == nil || meta.LAUses != expected.LAUses || meta.LR != expected.LR || len(meta.Actions) != len(m.Actions) || len(meta.Traits) != len(m.Traits) {
				t.Fatal("trait/action/legendary metadata mismatch")
			}
			for ability, a := range m.Abilities {
				if n := monsterSaveBonus(key, ability, a.Score); n != a.Save {
					t.Fatalf("%s save %d != %d", ability, n, a.Save)
				}
			}
			monsterHP := func() int {
				f, err := readFight(db, camp)
				if err != nil {
					t.Fatal(err)
				}
				_, e := f.entry(id)
				return asInt(e["hp"])
			}
			// Check all 13 damage types against the imported stat block, using the actual GM damage endpoint.
			for _, typ := range []string{"acid", "bludgeoning", "cold", "fire", "force", "lightning", "necrotic", "piercing", "poison", "psychic", "radiant", "slashing", "thunder"} {
				call("POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target_id": id, "hp": m.HP})
				want := 20
				if strings.Contains(strings.ToLower(m.DmgImmune), typ) {
					want = 0
				} else if strings.Contains(strings.ToLower(m.Vulnerable), typ) {
					want = 40
				} else if strings.Contains(strings.ToLower(m.Resistances), typ) {
					want = 10
				}
				call("POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target_id": id, "damage": 20, "damage_type": typ, "apply_resistances": true})
				if got := m.HP - monsterHP(); got != min(want, m.HP) {
					t.Fatalf("%s damage got %d want %d", typ, got, want)
				}
			}
			call("POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target_id": id, "hp": m.HP})
			var action srdMonsterEntry
			for _, a := range m.Actions {
				if strings.Contains(a.Text, "Attack Roll:") {
					action = a
					break
				}
			}
			if action.Name == "" {
				t.Fatal("no attack fixture")
			}
			use := ""
			if strings.Contains(action.Text, "damage of a type chosen") {
				use = "fire"
			}
			attack := func(apply bool) map[string]interface{} {
				return call("POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "The creature attacks.", "monster_action": map[string]interface{}{"monster": m.Name, "action": action.Name, "target": fmt.Sprint(character), "use": use, "apply_damage": apply}})
			}
			// Preview must never alter HP.
			pgExec(t, `UPDATE characters SET hp=10000,max_hp=10000,ac=1,conditions='[]' WHERE id=$1`, character)
			before := hp()
			attack(false)
			if hp() != before {
				t.Fatal("preview applied damage")
			}
			hit := false
			for i := 0; i < 20; i++ {
				pgExec(t, `UPDATE characters SET hp=10000,conditions='[]' WHERE id=$1`, character)
				j := attack(true)
				text := fmt.Sprint(j["monster_action_result"])
				damage := 10000 - hp()
				if damage > 0 {
					// Independent arithmetic bounds catch a silent fallback to the wrong dice.
					hitText := strings.SplitN(action.Text, "Hit:", 2)
					if len(hitText) != 2 {
						t.Fatal("fixture has no Hit text")
					}
					rolls := acceptanceDamageDice.FindAllStringSubmatch(strings.SplitN(hitText[1], ".", 2)[0], -1)
					low, high := 0, 0
					for _, r := range rolls {
						var n, d, bonus int
						fmt.Sscan(r[1], &n)
						fmt.Sscan(r[2], &d)
						fmt.Sscan(strings.ReplaceAll(r[3], " ", ""), &bonus)
						low += n + bonus
						high += 2*n*d + bonus
					}
					if low < 1 || damage < low || damage > high {
						t.Fatalf("attack damage %d outside %d..%d: %s", damage, low, high, text)
					}
					if use != "" && !strings.Contains(strings.ToLower(text), use+" damage") {
						t.Fatalf("choice not used: %s", text)
					}
					hit = true
					t.Logf("%s CR%s: HP=%d AC=%d attack=%s damage=%d; all 13 defenses and 6 saves passed", m.Name, m.CR, m.HP, m.AC, action.Name, damage)
					break
				}
			}
			if !hit {
				t.Fatal("20 attempts produced no damaging hit")
			}
			// At least one full round, and a GM-resolved defeat, through normal endpoints.
			call("POST", cp+"/combat/next", gm, map[string]interface{}{})
			call("POST", cp+"/combat/next", gm, map[string]interface{}{})
			f, err = readFight(db, camp)
			if err != nil || f.Round < 2 {
				t.Fatal("round did not advance")
			}
			call("POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target_id": id, "damage": m.HP + 1, "damage_type": "force", "apply_resistances": false})
			if monsterHP() != 0 {
				t.Fatal("GM adjudicated defeat failed")
			}
			var srdAfter int
			db.QueryRow(`SELECT COUNT(*) FROM monsters`).Scan(&srdAfter)
			if srdAfter != srdBefore {
				t.Fatal("custom import altered SRD catalog")
			}
			passed++
		})
	}
	t.Logf("SELECTED MECHANICS: %d/%d monster cases passed; every-ability and unique-replacement acceptance are not established by this test", passed, len(fixtures))
	if passed != len(fixtures) {
		t.Fail()
	}
}

var acceptanceDamageDice = regexp.MustCompile(`\((\d+)d(\d+)(?:\s*\+\s*(\d+))?\)`)
