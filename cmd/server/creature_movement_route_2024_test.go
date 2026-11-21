package main

import (
	"fmt"
	"strings"
	"testing"
)

// A charge through POST /api/gm/narrate on every target route. With a battle map the server reads the monster's own move (the GM's
// map update without forced_movement): 20 feet straight toward the target gives the Boar's Gore its extra damage and Prone; 10 feet
// gives neither, and a forced move is no charge. Without a map, "charged": true says it; without it the GM is told. The Giant
// Seahorse's alternative amount (condCharged) is judged on the companion route, which used to refuse it.
func TestPostgresChargeOnEveryTarget(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Charge Boar", "monster_key": "boar", "hp": 1000, "ac": 1, "initiative": -500},
		{"name": "Charge Seahorse", "monster_key": "giant-seahorse", "hp": 1000, "ac": 1, "initiative": -501},
		{"name": "Charge Victim", "monster_key": "goblin-warrior", "hp": 100000, "ac": 1, "initiative": -502}}})
	pgExec(t, `UPDATE characters SET hp=100000, max_hp=100000, ac=1 WHERE id=$1`, charID)
	pgExec(t, `UPDATE companions SET hp=100000, max_hp=100000, ac=1 WHERE id=$1`, petID)
	id := func(name string) int { return monsterIDsByKeyOrName(camp, name)[0] }
	boar, seahorse, victim := id("Charge Boar"), id("Charge Seahorse"), id("Charge Victim")

	putMap := func(attacker, target, attackerX int, forced bool) {
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"........"}, Tokens: []mapToken{
			{ID: attacker, Name: "attacker", Position: mapSquare{attackerX, 0}, Size: 1}, {ID: target, Name: "target", Position: mapSquare{5, 0}, Size: 1}}},
			"forced_movement": forced}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	prone := func(target int) bool {
		for _, c := range creatureConditions(camp, target) {
			if strings.HasPrefix(strings.ToLower(c), "prone") {
				return true
			}
		}
		return false
	}
	// attack until a hit (AC 1: only a 1 misses); before each try the move is made again (a miss spends the charge)
	attack := func(monster, action, targetName string, setup func(), extra map[string]interface{}) string {
		t.Helper()
		for i := 0; i < 20; i++ {
			setup()
			ma := map[string]interface{}{"monster": monster, "action": action, "target": targetName}
			for k, v := range extra {
				ma[k] = v
			}
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": ma})
			res := fmt.Sprint(r["monster_action_result"])
			if strings.Contains(res, "HIT") {
				return res
			}
		}
		t.Fatalf("%s never hit", action)
		return ""
	}
	twoAmounts := func(res string) bool { return strings.Count(strings.ToLower(res), "piercing damage") >= 2 } // the hit and the charge's extra
	moved := func(attacker, target, startX int, forced bool) func() {
		return func() {
			putMap(attacker, target, startX, true) // placed (forced: not a move)
			putMap(attacker, target, 4, forced)    // then it moves next to the target
		}
	}
	for _, tg := range []struct {
		id   int
		name string
	}{{charID, fmt.Sprint(charID)}, {victim, "Charge Victim"}} {
		endCondition(camp, tg.id, "prone", "")
		res := attack("Charge Boar", "Gore", tg.name, moved(boar, tg.id, 0, false), nil)
		if !twoAmounts(res) || !prone(tg.id) {
			t.Errorf("%s: a 20-foot charge gave no extra damage or Prone: %s", tg.name, res)
		}
		endCondition(camp, tg.id, "prone", "")
		res = attack("Charge Boar", "Gore", tg.name, moved(boar, tg.id, 2, false), nil)
		if twoAmounts(res) || prone(tg.id) || strings.Contains(res, "moved 20+") {
			t.Errorf("%s: a 10-foot move charged: %s", tg.name, res)
		}
		res = attack("Charge Boar", "Gore", tg.name, moved(boar, tg.id, 0, true), nil)
		if twoAmounts(res) || prone(tg.id) {
			t.Errorf("%s: a forced move charged: %s", tg.name, res)
		}
	}
	// the companion route: the Seahorse's alternative amount is judged (it used to be refused there)
	res := attack("Charge Seahorse", "Ram", "React Pet", moved(seahorse, pet, 0, false), nil)
	if strings.Contains(res, "cannot judge") || strings.Contains(res, "Nothing was rolled") {
		t.Errorf("the companion route refused a charge's alternative amount: %s", res)
	}

	// without a battle map: the GM says it, or is told
	pgExec(t, `UPDATE combat_state SET battle_map = NULL WHERE lobby_id = $1`, camp)
	endCondition(camp, charID, "prone", "")
	res = attack("Charge Boar", "Gore", fmt.Sprint(charID), func() {}, map[string]interface{}{"charged": true})
	if !twoAmounts(res) || !prone(charID) {
		t.Errorf("no map, charged: true: %s", res)
	}
	endCondition(camp, charID, "prone", "")
	res = attack("Charge Boar", "Gore", fmt.Sprint(charID), func() {}, nil)
	if twoAmounts(res) || prone(charID) || !strings.Contains(res, `"charged": true`) {
		t.Errorf("no map, nothing said: %s", res)
	}
}

