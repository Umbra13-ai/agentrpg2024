package main

import (
	"fmt"
	"strings"
	"testing"
)

// A monster's trait that acts on its death (Death Burst, monster_death_trigger_2024.go), through the routes: the GM's damage kills
// it (POST /api/gm/adjust-monster); after that request its saving throw effect has been rolled for every creature in its Emanation on
// the battle map (a character, a companion and another monster, whose own burst it sets off), not for one outside it; the shared log
// and the GM's notices say so; nothing stays pending. A monster without the trait records nothing. Without its token on the map
// nothing is rolled and the GM is told. A trigger left pending (a crash after the death's commit) is resolved at the next turn
// start (POST combat/next). The texts are invented with the SRD 5.2.1 Death Burst pattern.
func TestPostgresDeathBurst(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	burst := func(name string) srdMonster2024 {
		m := compendiumFixture()
		m.Name, m.Type, m.Legendary, m.LegendaryIn = name, "Elemental", nil, ""
		m.Traits = []srdMonsterEntry{{Name: "Death Burst", Text: "The imp explodes when it dies. Dexterity Saving Throw: DC 40, each creature in a 10-foot Emanation originating from the imp. Failure: 7 (2d6) Fire damage. Success: Half damage."}}
		return m
	}
	plain := compendiumFixture()
	plain.Name, plain.Type, plain.Traits, plain.Legendary, plain.LegendaryIn, plain.DmgImmune, plain.Resistances = "Burst Bystander", "Beast", nil, nil, "", "", ""
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented death-burst fixture",
		"monsters": []srdMonster2024{burst("Burst Imp"), burst("Chain Imp"), burst("Lone Imp"), plain}})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	keys := map[string]string{}
	for _, m := range imported["monsters"].([]interface{}) {
		mm := m.(map[string]interface{})
		keys[fmt.Sprint(mm["name"])] = fmt.Sprint(mm["monster_key"])
		t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, fmt.Sprint(mm["monster_key"])) })
	}
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	var add []map[string]interface{}
	for _, c := range []struct {
		name string
		hp   int
	}{{"Burst Imp", 50}, {"Chain Imp", 1}, {"Lone Imp", 50}, {"Burst Bystander", 1000}, {"Far Bystander", 1000}} {
		key := keys[c.name]
		if key == "" {
			key = keys["Burst Bystander"]
		}
		add = append(add, map[string]interface{}{"name": c.name, "monster_key": key, "hp": c.hp, "ac": 1, "initiative": -300})
	}
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": add})
	id := func(name string) int { return monsterIDsByKeyOrName(camp, name)[0] }
	pgExec(t, `UPDATE companions SET hp=1000,max_hp=1000 WHERE id=$1`, petID)
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000 WHERE id=$1`, charID)
	// the imp at (0,0); within 10 feet: the character (5 ft), the companion (10 ft), the chain imp (5 ft) and a bystander (10 ft);
	// a bystander at 30 ft; the lone imp has no token
	_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
	req := map[string]interface{}{"map": encounterMap{Grid: []string{"........", "........"}, Tokens: []mapToken{
		{ID: id("Burst Imp"), Name: "Burst Imp", Position: mapSquare{0, 0}, Size: 1},
		{ID: charID, Name: "hero", Position: mapSquare{1, 0}, Size: 1},
		{ID: pet, Name: "React Pet", Position: mapSquare{2, 0}, Size: 1},
		{ID: id("Chain Imp"), Name: "Chain Imp", Position: mapSquare{0, 1}, Size: 1},
		{ID: id("Burst Bystander"), Name: "Burst Bystander", Position: mapSquare{2, 1}, Size: 1},
		{ID: id("Far Bystander"), Name: "Far Bystander", Position: mapSquare{6, 0}, Size: 1}}}, "forced_movement": true}
	if rev, ok := state["revision"].(string); ok {
		req["confirmation"] = rev
	}
	if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
		t.Fatalf("put map: %d %v", c, r)
	}
	charHP := func() (h int) { db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, charID).Scan(&h); return }
	petHP := func() (h int) { db.QueryRow(`SELECT hp FROM companions WHERE id=$1`, petID).Scan(&h); return }
	monHP := func(name string) int {
		f, _ := readFight(db, camp)
		_, e := f.entry(id(name))
		return asInt(e["hp"])
	}
	pending := func() (n int) {
		db.QueryRow(`SELECT COUNT(*) FROM pending_death_triggers WHERE lobby_id=$1`, camp).Scan(&n)
		return
	}
	kill := func(name string) {
		if c, r := pgCall(t, "POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target": name, "hp": 0}); c != 200 {
			t.Fatalf("adjust-monster %s: %d %v", name, c, r)
		}
	}
	notice := func(s string) bool {
		for _, n := range noticesOf(t, camp) {
			if strings.Contains(n.Text, s) {
				return true
			}
		}
		return false
	}

	// control: a monster without the trait records nothing
	kill("Far Bystander")
	if pending() != 0 || notice("Far Bystander dies") {
		t.Fatalf("a monster without a death trigger recorded one")
	}
	pgCall(t, "POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target": "Far Bystander", "hp": 1000})

	c0, p0, b0 := charHP(), petHP(), monHP("Burst Bystander")
	// the killing request's own reply says what its death set off: both bursts of the chain, the GM's whole lines
	c, r := pgCall(t, "POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target": "Burst Imp", "hp": 0})
	if c != 200 {
		t.Fatalf("adjust-monster Burst Imp: %d %v", c, r)
	}
	if after := fmt.Sprint(r["after_this_request"]); !strings.Contains(after, "Burst Imp dies: its Death Burst acts") || !strings.Contains(after, "Chain Imp dies: its Death Burst acts") {
		t.Errorf("the killing reply does not say what the bursts did: after_this_request = %v", r["after_this_request"])
	}
	if pending() != 0 {
		t.Fatalf("triggers left pending after the request: %d", pending())
	}
	if charHP() >= c0 || petHP() >= p0 || monHP("Burst Bystander") >= b0 {
		t.Errorf("the burst did not damage every creature in its Emanation: hero %d->%d, companion %d->%d, bystander %d->%d", c0, charHP(), p0, petHP(), b0, monHP("Burst Bystander"))
	}
	if monHP("Far Bystander") != 1000 {
		t.Errorf("a creature 30 feet away took the 10-foot burst: %d", monHP("Far Bystander"))
	}
	if monHP("Chain Imp") != 0 || !notice("Chain Imp dies: its Death Burst acts") {
		t.Errorf("the burst killed the chain imp without setting off its own: hp %d", monHP("Chain Imp"))
	}
	if !notice("Burst Imp dies: its Death Burst acts") {
		t.Errorf("the GM's notices do not report the burst")
	}
	var logged int
	db.QueryRow(`SELECT COUNT(*) FROM actions WHERE lobby_id=$1 AND action_type='monster_trait' AND description ILIKE '%Death Burst%'`, camp).Scan(&logged)
	if logged < 2 {
		t.Errorf("the shared log has %d Death Burst records, want the two bursts", logged)
	}

	// without a token on the map: nothing rolled, the GM is told
	c1 := charHP()
	kill("Lone Imp")
	if charHP() != c1 || pending() != 0 || !notice("Lone Imp dies: its Death Burst acts. Nothing was rolled") {
		t.Errorf("a burst without its token rolled or was not given to the GM: hero %d->%d", c1, charHP())
	}

	// the backstop: a trigger the request did not resolve (a crash after the death's commit) at the next turn start
	pgCall(t, "POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target": "Burst Imp", "hp": 50})
	pgExec(t, `INSERT INTO pending_death_triggers (lobby_id, monster_id, name, trait_name, trait_text) VALUES ($1,$2,'Burst Imp','Death Burst',$3)`,
		camp, id("Burst Imp"), burst("x").Traits[0].Text)
	c2 := charHP()
	if c, r := pgCall(t, "POST", cp+"/combat/next", gm, map[string]interface{}{}); c != 200 {
		t.Fatalf("combat/next: %d %v", c, r)
	}
	if pending() != 0 || charHP() >= c2 {
		t.Errorf("the pending trigger was not resolved at the turn start: pending %d, hero %d->%d", pending(), c2, charHP())
	}
}

// The import report and the stat block say what the server does with a trait it applies, in the GM's words, not only the code's names.
func TestTraitReportSaysWhatTheServerDoes(t *testing.T) {
	trait := srdMonsterEntry{Name: "Death Burst", Text: "The imp explodes when it dies. Dexterity Saving Throw: DC 11, each creature in a 10-foot Emanation originating from the imp. Failure: 7 (2d6) Fire damage. Success: Half damage."}
	rule, ok := traitRuleFor(trait)
	if !ok {
		t.Fatal("no trait rule reads a death burst")
	}
	applied, _ := entryReport("trait", trait)["applied"].([]map[string]interface{})
	if len(applied) == 0 {
		t.Fatal("nothing applied")
	}
	for _, a := range applied {
		if a["server_does"] != rule.Does {
			t.Errorf("%q: server_does %v, want the rule's %q", a["sentence"], a["server_does"], rule.Does)
		}
	}
}

// TestPostgresDeathTraitsOutsideTheSRD (Darkling death traits): a trait that acts "when the <creature> dies" is set off by the death; its
// 2024-form saving throw is rolled on the battle map (Blinded until the end of the target's next turn), and words the server can't read go
// to the GM at the moment of the death.
func TestPostgresDeathTraitsOutsideTheSRD(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, _, _ := companionReactionFixture(t)
	mk := func(name, text string) srdMonster2024 {
		m := compendiumFixture()
		m.Name, m.Type, m.Legendary, m.LegendaryIn = name, "Fey", nil, ""
		m.Traits = []srdMonsterEntry{{Name: "Death Flash", Text: text}}
		return m
	}
	readable := mk("Test Glimmer", "When the test glimmer dies, light flashes from it. Constitution Saving Throw: DC 40, each creature in a 10-foot Emanation originating from the glimmer. Failure: The target has the Blinded condition until the end of its next turn.")
	unread := mk("Test Old Glimmer", "When the test glimmer dies, light flashes from it, and each creature that sees it must win a contest of wills or be dazzled.")
	if _, ok := deathTriggerOf(readable.Traits); !ok {
		t.Fatal("a 'when the <creature> dies' trait is not a death trigger")
	}
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented death-trait fixture",
		"monsters": []srdMonster2024{readable, unread}})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	keys := map[string]string{}
	for _, m := range imported["monsters"].([]interface{}) {
		mm := m.(map[string]interface{})
		keys[fmt.Sprint(mm["name"])] = fmt.Sprint(mm["monster_key"])
		t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, fmt.Sprint(mm["monster_key"])) })
	}
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Test Glimmer", "monster_key": keys["Test Glimmer"], "hp": 5, "ac": 1, "initiative": -300},
		{"name": "Test Old Glimmer", "monster_key": keys["Test Old Glimmer"], "hp": 5, "ac": 1, "initiative": -301}}})
	glimmer := monsterIDsByKeyOrName(camp, "Test Glimmer")[0]
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,con=1 WHERE id=$1`, charID)
	_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
	req := map[string]interface{}{"map": encounterMap{Grid: []string{"......"}, Tokens: []mapToken{
		{ID: glimmer, Name: "Test Glimmer", Position: mapSquare{0, 0}, Size: 1}, {ID: charID, Name: "hero", Position: mapSquare{1, 0}, Size: 1}}}, "forced_movement": true}
	if rev, ok := state["revision"].(string); ok {
		req["confirmation"] = rev
	}
	if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
		t.Fatalf("put map: %d %v", c, r)
	}
	for _, name := range []string{"Test Glimmer", "Test Old Glimmer"} {
		if c, r := pgCall(t, "POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target": name, "hp": 0}); c != 200 {
			t.Fatalf("adjust-monster %s: %d %v", name, c, r)
		}
	}
	drainDeathTriggers() // the request resolved them already (withProtections); any left are resolved now
	var lines string
	recs, _ := readActions(actionFilter{lobby: camp, limit: 50})
	for _, a := range recs {
		lines += a.Description + " " + a.Result + "\n"
	}
	if rs := recordsOf(camp, charID, "blinded"); len(rs) != 1 {
		t.Errorf("the readable death flash didn't blind the hero within 10 feet: %v\n%s", rs, lines)
	}
	if !strings.Contains(lines, "Test Old Glimmer dies: its Death Flash acts. Nothing was rolled") { // its words are the GM's notice (the log keeps them from players)
		t.Errorf("the unread death trait wasn't handed to the GM at the death:\n%s", lines)
	}
}
