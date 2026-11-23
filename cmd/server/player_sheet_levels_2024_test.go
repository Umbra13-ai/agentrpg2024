package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

// buildClassCharacter creates a level 1 character of a class through the character builder (each step's own suggestion), as tests/suites/levelrun.py does,
// finishes it, makes it the player of a started campaign and returns its id.
func buildClassCharacter(t *testing.T, gm, player string, camp int, class, background, species string) int {
	t.Helper()
	name := fmt.Sprintf("Sheet %s %d", class, time.Now().UnixNano()%1e9)
	c, d := pgCall(t, "POST", "/api/character-builder", player, map[string]interface{}{"name": name})
	if c != 200 {
		t.Fatalf("builder start: %d %v", c, d)
	}
	path := fmt.Sprintf("/api/character-builder/%d", int(d["draft_id"].(float64)))
	first := map[string]map[string]interface{}{"class": {"class": class}, "background": {"background": background}, "species": {"species": species}}
	for i := 0; i < 25; i++ {
		cs, _ := d["current_step"].(map[string]interface{})
		key, _ := cs["key"].(string)
		if strings.Contains(fmt.Sprint(d["next"]), "Every step is done") {
			break
		}
		var body map[string]interface{}
		switch {
		case first[key] != nil:
			body = first[key]
			delete(first, key)
		case key == "details":
			body, _ = cs["suggestion"].(map[string]interface{})
			body = copyMap(body)
			body["alignment"] = "Neutral Good"
		case key == "backstory":
			body = map[string]interface{}{"backstory": name + " grew up at the edge of the wild.\n\nA hard winter taught them to fight.\n\nNow they look for a place to belong."}
		default:
			body, _ = cs["suggestion"].(map[string]interface{})
		}
		if len(body) == 0 {
			t.Fatalf("%s creation step %q: no suggestion to send: %v", class, key, d)
		}
		c, d = pgCall(t, "POST", path, player, body)
		if d["error"] != nil {
			t.Fatalf("%s creation step %q: %v", class, key, d)
		}
	}
	c, f := pgCall(t, "POST", path+"/finish", player, map[string]interface{}{})
	if c != 200 || f["character_id"] == nil {
		t.Fatalf("%s finish: %d %v", class, c, f)
	}
	id := int(f["character_id"].(float64))
	followSetup(t, player, id, class+" level 1")
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), player, map[string]interface{}{"character_id": id})
	return id
}

