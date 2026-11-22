package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every attack route of a character hands its hit to the on-hit rider owner (hit_riders_2024.go): a function that builds an attack
// roll (attackCtx) either calls the owner or resolves the hit through resolveAttackOnMonster, which does. The others build a
// monster's or a device's roll, or only build the roll for a caller that resolves it (feedback log 65).
func TestEveryCharacterAttackRouteGetsTheHitRiders(t *testing.T) {
	notACharacter := map[string]string{
		"monsterAttacksCompanion":  "a monster's attack",
		"runMonsterAttackSequence": "a monster's attack on another monster (monster_vs_monster.go, monster_attack_continuation_2024.go)",
		"characterWeaponAttack":    "builds the roll; its callers resolve the hit with resolveAttackOnMonster",
	}
	owners := map[string]bool{"hitRiders": true, "hitWithRiders": true, "dealRiders": true, "damageCharacterWithRiders": true,
		"resolveAttackOnMonster": true, "ridersOnMonster": true}
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	found := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			builds, monster, calls := false, false, false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CompositeLit:
					if id, ok := x.Type.(*ast.Ident); ok && id.Name == "attackCtx" {
						builds = true
						for _, el := range x.Elts {
							kv, ok := el.(*ast.KeyValueExpr)
							if !ok {
								continue
							}
							if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "attacker" {
								if v, ok := kv.Value.(*ast.Ident); ok && (v.Name == "monsterID" || v.Name == "attackerID") && fn.Name.Name != "companionAttack" {
									monster = true // a monster's attack (monster_attacks.go, monster_effects_2024.go)
								}
							}
						}
						hasAttacker := false
						for _, el := range x.Elts {
							if kv, ok := el.(*ast.KeyValueExpr); ok {
								if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "attacker" {
									hasAttacker = true
								}
							}
						}
						if !hasAttacker {
							monster = true // a device's roll (the Apparatus of the Crab): nobody's riders
						}
					}
				case *ast.CallExpr:
					if id, ok := x.Fun.(*ast.Ident); ok && owners[id.Name] {
						calls = true
					}
				}
				return true
			})
			if !builds {
				continue
			}
			found++
			if calls || monster || notACharacter[fn.Name.Name] != "" {
				continue
			}
			t.Errorf("%s: %s makes a character's attack roll without the hit riders (hitWithRiders, hit_riders_2024.go)", fset.Position(fn.Pos()), fn.Name.Name)
		}
	}
	if found < 10 {
		t.Fatalf("only %d attack routes found: the test reads the wrong thing", found)
	}
}

func TestRiderNotes(t *testing.T) {
	rs := []hitRider{{Name: "Hex", Dice: "2d6", Damage: 9, Type: "necrotic"}, {Name: "Hunter's Mark", Dice: "1d6", Damage: 4, Type: "force"}}
	if got := riderNote(rs); got != " (+9 Hex, 2d6 necrotic) (+4 Hunter's Mark, 1d6 force)" {
		t.Errorf("riderNote: %q", got)
	}
	if riderTotal(rs) != 13 {
		t.Errorf("riderTotal: %d", riderTotal(rs))
	}
	if got := ridersOfType(append(rs, hitRider{Name: "Hex", Dice: "1d6", Damage: 2, Type: "necrotic"}), "necrotic"); got != "Hex (2d6, 1d6)" {
		t.Errorf("ridersOfType: %q", got)
	}
	if got := riderClause(rs[0], 4, "resistant"); got != " (+4 Hex, 2d6 necrotic: it resists)" {
		t.Errorf("riderClause: %q", got)
	}
	for in, want := range map[string]string{"Hex on the goblin: Dexterity": "Dexterity", "hex it, WIS": "Wisdom", "hex the goblin": "", "strength!": "Strength"} {
		if got := hexAbilityIn(in); got != want {
			t.Errorf("hexAbilityIn(%q) = %q, want %q", in, got, want)
		}
	}
	if hitRiders(0, -1, false) != nil || hitRiders(companionIDBase+3, -1, false) != nil {
		t.Error("a monster or a companion has a character's riders")
	}
}

