package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Every SRD stat block entry with a replacement sentence ("It can replace one attack with a use of (A) Sleep Breath or (B)
// Spellcasting to cast Scorching Ray") is read by replaceRuleOf: each option is an entry of that monster or a spell, what it
// replaces is one of the monster's attacks (or any attack, or a spell the entry casts), and the GM's stat block lists them.
func TestPostgresReplaceRulesReadEverySRDEntry(t *testing.T) {
	testPostgres(t) // the SRD spells, which tell a spell option from an entry
	n := 0
	for _, m := range srdMonsters2024() {
		meta := monsterMetaFor(monsterSlug(m.Name))
		if meta == nil {
			continue
		}
		for _, group := range [][]srdMonsterEntry{meta.Actions, meta.BonusActions, meta.Reactions, meta.Legendary} {
			for _, e := range group {
				if !strings.Contains(e.Text, "can replace") {
					continue
				}
				rule, ok := replaceRuleOf(e.Text)
				if !ok {
					t.Errorf("%s %s: the replacement sentence isn't read: %s", m.Name, e.Name, e.Text)
					continue
				}
				n++
				for _, o := range rule.Options {
					if o.Entry != "" {
						if _, _, ok := findMonsterEntry(meta, o.Entry); !ok {
							t.Errorf("%s %s: option %q names no entry of the stat block", m.Name, e.Name, o.Entry)
						}
					}
					if o.Spell != "" {
						if _, ok := srdSpellsMemory[normalizeSpellSlug(o.Spell)]; !ok {
							t.Errorf("%s %s: option %q names no SRD spell", m.Name, e.Name, o.Spell)
						}
					}
				}
				if rule.Replaced != "" && !strings.Contains(e.Text, rule.Replaced) {
					t.Errorf("%s %s: replaces %q, which the text doesn't name", m.Name, e.Name, rule.Replaced)
				}
				if v := replaceChoicesView(meta); len(v) == 0 {
					t.Errorf("%s: the GM's stat block lists no choices", m.Name)
				}
			}
		}
	}
	if n < 40 {
		t.Fatalf("only %d entries with a replacement sentence read", n)
	}
	// the examples: options, counts and what is replaced
	for text, want := range map[string]string{
		"The dragon makes three Rend attacks. It can replace one attack with a use of (A) Sleep Breath or (B) Spellcasting to cast Scorching Ray (level 3 version).": `1 "" [Sleep Breath/Sleep Breath/ Scorching Ray/Spellcasting/Scorching Ray3]`,
		"The dragon makes three Rend attacks. It can replace two attacks with a use of Sleep Breath.":                                                                `2 "" [Sleep Breath/Sleep Breath/]`,
		"The pit fiend casts Fireball (level 5 version) twice. It can replace one Fireball with Hold Monster (level 7 version) or Wall of Fire.":                     `1 "Fireball" [Hold Monster//Hold Monster7 Wall of Fire//Wall of Fire]`,
		"The chimera makes one Ram attack, one Bite attack, and one Claw attack. It can replace the Claw attack with a use of Fire Breath if available.":             `1 "Claw" [Fire Breath/Fire Breath/]`,
	} {
		rule, _ := replaceRuleOf(text)
		var opts []string
		for _, o := range rule.Options {
			s := o.Name + "/" + o.Entry + "/" + o.Spell
			if o.Level > 0 {
				s += fmt.Sprint(o.Level)
			}
			opts = append(opts, s)
		}
		if got := fmt.Sprintf("%d %q %v", rule.Count, rule.Replaced, opts); got != want {
			t.Errorf("%s\n got %s\nwant %s", text, got, want)
		}
	}
}

// Feedback log 69 (I-GM-20, I-GM-21): narrate runs the monster action first, and a refused or unknown one posts nothing, the
// narration included; an entry's replacement option is taken with "use" (the Pit Fiend's Hold Monster in place of one Fireball, a
// Young Brass Dragon's Sleep Breath in place of two Rends) and any other word is refused before anything is rolled.
func TestPostgresNarrateRunsTheMonsterActionFirst(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "pg narrate first gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Narrate First", "max_players": 2})
	lobby := int(j["campaign_id"].(float64))
	pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/schedule", lobby), gm, map[string]interface{}{"state": "off"})
	_, tok := pgAgent(t, "pg narrate first player")
	_, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Held %d", time.Now().UnixNano()%1e9)))
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
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", lobby), gm, map[string]interface{}{
		"combatants": []map[string]interface{}{{"name": "Test Fiend", "monster_key": "pit-fiend"}, {"name": "Test Wyrmling", "monster_key": "young-brass-dragon"}}}); c != 200 {
		t.Fatalf("add: %d %v", c, j)
	}
	pgExec(t, `UPDATE characters SET max_hp = 900, hp = 900 WHERE id = $1`, char)
	narrations := func(text string) int {
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM actions WHERE lobby_id = $1 AND action_type = 'narration' AND description = $2`, lobby, text).Scan(&n)
		return n
	}
	narrate := func(text string, ma map[string]interface{}) (int, map[string]interface{}) {
		ma["target"] = fmt.Sprint(char)
		return pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": lobby, "narration": text, "monster_action": ma})
	}

	// a use the entry doesn't offer: refused, nothing recorded, nothing spent
	if c, j := narrate("Hellfire, take one.", map[string]interface{}{"monster": "Test Fiend", "action": "Hellfire Spellcasting", "use": "Teleport"}); c != 409 ||
		j["error"] != "monster_action_refused" || j["narration_recorded"] != false || !strings.Contains(fmt.Sprint(j["message"]), "Hold Monster") {
		t.Errorf("a use Hellfire doesn't offer: %d %v", c, j)
	}
	if narrations("Hellfire, take one.") != 0 {
		t.Error("the refused action's narration was posted")
	}
	// Hold Monster in place of one Fireball
	c, j := narrate("Hellfire, take two.", map[string]interface{}{"monster": "Test Fiend", "action": "Hellfire Spellcasting", "use": "hold monster"})
	res := fmt.Sprint(j["monster_action_result"])
	if c != 200 || !strings.Contains(res, "Hold Monster") || strings.Count(res, "Fireball") == 0 || j["narration_recorded"] != true {
		t.Fatalf("Hellfire with Hold Monster: %d %v", c, j)
	}
	if narrations("Hellfire, take two.") != 1 {
		t.Error("the narration wasn't posted once")
	}
	// spent: the next one is refused and posts nothing
	if c, j := narrate("Hellfire, take three.", map[string]interface{}{"monster": "Test Fiend", "action": "Hellfire Spellcasting"}); c != 409 || narrations("Hellfire, take three.") != 0 {
		t.Errorf("Hellfire not recharged: %d %v", c, j)
	}
	// an action the stat block doesn't have: 400, with the actions it has, nothing posted
	if c, j := narrate("The fiend dances.", map[string]interface{}{"monster": "Test Fiend", "action": "Dance"}); c != 400 || j["error"] != "unknown_monster_action" ||
		j["monster_actions"] == nil || narrations("The fiend dances.") != 0 {
		t.Errorf("an unknown action: %d %v", c, j)
	}
	// a Multiattack with its option: one Rend and Sleep Breath (in place of two)
	pgExec(t, `UPDATE characters SET conditions = '[]' WHERE id = $1`, char)
	c, j = narrate("The wyrmling breathes.", map[string]interface{}{"monster": "Test Wyrmling", "action": "Multiattack", "use": "Sleep Breath"})
	res = fmt.Sprint(j["monster_action_result"])
	if c != 200 || !strings.Contains(res, "Sleep Breath") || !strings.Contains(res, "Constitution") || strings.Count(res, "Rend") != 1 {
		t.Errorf("Multiattack with Sleep Breath: %d %s", c, res)
	}
	if c, j := narrate("The wyrmling roars.", map[string]interface{}{"monster": "Test Wyrmling", "action": "Multiattack", "use": "Fire Breath"}); c != 409 || narrations("The wyrmling roars.") != 0 {
		t.Errorf("a Multiattack use it doesn't offer: %d %v", c, j)
	}
}
