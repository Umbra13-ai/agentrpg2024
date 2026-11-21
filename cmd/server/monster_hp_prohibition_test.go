package main

import (
	"fmt"
	"strings"
	"testing"
)

// The explicit stat-block prohibition "can't regain Hit Points or gain Temporary Hit Points" against every way Hit Points
// come back: direct healing, absorption, hit refunds, Regeneration and companion healing; and the failure handling of the
// catalog read that enforces it (a failed or unreadable read fails the change loudly, never silently permits the gain).
// Invented fixtures only; the rule lives in the shared owners (monster_hp_2024.go), never in per-monster code.

const hpGainProhibitionText = "The collective can't regain Hit Points or gain Temporary Hit Points."
const hpGainAbsorptionText = "Whenever the warden is subjected to Fire damage, it regains a number of Hit Points equal to the Fire damage dealt."

// hpGainMonster imports an invented absorber (Fire immunity plus a Fire absorption trait, optionally with the explicit
// Hit Point gain prohibition) and adds it to the campaign's fight. It returns the compendium key and the combatant id.
func hpGainMonster(t *testing.T, gm string, camp int, name string, restricted, regenerates bool) (string, int) {
	t.Helper()
	m := compendiumFixture()
	m.Name = name
	m.DmgImmune = "Poison, Fire"
	m.Resistances = ""
	m.Traits = []srdMonsterEntry{{Name: "Fire Absorption", Text: hpGainAbsorptionText}}
	if regenerates {
		m.Traits = append(m.Traits, srdMonsterEntry{Name: "Regeneration", Text: "The warden regains 15 Hit Points at the start of each of its turns."})
	}
	if restricted {
		m.Traits = append(m.Traits, srdMonsterEntry{Name: "Collective", Text: hpGainProhibitionText})
	}
	status, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "Invented HP gain prohibition control", "monsters": []srdMonster2024{m}})
	if status != 201 {
		t.Fatalf("import %d %v", status, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM custom_monsters WHERE key=$1", key); err != nil {
			t.Error(err)
		}
	})
	status, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": name, "monster_key": key}}})
	if status != 200 {
		t.Fatalf("combat add %d %v", status, j)
	}
	return key, monsterIDsByKeyOrName(camp, name)[0]
}

// Absorption and the Hit Point writer: a prohibited creature is immune to its absorption type, nothing more; an
// unrestricted one regains the damage dealt. Every absorption decision (the GM's damage route, the extra-damage funnel
// and riders) and the writer's refund branch are checked.
func TestPostgresHPGainProhibitionAbsorption(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, _, _ := companionReactionFixture(t)
	for _, restricted := range []bool{false, true} {
		t.Run(fmt.Sprint(restricted), func(t *testing.T) {
			key, id := hpGainMonster(t, gm, camp, fmt.Sprintf("Invented absorber %t", restricted), restricted, false)
			if err := setMonsterHP(camp, id, 100); err != nil {
				t.Fatal(err)
			}
			text, ok := damageMonster(camp, id, 40, "fire") // the GM's damage route (hurtMonster)
			if !ok {
				t.Fatalf("damage failed: %s", text)
			}
			wantHP, wantWord := 140, "absorbs"
			if restricted {
				wantHP, wantWord = 100, "immune"
			}
			if got := monsterHPNow(camp, id); got != wantHP {
				t.Errorf("HP %d, want %d (%s)", got, wantHP, text)
			}
			if !strings.Contains(text, wantWord) {
				t.Errorf("the reply says %q, want %q in it", text, wantWord)
			}
			if restricted && strings.Contains(text, "absorbs") {
				t.Errorf("a prohibited creature was told to absorb: %s", text)
			}
			d, how := adjustMonsterExtra(key, "fire", 9) // the one funnel of a hit's extra damage (Graze, weapon effects)
			if !restricted && (d != -9 || how != "absorbed") {
				t.Errorf("adjustMonsterExtra = %d %q, want -9 absorbed", d, how)
			}
			if restricted && (d != 0 || how != "immune") {
				t.Errorf("adjustMonsterExtra = %d %q, want 0 immune for a prohibited creature", d, how)
			}
			total, rtext, _ := adjustRiders(key, map[string]int{"fire": 7})
			if !restricted && (total != -7 || !strings.Contains(rtext, "absorbs")) {
				t.Errorf("adjustRiders = %d %q, want -7 with absorption", total, rtext)
			}
			if restricted && (total != 0 || strings.Contains(rtext, "absorbs")) {
				t.Errorf("adjustRiders = %d %q, want 0 without absorption for a prohibited creature", total, rtext)
			}
			// the writer itself: a hit whose rules gave Hit Points back (lost < 0)
			b0 := copyEntry(mustEntry(t, camp, id))
			a0 := copyEntry(b0)
			a0["hp"] = asInt(b0["hp"]) + 20
			saved, err := saveMonsterHit(camp, id, b0, a0, -20, map[string]int{"fire": 0})
			if err != nil {
				t.Fatalf("saveMonsterHit: %v", err)
			}
			wantSaved := asInt(b0["hp"]) + 20
			if restricted {
				wantSaved = asInt(b0["hp"])
			}
			if saved != wantSaved {
				t.Errorf("the writer gave Hit Points back: got %d, want %d", saved, wantSaved)
			}
			if got := monsterHPNow(camp, id); got != wantSaved {
				t.Errorf("stored HP %d, want %d", got, wantSaved)
			}
		})
	}
}

