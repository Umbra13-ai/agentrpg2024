package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A hit on a standing monster whose stat block has Spellcasting gives the GM a CONDITIONAL Concentration reminder (only if it is
// concentrating; the server tracks no monster Concentration). Controls: a monster without Spellcasting, a hit that defeats the caster, and zero damage give no note. Route level:
// hurtMonster/damageMonster is the owner of damage routed through it; routes are not each driven, and damage that bypasses it gets no note.
func TestPostgresMonsterConcentrationNote(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "conc gm")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Monster reminders", "max_players": 2})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, player := pgAgent(t, "conc player")
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Conc Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var base srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Goblin Warrior" {
			base = m
		}
	}
	caster, plain := base, base
	caster.Name, plain.Name = "Conc Caster", "Conc Plain"
	caster.Actions = append([]srdMonsterEntry{{Name: "Spellcasting", Text: "The caster casts one of the following spells, using Intelligence as the spellcasting ability (spell save DC 14): At Will: Light. 1/Day: Bless."}}, base.Actions...)
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented test monsters", "monsters": []srdMonster2024{caster, plain}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	keys := map[string]string{}
	for _, m := range j["monsters"].([]interface{}) {
		mm := m.(map[string]interface{})
		keys[fmt.Sprint(mm["name"])] = fmt.Sprint(mm["monster_key"])
	}
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Conc Caster", "monster_key": keys["Conc Caster"], "initiative": 12}, {"name": "Conc Plain", "monster_key": keys["Conc Plain"], "initiative": 10}}})
	f, _ := readFight(db, camp)
	ids := map[string]int{}
	for _, e := range f.Order {
		ids[sprintAny(e["name"])] = asInt(e["id"])
	}
	setHP := func(id, hp int) {
		tx, _ := db.Begin()
		locked, err := lockFight(tx, camp)
		if err != nil {
			t.Fatal(err)
		}
		_, e := locked.entry(id)
		e["hp"], e["max_hp"] = hp, 1000
		if err := writeFight(tx, locked); err != nil || tx.Commit() != nil {
			t.Fatalf("setting hp: %v", err)
		}
	}
	// The reminders are PRIVATE (a player's reply must not say a monster casts or concentrates): they go to the GM's notices. hit returns the
	// reply text and the notices the call wrote, joined, so one assertion reads both; the text itself is checked to carry none.
	drain := func() string {
		var out []string
		for _, n := range noticesOf(t, camp) {
			out = append(out, n.Text)
		}
		pgExec(t, `UPDATE gm_notices SET acknowledged = true WHERE lobby_id = $1`, camp)
		return strings.Join(out, " | ")
	}
	hit := func(name string, dmg, hp int) string {
		drain()
		setHP(ids[name], hp)
		h, ok := hurtMonster(camp, ids[name], dmg, "force", false)
		if !ok {
			t.Fatalf("%s not in the fight", name)
		}
		if strings.Contains(strings.ToLower(h.Text), "concentrat") {
			t.Errorf("the damage text a caller returns carries a Concentration note: %q", h.Text)
		}
		return h.Text + " || " + drain()
	}
	if ids["Conc Caster"] >= 0 || ids["Conc Plain"] >= 0 {
		t.Fatalf("monsters not in the fight: %v", ids)
	}
	for _, c := range []struct{ dmg, dc int }{{8, 10}, {30, 15}, {100, 30}} {
		txt := hit("Conc Caster", c.dmg, 1000)
		if !strings.Contains(txt, "Only if Conc Caster is currently concentrating") || !strings.Contains(txt, "does not mean it is") || !strings.Contains(txt, "ignore this") {
			t.Errorf("the note must be conditional on the monster concentrating: %q", txt)
		}
		if !strings.Contains(txt, fmt.Sprintf("DC %d (", c.dc)) {
			t.Errorf("%d damage: want a Concentration note with DC %d: %q", c.dmg, c.dc, txt)
		}
	}
	// Temporary Hit Points absorbing the damage: still damage taken, DC from the whole damage
	setMonsterTempHP(camp, ids["Conc Caster"], 500)
	if txt := hit("Conc Caster", 30, 1000); !strings.Contains(txt, "DC 15 (") {
		t.Errorf("damage absorbed by Temporary Hit Points: want the note with DC 15 (from the 30 damage): %q", txt)
	}
	setMonsterTempHP(camp, ids["Conc Caster"], 0)
	if txt := hit("Conc Plain", 30, 1000); strings.Contains(txt, "concentrating") {
		t.Errorf("a monster without Spellcasting got a Concentration note: %q", txt)
	}
	// a kill: no save, but the glossary's "or you die" ends Concentration, said once, as a conditional note
	if txt := hit("Conc Caster", 30, 20); strings.Contains(txt, "Constitution saving throw") || strings.Contains(txt, "DC ") || !strings.Contains(txt, "is dead, which ends Concentration") || !strings.Contains(txt, "If it was concentrating") {
		t.Errorf("a killed caster: want the Concentration-ends note and no save: %q", txt)
	}
	if txt := hit("Conc Plain", 30, 20); strings.Contains(txt, "Concentration") {
		t.Errorf("a killed monster without Spellcasting got a Concentration note: %q", txt)
	}
	// the GM's conditions route: a caster that becomes Incapacitated gets the rule named at that moment; a non-caster and a condition
	// that does not incapacitate (Poisoned) get nothing. The note is written by the condition owner, so every caller of addCondition reaches the GM's notices.
	gmCond := func(name, cond string) string {
		drain()
		c, r := pgCall(t, "POST", "/api/gm/conditions", gm, map[string]interface{}{"campaign_id": camp, "target_id": ids[name], "condition": cond, "source": "test"})
		if c != 200 {
			t.Fatalf("gm condition %s on %s: %d %v", cond, name, c, r)
		}
		if n := drain(); n != "" {
			return n
		}
		return "<nil>"
	}
	if n := gmCond("Conc Caster", "stunned"); !strings.Contains(n, "now Incapacitated (stunned)") || !strings.Contains(n, "if it was concentrating") {
		t.Errorf("a stunned caster: want the Concentration-ends note: %q", n)
	}
	if n := gmCond("Conc Plain", "stunned"); n != "<nil>" {
		t.Errorf("a stunned non-caster got a Concentration note: %q", n)
	}
	if n := gmCond("Conc Caster", "poisoned"); n != "<nil>" {
		t.Errorf("Poisoned does not incapacitate, yet: %q", n)
	}
	removeConditionsWhere(camp, ids["Conc Caster"], func(conditionRecord) bool { return true })
	removeConditionsWhere(camp, ids["Conc Plain"], func(conditionRecord) bool { return true })
	// already Incapacitated (Paralyzed): Concentration has ended, so damage asks for no save
	applyCondition(camp, ids["Conc Caster"], "paralyzed", "test", 10, "", 0)
	if txt := hit("Conc Caster", 30, 1000); strings.Contains(txt, "DC ") || strings.Contains(txt, "Constitution saving throw") || !strings.Contains(txt, "Incapacitated") {
		t.Errorf("an Incapacitated caster: want a no-save note: %q", txt)
	}
	removeConditionsWhere(camp, ids["Conc Caster"], func(conditionRecord) bool { return true })
	if txt := hit("Conc Caster", 30, 1000); !strings.Contains(txt, "DC 15 (") {
		t.Errorf("after the condition ends the reminder returns: %q", txt)
	}
	if txt := hit("Conc Caster", 0, 1000); strings.Contains(txt, "concentrating") {
		t.Errorf("no damage, yet a Concentration note: %q", txt)
	}
}

