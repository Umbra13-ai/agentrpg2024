package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/agentrpg/agentrpg/game"
)

// The early-game playtest set (owner, Nov 22: CR 0 to 1, widened to CR 3 the same day: 201 SRD 5.2.1 monsters) through the real routes, every entry:
//
//   - each action, Bonus Action and Reaction is sent as POST /api/gm/narrate's monster_action at a character in a fight, with every
//     option its entry offers in use (a form, a standard action, a spell: read from the entry by the server's own readers), after the
//     setup its own gate needs (a character's hit on it for a Reaction to a hit, a Bloodied target hit just before for "Immediately
//     after dealing damage to a creature that is already Bloodied", a grapple for a swallow, Dim Light for a light-gated Hide);
//   - an attack is repeated until it hits and a saving throw until it fails, so what the entry does is seen;
//   - what is stored is read back: the character's Hit Points, its condition records and the combat effects on it, the monster's own
//     records, effects and state, the character's inventory (a corroded item). An attack that hits deals damage; a hit or failed save that gives conditions leaves their records.
//   - each entry ends applied (something stored), or flagged to the GM (the reply hands the GM what the server doesn't do), or
//     refused by its own rule (the refusal names it). A silent entry, an unexplained refusal or a missing result fails.
//   - each trait is in the GM's view of the fight (GET /api/campaigns/{id}/combat) marked as applied by the server, or by the GM.
//
// EARLY_ROUTES_OUT receives one line per run.

type earlyRun struct {
	monster, kind, entry, use string
	code                      int
	result                    string
	hpLost, conds, fx         int
	selfChanged, itemChanged  bool
	died                      bool
	verdict                   string
}