// Regeneration runs through the shared healing owner: a prohibited creature does not regenerate; an unrestricted one does.
func TestPostgresHPGainProhibitionRegeneration(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, _, _ := companionReactionFixture(t)
	for _, restricted := range []bool{false, true} {
		t.Run(fmt.Sprint(restricted), func(t *testing.T) {
			_, id := hpGainMonster(t, gm, camp, fmt.Sprintf("Invented regenerator %t", restricted), restricted, true)
			if err := setMonsterHP(camp, id, 50); err != nil {
				t.Fatal(err)
			}
			e, _, _ := monsterCombatant(camp, id)
			log := monsterFeaturesTurnStart(camp, id, sprintAny(e["name"]))
			want := 65
			if restricted {
				want = 50
			}
			if got := monsterHPNow(camp, id); got != want {
				t.Errorf("HP %d after its turn starts, want %d (log %v)", got, want, log)
			}
		})
	}
}

// A companion made from a prohibited form is immune to its absorption type without regaining anything; the GM's companion
// damage route says so.
func TestPostgresHPGainProhibitionCompanionAbsorption(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, _, _ := companionReactionFixture(t)
	for _, restricted := range []bool{false, true} {
		t.Run(fmt.Sprint(restricted), func(t *testing.T) {
			key, _ := hpGainMonster(t, gm, camp, fmt.Sprintf("Invented companion absorber %t", restricted), restricted, false)
			status, j := pgCall(t, "POST", "/api/gm/companion", gm, map[string]interface{}{"campaign_id": camp, "character_id": hero, "monster_key": key})
			if status != 200 {
				t.Fatalf("companion %d %v", status, j)
			}
			pet := asInt(j["companion"].(map[string]interface{})["id"])
			change := func(body map[string]interface{}) map[string]interface{} {
				body["campaign_id"] = camp
				body["companion_id"] = pet
				s, r := pgCall(t, "POST", "/api/gm/companion", gm, body)
				if s != 200 {
					t.Fatalf("companion change %d %v", s, r)
				}
				return r
			}
			change(map[string]interface{}{"damage": 10, "damage_type": "slashing"}) // a type it is not immune to wounds it
			r := change(map[string]interface{}{"damage": 6, "damage_type": "fire"})
			result := fmt.Sprint(r["result"])
			c, ok := loadCompanion(pet)
			if !ok {
				t.Fatal("companion missing")
			}
			wantHP, wantWord := 396, "absorbs"
			if restricted {
				wantHP, wantWord = 390, "immune"
			}
			if c.HP != wantHP {
				t.Errorf("companion HP %d, want %d (%s)", c.HP, wantHP, result)
			}
			if !strings.Contains(result, wantWord) {
				t.Errorf("the route says %q, want %q in it", result, wantWord)
			}
			if restricted && strings.Contains(result, "absorbs") {
				t.Errorf("a prohibited companion was told to absorb: %s", result)
			}
		})
	}
}

