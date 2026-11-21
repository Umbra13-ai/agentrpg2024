package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A stat block that locks one of its attacks while a grapple it created lasts ("Until the grapple ends, the
// kuo-toa can't make Pincer Staff attacks"; Vine Blight, Stirge) is refused at the one attack entry with nothing
// rolled or spent, and frees again when the grapple ends. Route test over gm/narrate; the lock reads live
// condition state, so removing the grapple (the escape machinery's end state) unlocks the attack.
func TestPostgresAttackLockout(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "lockout gm")
	_, player := pgAgent(t, "lockout player")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Attack lockout", "max_players": 2})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Lockout Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var goblin srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Goblin Warrior" {
			goblin = m
		}
	}
	grappler := goblin
	grappler.Name = "Test Grappler"
	grappler.Actions = []srdMonsterEntry{
		{Name: "Latch", Text: "Melee Attack Roll: +60, reach 10 ft. Hit: 10 (2d6 + 3) Piercing damage. If the target is a Medium or smaller creature, it has the Grappled condition (escape DC 12). Until the grapple ends, the grappler can't make Latch attacks."},
		{Name: "Latchday (1/Day)", Text: "Melee Attack Roll: +60, reach 10 ft. Hit: 5 (1d8 + 1) Piercing damage. Until the grapple ends, the grappler can't make Latchday attacks."},
		{Name: "Punch", Text: "Melee Attack Roll: +60, reach 10 ft. Hit: 3 (1d4 + 1) Bludgeoning damage."},
	}
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variant for attack lockout tests", "monsters": []srdMonster2024{grappler}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Test Grappler", "monster_key": key, "initiative": 20},
		{"name": "Test Grappler", "monster_key": key, "initiative": 19},
	}})
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,ac=1 WHERE id=$1`, char)
	grapIDs := monsterIDsByKeyOrName(camp, "Test Grappler")
	if len(grapIDs) != 2 {
		t.Fatalf("expected two Test Grapplers, got %v", grapIDs)
	}

	narrateOn := func(monster, action string) (string, map[string]interface{}) {
		t.Helper()
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": monster, "action": action, "target": fmt.Sprint(char)}})
		atk, _ := r["monster_attack"].(map[string]interface{})
		return fmt.Sprint(r["monster_action_result"]), atk
	}
	narrate := func(action string) (string, map[string]interface{}) {
		return narrateOn(fmt.Sprintf("#%d", grapIDs[0]), action)
	}
	hp := func() int {
		var n int
		if err := db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, char).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// the grapple lands: bounded hit (+60 vs AC 1: only a natural 1 misses)
	var res string
	for try := 0; try < 30; try++ {
		res, _ = narrate("Latch")
		if strings.Contains(res, "Grappled") {
			break
		}
	}
	recs, _ := readConditionRecords(db, camp, char)
	grappled := 0
	for _, r := range recs {
		if r.Condition == "grappled" {
			grappled++
		}
	}
	if grappled != 1 || !strings.Contains(res, "Grappled") {
		t.Fatalf("the Latch hit did not grapple once: %s %+v", res, recs)
	}

	// locked: refused, nothing rolled, persisted HP unchanged, no second grapple
	before := hp()
	res, atk := narrate("Latch")
	if atk["refused"] != true || !strings.Contains(res, "can't make Latch attacks until the grapple it created ends") {
		t.Fatalf("the locked Latch was not refused with the reason: %v %s", atk, res)
	}
	if hp() != before {
		t.Fatalf("a refused locked attack changed HP %d -> %d", before, hp())
	}
	recs, _ = readConditionRecords(db, camp, char)
	if n := 0; true {
		for _, r := range recs {
			if r.Condition == "grappled" {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("the refusal changed the grapple state: %+v", recs)
		}
	}

	// audience (docs/skill.md "Refusals and audience"): the GM caller's reply carries the full reason; the
	// shared log keeps only the player-safe observation; the private clause is the GM's notice; no player
	// read surfaces the private clause
	var logged string
	if err := db.QueryRow(`SELECT result FROM actions WHERE lobby_id=$1 AND result LIKE '%Latch%' ORDER BY id DESC LIMIT 1`, camp).Scan(&logged); err != nil {
		t.Fatalf("the refusal was not logged: %v", err)
	}
	if !strings.Contains(logged, "can't make Latch attacks. Nothing was rolled.") || strings.Contains(logged, "grapple it created") {
		t.Fatalf("the shared log did not keep only the player-safe refusal: %q", logged)
	}
	var notice string
	if err := db.QueryRow(`SELECT text FROM gm_notices WHERE lobby_id=$1 AND text LIKE '%Latch%' ORDER BY id DESC LIMIT 1`, camp).Scan(&notice); err != nil {
		t.Fatalf("the refusal left no GM notice: %v", err)
	}
	if !strings.Contains(notice, "until the grapple it created ends") {
		t.Fatalf("the GM notice did not carry the full private reason: %q", notice)
	}
	for _, path := range []string{"/api/my-turn", cp + "/log", cp} {
		_, pj := pgCall(t, "GET", path, player, nil)
		if s := fmt.Sprint(pj); strings.Contains(s, "grapple it created") {
			t.Fatalf("a player read leaked the private refusal reason on %s: %s", path, s)
		}
	}

	// only the named attack is locked
	res, atk = narrate("Punch")
	if atk["refused"] == true || !strings.Contains(res, "Punch:") {
		t.Fatalf("an attack the block does not lock was refused: %v %s", atk, res)
	}

	// identity: the SAME lockout on a different monster of the same kind does not lock this one. Grappler B is
	// grapple-free, so its Latch resolves while grappler A's grapple holds.
	res, atk = narrateOn(fmt.Sprintf("#%d", grapIDs[1]), "Latch")
	if atk["refused"] == true || !strings.Contains(res, "Latch:") {
		t.Fatalf("another monster's grapple locked this monster's attack: %v %s", atk, res)
	}

	// no consumption: a refused locked attempt does not spend a (1/Day) use. Latchday is refused by the lockout
	// while the grapple holds; after the grapple ends it works once and only then is its use spent.
	res, atk = narrate("Latchday (1/Day)")
	if atk["refused"] != true || !strings.Contains(res, "can't make Latchday attacks") || strings.Contains(res, "1/Day") {
		t.Fatalf("the locked (1/Day) attack was not refused by the lockout: %v %s", atk, res)
	}

	// the grapple over: the attack is free again
	pgExec(t, `DELETE FROM condition_records WHERE lobby_id=$1 AND target_id=$2 AND condition='grappled'`, camp, char)
	res, atk = narrate("Latch")
	if atk["refused"] == true || !strings.Contains(res, "Latch:") {
		t.Fatalf("Latch stayed locked after the grapple ended: %v %s", atk, res)
	}
	// the freed Latch grappled again: end that grapple too before the no-consumption sequence
	pgExec(t, `DELETE FROM condition_records WHERE lobby_id=$1 AND target_id=$2 AND condition='grappled'`, camp, char)
	// Latchday works once (hit or miss: the use counts once the attack is made), then its day use is spent:
	// exactly one use counted, the locked refusal spent nothing.
	res, atk = narrate("Latchday (1/Day)")
	if atk["refused"] == true || !strings.Contains(res, "Latchday (1/Day):") {
		t.Fatalf("Latchday did not resolve after the grapple ended: %v %s", atk, res)
	}
	res, atk = narrate("Latchday (1/Day)")
	if atk["refused"] != true || !strings.Contains(res, "1/Day") || strings.Contains(res, "can't make Latchday attacks") {
		t.Fatalf("Latchday's single use was not spent exactly once: %v %s", atk, res)
	}
}

// The grapple and attach lockouts are both enforced at the attack entry (grapple: the live condition
// record; attach: the attach owner's effect row), so their sentences are not left in the hit residual
// for the GM.
func TestAttackLockoutResidual(t *testing.T) {
	var e saveEffect
	extra := strings.Join(e.readRider("Hit: 9 (2d6 + 2) Piercing damage. If the target is a Medium or smaller creature, it has the Grappled condition (escape DC 12). Until the grapple ends, the kuo-toa can't make Pincer Staff attacks."), ". ")
	if extra != "" {
		t.Errorf("enforced grapple lockout left for the GM: %q", extra)
	}
	var e2 saveEffect
	extra2 := strings.Join(e2.readRider("Hit: 6 (1d6 + 3) Piercing damage, and the stirge attaches to the target. While attached, the stirge can't make Proboscis attacks, and the target takes 5 (2d4) Necrotic damage at the start of each of the stirge's turns."), ". ")
	if extra2 != "" {
		t.Errorf("enforced attach onset, lockout and recurring left for the GM: %q", extra2)
	}
}
