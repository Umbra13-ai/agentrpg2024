package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// companionReactionFixture: a campaign with one character, one imported-monster companion and one enemy in combat.
func companionReactionFixture(t *testing.T) (gm, player string, camp, char, petID, enemy int) {
	t.Helper()
	_, gm = pgAgent(t, "comp react gm")
	_, player = pgAgent(t, "comp react player")
	call := func(method, path, token string, body interface{}) map[string]interface{} {
		t.Helper()
		status, j := pgCall(t, method, path, token, body)
		if status != 200 && status != 201 {
			t.Fatalf("%s %s: %d %v", method, path, status, j)
		}
		return j
	}
	camp = int(call("POST", "/api/campaigns", gm, map[string]interface{}{"name": "Companion reactions", "max_players": 2})["campaign_id"].(float64))
	char = int(call("POST", "/api/characters", player, pgRogue(fmt.Sprintf("Comp react hero %d", camp)))["character_id"].(float64))
	pgFinish(t, player, char)
	pgExec(t, `UPDATE characters SET lobby_id=$1,hp=5000,max_hp=5000,ac=1 WHERE id=$2`, camp, char)
	pgExec(t, `UPDATE lobbies SET status='active',schedule_state='off' WHERE id=$1`, camp)
	m := compendiumFixture()
	m.Name = "Comp React Beast"
	m.Type = "Beast"
	m.Actions = []srdMonsterEntry{{Name: "Bite", Text: "Melee Attack Roll: +30, reach 5 ft. Hit: 7 (1d8 + 3) Piercing damage."}}
	imported := call("POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented companion reaction fixture", "monsters": []srdMonster2024{m}})
	key := imported["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	cp := fmt.Sprintf("/api/campaigns/%d/combat", camp)
	call("POST", cp+"/start", gm, map[string]interface{}{})
	t.Cleanup(func() { pgCall(t, "POST", cp+"/end", gm, nil) })
	call("POST", cp+"/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Opponent", "monster_key": key, "hp": 1000, "ac": 1, "initiative": -100}}})
	f, err := readFight(db, camp)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range f.Order {
		if e["monster_key"] == key {
			enemy = asInt(e["id"])
		}
	}
	j := call("POST", "/api/gm/companion", gm, map[string]interface{}{"campaign_id": camp, "character_id": char, "monster_key": key, "name": "React Pet"})
	petID = int(j["companion"].(map[string]interface{})["id"].(float64))
	return
}

// The condition owner keeps a companion's conditions (it has no characters row, like a monster), and a companion that can't take
// Reactions (the marker, or an Incapacitating condition) neither attacks with its Reaction (Pact of the Chain) nor delivers a touch spell.
func TestPostgresCompanionCantTakeReactions(t *testing.T) {
	testPostgres(t)
	_, player, camp, char, petID, enemy := companionReactionFixture(t)
	pet, _ := loadCompanion(petID)
	pgExec(t, `UPDATE companions SET kind='familiar',reaction_round=0,reaction_lobby=0,last_acted_round=0,last_acted_lobby=0 WHERE id=$1`, petID)
	reset := func() {
		pgExec(t, `UPDATE companions SET reaction_round=0,reaction_lobby=0,last_acted_round=0,last_acted_lobby=0 WHERE id=$1`, petID)
		pgExec(t, `UPDATE characters SET eldritch_invocations='["pact-of-the-chain"]',action_used=false,attacks_remaining=NULL WHERE id=$1`, char)
	}
	spent := func() (bool, int) {
		var used bool
		var rr int
		db.QueryRow(`SELECT action_used FROM characters WHERE id=$1`, char).Scan(&used)
		db.QueryRow(`SELECT reaction_round FROM companions WHERE id=$1`, petID).Scan(&rr)
		return used, rr
	}
	attack := func() (int, map[string]interface{}) {
		reset()
		return pgCall(t, "POST", "/api/action", player, map[string]interface{}{"companion_id": petID, "action": "attack", "target_id": enemy, "description": "damage type: piercing"})
	}
	refused := func(label string) {
		t.Helper()
		code, j := attack()
		used, rr := spent()
		if code != 400 || j["error"] != "cant_take_reactions" || used || rr != 0 {
			t.Errorf("%s: want a refusal that spends nothing, got %d %v (owner action used %v, reaction round %d)", label, code, j, used, rr)
		}
	}
	// control: with no condition the familiar's Reaction attack works and spends the owner's attack and the familiar's Reaction
	if code, j := attack(); code != 200 {
		t.Fatalf("control: the familiar's attack: %d %v", code, j)
	}
	if used, rr := spent(); !used || rr == 0 {
		t.Fatalf("control: the attack should spend the owner's action and the familiar's Reaction (%v, %d)", used, rr)
	}
	// the marker
	res := addCondition(camp, pet.CombatID, condAdd{Condition: "no_reactions", Source: "test effect", Rounds: 2, Replace: true})
	if res.Err != nil || !res.Added || !cantTakeReactions(camp, pet.CombatID) {
		t.Fatalf("the condition owner did not keep the marker on a companion: %+v", res)
	}
	refused("no_reactions marker")
	if msg := familiarTouchCheckForTest(t, char, camp); !strings.Contains(msg, "can't take Reactions") {
		t.Errorf("touch delivery with the marker: %q", msg)
	}
	removeConditionsWhere(camp, pet.CombatID, func(conditionRecord) bool { return true })
	if code, j := attack(); code != 200 {
		t.Errorf("after the marker is removed the attack works again: %d %v", code, j)
	}
	// an Incapacitating condition
	if r := addCondition(camp, pet.CombatID, condAdd{Condition: "stunned", Source: "test effect", Rounds: 2}); r.Err != nil || !r.Added {
		t.Fatalf("stunned on a companion: %+v", r)
	}
	refused("stunned")
	if msg := familiarTouchCheckForTest(t, char, camp); !strings.Contains(msg, "Incapacitated") {
		t.Errorf("touch delivery while stunned: %q", msg)
	}
	removeConditionsWhere(camp, pet.CombatID, func(conditionRecord) bool { return true })
	// a condition that does not stop Reactions
	addCondition(camp, pet.CombatID, condAdd{Condition: "poisoned", Source: "test effect", Rounds: 2})
	if code, j := attack(); code != 200 {
		t.Errorf("a poisoned familiar can still take a Reaction: %d %v", code, j)
	}
	removeConditionsWhere(camp, pet.CombatID, func(conditionRecord) bool { return true })
}

// familiarTouchCheckForTest: the helper the cast route calls (helper level, not the cast route: that needs a Touch spell and spell data).
func familiarTouchCheckForTest(t *testing.T, char, camp int) string {
	t.Helper()
	saved := srdSpellsMemory
	srdSpellsMemory = make(map[string]SRDSpell, len(saved)+1)
	for k, v := range saved {
		srdSpellsMemory[k] = v
	}
	t.Cleanup(func() { srdSpellsMemory = saved })
	srdSpellsMemory["test-touch"] = SRDSpell{Name: "Test Touch", Level: 1, Range: "Touch"}
	pgExec(t, `UPDATE companions SET reaction_round=0,reaction_lobby=0 WHERE owner_id=$1`, char)
	msg := familiarTouchCheck(char, camp, "test-touch", "delivered by my familiar")
	familiarTouchPending.Delete(char)
	return msg
}

func TestCompanionConditionIDBase(t *testing.T) {
	if companionIDBase != 1000000000 {
		t.Fatalf("inCampaignSQL spells companionIDBase as a literal (1000000000); update it with the constant (now %d)", companionIDBase)
	}
}