func TestPostgresEarlyGameEveryEntry(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "early gm")
	_, player := pgAgent(t, "early player")
	must := func(method, path, token string, body interface{}) map[string]interface{} {
		t.Helper()
		status, j := pgCall(t, method, path, token, body)
		if status != 200 && status != 201 {
			t.Fatalf("%s %s: %d %v", method, path, status, j)
		}
		return j
	}
	camp := int(must("POST", "/api/campaigns", gm, map[string]interface{}{"name": "Early game routes", "max_players": 2})["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	// a halfling: no damage Resistance (a dwarf's halves Poison), and Small, so a swallow of "a Small or smaller target" is seen too
	heroReq := pgRogue(fmt.Sprintf("Early Hero %d", camp))
	heroReq["species"] = "halfling"
	char := int(must("POST", "/api/characters", player, heroReq)["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var hero, equipped, heroSize string
	db.QueryRow(`SELECT name, COALESCE(equipped_main_hand,'') FROM characters WHERE id=$1`, char).Scan(&hero, &equipped)
	if equipped == "" {
		pgCall(t, "POST", "/api/characters/equip-weapon", player, map[string]interface{}{"character_id": char, "weapon": "dagger", "slot": "main_hand"})
		equipped = "dagger"
	}
	must("POST", cp+"/combat/start", gm, map[string]interface{}{})
	t.Cleanup(func() { pgCall(t, "POST", cp+"/combat/end", gm, nil) })
	heroSize = creatureSize(camp, char)

	const fullHP = 100000
	var startInventory string
	db.QueryRow(`SELECT inventory::text FROM characters WHERE id=$1`, char).Scan(&startInventory)
	inventory := func() string {
		var inv string
		db.QueryRow(`SELECT inventory::text FROM characters WHERE id=$1`, char).Scan(&inv)
		return inv
	}
	resetHero := func(hp int) {
		pgExec(t, `UPDATE characters SET inventory=$1::jsonb WHERE id=$2`, startInventory, char) // a corroded weapon or armor back as it was
		pgExec(t, `DELETE FROM condition_records WHERE lobby_id=$1 AND target_id=$2`, camp, char)
		// alive, with every score 1 (saves fail often enough to see what they do), nothing left from the run before
		pgExec(t, `UPDATE characters SET hp=$1,max_hp=$2,ac=1,temp_hp=0,conditions='[]',wild_shape_form='',action_used=false,attacks_remaining=NULL,weapon_interaction_used=false,
			is_dead=false,is_stable=false,death_save_successes=0,death_save_failures=0,str=1,dex=1,con=1,intl=1,wis=1,cha=1 WHERE id=$3`, hp, fullHP, char)
		pgExec(t, `DELETE FROM combat_effects WHERE lobby_id=$1`, camp)
		pgExec(t, `DELETE FROM spell_effects WHERE lobby_id=$1`, camp)
		pgExec(t, `DELETE FROM ability_reductions WHERE creature_id=$1`, char)
		var held string
		db.QueryRow(`SELECT COALESCE(equipped_main_hand,'') FROM characters WHERE id=$1`, char).Scan(&held)
		if held == "" { // an Unconscious earlier in the run dropped it (the condition owner)
			if c, j := pgCall(t, "POST", "/api/characters/equip-weapon", player, map[string]interface{}{"character_id": char, "weapon": equipped, "slot": "main_hand"}); c != 200 {
				t.Fatalf("equip again: %d %v", c, j)
			}
		}
	}
	heroDead := func() bool {
		var dead bool
		db.QueryRow(`SELECT COALESCE(is_dead,false) FROM characters WHERE id=$1`, char).Scan(&dead)
		return dead
	}
	heroHP := func() int {
		var hp int
		db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, char).Scan(&hp)
		return hp
	}
	effectsOn := func(id int) int {
		var n int
		var spells int
		db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1 AND (target_id=$2 OR source_id=$2)`, camp, id).Scan(&n)
		db.QueryRow(`SELECT count(*) FROM spell_effects WHERE lobby_id=$1 AND (target_id=$2 OR caster_id=$2)`, camp, id).Scan(&spells)
		return n + spells
	}
	selfSnapshot := func(id int) string {
		s := loadMonsterState(camp, id)
		return fmt.Sprint(len(loadConditionRecords(camp, id)), effectsOn(id), s.DayUses, s.RechargeSpent, s.LAOnce, monsterEntryVitals(camp, id).HP, formOf(camp, id))
	}
	narrateAt := func(monster, action, target, use string, attacks []string) (int, string) {
		ma := map[string]interface{}{"monster": monster, "action": action, "target": target, "confirm_targeting": true}
		if use != "" {
			ma["use"] = use
		}
		if len(attacks) > 0 {
			ma["attacks"] = attacks
		}
		code, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "early-game route test", "monster_action": ma})
		return code, fmt.Sprint(j["monster_action_result"], " ", j["error"])
	}
	narrate := func(monster, action, use string, attacks []string) (int, string) {
		return narrateAt(monster, action, hero, use, attacks)
	}
	// heroHits: the character hits the monster with its weapon (POST /api/action), the trigger of a Reaction to a hit or an attack roll
	heroHits := func(id int) bool {
		for try := 0; try < 40; try++ {
			clearMonsterReactionState(camp)
			monsterReactionRound.Delete(fmt.Sprintf("%d:%d", camp, id))
			setMonsterTempHP(camp, id, 0)
			setMonsterHP(camp, id, 1000)
			pgExec(t, `UPDATE characters SET action_used=false,attacks_remaining=NULL,conditions='[]',weapon_interaction_used=false WHERE id=$1`, char)
			_, r := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"action": "attack", "target_id": id, "description": "I attack with my " + equipped})
			if am, _ := r["attack"].(map[string]interface{}); am != nil && am["hit"] == true {
				return true
			} else if try == 39 {
				var eq, inv string
				db.QueryRow(`SELECT COALESCE(equipped_main_hand,''), inventory::text FROM characters WHERE id=$1`, char).Scan(&eq, &inv)
				t.Logf("the character's last attack: %v (equipped %q at start, now %q; %s)", r, equipped, eq, inv)
			}
		}
		return false
	}
	// hitWith: the monster's attack entry until it hits (and, with cond, until the character has that condition from it)
	hitWith := func(name string, id int, attack, cond string) bool {
		for try := 0; try < 30; try++ {
			pgExec(t, `DELETE FROM combat_monster_state WHERE lobby_id=$1 AND monster_id=$2`, camp, id)
			_, res := narrate(name, attack, "", nil)
			if !strings.Contains(res, "HIT") {
				continue
			}
			if cond == "" || len(recordsOf(camp, char, cond)) > 0 {
				return true
			}
		}
		return false
	}

	type fightCheck struct{ monster, slug string }
	var runs []earlyRun
	var traitRows []string
	fail := func(r earlyRun, why string) {
		t.Errorf("%s / %s (%s, use %q): %s: %d %s", r.monster, r.entry, r.kind, r.use, why, r.code, r.result)
	}
	for i, m := range earlyGameMonsters() {
		name := fmt.Sprintf("%s %d", m.Name, i)
		slug := game.Slug(m.Name)
		must("POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": name, "monster_key": slug, "hp": 1000, "ac": 1, "initiative": -100 - i}}})
		id := monsterIDsByKeyOrName(camp, name)[0]
		rows := make([]monsterActionRow, 0, len(m.Actions))
		for _, a := range m.Actions {
			rows = append(rows, parseMonsterAttack(a.Name, a.Text).monsterActionRow)
		}
		firstAttack, grappleAttack := "", ""
		for _, a := range m.Actions {
			if isAttackEntry(a.Text) && !strings.HasPrefix(a.Name, "Multiattack") {
				if firstAttack == "" {
					firstAttack = a.Name
				}
				if grappleAttack == "" && strings.Contains(a.Text, "Grappled condition") {
					grappleAttack = a.Name
				}
			}
		}

		// traits: the GM's view of the fight says who applies each
		_, view := pgCall(t, "GET", cp+"/combat", gm, nil)
		shown := map[string]bool{}
		if cs, ok := view["combatants"].([]interface{}); ok {
			for _, c := range cs {
				cm, _ := c.(map[string]interface{})
				if asInt(cm["id"]) != id {
					continue
				}
				sb, _ := cm["stat_block"].(map[string]interface{})
				ts, _ := sb["traits"].([]interface{})
				for _, x := range ts {
					tm, _ := x.(map[string]interface{})
					shown[fmt.Sprint(tm["name"])] = true
					by := "the GM"
					if tm["server_applies"] == true {
						by = "the server"
					}
					traitRows = append(traitRows, fmt.Sprintf("%s | trait | %s | applied by %s: %v", m.Name, tm["name"], by, tm["server_does"]))
					if tm["server_does"] == nil || fmt.Sprint(tm["server_does"]) == "" {
						t.Errorf("%s / %s: the GM's view says nothing about who applies the trait", m.Name, tm["name"])
					}
				}
			}
		}
		for _, tr := range m.Traits {
			if !shown[tr.Name] {
				t.Errorf("%s / %s: the trait is not in the GM's view of the fight", m.Name, tr.Name)
			}
		}

		for _, g := range []struct {
			kind    string
			entries []srdMonsterEntry
		}{{"action", m.Actions}, {"bonus", m.BonusActions}, {"reaction", m.Reactions}} {
			for _, e := range g.entries {
				// the options the entry offers, read by the server's own readers: every one is run
				uses := []string{""}
				if forms, trueForm := shapeShiftForms(e.Text); len(forms) > 0 {
					uses = append(append([]string{}, forms...), trueForm)
				} else if acts := monsterStandardActions(e.Text); len(acts) > 1 {
					uses = acts
				} else {
					var spells []string
					spells = append(spells, castChoiceList(e.Text)...)
					for _, tier := range spellTiersOf(e.Text) {
						spells = append(spells, tier.Spells...)
					}
					if len(spells) > 0 {
						uses = spells
					}
				}
				var attacks []string
				if strings.HasPrefix(e.Name, "Multiattack") {
					if _, problem := multiattackPlan(e.Text, rows); problem != "" {
						if spec, ok := parseMultiattackChoice(e.Text, rows); ok {
							for k := 0; k < spec.budget; k++ {
								attacks = append(attacks, titleCase(spec.options[0]))
							}
						}
					}
				}
				for _, use := range uses {
					run := earlyRun{monster: m.Name, kind: g.kind, entry: e.Name, use: use}
					a := parseMonsterAttack(e.Name, e.Text)
					rider, hasRider := parseHitRider(e.Text)
					save, hasSave := parseSaveEffect(e.Text)
					swallow := readSwallow(e.Text)
					for try := 0; try < 60; try++ {
						resetHero(fullHP)
						pgExec(t, `DELETE FROM combat_monster_state WHERE lobby_id=$1 AND monster_id=$2`, camp, id)
						pgExec(t, `DELETE FROM monster_day_uses WHERE campaign_id=$1`, camp)
						clearMonsterReactionState(camp)
						monsterReactionRound.Delete(fmt.Sprintf("%d:%d", camp, id))
						setMonsterHP(camp, id, 1000)
						pgCall(t, "POST", "/api/gm/set-lighting", gm, map[string]interface{}{"campaign_id": camp, "lighting": "bright"})
						// the setup the entry's own gate needs
						if needs, _ := reactionSelfHitTrigger(e.Text); needs || strings.Contains(e.Text, "makes an attack roll against it") {
							if !heroHits(id) {
								t.Fatalf("%s: the character's attack never hit", name)
							}
						}
						if afterBloodiedHitRe.MatchString(e.Text) {
							resetHero(fullHP/2 - 1)
							if !hitWith(name, id, firstAttack, "") {
								t.Fatalf("%s / %s: its attack never hit", name, firstAttack)
							}
						}
						if swallow != nil && grappleAttack != "" {
							if !hitWith(name, id, grappleAttack, "grappled") {
								t.Fatalf("%s / %s: its attack never grappled", name, grappleAttack)
							}
						}
						if hideLightGateRe.MatchString(e.Text) {
							pgCall(t, "POST", "/api/gm/set-lighting", gm, map[string]interface{}{"campaign_id": camp, "lighting": "dim"})
						}
						// what a single save target must be ("one Frightened creature", "one living creature ... that has 0 Hit Points"):
						// the hero is made so, read from the same header as the server's check (singleSaveTarget)
						if parts := singleSaveTarget(e.Text); parts != nil {
							if parts[2] != "" {
								addCondition(camp, char, condAdd{Condition: strings.ToLower(parts[2]), Source: "the route test's setup"})
							}
							if parts[5] != "" {
								resetHero(0)
							}
						}
						if hasSave && save.DropAtOrBelow > 0 { // "If the target has 20 Hit Points or fewer, it drops to 0": that branch
							pgExec(t, `UPDATE characters SET hp=$1 WHERE id=$2`, save.DropAtOrBelow, char)
						}
						// "Bite (Wolf or Hybrid Form Only)": the monster's own Shape-Shift into the first form the entry names
						if forms := entryForms(e.Name); len(forms) > 0 {
							shift, ok := shapeShiftEntry(monsterMetaFor(slug))
							if !ok {
								t.Fatalf("%s / %s: a form-gated entry without a Shape-Shift entry", name, e.Name)
							}
							_, trueForm := shapeShiftForms(shift.Text)
							if c, res := narrate(name, shift.Name, forms[0], nil); c != 200 || (formOf(camp, id) == "") != strings.EqualFold(forms[0], trueForm) {
								t.Fatalf("%s: Shape-Shift into %s: %d %s", name, forms[0], c, res)
							}
						}
						target := hero
						if g.kind == "reaction" && strings.Contains(e.Text, " ally ") { // "The goblin chooses a Small or Medium ally within 5 feet of itself"
							ally := name + " ally"
							if len(monsterIDsByKeyOrName(camp, ally)) == 0 {
								must("POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": ally, "monster_key": slug, "hp": 1000, "ac": 1, "initiative": -1000 - i}}})
							}
							target = ally
						}
						hpBefore, selfBefore, invBefore, deadBefore := heroHP(), selfSnapshot(id), inventory(), heroDead()
						fxBefore := effectsOn(char)
						run.code, run.result = narrateAt(name, e.Name, target, use, attacks)
						run.hpLost, run.conds, run.fx = hpBefore-heroHP(), len(loadConditionRecords(camp, char)), effectsOn(char)-fxBefore
						run.selfChanged = selfSnapshot(id) != selfBefore
						run.itemChanged = inventory() != invBefore // the item state owner keeps corrosion on the item
						run.died = heroDead() && !deadBefore
						missed := (isAttackEntry(e.Text) || strings.HasPrefix(e.Name, "Multiattack")) && !strings.Contains(run.result, "HIT")
						saved := strings.Contains(run.result, "succeeds on the") && !strings.Contains(run.result, "fails the")
						// a roll that did nothing this time: a Hide that failed, a Parry whose +2 AC didn't turn the hit into a miss, a hit that rolled 0 damage
						notYet := strings.Contains(run.result, "Not hidden") || strings.Contains(run.result, "still hits") || strings.Contains(run.result, "still a hit") || zeroDamageRe.MatchString(run.result)
						if !missed && !saved && !notYet {
							break
						}
					}
					// stored, or the rule's own answer that it does nothing to this target ("isn't a Beast, so Animal Friendship has no effect")
					stored := run.hpLost > 0 || run.conds > 0 || run.fx > 0 || run.selfChanged || run.itemChanged || run.died || strings.Contains(run.result, "has no effect")
					flagged := strings.Contains(run.result, "GM")
					switch {
					case run.code != 200 && !(swallow != nil && sizeOrder[strings.ToLower(heroSize)] > sizeOrder[swallow.maxSize] && strings.Contains(run.result, "swallows only a target of")):
						run.verdict = "REFUSED"
						fail(run, "refused")
					case strings.TrimSpace(strings.TrimSuffix(run.result, " <nil>")) == "" || strings.Contains(run.result, "needs GM adjudication") || strings.Contains(run.result, "could not be"):
						run.verdict = "NO RESULT"
						fail(run, "no result")
					case run.code != 200:
						run.verdict = "refused by its rule"
					case isAttackEntry(e.Text) && strings.Contains(run.result, "HIT") && a.DamageDice != "" && run.hpLost <= 0 && !strings.Contains(run.result, "(not applied)") &&
						!zeroDamageRe.MatchString(run.result): // "1 (1d2 - 1) Piercing damage" can roll 0
						run.verdict = "HIT WITHOUT DAMAGE"
						fail(run, "the hit dealt no damage")
					case stored && flagged:
						run.verdict = "applied, rest flagged to the GM"
					case stored:
						run.verdict = "applied"
					case flagged:
						run.verdict = "flagged to the GM"
					default:
						run.verdict = "SILENT"
						fail(run, "nothing stored and nothing flagged to the GM")
					}
					// a hit's or a failed save's conditions are recorded on the character (size-gated and immune ones say so in the reply)
					var want []string
					if isAttackEntry(e.Text) && strings.Contains(run.result, "HIT") && hasRider && !rider.CondIfAdvantage {
						want = rider.Conditions
					} else if hasSave && !isAttackEntry(e.Text) && strings.Contains(run.result, "fails the") && len(splitSaveOptions(e.Text)) < 2 {
						want = save.Conditions
					}
					for _, c := range want {
						if len(recordsOf(camp, char, c)) == 0 && !strings.Contains(strings.ToLower(run.result), "not "+c) && !strings.Contains(run.result, "only if") {
							fail(run, "no "+c+" record")
						}
					}
					runs = append(runs, run)
				}
			}
		}
		pgCall(t, "POST", cp+"/combat/remove", gm, map[string]interface{}{"combatant_id": id})
	}
	verdicts := map[string]int{}
	entries := map[string]bool{}
	for _, r := range runs {
		verdicts[r.verdict]++
		entries[r.monster+"|"+r.kind+"|"+r.entry] = true
	}
	t.Logf("early game routes: %d runs over %d actions, Bonus Actions and Reactions, %d traits: %v", len(runs), len(entries), len(traitRows), verdicts)
	if len(entries)+len(traitRows) != 540 {
		t.Errorf("%d entries and %d traits checked; the set has 540", len(entries), len(traitRows))
	}
	if out := os.Getenv("EARLY_ROUTES_OUT"); out != "" {
		var lines []string
		cell := func(x string) string {
			return strings.ReplaceAll(regexp.MustCompile(`\s+`).ReplaceAllString(x, " "), "|", "/")
		}
		for _, r := range runs {
			var stored []string
			for _, x := range []struct {
				on   bool
				what string
			}{{r.hpLost > 0, "Hit Points"}, {r.conds > 0, "condition records"}, {r.fx > 0, "effects on the character"}, {r.selfChanged, "the monster's own state"}, {r.itemChanged, "an item"}, {r.died, "the character's death"}} {
				if x.on {
					stored = append(stored, x.what)
				}
			}
			reply := strings.TrimSuffix(strings.TrimSpace(r.result), " <nil>")
			if len(reply) > 260 {
				reply = reply[:257] + "..."
			}
			use := r.use
			if use == "" {
				use = "-"
			}
			lines = append(lines, fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s |", r.monster, r.kind, cell(r.entry), cell(use), r.verdict, strings.Join(stored, ", "), cell(reply)))
		}
		for _, tr := range traitRows {
			p := strings.SplitN(tr, " | ", 4)
			lines = append(lines, fmt.Sprintf("| %s | trait | %s | - | %s |  |  |", p[0], cell(p[2]), cell(p[3])))
		}
		sort.Strings(lines)
		head := "# SRD 5.2.1 early-game monsters (CR 0 to 3): the route test\n\nWritten by `EARLY_ROUTES_OUT=$PWD/docs/SRD_EARLY_GAME_ROUTES.md go test ./cmd/server -run TestPostgresEarlyGameEveryEntry`" +
			" (srd_early_game_routes_2024_test.go, Postgres): one run's result. Every action, Bonus Action and Reaction of the 201 monsters sent through" +
			" POST /api/gm/narrate's monster_action at a character, once per option its entry offers (`use`), after the setup its gate needs; an attack" +
			" repeated until it hits and a save until it fails. \"Stored\" is what the server wrote; \"flagged to the GM\" means the reply hands the GM what" +
			" the server doesn't do. Movement is run without a battle map here (the GM places the creature). Traits: who applies each, as the GM's view of the fight says.\n\n" +
			fmt.Sprintf("Runs: %d over %d actions, Bonus Actions and Reactions; traits: %d. Verdicts: %v.\n\n", len(runs), len(entries), len(traitRows), verdicts) +
			"| monster | kind | entry | use | verdict | stored | reply (shortened) |\n|---|---|---|---|---|---|---|\n"
		os.WriteFile(out, []byte(head+strings.Join(lines, "\n")+"\n"), 0o644)
	}
}

var zeroDamageRe = regexp.MustCompile(`HIT, 0 [A-Z][a-z]+ damage`)

// formOf: the monster's recorded Shape-Shift form ("" in its true form).
func formOf(camp, id int) string {
	f, _ := shapeShiftRecord(camp, id)
	return f
}