// A hit that pushes its target (the Satyr's Hooves) on every target route: on the map the target's token moves 10 feet straight
// away; a target too big stays; without a map the GM is told.
func TestPostgresHitPushOnEveryTarget(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Push Satyr", "monster_key": "satyr", "hp": 1000, "ac": 1, "initiative": -500},
		{"name": "Push Victim", "monster_key": "goblin-warrior", "hp": 100000, "ac": 1, "initiative": -502},
		{"name": "Push Ogre", "monster_key": "ogre", "hp": 100000, "ac": 1, "initiative": -503}}})
	pgExec(t, `UPDATE characters SET hp=100000, max_hp=100000, ac=1 WHERE id=$1`, charID)
	pgExec(t, `UPDATE companions SET hp=100000, max_hp=100000, ac=1 WHERE id=$1`, petID)
	id := func(name string) int { return monsterIDsByKeyOrName(camp, name)[0] }
	satyr := id("Push Satyr")
	putMap := func(target, size int) {
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"..........", "..........", ".........."}, Tokens: []mapToken{
			{ID: satyr, Name: "satyr", Position: mapSquare{0, 0}, Size: 1}, {ID: target, Name: "target", Position: mapSquare{1, 0}, Size: size}}},
			"forced_movement": true}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	at := func(target int) mapSquare {
		m, _ := activeEncounterMap(camp)
		tk, _ := m.token(target)
		return tk.Position
	}
	hit := func(targetName string, setup func()) string {
		t.Helper()
		for i := 0; i < 20; i++ {
			setup()
			_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": "Push Satyr", "action": "Hooves", "target": targetName}})
			if res := fmt.Sprint(r["monster_action_result"]); strings.Contains(res, "HIT") {
				return res
			}
		}
		t.Fatal("the Hooves never hit")
		return ""
	}
	for _, tg := range []struct {
		id   int
		name string
	}{{charID, fmt.Sprint(charID)}, {id("Push Victim"), "Push Victim"}, {pet, "React Pet"}} {
		res := hit(tg.name, func() { putMap(tg.id, 1) })
		if p := at(tg.id); p != (mapSquare{3, 0}) || !strings.Contains(res, "pushed 10 feet") || strings.Contains(res, "pushes the target") {
			t.Errorf("%s: pushed to %v: %s", tg.name, p, res)
		}
	}
	ogre := id("Push Ogre") // Large: the push is for a Medium or smaller target
	res := hit("Push Ogre", func() { putMap(ogre, 2) })
	if p := at(ogre); p != (mapSquare{1, 0}) || strings.Contains(res, "pushed") {
		t.Errorf("a Large target was pushed to %v: %s", p, res)
	}
	pgExec(t, `UPDATE combat_state SET battle_map = NULL WHERE lobby_id = $1`, camp)
	if res := hit(fmt.Sprint(charID), func() {}); !strings.Contains(res, "no battle map: the GM places it") {
		t.Errorf("no map: %s", res)
	}
}

