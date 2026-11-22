package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func compendiumFixture() srdMonster2024 {
	// Original high-level test creature, intentionally absent from the SRD.
	var m srdMonster2024
	json.Unmarshal([]byte(`{"name":"Test Astral Warden","size":"Huge","type":"Construct","ac":22,"hp":400,"cr":"24","xp":62000,"proficiency_bonus":7,"speed":"40 ft.","abilities":{"str":{"score":26,"save":8},"dex":{"score":14,"save":9},"con":{"score":26,"save":15},"int":{"score":18,"save":4},"wis":{"score":20,"save":12},"cha":{"score":22,"save":6}},"damage_immunities":"Poison","damage_resistances":"Fire","traits":[{"name":"Magic Resistance","text":"The warden has Advantage on saving throws against spells."},{"name":"Legendary Resistance (3/Day)","text":"The warden can choose to succeed on a failed saving throw."}],"actions":[{"name":"Slam","text":"Melee Attack Roll: +15, reach 10 ft. Hit: 21 (3d8 + 8) Force damage."}],"legendary_intro":"Legendary Action Uses: 3 (4 in Lair).","legendary_actions":[{"name":"Stride","text":"The warden moves up to half its Speed."}]}`), &m)
	return m
}
func TestCustomMonsterValidation(t *testing.T) {
	m := compendiumFixture()
	if msg := validateCustomMonster(m); msg != "" {
		t.Fatal(msg)
	}
	for _, bad := range []func(*srdMonster2024){func(m *srdMonster2024) { m.HP = 0 }, func(m *srdMonster2024) { m.AC = 51 }, func(m *srdMonster2024) { m.CR = "31" }, func(m *srdMonster2024) { delete(m.Abilities, "int") }, func(m *srdMonster2024) { m.Actions[0].Text = "" }} {
		m = compendiumFixture()
		bad(&m)
		if validateCustomMonster(m) == "" {
			t.Fatal("invalid monster accepted")
		}
	}
	m = compendiumFixture()
	s := customMonsterStats("custom:test", m)
	if s["legendary_resistances"] != 3 || s["legendary_action_count"] != 3 || s["intl"] != 18 || s["hit_dice"] != "" {
		t.Fatalf("adaptation: %v", s)
	}
}

// isolateCustomMonsters: the compendium list is one table, and this test counts and pages through it. The rows other tests left are set
// aside for the length of the test and put back after it, together with removing this test's own rows (the pages it adds), so the result
// does not depend on which tests ran before or after it (run with -shuffle).
func isolateCustomMonsters(t *testing.T) {
	t.Helper()
	type row struct {
		key, note  string
		def, stats []byte
		createdAt  time.Time
	}
	var kept []row
	rs, err := db.Query(`SELECT key, definition, stats, source_note, created_at FROM custom_monsters`)
	if err != nil {
		t.Fatal(err)
	}
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.key, &r.def, &r.stats, &r.note, &r.createdAt); err != nil {
			t.Fatal(err)
		}
		kept = append(kept, r)
	}
	rs.Close()
	pgExec(t, `DELETE FROM custom_monsters`)
	t.Cleanup(func() {
		db.Exec(`DELETE FROM custom_monsters`)
		for _, r := range kept {
			db.Exec(`INSERT INTO custom_monsters(key,definition,stats,source_note,created_at) VALUES($1,$2,$3,$4,$5)`, r.key, r.def, r.stats, r.note, r.createdAt)
		}
	})
}