// The catalog read that enforces the prohibition fails closed: a failed or unreadable read fails the HP change loudly
// (a database error the caller answers), never a silent permit. Both the in-process owner and the GM route are checked,
// on a restricted and an unrestricted monster; the absolute GM correction still works afterward.
func TestPostgresHPGainProhibitionCatalogFailure(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, _, _ := companionReactionFixture(t)
	for _, restricted := range []bool{false, true} {
		t.Run(fmt.Sprint(restricted), func(t *testing.T) {
			key, id := hpGainMonster(t, gm, camp, fmt.Sprintf("Invented catalog control %t", restricted), restricted, false)
			if err := setMonsterHP(camp, id, 100); err != nil {
				t.Fatal(err)
			}
			pgExec(t, `ALTER TABLE custom_monsters RENAME TO custom_monsters_broken`)
			_, _, herr := healMonster(camp, id, 5)
			terr := setMonsterTempHP(camp, id, 7)
			status, _ := pgCall(t, "POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target_id": id, "heal": 5})
			pgExec(t, `ALTER TABLE custom_monsters_broken RENAME TO custom_monsters`)
			if herr == nil || terr == nil {
				t.Errorf("the owner permitted the gain over a failed catalog read (heal %v, temp %v)", herr, terr)
			}
			if status != 500 {
				t.Errorf("the GM route answered %d over a failed catalog read, want 500", status)
			}
			if got := monsterHPNow(camp, id); got != 100 {
				t.Errorf("HP %d after the refused gain, want 100", got)
			}
			// unreadable stored traits fail the same way
			pgExec(t, `UPDATE custom_monsters SET stats = jsonb_set(stats, '{traits}', '"broken"') WHERE key = $1`, key)
			_, _, herr = healMonster(camp, id, 5)
			status, _ = pgCall(t, "POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target_id": id, "heal": 5})
			if herr == nil {
				t.Error("the owner permitted the gain over unreadable traits")
			}
			if status != 500 {
				t.Errorf("the GM route answered %d over unreadable traits, want 500", status)
			}
			if got := monsterHPNow(camp, id); got != 100 {
				t.Errorf("HP %d after the refused gain, want 100", got)
			}
			if _, err := db.Exec("DELETE FROM custom_monsters WHERE key=$1", key); err != nil { // broken beyond cleanup's repair
				t.Error(err)
			}
			// the catalog reads again: the absolute correction and a legal heal both work
			key2, id2 := hpGainMonster(t, gm, camp, fmt.Sprintf("Invented catalog control %t restored", restricted), restricted, false)
			_ = key2
			if err := setMonsterHP(camp, id2, 90); err != nil {
				t.Errorf("the absolute correction failed after the catalog recovered: %v", err)
			}
			if _, _, err := healMonster(camp, id2, 5); err != nil {
				t.Fatalf("the control heal failed after the catalog recovered: %v", err)
			}
			want := 95
			if restricted {
				want = 90
			}
			if got := monsterHPNow(camp, id2); got != want {
				t.Errorf("HP %d after the recovered catalog, want %d", got, want)
			}
		})
	}
}