// An entry's own move through narrate and the GM's map update: the Blink Dog's Teleport opens no Opportunity Attack where its walk
// away does; the Gnoll Warrior's Rampage is refused until its last hit damaged a creature that was already Bloodied, then gives
// its half-Speed move.
func TestPostgresEntryMovesAndRampageGate(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, _, _ := companionReactionFixture(t)
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Move Dog", "monster_key": "blink-dog", "hp": 1000, "ac": 1, "initiative": -500},
		{"name": "Move Gnoll", "monster_key": "gnoll-warrior", "hp": 1000, "ac": 1, "initiative": -501}}})
	pgExec(t, `UPDATE characters SET hp=5000, max_hp=5000, ac=1, reaction_used=false WHERE id=$1`, charID)
	id := func(name string) int { return monsterIDsByKeyOrName(camp, name)[0] }
	dog, gnoll := id("Move Dog"), id("Move Gnoll")
	putMap := func(mover, moverX int, forced bool) {
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"............"}, Tokens: []mapToken{
			{ID: charID, Name: "hero", Position: mapSquare{0, 0}, Size: 1}, {ID: mover, Name: "mover", Position: mapSquare{moverX, 0}, Size: 1}}},
			"forced_movement": forced}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	oaNotes := func() (n int) {
		db.QueryRow(`SELECT COUNT(*) FROM actions WHERE lobby_id = $1 AND action_type = 'opportunity_attack_available'`, camp).Scan(&n)
		return
	}
	narrate := func(monster, action, target string) (int, map[string]interface{}) {
		return pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": monster, "action": action, "target": target}})
	}
	// control: the dog walks away from the hero: an Opportunity Attack opens
	putMap(dog, 1, true)
	before := oaNotes()
	putMap(dog, 9, false)
	if oaNotes() != before+1 {
		t.Fatalf("control: walking away opened %d Opportunity Attacks", oaNotes()-before)
	}
	// the Teleport: the same move opens none
	pgExec(t, `UPDATE characters SET reaction_used=false WHERE id=$1`, charID)
	putMap(dog, 1, true)
	c, r := narrate("Move Dog", "Teleport", "")
	if c != 200 || !strings.Contains(fmt.Sprint(r["monster_action_result"]), "Movement (Teleport): up to 40 ft, teleporting") {
		t.Fatalf("teleport: %d %v", c, r)
	}
	before = oaNotes()
	putMap(dog, 9, false)
	if oaNotes() != before {
		t.Errorf("the Teleport opened an Opportunity Attack")
	}

	// Rampage: refused after a hit on a healthy hero, allowed after a hit on a Bloodied one
	putMap(gnoll, 1, true)
	rend := func() {
		t.Helper()
		for i := 0; i < 20; i++ {
			if _, r := narrate("Move Gnoll", "Rend", fmt.Sprint(charID)); strings.Contains(fmt.Sprint(r["monster_action_result"]), "HIT") {
				return
			}
		}
		t.Fatal("Rend never hit")
	}
	rend()
	if c, r := narrate("Move Gnoll", "Rampage", fmt.Sprint(charID)); c == 200 || !strings.Contains(fmt.Sprint(r["message"]), "already Bloodied") {
		t.Errorf("Rampage after a hit on a healthy hero: %d %v", c, r)
	}
	pgExec(t, `UPDATE characters SET hp=2000 WHERE id=$1`, charID)
	rend()
	c, r = narrate("Move Gnoll", "Rampage", fmt.Sprint(charID))
	if res := fmt.Sprint(r["monster_action_result"]); c != 200 || !strings.Contains(res, "Movement (Rampage): up to 15 ft") {
		t.Errorf("Rampage after a hit on a Bloodied hero: %d %v", c, r)
	}
}