func TestPostgresCustomCompendium(t *testing.T) {
	testPostgres(t)
	isolateCustomMonsters(t)
	_, gm := pgAgent(t, "compendium gm")
	_, other := pgAgent(t, "compendium other gm")
	_, player := pgAgent(t, "compendium player")
	create := func(token string) int {
		c, j := pgCall(t, "POST", "/api/campaigns", token, map[string]interface{}{"name": "Compendium test", "max_players": 4})
		if c != 200 {
			t.Fatalf("campaign %d %v", c, j)
		}
		return int(j["campaign_id"].(float64))
	}
	camp, otherCamp := create(gm), create(other)
	m := compendiumFixture()
	if path := os.Getenv("CUSTOM_MONSTER_FIXTURE"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		m = srdMonster2024{}
		if err = json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/gm/compendium/monsters"
	body := map[string]interface{}{"source_note": "Private test reference", "monsters": []srdMonster2024{m}}
	for _, tok := range []string{player} {
		if c, j := pgCall(t, "POST", path, tok, body); c != 403 {
			t.Fatalf("unauthorized import %d %v", c, j)
		}
	}
	if c, _ := pgCall(t, "GET", fmt.Sprintf("%s?campaign_id=%d", path, camp), "", nil); c != 401 {
		t.Fatalf("anonymous read %d", c)
	}
	var before int
	db.QueryRow(`SELECT COUNT(*) FROM monsters`).Scan(&before)
	c, j := pgCall(t, "POST", path, gm, body)
	if c != 201 {
		t.Fatalf("import %d %v", c, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	if !strings.HasPrefix(key, "custom:") {
		t.Fatal(key)
	}
	for _, tok := range []string{player} {
		if c, j := pgCall(t, "GET", fmt.Sprintf("%s?campaign_id=%d", path, camp), tok, nil); c != 403 {
			t.Fatalf("unauthorized read %d %v", c, j)
		}
	}
	if c, j := pgCall(t, "GET", fmt.Sprintf("%s?campaign_id=%d", path, camp), gm, nil); c != 200 || len(j["monsters"].([]interface{})) != 1 {
		t.Fatalf("readback %d %v", c, j)
	}
	if c, j := pgCall(t, "GET", "/api/universe/monsters/"+key, other, nil); c != 404 {
		t.Fatalf("public catalog leak %d %v", c, j)
	}
	if c, j := pgCall(t, "GET", "/api/universe/monsters/"+key, gm, nil); c != 404 {
		t.Fatalf("SRD catalog mixed %d %v", c, j)
	}
	// Invalid batch is atomic; malformed bodies use the shared strict reader.
	bad := m
	bad.HP = 0
	body["monsters"] = []srdMonster2024{m, bad}
	if c, j := pgCall(t, "POST", path, gm, body); c != 400 {
		t.Fatalf("invalid batch %d %v", c, j)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM custom_monsters`).Scan(&n)
	if n != 1 {
		t.Fatal("partial batch persisted")
	}
	// An overlong derived database field is refused during import, not deferred until combat.
	bad = m
	bad.HPFormula = strings.Repeat("1", 30) + "d8"
	body["monsters"] = []srdMonster2024{m, bad}
	if c, j := pgCall(t, "POST", path, gm, body); c != 400 {
		t.Fatalf("storage validation %d %v", c, j)
	}
	db.QueryRow(`SELECT COUNT(*) FROM custom_monsters`).Scan(&n)
	if n != 1 {
		t.Fatal("database validation left a partial import")
	}
	body["monsters"] = []srdMonster2024{m}
	body["unexpected"] = true
	if c, _ := pgCall(t, "POST", path, gm, body); c != 400 {
		t.Fatal("unknown field accepted")
	}
	delete(body, "unexpected")
	// Seed and migration reruns preserve private content.
	seedMonsters2024()
	if err := migrate(); err != nil {
		t.Fatal(err)
	}
	var after int
	db.QueryRow(`SELECT COUNT(*) FROM monsters`).Scan(&after)
	if after != before {
		t.Fatalf("SRD count %d -> %d", before, after)
	}
	sb := monsterStatBlock(key)
	if sb == nil || sb["hp"] != m.HP || sb["ac"] != m.AC {
		t.Fatalf("stat block %v", sb)
	}
	meta := monsterMetaFor(key)
	if meta == nil || !metaHasTrait(meta, "Magic Resistance") || meta.LR != 3 || meta.LAUses != 3 {
		t.Fatalf("metadata %+v", meta)
	}
	if got := monsterSaveBonus(key, "dex", m.Abilities["dex"].Score); got != m.Abilities["dex"].Save {
		t.Fatalf("save %d", got)
	}
	if d, _ := adjustMonsterDamage(key, "poison", 20); d != 0 {
		t.Fatalf("immunity %d", d)
	}
	for _, id := range []int{camp, otherCamp} {
		pgExec(t, `UPDATE lobbies SET status='active',schedule_state='off' WHERE id=$1`, id)
		pgFight(t, id)
	}
	add := map[string]interface{}{"combatants": []map[string]interface{}{{"name": m.Name, "monster_key": key, "initiative": 1}}}
	// an import is private to its GM until it is shared (owner, Nov 22)
	if c, j := pgCall(t, "GET", path, other, nil); c != 200 || len(j["monsters"].([]interface{})) != 0 {
		t.Fatalf("private import listed for another GM %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", otherCamp), other, add); c != 404 {
		t.Fatalf("another GM used a private import %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", path+"/share", gm, map[string]interface{}{"monster_key": key, "shared": true}); c != 200 {
		t.Fatalf("share %d %v", c, j)
	}
	if c, j := pgCall(t, "GET", path, other, nil); c != 200 || len(j["monsters"].([]interface{})) != 1 {
		t.Fatalf("shared GM read %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", otherCamp), other, add); c != 200 {
		t.Fatalf("shared campaign combat %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, add); c != 200 {
		t.Fatalf("combat add %d %v", c, j)
	}
	f, err := readFight(db, camp)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range f.Order {
		if e["monster_key"] == key {
			found = true
			if asInt(e["hp"]) != m.HP || asInt(e["ac"]) != m.AC {
				t.Fatalf("combat defaults %v", e)
			}
		}
	}
	if !found {
		t.Fatal("custom monster missing from fight")
	}

	// Exercise an imported attack through the normal GM narration endpoint.
	c, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Compendium target %d", camp)))
	if c != 200 {
		t.Fatalf("target %d %v", c, j)
	}
	char := int(j["character_id"].(float64))
	pgExec(t, `UPDATE characters SET lobby_id=$1,hp=1000,max_hp=1000,ac=1 WHERE id=$2`, camp, char)
	attack := ""
	for _, a := range m.Actions {
		if atkBonusRe.MatchString(a.Text) {
			attack = a.Name
			break
		}
	}
	if attack == "" {
		t.Fatal("fixture has no attack")
	}
	hit := false
	for i := 0; i < 20; i++ {
		c, j = pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "custom: is allowed in narration prose. The creature strikes.", "monster_action": map[string]interface{}{"monster": m.Name, "action": attack, "target": fmt.Sprint(char)}})
		if c != 200 {
			t.Fatalf("attack %d %v", c, j)
		}
		var hp int
		db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, char).Scan(&hp)
		if hp < 1000 {
			hit = true
			break
		}
	}
	if !hit {
		t.Fatal("imported attack never applied damage")
	}

	// A revised import has a new key, leaving existing stats stable, even with an SRD name.
	srd := srdMonsters2024()[0]
	srdHP := monsterStatBlock(monsterSlug(srd.Name))["hp"]
	m.Name = srd.Name
	m.HP++
	body["monsters"] = []srdMonster2024{m}
	c, j = pgCall(t, "POST", path, gm, body)
	if c != 201 {
		t.Fatalf("revision %d %v", c, j)
	}
	key2 := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	if key2 == key || monsterStatBlock(key)["hp"] == m.HP {
		t.Fatal("revision changed old entry")
	}
	if monsterStatBlock(monsterSlug(srd.Name))["hp"] != srdHP {
		t.Fatal("custom name overwrote SRD")
	}
	// Pagination is stable and never mixes a different campaign's entries.
	pgExec(t, `INSERT INTO custom_monsters(key,definition,stats,source_note)
 SELECT 'custom:page-' || $1::text || '-' || n::text,definition,stats,source_note
 FROM custom_monsters CROSS JOIN generate_series(1,100) n WHERE key=$2`, camp, key)
	c, j = pgCall(t, "GET", fmt.Sprintf("%s?campaign_id=%d", path, camp), gm, nil)
	if c != 200 || len(j["monsters"].([]interface{})) != 100 || j["next_after"] == "" {
		t.Fatalf("first page %d %v", c, j)
	}
	afterKey := j["next_after"].(string)
	c, j = pgCall(t, "GET", fmt.Sprintf("%s?campaign_id=%d&after=%s", path, camp, afterKey), gm, nil)
	if c != 200 || len(j["monsters"].([]interface{})) != 2 || j["next_after"] != "" {
		t.Fatalf("last page %d %v", c, j)
	}
	c, j = pgCall(t, "GET", fmt.Sprintf("%s?campaign_id=%d", path, otherCamp), other, nil)
	if c != 200 || len(j["monsters"].([]interface{})) != 100 {
		t.Fatalf("shared list %d %v", c, j)
	}
}
