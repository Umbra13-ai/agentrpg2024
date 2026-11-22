package main

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Rules the spell sweep (Part B, stage 3) brought to their owners.

// A spell whose dice are extra damage on the caster's later hits is read from its own text (laterHitDamageRe): the cast deals none and says so;
// an owner (Hex, Hunter's Mark: hitRiderRules; a buff's effect row) keeps its own words, any other tells the player the server doesn't add it.
func TestLaterHitDamageSpellsAreReadFromTheirText(t *testing.T) {
	testPostgres(t)
	var got []string
	for slug, sp := range srdSpellsMemory {
		if !laterHitDamageRe.MatchString(sp.Description) {
			if castDamageOnLaterHits(slug) {
				t.Errorf("%s: castDamageOnLaterHits disagrees with the pattern", slug)
			}
			continue
		}
		got = append(got, slug)
		if !castDealsNoDamage(slug) {
			t.Errorf("%s: its dice ride on later hits, but the cast deals them", slug)
		}
		_, buff := buffSpells[slug]
		note := laterHitDamageNote(slug)
		if isMarkSpell(slug) || buff {
			if note != "" {
				t.Errorf("%s has an owner for its extra damage but the cast says the server doesn't add it", slug)
			}
		} else if !strings.Contains(note, "doesn't add this extra damage") {
			t.Errorf("%s: no owner adds its extra damage and the cast doesn't say so", slug)
		}
	}
	sort.Strings(got)
	want := []string{"conjure-minor-elementals", "divine-smite", "hex", "hunters-mark", "searing-smite", "shining-smite"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("the spells that deal extra damage on later hits (SRD 5.2.1) are %v; the test knows %v: check each has an owner or a note", got, want)
	}
}

// Magic Missile (no attack roll, no save) aimed at characters: the darts are shared out round-robin over the characters named (dartShares, the owner a
// monster's cast uses too), each dart a hit of 1d4+1 Force, and the damage is dealt. The cast text no longer tells damage it did not deal.
func TestPostgresMagicMissileAtCharacters(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "mm gm")
	_, player := pgAgent(t, "mm player")
	_, ally1 := pgAgent(t, "mm ally one")
	_, ally2 := pgAgent(t, "mm ally two")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Magic missile", "max_players": 4})
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	caster := buildClassCharacter(t, gm, player, camp, "wizard", "sage", "gnome")
	friends := []int{}
	for i, tok := range []string{ally1, ally2} {
		_, aj := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Mm Ally %d %d", i, camp)))
		id := int(aj["character_id"].(float64))
		pgCall(t, "POST", cp+"/join", tok, map[string]interface{}{"character_id": id})
		pgFinish(t, tok, id)
		pgCall(t, "POST", cp+"/ready", tok, nil)
		friends = append(friends, id)
	}
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	if c, j := pgCall(t, "POST", cp+"/start", gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	pgExec(t, `UPDATE characters SET prepared_spells='["magic-missile"]'::jsonb, known_spells='["magic-missile"]'::jsonb WHERE id=$1`, caster)
	pgExec(t, `UPDATE characters SET level=3 WHERE id=$1`, caster) // level 2 slots for the upcast
	hp := func(id int) int {
		var n int
		db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, id).Scan(&n)
		return n
	}
	for _, c := range []struct {
		name    string
		targets []int
		slot    int
		darts   []int // darts each target is dealt, in the order named
	}{
		{"one character takes all three darts", friends[:1], 1, []int{3}},
		{"two characters share them round-robin", friends, 1, []int{2, 1}},
		{"an upcast adds a dart", friends[:1], 2, []int{4}},
	} {
		for _, id := range friends {
			pgExec(t, `UPDATE characters SET hp=1000, max_hp=1000, temp_hp=0, is_dead=false WHERE id=$1`, id)
		}
		pgExec(t, `UPDATE characters SET spell_slots_used='{}'::jsonb WHERE id=$1`, caster)
		pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "The scene moves on."}) // a new exploration beat
		before := map[int]int{}
		for _, id := range c.targets {
			before[id] = hp(id)
		}
		body := map[string]interface{}{"action": "cast", "character_id": caster, "spell_slug": "magic-missile", "slot_level": c.slot, "target_ids": c.targets}
		code, r := pgCall(t, "POST", "/api/action", player, body)
		if code != 200 {
			t.Fatalf("%s: %d %v", c.name, code, r)
		}
		for i, id := range c.targets {
			lost := before[id] - hp(id)
			lo, hi := 2*c.darts[i], 5*c.darts[i] // each dart is 1d4+1
			if lost < lo || lost > hi {
				t.Errorf("%s: character %d lost %d Hit Points, %d dart(s) deal %d to %d: %v", c.name, id, lost, c.darts[i], lo, hi, r["result"])
			}
		}
		if res := fmt.Sprint(r["result"]); strings.Contains(res, "force damage. You create") {
			t.Errorf("%s: the cast still claims damage up front: %s", c.name, res)
		}
	}
}