// A Swarm shares spaces both ways and an incorporeal creature stands inside objects: the GM may place a Swarm of Rats in a
// character's space, a character's path may end in the swarm's space (another monster's still refuses it), a Specter may stand in
// a wall and takes its trait's Force damage when its turn ends there.
func TestPostgresSwarmAndIncorporealSpaces(t *testing.T) {
	testPostgres(t)
	gm, player, camp, charID, _, _ := companionReactionFixture(t)
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Space Rats", "monster_key": "swarm-of-rats", "hp": 1000, "ac": 1, "initiative": -500},
		{"name": "Space Goblin", "monster_key": "goblin-warrior", "hp": 1000, "ac": 1, "initiative": -501},
		{"name": "Space Specter", "monster_key": "specter", "hp": 1000, "ac": 1, "initiative": -502}}})
	pgExec(t, `UPDATE characters SET movement_remaining=30 WHERE id=$1`, charID)
	id := func(name string) int { return monsterIDsByKeyOrName(camp, name)[0] }
	rats, goblin, specter := id("Space Rats"), id("Space Goblin"), id("Space Specter")
	put := func(tokens []mapToken) (int, map[string]interface{}) {
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"....#....."}, Tokens: tokens}, "forced_movement": true}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		return pgCall(t, "PUT", cp+"/battle-map", gm, req)
	}
	// the GM places the swarm in the character's space; a goblin there is refused
	if c, r := put([]mapToken{{ID: charID, Name: "hero", Position: mapSquare{1, 0}, Size: 1}, {ID: rats, Name: "rats", Position: mapSquare{1, 0}, Size: 1}}); c != 200 {
		t.Errorf("a swarm in a character's space: %d %v", c, r)
	}
	if c, _ := put([]mapToken{{ID: charID, Name: "hero", Position: mapSquare{1, 0}, Size: 1}, {ID: goblin, Name: "goblin", Position: mapSquare{1, 0}, Size: 1}}); c == 200 {
		t.Error("a goblin was placed in a character's space")
	}
	// the character's path ends in the swarm's space; the goblin's space still refuses it
	if c, r := put([]mapToken{{ID: charID, Name: "hero", Position: mapSquare{0, 0}, Size: 1}, {ID: rats, Name: "rats", Position: mapSquare{2, 0}, Size: 1},
		{ID: goblin, Name: "goblin", Position: mapSquare{6, 0}, Size: 1}}); c != 200 {
		t.Fatalf("put: %d %v", c, r)
	}
	f, _ := readFight(db, camp)
	pgExec(t, `UPDATE combat_state SET current_turn_index=$1 WHERE lobby_id=$2`, idxOfID(f, charID), camp)
	move := func(path []mapSquare) (int, map[string]interface{}) {
		body := map[string]interface{}{"character_id": charID, "path": path}
		_, pv := pgCall(t, "POST", cp+"/battle-map/preview", player, body)
		if conf, _ := pv["confirmation"].(string); conf != "" {
			body["confirmation"] = conf
		} else {
			return 400, pv
		}
		return pgCall(t, "POST", cp+"/battle-map/move", player, body)
	}
	if c, r := move([]mapSquare{{1, 0}, {2, 0}}); c != 200 {
		t.Errorf("a path ending in the swarm's space: %d %v", c, r)
	}
	pgExec(t, `UPDATE characters SET movement_remaining=30 WHERE id=$1`, charID)
	if c, _ := move([]mapSquare{{3, 0}, {4, 0}, {5, 0}, {6, 0}}); c == 200 {
		t.Error("a path ended in the goblin's space")
	}

	// the Specter stands in the wall (x=4) and takes its Force damage when its turn ends there
	if c, r := put([]mapToken{{ID: charID, Name: "hero", Position: mapSquare{0, 0}, Size: 1}, {ID: specter, Name: "specter", Position: mapSquare{4, 0}, Size: 1}}); c != 200 {
		t.Fatalf("an incorporeal creature in a wall: %d %v", c, r)
	}
	if c, _ := put([]mapToken{{ID: charID, Name: "hero", Position: mapSquare{0, 0}, Size: 1}, {ID: goblin, Name: "goblin", Position: mapSquare{4, 0}, Size: 1}}); c == 200 {
		t.Error("a goblin was placed in a wall")
	}
	put([]mapToken{{ID: charID, Name: "hero", Position: mapSquare{0, 0}, Size: 1}, {ID: specter, Name: "specter", Position: mapSquare{4, 0}, Size: 1}})
	f, _ = readFight(db, camp)
	pgExec(t, `UPDATE combat_state SET current_turn_index=$1, turn_claim='' WHERE lobby_id=$2`, idxOfID(f, specter), camp)
	before := monsterHPNow(camp, specter)
	if c, r := pgCall(t, "POST", cp+"/combat/next", gm, map[string]interface{}{}); c != 200 {
		t.Fatalf("next: %d %v", c, r)
	}
	if after := monsterHPNow(camp, specter); after >= before {
		t.Errorf("the Specter ended its turn in a wall and took nothing: %d -> %d", before, after)
	}
}

