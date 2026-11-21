package main

import (
	"fmt"
	"strings"
	"testing"
)

// importedGolem imports the SRD Iron Golem through the shared compendium and puts it into a fight; it returns the campaign and the
// monster's combat id. Absorption is a trait of its stat block (monsterAbsorbs), not code for this monster.
func importedGolem(t *testing.T) (camp, id int) {
	return importedAbsorber(t, "Fire")
}

// importedAbsorber is the SRD Iron Golem with its Fire absorption changed to another damage type (the trait text and the immunity
// move together), imported through the shared compendium: the rule reads the text, so any type works.
func importedAbsorber(t *testing.T, dtype string) (camp, id int) {
	t.Helper()
	testPostgres(t)
	_, gm := pgAgent(t, "absorption GM")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Absorption", "max_players": 2})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp = int(j["campaign_id"].(float64))
	var m srdMonster2024
	for _, entry := range srdMonsters2024() {
		if entry.Name == "Iron Golem" {
			m = entry
		}
	}
	if m.Name == "" {
		t.Fatal("fixture missing")
	}
	if dtype != "Fire" {
		m.Name = "Absorbing " + dtype + " Golem"
		m.DmgImmune = strings.ReplaceAll(m.DmgImmune, "Fire", dtype)
		var tr []srdMonsterEntry
		for _, e := range m.Traits {
			e.Text = strings.ReplaceAll(e.Text, "Fire", dtype)
			tr = append(tr, e)
		}
		m.Traits = tr
	}
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Iron Golem imported for absorption tests", "monsters": []srdMonster2024{m}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	pgExec(t, `UPDATE lobbies SET status='active',schedule_state='off' WHERE id=$1`, camp)
	pgFight(t, camp)
	c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Absorbing Golem", "monster_key": key}}})
	if c != 200 {
		t.Fatalf("combat/add: %d %v", c, j)
	}
	f, err := readFight(db, camp)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range f.Order {
		if e["monster_key"] == key {
			id = asInt(e["id"])
		}
	}
	if id == 0 {
		t.Fatal("combatant missing")
	}
	return camp, id
}

// Release-blocking counterexample from the 2026-11-17 audit: a wounded imported Iron Golem regains the Fire damage dealt.
func TestPostgresFullAbilityAbsorption(t *testing.T) {
	camp, id := importedGolem(t)
	if err := setMonsterHP(camp, id, 100); err != nil {
		t.Fatal(err)
	}
	text, ok := damageMonster(camp, id, 40, "fire")
	if !ok {
		t.Fatalf("shared damage path failed: %s", text)
	}
	if got := monsterHPNow(camp, id); got != 140 {
		t.Fatalf("Fire Absorption: wounded HP 100, incoming Fire 40; want HP 140, got %d. %s", got, text)
	}
}

// The rule, not the example: trigger and non-trigger, the maximum, Hit Points from other types, a critical hit's amount.
func TestPostgresAbsorptionRule(t *testing.T) {
	camp, id := importedGolem(t)
	max := asInt(mustEntry(t, camp, id)["max_hp"])
	check := func(name string, start, dmg int, dtype string, want int) {
		t.Helper()
		if err := setMonsterHP(camp, id, start); err != nil {
			t.Fatal(err)
		}
		text, ok := damageMonster(camp, id, dmg, dtype)
		if !ok {
			t.Fatalf("%s: damage failed", name)
		}
		if got := monsterHPNow(camp, id); got != want {
			t.Errorf("%s: HP %d, want %d (%s)", name, got, want, text)
		}
	}
	check("fire heals the damage dealt", 100, 40, "fire", 140)
	check("healing stops at the maximum", max-5, 40, "fire", max)
	check("full Hit Points stay full", max, 40, "fire", max)
	check("another immunity does not heal", 100, 40, "poison", 100)
	check("a type it is not immune to hurts", 100, 40, "cold", 60)
	check("a critical hit's larger amount heals as dealt", 50, 77, "fire", 127)
	text, _ := damageMonster(camp, id, 10, "fire")
	if !strings.Contains(text, "absorbs") {
		t.Errorf("the GM is not told about the absorption: %s", text)
	}
}

func mustEntry(t *testing.T, camp, id int) map[string]interface{} {
	t.Helper()
	f, err := readFight(db, camp)
	if err != nil {
		t.Fatal(err)
	}
	_, e := f.entry(id)
	if e == nil {
		t.Fatal("entry missing")
	}
	return e
}

func TestAdjustRidersAbsorbs(t *testing.T) {
	camp, id := importedGolem(t)
	f, err := readFight(db, camp)
	if err != nil {
		t.Fatal(err)
	}
	_, e := f.entry(id)
	key := sprintAny(e["monster_key"])
	total, text, _ := adjustRiders(key, map[string]int{"fire": 7, "cold": 3})
	if total != -7+3 || !strings.Contains(text, "absorbs the extra 7 fire") {
		t.Errorf("rider absorption: total=%d text=%q", total, text)
	}
}

// Every helper that adds extra damage to a weapon hit goes through adjustMonsterExtra: an absorbing monster gets the damage back as
// Hit Points (a negative amount the hit applies), and the reply says so. Checked for the types the helpers deal.
func TestPostgresExtraDamageAbsorbed(t *testing.T) {
	for _, tc := range []struct{ dtype string }{{"Slashing"}, {"Cold"}} {
		camp, id := importedAbsorber(t, tc.dtype)
		f, err := readFight(db, camp)
		if err != nil {
			t.Fatal(err)
		}
		_, e := f.entry(id)
		key := sprintAny(e["monster_key"])
		dt := strings.ToLower(tc.dtype)
		d, how := adjustMonsterExtra(key, dt, 9)
		if d != -9 || how != "absorbed" {
			t.Errorf("%s: adjustMonsterExtra = %d %q, want -9 absorbed", dt, d, how)
		}
		// the same monster takes other types as before
		if d, how := adjustMonsterExtra(key, "poison", 9); d != 0 || how != "immune" {
			t.Errorf("poison: %d %q (immune, not absorbed)", d, how)
		}
		var amount int
		var text string
		switch dt {
		case "slashing":
			amount, text = viciousHit(key, "Vicious Weapon", false)
		case "cold":
			amount, text = frostBrandHit(key, false)
		}
		if amount >= 0 || !strings.Contains(text, "absorbs") || !strings.Contains(text, "regains that many Hit Points") {
			t.Errorf("%s helper: amount %d, text %q", dt, amount, text)
		}
	}
	// a monster that does not absorb is unchanged
	if d, how := adjustMonsterExtra("", "fire", 9); d != 9 || how != "" {
		t.Errorf("no monster: %d %q", d, how)
	}
}

// weaponHitEffects is the one place the held item's extra damage comes back from: for an absorbing monster the extra is negative.
func TestPostgresWeaponHitEffectsAbsorbs(t *testing.T) {
	camp, id := importedAbsorber(t, "Cold")
	f, err := readFight(db, camp)
	if err != nil {
		t.Fatal(err)
	}
	_, e := f.entry(id)
	key := sprintAny(e["monster_key"])
	extra, dies, text := weaponHitEffects(camp, 0, id, key, "Absorbing Cold Golem", "Frost Brand", false, 10, 50)
	if extra >= 0 || dies || !strings.Contains(text, "absorbs") {
		t.Errorf("extra=%d dies=%v text=%q", extra, dies, text)
	}
}
