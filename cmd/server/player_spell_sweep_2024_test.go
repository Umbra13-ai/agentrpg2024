package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The spell sweep (Part B, stage 3 of the player-side audit): a level 20 character of each casting class casts every spell of its class list (the
// class_spells table: 879 entries over 339 SRD spells) once at a monster in a fight and once at an ally, through POST /api/action, and what the
// server stored is read back. A cast is resolved (something stored: Hit Points, a condition, an effect, a slot), told (the reply says what the GM
// decides: the server does not apply it), or refused by state (a component, the casting time of a fight, a target the spell needs); it fails when it
// answers 5xx, answers success having changed nothing and said nothing, refuses a spell on the character's list as not its own, or refuses in other
// words than "Cannot ...". SPELL_SWEEP_OUT=file writes the table.

var spellSweepClasses = []struct{ class, background, species string }{
	{"bard", "acolyte", "human"}, {"cleric", "acolyte", "dwarf"}, {"druid", "sage", "elf"}, {"paladin", "soldier", "dragonborn"},
	{"ranger", "criminal", "elf"}, {"sorcerer", "sage", "tiefling"}, {"warlock", "criminal", "tiefling"}, {"wizard", "sage", "gnome"},
}

// spellResolvedOnTarget: the words of a spell that was worked out against its target (an attack roll, a saving throw, an immunity), so no change is its result.
var spellResolvedOnTarget = regexp.MustCompile(`(?i)spell attack against|saving throw|\bsave\b|\bsaves\b|immune|resist|no effect|is dead|unaffected`)

// allyHurtByCast: the ally's Hit Points are not what they were before the cast.
func allyHurtByCast(friend, hpBefore int) bool {
	var hp int
	db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, friend).Scan(&hp)
	return hp != hpBefore
}