// The same conditional reminder reaches the monster's damage from a CHARACTER's attack (POST /api/action attack, attack_target.go),
// which writes the monster's Hit Points itself and does not go through hurtMonster. Invented monsters, AC 1 (a try repeats on a 1).
// Route level for the reply text. The reminder is a note only: no Concentration is tracked.
func TestPostgresMonsterConcentrationNoteOnCharacterAttack(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "conc atk gm")
	_, player := pgAgent(t, "conc atk player")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Monster reminders attack", "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("ConcAtk Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var base srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Goblin Warrior" {
			base = m
		}
	}
	caster, plain := base, base
	caster.Name, plain.Name = "ConcAtk Caster", "ConcAtk Plain"
	caster.AC, plain.AC = 1, 1
	caster.HP, plain.HP = 500, 500 // the hits below set the Hit Points to what they need; a base HP of a few would let an ordinary hit kill and flake the survive-the-hit cases
	caster.Actions = append([]srdMonsterEntry{{Name: "Spellcasting", Text: "The caster casts one of the following spells, using Intelligence as the spellcasting ability (spell save DC 14): At Will: Light."}}, base.Actions...)
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented test monsters", "monsters": []srdMonster2024{caster, plain}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	keys := map[string]string{}
	for _, m := range j["monsters"].([]interface{}) {
		mm := m.(map[string]interface{})
		keys[fmt.Sprint(mm["name"])] = fmt.Sprint(mm["monster_key"])
	}
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "ConcAtk Caster", "monster_key": keys["ConcAtk Caster"], "initiative": 1}, {"name": "ConcAtk Plain", "monster_key": keys["ConcAtk Plain"], "initiative": 1}}})
	var equipped string
	db.QueryRow(`SELECT COALESCE(equipped_main_hand,'') FROM characters WHERE id=$1`, char).Scan(&equipped)
	if equipped == "" {
		pgCall(t, "POST", "/api/characters/equip-weapon", player, map[string]interface{}{"character_id": char, "weapon": "dagger", "slot": "main_hand"})
		equipped = "dagger"
	}
	knock := ""
	keepNotices := false
	lastReply := ""
	drainNotices := func() string {
		var out []string
		for _, n := range noticesOf(t, camp) {
			out = append(out, n.Text)
		}
		pgExec(t, `UPDATE gm_notices SET acknowledged = true WHERE lobby_id = $1`, camp)
		return strings.Join(out, " | ")
	}
	attack := func(name string, hp int, temp ...int) string {
		t.Helper()
		ids := monsterIDsByKeyOrName(camp, name)
		if len(ids) != 1 {
			t.Fatalf("%s: ids %v", name, ids)
		}
		for try := 0; try < 40; try++ {
			drainNotices()
			if err := setMonsterHP(camp, ids[0], hp); err != nil {
				t.Fatal(err)
			}
			if len(temp) > 0 {
				setMonsterTempHP(camp, ids[0], temp[0])
			}
			pgExec(t, `UPDATE characters SET action_used=false,attacks_remaining=NULL,conditions='[]' WHERE id=$1`, char)
			status, r := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"action": "attack", "target_id": ids[0], "description": "I attack with my " + equipped + knock})
			if status != 200 {
				t.Fatalf("attack: %d %v", status, r)
			}
			if am, _ := r["attack"].(map[string]interface{}); am != nil && am["hit"] == true { // this reply's own hit, not a stale record of an earlier one
				lastReply = fmt.Sprint(r)
				for _, m := range []string{"currently concentrating", "concentration ends while", "which ends concentration", "the server does not track", "gm_notices"} {
					if strings.Contains(strings.ToLower(lastReply), m) {
						t.Errorf("the character's attack reply carries a Concentration note (%q): %.400s", m, lastReply)
					}
				}
				if keepNotices {
					return ""
				}
				return drainNotices() // the GM's notices this hit wrote
			}
		}
		t.Fatalf("%s: no hit in 40 tries", name)
		return ""
	}
	if txt := attack("ConcAtk Caster", 500); !strings.Contains(txt, "Only if ConcAtk Caster is currently concentrating") || !strings.Contains(txt, "ignore this") {
		t.Errorf("a character's hit on a caster must carry the conditional reminder: %s", txt)
	}
	// Temporary Hit Points absorb all of the damage: the real Hit Points do not change, but the creature still took damage
	if txt := attack("ConcAtk Caster", 500, 1000); !strings.Contains(txt, "Only if ConcAtk Caster is currently concentrating") {
		t.Errorf("damage absorbed by Temporary Hit Points is still damage taken, so the reminder applies: %s", txt)
	}
	cid := monsterIDsByKeyOrName(camp, "ConcAtk Caster")[0]
	if v := monsterEntryVitals(camp, cid); v.HP != v.Max || v.Temp >= 1000 {
		t.Errorf("setup: the Temporary Hit Points should have absorbed the whole hit (real HP unchanged, Temporary Hit Points reduced): %+v", v)
	}
	// AUDIENCE: the notice a character's hit writes is private. The player's own reply (checked in attack), every player-readable route and
	// the shared action log carry nothing of it; the player cannot read or acknowledge the GM's notices; the GM can, and acknowledging removes it.
	keepNotices = true
	attack("ConcAtk Caster", 500, 0)
	keepNotices = false
	if len(noticesOf(t, camp)) == 0 {
		t.Fatal("audience: control: the hit wrote no GM notice, so the absence checks below prove nothing")
	}
	// markers of the notice's own wording (a campaign or rules text may use the word Concentration for other reasons)
	markers := []string{"currently concentrating", "concentration ends while", "which ends concentration", "the server does not track", "gm_notices", "end that effect now"}
	leak := func(where, out string) {
		low := strings.ToLower(out)
		for _, m := range markers {
			if i := strings.Index(low, m); i >= 0 {
				t.Errorf("audience: %s shows a private Concentration notice (%q): %.300s", where, m, out[max(0, i-100):])
				return
			}
		}
	}
	for _, rd := range []struct{ method, path string }{{"GET", "/api/my-turn"}, {"GET", cp + "/log"}, {"GET", cp}, {"GET", "/api/gm/status?campaign_id=" + fmt.Sprint(camp)}} {
		_, j := pgCall(t, rd.method, rd.path, player, nil)
		leak("player "+rd.method+" "+rd.path, fmt.Sprint(j))
	}
	if recs, err := readActions(actionFilter{lobby: camp, limit: 200}); err != nil || len(recs) == 0 {
		t.Fatalf("audience: reading the shared action log: %d records, %v", len(recs), err)
	} else {
		for _, rec := range recs {
			leak("the action log ("+rec.Type+")", rec.Description+" "+rec.Result)
		}
	}
	for _, m := range []string{"GET", "DELETE"} {
		code, j := pgCall(t, m, "/api/gm/notices", player, map[string]interface{}{"all": true})
		if code != 403 || !strings.Contains(fmt.Sprint(j), "not_gm") || strings.Contains(strings.ToLower(fmt.Sprint(j)), "concentrat") {
			t.Errorf("audience: a player's %s /api/gm/notices got %d %v", m, code, j)
		}
	}
	if len(noticesOf(t, camp)) == 0 {
		t.Error("audience: a player's refused DELETE acknowledged the GM's notices")
	}
	code, j := pgCall(t, "GET", "/api/gm/notices?campaign_id="+fmt.Sprint(camp), gm, nil)
	if out := fmt.Sprint(j); code != 200 || !strings.Contains(out, "ConcAtk Caster") || !strings.Contains(out, "concentrating") {
		t.Errorf("audience: the GM's notices lack the Concentration reminder: %d %v", code, j)
	}
	if _, j := pgCall(t, "GET", "/api/gm/status?campaign_id="+fmt.Sprint(camp), gm, nil); !strings.Contains(fmt.Sprint(j["gm_notices"]), "ConcAtk Caster") {
		t.Errorf("audience: the GM's status lacks gm_notices: %v", j["gm_notices"])
	}
	if code, j := pgCall(t, "DELETE", "/api/gm/notices?campaign_id="+fmt.Sprint(camp), gm, map[string]interface{}{"all": true}); code != 200 || len(noticesOf(t, camp)) != 0 {
		t.Errorf("audience: the GM could not acknowledge: %d %v", code, j)
	}
	// a kill: the Concentration-ends note, no save
	if txt := attack("ConcAtk Caster", 1, 0); !strings.Contains(txt, "is dead, which ends Concentration") || strings.Contains(txt, "Constitution saving throw") {
		t.Errorf("a character's killing hit on a caster: want the Concentration-ends note and no save: %s", txt)
	}
	if txt := attack("ConcAtk Plain", 500); strings.Contains(txt, "concentrating") {
		t.Errorf("a monster without Spellcasting got the reminder: %s", txt)
	}
	// a knock-out (Knocking Out a Creature: 1 Hit Point, Unconscious, which includes Incapacitated): the Concentration-ends note
	knock = " to knock it out, nonlethal"
	if txt := attack("ConcAtk Caster", 1, 0); !strings.Contains(lastReply, "knocked out") || !strings.Contains(txt, "has the Unconscious condition, which includes Incapacitated") || strings.Contains(txt, "Constitution saving throw") {
		t.Errorf("a knock-out of a caster: want the knocked-out text and the Concentration-ends note, no save: %s", txt)
	}
}

