package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The functional spell audit's families (Nov 23: FUNC-01, 02, 04, 05, 06, 08, 09, 11, 12), each through the rule's own owner and the route that
// found it. The dice are random, so a test that needs a hit or a miss casts until it has one.

func spellFightFixture(t *testing.T, name string) (tok string, char int, camp int, target int, cast func(slug string, body map[string]interface{}) string) {
	_, toks, camp, chars, mons := pgFightWith(t, name, 1)
	tok, char, target = toks[0], chars[0], mons[0]
	cast = func(slug string, body map[string]interface{}) string {
		t.Helper()
		pgExec(t, `UPDATE characters SET action_used = false, bonus_action_used = false, attacks_remaining = NULL, slot_spent_turn = '', spell_slots_used = '{}'::jsonb, concentrating_on = NULL WHERE id = $1`, char)
		body["action"], body["spell_slug"] = "cast", slug
		c, j := pgCall(t, "POST", "/api/action", tok, body)
		if c != 200 {
			t.Fatalf("cast %s: %d %v", slug, c, j)
		}
		return fmt.Sprint(j["result"]) + fmt.Sprint(j["message"])
	}
	return
}

func monsterHPOf(camp, id int) int {
	for _, e := range combatEntries(camp) {
		if asInt(e["id"]) == id {
			return asInt(e["hp"])
		}
	}
	return -1
}

const focusInventory = `[{"id":"i1","name":"Holy Symbol","type":"focus","spellcasting_focus":true},{"id":"i2","name":"Component Pouch","type":"focus","spellcasting_focus":true}]`

// FUNC-01: Acid Arrow's miss splashes half the initial damage; the data says so (on_miss) and the text of every spell is held to it.
func TestPostgresAcidArrowMissAndHybridSpells(t *testing.T) {
	testPostgres(t)
	for slug, sp := range srdSpellsMemory {
		says := regexp.MustCompile(`(?i)on a miss[^.]*half as much`).MatchString(sp.Description)
		has := len(sp.DamageParts) > 0 && sp.DamageParts[0].OnMiss == "half"
		if says != has {
			t.Errorf("%s: its text says half damage on a miss %v, its data on_miss %v", slug, says, has)
		}
		if isHybridSpell(sp) != (slug == "ice-knife") {
			t.Errorf("%s hybrid: %v (only Ice Knife is an attack with a save after it)", slug, isHybridSpell(sp))
		}
	}
	tok, char, camp, target, cast := spellFightFixture(t, "acidarrow")
	_ = tok
	pgExec(t, `UPDATE characters SET class = 'wizard', subclass = NULL, level = 5, intl = 10, hp = max_hp, known_spells = '["acid-arrow"]'::jsonb, prepared_spells = '["acid-arrow"]'::jsonb,
		inventory = $2::jsonb WHERE id = $1`, char, focusInventory)
	for try := 0; try < 40; try++ {
		before := monsterHPOf(camp, target)
		res := cast("acid-arrow", map[string]interface{}{"slot_level": 2, "target_id": target})
		if strings.Contains(res, "MISS") {
			if !strings.Contains(res, "half damage") || monsterHPOf(camp, target) >= before {
				t.Errorf("a missed Acid Arrow: Hit Points %d to %d: %q", before, monsterHPOf(camp, target), res)
			}
			return
		}
	}
	t.Log("no miss in 40 casts: the miss branch was not exercised")
}

// FUNC-02 and FUNC-05: the damage a cast shows for a part after the first is the damage it deals (rolled once), and Ice Knife makes its spell attack and
// then the explosion's save whether it hit or not.
func TestPostgresPartsAreRolledOnceAndIceKnifeAttacks(t *testing.T) {
	testPostgres(t)
	_, char, camp, target, cast := spellFightFixture(t, "iceparts")
	pgExec(t, `UPDATE characters SET class = 'wizard', subclass = NULL, level = 11, intl = 20, hp = max_hp,
		known_spells = '["ice-storm","ice-knife"]'::jsonb, prepared_spells = '["ice-storm","ice-knife"]'::jsonb, inventory = $2::jsonb WHERE id = $1`, char, focusInventory)
	also := regexp.MustCompile(`Also: (\d+) cold damage`)
	applied := regexp.MustCompile(`takes (\d+) cold damage`)
	checked := 0
	for try := 0; try < 12 && checked < 3; try++ {
		pgExec(t, `UPDATE characters SET spell_slots_used = '{}'::jsonb WHERE id = $1`, char)
		res := cast("ice-storm", map[string]interface{}{"slot_level": 4, "target_id": target})
		a, b := also.FindStringSubmatch(res), applied.FindStringSubmatch(res)
		if a == nil || b == nil {
			continue // the target saved for no cold damage or the text had none
		}
		shown, got := mustInt(a[1]), mustInt(b[1])
		if got != shown && got != shown/2 { // a failed save takes it all, a success half
			t.Errorf("Ice Storm showed %d cold damage and dealt %d: %q", shown, got, res)
		}
		checked++
	}
	if checked == 0 {
		t.Log("no Ice Storm result with both numbers in 12 casts")
	}
	// Ice Knife: its attack roll comes first (the shard), the explosion's save follows hit or miss
	for try := 0; try < 6; try++ {
		res := cast("ice-knife", map[string]interface{}{"slot_level": 1, "target_id": target})
		if !strings.Contains(res, "Spell attack against") {
			t.Fatalf("Ice Knife rolled no spell attack: %q", res)
		}
		if !strings.Contains(res, "Dexterity saving throw") && !strings.Contains(res, "saving throw") {
			t.Fatalf("Ice Knife's explosion had no saving throw: %q", res)
		}
	}
	_ = camp
}

