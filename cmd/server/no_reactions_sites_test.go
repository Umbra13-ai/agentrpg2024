package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The one-line "can't take Reactions" checks in the GM Reaction routes, each driven through its HTTP route with the no_reactions marker
// on the character: the route answers that the character can't take Reactions and does not mark the Reaction used. A control call
// without the marker must not give that answer, so a refusal for another reason cannot pass for this check.
func TestPostgresCantTakeReactionsGMRoutes(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "noreact sites gm")
	_, player := pgAgent(t, "noreact sites player")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "No reactions sites", "max_players": 2})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("NoReact Sites %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	pgExec(t, `UPDATE characters SET level=5,hp=100,max_hp=100,reaction_used=false WHERE id=$1`, char)
	used := func() bool {
		var u bool
		db.QueryRow(`SELECT COALESCE(reaction_used,false) FROM characters WHERE id=$1`, char).Scan(&u)
		return u
	}
	ready := func() {
		pgExec(t, `UPDATE characters SET readied_action='{"trigger":"the door opens","action":"attack"}' WHERE id=$1`, char)
	}
	refused := func(r map[string]interface{}) bool {
		return r["error"] == "no_reaction" || r["error"] == "reaction_used"
	}
	routes := []struct {
		path, class, sub string
		body             map[string]interface{}
	}{
		{"/api/gm/counterspell", "wizard", "", map[string]interface{}{"campaign_id": camp, "caster_id": char, "target_id": char}},
		{"/api/gm/cutting-words", "bard", "lore", map[string]interface{}{"campaign_id": camp, "bard_id": char, "enemy_roll": 15, "roll_type": "attack"}},
		{"/api/gm/deflect-attacks", "monk", "", map[string]interface{}{"campaign_id": camp, "character_id": char, "damage": 10, "damage_type": "piercing"}},
		{"/api/gm/superior-hunters-defense", "ranger", "hunter", map[string]interface{}{"campaign_id": camp, "character_id": char, "damage": 10, "damage_type": "fire"}},
		{"/api/gm/falling-damage", "monk", "", map[string]interface{}{"campaign_id": camp, "character_id": char, "distance_feet": 100, "use_slow_fall": true}},
		{"/api/gm/trigger-readied", "rogue", "", map[string]interface{}{"campaign_id": camp, "character_id": char}},
		{"/api/gm/retaliation", "barbarian", "berserker", map[string]interface{}{"campaign_id": camp, "character_id": char, "attacker_name": "Goblin"}},
		{"/api/gm/opportunity-attack", "rogue", "", map[string]interface{}{"campaign_id": camp, "attacker_id": char, "target_id": char}},
		{"/api/gm/uncanny-dodge", "rogue", "", map[string]interface{}{"campaign_id": camp, "character_id": char, "damage": 10, "attacker_name": "x"}},
	}
	for _, rt := range routes {
		removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
		pgExec(t, `UPDATE characters SET reaction_used=false,class=$2,subclass=$3,level=15,cha=20 WHERE id=$1`, char, rt.class, rt.sub)
		ready()
		_, ctl := pgCall(t, "POST", rt.path, gm, rt.body)
		if rt.path == "/api/gm/cutting-words" && (ctl["bardic_inspiration_remaining"] == nil || !used()) {
			t.Fatalf("cutting-words control did not succeed and spend the Reaction: %v", ctl)
		}
		if refused(ctl) {
			t.Fatalf("%s: the control call (no marker) gave the marker's answer: %v", rt.path, ctl)
		}
		removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
		pgExec(t, `UPDATE characters SET reaction_used=false WHERE id=$1`, char)
		ready()
		if err := setMarker(char, "no_reactions", "test"); err != nil {
			t.Fatalf("marker: %v", err)
		}
		code, r := pgCall(t, "POST", rt.path, gm, rt.body)
		got := strings.ToLower(fmt.Sprint(r))
		t.Logf("%s -> %d %v", rt.path, code, r)
		if code == 200 || !refused(r) || !strings.Contains(got, "reaction") {
			t.Fatalf("%s let a Reaction through under the marker (%d): %v", rt.path, code, r)
		}
		if used() {
			t.Fatalf("%s marked the Reaction used although it was refused", rt.path)
		}
	}
}

