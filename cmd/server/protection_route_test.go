package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// protectionFixture: a started campaign with one character (hp 1000) in a fight with "Prot Attacker" (Jab: +5 with a Prone rider; Heavy
// Jab: +60) and "Prot Guardian" (Protection). Each test builds its own campaign.
// protectionExtra receives a second player and character that join the same campaign before it starts (pass one to protectionFixture).
type protectionExtra struct {
	Player string
	Char   int
}

func protectionFixture(t *testing.T, extra ...*protectionExtra) (gm, player string, camp, char int, hpNow func() int, prone func() bool) {
	t.Helper()
	testPostgres(t)
	_, gm = pgAgent(t, "protect gm")
	_, player = pgAgent(t, "protect player")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Protection", "max_players": 2})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp = int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Wearer %d", time.Now().UnixNano()%1e9)))
	char = int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	if len(extra) > 0 {
		_, p2 := pgAgent(t, "protect player two")
		_, j2 := pgCall(t, "POST", "/api/characters", p2, pgRogue(fmt.Sprintf("Second %d", time.Now().UnixNano()%1e9)))
		extra[0].Player, extra[0].Char = p2, int(j2["character_id"].(float64))
		pgCall(t, "POST", cp+"/join", p2, map[string]interface{}{"character_id": extra[0].Char})
		pgFinish(t, p2, extra[0].Char)
		pgCall(t, "POST", cp+"/ready", p2, nil)
	}
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var goblin srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Goblin Warrior" {
			goblin = m
		}
	}
	attacker := goblin
	attacker.Name = "Prot Attacker"
	attacker.Actions = []srdMonsterEntry{
		{Name: "Jab", Text: "Melee Attack Roll: +5, reach 5 ft. Hit: 5 (1d6 + 2) Bludgeoning damage. If the target is a Large or smaller creature, it has the Prone condition."},
		{Name: "Heavy Jab", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 5 (1d6 + 2) Bludgeoning damage. If the target is a Large or smaller creature, it has the Prone condition."},
	}
	guardian := goblin
	guardian.Name = "Prot Guardian"
	guardian.Reactions = []srdMonsterEntry{{Name: "Protection", Text: "Trigger: An attack roll hits the wearer of the guardian's amulet while the wearer is within 5 feet of the guardian. Response: The wearer gains a +5 bonus to AC, including against the triggering attack and possibly causing it to miss, until the start of the guardian's next turn."}}
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variants for the Protection test", "monsters": []srdMonster2024{attacker, guardian}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	keys := map[string]string{}
	for _, m := range j["monsters"].([]interface{}) {
		mm := m.(map[string]interface{})
		keys[fmt.Sprint(mm["name"])] = fmt.Sprint(mm["monster_key"])
	}
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Prot Attacker", "monster_key": keys["Prot Attacker"], "initiative": 20}, {"name": "Prot Guardian", "monster_key": keys["Prot Guardian"], "initiative": 1}}})
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,temp_hp=0 WHERE id=$1`, char)
	hpNow = func() int { var h int; db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, char).Scan(&h); return h }
	prone = func() bool { return hasCondMarker(camp, char, "prone") }
	return
}

// Protection (a monster guardian's Reaction: the wearer gains +5 AC, possibly turning the hit into a miss) with a MONSTER attacker and a
// CHARACTER wearer. The hit's damage and the conditions its rider gave (Prone) are taken back when the +5 turns it into a miss; a hit that
// still beats AC + 5 is left alone. Same rule for a cast Shield (shieldReaction).
func TestPostgresProtectionVersusMonsterAttacker(t *testing.T) {
	gm, _, camp, char, hpNow, prone := protectionFixture(t)
	protect := func() string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Prot Guardian", "action": "Protection", "target_ids": []int{char}}})
		return fmt.Sprint(r["monster_action_result"])
	}
	try := func(action string, wantMiss bool) string {
		t.Helper()
		for n := 0; n < 250; n++ {
			monsterHitsOn.Delete(char)
			removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
			clearSpellEffects(camp)
			pgExec(t, `UPDATE characters SET hp=1000,temp_hp=0,reaction_used=false WHERE id=$1`, char)
			monsterReactionRound.Delete(fmt.Sprintf("%d:%d", camp, -2))
			pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": "Prot Attacker", "action": action, "target": fmt.Sprint(char)}})
			hits := thisTurnHits(char)
			if len(hits) != 1 || hits[0].Nat20 || hpNow() == 1000 {
				continue
			}
			if wantMiss && hits[0].Total >= hits[0].AC+5 || !wantMiss && hits[0].Total < hits[0].AC+5 {
				continue
			}
			return protect()
		}
		t.Fatalf("%s: no hit of the wanted kind (miss=%v) in 250 tries", action, wantMiss)
		return ""
	}
	if res := try("Jab", true); !strings.Contains(res, "Now a miss") || hpNow() != 1000 || prone() || !strings.Contains(res, "What the hit gave is removed") {
		t.Fatalf("Protection did not take the hit back (hp %d, prone %v): %s", hpNow(), prone(), res)
	}
	if res := try("Heavy Jab", false); strings.Contains(res, "Now a miss") || hpNow() == 1000 || !prone() {
		t.Fatalf("Protection changed a hit that still lands (hp %d, prone %v): %s", hpNow(), prone(), res)
	}
}

// Shield by standing order (reaction_orders_2024.go): when it turns the hit into a miss the hit's rider (Prone) is not applied at all.
// Random attack rolls: retried up to 250 times, and it fails if the Shield case is never reached.
func TestPostgresShieldStandingOrderVersusMonsterAttacker(t *testing.T) {
	gm, _, camp, char, hpNow, prone := protectionFixture(t)
	// the shared spell cache is replaced by other tests: this test's own Shield entry (and a restore afterwards)
	saved := srdSpellsMemory
	t.Cleanup(func() { srdSpellsMemory = saved })
	srdSpellsMemory = map[string]SRDSpell{}
	for k, v := range saved {
		srdSpellsMemory[k] = v
	}
	srdSpellsMemory["shield"] = SRDSpell{Name: "Shield", Level: 1, CastingTime: "Reaction, which you take when you are hit by an attack roll"}
	pgExec(t, `UPDATE characters SET class='wizard',level=5,known_spells='["shield"]',prepared_spells='["shield"]',spell_slots_used='{}',reaction_orders='{"shield":{"on":true}}' WHERE id=$1`, char)
	if len(castableSpellsWhere(char, func(sp SRDSpell) bool { return sp.Name == "Shield" })) == 0 {
		t.Fatal("the test character cannot cast Shield: the standing-order scenario cannot run")
	}
	for n := 0; n < 250; n++ {
		monsterHitsOn.Delete(char)
		removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
		clearSpellEffects(camp)
		pgExec(t, `UPDATE characters SET hp=1000,temp_hp=0,reaction_used=false,spell_slots_used='{}',slot_spent_turn='' WHERE id=$1`, char)
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Prot Attacker", "action": "Jab", "target": fmt.Sprint(char)}})
		res := fmt.Sprint(r["monster_action_result"])
		if !strings.Contains(res, "Shield (standing order") {
			continue // the attack missed, or hit beyond AC + 5: Shield was not cast
		}
		if prone() || hpNow() != 1000 || !strings.Contains(res, "riders are not applied") {
			t.Fatalf("a hit that the standing-order Shield made a miss still applied its rider or damage (hp %d, prone %v): %s", hpNow(), prone(), res)
		}
		return
	}
	t.Fatal("the standing-order Shield was never cast in 250 tries")
}

// Shield cast by the player through POST /api/action: a hit it turns into a miss loses its rider and damage. Random attack rolls:
// retried up to 250 times, and it fails if the Shield case is never reached.
func TestPostgresShieldCastVersusMonsterAttacker(t *testing.T) {
	gm, player, camp, char, hpNow, prone := protectionFixture(t)
	// the shared spell cache is replaced by other tests: this test's own Shield entry (and a restore afterwards)
	saved := srdSpellsMemory
	t.Cleanup(func() { srdSpellsMemory = saved })
	srdSpellsMemory = map[string]SRDSpell{}
	for k, v := range saved {
		srdSpellsMemory[k] = v
	}
	srdSpellsMemory["shield"] = SRDSpell{Name: "Shield", Level: 1, CastingTime: "Reaction, which you take when you are hit by an attack roll"}
	pgExec(t, `UPDATE characters SET class='wizard',level=5,known_spells='["shield"]',prepared_spells='["shield"]',spell_slots_used='{}',reaction_orders='{}' WHERE id=$1`, char)
	// its rider and its damage. Random attack rolls, so it retries until a hit that the +5 turns into a miss occurs.
	pgExec(t, `UPDATE characters SET reaction_orders='{}' WHERE id=$1`, char)
	for n := 0; n < 250; n++ {
		monsterHitsOn.Delete(char)
		removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
		clearSpellEffects(camp)
		pgExec(t, `UPDATE characters SET hp=1000,temp_hp=0,reaction_used=false,spell_slots_used='{}',slot_spent_turn='' WHERE id=$1`, char)
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Prot Attacker", "action": "Jab", "target": fmt.Sprint(char)}})
		res := fmt.Sprint(r["monster_action_result"])
		if hpNow() == 1000 || !prone() {
			continue // a miss, or no rider given: nothing to take back
		}
		code, cr := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"action": "cast", "spell_slug": "shield"})
		out := fmt.Sprint(cr)
		if !strings.Contains(out, "Still a hit") && !strings.Contains(out, "Now a miss") {
			t.Fatalf("the cast was refused or did nothing (%d): %.400s", code, out) // not a random miss: a setup problem
		}
		if !strings.Contains(out, "Now a miss") {
			continue // the hit beats AC + 5: Shield leaves it
		}
		if prone() || hpNow() != 1000 || !strings.Contains(out, "What the hit gave is removed") {
			t.Fatalf("a hit that the cast Shield made a miss kept its rider or damage (hp %d, prone %v, %d): %s %s", hpNow(), prone(), code, res, out)
		}
		return
	}
	t.Fatal("no hit that a cast Shield turns into a miss in 250 tries (probabilistic: random rolls)")
}

// One spell slot per turn, by combat turn (prepared_spells_2024.go: the marker is round:turn:start, so it follows whose turn it is, not the
// caster's own turns): a Shield cast in reply to a hit on another creature's turn uses that turn's slot; a second Shield on the same
// turn is refused with the reason; once the combat turn changes the slot is free again. Not fully deterministic: +60 misses on a natural 1, so the monster attack is retried (up to 40 tries); the slot assertions are deterministic.
func TestPostgresShieldSlotPerTurn(t *testing.T) {
	var two protectionExtra
	gm, player, camp, char, _, _ := protectionFixture(t, &two)
	saved := srdSpellsMemory
	t.Cleanup(func() { srdSpellsMemory = saved })
	srdSpellsMemory = map[string]SRDSpell{}
	for k, v := range saved {
		srdSpellsMemory[k] = v
	}
	srdSpellsMemory["shield"] = SRDSpell{Name: "Shield", Level: 1, CastingTime: "Reaction, which you take when you are hit by an attack roll"}
	for _, c := range []int{char, two.Char} {
		pgExec(t, `UPDATE characters SET class='wizard',level=5,known_spells='["shield"]',prepared_spells='["shield"]',spell_slots_used='{}',reaction_orders='{}',slot_spent_turn='' WHERE id=$1`, c)
	}
	reactionUsed := func(c int) bool {
		var u bool
		db.QueryRow(`SELECT COALESCE(reaction_used,false) FROM characters WHERE id=$1`, c).Scan(&u)
		return u
	}
	// hitAndCast: the monster's sure hit on c, then c's player casts Shield. The Reaction is made free first, so a refusal can only
	// come from the slot rule (or the cast itself), never from a Reaction spent by an earlier cast.
	hitAndCast := func(c int, tok string) (int, string) {
		t.Helper()
		monsterHitsOn.Delete(c)
		// +60 still misses on a natural 1 (about 1 try in 20): repeat until the hit lands, up to 40 tries
		for try := 0; try < 40 && len(thisTurnHits(c)) == 0; try++ {
			monsterHitsOn.Delete(c)
			pgExec(t, `UPDATE characters SET hp=1000,reaction_used=false WHERE id=$1`, c)
			pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": "Prot Attacker", "action": "Heavy Jab", "target": fmt.Sprint(c)}})
		}
		if len(thisTurnHits(c)) == 0 {
			t.Fatal("the +60 attack did not hit in 40 tries")
		}
		pgExec(t, `UPDATE characters SET reaction_used=false WHERE id=$1`, c)
		if reactionUsed(c) {
			t.Fatal("the Reaction was not free before the cast")
		}
		code, r := pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "cast", "spell_slug": "shield"})
		return code, fmt.Sprint(r)
	}
	if code, out := hitAndCast(char, player); code != 200 || !strings.Contains(out, "Shield: +5") || !strings.Contains(out, "resource_consumed:reaction") {
		t.Fatalf("the first Shield on this turn was not cast, or did not cost the Reaction (%d): %.400s", code, out)
	}
	if code, out := hitAndCast(char, player); code != 409 || !strings.Contains(out, "already expended a spell slot this turn") || reactionUsed(char) {
		t.Fatalf("a second slot spell on the same turn was not refused by the slot rule with the Reaction free (%d): %.300s", code, out)
	}
	// another character's allowance is separate on the same combat turn
	if code, out := hitAndCast(two.Char, two.Player); code != 200 || !strings.Contains(out, "Shield: +5") {
		t.Fatalf("a second character's own slot was refused on the shared turn (%d): %.300s", code, out)
	}
	pgExec(t, `UPDATE combat_state SET round_number = COALESCE(round_number,0) + 1 WHERE lobby_id=$1`, camp) // the combat turn changes
	if code, out := hitAndCast(char, player); code != 200 || !strings.Contains(out, "Shield: +5") {
		t.Fatalf("the slot was not free again on the next turn (%d): %.300s", code, out)
	}
	// the Reaction limit alone gives a different refusal (not the slot message): a turn with a free slot and a spent Reaction
	pgExec(t, `UPDATE combat_state SET round_number = COALESCE(round_number,0) + 1 WHERE lobby_id=$1`, camp)
	monsterHitsOn.Delete(char)
	for try := 0; try < 40 && len(thisTurnHits(char)) == 0; try++ {
		pgExec(t, `UPDATE characters SET hp=1000 WHERE id=$1`, char)
		pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Prot Attacker", "action": "Heavy Jab", "target": fmt.Sprint(char)}})
	}
	if len(thisTurnHits(char)) == 0 {
		t.Fatal("the +60 attack did not hit in 40 tries")
	}
	pgExec(t, `UPDATE characters SET reaction_used=true WHERE id=$1`, char)
	if code, out := func() (int, string) {
		c, r := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"action": "cast", "spell_slug": "shield"})
		return c, fmt.Sprint(r)
	}(); code == 200 || strings.Contains(out, "already expended a spell slot") {
		t.Fatalf("with a free slot and a spent Reaction the cast was allowed or blamed on the slot rule (%d): %.300s", code, out)
	}
}

// Protection (Shield Guardian) with a MONSTER wearer: the +5 AC and the hit take-back are built for a character, so a monster wearer is
// refused with an explicit GM line (nothing applied, nothing spent), by name and by id; an unknown name keeps the old "name the wearer"
// answer. Control: a character wearer still gets the +5. Route POST /api/gm/narrate monster_action.
func TestPostgresProtectionMonsterWearerIsFlagged(t *testing.T) {
	gm, _, camp, char, _, _ := protectionFixture(t)
	protect := func(body map[string]interface{}) string {
		body["monster"], body["action"] = "Prot Guardian", "Protection"
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": body})
		return fmt.Sprint(r["monster_action_result"])
	}
	monsterReactionRound.Delete(fmt.Sprintf("%d:%d", camp, -2))
	if r := protect(map[string]interface{}{"target": "Prot Attacker"}); !strings.Contains(r, "wearer is a monster") || strings.Contains(r, "until the start of your next turn") {
		t.Errorf("monster wearer by name: %s", r)
	}
	if r := protect(map[string]interface{}{"target_ids": []int{-2}}); !strings.Contains(r, "wearer is a monster") {
		t.Errorf("monster wearer by id: %s", r)
	}
	if r := protect(map[string]interface{}{"target": "Nobody Here"}); !strings.Contains(r, "Name the wearer") {
		t.Errorf("unknown wearer: %s", r)
	}
	monsterReactionRound.Delete(fmt.Sprintf("%d:%d", camp, -2))
	if r := protect(map[string]interface{}{"target_ids": []int{char}}); !strings.Contains(r, "+5 bonus to AC") {
		t.Errorf("control, character wearer: %s", r)
	}
}
