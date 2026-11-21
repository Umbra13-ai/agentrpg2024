package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestPostgresMonsterCoreRoutes(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "core routes gm")
	_, player := pgAgent(t, "core routes player")
	call := func(method, path, token string, body interface{}) map[string]interface{} {
		t.Helper()
		status, j := pgCall(t, method, path, token, body)
		if status != 200 && status != 201 {
			t.Fatalf("%s %s: %d %v", method, path, status, j)
		}
		return j
	}
	camp := int(call("POST", "/api/campaigns", gm, map[string]interface{}{"name": "Core routes", "max_players": 2})["campaign_id"].(float64))
	char := int(call("POST", "/api/characters", player, pgRogue(fmt.Sprintf("Core routes target %d", camp)))["character_id"].(float64))
	pgFinish(t, player, char)
	pgExec(t, `UPDATE characters SET lobby_id=$1,hp=5000,max_hp=5000,ac=1 WHERE id=$2`, camp, char)
	pgExec(t, `UPDATE lobbies SET status='active',schedule_state='off' WHERE id=$1`, camp)
	m := compendiumFixture()
	m.Name = "Imported Elemental"
	m.Type = "Elemental"
	m.Actions = []srdMonsterEntry{{Name: "Burst", Text: "Melee Attack Roll: +30, reach 10 ft. Hit: 12 (2d6 + 5) damage of a type chosen by the creature: Cold or Fire."}}
	imported := call("POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "Original core route fixture", "monsters": []srdMonster2024{m}})
	key := imported["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	defer db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key)
	cp := fmt.Sprintf("/api/campaigns/%d/combat", camp)
	call("POST", cp+"/start", gm, map[string]interface{}{})
	defer pgCall(t, "POST", cp+"/end", gm, nil)
	call("POST", cp+"/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Opponent", "monster_key": key, "hp": 1000, "ac": 1, "initiative": -100}}})
	f, err := readFight(db, camp)
	if err != nil {
		t.Fatal(err)
	}
	enemy := 0
	for _, e := range f.Order {
		if e["monster_key"] == key {
			enemy = asInt(e["id"])
		}
	}
	enemyHP := func() int {
		f, err := readFight(db, camp)
		if err != nil {
			t.Fatal(err)
		}
		_, e := f.entry(enemy)
		return asInt(e["hp"])
	}
	j := call("POST", "/api/gm/companion", gm, map[string]interface{}{"campaign_id": camp, "character_id": char, "monster_key": key, "name": "Elemental Ally"})
	petID := int(j["companion"].(map[string]interface{})["id"].(float64))
	pet, _ := loadCompanion(petID)
	if pet.CreatureType != "elemental" || len(pet.Attacks) != 1 || len(pet.Attacks[0].DamageChoices) != 2 {
		t.Fatalf("creation lost definition: %+v", pet)
	}
	action := func(desc string) (int, map[string]interface{}) {
		return pgCall(t, "POST", "/api/action", player, map[string]interface{}{"companion_id": petID, "action": "attack", "target_id": enemy, "description": desc})
	}
	before := enemyHP()
	status, j := action("")
	if status != 400 || enemyHP() != before {
		t.Fatalf("missing companion choice: %d %v", status, j)
	}
	pet, _ = loadCompanion(petID)
	if pet.lastRound != 0 {
		t.Fatal("refused companion action spent its turn")
	}
	hit := false
	for i := 0; i < 15; i++ {
		pgExec(t, `UPDATE companions SET last_acted_round=0,last_acted_lobby=0 WHERE id=$1`, petID)
		before = enemyHP()
		status, j = action("damage type: fire")
		if status != 200 {
			t.Fatalf("companion attack: %d %v", status, j)
		}
		if enemyHP() < before {
			hit = true
			break
		}
	}
	if !hit {
		t.Fatal("companion never damaged monster")
	}

	// A monster targeting a companion uses the same definition and typed damage.
	for i := 0; i < 15; i++ {
		beforePet, _ := loadCompanion(petID)
		j = call("POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "Opponent attacks the companion.", "monster_action": map[string]interface{}{"monster": "Opponent", "action": "Burst", "target": "Elemental Ally", "use": "cold"}})
		after, _ := loadCompanion(petID)
		if after.HP < beforePet.HP {
			if !strings.Contains(strings.ToLower(fmt.Sprint(j["monster_action_result"])), "cold") {
				t.Fatal("lost companion target damage type")
			}
			break
		}
		if i == 14 {
			t.Fatal("monster never damaged companion")
		}
	}

	// Item-created companions use the same conversion, including chosen damage.
	itemID, name := insertItemCompanion(char, "summon", "Item Ally", "item", key, "", "test", "", 1000)
	if itemID == 0 || name != m.Name {
		t.Fatal("item companion creation failed")
	}
	item, _ := loadCompanion(itemID)
	plan, why := prepareCompanionPlan(item, "damage type: cold")
	if why != "" || len(plan) != 1 || plan[0].Dice != "2d6+5" || plan[0].Type != "cold" {
		t.Fatalf("item adapter: %s %+v", why, plan)
	}

	legacyItem := item
	legacyItem.Attacks = []compAttack{{Name: "Burst", Bonus: 30, Dice: "", Type: "", Count: 1}}
	plan, why = prepareCompanionPlan(legacyItem, "damage type: fire")
	if why != "" || len(plan) != 1 || plan[0].Dice != "2d6+5" {
		t.Fatalf("legacy item summon: %s %+v", why, plan)
	}
	// Old saved pets without source text recover it from their immutable form.
	pgExec(t, `UPDATE companions SET attacks='[{"name":"Burst","bonus":30,"dice":"","type":"","count":1}]' WHERE id=$1`, petID)
	pgExec(t, `UPDATE companions SET last_acted_round=0,last_acted_lobby=0 WHERE id=$1`, petID)
	status, j = action("damage type: cold")
	if status != 200 {
		t.Fatalf("legacy pet: %d %v", status, j)
	}

	// Familiar refusal occurs before the owner's Attack action or Reaction is spent.
	pgExec(t, `UPDATE companions SET kind='familiar',reaction_round=0,reaction_lobby=0,last_acted_round=0,last_acted_lobby=0 WHERE id=$1`, petID)
	pgExec(t, `UPDATE characters SET eldritch_invocations='["pact-of-the-chain"]',action_used=false,attacks_remaining=NULL WHERE id=$1`, char)
	status, j = action("")
	if status != 400 {
		t.Fatalf("familiar invalid choice: %d %v", status, j)
	}
	var used bool
	var reaction int
	db.QueryRow(`SELECT action_used FROM characters WHERE id=$1`, char).Scan(&used)
	db.QueryRow(`SELECT reaction_round FROM companions WHERE id=$1`, petID).Scan(&reaction)
	if used || reaction != 0 {
		t.Fatal("invalid familiar choice spent a resource")
	}
	status, j = action("damage type: fire")
	if status != 200 {
		t.Fatalf("familiar valid choice: %d %v", status, j)
	}
	db.QueryRow(`SELECT action_used FROM characters WHERE id=$1`, char).Scan(&used)
	db.QueryRow(`SELECT reaction_round FROM companions WHERE id=$1`, petID).Scan(&reaction)
	if !used || reaction == 0 {
		t.Fatal("valid familiar attack did not spend resources")
	}

	// Wild Shape uses the same source adapter and damage owner. This directly tests
	// its resolution function; selecting custom forms remains prohibited.
	pgExec(t, `UPDATE characters SET wild_shape_form='wolf',class='druid',level=2 WHERE id=$1`, char)
	attackTargets.Store(char, enemy)
	defer attackTargets.Delete(char)
	text := wildShapeAttack(char, "Bite")
	if strings.Contains(text, "unsupported") || !strings.Contains(text, "Wild Shape") {
		t.Fatalf("wild shape resolution: %s", text)
	}
	// Exercise player weapon attacks and Wild Shape through the public action route
	// against a custom creature, rather than only GM-adjudicated damage.
	pgExec(t, `UPDATE characters SET wild_shape_form='',weapon_interaction_used=false WHERE id=$1`, char)
	var equipped string
	db.QueryRow(`SELECT COALESCE(equipped_main_hand,'') FROM characters WHERE id=$1`, char).Scan(&equipped)
	if equipped == "" {
		call("POST", "/api/characters/equip-weapon", player, map[string]interface{}{"character_id": char, "weapon": "dagger", "slot": "main_hand"})
		equipped = "dagger"
	}
	for _, form := range []string{"", "wolf"} {
		pgExec(t, `UPDATE characters SET wild_shape_form=$1,action_used=false,attacks_remaining=NULL,conditions='[]' WHERE id=$2`, form, char)
		desc := "I attack with my " + equipped
		if form != "" {
			desc = "Bite"
		}
		hit := false
		for i := 0; i < 20; i++ {
			pgExec(t, `UPDATE characters SET action_used=false,attacks_remaining=NULL WHERE id=$1`, char)
			before := enemyHP()
			status, j := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"action": "attack", "target_id": enemy, "description": desc})
			if status != 200 {
				t.Fatalf("player attack form %q: %d %v", form, status, j)
			}
			if enemyHP() < before {
				hit = true
				break
			}
		}
		if !hit {
			t.Fatalf("player attack form %q never applied damage", form)
		}
	}

}