// pgFightWith: a started campaign with one character (a finished pgRogue, the schedule off) in a fight with two monsters with low
// Armor Class and many Hit Points. It answers the GM's token, the player's token, the campaign, the character and the monsters.
func pgFightWith(t *testing.T, label string, players int) (gm string, toks []string, camp int, chars []int, monsters []int) {
	t.Helper()
	_, gm = pgAgent(t, label+" gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": fmt.Sprintf("Pg %s %d", label, time.Now().UnixNano()%1e9), "max_players": players})
	camp = int(j["campaign_id"].(float64))
	pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/schedule", camp), gm, map[string]interface{}{"state": "off"})
	for i := 0; i < players; i++ {
		_, tok := pgAgent(t, fmt.Sprintf("%s player %d", label, i))
		_, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg %s %d %d", label, i, time.Now().UnixNano()%1e9)))
		id := int(j["character_id"].(float64))
		pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), tok, map[string]interface{}{"character_id": id})
		pgFinish(t, tok, id)
		pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", camp), tok, nil)
		toks, chars = append(toks, tok), append(chars, id)
	}
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", camp), gm, nil)
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", camp), gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/start", camp), gm, map[string]interface{}{}); c != 200 {
		t.Fatalf("combat start: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Rider Target", "monster_key": "goblin-warrior", "hp": 3000, "ac": 1},
		{"name": "Other Target", "monster_key": "goblin-warrior", "hp": 3000, "ac": 1}}}); c != 200 {
		t.Fatalf("add: %d %v", c, j)
	}
	for _, e := range combatEntries(camp) {
		if id := asInt(e["id"]); id < 0 {
			monsters = append(monsters, id)
		}
	}
	if len(monsters) != 2 {
		t.Fatalf("monsters: %v", monsters)
	}
	return
}

// Feedback log 65 (I-KESTREL-3, -4): Hex and Hunter's Mark add their damage after the hit on every attack route, each beam's line
// is the attacker's, and the cast says who is marked and, for Hex, which ability's checks have Disadvantage.
func TestPostgresHitRidersOnEveryAttackRoute(t *testing.T) {
	testPostgres(t)
	_, toks, camp, chars, mons := pgFightWith(t, "riders", 1)
	tok, char, target, other := toks[0], chars[0], mons[0], mons[1]
	pgExec(t, `UPDATE characters SET class = 'warlock', subclass = NULL, level = 17, cha = 18,
		known_spells = '["eldritch-blast","chill-touch","hex"]', inventory = COALESCE(inventory,'[]'::jsonb) || '[{"name":"Component Pouch","quantity":1}]'::jsonb WHERE id = $1`, char)
	hp := func(id int) int {
		for _, e := range combatEntries(camp) {
			if asInt(e["id"]) == id {
				return asInt(e["hp"])
			}
		}
		return -1
	}
	act := func(body map[string]interface{}) (int, string) {
		t.Helper()
		pgExec(t, `UPDATE characters SET action_used = false, bonus_action_used = false, attacks_remaining = NULL WHERE id = $1`, char)
		c, j := pgCall(t, "POST", "/api/action", tok, body)
		return c, fmt.Sprint(j["result"]) + fmt.Sprint(j["message"])
	}
	mark := func(spell string, on int) {
		pgExec(t, `UPDATE characters SET concentrating_on = $1 WHERE id = $2`, fmt.Sprintf("%s:%d", spell, on), char)
	}

	// casting Hex: the creature marked and the ability named
	c, res := act(map[string]interface{}{"action": "cast", "spell_slug": "hex", "target_id": target, "description": "Hex on it: Dexterity"})
	var conc string
	db.QueryRow(`SELECT COALESCE(concentrating_on,'') FROM characters WHERE id = $1`, char).Scan(&conc)
	if c != 200 || conc != fmt.Sprintf("Hex:%d", target) || !strings.Contains(res, "Disadvantage on Dexterity checks") || !strings.Contains(res, "each of your hits on it") {
		t.Errorf("casting Hex: %d %q, concentrating on %q", c, res, conc)
	}

	// Eldritch Blast (four beams at level 17): every beam's attack line, and Hex's die on each beam that hits, dealt as Necrotic
	mark("Hex", target)
	before := hp(target)
	c, res = act(map[string]interface{}{"action": "cast", "spell_slug": "eldritch-blast", "target_id": target})
	if c != 200 || !strings.Contains(res, "Beam 4: Spell attack against Rider Target: ") || !strings.Contains(res, " to hit (d20") {
		t.Fatalf("Eldritch Blast: %d %q", c, res)
	}
	if strings.Contains(res, "HIT") && (!strings.Contains(res, "Hex, 1d6 necrotic") && !strings.Contains(res, "Hex, 2d6 necrotic") || !strings.Contains(res, "necrotic damage.")) {
		t.Errorf("Eldritch Blast hit without Hex's damage: %q", res)
	}
	if strings.Contains(res, "HIT") && before-hp(target) < 2 {
		t.Errorf("Eldritch Blast's hits took %d Hit Points: %q", before-hp(target), res)
	}

	// a single spell attack (Chill Touch) and a weapon attack (here an Unarmed Strike) with Hunter's Mark: tried until one hits
	for _, route := range []struct {
		spell, word string
		body        map[string]interface{}
	}{
		{"Hex", "Hex (", map[string]interface{}{"action": "cast", "spell_slug": "chill-touch", "target_id": target}},
		{"Hunter's Mark", "Hunter's Mark, 1d6 force", map[string]interface{}{"action": "attack", "target_id": target, "description": "I punch it"}},
	} {
		mark(route.spell, target)
		hit := false
		for i := 0; i < 20 && !hit; i++ {
			c, res = act(route.body)
			if c != 200 {
				t.Fatalf("%v: %d %q", route.body, c, res)
			}
			if strings.Contains(res, "HIT") {
				hit = true
				if !strings.Contains(res, route.word) && !strings.Contains(res, strings.Replace(route.word, "1d6", "2d6", 1)) { // a Critical Hit doubles the die
					t.Errorf("%v hit without %s: %q", route.body["action"], route.spell, res)
				}
			}
		}
		if !hit {
			t.Errorf("%v never hit AC 1 in 20 tries", route.body)
		}
	}

	// the mark is on one creature: a hit on another gets nothing
	mark("Hunter's Mark", target)
	for i := 0; i < 20; i++ {
		_, res = act(map[string]interface{}{"action": "attack", "target_id": other, "description": "I punch the other one"})
		if strings.Contains(res, "HIT") {
			if strings.Contains(res, "Hunter's Mark") {
				t.Errorf("a hit on an unmarked creature got Hunter's Mark: %q", res)
			}
			break
		}
	}
}

