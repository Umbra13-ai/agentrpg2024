package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Magical Darkness (the Darkmantle's Darkness Aura (1/Day)): the action establishes a token-anchored
// emanation on the monster's Concentration, Darkvision can't penetrate it, no light brightens it, and
// the 1/Day use is spent. The emanation ends with the Concentration.
func TestPostgresDarknessAura(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "darkness gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Darkness aura", "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	_, player := pgAgent(t, "darkness player")
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Darkness Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	_, player2 := pgAgent(t, "darkness player 2")
	_, j2 := pgCall(t, "POST", "/api/characters", player2, pgRogue(fmt.Sprintf("Darkness Scout %d", time.Now().UnixNano()%1e9)))
	char2 := int(j2["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player2, map[string]interface{}{"character_id": char2})
	pgFinish(t, player2, char2)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", player2, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var darkmantle srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Darkmantle" {
			darkmantle = m
		}
	}
	if !magicalDarknessRe.MatchString("") {
		found := false
		for _, a := range darkmantle.Actions {
			if magicalDarknessRe.MatchString(a.Text) {
				found = true
			}
		}
		if !found { // the positive guard: a catalog that lost the clause FAILS the test loudly
			t.Fatalf("the SRD Darkmantle carries no Magical Darkness clause: %+v", darkmantle.Actions)
		}
	}
	m := darkmantle
	m.Name = "Darkness Lurker"
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Darkmantle variant for the darkness test", "monsters": []srdMonster2024{m}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Darkness Lurker", "monster_key": key, "initiative": 1}}})
	ids := monsterIDsByKeyOrName(camp, "Darkness Lurker")
	if len(ids) != 1 {
		t.Fatalf("monster id: %v", ids)
	}
	id := ids[0]

	// The map: the lurker and the hero stand 10 feet apart.
	lurkerToken := mapToken{ID: id, Name: "Darkness Lurker", Position: mapSquare{1, 1}, Size: 1}
	heroToken := mapToken{ID: char, Name: "Darkness Hero", Position: mapSquare{3, 1}, Size: 1}
	scoutToken := mapToken{ID: char2, Name: "Darkness Scout", Position: mapSquare{11, 1}, Size: 1}
	putMap := func(tokens ...mapToken) {
		t.Helper()
		_, st := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"............", "............", "............"}, Tokens: tokens}}
		if rev, ok := st["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	putMap(lurkerToken, heroToken, scoutToken)
	if magicalDarknessAt(camp, char) {
		t.Fatal("no emanation before the action")
	}

	act := func(action string) (string, int) {
		code, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{
			"narration":      "The lurker acts.",
			"monster_action": map[string]interface{}{"monster": "Darkness Lurker", "action": action},
		})
		return fmt.Sprint(j["monster_action_result"]), code
	}
	res, code := act("Darkness Aura")
	if code != 200 || !strings.Contains(res, "Magical Darkness fills a 15-foot Emanation") {
		t.Fatalf("the action: %d %s", code, res)
	}
	if !strings.Contains(res, "Concentration") {
		t.Fatalf("the reply should name the Concentration: %s", res)
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind='magical_darkness' AND source_id=$2`, camp, id).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("the emanation row: %d %v", rows, err)
	}
	if spell := monsterConcentrationOn(camp, id); !strings.Contains(spell, "Darkness Aura") {
		t.Fatalf("the aura rides the lurker's Concentration, found %q", spell)
	}

	// 1/Day: the second use is refused.
	res, _ = act("Darkness Aura")
	if !strings.Contains(strings.ToLower(res), "day") || strings.Contains(res, "fills a 15-foot Emanation around Darkness Lurker. Darkvision") {
		t.Fatalf("the second use should be refused by the 1/Day limit: %s", res)
	}

	// The hero's square is inside: the hero can't see the lurker (Darkvision or not piercing), and
	// the attack carries the Disadvantage from either endpoint. The origin's own square is NOT in
	// its Emanation (SRD 5.2.1 "Emanation") - the hero is blind to the lurker because the HERO
	// stands inside, not because the lurker's square reads as darkness.
	if !magicalDarknessAt(camp, char) {
		t.Fatal("the hero stands in the emanation")
	}
	if magicalDarknessAt(camp, id) {
		t.Fatal("the origin's own square is not in its Emanation")
	}
	if !magicalDarknessBlocksSight(camp, char, id) {
		t.Fatal("the hero inside the emanation can't see the lurker")
	}
	roll := (&attackCtx{lobby: camp, attacker: char, target: id}).gather()
	blind := false
	for _, d := range roll.Disadvantage {
		if strings.Contains(d, "can't see") {
			blind = true
		}
	}
	if !blind {
		t.Fatalf("the attack should carry the unseen-target Disadvantage: %+v", roll.Disadvantage)
	}

	// The lurker's own Blindsight pierces its darkness: no endpoint penalty on its attack. And the
	// shared unseen model gives it the other leg: the blinded hero can't see it, so it attacks with
	// Advantage (SRD 5.2.1 "Blinded": attack rolls against you have Advantage).
	if !piercesMagicalDarkness(camp, id) {
		t.Fatal("the darkmantle's Blindsight pierces its own darkness")
	}
	roll = (&attackCtx{lobby: camp, attacker: id, target: char}).gather()
	for _, d := range roll.Disadvantage {
		if strings.Contains(d, "can't see") {
			t.Fatalf("the lurker sees through its own darkness: %+v", roll.Disadvantage)
		}
	}
	unseenAdv := false
	for _, a := range roll.Advantage {
		if strings.Contains(a, "can't see the attacker") {
			unseenAdv = true
		}
	}
	if !unseenAdv {
		t.Fatalf("the blinded hero can't see the lurker, so the lurker attacks with Advantage: %+v", roll.Advantage)
	}

	// Sense ranges: a short Blindsight does not pierce beyond its reach - Blindsight 5 leaves the
	// lurker (10 feet away) unseen; Blindsight 60 pierces (SRD 5.2.1 "Blindsight": within that range).
	if _, err := db.Exec(`UPDATE characters SET blindsight_range=5 WHERE id=$1`, char); err != nil {
		t.Fatal(err)
	}
	if !magicalDarknessBlocksSight(camp, char, id) {
		t.Fatal("Blindsight 5 should not pierce the darkness at 10 feet")
	}
	if _, err := db.Exec(`UPDATE characters SET blindsight_range=60 WHERE id=$1`, char); err != nil {
		t.Fatal(err)
	}
	if magicalDarknessBlocksSight(camp, char, id) {
		t.Fatal("Blindsight 60 should pierce the darkness at 10 feet")
	}
	if _, err := db.Exec(`UPDATE characters SET blindsight_range=0 WHERE id=$1`, char); err != nil {
		t.Fatal(err)
	}

	// No light illuminates the area: a Bright Light trait at the same square does not brighten it.
	// (monsterLightingAt returns before the light loop when the target stands in magical darkness.)
	if got := monsterLightingAt(camp, char); got != "magical darkness" {
		t.Fatalf("the area itself reads as magical darkness: %q", got)
	}

	// Ordinary darkness, the same shared model both ways: with the scene dark, the lurker's
	// Blindsight 60 sees the scout (no Disadvantage) while the scout can't see the lurker (the
	// unseen-attacker Advantage); the scout's own attack carries only the Disadvantage.
	if code, j := pgCall(t, "POST", "/api/gm/set-lighting", gm, map[string]interface{}{"campaign_id": camp, "lighting": "darkness"}); code != 200 {
		t.Fatalf("set ambient lighting: %d %v", code, j)
	}
	defer pgCall(t, "POST", "/api/gm/set-lighting", gm, map[string]interface{}{"campaign_id": camp, "lighting": "bright"})
	// The scout sees nothing in it: strip the Darkvision its sheet came with.
	if _, err := db.Exec(`UPDATE characters SET darkvision_range=0 WHERE id=$1`, char2); err != nil {
		t.Fatal(err)
	}
	if !creatureSeesInLighting(camp, id, "darkness", 20) {
		t.Fatal("the lurker's Blindsight 60 makes out ordinary darkness at 20 feet")
	}
	if creatureSeesInLighting(camp, char2, "darkness", 20) {
		t.Fatal("the scout has no sense that makes out ordinary darkness")
	}
	roll = (&attackCtx{lobby: camp, attacker: id, target: char2}).gather()
	for _, d := range roll.Disadvantage {
		if strings.Contains(d, "can't see") {
			t.Fatalf("the lurker sees in ordinary darkness: %+v", roll.Disadvantage)
		}
	}
	unseenAdv = false
	for _, a := range roll.Advantage {
		if strings.Contains(a, "can't see the attacker") {
			unseenAdv = true
		}
	}
	if !unseenAdv {
		t.Fatalf("the scout can't see the lurker in ordinary darkness: %+v", roll.Advantage)
	}
	roll = (&attackCtx{lobby: camp, attacker: char2, target: id}).gather()
	blind = false
	for _, d := range roll.Disadvantage {
		if strings.Contains(d, "can't see") {
			blind = true
		}
	}
	if !blind {
		t.Fatalf("the scout attacks blind in ordinary darkness: %+v", roll.Disadvantage)
	}
	if code, j := pgCall(t, "POST", "/api/gm/set-lighting", gm, map[string]interface{}{"campaign_id": camp, "lighting": "bright"}); code != 200 {
		t.Fatalf("restore lighting: %d %v", code, j)
	}

	// Sight lines crossing the emanation with both endpoints outside: the hero and the scout on
	// opposite sides of the aura can't see each other - the attack carries BOTH the unseen-target
	// Disadvantage and the unseen-attacker Advantage, which cancel to a straight roll (the shared
	// model; endpoint-only Disadvantage is not the rule).
	lurkerToken.Position = mapSquare{5, 1}
	heroToken.Position = mapSquare{1, 1}
	scoutToken.Position = mapSquare{9, 1}
	putMap(lurkerToken, heroToken, scoutToken)
	if !magicalDarknessBlocksSight(camp, char, char2) || !magicalDarknessBlocksSight(camp, char2, char) {
		t.Fatal("the aura between the two heroes blocks the line both ways")
	}
	roll = (&attackCtx{lobby: camp, attacker: char, target: char2}).gather()
	unseenAdv, blind = false, false
	for _, a := range roll.Advantage {
		if strings.Contains(a, "can't see the attacker") {
			unseenAdv = true
		}
	}
	for _, d := range roll.Disadvantage {
		if strings.Contains(d, "can't see") {
			blind = true
		}
	}
	if !unseenAdv || !blind {
		t.Fatalf("mutual unseen should carry both legs: adv %+v dis %+v", roll.Advantage, roll.Disadvantage)
	}
	if mode := roll.Mode(); mode != "cancelled" {
		t.Fatalf("the two legs cancel to a straight roll, got %q", mode)
	}

	// The area is an Emanation: it moves with its origin (SRD 5.2.1 "Emanation").
	lurkerToken.Position = mapSquare{8, 1}
	putMap(lurkerToken, heroToken, scoutToken)
	if magicalDarknessAt(camp, char) {
		t.Fatal("the hero's square is clear once the aura moves away")
	}
	if !magicalDarknessAt(camp, char2) {
		t.Fatal("the scout now stands in the moved aura")
	}

	// The ten-minute ceiling: aged past 100 rounds, the lazy sweep ends the emanation.
	if _, err := db.Exec(`UPDATE combat_effects SET text=$1 WHERE lobby_id=$2 AND kind='magical_darkness' AND source_id=$3`,
		fmt.Sprintf("radius: 15 | round: %d", currentRound(camp)-magicalDarknessRounds), camp, id); err != nil {
		t.Fatal(err)
	}
	if magicalDarknessAt(camp, char2) {
		t.Fatal("the emanation is past its ten minutes")
	}
	if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind='magical_darkness' AND source_id=$2`, camp, id).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("the expired emanation is swept: %d %v", rows, err)
	}

	// Death: the emanation ends when its anchor dies (the lazy sweep is the backstop for it).
	if err := startMagicalDarkness(camp, id, 15, nil); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := db.QueryRow(`SELECT turn_order FROM combat_state WHERE lobby_id=$1`, camp).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	killed := false
	for _, e := range entries {
		if asInt(e["id"]) == id {
			e["hp"] = 0
			killed = true
		}
	}
	if !killed {
		t.Fatal("the lurker is in the turn order")
	}
	if out, err := json.Marshal(entries); err != nil {
		t.Fatal(err)
	} else if _, err := db.Exec(`UPDATE combat_state SET turn_order=$1 WHERE lobby_id=$2`, out, camp); err != nil {
		t.Fatal(err)
	}
	if len(magicalDarknessRows(camp)) != 0 || magicalDarknessAt(camp, char2) {
		t.Fatal("the emanation dies with its anchor")
	}

	// The emanation ends with the Concentration, and the visibility goes with it. (The row was
	// already swept by the death leg above; ending the Concentration stays the primary removal path
	// and must hold with no row left to delete.)
	if !monsterConcentrationEnds(camp, id, "Darkness Lurker", "the test ends it") {
		t.Fatal("the lurker was concentrating")
	}
	if err := db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND kind='magical_darkness' AND source_id=$2`, camp, id).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("the emanation ended with the Concentration: %d %v", rows, err)
	}
	if magicalDarknessAt(camp, char) {
		t.Fatal("the hero sees again once the darkness ends")
	}
}