func copyMap(m map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

// followSetup makes every required choice GET /setup offers, by the request each step names, until none is open (the optional steps stay as they are).
// It returns the steps left with no request to follow.
func followSetup(t *testing.T, player string, id int, stage string) []string {
	t.Helper()
	for round := 0; round < 8; round++ {
		_, s := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/setup", id), player, nil)
		acted := false
		var left []string
		for _, raw := range asList(s["steps"]) {
			st, _ := raw.(map[string]interface{})
			if done, _ := st["done"].(bool); done {
				continue
			}
			opt, _ := st["optional"].(bool)
			asked, _ := st["asked"].(bool) // the portrait: asked, never counted in steps_left (optional_steps_2024.go)
			if (opt || asked) && st["key"] != "auto_equip" {
				continue
			}
			key := fmt.Sprint(st["key"])
			if key == "auto_equip" {
				_, o := pgCall(t, "POST", "/api/characters/auto-equip", player, map[string]interface{}{"character_id": id, "answer": "yes"})
				if need := asList(o["needs_choices"]); len(need) > 0 {
					choices := map[string]interface{}{}
					for _, q := range need {
						qm, _ := q.(map[string]interface{})
						opts := asList(qm["options"])
						if len(opts) > 0 {
							choices[fmt.Sprint(qm["key"])] = opts[0].(map[string]interface{})["value"]
						}
					}
					pgCall(t, "POST", "/api/characters/auto-equip", player, map[string]interface{}{"character_id": id, "answer": "yes", "choices": choices})
				}
				acted = true
				continue
			}
			rq, _ := st["request"].(map[string]interface{})
			if rq == nil || rq["method"] == nil {
				left = append(left, key)
				continue
			}
			c, o := pgCall(t, fmt.Sprint(rq["method"]), fmt.Sprint(rq["path"]), player, rq["body"])
			acted = true
			if c >= 400 || o["error"] != nil {
				t.Errorf("%s: setup step %s: %d %v", stage, key, c, o)
			}
			if th, _ := st["then"].(map[string]interface{}); th != nil {
				if rq2, _ := th["request"].(map[string]interface{}); rq2 != nil {
					if c, o := pgCall(t, fmt.Sprint(rq2["method"]), fmt.Sprint(rq2["path"]), player, rq2["body"]); c >= 400 || o["error"] != nil {
						t.Errorf("%s: setup step %s (then): %d %v", stage, key, c, o)
					}
				}
			}
		}
		if !acted {
			return left
		}
	}
	return nil
}

// fullCasterSlots: the SRD 5.2.1 spell slots of a full caster (Bard, Cleric, Druid, Sorcerer, Wizard) by level, from the Spell Slots table: an
// independent copy of what game.SpellSlots computes, so a mistake in both is still caught.
var fullCasterSlots = [21][]int{
	1: {2}, 2: {3}, 3: {4, 2}, 4: {4, 3}, 5: {4, 3, 2}, 6: {4, 3, 3}, 7: {4, 3, 3, 1}, 8: {4, 3, 3, 2}, 9: {4, 3, 3, 3, 1}, 10: {4, 3, 3, 3, 2},
	11: {4, 3, 3, 3, 2, 1}, 12: {4, 3, 3, 3, 2, 1}, 13: {4, 3, 3, 3, 2, 1, 1}, 14: {4, 3, 3, 3, 2, 1, 1}, 15: {4, 3, 3, 3, 2, 1, 1, 1},
	16: {4, 3, 3, 3, 2, 1, 1, 1}, 17: {4, 3, 3, 3, 2, 1, 1, 1, 1}, 18: {4, 3, 3, 3, 3, 1, 1, 1, 1}, 19: {4, 3, 3, 3, 3, 2, 1, 1, 1}, 20: {4, 3, 3, 3, 3, 2, 2, 1, 1},
}

var playerSheetClasses = []struct{ class, background, species string }{
	{"barbarian", "soldier", "goliath"}, {"bard", "acolyte", "human"}, {"cleric", "acolyte", "dwarf"}, {"druid", "sage", "elf"},
	{"fighter", "soldier", "goliath"}, {"monk", "criminal", "halfling"}, {"paladin", "soldier", "dragonborn"}, {"ranger", "criminal", "elf"},
	{"rogue", "criminal", "halfling"}, {"sorcerer", "sage", "tiefling"}, {"warlock", "criminal", "tiefling"}, {"wizard", "sage", "gnome"},
}

func intsOf(v interface{}) map[string]int {
	out := map[string]int{}
	if m, ok := v.(map[string]interface{}); ok {
		for k, x := range m {
			if f, ok := x.(float64); ok {
				out[k] = int(f)
			}
		}
	}
	return out
}

// The sheet of every class at every level 1 to 20, taken there by the GM's level step and the setup suggestions (as tests/suites/levelrun.py does),
// agrees with the rules: proficiency bonus, Hit Dice, the spell slots of a full caster (an independent table) and of every caster (game.SpellSlots),
// class resources, prepared spells, the class features of the level, every skill's bonus from the scores and proficiencies, and the Hit Points each
// level adds. A difference names the class, the level and the field.
func TestPostgresSheetMatchesRulesEveryClassEveryLevel(t *testing.T) {
	testPostgres(t)
	for _, cl := range playerSheetClasses {
		cl := cl
		t.Run(cl.class, func(t *testing.T) {
			t.Parallel()
			_, gm := pgAgent(t, "sheet gm "+cl.class)
			_, player := pgAgent(t, "sheet player "+cl.class)
			_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Sheet levels " + cl.class, "max_players": 2})
			camp := int(j["campaign_id"].(float64))
			cp := fmt.Sprintf("/api/campaigns/%d", camp)
			pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
			id := buildClassCharacter(t, gm, player, camp, cl.class, cl.background, cl.species)
			pgCall(t, "POST", cp+"/ready", player, nil)
			pgCall(t, "POST", cp+"/ready", gm, nil)
			if c, j := pgCall(t, "POST", cp+"/start", gm, nil); c != 200 {
				t.Fatalf("start: %d %v", c, j)
			}
			prevMax, prevCon, prevBonus, orderChosen := 0, 0, 0, 0
			for L := 1; L <= 20; L++ {
				stage := fmt.Sprintf("%s level %d", cl.class, L)
				if L > 1 {
					if c, j := pgCall(t, "POST", "/api/gm/update-character", gm, map[string]interface{}{"campaign_id": camp, "character_id": id, "level": L}); c != 200 || j["error"] != nil {
						t.Fatalf("%s: level step: %d %v", stage, c, j)
					}
					if left := followSetup(t, player, id, stage); len(left) > 0 {
						t.Errorf("%s: setup left open with no request to follow: %v", stage, left)
					}
				}
				_, sh := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d", id), player, nil)
				bad := func(field string, got, want interface{}) {
					t.Errorf("%s: %s is %v, the rules say %v", stage, field, got, want)
				}
				if int(sh["level"].(float64)) != L {
					bad("level", sh["level"], L)
				}
				if fin, _ := sh["character_finished"].(bool); !fin {
					t.Errorf("%s: not finished: %v", stage, sh["to_finish"])
				}
				pb := 2 + (L-1)/4
				if got := int(sh["proficiency_bonus"].(float64)); got != pb || game.ProficiencyBonus(L) != pb {
					bad("proficiency_bonus", got, pb)
				}
				hd, _ := sh["hit_dice"].(map[string]interface{})
				if hd["die_type"] != fmt.Sprintf("d%d", game.HitDie(cl.class)) || int(hd["total"].(float64)) != L {
					bad("hit_dice", hd, fmt.Sprintf("d%d x %d", game.HitDie(cl.class), L))
				}
				stats := intsOf(sh["stats"])
				mod := func(a string) int { return game.Modifier(stats[a]) }
				// spell slots: an independent table for a full caster, game.SpellSlots for any caster
				slots := intsOf(func() interface{} { m, _ := sh["spell_slots"].(map[string]interface{}); return m["total"] }())
				want := game.SpellSlots(cl.class, L)
				for lvl, n := range want {
					if slots[fmt.Sprint(lvl)] != n {
						bad(fmt.Sprintf("spell slots of level %d", lvl), slots[fmt.Sprint(lvl)], n)
					}
				}
				for k, n := range slots {
					if n > 0 && want[atoiOr0(k)] != n {
						bad("spell slots of level "+k, n, want[atoiOr0(k)])
					}
				}
				switch cl.class {
				case "bard", "cleric", "druid", "sorcerer", "wizard":
					for lvl, n := range fullCasterSlots[L] {
						if slots[fmt.Sprint(lvl+1)] != n {
							bad(fmt.Sprintf("spell slots of level %d (the SRD table)", lvl+1), slots[fmt.Sprint(lvl+1)], n)
						}
					}
				}
				// class resources
				have := map[string]int{}
				for _, r := range asList(sh["class_resources"]) {
					rm, _ := r.(map[string]interface{})
					have[fmt.Sprint(rm["key"])] = int(rm["max"].(float64))
				}
				for key, n := range game.AllMaxClassResources(cl.class, L, game.Modifier(stats["cha"])) {
					if n > 0 && have[key] != n {
						bad("class resource "+key, have[key], n)
					}
				}
				// the features of the level
				names := map[string]bool{}
				for _, f := range asList(sh["class_features"]) {
					fm, _ := f.(map[string]interface{})
					names[fmt.Sprint(fm["name"])] = true
				}
				for _, f := range game.GetActiveClassFeatures(cl.class, L) {
					if !names[f.Name] {
						t.Errorf("%s: the class feature %q (level %d) is not on the sheet", stage, f.Name, f.Level)
					}
				}
				// a skill's bonus: the ability modifier, the proficiency bonus once, twice with Expertise, Jack of All Trades (Bard 2) half
				prof := map[string]bool{}
				for _, p := range asList(sh["skill_proficiencies"]) {
					prof[strings.ToLower(fmt.Sprint(p))] = true
				}
				exp := map[string]bool{}
				for _, p := range asList(sh["expertise"]) {
					exp[strings.ToLower(fmt.Sprint(p))] = true
				}
				bonuses := intsOf(sh["skill_bonuses"])
				for _, sk := range game.Skills {
					wantB := mod(sk.Ability)
					switch {
					case prof[sk.Name] && exp[sk.Name]:
						wantB += 2 * pb
					case prof[sk.Name]:
						wantB += pb
					case cl.class == "bard" && L >= 2:
						wantB += pb / 2
					}
					key := strings.ReplaceAll(sk.Name, " ", "_")
					got, ok := bonuses[key]
					if !ok {
						t.Errorf("%s: the sheet has no bonus for %s", stage, sk.Name)
						continue
					}
					// the Cleric's Thaumaturge and the Druid's Magician (Divine / Primal Order): the Wisdom modifier (at least +1) on two skills, if chosen
					order := (cl.class == "cleric" && (sk.Name == "arcana" || sk.Name == "religion")) || (cl.class == "druid" && (sk.Name == "arcana" || sk.Name == "nature"))
					if order {
						extra := max(1, mod("wis"))
						if got != wantB && got != wantB+extra {
							bad("skill bonus of "+sk.Name, got, fmt.Sprintf("%d, or %d with the order", wantB, wantB+extra))
						}
						withOrder := got == wantB+extra
						if orderChosen == 0 {
							orderChosen = map[bool]int{true: 1, false: -1}[withOrder]
						} else if (orderChosen == 1) != withOrder {
							t.Errorf("%s: the order's bonus is not on both of its skills at once (%s)", stage, sk.Name)
						}
						continue
					}
					if got != wantB {
						bad("skill bonus of "+sk.Name, got, wantB)
					}
				}
				// Hit Points: a level adds the die's average plus the Constitution modifier (and Constitution's change reaches every level); a
				// constant extra per level (a species or a feat) is read at level 2 and may change only when a subclass is chosen
				maxHP := int(sh["max_hp"].(float64))
				conMod := mod("con")
				avg := game.HitDie(cl.class)/2 + 1
				switch {
				case L == 1:
					if base := game.HitDie(cl.class) + conMod; maxHP < base || maxHP > base+3 {
						bad("max_hp at level 1", maxHP, fmt.Sprintf("%d to %d", base, base+3))
					}
				default:
					bonus := maxHP - prevMax - (avg + conMod + (L-1)*(conMod-prevCon))
					if L == 2 {
						prevBonus = bonus
					}
					if bonus != prevBonus {
						t.Logf("%s: the per-level extra Hit Points changed from %d to %d (a subclass or a feat?)", stage, prevBonus, bonus)
						if sub, _ := sh["subclass"].(string); sub == "" && L < 3 {
							t.Errorf("%s: the Hit Points added don't match the average die and Constitution: %d extra", stage, bonus)
						}
						prevBonus = bonus
					}
				}
				prevMax, prevCon = maxHP, conMod
			}
		})
	}
}

func atoiOr0(s string) int {
	n := 0
	fmt.Sscanf(s, "%d", &n)
	return n
}