// Feedback log 67 (I-ALDRIC-4): a spell or feature that heals or restores is refused at a dead character before anything is spent,
// and a pending turn whose first step is refused that way takes its fallback. Spells that bring back the dead are not refused.
func TestPostgresHealingRefusesTheDead(t *testing.T) {
	testPostgres(t)
	gm, toks, camp, chars, mons := pgFightWith(t, "dead heal", 2)
	tok, healer, ally, monster := toks[0], chars[0], chars[1], mons[0]
	pgExec(t, `UPDATE characters SET class = 'cleric', subclass = NULL, level = 9, wis = 18, spell_slots_used = '{}',
		known_spells = '["sacred-flame"]', prepared_spells = '["healing-word","cure-wounds","lesser-restoration","revivify"]' WHERE id = $1`, healer)
	kill := func() {
		pgExec(t, `UPDATE characters SET hp = 0, is_dead = true WHERE id = $1`, ally)
	}
	kill()

	// every spell that heals or restores, and every feature that heals, is refused at the dead ally; a living ally is not refused
	aimedTargets.Store(healer, ally)
	n := 0
	for key, sp := range srdSpellsMemory {
		if !spellRestores(key, sp) {
			continue
		}
		n++
		if why := livingTargetRefusal("cast", "", healer, key); !strings.Contains(why, "is dead") || !strings.HasPrefix(why, "Cannot cast "+sp.Name) {
			t.Errorf("%s at a dead ally: %q", key, why)
		}
	}
	for key := range revivalSpells {
		if why := livingTargetRefusal("cast", "", healer, key); why != "" {
			t.Errorf("%s brings back the dead, but was refused: %q", key, why)
		}
	}
	tagged := 0
	for _, at := range actionTypes {
		if !at.hasTag("heals") {
			continue
		}
		tagged++
		if why := livingTargetRefusal(at.Name, "", healer); !strings.Contains(why, "is dead") {
			t.Errorf("%s at a dead ally: %q", at.Name, why)
		}
	}
	if why := deadTargetRefusal("give the Potion of Healing to", []int{ally}); !strings.HasPrefix(why, "Cannot give the Potion of Healing to ") {
		t.Errorf("a potion for the dead: %q", why)
	}
	pgExec(t, `UPDATE characters SET is_dead = false, hp = 0 WHERE id = $1`, ally) // dying, not dead: healing works
	if why := livingTargetRefusal("cast", "", healer, "healing-word"); why != "" {
		t.Errorf("a dying ally: %q", why)
	}
	aimedTargets.Delete(healer)
	if n < 6 || tagged < 3 {
		t.Fatalf("only %d spells and %d features checked", n, tagged)
	}
	kill()

	// through the request: refused with 409, no slot and no Bonus Action spent
	slots := func() string {
		var s string
		db.QueryRow(`SELECT COALESCE(spell_slots_used::text,'') FROM characters WHERE id = $1`, healer).Scan(&s)
		return s
	}
	was := slots()
	c, j := pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "cast", "spell_slug": "healing-word", "slot_level": 5, "target_character_id": ally})
	var bonus bool
	db.QueryRow(`SELECT COALESCE(bonus_action_used,false) FROM characters WHERE id = $1`, healer).Scan(&bonus)
	if c != 409 || !strings.Contains(fmt.Sprint(j["message"]), "is dead") || strings.Count(fmt.Sprint(j["message"]), "Nothing was spent") != 1 || slots() != was || bonus {
		t.Errorf("Healing Word at the dead: %d %v; slots %s -> %s; bonus action used %v", c, j, was, slots(), bonus)
	}

	// a pending turn: Healing Word on the ally, then Sacred Flame; the fallback is Sacred Flame alone. The ally dies before the slot
	// comes: the fallback runs and the slot stays unspent.
	pgExec(t, `UPDATE characters SET is_dead = false, hp = 5 WHERE id = $1`, ally)
	var raw []byte
	db.QueryRow(`SELECT turn_order FROM combat_state WHERE lobby_id = $1 AND active = true`, camp).Scan(&raw)
	var order []map[string]interface{}
	json.Unmarshal(raw, &order)
	var first, rest []map[string]interface{}
	for _, e := range order {
		switch asInt(e["id"]) {
		case monster:
			first = append(first, e)
		case healer:
			rest = append([]map[string]interface{}{e}, rest...)
		default:
			rest = append(rest, e)
		}
	}
	order = append(first, rest...) // the monster's turn now, the healer's next
	raw, _ = json.Marshal(order)
	pgExec(t, `UPDATE combat_state SET turn_order = $1, current_turn_index = 0 WHERE lobby_id = $2 AND active = true`, raw, camp)
	pgExec(t, `UPDATE characters SET action_used = false, bonus_action_used = false WHERE id = $1`, healer)
	c, j = pgCall(t, "POST", "/api/pending-turn", tok, map[string]interface{}{
		"steps": []map[string]interface{}{{"action": "cast", "spell_slug": "healing-word", "slot_level": 5, "target_character_id": ally},
			{"action": "cast", "spell_slug": "sacred-flame", "target_id": monster}},
		"fallbacks": [][]map[string]interface{}{{{"action": "cast", "spell_slug": "sacred-flame", "target_id": monster}}}})
	if c != 200 {
		t.Fatalf("pending turn: %d %v", c, j)
	}
	kill()
	was = slots()
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/next", camp), gm, map[string]interface{}{}); c != 200 {
		t.Fatalf("next: %d %v", c, j)
	}
	var status, note string
	var result []byte
	db.QueryRow(`SELECT status, COALESCE(note,''), COALESCE(result::text,'') FROM pending_turns WHERE character_id = $1 ORDER BY id DESC LIMIT 1`, healer).Scan(&status, &note, &result)
	if status != "resolved" || note != "fallback 1" || !strings.Contains(string(result), "Sacred Flame") || slots() != was {
		t.Errorf("the pending turn: %s %q %s; slots %s -> %s", status, note, result, was, slots())
	}
}