// noticesOf: the campaign's pending GM notices, failing the test when the table cannot be read.
func noticesOf(t *testing.T, camp int) []gmNotice {
	t.Helper()
	list, err := pendingGMNotices(camp)
	if err != nil {
		t.Fatalf("reading the GM notices: %v", err)
	}
	return list
}

// TestPostgresGMNoticesCampaignIsolationAndFailures: the persistent GM notices belong to one campaign. A GM of another campaign cannot list
// or acknowledge them (by ids, by all, or by naming the campaign), status is campaign-scoped, a mixed id list acknowledges only the
// caller's own and reports the rest, and a database failure is explicit (held in memory and flagged, an error on read), never "no notices".
func TestPostgresGMNoticesCampaignIsolationAndFailures(t *testing.T) {
	testPostgres(t)
	mk := func(label string) (string, int) {
		_, gm := pgAgent(t, label)
		_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": label, "max_players": 2})
		camp := int(j["campaign_id"].(float64))
		cp := fmt.Sprintf("/api/campaigns/%d", camp)
		pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
		_, player := pgAgent(t, label+" player")
		_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Iso Hero %d", time.Now().UnixNano()%1e9)))
		char := int(j["character_id"].(float64))
		pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
		pgFinish(t, player, char)
		pgCall(t, "POST", cp+"/ready", player, nil)
		pgCall(t, "POST", cp+"/ready", gm, nil)
		if c, j := pgCall(t, "POST", cp+"/start", gm, nil); c != 200 {
			t.Fatalf("start: %d %v", c, j)
		}
		return gm, camp
	}
	gmA, campA := mk("notice iso A")
	gmB, campB := mk("notice iso B")
	t.Cleanup(func() { heldGMNotices.ack(campA, nil, true); heldGMNotices.ack(campB, nil, true) })
	addGMNotice(campA, "ISO-A secret one")
	addGMNotice(campA, "ISO-A secret two")
	addGMNotice(campB, "ISO-B own note")
	idsOf := func(camp int) []int {
		out := []int{}
		for _, n := range noticesOf(t, camp) {
			out = append(out, n.ID)
		}
		return out
	}
	a, b := idsOf(campA), idsOf(campB)
	if len(a) != 2 || len(b) != 1 {
		t.Fatalf("setup: campaign A has %v, campaign B has %v", a, b)
	}
	q := func(c int) string { return "?campaign_id=" + fmt.Sprint(c) }
	// B's GM reads: only B's own; naming A is refused
	_, j := pgCall(t, "GET", "/api/gm/notices"+q(campB), gmB, nil)
	if out := fmt.Sprint(j); !strings.Contains(out, "ISO-B own note") || strings.Contains(out, "ISO-A") {
		t.Errorf("B's GM list: %v", j)
	}
	for _, m := range []string{"GET", "DELETE"} {
		code, j := pgCall(t, m, "/api/gm/notices"+q(campA), gmB, map[string]interface{}{"all": true})
		if code == 200 || strings.Contains(fmt.Sprint(j), "ISO-A") {
			t.Errorf("B's GM %s naming campaign A got %d %v", m, code, j)
		}
	}
	// B's GM, own campaign, acknowledges A's ids and "all": nothing of A's changes
	code, j := pgCall(t, "DELETE", "/api/gm/notices"+q(campB), gmB, map[string]interface{}{"ids": a})
	if code != 200 || j["acknowledged"] != float64(0) || len(idsOf(campA)) != 2 || strings.Contains(fmt.Sprint(j["gm_notices"]), "ISO-A") {
		t.Errorf("B's GM acknowledging A's ids: %d %v; A now has %v", code, j, idsOf(campA))
	}
	// mixed ids: B's own, A's, and one that does not exist: only B's own is acknowledged, the others are reported
	code, j = pgCall(t, "DELETE", "/api/gm/notices"+q(campB), gmB, map[string]interface{}{"ids": []int{b[0], a[0], 999999999}})
	if um := fmt.Sprint(j["unmatched_ids"]); code != 200 || j["acknowledged"] != float64(1) || !strings.Contains(um, fmt.Sprint(a[0])) || !strings.Contains(um, "9.99999999e+08") || len(idsOf(campB)) != 0 || len(idsOf(campA)) != 2 {
		t.Errorf("mixed ids: %d %v; A has %v, B has %v", code, j, idsOf(campA), idsOf(campB))
	}
	addGMNotice(campB, "ISO-B second")
	code, j = pgCall(t, "DELETE", "/api/gm/notices"+q(campB), gmB, map[string]interface{}{"all": true})
	if code != 200 || len(idsOf(campA)) != 2 || len(idsOf(campB)) != 0 {
		t.Errorf("B's GM all: %d %v; A has %v", code, j, idsOf(campA))
	}
	// status is campaign-scoped
	_, j = pgCall(t, "GET", "/api/gm/status"+q(campB), gmB, nil)
	if strings.Contains(fmt.Sprint(j), "ISO-A") {
		t.Errorf("B's status shows A's notices: %v", j)
	}
	_, j = pgCall(t, "GET", "/api/gm/status"+q(campA), gmA, nil)
	if !strings.Contains(fmt.Sprint(j["gm_notices"]), "ISO-A secret one") {
		t.Errorf("control: A's own status lacks its notices: %v", j["gm_notices"])
	}
	if code, _ := pgCall(t, "GET", "/api/gm/status"+q(campA), gmB, nil); code == 200 {
		t.Error("B's GM read A's status")
	}

	// DATABASE FAILURE: the notice table cannot be written or read. The write is held in memory and flagged; the read is an error.
	pgExec(t, `ALTER TABLE gm_notices RENAME TO gm_notices_off`)
	restored := false
	restore := func() {
		if !restored {
			pgExec(t, `ALTER TABLE gm_notices_off RENAME TO gm_notices`)
			restored = true
		}
	}
	t.Cleanup(restore)
	addGMNotice(campA, "ISO-A held while the table was down")
	code, j = pgCall(t, "GET", "/api/gm/notices"+q(campA), gmA, nil)
	if code != 500 || j["error"] != "database_error" {
		t.Errorf("a failed read must be a database error, got %d %v", code, j)
	}
	code, j = pgCall(t, "DELETE", "/api/gm/notices"+q(campA), gmA, map[string]interface{}{"all": true})
	if code != 500 || j["error"] != "database_error" {
		t.Errorf("a failed acknowledgment must be a database error, got %d %v", code, j)
	}
	if len(heldGMNotices.list(campA)) != 1 {
		t.Errorf("a failed acknowledgment must not drop the held notice: %v", heldGMNotices.list(campA))
	}
	// the status route also answers the failure (the request's own failed database call), never a 200 that omits the notices
	if code, j = pgCall(t, "GET", "/api/gm/status"+q(campA), gmA, nil); code != 500 || j["error"] != "database_error" {
		t.Errorf("status with the table down: want a database error, got %d %v", code, j)
	}
	if _, j = pgCall(t, "GET", "/api/gm/status"+q(campB), gmB, nil); strings.Contains(fmt.Sprint(j), "held while") {
		t.Errorf("B's status shows A's held notice: %v", j)
	}
	restore()
	code, j = pgCall(t, "GET", "/api/gm/notices"+q(campA), gmA, nil)
	if out := fmt.Sprint(j); code != 200 || !strings.Contains(out, "held while the table was down") || !strings.Contains(out, "ISO-A secret one") {
		t.Errorf("after recovery: %d %v", code, j)
	}
	if code, j = pgCall(t, "DELETE", "/api/gm/notices"+q(campA), gmA, map[string]interface{}{"all": true}); code != 200 || len(noticesOf(t, campA)) != 0 {
		t.Errorf("acknowledging all, held and stored: %d %v", code, j)
	}
}