// Companion conditions stay with their campaign and their own id: a campaign naming another campaign's companion is refused and writes
// nothing; a character whose id equals a companion's own id keeps its own, separate conditions (the combat id is the companion id plus
// companionIDBase); a character's conditions behave as before (the characters row's list is kept in step); timed conditions of a companion
// run out when time passes in its campaign (and not in another); a companion that is gone keeps no conditions.
func TestPostgresCompanionConditionScope(t *testing.T) {
	testPostgres(t)
	_, _, campA, charA, petA, _ := companionReactionFixture(t)
	_, _, campB, charB, petB, _ := companionReactionFixture(t)
	pa, _ := loadCompanion(petA)
	pb, _ := loadCompanion(petB)
	if campA == campB || pa.CombatID == pb.CombatID {
		t.Fatalf("setup: %d %d %d %d", campA, campB, pa.CombatID, pb.CombatID)
	}
	// a character whose id equals companion A's own id: make one if the ids do not already coincide
	// (the characters table is shared, so rather than forcing an id the test checks the general rule below with both real ids)
	for _, ch := range []int{charA, charB} {
		if ch == pa.CombatID || ch == pb.CombatID {
			t.Fatalf("a character id %d equals a companion combat id", ch)
		}
	}
	// 1. the right campaign: works and is stored with that campaign
	if r := addCondition(campA, pa.CombatID, condAdd{Condition: "stunned", Source: "scope test", Rounds: 3}); r.Err != nil || !r.Added {
		t.Fatalf("own campaign: %+v", r)
	}
	var lob int
	db.QueryRow(`SELECT lobby_id FROM condition_records WHERE target_id=$1`, pa.CombatID).Scan(&lob)
	if lob != campA {
		t.Errorf("the record is kept with campaign %d, got %d", campA, lob)
	}
	// 2. another campaign names it: refused, nothing written, nothing removed
	r := addCondition(campB, pa.CombatID, condAdd{Condition: "paralyzed", Source: "scope test"})
	if r.Err == nil || r.Added {
		t.Errorf("campaign B must not be able to put a condition on campaign A's companion: %+v", r)
	}
	if gone := removeConditionsWhere(campB, pa.CombatID, func(conditionRecord) bool { return true }); len(gone) != 0 {
		t.Errorf("campaign B removed campaign A's companion's conditions: %v", gone)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM condition_records WHERE target_id=$1`, pa.CombatID).Scan(&n)
	if n != 1 {
		t.Errorf("campaign A's companion should still have exactly its one condition, has %d", n)
	}
	// 3. the characters with the companions' own numeric ids are separate creatures
	if pa.ID <= 0 {
		t.Fatal("setup")
	}
	// Construct the numeric collision rather than depending on unrelated tests'
	// character/companion sequence positions. This is a test-only minimal row.
	res, err := db.Exec(`INSERT INTO characters(id,agent_id,name,lobby_id) SELECT $1,agent_id,'Explicit collision control',lobby_id FROM characters WHERE id=$2 ON CONFLICT(id) DO NOTHING`, pa.ID, charA)
	if err != nil {
		t.Fatal(err)
	}
	inserted, _ := res.RowsAffected()
	if inserted > 0 {
		pgExec(t, `SELECT setval(pg_get_serial_sequence('characters','id'),GREATEST((SELECT MAX(id) FROM characters),(SELECT last_value FROM characters_id_seq)))`)
		t.Cleanup(func() { db.Exec(`DELETE FROM characters WHERE id=$1`, pa.ID) })
	}
	var otherChar, otherLobby int
	db.QueryRow(`SELECT id, COALESCE(lobby_id,0) FROM characters WHERE id=$1`, pa.ID).Scan(&otherChar, &otherLobby) // the character whose id equals the companion's own id
	if otherChar == 0 {
		t.Fatalf("collision control missing: no character has the companion's own id %d", pa.ID)
	}
	if hasCondMarker(campA, otherChar, "stunned") && otherChar != charA {
		t.Errorf("character %d took on companion %d's condition", otherChar, pa.ID)
	}
	if r := addCondition(otherLobby, otherChar, condAdd{Condition: "blinded", Source: "scope test"}); r.Err != nil || !r.Added {
		t.Fatalf("the character with the companion's id: %+v", r)
	}
	if hasCondMarker(campA, pa.CombatID, "blinded") {
		t.Errorf("the companion took on the same-numbered character's condition")
	}
	if len(loadConditionRecords(campA, pa.CombatID)) != 1 {
		t.Errorf("the companion's conditions changed when a same-numbered character's did: %+v", loadConditionRecords(campA, pa.CombatID))
	}
	removeConditionsWhere(otherLobby, otherChar, func(r conditionRecord) bool { return r.Condition == "blinded" })
	if len(loadConditionRecords(campA, pa.CombatID)) != 1 {
		t.Errorf("removing the character's condition touched the companion's")
	}
	// 4. a character's conditions are unchanged: the owner keeps the list on the characters row and reads it back
	if r := addCondition(campA, charA, condAdd{Condition: "stunned", Source: "scope test", Rounds: 3}); r.Err != nil || !r.Added {
		t.Fatalf("character condition: %+v", r)
	}
	var list string
	db.QueryRow(`SELECT COALESCE(conditions::text,'') FROM characters WHERE id=$1`, charA).Scan(&list)
	if !strings.Contains(list, "stunned") {
		t.Errorf("the character's own conditions list lost the condition: %s", list)
	}
	db.QueryRow(`SELECT COALESCE(conditions::text,'') FROM characters WHERE id=$1`, charB).Scan(&list)
	if strings.Contains(list, "stunned") {
		t.Errorf("another campaign's character got it: %s", list)
	}
	// 5. time passes in campaign B: nothing of campaign A's changes; in campaign A it counts down
	addCondition(campB, pb.CombatID, condAdd{Condition: "stunned", Source: "scope test", Rounds: 1})
	passConditionTime(campB, 1, 0)
	if hasCondMarker(campB, pb.CombatID, "stunned") {
		t.Error("a companion's one-round condition should run out when a round passes in its campaign")
	}
	recs := loadConditionRecords(campA, pa.CombatID)
	if len(recs) != 1 || recs[0].DurationRounds != 3 {
		t.Errorf("time passing in campaign B changed campaign A's companion: %+v", recs)
	}
	passConditionTime(campA, 1, 0)
	if recs = loadConditionRecords(campA, pa.CombatID); len(recs) != 1 || recs[0].DurationRounds != 2 {
		t.Errorf("a round passing in campaign A should take one round off the companion's condition: %+v", recs)
	}
	if !hasCondMarker(campA, charA, "stunned") {
		t.Error("the character's condition should still be there (3 rounds, one passed, started at 3)")
	}
	// 6. a companion that is gone keeps no conditions
	endCompanion(pa.ID, "test")
	db.QueryRow(`SELECT COUNT(*) FROM condition_records WHERE target_id=$1`, pa.CombatID).Scan(&n)
	if n != 0 {
		t.Errorf("a gone companion's conditions should be deleted, %d left", n)
	}
}

// POST /api/gm/conditions aimed at a companion's combat id: today it REFUSES (404 character_not_found: the route takes a character id or a
// monster's negative id) and changes nothing, neither on the companion nor on the character that has the companion's plain id. This pins
// the refusal; it is not a claim that conditions can be applied to companions through any route.
func TestPostgresCompanionConditionRoutes(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, petID, _ := companionReactionFixture(t)
	pet, _ := loadCompanion(petID)
	var before string
	db.QueryRow(`SELECT COALESCE(conditions::text,'') || COALESCE(exhaustion_level,0)::text FROM characters WHERE id=$1`, pet.ID).Scan(&before)
	for _, cond := range []string{"stunned", "exhaustion"} {
		code, j := pgCall(t, "POST", "/api/gm/conditions", gm, map[string]interface{}{"campaign_id": camp, "target_id": pet.CombatID, "condition": cond})
		if code != 404 || j["error"] != "character_not_found" || len(loadConditionRecords(camp, pet.CombatID)) != 0 {
			t.Errorf("%s on a companion's combat id: want a refusal that changes nothing, got %d %v", cond, code, j)
		}
	}
	var after string
	db.QueryRow(`SELECT COALESCE(conditions::text,'') || COALESCE(exhaustion_level,0)::text FROM characters WHERE id=$1`, pet.ID).Scan(&after)
	if before != after {
		t.Errorf("the character with the companion's plain id %d changed: %q to %q", pet.ID, before, after)
	}
}

// A companion with its own turn entry: its timed condition counts down at the end of ITS turn and then runs out (condition_turns.go,
// through the condition owner). A control: the countdown does not run at the end of another creature's turn.
func TestPostgresCompanionConditionEndOfTurnCountdown(t *testing.T) {
	testPostgres(t)
	_, _, camp, _, petID, enemy := companionReactionFixture(t)
	pet, _ := loadCompanion(petID)
	changeFight(camp, func(f *fight) error {
		f.place(map[string]interface{}{"id": pet.CombatID, "name": pet.Name, "initiative": 5, "dex_score": 12, "is_companion": true, "owner_id": pet.OwnerID})
		return nil
	})
	index := func(id int) int {
		f, err := readFight(db, camp)
		if err != nil {
			t.Fatal(err)
		}
		i, _ := f.entry(id)
		return i
	}
	pi, ei := index(pet.CombatID), index(enemy)
	if pi < 0 || ei < 0 {
		t.Fatalf("setup: companion at %d, enemy at %d", pi, ei)
	}
	if r := addCondition(camp, pet.CombatID, condAdd{Condition: "stunned", Source: "countdown test", Rounds: 2}); r.Err != nil || !r.Added {
		t.Fatalf("stunned: %+v", r)
	}
	rounds := func() int {
		recs := loadConditionRecords(camp, pet.CombatID)
		if len(recs) == 0 {
			return -1
		}
		return recs[0].DurationRounds
	}
	endTurnEffects(camp, ei) // another creature's turn ends: nothing happens to the companion's condition
	if rounds() != 2 {
		t.Errorf("another creature's turn end changed the companion's condition: %d", rounds())
	}
	endTurnEffects(camp, pi)
	if rounds() != 1 {
		t.Errorf("the end of the companion's turn should take one round off: %d", rounds())
	}
	endTurnEffects(camp, pi)
	if rounds() != -1 {
		t.Errorf("the second end of its turn should end the condition: %d", rounds())
	}
}

// A player's spell aimed at a companion (companion_spell_targets_2024.go): a plain healing spell heals it through the companion Hit Point
// owner; every other spell is refused (companion_target_unsupported) with nothing spent, stored or printed as if it had worked; a
// companion of another campaign or a dismissed one is the same 404 as an unknown id. Control: the same casts at the enemy and at the
// caster work. Not covered: Beacon of Hope or other buffs on a companion (refused), monster riders against companions.
func TestPostgresSpellAtCompanion(t *testing.T) {
	testPostgres(t)
	saved := srdSpellsMemory
	t.Cleanup(func() { srdSpellsMemory = saved })
	srdSpellsMemory = map[string]SRDSpell{}
	if err := loadSRDFromDB(); err != nil {
		t.Fatal(err)
	}
	_, player, _, char, petID, enemy := companionReactionFixture(t)
	_, _, _, _, otherPet, _ := companionReactionFixture(t)
	pgExec(t, `UPDATE characters SET class='cleric',level=5,known_spells='["magic-missile","hold-person","cure-wounds","mass-healing-word"]',prepared_spells='["magic-missile","hold-person","cure-wounds","mass-healing-word"]',spell_slots_used='{}',inventory=$2::jsonb WHERE id=$1`, char, `[{"id":"i1","name":"Holy Symbol","type":"focus","spellcasting_focus":true},{"id":"i2","name":"Component Pouch","type":"focus","spellcasting_focus":true}]`)
	cast := func(body map[string]interface{}) (int, map[string]interface{}) {
		pgExec(t, `UPDATE characters SET action_used=false,bonus_action_used=false,spell_slots_used='{}',slot_spent_turn='',conditions='[]' WHERE id=$1`, char)
		body["action"] = "cast"
		return pgCall(t, "POST", "/api/action", player, body)
	}
	petHP := func(id int) (hp int) { db.QueryRow(`SELECT hp FROM companions WHERE id=$1`, id).Scan(&hp); return }
	slots := func() (s string) {
		db.QueryRow(`SELECT spell_slots_used::text FROM characters WHERE id=$1`, char).Scan(&s)
		return
	}
	if code, r := cast(map[string]interface{}{"spell_slug": "magic-missile", "target_id": enemy}); code != 200 {
		t.Fatalf("control: the cast at the enemy failed: %d %v", code, r)
	}
	hurt := func() { pgExec(t, `UPDATE companions SET hp=100 WHERE id IN ($1,$2)`, petID, otherPet) }
	// unsupported spells: refused, nothing changes
	hurt()
	for _, spell := range []string{"magic-missile", "hold-person"} {
		code, r := cast(map[string]interface{}{"spell_slug": spell, "target_id": companionIDBase + petID})
		var n int
		db.QueryRow(`SELECT count(*) FROM condition_records WHERE target_id=$1`, companionIDBase+petID).Scan(&n)
		if code != 400 || fmt.Sprint(r["error"]) != "companion_target_unsupported" || petHP(petID) != 100 || n != 0 || slots() != "{}" {
			t.Errorf("%s at the companion: code %d, reply %v, hp %d, conditions %d, slots %s", spell, code, r, petHP(petID), n, slots())
		}
	}
	// healing: heals it, spends the slot
	code, r := cast(map[string]interface{}{"spell_slug": "cure-wounds", "target_id": companionIDBase + petID})
	if got := petHP(petID); code != 200 || got <= 100 || got > 400 || slots() == "{}" || !strings.Contains(fmt.Sprint(r["result"]), "regains") {
		t.Errorf("Cure Wounds at the companion: code %d, hp %d, slots %s, reply %v", code, got, slots(), r)
	}
	// several targets, one of them a companion
	hurt()
	code, r = cast(map[string]interface{}{"spell_slug": "mass-healing-word", "target_ids": []int{companionIDBase + petID, char}})
	if got := petHP(petID); code != 200 || got <= 100 {
		t.Errorf("Mass Healing Word with a companion: code %d, hp %d, reply %v", code, got, r)
	}
	// never above the maximum
	pgExec(t, `UPDATE companions SET hp=max_hp-1 WHERE id=$1`, petID)
	cast(map[string]interface{}{"spell_slug": "cure-wounds", "target_id": companionIDBase + petID})
	var mx int
	db.QueryRow(`SELECT max_hp FROM companions WHERE id=$1`, petID).Scan(&mx)
	if petHP(petID) != mx {
		t.Errorf("healing went past the maximum or stopped short: %d of %d", petHP(petID), mx)
	}
	// another campaign's companion, a dismissed one: the unknown-id 404, nothing healed
	hurt()
	if code, r := cast(map[string]interface{}{"spell_slug": "cure-wounds", "target_id": companionIDBase + otherPet}); code != 404 || petHP(otherPet) != 100 || slots() != "{}" {
		t.Errorf("another campaign's companion: code %d hp %d slots %s reply %v", code, petHP(otherPet), slots(), r)
	}
	pgExec(t, `UPDATE companions SET status='dismissed' WHERE id=$1`, petID)
	if code, r := cast(map[string]interface{}{"spell_slug": "cure-wounds", "target_id": companionIDBase + petID}); code != 404 || petHP(petID) != 100 || slots() != "{}" {
		t.Errorf("dismissed companion: code %d hp %d slots %s reply %v", code, petHP(petID), slots(), r)
	}
}

// A monster's hit on a companion gives what a hit gives besides damage (companionHitRiders, companion_spell_targets_2024.go): its
// conditions go to the condition owner, its own saving throw is rolled with the companion's stat block (applySaveEffectOnCompanion), and a size gate is named for the GM instead of being dropped. Route:
// POST /api/gm/narrate with a monster_action aimed at the companion. Not covered: Speed/Disadvantage/Exhaustion riders (flagged), a companion's size, Legendary and Magic Resistance on a companion.
func TestPostgresMonsterHitRidersOnCompanion(t *testing.T) {
	testPostgres(t)
	gm, player, camp, charID, petID, _ := companionReactionFixture(t)
	m := compendiumFixture()
	m.Name = "Comp Rider Brute"
	m.Type = "Giant"
	m.Actions = []srdMonsterEntry{
		{Name: "Zap", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 5 (1d6 + 2) Lightning damage, and the target has the Stunned condition until the start of the brute's next turn."},
		{Name: "Jab", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 5 (1d6 + 2) Bludgeoning damage. If the target is a Large or smaller creature, it has the Prone condition."},
		{Name: "Venom", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 5 (1d6 + 2) Piercing damage. Constitution Saving Throw: DC 99. Failure: 5 (1d10) Acid damage, and the target has the Poisoned condition."},
		{Name: "Mild", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 5 (1d6 + 2) Piercing damage. Constitution Saving Throw: DC 1. Failure: 5 (1d10) Acid damage, and the target has the Poisoned condition."},
		{Name: "Plain", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 5 (1d6 + 2) Slashing damage."},
	}
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented rider fixture", "monsters": []srdMonster2024{m}})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	key := imported["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	cp := fmt.Sprintf("/api/campaigns/%d/combat", camp)
	pgCall(t, "POST", cp+"/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Rider Brute", "monster_key": key, "hp": 1000, "ac": 1, "initiative": -100}}})
	pgExec(t, `UPDATE companions SET hp=100000,max_hp=100000,ac=1 WHERE id=$1`, petID)
	conds := func() (out string) {
		rows, err := db.Query(`SELECT condition FROM condition_records WHERE target_id=$1 ORDER BY condition`, companionIDBase+petID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var c string
			rows.Scan(&c)
			out += c + ","
		}
		return
	}
	attack := func(action string) string {
		for n := 0; n < 30; n++ { // a natural 1 misses: retry until the reply's own hit shows damage
			removeConditionsWhere(camp, companionIDBase+petID, func(conditionRecord) bool { return true })
			c, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "The brute attacks.", "monster_action": map[string]interface{}{"monster": "Rider Brute", "action": action, "target": "React Pet"}})
			if c != 200 {
				t.Fatalf("%s: %d %v", action, c, j)
			}
			if r := fmt.Sprint(j["monster_action_result"]); strings.Contains(r, " damage") && !strings.Contains(r, "misses") {
				return r
			}
		}
		t.Fatalf("%s never hit", action)
		return ""
	}
	if r := attack("Plain"); conds() != "" || strings.Contains(r, "not applied automatically") {
		t.Errorf("control: a plain hit gave a condition or a GM flag: %q %s", conds(), r)
	}
	if r := attack("Zap"); !strings.Contains(conds(), "stunned") || !strings.Contains(r, "Stunned") {
		t.Errorf("Zap: conditions %q, reply %s", conds(), r)
	}
	if r := attack("Jab"); strings.Contains(conds(), "prone") || !strings.Contains(r, "size or smaller") || !strings.Contains(r, "GM, not applied automatically") {
		t.Errorf("Jab (size-gated): conditions %q, reply %s", conds(), r)
	}
	// audience: the shared log never stores the GM's note (raw rows, then every player-facing surface), the GM has it as a private notice
	var stored int
	if err := db.QueryRow(`SELECT COUNT(*) FROM actions WHERE lobby_id=$1 AND (COALESCE(result,'') ILIKE '%[GM%' OR COALESCE(result,'') ILIKE '%size or smaller%' OR COALESCE(description,'') ILIKE '%size or smaller%')`, camp).Scan(&stored); err != nil || stored != 0 {
		t.Errorf("the shared action log stores the GM's note in %d rows (err %v)", stored, err)
	}
	notice := false
	for _, n := range noticesOf(t, camp) {
		if strings.Contains(n.Text, "size or smaller") {
			notice = true
		}
	}
	if !notice {
		t.Errorf("the GM's note is missing from the private notices")
	}
	for _, path := range []string{fmt.Sprintf("/api/campaigns/%d/feed", camp), fmt.Sprintf("/api/campaigns/%d", camp), fmt.Sprintf("/api/campaigns/%d/combat", camp), "/api/my-turn", fmt.Sprintf("/api/characters/%d", charID)} {
		_, j := pgCall(t, "GET", path, player, nil)
		body := fmt.Sprint(j)
		if strings.HasSuffix(path, "/feed") && !strings.Contains(body, "Rider Brute") { // control: the surface does show the monster's turn
			t.Errorf("feed control: the monster's action is not in the feed, so the check proves nothing: %.300s", body)
		}
		if strings.Contains(body, "size or smaller") || strings.Contains(body, "[GM") {
			t.Errorf("%s shows the GM's note to a player", path)
		}
	}
	if r := attack("Venom"); !strings.Contains(conds(), "poisoned") || !regexp.MustCompile(`React Pet takes \d+ acid damage \(`).MatchString(r) || !strings.Contains(r, "Poisoned") {
		t.Errorf("Venom (own saving throw, failed at DC 99): conditions %q, reply %s", conds(), r)
	}
	if r := attack("Mild"); strings.Contains(conds(), "poisoned") || strings.Contains(r, "Poisoned") || !strings.Contains(r, "succeeds on the Constitution saving throw (") {
		t.Errorf("Mild (own saving throw, DC 1: always passed): conditions %q, reply %s", conds(), r)
	}
}

// A companion's damage goes through its stat block's resistances, immunities and vulnerabilities (damageCompanion; before, every type
// was taken in full). Routes: the GM's exact damage (POST /api/gm/companion) and a monster's attack on the companion (POST
// /api/gm/narrate monster_action). The companion's form is the monster it was made from. Not covered: absorbing traits, Petrified,
// underwater fire and a Polymorph form on a companion (the last two are not applied to companions).
func TestPostgresCompanionDamageTypes(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, petID, _ := companionReactionFixture(t)
	m := compendiumFixture()
	m.Name, m.Type = "Comp Resist Beast", "Beast"
	m.Resistances, m.DmgImmune, m.Vulnerable = "Poison, Piercing", "Fire", "Cold"
	m.Actions = []srdMonsterEntry{{Name: "Bite", Text: "Melee Attack Roll: +30, reach 5 ft. Hit: 7 (1d8 + 3) Piercing damage."}}
	st, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented resistance fixture", "monsters": []srdMonster2024{m}})
	if st != 201 {
		t.Fatalf("import: %d %v", st, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	pgExec(t, `UPDATE companions SET form=$1,hp=1000,max_hp=1000 WHERE id=$2`, key, petID)
	hp := func() (h int) { db.QueryRow(`SELECT hp FROM companions WHERE id=$1`, petID).Scan(&h); return }
	hit := func(amount int, dtype string) (int, string) {
		pgExec(t, `UPDATE companions SET hp=1000 WHERE id=$1`, petID)
		c, r := pgCall(t, "POST", "/api/gm/companion", gm, map[string]interface{}{"campaign_id": camp, "companion_id": petID, "damage": amount, "damage_type": dtype})
		if c != 200 {
			t.Fatalf("damage %s: %d %v", dtype, c, r)
		}
		return 1000 - hp(), fmt.Sprint(r)
	}
	for _, tc := range []struct {
		dtype string
		lost  int
		word  string
	}{{"acid", 20, ""}, {"poison", 10, "resists"}, {"fire", 0, "immune"}, {"cold", 40, "vulnerable"}, {"Poison", 10, "resists"}} {
		if lost, reply := hit(20, tc.dtype); lost != tc.lost || (tc.word != "" && !strings.Contains(reply, tc.word)) {
			t.Errorf("20 %s damage: lost %d (want %d), reply %s", tc.dtype, lost, tc.lost, reply)
		}
	}
	// a monster's attack: the Bite is Piercing, which this companion resists
	cp := fmt.Sprintf("/api/campaigns/%d/combat", camp)
	_ = cp
	pgExec(t, `UPDATE companions SET hp=1000,ac=1 WHERE id=$1`, petID)
	for n := 0; n < 30; n++ {
		pgExec(t, `UPDATE companions SET hp=1000 WHERE id=$1`, petID)
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": map[string]interface{}{"monster": "Opponent", "action": "Bite", "target": "React Pet"}})
		res := fmt.Sprint(r["monster_action_result"])
		if !strings.Contains(res, " damage") || strings.Contains(res, "misses") {
			continue
		}
		if lost := 1000 - hp(); !strings.Contains(res, "(it resists)") || lost > 9 || lost < 1 {
			t.Errorf("monster attack on a Piercing-resistant companion: lost %d, reply %s", lost, res)
		}
		return
	}
	t.Fatal("the monster's attack never hit")
}

// A condition on a companion reaches the attack rolls against it (creatureConditions reads a companion's records like a monster's;
// before, a stored condition on a companion changed nothing for attackers). Route: POST /api/gm/narrate monster_action. Control: the
// same attack on the companion with no condition has no Advantage. Not covered: saving throws of a companion, Restrained and the
// other conditions that change attack rolls (only Paralyzed, which also makes a melee hit within 5 feet critical, is driven).
func TestPostgresCompanionConditionsAffectAttacks(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, petID, _ := companionReactionFixture(t)
	pgExec(t, `UPDATE companions SET hp=100000,max_hp=100000,ac=1 WHERE id=$1`, petID)
	attack := func() string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": map[string]interface{}{"monster": "Opponent", "action": "Bite", "target": "React Pet"}})
		return fmt.Sprint(r["monster_action_result"])
	}
	for n := 0; n < 4; n++ {
		if r := attack(); strings.Contains(r, "Advantage") || strings.Contains(r, "Paralyzed") {
			t.Fatalf("control: an unconditioned companion's attack roll shows a condition: %s", r)
		}
	}
	addCondition(camp, companionIDBase+petID, condAdd{Condition: "paralyzed", Source: "test", Rounds: 10})
	for n := 0; n < 4; n++ {
		if r := attack(); !strings.Contains(r, "with Advantage (the target is Paralyzed)") || !strings.Contains(r, "CRITICAL HIT") {
			t.Errorf("a Paralyzed companion: want Advantage and a critical hit, got %s", r)
		}
	}
}

// Magic Resistance and the Legendary Resistance flag for a companion (the stat block it was made from has both): the save rule gives
// Advantage against a spell (HELPER LEVEL: saveCtx.gather on the companion's combat id; no player or GM route aims a saving-throw
// spell at a companion yet, so this proves the shared rule reads a companion's traits, not a route), (the Legendary Resistance GM flag is NOT tested here)
// (the Legendary Resistance GM flag is not tested).
func TestPostgresCompanionMagicAndLegendaryResistance(t *testing.T) {
	testPostgres(t)
	_, _, camp, _, petID, _ := companionReactionFixture(t)
	// the fixture's stat block has Magic Resistance and Legendary Resistance traits (compendiumFixture); Comp React Beast clears them, so use the traits on the row
	pgExec(t, `UPDATE companions SET traits=$2::jsonb WHERE id=$1`, petID, `[{"name":"Magic Resistance","text":"Advantage on saving throws against spells."}]`)
	cid := companionIDBase + petID
	adv := func(spell bool) []string {
		c := &saveCtx{lobby: camp, saver: cid, ability: "wis", dc: 10, spell: spell}
		return c.gather().Advantage
	}
	if got := adv(true); len(got) != 1 || !strings.Contains(strings.Join(got, ","), "Magic Resistance") {
		t.Errorf("a spell save of a companion with Magic Resistance: Advantage %v", got)
	}
	if got := adv(false); len(got) != 0 {
		t.Errorf("control: a save that is not against a spell has Advantage %v", got)
	}
	pgExec(t, `UPDATE companions SET traits='[]'::jsonb WHERE id=$1`, petID)
	if got := adv(true); len(got) != 0 {
		t.Errorf("control: a companion without the trait has Advantage %v", got)
	}
}

// A monster's attack on a companion respects the battle map's reach, and the Critical Hit a Paralyzed target gives needs the attacker
// within 5 feet (the published rule); Advantage against a Paralyzed target does not (monsterAttacksCompanion now reads the map as the
// attack on a character does). Route: PUT /api/campaigns/{id}/battle-map for the tokens, POST /api/gm/narrate monster_action.
// Negative controls: a 10 ft hit with a 15 ft reach has Advantage and is mostly NOT a critical hit (only a natural 20 is), a target past
// the reach is not attacked and loses no Hit Points. Not covered: ranged attacks and long range against a companion on the map.
func TestPostgresCompanionParalyzedCriticalNeedsProximity(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, petID, _ := companionReactionFixture(t)
	m := compendiumFixture()
	m.Name, m.Type = "Comp Reach Brute", "Giant"
	m.Actions = []srdMonsterEntry{
		{Name: "Lash", Text: "Melee Attack Roll: +60, reach 15 ft. Hit: 5 (1d6 + 2) Slashing damage."},
		{Name: "Bite", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 5 (1d6 + 2) Piercing damage."},
	}
	st, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented reach fixture", "monsters": []srdMonster2024{m}})
	if st != 201 {
		t.Fatalf("import: %d %v", st, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Reach Brute", "monster_key": key, "hp": 1000, "ac": 1, "initiative": -100}}})
	brute := monsterIDsByKeyOrName(camp, "Reach Brute")[0]
	pet := companionIDBase + petID
	pgExec(t, `UPDATE companions SET hp=100000,max_hp=100000,ac=1 WHERE id=$1`, petID)
	place := func(petX int) {
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"............"}, Tokens: []mapToken{
			{ID: brute, Name: "Reach Brute", Position: mapSquare{0, 0}, Size: 1}, {ID: pet, Name: "React Pet", Position: mapSquare{petX, 0}, Size: 1}}}, "forced_movement": true}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	hp := func() (h int) { db.QueryRow(`SELECT hp FROM companions WHERE id=$1`, petID).Scan(&h); return }
	attack := func(action string) string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": map[string]interface{}{"monster": "Reach Brute", "action": action, "target": "React Pet"}})
		return fmt.Sprint(r["monster_action_result"])
	}
	addCondition(camp, pet, condAdd{Condition: "paralyzed", Source: "test", Rounds: 100})
	place(1) // 5 ft: Advantage and a critical hit on every hit
	for n := 0; n < 8; n++ {
		if r := attack("Bite"); strings.Contains(r, " damage") && (!strings.Contains(r, "with Advantage (the target is Paralyzed)") || !strings.Contains(r, "CRITICAL HIT")) {
			t.Errorf("5 ft, Paralyzed: want Advantage and a critical hit, got %s", r)
		}
	}
	place(2) // 10 ft: inside the Lash's 15 ft reach, not within 5 ft
	sawPlainHit := false
	for n := 0; n < 40; n++ {
		r := attack("Lash")
		if !strings.Contains(r, " damage") {
			continue
		}
		if !strings.Contains(r, "with Advantage (the target is Paralyzed)") {
			t.Fatalf("10 ft, Paralyzed: Advantage must stay (it does not need proximity): %s", r)
		}
		if !strings.Contains(r, "CRITICAL HIT") {
			sawPlainHit = true
		}
	}
	if !sawPlainHit {
		t.Errorf("10 ft, Paralyzed: every hit was critical, so the 5 ft requirement is not applied")
	}
	before := hp()
	if r := attack("Bite"); hp() != before || !strings.Contains(r, "beyond its 5 ft reach - no attack") {
		t.Errorf("a Bite from 10 ft: Hit Points %d -> %d, reply %s", before, hp(), r)
	}
	place(4) // 20 ft: beyond the Lash too
	before = hp()
	if r := attack("Lash"); hp() != before || !strings.Contains(r, "beyond its 15 ft reach - no attack") {
		t.Errorf("a Lash from 20 ft: Hit Points %d -> %d, reply %s", before, hp(), r)
	}
}

// A monster's saving-throw effect (no attack roll) names a companion: the shared save rule rolls it with the companion's stat block and
// deals the damage through the companion Hit Point owner (targetIDsFrom resolves companions, applySaveEffect has the companion branch).
// Route: POST /api/gm/narrate monster_action with target (name) or target_ids (combat id). Controls: DC 1 passes (nothing dealt), a name
// that is no companion of the campaign deals nothing. An Emanation on the battle map with no one named
// reaches an active companion in it and not one outside it (see the map case below). Not covered: a Line, Cone or Sphere aimed with
// "point" (areaCharacters now lists companions, not route-tested), monster spells, Legendary Resistance.
func TestPostgresMonsterEffectAtCompanion(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, petID, _ := companionReactionFixture(t)
	m := compendiumFixture()
	m.Name, m.Type = "Comp Pulse Caster", "Aberration"
	m.Actions = []srdMonsterEntry{
		{Name: "Hard Pulse", Text: "Dexterity Saving Throw: DC 99, one creature. Failure: 10 (3d6) Force damage. Success: No damage."},
		{Name: "Ring Pulse", Text: "Dexterity Saving Throw: DC 99, each creature in a 10-foot Emanation. Failure: 10 (3d6) Force damage. Success: No damage."},
		{Name: "Soft Pulse", Text: "Dexterity Saving Throw: DC 1, one creature. Failure: 10 (3d6) Force damage. Success: No damage."},
	}
	st, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented effect fixture", "monsters": []srdMonster2024{m}})
	if st != 201 {
		t.Fatalf("import: %d %v", st, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Pulse Caster", "monster_key": key, "hp": 1000, "ac": 1, "initiative": -100}}})
	pet := companionIDBase + petID
	pgExec(t, `UPDATE companions SET hp=100000,max_hp=100000 WHERE id=$1`, petID)
	hp := func() (h int) { db.QueryRow(`SELECT hp FROM companions WHERE id=$1`, petID).Scan(&h); return }
	run := func(action string, ma map[string]interface{}) string {
		ma["monster"], ma["action"] = "Pulse Caster", action
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": ma})
		return fmt.Sprint(r["monster_action_result"])
	}
	for name, ma := range map[string]map[string]interface{}{"target_ids": {"target_ids": []int{pet}}, "target name": {"target": "React Pet"}} {
		before := hp()
		if r := run("Hard Pulse", ma); hp() >= before {
			t.Errorf("%s: a failed save must deal damage (%d -> %d): %s", name, before, hp(), r)
		}
	}
	before := hp()
	if r := run("Soft Pulse", map[string]interface{}{"target_ids": []int{pet}}); hp() != before {
		t.Errorf("DC 1 save passed but Hit Points %d -> %d: %s", before, hp(), r)
	}
	if r := run("Hard Pulse", map[string]interface{}{"target": "No Such Pet"}); hp() != before {
		t.Errorf("an unknown name hurt the companion: %s", r)
	}
	// the Emanation on the battle map: a companion 10 ft away is in it, one 30 ft away is not
	place := func(x int) {
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		brute := monsterIDsByKeyOrName(camp, "Pulse Caster")[0]
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"..................."}, Tokens: []mapToken{
			{ID: brute, Name: "Pulse Caster", Position: mapSquare{0, 0}, Size: 1}, {ID: pet, Name: "React Pet", Position: mapSquare{x, 0}, Size: 1}}}, "forced_movement": true}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	place(2)
	before = hp()
	if r := run("Ring Pulse", map[string]interface{}{}); hp() >= before {
		t.Errorf("Emanation 10 ft away: the companion in it was not affected (%d -> %d): %s", before, hp(), r)
	}
	place(6)
	before = hp()
	if r := run("Ring Pulse", map[string]interface{}{}); hp() != before {
		t.Errorf("Emanation, companion 30 ft away was hurt (%d -> %d): %s", before, hp(), r)
	}
}

// Campaign scope of a monster's save effect at companions: another campaign's companion must never get a save, damage or a condition
// from THIS campaign's monster, named by combat id, by a name both companions share, or by a map token placed inside an Emanation.
// Controls: the local companion IS hurt and poisoned in each case. Status: a dismissed or gone local companion is not affected.
// Route: POST /api/gm/narrate monster_action in campaign A; B's companion is checked in the database (Hit Points, condition records).
func TestPostgresMonsterEffectCompanionCampaignScope(t *testing.T) {
	testPostgres(t)
	gm, _, campA, _, petA, _ := companionReactionFixture(t)
	_, _, campB, _, petB, _ := companionReactionFixture(t)
	if campA == campB {
		t.Fatal("setup")
	}
	m := compendiumFixture()
	m.Name, m.Type = "Scope Pulse Caster", "Aberration"
	m.Actions = []srdMonsterEntry{
		{Name: "Venom Pulse", Text: "Dexterity Saving Throw: DC 99, one creature. Failure: 10 (3d6) Force damage, and the target has the Poisoned condition."},
		{Name: "Ring Pulse", Text: "Dexterity Saving Throw: DC 99, each creature in a 30-foot Emanation. Failure: 10 (3d6) Force damage."},
	}
	st, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented scope fixture", "monsters": []srdMonster2024{m}})
	if st != 201 {
		t.Fatalf("import: %d %v", st, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	cp := fmt.Sprintf("/api/campaigns/%d", campA)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Scope Caster", "monster_key": key, "hp": 1000, "ac": 1, "initiative": -100}}})
	caster := monsterIDsByKeyOrName(campA, "Scope Caster")[0]
	pgExec(t, `UPDATE companions SET hp=100000,max_hp=100000 WHERE id IN ($1,$2)`, petA, petB)
	pa, _ := loadCompanion(petA)
	pb, _ := loadCompanion(petB)
	if pa.Name != pb.Name || pa.CombatID == pb.CombatID {
		t.Fatalf("setup: names %q %q ids %d %d", pa.Name, pb.Name, pa.CombatID, pb.CombatID)
	}
	hp := func(id int) (h int) { db.QueryRow(`SELECT hp FROM companions WHERE id=$1`, id).Scan(&h); return }
	conds := func(combatID int) (n int) {
		db.QueryRow(`SELECT COUNT(*) FROM condition_records WHERE target_id=$1`, combatID).Scan(&n)
		return
	}
	run := func(action string, ma map[string]interface{}) string {
		ma["monster"], ma["action"] = "Scope Caster", action
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": campA, "narration": "x", "monster_action": ma})
		return fmt.Sprint(r["monster_action_result"])
	}
	// 1. B's companion named by its combat id: nothing happens to it (and nothing to A's)
	bHP, aHP := hp(petB), hp(petA)
	r := run("Venom Pulse", map[string]interface{}{"target_ids": []int{pb.CombatID}})
	if hp(petB) != bHP || conds(pb.CombatID) != 0 || hp(petA) != aHP {
		t.Errorf("foreign combat id: B hp %d->%d conditions %d, A hp %d->%d: %s", bHP, hp(petB), conds(pb.CombatID), aHP, hp(petA), r)
	}
	// 2. the shared name resolves to A's own companion only (control: it is hurt and poisoned)
	run("Venom Pulse", map[string]interface{}{"target": pa.Name})
	if hp(petA) >= aHP || conds(pa.CombatID) == 0 || hp(petB) != bHP || conds(pb.CombatID) != 0 {
		t.Errorf("shared name: A hp %d->%d conditions %d (want hurt, poisoned), B hp %d->%d conditions %d (want none)", aHP, hp(petA), conds(pa.CombatID), bHP, hp(petB), conds(pb.CombatID))
	}
	// 3. a map token with B's combat id inside A's Emanation is not affected; A's own token is (control)
	_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
	req := map[string]interface{}{"map": encounterMap{Grid: []string{"............"}, Tokens: []mapToken{
		{ID: caster, Name: "Scope Caster", Position: mapSquare{0, 0}, Size: 1},
		{ID: pa.CombatID, Name: "A Pet", Position: mapSquare{1, 0}, Size: 1}}}, "forced_movement": true}
	if rev, ok := state["revision"].(string); ok {
		req["confirmation"] = rev
	}
	if c, rr := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
		t.Fatalf("put map: %d %v", c, rr)
	}
	// the map route refuses a token that is not in this fight's initiative (token_not_in_initiative), so the foreign token is written
	// straight into the stored map: the boundary under test must hold even when a stale or forged token is there
	foreign, _ := json.Marshal([]mapToken{{ID: pb.CombatID, Name: "B Pet", Position: mapSquare{2, 0}, Size: 1}})
	pgExec(t, `UPDATE combat_state SET battle_map = jsonb_set(battle_map::jsonb, '{tokens}', (battle_map::jsonb->'tokens') || $2::jsonb) WHERE lobby_id=$1 AND active = true`, campA, string(foreign))
	if mp, _ := activeEncounterMap(campA); len(mp.Tokens) != 3 {
		t.Fatalf("setup: the foreign token is not on the map: %+v", mp.Tokens)
	}
	aHP, bHP = hp(petA), hp(petB)
	r = run("Ring Pulse", map[string]interface{}{})
	if hp(petA) >= aHP || hp(petB) != bHP {
		t.Errorf("Emanation: A hp %d->%d (want hurt), B hp %d->%d (want unchanged): %s", aHP, hp(petA), bHP, hp(petB), r)
	}
	// 4. a dismissed and a gone local companion are not affected, even named by combat id or inside the Emanation
	for _, status := range []string{"dismissed", "gone"} {
		pgExec(t, `UPDATE companions SET status=$2 WHERE id=$1`, petA, status)
		aHP = hp(petA)
		run("Venom Pulse", map[string]interface{}{"target_ids": []int{pa.CombatID}})
		run("Ring Pulse", map[string]interface{}{})
		if hp(petA) != aHP {
			t.Errorf("a %s companion was damaged (%d -> %d)", status, aHP, hp(petA))
		}
	}
}

// The end-of-turn countdown of a companion's condition through the real turn-advance route (POST /api/campaigns/{id}/combat/next),
// not endTurnEffects called directly. The companion has its own turn entry (placed in the order, as AGENTRPG_FAMILIAR_INITIATIVE=own
// does). Round 1 = Stunned for 2 rounds: the enemy's turn end changes nothing, the companion's first turn end takes a round off, its
// second ends the condition. Controls: the counts after each advance, and the turn actually moved each time.
func TestPostgresCompanionConditionCountdownThroughTurnAdvance(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, petID, _ := companionReactionFixture(t)
	pet, _ := loadCompanion(petID)
	changeFight(camp, func(f *fight) error {
		f.place(map[string]interface{}{"id": pet.CombatID, "name": pet.Name, "initiative": 5, "dex_score": 12, "is_companion": true, "owner_id": pet.OwnerID})
		return nil
	})
	if r := addCondition(camp, pet.CombatID, condAdd{Condition: "stunned", Source: "turn route test", Rounds: 2}); r.Err != nil || !r.Added {
		t.Fatalf("stunned: %+v", r)
	}
	cp := fmt.Sprintf("/api/campaigns/%d/combat", camp)
	current := func() int {
		f, err := readFight(db, camp)
		if err != nil {
			t.Fatal(err)
		}
		return asInt(f.Order[f.Index]["id"])
	}
	rounds := func() int {
		recs := loadConditionRecords(camp, pet.CombatID)
		if len(recs) == 0 {
			return -1
		}
		return recs[0].DurationRounds
	}
	// the turn is the companion's (the order also holds the character and the enemy)
	changeFight(camp, func(f *fight) error {
		i, _ := f.entry(pet.CombatID)
		f.Index = i
		return nil
	})
	if current() != pet.CombatID {
		t.Fatalf("setup: the turn is %d, not the companion's %d", current(), pet.CombatID)
	}
	next := func(why string) {
		t.Helper()
		if st, j := pgCall(t, "POST", cp+"/next", gm, map[string]interface{}{}); st != 200 {
			t.Fatalf("%s: next %d %v", why, st, j)
		}
	}
	next("the companion's turn ends (first)")
	if current() == pet.CombatID || rounds() != 1 {
		t.Errorf("after the companion's first turn end: turn %d, rounds left %d (want another creature's turn, 1)", current(), rounds())
	}
	for n := 0; current() != pet.CombatID; n++ { // other creatures' turns end: the condition is not touched
		if n > 6 {
			t.Fatalf("the turn never came back to the companion")
		}
		next("another creature's turn ends")
		if rounds() != 1 {
			t.Errorf("another creature's turn end changed the companion's condition: %d", rounds())
		}
	}
	next("the companion's turn ends (second)")
	if rounds() != -1 {
		t.Errorf("the companion's second turn end should end the condition: %d", rounds())
	}
}

// A Sphere aimed at a grid square with "point" (POST /api/gm/narrate monster_action) reaches the active companions standing in it and
// not the ones outside it, with the shared save rule (areaCharacters lists companions). The GM names nobody. Controls: the companion
// inside is hurt, the one outside (another of this campaign's tokens at 40 ft from the point) is not. Not covered: Line and Cone aimed
// with point, a Sphere centered on a listed target.
func TestPostgresMonsterSphereAtPointReachesCompanions(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, petID, _ := companionReactionFixture(t)
	m := compendiumFixture()
	m.Name, m.Type = "Point Burst Caster", "Aberration"
	m.Actions = []srdMonsterEntry{{Name: "Burst", Text: "Dexterity Saving Throw: DC 99, each creature in a 10-foot-radius Sphere. Failure: 10 (3d6) Fire damage. Success: Half damage."}}
	st, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented point fixture", "monsters": []srdMonster2024{m}})
	if st != 201 {
		t.Fatalf("import: %d %v", st, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Burst Caster", "monster_key": key, "hp": 1000, "ac": 1, "initiative": -100}}})
	caster := monsterIDsByKeyOrName(camp, "Burst Caster")[0]
	pet := companionIDBase + petID
	pgExec(t, `UPDATE companions SET hp=100000,max_hp=100000 WHERE id=$1`, petID)
	hp := func() (h int) { db.QueryRow(`SELECT hp FROM companions WHERE id=$1`, petID).Scan(&h); return }
	put := func(petX int) {
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"............................."}, Tokens: []mapToken{
			{ID: caster, Name: "Burst Caster", Position: mapSquare{0, 0}, Size: 1}, {ID: pet, Name: "React Pet", Position: mapSquare{petX, 0}, Size: 1}}}, "forced_movement": true}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	burst := func() string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": map[string]interface{}{"monster": "Burst Caster", "action": "Burst", "point": []int{10, 0}}})
		return fmt.Sprint(r["monster_action_result"])
	}
	put(11) // 5 ft from the point (10,0): inside the 10-foot radius
	before := hp()
	if r := burst(); hp() >= before {
		t.Errorf("the companion inside the Sphere was not affected (%d -> %d): %s", before, hp(), r)
	}
	put(18) // 40 ft from the point
	before = hp()
	if r := burst(); hp() != before {
		t.Errorf("the companion outside the Sphere was affected (%d -> %d): %s", before, hp(), r)
	}
}

// A monster's spell at a companion (monster Spellcasting through POST /api/gm/narrate, target_ids = the companion's combat id). Saving
// throw spells use the shared save rule (Hold Person: Paralyzed stored; Fireball: damage through the companion Hit Points). A spell
// ATTACK roll (Fire Bolt) and Command are not automated for a companion: the reply is a GM line, nothing is rolled, dealt or stored. The
// probe that found this printed a blank name, AC 0 and "HIT, takes 0 damage" (the code read a character row). Controls: Fire Bolt at the
// character is still resolved. Not covered: Command by name beyond this refusal, other spell families (summons, buffs) at a companion.
func TestPostgresMonsterSpellAtCompanion(t *testing.T) {
	testPostgres(t)
	gm, _, camp, char, petID, _ := companionReactionFixture(t)
	m := compendiumFixture()
	m.Name, m.Type = "Probe Spell Caster", "Aberration"
	m.Actions = []srdMonsterEntry{{Name: "Spellcasting", Text: "The caster casts one of the following spells, using Intelligence as the spellcasting ability (spell save DC 99, +60 to hit with spell attacks): At Will: Fire Bolt, Hold Person, Command. 1/Day each: Fireball."}}
	st, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented spell fixture", "monsters": []srdMonster2024{m}})
	if st != 201 {
		t.Fatalf("import: %d %v", st, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Probe Caster", "monster_key": key, "hp": 1000, "ac": 1, "initiative": -100}}})
	pet := companionIDBase + petID
	pgExec(t, `UPDATE companions SET hp=100000,max_hp=100000,ac=1 WHERE id=$1`, petID)
	hp := func() (h int) { db.QueryRow(`SELECT hp FROM companions WHERE id=$1`, petID).Scan(&h); return }
	cast := func(sp string, ids []int) string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": map[string]interface{}{"monster": "Probe Caster", "action": "Spellcasting", "use": sp, "target_ids": ids}})
		return fmt.Sprint(r["monster_action_result"])
	}
	for _, sp := range []string{"Fire Bolt", "Command"} {
		before, nc := hp(), len(loadConditionRecords(camp, pet))
		r := cast(sp, []int{pet})
		if hp() != before || len(loadConditionRecords(camp, pet)) != nc || !strings.Contains(r, "not resolved by the server") || strings.Contains(r, "HIT") {
			t.Errorf("%s at a companion: want a neutral line, nothing changed (hp %d -> %d): %s", sp, before, hp(), r)
		}
		found := false
		for _, n := range noticesOf(t, camp) {
			if strings.Contains(n.Text, "not automated") && strings.Contains(n.Text, sp) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s at a companion: the GM's notices do not carry the explanation", sp)
		}
	}
	before := hp()
	if r := cast("Fireball", []int{pet}); hp() >= before {
		t.Errorf("Fireball at a companion should deal damage through the save rule (%d -> %d): %s", before, hp(), r)
	} else if strings.Contains(r, "Legendary Resistance") {
		t.Errorf("the stat block's Legendary Resistance reached the reply that is also written to the shared action log: %s", r)
	}
	var logged int
	db.QueryRow(`SELECT COUNT(*) FROM actions WHERE lobby_id=$1 AND result ILIKE '%Legendary Resistance%'`, camp).Scan(&logged)
	if logged != 0 {
		t.Errorf("Legendary Resistance reached the shared action log (%d rows)", logged)
	}
	lr := false
	for _, n := range noticesOf(t, camp) {
		if strings.Contains(n.Text, "Legendary Resistance") {
			lr = true
		}
	}
	if !lr {
		t.Errorf("the failed save of a stat block with Legendary Resistance gave the GM no notice")
	}
	// Hold Person names a Humanoid (SRD 5.2.1): the Beast companion is ineligible (nothing rolled, no Paralyzed), a Humanoid companion is
	// eligible (positive control: Paralyzed stored)
	nc := len(loadConditionRecords(camp, pet))
	if r := cast("Hold Person", []int{pet}); len(loadConditionRecords(camp, pet)) != nc || !strings.Contains(r, "isn't a Humanoid") || strings.Contains(r, "saving throw") {
		t.Errorf("Hold Person at a Beast companion must have no effect and roll nothing: %s", r)
	}
	hm := compendiumFixture()
	hm.Name, hm.Type = "Comp Humanoid Pet", "Humanoid"
	hm.Actions = []srdMonsterEntry{{Name: "Club", Text: "Melee Attack Roll: +3, reach 5 ft. Hit: 3 (1d4 + 1) Bludgeoning damage."}}
	hst, hj := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented humanoid fixture", "monsters": []srdMonster2024{hm}})
	if hst != 201 {
		t.Fatalf("import humanoid: %d %v", hst, hj)
	}
	hkey := hj["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, hkey) })
	cj := map[string]interface{}{}
	if cs, c2 := pgCall(t, "POST", "/api/gm/companion", gm, map[string]interface{}{"campaign_id": camp, "character_id": char, "monster_key": hkey, "name": "Humanoid Pet"}); cs != 200 && cs != 201 {
		t.Fatalf("humanoid companion: %d %v", cs, c2)
	} else {
		cj = c2
	}
	hpet := companionIDBase + int(cj["companion"].(map[string]interface{})["id"].(float64))
	if r := cast("Hold Person", []int{hpet}); len(loadConditionRecords(camp, hpet)) == 0 {
		t.Errorf("Hold Person at a Humanoid companion should store Paralyzed: %s", r)
	}
	pgExec(t, `UPDATE characters SET hp=1000,ac=1 WHERE id=$1`, char)
	if r := cast("Fire Bolt", []int{char}); !strings.Contains(r, "HIT") && !strings.Contains(r, "MISS") && !strings.Contains(r, "hit") {
		t.Errorf("control: Fire Bolt at a character should still be resolved: %s", r)
	}
}

// Antimagic Field and a companion (spells_more_2024.go antimagicCovers reads map tokens by id): a monster's spell at a companion inside
// a field cast by another creature has no effect and rolls nothing; the same companion outside the 10 ft is affected (control). The field
// is stored with addSpellEffect (helper), the spell goes through POST /api/gm/narrate monster_action. Not covered: Globe of Invulnerability
// (same pattern, untested for companions), a field cast by a character.
func TestPostgresAntimagicFieldCoversCompanion(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, petID, enemy := companionReactionFixture(t)
	m := compendiumFixture()
	m.Name, m.Type = "Field Spell Caster", "Aberration"
	m.Actions = []srdMonsterEntry{{Name: "Spellcasting", Text: "The caster casts one of the following spells, using Intelligence as the spellcasting ability (spell save DC 99, +60 to hit with spell attacks): At Will: Fireball."}}
	st, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented field fixture", "monsters": []srdMonster2024{m}})
	if st != 201 {
		t.Fatalf("import: %d %v", st, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Field Caster", "monster_key": key, "hp": 1000, "ac": 1, "initiative": -100}}})
	caster := monsterIDsByKeyOrName(camp, "Field Caster")[0]
	pet := companionIDBase + petID
	pgExec(t, `UPDATE companions SET hp=100000,max_hp=100000 WHERE id=$1`, petID)
	addSpellEffect(camp, enemy, enemy, "Antimagic Field", "antimagic_field", "", 100, "", 0)
	hp := func() (h int) { db.QueryRow(`SELECT hp FROM companions WHERE id=$1`, petID).Scan(&h); return }
	place := func(petX int) {
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"............................."}, Tokens: []mapToken{
			{ID: enemy, Name: "Opponent", Position: mapSquare{0, 0}, Size: 1}, {ID: pet, Name: "React Pet", Position: mapSquare{petX, 0}, Size: 1},
			{ID: caster, Name: "Field Caster", Position: mapSquare{20, 0}, Size: 1}}}, "forced_movement": true}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	cast := func() string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": map[string]interface{}{"monster": "Field Caster", "action": "Spellcasting", "use": "Fireball", "target_ids": []int{pet}}})
		return fmt.Sprint(r["monster_action_result"])
	}
	place(1) // 5 ft from the field's caster
	before := hp()
	if r := cast(); hp() != before || !strings.Contains(r, "Antimagic Field") {
		t.Errorf("a companion inside the field: want no effect and the field named (%d -> %d): %s", before, hp(), r)
	}
	place(5) // 25 ft: outside
	before = hp()
	if r := cast(); hp() >= before {
		t.Errorf("control: outside the field the spell should work (%d -> %d): %s", before, hp(), r)
	}
}

// Globe of Invulnerability and a companion (spells_more_2024.go globeShields reads map tokens by id): a level 3 monster spell at a companion
// inside a level 6 globe centered on the map square (0,0), cast from outside it has no effect and rolls nothing; the same companion outside the 10 ft is affected (control). The field
// is stored with addSpellEffect (helper), the spell goes through POST /api/gm/narrate monster_action. Not covered: a globe cast by a
// character, a spell cast from inside the globe.
func TestPostgresGlobeCoversCompanion(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, petID, enemy := companionReactionFixture(t)
	m := compendiumFixture()
	m.Name, m.Type = "Field Spell Caster", "Aberration"
	m.Actions = []srdMonsterEntry{{Name: "Spellcasting", Text: "The caster casts one of the following spells, using Intelligence as the spellcasting ability (spell save DC 99, +60 to hit with spell attacks): At Will: Fireball."}}
	st, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented field fixture", "monsters": []srdMonster2024{m}})
	if st != 201 {
		t.Fatalf("import: %d %v", st, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Field Caster", "monster_key": key, "hp": 1000, "ac": 1, "initiative": -100}}})
	caster := monsterIDsByKeyOrName(camp, "Field Caster")[0]
	pet := companionIDBase + petID
	pgExec(t, `UPDATE companions SET hp=100000,max_hp=100000 WHERE id=$1`, petID)
	addSpellEffect(camp, enemy, enemy, "Globe of Invulnerability", "globe_of_invulnerability", "6|0|0", 100, "", 0)
	hp := func() (h int) { db.QueryRow(`SELECT hp FROM companions WHERE id=$1`, petID).Scan(&h); return }
	place := func(petX int) {
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"............................."}, Tokens: []mapToken{
			{ID: enemy, Name: "Opponent", Position: mapSquare{0, 0}, Size: 1}, {ID: pet, Name: "React Pet", Position: mapSquare{petX, 0}, Size: 1},
			{ID: caster, Name: "Field Caster", Position: mapSquare{20, 0}, Size: 1}}}, "forced_movement": true}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	cast := func() string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": map[string]interface{}{"monster": "Field Caster", "action": "Spellcasting", "use": "Fireball", "target_ids": []int{pet}}})
		return fmt.Sprint(r["monster_action_result"])
	}
	place(1) // 5 ft from the field's caster
	before := hp()
	if r := cast(); hp() != before || !strings.Contains(r, "Globe of Invulnerability") {
		t.Errorf("a companion inside the globe: want no effect and the field named (%d -> %d): %s", before, hp(), r)
	}
	place(5) // 25 ft: outside
	before = hp()
	if r := cast(); hp() >= before {
		t.Errorf("control: outside the globe the spell should work (%d -> %d): %s", before, hp(), r)
	}
}

// An action that "uses" another attack action ("The giant uses Bite."), aimed at a companion by combat id and by name, rolls that
// attack at the companion (+60 vs AC 1: only a natural 1 misses, so each aim is tried until a hit) and deals the damage.
func TestPostgresUsesActionAtCompanion(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, petID, _ := companionReactionFixture(t)
	m := compendiumFixture()
	m.Name, m.Type = "Uses Probe", "Giant"
	m.Actions = []srdMonsterEntry{
		{Name: "Bite", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 5 (1d6 + 2) Piercing damage."},
		{Name: "Frenzy", Text: "The giant uses Bite."},
	}
	st, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented uses fixture", "monsters": []srdMonster2024{m}})
	if st != 201 {
		t.Fatalf("import: %d %v", st, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Uses Probe", "monster_key": key, "hp": 1000, "ac": 1, "initiative": -100}}})
	pet := companionIDBase + petID
	pgExec(t, `UPDATE companions SET hp=100000,max_hp=100000,ac=1 WHERE id=$1`, petID)
	for _, ma := range []map[string]interface{}{{"target_ids": []int{pet}}, {"target": "React Pet"}} {
		var before, after int
		db.QueryRow(`SELECT hp FROM companions WHERE id=$1`, petID).Scan(&before)
		ma["monster"], ma["action"] = "Uses Probe", "Frenzy"
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": ma})
		db.QueryRow(`SELECT hp FROM companions WHERE id=$1`, petID).Scan(&after)
		if after >= before {
			if _, r2 := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": ma}); r2 != nil {
				db.QueryRow(`SELECT hp FROM companions WHERE id=$1`, petID).Scan(&after)
			}
		}
		if after >= before {
			t.Errorf("%v: the companion took no damage (%d -> %d): %v", ma, before, after, r["monster_action_result"])
		}
	}
}

// armorClassOf for a companion id (HELPER level): its own Armor Class, not the characters table (which has no such row and gave 0).
// Controls: a character and a monster still get theirs; a companion of another campaign is not read as this campaign's (it gets the
// unknown-creature default 10, a stated limitation: the default is a guess, not a flag). Route paths that reach it for a companion are
// the attack code (a monster's attack passes the companion's AC itself).
func TestPostgresArmorClassOfCompanion(t *testing.T) {
	testPostgres(t)
	_, _, campA, charA, petA, enemy := companionReactionFixture(t)
	_, _, campB, _, petB, _ := companionReactionFixture(t)
	pgExec(t, `UPDATE companions SET ac=17 WHERE id=$1`, petA)
	pgExec(t, `UPDATE companions SET ac=23 WHERE id=$1`, petB)
	pgExec(t, `UPDATE characters SET ac=14,cover_bonus=0 WHERE id=$1`, charA)
	if got := armorClassOf(campA, companionIDBase+petA); got != 17 {
		t.Errorf("companion AC: got %d, want 17", got)
	}
	if got := armorClassOf(campA, companionIDBase+petB); got == 23 {
		t.Errorf("another campaign's companion AC was read (23) from campaign A")
	}
	if got := armorClassOf(campB, companionIDBase+petB); got != 23 {
		t.Errorf("campaign B's own companion AC: got %d, want 23", got)
	}
	if got := armorClassOf(campA, charA); got != 14 {
		t.Errorf("control: character AC got %d, want 14", got)
	}
	if got := armorClassOf(campA, enemy); got != 1 {
		t.Errorf("control: monster AC got %d, want 1", got)
	}
}

// A kill by a Hit Point set (POST /api/gm/adjust-monster hp 0) of a monster whose stat block has Spellcasting gives the GM the private
// "ends Concentration" notice, as a kill by damage does; it is not in the public action log. Controls: a set that leaves it standing, and
// a monster without Spellcasting, give none. Not covered: the power-word, item and Cleric paths that also call setMonsterHP (same owner).
func TestPostgresHitPointSetKillConcentrationNote(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, _, enemy := companionReactionFixture(t)
	m := compendiumFixture()
	m.Name, m.Type = "Set Caster", "Aberration"
	m.Actions = []srdMonsterEntry{{Name: "Spellcasting", Text: "The caster casts one of the following spells, using Intelligence as the spellcasting ability (spell save DC 14): At Will: Light. 1/Day: Bless."}}
	st, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented set fixture", "monsters": []srdMonster2024{m}})
	if st != 201 {
		t.Fatalf("import: %d %v", st, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Set Caster", "monster_key": key, "hp": 50, "ac": 1, "initiative": -100}}})
	one := monsterIDsByKeyOrName(camp, "Set Caster")[0]
	set := func(id, hp int) {
		t.Helper()
		if c, r := pgCall(t, "POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target_id": id, "hp": hp}); c != 200 {
			t.Fatalf("adjust: %d %v", c, r)
		}
	}
	count := func() (n int) {
		for _, x := range noticesOf(t, camp) {
			if strings.Contains(x.Text, "ends Concentration") {
				n++
			}
		}
		return
	}
	base := count()
	set(one, 5)
	if count() != base {
		t.Errorf("a set that leaves the caster standing gave the notice")
	}
	set(one, 0)
	if count() != base+1 {
		t.Errorf("a kill by a Hit Point set gave %d notices, want 1", count()-base)
	}
	var pub int
	db.QueryRow(`SELECT COUNT(*) FROM actions WHERE lobby_id=$1 AND result ILIKE '%Concentration%'`, camp).Scan(&pub)
	if pub != 0 {
		t.Errorf("the Concentration reminder reached the public action log (%d rows)", pub)
	}
	// the GM's exact damage: a hit that leaves the caster standing gives the conditional Concentration save, a kill gives the ends-note
	set(one, 40)
	base = count()
	conc := func() (n int) {
		for _, x := range noticesOf(t, camp) {
			if strings.Contains(x.Text, "Constitution saving throw") {
				n++
			}
		}
		return
	}
	cbase := conc()
	if c, r := pgCall(t, "POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target_id": one, "damage": 12}); c != 200 {
		t.Fatalf("exact damage: %d %v", c, r)
	}
	if conc() != cbase+1 {
		t.Errorf("exact damage that leaves the caster standing: want one Concentration save reminder, got %d", conc()-cbase)
	}
	if c, r := pgCall(t, "POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target_id": one, "damage": 500}); c != 200 {
		t.Fatalf("exact kill: %d %v", c, r)
	}
	if count() != base+1 {
		t.Errorf("an exact-damage kill gave %d ends-notices, want 1", count()-base)
	}
	base = count()
	set(enemy, 0) // control: a monster without Spellcasting (the fixture's Opponent) gives no notice
	if count() != base {
		t.Errorf("a kill of a non-caster by a Hit Point set gave the notice")
	}
}

// TestPostgresActionLogNeverStoresGMNotes: the storage boundary (writeAction) keeps a GM's private note out of the shared log for any
// writer, and sends it to the private notices. Control: the public text is stored.
func TestPostgresActionLogNeverStoresGMNotes(t *testing.T) {
	testPostgres(t)
	_, _, camp, _, _, _ := companionReactionFixture(t)
	if err := recordGMAction(db, camp, "monster_x", "Ogre: swings", "Ogre hits. [GM, not applied automatically: the secret rider text]"); err != nil {
		t.Fatal(err)
	}
	var res string
	if err := db.QueryRow(`SELECT result FROM actions WHERE lobby_id=$1 AND action_type='monster_x'`, camp).Scan(&res); err != nil || res != "Ogre hits." {
		t.Errorf("stored result %q (err %v)", res, err)
	}
	found := false
	for _, n := range noticesOf(t, camp) {
		found = found || strings.Contains(n.Text, "secret rider text")
	}
	if !found {
		t.Errorf("the private note did not reach the GM notices")
	}
}

// Line and Cone areas aimed with "point" reach companions in the shape and spare those outside it. Route POST /api/gm/narrate
// monster_action (invented stat blocks). Each shape: a companion on the axis inside the length is hurt, one beyond it is not.
// Not covered: companions off the axis (width edge cases), Cube.
func TestPostgresMonsterLineConeAtPointReachCompanions(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, petID, _ := companionReactionFixture(t)
	m := compendiumFixture()
	m.Name, m.Type = "Shape Caster", "Aberration"
	m.Actions = []srdMonsterEntry{
		{Name: "Beam", Text: "Dexterity Saving Throw: DC 99, each creature in a 30-foot-long, 5-foot-wide Line. Failure: 10 (3d6) Fire damage. Success: Half damage."},
		{Name: "Fan", Text: "Dexterity Saving Throw: DC 99, each creature in a 30-foot Cone. Failure: 10 (3d6) Fire damage. Success: Half damage."},
	}
	st, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented shape fixture", "monsters": []srdMonster2024{m}})
	if st != 201 {
		t.Fatalf("import: %d %v", st, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Shape Caster", "monster_key": key, "hp": 1000, "ac": 1, "initiative": -100}}})
	caster := monsterIDsByKeyOrName(camp, "Shape Caster")[0]
	pet := companionIDBase + petID
	pgExec(t, `UPDATE companions SET hp=100000,max_hp=100000 WHERE id=$1`, petID)
	hp := func() (h int) { db.QueryRow(`SELECT hp FROM companions WHERE id=$1`, petID).Scan(&h); return }
	put := func(petX int) {
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"........................................"}, Tokens: []mapToken{
			{ID: caster, Name: "Shape Caster", Position: mapSquare{0, 0}, Size: 1}, {ID: pet, Name: "React Pet", Position: mapSquare{petX, 0}, Size: 1}}}, "forced_movement": true}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	for _, action := range []string{"Beam", "Fan"} {
		use := func() string {
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": map[string]interface{}{"monster": "Shape Caster", "action": action, "point": []int{20, 0}}})
			return fmt.Sprint(r["monster_action_result"])
		}
		put(3) // 15 ft along the axis toward the point
		before := hp()
		if r := use(); hp() >= before {
			t.Errorf("%s: the companion inside the shape was not affected (%d -> %d): %s", action, before, hp(), r)
		}
		put(38) // 190 ft away
		before = hp()
		if r := use(); hp() != before {
			t.Errorf("%s: the companion beyond the shape was affected (%d -> %d): %s", action, before, hp(), r)
		}
	}
}