func TestPostgresEveryClassSpellResolves(t *testing.T) {
	testPostgres(t)
	var mu sync.Mutex
	var report []string
	add := func(line string) { mu.Lock(); report = append(report, line); mu.Unlock() }
	for _, cl := range spellSweepClasses {
		cl := cl
		t.Run(cl.class, func(t *testing.T) {
			t.Parallel()
			_, gm := pgAgent(t, "spell gm "+cl.class)
			_, player := pgAgent(t, "spell player "+cl.class)
			_, ally := pgAgent(t, "spell ally "+cl.class)
			_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Spell sweep " + cl.class, "max_players": 3})
			camp := int(j["campaign_id"].(float64))
			cp := fmt.Sprintf("/api/campaigns/%d", camp)
			pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
			id := buildClassCharacter(t, gm, player, camp, cl.class, cl.background, cl.species)
			_, aj := pgCall(t, "POST", "/api/characters", ally, pgRogue(fmt.Sprintf("Spell Ally %d", camp)))
			friend := int(aj["character_id"].(float64))
			pgCall(t, "POST", cp+"/join", ally, map[string]interface{}{"character_id": friend})
			pgFinish(t, ally, friend)
			for L := 2; L <= 20; L++ {
				stage := fmt.Sprintf("%s level %d", cl.class, L)
				if c, j := pgCall(t, "POST", "/api/gm/update-character", gm, map[string]interface{}{"campaign_id": camp, "character_id": id, "level": L}); c != 200 || j["error"] != nil {
					t.Fatalf("%s: level step: %d %v", stage, c, j)
				}
				followSetup(t, player, id, stage)
			}
			pgCall(t, "POST", cp+"/ready", player, nil)
			pgCall(t, "POST", cp+"/ready", ally, nil)
			pgCall(t, "POST", cp+"/ready", gm, nil)
			if c, j := pgCall(t, "POST", cp+"/start", gm, nil); c != 200 {
				t.Fatalf("start: %d %v", c, j)
			}
			pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
			t.Cleanup(func() { pgCall(t, "POST", cp+"/combat/end", gm, nil) })
			pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Sweep Ogre", "monster_key": "ogre", "hp": 5000, "ac": 1, "initiative": 1}}})
			ogre := monsterIDsByKeyOrName(camp, "Sweep Ogre")[0]
			for n := 0; currentTurnID(camp) != id; n++ {
				if n == 10 {
					t.Fatalf("the caster never got its turn")
				}
				pgCall(t, "POST", cp+"/combat/next", gm, nil)
			}
			// every spell of the class list is on the character (cantrips included: known and prepared), so the cast is the spell's own question
			list := getClassSpellList(cl.class)
			sort.Strings(list)
			lj, _ := json.Marshal(list)
			pgExec(t, `UPDATE characters SET prepared_spells=$1::jsonb, known_spells=$1::jsonb WHERE id=$2`, string(lj), id)
			// the costly materials (a spell's "worth 300+ GP, which the spell consumes") are in the inventory each time, and a hand is free for the Somatic component
			pgCall(t, "POST", "/api/characters/unequip-armor", player, map[string]interface{}{"character_id": id, "shield": true})
			var baseInv []invItem
			editInventory(id, func(inv []invItem) []invItem { baseInv = append(baseInv, inv...); return inv })
			materials := []string{"Diamond", "Ruby", "Emerald", "Sapphire", "Pearl", "Gem", "Jewel", "Gold", "Silver", "Platinum", "Dust", "Powder", "Incense",
				"Holy Symbol", "Holy Water", "Oil", "Herbs", "Crystal", "Stone", "Bone"}
			reset := func() {
				editInventory(id, func([]invItem) []invItem {
					inv := append([]invItem{}, baseInv...)
					for _, m := range materials {
						inv = append(inv, invItem{"name": m, "value": 100000, "quantity": 5})
					}
					return inv
				})
				pgExec(t, `UPDATE characters SET hp=max_hp, temp_hp=0, action_used=false, bonus_action_used=false, reaction_used=false, attacks_remaining=NULL,
					movement_remaining=30, class_resources_used='{}'::jsonb, spell_slots_used='{}'::jsonb, pact_slots_used='{}'::jsonb, wild_shape_form='',
					concentrating_on=NULL, conditions='[]', slot_spent_turn='' WHERE id=$1`, id)
				pgExec(t, `UPDATE characters SET hp=max_hp/2, temp_hp=0, conditions='[]', is_dead=false, is_stable=false, death_save_successes=0, death_save_failures=0 WHERE id=$1`, friend)
				pgExec(t, `DELETE FROM condition_records WHERE lobby_id=$1`, camp)
				pgExec(t, `DELETE FROM combat_effects WHERE lobby_id=$1`, camp)
				pgExec(t, `DELETE FROM spell_effects WHERE lobby_id=$1`, camp)
			}
			for _, slug := range list {
				sp := srdSpellsMemory[slug]
				for _, aim := range []string{"monster", "ally"} {
					reset()
					body := map[string]interface{}{"action": "cast", "character_id": id, "spell_slug": slug, "target_id": ogre}
					if aim == "ally" {
						body["target_id"] = friend
					}
					if sp.Level > 0 {
						body["slot_level"] = sp.Level
					}
					if cl.class == "warlock" && sp.Level >= 6 { // Mystic Arcanum: one spell of the level, cast by the words "Mystic Arcanum"
						aj, _ := json.Marshal(map[string]string{fmt.Sprint(sp.Level): slug})
						pgExec(t, `UPDATE characters SET mystic_arcanum=$1::jsonb, mystic_arcanum_used='[]'::jsonb WHERE id=$2`, string(aj), id)
						body["description"] = "using Mystic Arcanum"
					}
					// what a cast did besides spend its slot and use its turn
					state := func() string {
						return featureSweepStateWithout(camp, id, "spell_slots_used", "pact_slots_used", "slot_spent_turn", "class_resources_used", "mystic_arcanum_used") + featureSweepState(camp, friend)
					}
					var hpBefore int
					db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, friend).Scan(&hpBefore)
					before := state()
					code, reply := pgCall(t, "POST", "/api/action", player, body)
					after := state()
					text := fmt.Sprint(reply)
					result := fmt.Sprint(reply["result"])
					verdict := "resolved"
					switch {
					case code >= 500:
						verdict = "FAIL 5xx"
					case code >= 400 && (featureNotYours(reply) || reply["error"] == "spell_not_available"):
						verdict = "FAIL the spell is on the character's list, the cast refuses it as not its own"
					case code >= 400:
						verdict = "refused by state"
					case !strings.HasPrefix(result, "Cannot ") && refusalWording.MatchString(result):
						verdict = "FAIL a refusal worded as a result"
					case aim == "ally" && code < 400 && sp.DamageDice != "" && !castDealsNoDamage(slug) && allyHurtByCast(friend, hpBefore) == false && !spellResolvedOnTarget.MatchString(result):
						verdict = "FAIL the cast tells damage it did not deal to the ally and does not say why (no attack roll, save or immunity)"
					case before == after && reply["result"] == nil && reply["message"] == nil:
						verdict = "FAIL changed nothing and said nothing"
					case before == after:
						verdict = "told only"
					}
					if dbg := os.Getenv("SPELL_SWEEP_DEBUG"); dbg == slug {
						var hpAfter int
						db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, friend).Scan(&hpAfter)
						t.Logf("%s %s at the %s: %d (ally hp %d -> %d) %s", cl.class, slug, aim, code, hpBefore, hpAfter, text)
					}
					short := text
					if len(short) > 170 {
						short = short[:170]
					}
					add(fmt.Sprintf("| %s | L%d | %s | %s | %d | %s | %s |", cl.class, sp.Level, slug, aim, code, verdict, strings.ReplaceAll(short, "|", "/")))
					if strings.HasPrefix(verdict, "FAIL") {
						t.Errorf("%s: %s at the %s: %s: %d %s", cl.class, slug, aim, verdict, code, short)
					}
				}
			}
		})
	}
	t.Cleanup(func() {
		if out := os.Getenv("SPELL_SWEEP_OUT"); out != "" {
			sort.Strings(report)
			os.WriteFile(out, []byte("| class | level | spell | aimed at | code | verdict | reply |\n|---|---|---|---|---|---|---|\n"+strings.Join(report, "\n")+"\n"), 0o644)
		}
	})
}
