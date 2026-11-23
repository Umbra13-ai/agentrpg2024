package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// The GM's update-character and the admin page set a character's Hit Points through the Hit Point owner (editVitals, editVitalsIn):
// hp 0 takes a character to 0 (a playtest GM's set {hp: 0} did nothing), a dead character never keeps Hit Points (a raw edit left one
// dead at 68 with a maximum of 0), the maximum is at least 1, and a refusal changes nothing. Both editors, every Hit Point field.
func TestPostgresEditorsSetHitPointsThroughTheOwner(t *testing.T) {
	testPostgres(t)
	const key = "test-admin-key-for-vitals"
	t.Setenv("ADMIN_KEY", key)
	admin := func(body interface{}, id int) (int, map[string]interface{}) {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", fmt.Sprintf("/api/admin/characters/%d", id), bytes.NewReader(b))
		r.RemoteAddr = "10.77.3.1:1234"
		r.Header.Set("X-Admin-Key", key)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		newServer("").Handler.ServeHTTP(w, r)
		out := map[string]interface{}{}
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	gm, _, camp, chars, _ := pgFightWith(t, "vitals edit", 1)
	char := chars[0]
	vitals := func() (hp, max, temp, fails int, stable, dead bool) {
		db.QueryRow(`SELECT hp, max_hp, COALESCE(temp_hp,0), COALESCE(death_save_failures,0), COALESCE(is_stable,false), COALESCE(is_dead,false) FROM characters WHERE id = $1`, char).
			Scan(&hp, &max, &temp, &fails, &stable, &dead)
		return
	}
	gmSet := func(set map[string]interface{}) (int, map[string]interface{}) {
		return pgCall(t, "POST", "/api/gm/update-character", gm, map[string]interface{}{"campaign_id": camp, "character_id": char, "set": set})
	}
	editors := []struct {
		name string
		set  func(map[string]interface{}) (int, map[string]interface{})
	}{
		{"the GM", gmSet},
		{"the admin", func(set map[string]interface{}) (int, map[string]interface{}) {
			return admin(map[string]interface{}{"set": set}, char)
		}},
	}
	for _, ed := range editors {
		pgExec(t, `UPDATE characters SET max_hp = 40, hp = 40, temp_hp = 0, is_dead = false, is_stable = false, death_save_failures = 0 WHERE id = $1`, char)
		if c, j := ed.set(map[string]interface{}{"hp": 0}); c != 200 {
			t.Fatalf("%s: hp 0: %d %v", ed.name, c, j)
		}
		if hp, _, _, _, _, dead := vitals(); hp != 0 || dead {
			t.Errorf("%s: hp 0 left hp %d dead %v", ed.name, hp, dead)
		}
		if c, j := ed.set(map[string]interface{}{"death_save_failures": 2}); c != 200 {
			t.Errorf("%s: saves at 0: %d %v", ed.name, c, j)
		}
		if c, _ := ed.set(map[string]interface{}{"is_dead": true}); c != 200 {
			t.Errorf("%s: is_dead true: %d", ed.name, c)
		}
		// a dead character given Hit Points without being brought back: refused, nothing changed
		if c, j := ed.set(map[string]interface{}{"hp": 20, "max_hp": 50}); c != 400 || !strings.Contains(fmt.Sprint(j), "is_dead false") {
			t.Errorf("%s: hp for a dead character: %d %v", ed.name, c, j)
		}
		if hp, max, _, _, _, dead := vitals(); hp != 0 || max != 40 || !dead {
			t.Errorf("%s: the refused edit changed hp %d max %d dead %v", ed.name, hp, max, dead)
		}
		if c, j := ed.set(map[string]interface{}{"max_hp": 0}); c != 400 {
			t.Errorf("%s: a maximum of 0: %d %v", ed.name, c, j)
		}
		// brought back
		if c, j := ed.set(map[string]interface{}{"is_dead": false, "hp": 12}); c != 200 {
			t.Errorf("%s: brought back: %d %v", ed.name, c, j)
		}
		if hp, _, _, fails, stable, dead := vitals(); hp != 12 || dead || fails != 0 || stable {
			t.Errorf("%s: brought back: hp %d dead %v fails %d stable %v", ed.name, hp, dead, fails, stable)
		}
		// a row written by a raw edit before the rule: hp 0 repairs it
		pgExec(t, `UPDATE characters SET is_dead = true, hp = 68, max_hp = 1 WHERE id = $1`, char)
		if c, j := ed.set(map[string]interface{}{"hp": 0, "max_hp": 68}); c != 200 {
			t.Errorf("%s: repairing a dead row at 68: %d %v", ed.name, c, j)
		}
		if hp, max, _, _, _, dead := vitals(); hp != 0 || max != 68 || !dead {
			t.Errorf("%s: the dead row: hp %d max %d dead %v", ed.name, hp, max, dead)
		}
	}
	// the GM's shortcuts go the same way
	pgExec(t, `UPDATE characters SET max_hp = 40, hp = 40, is_dead = false WHERE id = $1`, char)
	if c, j := pgCall(t, "POST", "/api/gm/update-character", gm, map[string]interface{}{"campaign_id": camp, "character_id": char, "max_hp": 30, "hp": 0}); c != 200 {
		t.Errorf("the shortcuts: %d %v", c, j)
	}
	if hp, max, _, _, _, _ := vitals(); hp != 0 || max != 30 {
		t.Errorf("the shortcuts: hp %d max %d", hp, max)
	}
}
