package main

// The GM's Witchlight fix list (23 Nov 2026): a monster named by its combat id whose action is unknown is refused for the action, not
// the monster; a lasting effect the GM is handed by the adjudication fallback is an open item (B2); an attached max_hp_drain and
// attacked_with_disadvantage are applied by their owners (B2, B3). Every monster here is invented.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestPostgresGMDocFixes(t *testing.T) {
	testPostgres(t)
	swarm := srdMonsterEntry{Name: "Gnat Cloud", Text: "A cloud of gnats fills a 20-foot Sphere for 1 minute; each creature that starts its turn there takes 3 (1d6) Piercing damage."}
	sap := srdMonsterEntry{Name: "Sapping Claw", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 1 (1d1) Slashing damage plus 4 (1d8) Necrotic damage. The wound saps the target's vitality until it rests.",
		Mechanics: []entryMechanic{{"kind": "max_hp_drain", "damage_type": "necrotic", "covers": "The wound saps the target's vitality until it rests."}}}
	gm, _, camp, hero, fiend := earlyRulesFixture(t, "Test Doc Fiend", []srdMonsterEntry{swarm, sap})
	// an unknown action of a monster named by its combat id: the action is refused, with the monster's actions
	c, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
		"monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", fiend), "action": "Fireball", "target": fmt.Sprint(hero)}})
	if b, _ := json.Marshal(j); c != 400 || !strings.Contains(string(b), "No action of that name") || !strings.Contains(string(b), "Gnat Cloud") {
		t.Errorf("an unknown action by combat id: %d %s", c, b)
	}
	// the adjudication fallback hands a lasting area to the GM: an open item
	c, j = pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
		"monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", fiend), "action": "Gnat Cloud", "target_ids": []int{hero}, "confirm_targeting": true}})
	_, items := pgCall(t, "GET", fmt.Sprintf("/api/gm/open-items?campaign_id=%d", camp), gm, nil)
	if b, _ := json.Marshal(items); c != 200 || !strings.Contains(string(b), "Gnat Cloud") {
		t.Errorf("the lasting area handed to the GM is no open item: %d %v / %s", c, j["monster_action_result"], b)
	}
	// max_hp_drain: the Necrotic damage taken comes off the Hit Point maximum
	var before, after int
	db.QueryRow(`SELECT max_hp FROM characters WHERE id=$1`, hero).Scan(&before)
	_, line := narrateHit(t, gm, camp, fiend, "Sapping Claw", fmt.Sprint(hero), "")
	db.QueryRow(`SELECT max_hp FROM characters WHERE id=$1`, hero).Scan(&after)
	if after >= before || after < before-16 || strings.Contains(line, "saps the target") { // 1d8 Necrotic, 2d8 on a critical
		t.Errorf("max_hp_drain: maximum %d -> %d: %s", before, after, line)
	}
}

func TestAttackedWithDisadvantageMechanic(t *testing.T) {
	cases := []struct{ mechanics, want string }{
		{`[{"kind":"attacked_with_disadvantage"}]`, ""},
		{`[{"kind":"attacked_with_disadvantage","unless_incapacitated":false}]`, ""},
		{`[{"kind":"attacked_with_disadvantage","unless_incapacitated":"no"}]`, "unless_incapacitated must be true or false"},
		{`[{"kind":"max_hp_drain","damage_type":"necrotic"}]`, "attaches to an entry with an attack roll"},
	}
	for _, c := range cases {
		got := mechanicProblem("trait", srdMonsterEntry{Name: "Blur", Text: "The sprite moves in a blur.", Mechanics: mechanicsFromJSON(t, c.mechanics)})
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.mechanics, got, c.want)
		}
	}
}

func TestPostgresAttackedWithDisadvantageMechanic(t *testing.T) {
	testPostgres(t)
	gm, player, camp, hero, sprite := earlyRulesFixture(t, "Test Blur Sprite", []srdMonsterEntry{{Name: "Prick", Text: "Melee Attack Roll: +4, reach 5 ft. Hit: 1 (1d1) Piercing damage."}})
	_ = gm
	_ = player
	entry, _, _ := monsterCombatant(camp, sprite)
	pgExec(t, `UPDATE custom_monsters SET definition = jsonb_set(definition, '{traits}', $2::jsonb) WHERE key=$1`, fmt.Sprint(entry["monster_key"]),
		`[{"name": "Blurred Dash", "text": "The sprite moves in a blur.", "mechanics": [{"kind": "attacked_with_disadvantage", "covers": "The sprite moves in a blur."}]}]`)
	if got := attackedWithDisadvantage(camp, sprite, nil); got != "Blurred Dash" {
		t.Fatalf("attacks against it: %q", got)
	}
	if got := attackedWithDisadvantage(camp, sprite, []string{"incapacitated"}); got != "" {
		t.Errorf("Incapacitated: %q", got)
	}
	c := attackCtx{lobby: camp, attacker: hero, target: sprite}
	if roll := c.gather(); !anyContains(roll.Disadvantage, "Blurred Dash") {
		t.Errorf("a character's attack roll against it: %v", roll.Disadvantage)
	}
}
