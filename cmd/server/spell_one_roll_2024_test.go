package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// Staged test I-ALDEN-PARTY-02: an Evoker's Empowered Evocation (+ Intelligence modifier to ONE damage roll of the spell) reaches every route
// that deals a Wizard Evocation spell's damage: the cast text's total, Magic Missile's darts at a monster and at a character, and
// Scorching Ray's separate beams. It is announced once and the Hit Points lost include it; a caster without the feature gets none.
func TestPostgresEmpoweredEvocationOnEveryRoute(t *testing.T) {
	testPostgres(t)
	_, toks, camp, chars, mons := pgFightWith(t, "evoker", 2)
	tok, char, friend, target := toks[0], chars[0], chars[1], mons[0]
	setCaster := func(sub string) {
		pgExec(t, `UPDATE characters SET class = 'wizard', subclass = NULLIF($2,''), level = 11, intl = 20, hp = max_hp, action_used = false,
			spell_slots_used = '{}'::jsonb, known_spells = '["magic-missile","scorching-ray","fire-bolt"]'::jsonb, prepared_spells = '["magic-missile","scorching-ray"]'::jsonb WHERE id = $1`, char, sub)
	}
	monsterHP := func() int {
		for _, e := range combatEntries(camp) {
			if asInt(e["id"]) == target {
				return asInt(e["hp"])
			}
		}
		return -1
	}
	cast := func(body map[string]interface{}) string {
		t.Helper()
		pgExec(t, `UPDATE characters SET action_used = false, bonus_action_used = false, attacks_remaining = NULL, slot_spent_turn = '', spell_slots_used = '{}'::jsonb WHERE id = $1`, char)
		c, j := pgCall(t, "POST", "/api/action", tok, body)
		if c != 200 {
			t.Fatalf("cast %v: %d %v", body, c, j)
		}
		return fmt.Sprint(j["result"]) + fmt.Sprint(j["message"])
	}
	const note = "Empowered Evocation: +5"
	dart := regexp.MustCompile(`hits: (\d+) force damage( \(Empowered Evocation: \+5\))?`)

	// the Evoker's Magic Missile at a monster: three darts, the bonus on one of them, the Hit Points lost are the lines' sum
	setCaster("evocation")
	before := monsterHP()
	res := cast(map[string]interface{}{"action": "cast", "spell_slug": "magic-missile", "slot_level": 1, "target_id": target})
	sum, withNote := 0, 0
	for _, m := range dart.FindAllStringSubmatch(res, -1) {
		var n int
		fmt.Sscan(m[1], &n)
		sum += n
		if m[2] != "" {
			withNote++
			if n < 2+5 {
				t.Errorf("the dart with the bonus dealt only %d: %q", n, res)
			}
		}
	}
	if got := strings.Count(res, note); got != 1 || withNote != 1 || before-monsterHP() != sum || sum < 3*2+5 {
		t.Errorf("Magic Missile at a monster: %d announcements, %d on a dart, Hit Points lost %d, darts' sum %d (at least 11): %q", got, withNote, before-monsterHP(), sum, res)
	}

	// the same at a character: the dart lines say it once
	res = cast(map[string]interface{}{"action": "cast", "spell_slug": "magic-missile", "slot_level": 1, "target_ids": []int{friend}})
	if strings.Count(res, note) != 1 {
		t.Errorf("Magic Missile at a character announced %d times: %q", strings.Count(res, note), res)
	}

	// Scorching Ray (three beams): once, on a beam that hit, and the Hit Points lost include it
	for try := 0; try < 6; try++ {
		before = monsterHP()
		res = cast(map[string]interface{}{"action": "cast", "spell_slug": "scorching-ray", "slot_level": 2, "target_id": target})
		if strings.Contains(res, "HIT") {
			break
		}
	}
	if strings.Contains(res, "HIT") {
		if got := strings.Count(res, note); got != 1 {
			t.Errorf("Scorching Ray announced the bonus %d times: %q", got, res)
		}
		if before-monsterHP() < 2+5 {
			t.Errorf("Scorching Ray's Hit Points lost %d, less than a beam's minimum and the bonus: %q", before-monsterHP(), res)
		}
	}

	// without the feature: no bonus anywhere (a Wizard of another subclass, and a level 9 Evoker)
	for _, c := range []struct {
		sub   string
		level int
	}{{"abjuration", 11}, {"evocation", 9}} {
		setCaster(c.sub)
		pgExec(t, `UPDATE characters SET level = $2 WHERE id = $1`, char, c.level)
		if res := cast(map[string]interface{}{"action": "cast", "spell_slug": "magic-missile", "slot_level": 1, "target_id": target}); strings.Contains(res, "Empowered Evocation") {
			t.Errorf("%s level %d got Empowered Evocation: %q", c.sub, c.level, res)
		}
	}
}