func mustInt(s string) int { n, _ := strconv.Atoi(s); return n }

// FUNC-04: Regenerate heals through the one healing reading: a level 20 Life Cleric's Supreme Healing (maximum dice) and Disciple of Life (2 + slot).
// FUNC-06: Vampiric Touch's caster regains half the Necrotic damage dealt.
func TestPostgresRegenerateAndVampiricTouch(t *testing.T) {
	testPostgres(t)
	_, char, camp, target, cast := spellFightFixture(t, "regenvamp")
	pgExec(t, `UPDATE characters SET class = 'cleric', subclass = 'life', level = 20, wis = 20, hp = 10, max_hp = 200,
		known_spells = '["regenerate"]'::jsonb, prepared_spells = '["regenerate"]'::jsonb, inventory = $2::jsonb WHERE id = $1`, char, focusInventory)
	res := cast("regenerate", map[string]interface{}{"slot_level": 7, "target_id": char})
	if !strings.Contains(res, "regains 56 Hit Points") || !strings.Contains(res, "Supreme Healing + Disciple of Life: +9") {
		t.Errorf("Regenerate for a level 20 Life Cleric with a level 7 slot should heal 32 + 15 + 9 = 56: %q", res)
	}
	var hp int
	db.QueryRow(`SELECT hp FROM characters WHERE id = $1`, char).Scan(&hp)
	if hp < 66 {
		t.Errorf("the Hit Points after Regenerate: %d, want at least 66 (10 + 56)", hp)
	}

	pgExec(t, `UPDATE characters SET class = 'wizard', subclass = NULL, level = 5, intl = 10, hp = 1, max_hp = 200,
		known_spells = '["vampiric-touch"]'::jsonb, prepared_spells = '["vampiric-touch"]'::jsonb, inventory = '[]'::jsonb WHERE id = $1`, char)
	necrotic := regexp.MustCompile(`HIT, (\d+) necrotic damage`)
	for try := 0; try < 30; try++ {
		pgExec(t, `UPDATE characters SET hp = 1 WHERE id = $1`, char)
		res = cast("vampiric-touch", map[string]interface{}{"slot_level": 3, "target_id": target})
		if m := necrotic.FindStringSubmatch(res); m != nil {
			dealt := mustInt(m[1])
			db.QueryRow(`SELECT hp FROM characters WHERE id = $1`, char).Scan(&hp)
			if hp != 1+dealt/2 {
				t.Errorf("Vampiric Touch dealt %d: the caster has %d Hit Points, want %d: %q", dealt, hp, 1+dealt/2, res)
			}
			_ = camp
			return
		}
	}
	t.Log("no Vampiric Touch hit in 30 casts")
}

// FUNC-09: Bestow Curse's cast deals no damage (its 1d8 is the curse's extra damage on later hits).
func TestPostgresBestowCurseDealsNoDamageOnCast(t *testing.T) {
	testPostgres(t)
	_, char, camp, target, cast := spellFightFixture(t, "bestow")
	pgExec(t, `UPDATE characters SET class = 'wizard', subclass = NULL, level = 5, intl = 20, hp = max_hp, known_spells = '["bestow-curse"]'::jsonb, prepared_spells = '["bestow-curse"]'::jsonb, inventory = '[]'::jsonb WHERE id = $1`, char)
	if !castDamageOnLaterHits("bestow-curse") {
		t.Fatal("Bestow Curse's dice are not read as damage on later hits")
	}
	for try := 0; try < 6; try++ {
		before := monsterHPOf(camp, target)
		cast("bestow-curse", map[string]interface{}{"slot_level": 3, "target_id": target, "description": "Cast Bestow Curse choosing disadvantage on Wisdom ability checks and Wisdom saving throws"})
		if after := monsterHPOf(camp, target); after != before {
			t.Fatalf("Bestow Curse changed the target's Hit Points %d to %d", before, after)
		}
	}
}