// The item Reactions (legendary_more_2024.go, reactionSpent): Scarab of Protection refuses while "can't take Reactions" stands, and spends
// neither the Reaction nor a charge. HELPER-LEVEL (scarabPreserves is called directly; the save routes that reach it are not driven here).
func TestPostgresCantTakeReactionsItemReaction(t *testing.T) {
	_, _, camp, char, _, _ := protectionFixture(t)
	pgExec(t, `UPDATE characters SET inventory='[{"name":"Scarab of Protection","charges":5,"charges_max":12}]', attuned_items='["Scarab of Protection"]', reaction_used=false WHERE id=$1`, char)
	removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
	used := func() bool {
		var u bool
		db.QueryRow(`SELECT COALESCE(reaction_used,false) FROM characters WHERE id=$1`, char).Scan(&u)
		return u
	}
	if note := scarabPreserves(char, true, false); note == "" || !used() {
		t.Fatalf("control: the scarab did not work without the marker (note %q, reaction used %v)", note, used())
	}
	pgExec(t, `UPDATE characters SET inventory='[{"name":"Scarab of Protection","charges":5,"charges_max":12}]', reaction_used=false WHERE id=$1`, char)
	if err := setMarker(char, "no_reactions", "test"); err != nil {
		t.Fatal(err)
	}
	if note := scarabPreserves(char, true, false); note != "" || used() {
		t.Fatalf("the scarab worked under can't-take-Reactions (note %q, reaction used %v)", note, used())
	}
	if cur, _, _ := itemCharges(carriedItem(char, "scarab of protection")); cur != 5 {
		t.Fatalf("a refused scarab spent a charge: %d left", cur)
	}
}

// Staff of the Magi Spell Absorption refuses under can't-take-Reactions and keeps its charges. HELPER-LEVEL (staffAbsorbs called directly).
func TestPostgresCantTakeReactionsStaffAbsorption(t *testing.T) {
	_, _, camp, char, _, _ := protectionFixture(t)
	setup := func() {
		pgExec(t, `UPDATE characters SET inventory='[{"name":"Staff of the Magi","charges":10,"charges_max":50}]', attuned_items='["Staff of the Magi"]', equipped_main_hand='Staff of the Magi', reaction_used=false WHERE id=$1`, char)
	}
	removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
	setup()
	if ok, note := staffAbsorbs(camp, char, 3); !ok {
		t.Fatalf("control: the staff did not absorb without the marker (%q)", note)
	}
	setup()
	if err := setMarker(char, "no_reactions", "test"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := staffAbsorbs(camp, char, 3); ok {
		t.Fatal("the staff absorbed under can't-take-Reactions")
	}
	if cur, _, _ := itemCharges(carriedItem(char, "staff of the magi")); cur != 10 {
		t.Fatalf("a refused staff changed its charges: %d", cur)
	}
}

// Ring of Spell Turning's Reaction (turnSpellBack): it spends the wearer's Reaction and hits the caster, and it refuses under
// can't-take-Reactions without spending anything or touching the caster. HELPER-LEVEL (turnSpellBack is called directly; the spell-save route that
// reaches it is not driven here). The save the caster rolls is random, so the test checks the Reaction and the output text, not the damage.
func TestPostgresCantTakeReactionsRingSpellTurning(t *testing.T) {
	_, _, camp, char, _, _ := protectionFixture(t)
	caster := monsterTargetByName(camp, "Prot Attacker")
	if caster >= 0 {
		t.Fatalf("setup: the caster did not resolve to a monster id (%d)", caster)
	}
	effect := saveEffect{Ability: "dex", DC: 30, Damage: []damagePart{{Dice: "1d6", Type: "fire"}}, HalfOnSuccess: true, IsSpell: true, SpellLevel: 3, SingleTarget: true, SourceID: caster}
	rolled := map[int]int{0: 4}
	used := func() bool {
		var u bool
		db.QueryRow(`SELECT reaction_used FROM characters WHERE id=$1`, char).Scan(&u)
		return u
	}
	removeConditionsWhere(camp, char, func(conditionRecord) bool { return true })
	pgExec(t, `UPDATE characters SET reaction_used=false WHERE id=$1`, char)
	if out := turnSpellBack(camp, char, "test", effect, rolled); out == "" || !used() || !strings.Contains(out, "turns the spell back") {
		t.Fatalf("control: the ring did not turn the spell back (out %q, reaction used %v)", out, used())
	}
	pgExec(t, `UPDATE characters SET reaction_used=false WHERE id=$1`, char)
	if err := setMarker(char, "no_reactions", "test"); err != nil {
		t.Fatal(err)
	}
	if out := turnSpellBack(camp, char, "test", effect, rolled); out != "" || used() {
		t.Fatalf("the ring turned a spell under can't-take-Reactions (out %q, reaction used %v)", out, used())
	}
}
