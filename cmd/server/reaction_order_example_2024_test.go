package main

// I-CALLA-8 (feedback item 70): a Cleric 2 without Shield was told to set the Shield order. The example of a standing reaction order
// is reactionOrderExample, the first order the character can hold (reactionOrderProblem, which asks the action table's Who for the
// feature orders), read by the turn_left reaction note and the orders' own reply; the Reactions turn_left lists come from the table.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

var classKeys = []string{"barbarian", "bard", "cleric", "druid", "fighter", "monk", "paladin", "ranger", "rogue", "sorcerer", "warlock", "wizard"}

func TestPostgresReactionOrderExampleIsHoldable(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "pg orders gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Orders", "max_players": 2})
	lobby := int(j["campaign_id"].(float64))
	base := fmt.Sprintf("/api/campaigns/%d", lobby)
	pgCall(t, "PATCH", base+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, tok := pgAgent(t, "pg orders player")
	_, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Orders %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", base+"/join", tok, map[string]interface{}{"character_id": char})
	pgFinish(t, tok, char)
	pgCall(t, "POST", base+"/ready", tok, nil)
	pgCall(t, "POST", base+"/ready", gm, nil)
	if c, j := pgCall(t, "POST", base+"/start", gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	order := fmt.Sprintf(`[{"id":%d,"name":"Pg Orders","initiative":20},{"id":-1,"name":"Orders Goblin","initiative":10,"hp":7,"is_monster":true}]`, char)
	pgExec(t, `INSERT INTO combat_state (lobby_id, round_number, current_turn_index, turn_order, turn_started_at, active) VALUES ($1, 1, 0, $2, NOW(), true)
		ON CONFLICT (lobby_id) DO UPDATE SET round_number = 1, current_turn_index = 0, turn_order = EXCLUDED.turn_order, turn_started_at = NOW(), active = true, turn_claim = ''`, lobby, order)

	check := func(class string, level int, prepared, want string) {
		t.Helper()
		pgExec(t, `UPDATE characters SET class = $1, level = $2, subclass = '', prepared_spells = $3, known_spells = '[]', reaction_orders = '{}',
			reaction_used = false, hp = GREATEST(hp, 1) WHERE id = $4`, class, level, prepared, char)
		what := fmt.Sprintf("%s %d", class, level)
		ex := reactionOrderExample(char)
		if ex == "" {
			t.Errorf("%s: no example (every character can hold opportunity_attack)", what)
			return
		}
		var body map[string]interface{}
		if err := json.Unmarshal([]byte(ex[strings.Index(ex, "{"):]), &body); err != nil {
			t.Fatalf("%s: the example isn't a request: %q", what, ex)
		}
		key := fmt.Sprint(body["order"])
		if p := reactionOrderProblem(char, key); p != "" || (want != "" && key != want) {
			t.Errorf("%s: the example sets %q (%s), want %q", what, key, p, want)
		}
		// the orders' reply and the turn_left note say the same example, and the example works as sent
		if _, j := pgCall(t, "GET", fmt.Sprintf("/api/characters/reaction-orders?character_id=%d", char), tok, nil); j["how"] != ex {
			t.Errorf("%s: the orders' reply suggests %v, not %q", what, j["how"], ex)
		}
		rx, _ := turnLeft(char)["reaction"].(map[string]interface{})
		if note := fmt.Sprint(rx["note"]); !strings.Contains(note, ex) {
			t.Errorf("%s: the turn_left note: %q", what, note)
		}
		if c, j := pgCall(t, "POST", "/api/characters/reaction-orders", tok, body); c != 200 {
			t.Errorf("%s: the example refused: %d %v", what, c, j)
		}
		// the Reactions listed are the table's: Uncanny Dodge only for a Rogue 5+, Deflect Attacks only for a Monk 3+
		opts := fmt.Sprint(rx["options"])
		if !strings.Contains(opts, "Opportunity Attack") || strings.Contains(opts, "Uncanny Dodge") != (class == "rogue" && level >= 5) ||
			strings.Contains(opts, "Deflect Attacks") != (class == "monk" && level >= 3) {
			t.Errorf("%s: the Reactions listed: %s", what, opts)
		}
	}
	for _, class := range classKeys {
		for _, level := range []int{1, 3, 5} {
			want := "opportunity_attack"
			switch {
			case class == "rogue" && level >= 5:
				want = "uncanny_dodge"
			case class == "monk" && level >= 3:
				want = "deflect_attacks"
			}
			check(class, level, "[]", want)
		}
	}
	check("cleric", 2, "[]", "opportunity_attack") // the playtest's Cleric 2 without Shield
	check("wizard", 1, `["shield"]`, "shield")

	// a feature order the character lacks is refused with the table's words
	pgExec(t, `UPDATE characters SET class = 'rogue', level = 3 WHERE id = $1`, char)
	c, j := pgCall(t, "POST", "/api/characters/reaction-orders", tok, map[string]interface{}{"character_id": char, "order": "uncanny_dodge", "on": true})
	if c != 400 || !strings.Contains(fmt.Sprint(j["message"]), "Cannot use Uncanny Dodge: it is a Rogue feature from level 5") {
		t.Errorf("Uncanny Dodge for a Rogue 3: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", "/api/characters/reaction-orders", tok, map[string]interface{}{"character_id": char, "order": "parry", "on": true}); c != 400 || !strings.Contains(fmt.Sprint(j["message"]), "shield, uncanny_dodge, deflect_attacks, opportunity_attack") {
		t.Errorf("an unknown order: %d %v", c, j)
	}
}
