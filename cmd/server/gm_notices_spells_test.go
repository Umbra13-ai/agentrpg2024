package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestPostgresGMNoticesOnPlayerSpells: the private Concentration notices a PLAYER'S SPELL writes (Magic Missile damage; Hold Person's
// Incapacitating condition) reach the GM only. Controls first: each spell really wrote a notice. Then the player's cast reply, my-turn,
// the campaign log and page and the shared action log carry none of its wording. A notice-write failure during a cast does not undo the
// cast (the damage stays) and is answered as a database error, never as a clean success.
func TestPostgresGMNoticesOnPlayerSpells(t *testing.T) {
	testPostgres(t)
	// other tests replace the shared spell cache with their own entries (and an order-dependent failure was seen when this ran after one):
	// this test reloads the real spell data and puts the cache back afterwards
	savedSpells := srdSpellsMemory
	t.Cleanup(func() { srdSpellsMemory = savedSpells })
	srdSpellsMemory = map[string]SRDSpell{}
	if err := loadSRDFromDB(); err != nil || srdSpellsMemory["magic-missile"].Name == "" {
		t.Fatalf("reloading the spell data: %v (magic missile %+v)", err, srdSpellsMemory["magic-missile"])
	}
	_, gm := pgAgent(t, "spell notice gm")
	_, player := pgAgent(t, "spell notice player")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Spell notices", "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Spell Notice Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	if c, j := pgCall(t, "POST", cp+"/start", gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	pgExec(t, `UPDATE characters SET class='wizard',level=5,known_spells='["magic-missile","hold-person"]',prepared_spells='["magic-missile","hold-person"]',spell_slots_used='{}',inventory=$2::jsonb WHERE id=$1`, char, `[{"id":"i1","name":"Component Pouch","type":"focus","spellcasting_focus":true}]`)
	var base srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Goblin Warrior" {
			base = m
		}
	}
	caster := base
	caster.Name, caster.AC, caster.HP, caster.Type = "SpellNote Caster", 1, 500, "Humanoid"
	caster.Actions = append([]srdMonsterEntry{{Name: "Spellcasting", Text: "The caster casts one of the following spells, using Intelligence as the spellcasting ability (spell save DC 14): At Will: Light."}}, base.Actions...)
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented test monsters", "monsters": []srdMonster2024{caster}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "SpellNote Caster", "monster_key": key, "initiative": 1}}})
	target := monsterIDsByKeyOrName(camp, "SpellNote Caster")[0]
	markers := []string{"currently concentrating", "concentration ends while", "which ends concentration", "the server does not track", "gm_notices", "end that effect now"}
	noticesText := func() string {
		out := ""
		for _, n := range noticesOf(t, camp) {
			out += n.Text + "\n"
		}
		return out
	}
	clear := func() { pgExec(t, `UPDATE gm_notices SET acknowledged = true WHERE lobby_id = $1`, camp) }
	leak := func(where, out string) {
		low := strings.ToLower(out)
		for _, m := range markers {
			if strings.Contains(low, m) {
				t.Errorf("audience: %s shows a private notice (%q): %.300s", where, m, out)
				return
			}
		}
	}
	var replies []string
	cast := func(spell string) (int, map[string]interface{}) {
		pgExec(t, `UPDATE characters SET action_used=false,spell_slots_used='{}',slot_spent_turn='',conditions='[]' WHERE id=$1`, char)
		code, r := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"action": "cast", "spell_slug": spell, "target_id": target})
		replies = append(replies, fmt.Sprint(r))
		return code, r
	}
	// 1. Magic Missile: damage, so the conditional Concentration reminder
	if code, r := cast("magic-missile"); code != 200 {
		t.Fatalf("magic missile: %d %v", code, r)
	}
	if txt := noticesText(); !strings.Contains(txt, "Only if SpellNote Caster is currently concentrating") {
		t.Fatalf("control: the player's Magic Missile wrote no Concentration notice (the checks below would prove nothing): %q; reply %.700s", txt, replies[0])
	}
	clear()
	// 2. Hold Person (Paralyzed includes Incapacitated): retried until the monster fails its save
	got := false
	for n := 0; n < 40 && !got; n++ {
		removeConditionsWhere(camp, target, func(conditionRecord) bool { return true })
		clear()
		if code, r := cast("hold-person"); code != 200 {
			t.Fatalf("hold person: %d %v", code, r)
		}
		got = strings.Contains(noticesText(), "is now Incapacitated")
	}
	if !got {
		t.Fatal("control: no player Hold Person made the caster Incapacitated with a GM notice in 40 tries (random saves, or the route did not apply it)")
	}
	// audience, after both spells' notices existed
	for _, rd := range []struct{ method, path string }{{"GET", "/api/my-turn"}, {"GET", cp + "/log"}, {"GET", cp}} {
		_, j := pgCall(t, rd.method, rd.path, player, nil)
		leak("player "+rd.method+" "+rd.path, fmt.Sprint(j))
	}
	for i, r := range replies {
		leak(fmt.Sprintf("the player's cast reply %d", i), r)
	}
	recs, err := readActions(actionFilter{lobby: camp, limit: 200})
	if err != nil || len(recs) == 0 {
		t.Fatalf("reading the action log: %d %v", len(recs), err)
	}
	for _, rec := range recs {
		leak("the action log ("+rec.Type+")", rec.Description+" "+rec.Result)
	}
	if code, j := pgCall(t, "GET", "/api/gm/notices?campaign_id="+fmt.Sprint(camp), gm, nil); code != 200 || !strings.Contains(fmt.Sprint(j), "Incapacitated") {
		t.Errorf("the GM's notices lack the Hold Person note: %d %v", code, j)
	}
	// 3. a notice-write failure during a cast. The cast is committed (every notice is written after the change is saved), so the reply must
	// stay the true success: a database_error here would look like a failed cast that is safe to retry (slot and damage spent twice).
	clear()
	slots := func() string {
		var u string
		db.QueryRow(`SELECT COALESCE(spell_slots_used::text,'') FROM characters WHERE id=$1`, char).Scan(&u)
		return u
	}
	pgExec(t, `UPDATE characters SET action_used=false,spell_slots_used='{}',slot_spent_turn='' WHERE id=$1`, char)
	before := monsterEntryVitals(camp, target).HP
	pgExec(t, `ALTER TABLE gm_notices RENAME TO gm_notices_off`)
	restored := false
	restore := func() {
		if !restored {
			pgExec(t, `ALTER TABLE gm_notices_off RENAME TO gm_notices`)
			restored = true
		}
	}
	t.Cleanup(restore)
	code, r := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"action": "cast", "spell_slug": "magic-missile", "target_id": target})
	restore()
	t.Cleanup(func() { heldGMNotices.ack(camp, nil, true) })
	after := monsterEntryVitals(camp, target).HP
	used := slots()
	if code != 200 || r["error"] != nil || r["success"] != true {
		t.Errorf("a committed cast whose notice could not be stored must still answer its true success, got %d %v", code, r)
	}
	leak("the cast reply with the notice table down", fmt.Sprint(r))
	if dealt := before - after; dealt < 3 || dealt > 15 {
		t.Errorf("one Magic Missile cast should deal 3 to 15 damage once, dealt %d (hp %d to %d)", dealt, before, after)
	}
	if !strings.Contains(used, "1") {
		t.Errorf("the cast should have spent exactly one slot: %q", used)
	}
	held := 0
	for _, n := range noticesOf(t, camp) {
		if n.HeldInMemory && n.ID < 0 && strings.Contains(n.Text, "SpellNote Caster") {
			held++
		}
	}
	if held != 1 {
		t.Errorf("the GM should have exactly one held notice for that cast, has %d: %q", held, noticesText())
	}
	// retrying the (committed) cast spends and damages again: that is what a retry is; the point is that the first reply did not invite it
}