// TestPostgresGMNoticeAcknowledgmentIsAllOrNothing: an acknowledgment that fails part way changes nothing (one transaction), and the
// reply is a database error, not a partial success.
func TestPostgresGMNoticeAcknowledgmentIsAllOrNothing(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "notice ack gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Notice ack", "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	_, player := pgAgent(t, "notice ack player")
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Ack Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	addGMNotice(camp, "ACK first")
	addGMNotice(camp, "ACK second")
	list := noticesOf(t, camp)
	if len(list) != 2 {
		t.Fatalf("setup: %v", list)
	}
	pgExec(t, `CREATE OR REPLACE FUNCTION pg_test_refuse_ack() RETURNS trigger AS $$ BEGIN
		IF NEW.text = 'ACK second' THEN RAISE EXCEPTION 'test: ack refused'; END IF; RETURN NEW; END $$ LANGUAGE plpgsql`)
	pgExec(t, `CREATE TRIGGER pg_test_refuse_ack BEFORE UPDATE ON gm_notices FOR EACH ROW EXECUTE FUNCTION pg_test_refuse_ack()`)
	dropped := false
	drop := func() {
		if !dropped {
			pgExec(t, `DROP TRIGGER IF EXISTS pg_test_refuse_ack ON gm_notices`)
			pgExec(t, `DROP FUNCTION IF EXISTS pg_test_refuse_ack()`)
			dropped = true
		}
	}
	t.Cleanup(drop)
	code, j := pgCall(t, "DELETE", "/api/gm/notices?campaign_id="+fmt.Sprint(camp), gm, map[string]interface{}{"ids": []int{list[0].ID, list[1].ID}})
	if code != 500 || j["error"] != "database_error" || strings.Contains(fmt.Sprint(j), "test: ack refused") {
		t.Errorf("a failed acknowledgment: %d %v", code, j)
	}
	if got := noticesOf(t, camp); len(got) != 2 {
		t.Errorf("the failed acknowledgment must change nothing (the first id was acknowledged before the second failed), %d left: %v", len(got), got)
	}
	drop()
	if code, j = pgCall(t, "DELETE", "/api/gm/notices?campaign_id="+fmt.Sprint(camp), gm, map[string]interface{}{"ids": []int{list[0].ID, list[1].ID}}); code != 200 || j["acknowledged"] != float64(2) {
		t.Errorf("the retry: %d %v", code, j)
	}
}
