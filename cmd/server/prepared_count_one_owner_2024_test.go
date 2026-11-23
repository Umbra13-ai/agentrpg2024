package main

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// Playtest S-GM-15: the sheet listed a Magic Initiate spell inside prepared_spells while the setup page and the prepare request
// counted without it, so "prepared now" and the list's length disagreed. preparedChosen / preparedCount (prepared_spells_2024.go)
// are the one answer: the sheet, the prepare request and to_finish list and count what the character chose, and an origin spell is
// listed under origin_spells only.
func TestPostgresPreparedListAndCountAgreeEverywhere(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "pg prepared count")
	_, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Prep %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgFinish(t, tok, char)

	// a Wizard with a Magic Initiate spell (Shield, always prepared) and three spells it chose
	var raw []byte
	db.QueryRow(`SELECT COALESCE(origin,'{}') FROM characters WHERE id = $1`, char).Scan(&raw)
	rec, ok := loadOriginRecord(raw)
	if !ok {
		t.Fatalf("no origin record: %s", raw)
	}
	rec.Feats = append(rec.Feats, originFeat{Slug: "magic_initiate", Cantrips: []string{"light", "mage-hand"}, Spell: "shield", Ability: "int"})
	origin, _ := json.Marshal(rec)
	pgExec(t, `UPDATE characters SET class = 'wizard', level = 3, intl = 16, origin = $2,
		known_spells = '["magic-missile","mage-armor","sleep","shield","fire-bolt"]',
		prepared_spells = '["shield","magic-missile","mage-armor","sleep","fire-bolt"]' WHERE id = $1`, char, string(origin))

	_, sheet := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d", char), tok, nil)
	_, prep := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/prepare", char), tok, nil)
	listed := func(m map[string]interface{}) (leveled int, slugs []string) {
		for _, e := range m["prepared_spells"].([]interface{}) {
			sp := e.(map[string]interface{})
			slugs = append(slugs, fmt.Sprint(sp["slug"]))
			if lv, _ := sp["level"].(float64); lv > 0 {
				leveled++
			}
		}
		return
	}
	for name, m := range map[string]map[string]interface{}{"the sheet": sheet, "GET /prepare": prep} {
		n, slugs := listed(m)
		for _, s := range slugs {
			if s == "shield" {
				t.Errorf("%s lists the origin spell Shield in prepared_spells: %v", name, slugs)
			}
		}
		if c := int(m["prepared_count"].(float64)); c != n || n != 3 {
			t.Errorf("%s: prepared_count %d, %d leveled spells listed (%v); want 3 of each", name, c, n, slugs)
		}
		if left := int(m["preparations_left"].(float64)); left != int(m["max_prepared"].(float64))-3 {
			t.Errorf("%s: preparations_left %d with max_prepared %v", name, left, m["max_prepared"])
		}
	}
}
