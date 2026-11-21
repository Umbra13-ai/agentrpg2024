package main

import (
	"fmt"
	"testing"
)

// Invented fixtures verify the same explicit rule for both creature adapters.
func TestPostgresHPGainProhibition(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, _, _ := companionReactionFixture(t)
	for _, restricted := range []bool{false, true} {
		t.Run(fmt.Sprint(restricted), func(t *testing.T) {
			m := compendiumFixture()
			m.Name = fmt.Sprintf("Invented HP gain control %t", restricted)
			if restricted {
				m.Traits = []srdMonsterEntry{{Name: "Collective", Text: "The collective can't regain Hit Points or gain Temporary Hit Points."}}
			}
			status, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "Invented HP gain control", "monsters": []srdMonster2024{m}})
			if status != 201 {
				t.Fatalf("import %d %v", status, j)
			}
			key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
			t.Cleanup(func() {
				if _, err := db.Exec("DELETE FROM custom_monsters WHERE key=$1", key); err != nil {
					t.Error(err)
				}
			})
			cp := fmt.Sprintf("/api/campaigns/%d/combat/add", camp)
			status, j = pgCall(t, "POST", cp, gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": m.Name, "monster_key": key}}})
			if status != 200 {
				t.Fatalf("combat add %d %v", status, j)
			}
			id := monsterIDsByKeyOrName(camp, m.Name)[0]
			adjust := func(body map[string]interface{}) {
				body["campaign_id"] = camp
				body["target_id"] = id
				s, j := pgCall(t, "POST", "/api/gm/adjust-monster", gm, body)
				if s != 200 {
					t.Fatalf("adjust %d %v", s, j)
				}
			}
			hurt := max(1, m.HP-10)
			adjust(map[string]interface{}{"hp": hurt})
			adjust(map[string]interface{}{"heal": 5, "temp_hp": 7})
			entry, _, _ := monsterCombatant(camp, id)
			wantHP, wantTemp := min(m.HP, hurt+5), 7
			if restricted {
				wantHP, wantTemp = hurt, 0
			}
			if monsterHPNow(camp, id) != wantHP || asInt(entry["temp_hp"]) != wantTemp {
				t.Fatalf("monster got %v, want HP %d temp %d", entry, wantHP, wantTemp)
			}
			status, j = pgCall(t, "POST", "/api/gm/companion", gm, map[string]interface{}{"campaign_id": camp, "character_id": hero, "monster_key": key})
			if status != 200 {
				t.Fatalf("companion %d %v", status, j)
			}
			pet := asInt(j["companion"].(map[string]interface{})["id"])
			for _, body := range []map[string]interface{}{{"damage": 2, "damage_type": "force"}, {"heal": 1}} {
				body["campaign_id"] = camp
				body["companion_id"] = pet
				status, j = pgCall(t, "POST", "/api/gm/companion", gm, body)
				if status != 200 {
					t.Fatalf("companion change %d %v", status, j)
				}
			}
			c, ok := loadCompanion(pet)
			expected := m.HP - 1
			if restricted {
				expected = m.HP - 2
			}
			if !ok || c.HP != expected {
				t.Fatalf("companion HP %d want %d", c.HP, expected)
			}
		})
	}
}
