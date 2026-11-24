package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

// Preserve Life through POST /api/action: the reply names each creature healed with its amount, before and after, and the amount the
// half-maximum limit cut, from the GM route's own results.
func TestPostgresPreserveLifeReplySaysWhoWasHealed(t *testing.T) {
	testPostgres(t)
	_, toks, camp, chars, _ := pgFightWith(t, "preserve", 3)
	tok, cleric, dying, hurt := toks[0], chars[0], chars[1], chars[2]
	life := ""
	for s, sc := range game.AvailableSubclasses {
		if sc.Name == "Life Domain" {
			life = s
		}
	}
	pgExec(t, `UPDATE characters SET class='Cleric', level=3, subclass=$2, class_resources_used='{}'::jsonb, action_used=false WHERE id=$1`, cleric, life)
	pgExec(t, `UPDATE characters SET hp=0, max_hp=31 WHERE id=$1`, dying)
	pgExec(t, `UPDATE characters SET hp=10, max_hp=24 WHERE id=$1`, hurt)
	f, _ := readFight(db, camp)
	pgExec(t, `UPDATE combat_state SET current_turn_index=$1, turn_claim='' WHERE lobby_id=$2`, idxOfID(f, cleric), camp)
	letters := func(n int64) string { // names as players write them: words, no digits
		out := ""
		for i := 0; i < 6; i++ {
			out += string(rune('a' + n%26))
			n /= 26
		}
		return out
	}
	stamp := time.Now().UnixNano()
	dyingName, hurtName := "Aldwin "+letters(stamp), "Mirabel "+letters(stamp/7)
	pgExec(t, `UPDATE characters SET name=$2 WHERE id=$1`, dying, dyingName)
	pgExec(t, `UPDATE characters SET name=$2 WHERE id=$1`, hurt, hurtName)
	c, j := pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "preserve_life", "description": fmt.Sprintf("%s 8, %s 7", dyingName, hurtName)})
	res := fmt.Sprint(j["result"])
	if c != 200 || !strings.Contains(res, dyingName+" healed for 8 HP: 0 → 8 HP") || !strings.Contains(res, hurtName+" healed for 2 HP (capped at half max 12): 10 → 12 HP") ||
		!strings.Contains(res, "10 of 15 Hit Points restored") {
		t.Errorf("the reply: %d %q", c, res)
	}
}
