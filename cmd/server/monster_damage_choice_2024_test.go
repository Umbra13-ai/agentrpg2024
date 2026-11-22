package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// The SRD 5.2.1 Half-Dragon Claw wording: an extra amount "of the type chosen for the Draconic Origin trait" is dealt in the type the
// GM names in use; no type, or a word that is no damage type, is refused before anything is rolled.
func TestMonsterAttackDamageChoice(t *testing.T) {
	base := monsterActionRow{Name: "Claw", Desc: "Melee Attack Roll: +7, reach 5 ft. Hit: 6 (1d4 + 4) Slashing damage plus 7 (2d6) damage of the type chosen for the Draconic Origin trait."}
	for _, tc := range []struct {
		choice string
		ok     bool
	}{{"acid", true}, {" FIRE ", true}, {"poison", true}, {"", false}, {"mystery", false}, {"fire, cold", false}} {
		a, problem := prepareMonsterAttack(base, tc.choice)
		if (problem == "") != tc.ok {
			t.Errorf("choice %q: %q", tc.choice, problem)
		}
		want := "7 (2d6) " + titleCase(strings.ToLower(strings.TrimSpace(tc.choice))) + " damage"
		if tc.ok && (a.DamageDice != "1d4+4" || a.DamageType != "Slashing" || !strings.Contains(a.Desc, want)) {
			t.Errorf("choice %q: %+v", tc.choice, a)
		}
	}
	for _, a := range []monsterActionRow{{Name: "Unknown", Desc: "Hit: mystery damage."}, {Name: "Bad", DamageDice: "bad", DamageType: "Fire"}, {Name: "Untyped", DamageDice: "2d6", DamageType: ""}, {Name: "Zero", DamageDice: "0d6", DamageType: "Fire"}, {Name: "Huge", DamageDice: "1d999999", DamageType: "Fire"}} {
		if _, problem := prepareMonsterAttack(a, ""); problem == "" {
			t.Errorf("accepted unsupported %+v", a)
		}
	}
	a, problem := prepareMonsterAttack(monsterActionRow{Name: "Slam", DamageDice: "2d6+5", DamageType: "Force"}, "")
	if problem != "" || a.DamageDice != "2d6+5" {
		t.Errorf("fixed damage: %+v %q", a, problem)
	}
}

func TestPostgresMonsterDamageChoiceCombat(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "choice combat gm")
	_, player := pgAgent(t, "choice combat player")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Choice combat", "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	m := compendiumFixture()
	m.Name = "Choice Construct"
	m.Traits = append(m.Traits, srdMonsterEntry{Name: "Draconic Origin", Text: "The construct is related to a type of dragon associated with one of the following damage types (GM's choice): Acid, Cold, Fire, Lightning, or Poison."})
	m.Actions = []srdMonsterEntry{
		{Name: "Multiattack", Text: "The construct makes two Burst attacks."},
		{Name: "Burst", Text: "Melee or Ranged Attack Roll: +15, reach 30 ft. or range 150 ft. Hit: 25 (5d6 + 8) Bludgeoning damage plus 7 (2d6) damage of the type chosen for the Draconic Origin trait."},
		{Name: "Unknown", Text: "Melee Attack Roll: +15, reach 5 ft. Hit: an unknown amount of damage."},
		{Name: "Recharging Burst (Recharge 5–6)", Text: "Melee or Ranged Attack Roll: +15, reach 30 ft. or range 150 ft. Hit: 25 (5d6 + 8) Bludgeoning damage plus 7 (2d6) damage of the type chosen for the Draconic Origin trait."},
	}
	m.Legendary = []srdMonsterEntry{{Name: "Strike", Text: "The construct makes one Burst attack."}}
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "original test", "monsters": []srdMonster2024{m}})
	if c != 201 {
		t.Fatalf("import %d %v", c, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	pgExec(t, `UPDATE lobbies SET status='active',schedule_state='off' WHERE id=$1`, camp)
	pgFight(t, camp)
	status, j := pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Choice target %d", camp)))
	if status != 200 {
		t.Fatalf("fixture character: %d %v", status, j)
	}
	char := int(j["character_id"].(float64))
	pgExec(t, `UPDATE characters SET lobby_id=$1,hp=1000,max_hp=1000,ac=1 WHERE id=$2`, camp, char)
	c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Choice Construct", "monster_key": key, "initiative": 1}}})
	if c != 200 {
		t.Fatalf("add %d %v", c, j)
	}
	f, err := readFight(db, camp)
	if err != nil {
		t.Fatal(err)
	}
	monsterID := 0
	for _, e := range f.Order {
		if e["monster_key"] == key {
			monsterID = asInt(e["id"])
		}
	}
	if monsterID == 0 {
		t.Fatal("missing monster")
	}
	req := legendaryRequest{CombatantID: monsterID, Action: "Strike", Target: fmt.Sprint(char)}
	request := httptest.NewRequest("POST", "/api/gm/legendary-action", nil)
	_, code, _ := takeLegendaryAction(request, camp, req, "1:1", char)
	if code != "invalid_attack_damage" || loadMonsterState(camp, monsterID).LAUsed != 0 {
		t.Fatalf("legendary refusal %q consumed use", code)
	}
	req.Use = "fire"
	result, code, msg := takeLegendaryAction(request, camp, req, "1:1", char)
	if code != "" || loadMonsterState(camp, monsterID).LAUsed != 1 {
		t.Fatalf("legendary use %q %q %v", code, msg, result)
	}
	pgExec(t, `UPDATE characters SET hp=1000 WHERE id=$1`, char)
	hp := func() int { var n int; db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, char).Scan(&n); return n }
	attack := func(action, use string) string {
		t.Helper()
		ma := map[string]interface{}{"monster": "Choice Construct", "action": action, "target": fmt.Sprint(char)}
		if use != "" {
			ma["use"] = use
		}
		c, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "The construct attacks.", "monster_action": ma})
		if !narrateAnswered(c, j) {
			t.Fatalf("attack %d %v", c, j)
		}
		return fmt.Sprint(j["monster_action_result"])
	}
	beforeRecharge := loadMonsterState(camp, monsterID)
	refused := attack("Recharging Burst", "")
	afterRecharge := loadMonsterState(camp, monsterID)
	if len(afterRecharge.RechargeSpent) != len(beforeRecharge.RechargeSpent) || !strings.Contains(refused, "Nothing was rolled") {
		t.Fatalf("invalid recharge use spent resource: %s", refused)
	}
	attack("Recharging Burst", "fire")
	afterRecharge = loadMonsterState(camp, monsterID)
	if !containsStr(afterRecharge.RechargeSpent, "Recharging Burst") {
		t.Fatal("valid recharge action did not spend recharge")
	}
	// Automatic Opportunity Attacks have no choice input: refuse rather than
	// guessing damage, and do not consume the monster's Reaction.
	reactionKey := fmt.Sprintf("%d:%d", camp, monsterID)
	monsterReactionRound.Delete(reactionKey)
	beforeOA := hp()
	lines := resolveOpportunityAttacks(request, camp, char, []int{monsterID})
	if len(lines) != 1 || !strings.Contains(lines[0], "Nothing was rolled") || hp() != beforeOA {
		t.Fatalf("opportunity refusal: %v", lines)
	}
	if _, used := monsterReactionRound.Load(reactionKey); used {
		t.Fatal("refused opportunity attack spent reaction")
	}
	for _, choice := range []string{"", "mystery"} {
		before := hp()
		res := attack("Burst", choice)
		if hp() != before || !strings.Contains(res, "Nothing was rolled") {
			t.Fatalf("choice %q changed HP or did not refuse: %s", choice, res)
		}
	}
	before := hp()
	res := attack("Unknown", "")
	if hp() != before || !strings.Contains(res, "Nothing was rolled") {
		t.Fatalf("unsupported attack: %s", res)
	}
	before = hp()
	res = attack("Multiattack", "")
	if hp() != before || !strings.Contains(res, "Nothing was rolled") {
		t.Fatalf("partial multiattack: %s", res)
	}
	for _, action := range []string{"Burst", "Multiattack"} {
		hit := false
		for i := 0; i < 15; i++ {
			before = hp()
			res = attack(action, "fire")
			d := before - hp()
			if d > 0 {
				if d < 13 || !strings.Contains(strings.ToLower(res), "fire damage") {
					t.Fatalf("%s wrong damage %d: %s", action, d, res)
				}
				hit = true
				break
			}
		}
		if !hit {
			t.Fatalf("%s no damaging hit", action)
		}
	}
}