// The main hit (resolveAttackOnMonster, the character attack path): a prohibited absorber is immune, nothing more; an
// unrestricted one regains the damage dealt. The reply's words and the Hit Points agree.
func TestPostgresHPGainProhibitionMainHit(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, _, _ := companionReactionFixture(t)
	for _, restricted := range []bool{false, true} {
		t.Run(fmt.Sprint(restricted), func(t *testing.T) {
			_, id := hpGainMonster(t, gm, camp, fmt.Sprintf("Invented main-hit absorber %t", restricted), restricted, false)
			if err := setMonsterHP(camp, id, 100); err != nil {
				t.Fatal(err)
			}
			text, _, err := resolveAttackOnMonster(camp, 0, id, attackOutcome{Weapon: "Longsword", Total: 20, Natural: 12, Judged: true, Hit: true, Damage: 40, DamageType: "fire"})
			if err != nil {
				t.Fatal(err)
			}
			got := monsterHPNow(camp, id)
			if restricted {
				if got != 100 {
					t.Errorf("HP %d, want 100 (%s)", got, text)
				}
				if !strings.Contains(text, "immune") {
					t.Errorf("the reply says %q, want \"immune\" in it", text)
				}
				if strings.Contains(text, "absorbs") || strings.Contains(text, "regains") {
					t.Errorf("a prohibited creature was told to absorb: %s", text)
				}
			} else {
				if got != 140 {
					t.Errorf("HP %d, want 140 (%s)", got, text)
				}
				if !strings.Contains(text, "absorbs") || !strings.Contains(text, "regains") {
					t.Errorf("the reply says %q, want the absorption in it", text)
				}
			}
		})
	}
}

// A failed prohibition read fails the whole change atomically: one monster's stored traits are corrupted (its immunities
// stay readable, so the hit still reaches the absorption decision), and the refused absorption hit, the refused regain
// write and the refused heal/temp gains leave every field of the entry as it was.
func TestPostgresHPGainProhibitionCatalogAtomicity(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, _, _ := companionReactionFixture(t)
	key, id := hpGainMonster(t, gm, camp, "Invented atomic absorber", false, false)
	if err := setMonsterHP(camp, id, 100); err != nil {
		t.Fatal(err)
	}
	if err := setMonsterTempHP(camp, id, 7); err != nil {
		t.Fatal(err)
	}
	before := mustEntry(t, camp, id)
	pgExec(t, `UPDATE custom_monsters SET stats = jsonb_set(stats, '{traits}', '"broken"') WHERE key = $1`, key)

	if _, _, err := healMonster(camp, id, 10); err == nil {
		t.Error("healMonster: an unreadable catalog must fail the heal")
	}
	if err := setMonsterTempHP(camp, id, 12); err == nil {
		t.Error("setMonsterTempHP: an unreadable catalog must fail the gain")
	}
	if _, ok := hurtMonster(camp, id, 40, "fire", false); ok {
		t.Error("hurtMonster: an unreadable catalog must refuse the absorption hit")
	}
	b0 := mustEntry(t, camp, id)
	a0 := copyEntry(b0)
	a0["hp"] = asInt(b0["hp"]) + 20
	if _, err := saveMonsterHit(camp, id, b0, a0, -20, map[string]int{"fire": 0}); err == nil {
		t.Error("saveMonsterHit: an unreadable catalog must fail the regain write")
	}

	after := mustEntry(t, camp, id)
	for _, f := range []string{"hp", "temp_hp", "max_hp", "name", "monster_key"} {
		if fmt.Sprint(after[f]) != fmt.Sprint(before[f]) {
			t.Errorf("%s changed across the refused changes: %v -> %v (no partial mutation expected)", f, before[f], after[f])
		}
	}
}

// A monster whose row is simply absent from the catalog is not a catalog failure: sql.ErrNoRows is "no prohibition on
// record", and the gain is allowed. Distinct from a failed or unreadable read, which fails the change.
func TestPostgresHPGainProhibitionCatalogMissingRow(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, _, _ := companionReactionFixture(t)
	key, id := hpGainMonster(t, gm, camp, "Invented missing-row absorber", true, false)
	if err := setMonsterHP(camp, id, 100); err != nil {
		t.Fatal(err)
	}
	pgExec(t, `DELETE FROM custom_monsters WHERE key = $1`, key)
	if _, _, err := healMonster(camp, id, 10); err != nil {
		t.Fatalf("a missing catalog row is not a failure: %v", err)
	}
	if got := monsterHPNow(camp, id); got != 110 {
		t.Errorf("HP %d after the heal over a missing row, want 110", got)
	}
	if heals, err := absorptionHealsHP(db, key, "fire"); err != nil || heals {
		t.Errorf("absorptionHealsHP over a missing row = %t, %v; want false, nil", heals, err)
	}
}