// Shape-Shift, a fall and a drag through the GM's requests: the Quasit's bat form makes its Speed 10 ft (a 20-foot move is over it);
// an entry that makes its creature fall deals the falling damage, less what it reduces, from its token's height, and lands it; a
// monster dragging a character it grapples pays double, a Bugbear (Abduct) doesn't.
func TestPostgresShapeShiftFallAndDrag(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, _, _ := companionReactionFixture(t)
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	lurker := compendiumFixture()
	lurker.Name, lurker.Type, lurker.Traits, lurker.Legendary, lurker.LegendaryIn, lurker.DmgImmune, lurker.Resistances = "Test Lurker", "Monstrosity", nil, nil, "", "", ""
	lurker.Actions = []srdMonsterEntry{{Name: "Drop", Text: "The lurker falls. Dexterity Saving Throw: DC 11, one creature directly underneath the lurker. Failure: 10 (3d6) Piercing damage. Failure or Success: The lurker reduces any damage it takes from the fall by 20."}}
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented fall fixture", "monsters": []srdMonster2024{lurker}})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	key := fmt.Sprint(imported["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Form Quasit", "monster_key": "quasit", "hp": 1000, "ac": 1, "initiative": -500},
		{"name": "Fall Lurker", "monster_key": key, "hp": 1000, "ac": 1, "initiative": -501},
		{"name": "Drag Goblin", "monster_key": "goblin-warrior", "hp": 1000, "ac": 1, "initiative": -502},
		{"name": "Drag Bugbear", "monster_key": "bugbear-warrior", "hp": 1000, "ac": 1, "initiative": -503}}})
	id := func(name string) int { return monsterIDsByKeyOrName(camp, name)[0] }
	quasit, fall, goblin, bugbear := id("Form Quasit"), id("Fall Lurker"), id("Drag Goblin"), id("Drag Bugbear")
	put := func(tokens []mapToken, forced bool) {
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"............", "............"}, Tokens: tokens}, "forced_movement": forced}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	turnOf := func(id int) {
		f, _ := readFight(db, camp)
		pgExec(t, `UPDATE combat_state SET current_turn_index=$1, turn_started_at=NOW() WHERE lobby_id=$2`, idxOfID(f, id), camp)
	}
	lastMoveNote := func() (s string) {
		db.QueryRow(`SELECT COALESCE(result,'') FROM actions WHERE lobby_id = $1 AND action_type = 'monster_move' ORDER BY id DESC LIMIT 1`, camp).Scan(&s)
		return
	}
	narrate := func(monster, action, use string) (int, map[string]interface{}) {
		ma := map[string]interface{}{"monster": monster, "action": action}
		if use != "" {
			ma["use"] = use
		}
		return pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": ma})
	}
	// Shape-Shift: refused without a form, then the bat's Speed 10 ft makes a 20-foot move over it
	if c, r := narrate("Form Quasit", "Shape-Shift", ""); c == 200 || !strings.Contains(fmt.Sprint(r["message"]), "bat") {
		t.Errorf("Shape-Shift with no form: %d %v", c, r)
	}
	if c, r := narrate("Form Quasit", "Shape-Shift", "bat"); c != 200 || !strings.Contains(fmt.Sprint(r["monster_action_result"]), "its bat form") || !strings.Contains(fmt.Sprint(r["monster_action_result"]), "Fly 40 ft") {
		t.Fatalf("Shape-Shift into a bat: %d %v", c, r)
	}
	turnOf(quasit)
	put([]mapToken{{ID: quasit, Name: "quasit", Position: mapSquare{0, 0}, Size: 1}}, true)
	put([]mapToken{{ID: quasit, Name: "quasit", Position: mapSquare{4, 0}, Size: 1}}, false)
	if note := lastMoveNote(); !strings.Contains(note, "its Speed is 10 ft") {
		t.Errorf("the bat's Speed: %q", note)
	}
	narrate("Form Quasit", "Shape-Shift", "true form")
	if s, _ := monsterWalkSpeed(quasit, camp); s != 40 {
		t.Errorf("its true form's Speed: %d", s)
	}

	// a fall from 30 feet: 3d6 less 20, and it lands
	put([]mapToken{{ID: fall, Name: "lurker", Position: mapSquare{1, 0}, Size: 1, Altitude: 30}, {ID: charID, Name: "hero", Position: mapSquare{1, 1}, Size: 1}}, true)
	c, r := narrate("Fall Lurker", "Drop", "")
	if res := fmt.Sprint(r["monster_action_result"]); c != 200 || !strings.Contains(res, "falls 30 ft") || strings.Contains(res, "reduces any damage it takes from the fall") {
		t.Errorf("the fall (applied, not left to the GM): %d %v", c, r)
	}
	if m, _ := activeEncounterMap(camp); func() int { tk, _ := m.token(fall); return tk.Altitude }() != 0 {
		t.Error("the lurker didn't land")
	}

	// the drag: the goblin pays double, the bugbear (Abduct) doesn't
	for _, dragger := range []struct {
		id     int
		name   string
		double bool
	}{{goblin, "goblin", true}, {bugbear, "bugbear", false}} {
		removeConditionsWhere(camp, charID, func(r conditionRecord) bool { return r.Condition == "grappled" })
		addCondition(camp, charID, condAdd{Condition: "grappled", Source: grappleSource(dragger.id), SourceID: dragger.id})
		turnOf(dragger.id)
		put([]mapToken{{ID: dragger.id, Name: dragger.name, Position: mapSquare{0, 0}, Size: 1}, {ID: charID, Name: "hero", Position: mapSquare{1, 0}, Size: 1}}, true)
		pgExec(t, `DELETE FROM actions WHERE lobby_id = $1 AND action_type = 'monster_move'`, camp)
		put([]mapToken{{ID: dragger.id, Name: dragger.name, Position: mapSquare{5, 0}, Size: 1}, {ID: charID, Name: "hero", Position: mapSquare{6, 0}, Size: 1}}, false)
		if note := lastMoveNote(); strings.Contains(note, "drags") != dragger.double {
			t.Errorf("%s drags (double %v): %q", dragger.name, dragger.double, note)
		}
	}
}