// FUNC-08: the conditions one cast gave are ended by one repeated saving throw.
func TestPostgresOneRepeatedSaveForOneCast(t *testing.T) {
	testPostgres(t)
	_, _, camp, mons := func() (string, []string, int, []int) {
		_, toks, camp, _, mons := pgFightWith(t, "onesave", 1)
		return "", toks, camp, mons
	}()
	target := mons[0]
	index := -1
	for i, e := range combatEntries(camp) {
		if asInt(e["id"]) == target {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("the monster is not in the turn order")
	}
	give := func(dc int) {
		for _, c := range []string{"prone", "incapacitated"} {
			addCondition(camp, target, condAdd{Condition: c, Source: "Hideous Laughter", SourceID: 99, Rounds: 10, Magical: true, EndSave: &conditionSave{Ability: "wis", DC: dc, When: "end_of_turn"}})
		}
	}
	held := func() int {
		n := 0
		for _, r := range loadConditionRecords(camp, target) {
			if r.Condition == "prone" || r.Condition == "incapacitated" {
				n++
			}
		}
		return n
	}
	// a save that cannot fail: one save, both conditions end
	give(1)
	out := joinLines(endTurnEffects(camp, index))
	if held() != 0 || strings.Count(out, "repeats the") != 1 {
		t.Errorf("a successful repeated save: %d conditions left, %d saves rolled: %q", held(), strings.Count(out, "repeats the"), out)
	}
	// a save that cannot succeed: one save, both continue
	give(100)
	out = joinLines(endTurnEffects(camp, index))
	if held() != 2 || strings.Count(out, "repeats the") != 1 {
		t.Errorf("a failed repeated save: %d conditions left, %d saves rolled: %q", held(), strings.Count(out, "repeats the"), out)
	}
}

// FUNC-11: a Touch spell aimed at a creature of the encounter never buffs the caster.
// FUNC-12: Death Ward negates an instant death that deals no damage, and ends.
func TestPostgresTouchBuffsAndDeathWard(t *testing.T) {
	testPostgres(t)
	_, char, camp, target, cast := spellFightFixture(t, "touchward")
	pgExec(t, `UPDATE characters SET class = 'druid', subclass = NULL, level = 9, wis = 20, hp = max_hp, known_spells = '["barkskin","longstrider","shield-of-faith","darkvision"]'::jsonb,
		prepared_spells = '["barkskin","longstrider","shield-of-faith","darkvision"]'::jsonb, inventory = $2::jsonb WHERE id = $1`, char, focusInventory)
	for _, slug := range []string{"barkskin", "longstrider", "darkvision"} {
		for _, body := range []map[string]interface{}{{"target_id": target}, {"target_ids": []int{target}}} {
			body["slot_level"] = 3
			cast(slug, body)
			var n int
			db.QueryRow(`SELECT COUNT(*) FROM spell_effects WHERE target_id = $1`, char).Scan(&n)
			if n != 0 {
				t.Errorf("%s aimed at an encounter creature put %d effect(s) on the caster", slug, n)
			}
		}
	}
	// the effects creatures that are not characters can hold go to the creature
	cast("shield-of-faith", map[string]interface{}{"slot_level": 1, "target_id": target})
	var onMonster int
	db.QueryRow(`SELECT COUNT(*) FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = 'shield_of_faith'`, camp, target).Scan(&onMonster)
	if onMonster != 1 {
		rows, _ := db.Query(`SELECT kind, COUNT(*) FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 GROUP BY kind`, camp, target)
		for rows != nil && rows.Next() {
			var kind string
			var n int
			rows.Scan(&kind, &n)
			t.Logf("kind %s x%d", kind, n)
		}
		t.Errorf("Shield of Faith on an encounter creature: %d effect(s) on it", onMonster)
	}

	// Death Ward against an instant death
	addSpellEffect(camp, char, char, "Death Ward", "death_ward", "", 4800, "", 0)
	killed, warded, err := killInstantly(char)
	if err != nil || killed || !warded {
		t.Fatalf("with Death Ward: killed %v, warded %v, err %v", killed, warded, err)
	}
	if _, ok := charBuff(char, "death_ward"); ok {
		t.Error("Death Ward was not spent")
	}
	if killed, warded, _ = killInstantly(char); !killed || warded {
		t.Errorf("without Death Ward: killed %v, warded %v", killed, warded)
	}
}

func joinLines(l turnEffectLines) string {
	return strings.Join(append(append([]string{}, l.Public...), l.GM...), " ")
}