func TestMonsterDamageParserSharedAdapters(t *testing.T) {
	entries := []srdMonsterEntry{
		{Name: "Blade", Text: "Melee Attack Roll: +8. Hit: 12 (2d6 + 5) Slashing damage plus 7 (2d6) Fire damage."},
		{Name: "Claw", Text: "Melee Attack Roll: +3. Hit: 1 Slashing damage."},
		{Name: "Burst", Text: "Melee Attack Roll: +8. Hit: 12 (2d6 + 5) Bludgeoning damage plus 7 (2d6) damage of the type chosen for the Draconic Origin trait."},
	}
	var rows []monsterActionRow
	raw, err := json.Marshal(oldStyleActions(entries))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	companions := companionRows(rows)
	if len(companions) != 3 {
		t.Fatalf("lost attack: %+v", companions)
	}
	for i, row := range rows {
		monster, problem := prepareMonsterAttack(row, "fire")
		if problem != "" {
			t.Fatal(problem)
		}
		companion, problem := prepareCompanionAttack(companions[i], "fire")
		if problem != "" {
			t.Fatal(problem)
		}
		if monster.DamageDice != companion.Dice || monster.DamageType != companion.Type || monster.AttackBonus != companion.Bonus {
			t.Fatalf("adapter drift %s", row.Name)
		}
	}
	if got := rollStatBlockDamage(rows[1].DamageDice, rows[1].FlatDamage, true); got != 1 {
		t.Fatalf("flat damage doubled on critical: %d", got)
	}
	if len(attackExtraDamage(companions[0].StatBlockText)) != 1 {
		t.Fatal("companion lost secondary damage")
	}
}

func TestEverySRDAttackUsesSharedParser(t *testing.T) {
	count := 0
	for _, m := range srdMonsters2024() {
		for _, e := range m.Actions {
			if !isAttackEntry(e.Text) {
				continue
			}
			row := parseMonsterAttack(e.Name, e.Text)
			_, why := prepareMonsterAttack(row.monsterActionRow, "fire")
			hit := strings.SplitN(e.Text, "Hit:", 2)
			hasDamage := len(hit) == 2 && strings.Contains(strings.ToLower(strings.SplitN(hit[1], ".", 2)[0]), "damage")
			if hasDamage && why != "" {
				t.Errorf("%s %s: %s", m.Name, e.Name, why)
			}
			if !hasDamage && why == "" {
				t.Errorf("%s %s silently invented damage", m.Name, e.Name)
			}
			count++
		}
	}
	if count < 300 {
		t.Fatalf("unexpectedly few attacks: %d", count)
	}
	t.Logf("All %d SRD action attacks have a validated damage definition or explicit refusal through the shared owner", count)
}
