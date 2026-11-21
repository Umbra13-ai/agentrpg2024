package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

// TestPostgresBookRouteSweep plays every stat block in MM_FIXTURES_DIR through the real routes (compendium import, combat/add,
// POST /api/gm/narrate monster_action), so "route exercised" is measured separately from "imported" and "automated".
// For each monster it checks the stats the combat holds against the import, then makes each attack-roll action hit a very
// weak target (AC 1, 1000 HP) until a HIT is reported (up to 25 tries) and requires the Hit Points to drop. An action the
// shared rules refuse must say so ("GM"/"Nothing was rolled") and change nothing. MM_ROUTE_OUT receives a Markdown report.
func TestPostgresBookRouteSweep(t *testing.T) {
	testPostgres(t)
	dir := os.Getenv("MM_FIXTURES_DIR")
	if dir == "" {
		t.Skip("MM_FIXTURES_DIR not set")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(files)
	_, gm := pgAgent(t, "book sweep gm")
	_, player := pgAgent(t, "book sweep player")
	call := func(method, path, token string, body interface{}) map[string]interface{} {
		t.Helper()
		c, j := pgCall(t, method, path, token, body)
		if c != 200 && c != 201 {
			t.Fatalf("%s %s %d %v", method, path, c, j)
		}
		return j
	}
	campaign := int(call("POST", "/api/campaigns", gm, map[string]interface{}{"name": "Book route sweep", "max_players": 2})["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", campaign)
	call("PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	character := int(call("POST", "/api/characters", player, pgRogue(fmt.Sprintf("Sweep Hero %d", time.Now().UnixNano()%1e9)))["character_id"].(float64))
	call("POST", cp+"/join", player, map[string]interface{}{"character_id": character})
	pgFinish(t, player, character)
	call("POST", cp+"/ready", player, nil)
	call("POST", cp+"/ready", gm, nil)
	call("POST", cp+"/start", gm, nil)
	var rows []string
	var played, hits, refusedOK, monsters, riderChecked int
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			SourceNote string           `json:"source_note"`
			Monsters   []srdMonster2024 `json:"monsters"`
		}
		if json.Unmarshal(raw, &body) != nil || len(body.Monsters) != 1 {
			t.Fatalf("%s: unreadable", f)
		}
		m := body.Monsters[0]
		imported := call("POST", "/api/gm/compendium/monsters", gm, body)
		key := imported["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
		call("POST", cp+"/combat/start", gm, map[string]interface{}{})
		call("POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": m.Name, "monster_key": key, "initiative": 20}}})
		fight, err := readFight(db, campaign)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range fight.Order {
			if e["monster_key"] == key {
				found = true
				if asInt(e["hp"]) != m.HP || asInt(e["ac"]) != m.AC {
					t.Errorf("%s: combat holds hp %v ac %v, import has %d / %d", m.Name, e["hp"], e["ac"], m.HP, m.AC)
				}
			}
		}
		if !found {
			t.Errorf("%s: not in the fight", m.Name)
		}
		monsters++
		var notes []string
		for _, e := range m.Actions {
			if !strings.Contains(e.Text, "Attack Roll:") {
				continue
			}
			played++
			use := "fire"
			if c := parseMonsterAttack(e.Name, e.Text).Choices; len(c) > 0 {
				use = c[0] // the GM picks one of the listed damage types
			}
			result, dmg := "", 0
			for i := 0; i < 25; i++ {
				pgExec(t, `UPDATE characters SET hp=1000,ac=1 WHERE id=$1`, character)
				removeConditionsWhere(campaign, character, func(conditionRecord) bool { return true }) // an earlier hit's Stunned or Prone would give this one Advantage and a critical hit
				r := call("POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": campaign, "narration": "The creature attacks.",
					"monster_action": map[string]interface{}{"monster": m.Name, "action": e.Name, "target": fmt.Sprint(character), "use": use}})
				result = fmt.Sprint(r["monster_action_result"])
				var hp int
				db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, character).Scan(&hp)
				dmg = 1000 - hp
				if strings.Contains(result, "HIT") || strings.Contains(result, "Nothing was rolled") {
					break
				}
			}
			switch {
			case strings.Contains(result, "Nothing was rolled"):
				refusedOK++
				if dmg != 0 {
					t.Errorf("%s / %s: refused but target lost %d HP", m.Name, e.Name, dmg)
				}
				notes = append(notes, e.Name+": refused")
			case strings.Contains(result, "HIT"):
				hits++
				if dmg <= 0 {
					t.Errorf("%s / %s: HIT without damage: %s", m.Name, e.Name, result)
				}
				checkHitSemantics(t, m.Name, e, use, result, dmg)
				if rider, ok := parseHitRider(e.Text); ok { // the conditions the hit gives are stored (or the reply says why not), the rest of the text is the GM's
					recs, _ := readConditionRecords(db, campaign, character)
					for _, c := range rider.Conditions {
						stored := false
						for _, r := range recs {
							stored = stored || r.Condition == c
						}
						if !stored && !strings.Contains(result, "not "+c) && !strings.Contains(result, "not "+strings.Join(rider.Conditions, ", ")) {
							t.Errorf("%s / %s: the hit gives %s but no record is stored and the reply does not say why: %s", m.Name, e.Name, c, result)
						}
						riderChecked++
					}
					if rider.Extra != "" && !strings.Contains(result, "GM, not applied automatically") {
						t.Errorf("%s / %s: hit text left for the GM is not in the reply (%q): %s", m.Name, e.Name, rider.Extra, result)
					}
				}
				notes = append(notes, fmt.Sprintf("%s: hit for %d", e.Name, dmg))
			default:
				t.Errorf("%s / %s: no HIT in 25 tries at AC 1: %s", m.Name, e.Name, result)
				notes = append(notes, e.Name+": NO HIT")
			}
		}
		rows = append(rows, fmt.Sprintf("| %s | CR %s | %s |", m.Name, m.CR, strings.Join(notes, "; ")))
		call("POST", cp+"/combat/end", gm, nil)
	}
	summary := fmt.Sprintf("%d monsters imported and placed in a fight with matching stats; %d attack-roll actions played through POST /api/gm/narrate: %d hit and dealt damage, %d refused with nothing rolled; %d hit-rider conditions checked as stored (or explained).", monsters, played, hits, refusedOK, riderChecked)
	t.Log(summary)
	if out := os.Getenv("MM_ROUTE_OUT"); out != "" {
		body := "# Route sweep report\n\n" + summary + "\n\nOnly attack-roll actions are played. Saving-throw actions, spellcasting, traits and legendary actions are not exercised here.\n\n| Monster | CR | Attack actions |\n|---|---|---|\n" + strings.Join(rows, "\n") + "\n"
		if err := os.WriteFile(out, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// checkHitSemantics: what the HIT must contain beyond "some damage". The parsed attack lists its damage components; the damage
// dealt must lie between the smallest and the largest total those components can make (dice doubled on a critical hit; the
// conditional parts counted only in the maximum), and the reply must name each component's damage type.
func checkHitSemantics(t *testing.T, monster string, e srdMonsterEntry, use, result string, dealt int) {
	t.Helper()
	p := parseMonsterAttack(e.Name, e.Text)
	type comp struct {
		dice, typ string
		flat      *int
		optional  bool
	}
	var comps []comp
	if p.DamageDice != "" || p.FlatDamage != nil {
		typ := p.DamageType
		if len(p.Choices) > 0 {
			typ = use
		}
		comps = append(comps, comp{p.DamageDice, typ, p.FlatDamage, false})
	}
	for _, x := range p.Extras {
		typ := x.Type
		if typ == chosenTypeWord {
			typ = use
		}
		comps = append(comps, comp{x.Dice, typ, x.Flat, x.IfAdvantage})
	}
	lo, hi := 0, 0
	lowered := strings.ToLower(result)
	for _, c := range comps {
		cmin, cmax := 0, 0
		if c.flat != nil {
			cmin, cmax = *c.flat, *c.flat
		} else {
			n, sides := game.ParseDice(c.dice)
			f := game.FlatBonus(c.dice)
			cmin, cmax = max(0, n+f), 2*n*sides+f
		}
		if strings.EqualFold(c.typ, "poison") {
			cmin /= 2 // the sweep character is a Dwarf: poison damage is halved
		}
		if !c.optional {
			lo += cmin
		}
		hi += cmax
		if c.typ != "" && !strings.Contains(lowered, strings.ToLower(c.typ)) {
			t.Errorf("%s / %s: the reply never names %s damage: %s", monster, e.Name, c.typ, result)
		}
	}
	// "Hit or Miss:" gives the target its own saving throw and damage on top of the hit: a failed save adds up to its dice (a flake found
	// with a critical hit and a failed save: Dao Earth Burst dealt 42 against a 38 maximum for the hit alone)
	if i := strings.Index(e.Text, "Hit or Miss:"); i >= 0 {
		if se, ok := parseSaveEffect(e.Text[i+len("Hit or Miss:"):]); ok {
			for _, d := range se.Damage {
				n, sides := game.ParseDice(d.Dice)
				hi += n*sides + game.FlatBonus(d.Dice)
			}
		}
	}
	for _, a := range p.Alts {
		if a.Flat != nil {
			hi = max(hi, hi-1+*a.Flat)
		} else if a.Dice != "" {
			n, sides := game.ParseDice(a.Dice)
			hi += 2*n*sides + game.FlatBonus(a.Dice)
		}
	}
	if dealt < lo || dealt > hi {
		t.Errorf("%s / %s: dealt %d, outside what its damage components allow (%d to %d): %s", monster, e.Name, dealt, lo, hi, result)
	}
}
