package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Feedback log 63 and 64 (I-KESTREL-3, I-KESTREL-4): Hex on a monster, then Eldritch Blast at it, dealt no Necrotic; and a beam
// spell's reply showed no roll. The riders of a hit are one table (hitRiders), asked by every character attack after a hit; each
// beam is said by the attack owner.

// hitRidersFight: a campaign in a fight, one character and the monsters given; returns the character, its token and the
// monsters' combat ids by name.
func hitRidersFight(t *testing.T, monsters ...map[string]interface{}) (int, string, map[string]int) {
	t.Helper()
	_, gm := pgAgent(t, "pg riders gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Riders", "max_players": 2})
	lobby := int(j["campaign_id"].(float64))
	pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/schedule", lobby), gm, map[string]interface{}{"state": "off"})
	_, tok := pgAgent(t, "pg riders player")
	_, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Hexer %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", lobby), tok, map[string]interface{}{"character_id": char})
	pgFinish(t, tok, char)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), tok, nil)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), gm, nil)
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", lobby), gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/start", lobby), gm, map[string]interface{}{}); c != 200 {
		t.Fatalf("combat start: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", lobby), gm, map[string]interface{}{"combatants": monsters}); c != 200 {
		t.Fatalf("add: %d %v", c, j)
	}
	ids := map[string]int{}
	for _, e := range combatEntries(lobby) {
		if id := asInt(e["id"]); id < 0 {
			ids[fmt.Sprint(e["name"])] = id
		}
	}
	return char, tok, ids
}

func TestPostgresHitRiders(t *testing.T) {
	testPostgres(t)
	char, tok, ids := hitRidersFight(t,
		map[string]interface{}{"name": "Big Ogre", "monster_key": "ogre", "hp": 900},
		map[string]interface{}{"name": "Cold Shadow", "monster_key": "shadow", "hp": 900})
	ogre, shadow := ids["Big Ogre"], ids["Cold Shadow"]
	lobby := lobbyOfChar(char)
	conc := func(v string) { pgExec(t, `UPDATE characters SET concentrating_on = $1 WHERE id = $2`, v, char) }

	// the table: Hex and Hunter's Mark apply to every kind of attack roll, in their own type, only on the creature they are on
	conc(fmt.Sprintf("Hex:%d", ogre))
	for _, a := range []hitAttack{{weapon: "Dagger"}, {unarmed: true}, {spell: true}, {item: true}} {
		_, text, hits := hitRiderDamage(hitInfo{lobby: lobby, char: char, target: ogre, monsterKey: "ogre", attack: a})
		if len(hits) != 1 || hits[0].dtype != "necrotic" || !strings.Contains(text, "Hex: +") {
			t.Errorf("Hex on a hit by %+v: %v %q", a, hits, text)
		}
		if _, _, hits := hitRiderDamage(hitInfo{lobby: lobby, char: char, target: shadow, monsterKey: "shadow", attack: a}); len(hits) != 0 {
			t.Errorf("Hex on another creature added damage: %v", hits)
		}
	}
	conc(fmt.Sprintf("Hex:%d", shadow)) // a Shadow is immune to Necrotic damage
	if total, text, _ := hitRiderDamage(hitInfo{lobby: lobby, char: char, target: shadow, monsterKey: "shadow", attack: hitAttack{spell: true}}); total != 0 || !strings.Contains(text, "immune") {
		t.Errorf("Hex on a creature immune to Necrotic: %d %q", total, text)
	}
	conc(fmt.Sprintf("Hunter's Mark:%d", ogre))
	if _, _, hits := hitRiderDamage(hitInfo{lobby: lobby, char: char, target: ogre, monsterKey: "ogre", attack: hitAttack{spell: true}}); len(hits) != 1 || hits[0].dtype != "force" {
		t.Errorf("Hunter's Mark on a spell attack: %v", hits)
	}
	// Divine Favor is a weapon attack's only
	pgExec(t, `UPDATE characters SET concentrating_on = NULL WHERE id = $1`, char)
	addSpellEffect(lobby, char, char, "Divine Favor", "divine_favor", "", 999999, "", 0)
	for _, c := range []struct {
		a    hitAttack
		want bool
	}{{hitAttack{weapon: "Dagger"}, true}, {hitAttack{spell: true}, false}, {hitAttack{item: true}, false}} {
		_, _, hits := hitRiderDamage(hitInfo{lobby: lobby, char: char, target: ogre, monsterKey: "ogre", attack: c.a})
		if (len(hits) == 1) != c.want {
			t.Errorf("Divine Favor on %+v: %v", c.a, hits)
		}
	}
	pgExec(t, `DELETE FROM spell_effects WHERE target_id = $1 AND kind = 'divine_favor'`, char)

	// through the server: a weapon attack at the hexed ogre says the Hex damage
	conc(fmt.Sprintf("Hex:%d", ogre))
	pgExec(t, `UPDATE characters SET equipped_main_hand = 'dagger' WHERE id = $1`, char)
	hexed := false
	for i := 0; i < 12 && !hexed; i++ {
		pgExec(t, `UPDATE characters SET action_used = false, bonus_action_used = false, attacks_remaining = NULL, sneak_attack_used = false WHERE id = $1`, char)
		_, j := pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"character_id": char, "action": "attack", "description": "dagger", "target_id": ogre})
		res := fmt.Sprint(j["result"])
		if strings.Contains(res, "HIT") {
			if !strings.Contains(res, "Hex: +") || !strings.Contains(res, "Necrotic") {
				t.Fatalf("a hit on the hexed ogre: %s", res)
			}
			hexed = true
		}
	}
	if !hexed {
		t.Fatal("twelve dagger attacks without a hit")
	}

	// Eldritch Blast: each beam is its own line (d20, total), and each hit deals Hex's Necrotic
	pgExec(t, `UPDATE characters SET class = 'warlock', level = 11, cha = 20, known_spells = '["eldritch-blast"]' WHERE id = $1`, char)
	beamHit := false
	for i := 0; i < 6 && !beamHit; i++ {
		pgExec(t, `UPDATE characters SET action_used = false, bonus_action_used = false WHERE id = $1`, char)
		c, j := pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"character_id": char, "action": "cast", "spell_slug": "eldritch-blast", "target_id": ogre})
		res := fmt.Sprint(j["result"])
		if c != 200 || !strings.Contains(res, "Beam 1:") || !strings.Contains(res, "Beam 3:") || !strings.Contains(res, "d20") {
			t.Fatalf("Eldritch Blast at level 11: %d %s %v", c, res, j)
		}
		if strings.Contains(res, " of 3 hit") {
			if !strings.Contains(res, "Hex: +") {
				t.Fatalf("a beam hit the hexed ogre without Hex: %s", res)
			}
			beamHit = true
		}
	}
	if !beamHit {
		t.Fatal("six Eldritch Blasts without a hit")
	}
}
