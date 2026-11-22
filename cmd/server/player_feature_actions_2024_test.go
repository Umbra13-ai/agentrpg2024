package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The feature-action sweep (Part B, stage 2 of the player-side audit): a character of each class, at levels 1, 5, 11, 17 and 20, sends every
// action type the table says is open to it (actionTypesOpenTo, the list the sheet, my-turn and a refusal show) at a monster in a fight, by the
// request the table names, and what the server stored is read back. The sheet and actionTypes must agree: a type the table lists that its
// resolver refuses as not the character's (a "Cannot use ..." or an unknown action), a 5xx, or a 200 that changed nothing and said nothing
// fails, naming the class, the level and the type. A refusal that comes from the state (no use left, no target, nothing held) is the rule's
// answer and is listed, not failed.

// featureSweepState: what a request could have changed, as one comparable text (the character's row and records, the fight; not the log or the turn's spent parts, which every request changes).
func featureSweepState(camp, id int) string {
	// the whole row, but for the turn's spent parts, which every request changes
	var row string
	db.QueryRow(`SELECT (to_jsonb(c) - 'action_used' - 'bonus_action_used' - 'reaction_used' - 'attacks_remaining' - 'movement_remaining' - 'weapon_interaction_used')::text
		FROM characters c WHERE id=$1`, id).Scan(&row)
	var recs, effs, spells int
	db.QueryRow(`SELECT count(*) FROM condition_records WHERE lobby_id=$1`, camp).Scan(&recs)
	db.QueryRow(`SELECT count(*) FROM combat_effects WHERE lobby_id=$1`, camp).Scan(&effs)
	db.QueryRow(`SELECT count(*) FROM spell_effects WHERE lobby_id=$1`, camp).Scan(&spells)
	fight, _ := json.Marshal(combatEntries(camp))
	return fmt.Sprint(row, "|", recs, "|", effs, "|", spells, "|", string(fight))
}

// featureNotYours: the reply says the type is not the character's: one of the table's own refusal codes (whoRefusal.Code) or an unknown action.
// A refusal for the state the type is used in (nothing held, no use left, no target) is the rule's answer, not this.
func featureNotYours(reply map[string]interface{}) bool {
	switch fmt.Sprint(reply["error"]) {
	case "wrong_class", "level_too_low", "wrong_subclass", "choice_required", "feat_required", "invocation_required", "item_not_in_hand",
		"not_attuned", "effect_required", "companion_required", "wrong_state", "unknown_action":
		return true
	}
	return false
}

// featureStateSetups: the request that puts a character in the state a type is used in (actionType.While) when a class feature does.
var featureStateSetups = map[string]map[string]interface{}{
	"extend_rage":       {"action": "rage"},
	"revert_wild_shape": {"action": "wild_shape", "description": "wolf"},
}

// refusalWording: a result that reads as a refusal (a use already spent, a limit, something missing).
var refusalWording = regexp.MustCompile(`(?i)^(you('ve| have)? already|you can't|you cannot|you can use .{0,40} only once|[a-z' ]{3,40} is already |no (uses?|charges?|points?) (left|remain))`)

func TestPostgresEveryFeatureActionResolves(t *testing.T) {
	testPostgres(t)
	levels := map[int]bool{1: true, 5: true, 11: true, 17: true, 20: true}
	var mu = make(chan struct{}, 1)
	var report []string
	for _, cl := range playerSheetClasses {
		cl := cl
		t.Run(cl.class, func(t *testing.T) {
			t.Parallel()
			_, gm := pgAgent(t, "feature gm "+cl.class)
			_, player := pgAgent(t, "feature player "+cl.class)
			_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Feature actions " + cl.class, "max_players": 2})
			camp := int(j["campaign_id"].(float64))
			cp := fmt.Sprintf("/api/campaigns/%d", camp)
			pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
			id := buildClassCharacter(t, gm, player, camp, cl.class, cl.background, cl.species)
			pgCall(t, "POST", cp+"/ready", player, nil)
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
					t.Fatalf("the hero never got its turn")
				}
				pgCall(t, "POST", cp+"/combat/next", gm, nil)
			}
			sweep := func(L int) {
				stage := fmt.Sprintf("%s level %d", cl.class, L)
				reset := func() {
					pgExec(t, `UPDATE characters SET hp=max_hp, temp_hp=0, action_used=false, bonus_action_used=false, reaction_used=false, attacks_remaining=NULL,
						weapon_interaction_used=false, movement_remaining=30, class_resources_used='{}'::jsonb, spell_slots_used='{}'::jsonb, pact_slots_used='{}'::jsonb,
						wild_shape_form='', concentrating_on=NULL, conditions='[]', slot_spent_turn='' WHERE id=$1`, id)
					pgExec(t, `DELETE FROM condition_records WHERE lobby_id=$1`, camp)
					pgExec(t, `DELETE FROM combat_effects WHERE lobby_id=$1`, camp)
					pgExec(t, `DELETE FROM spell_effects WHERE lobby_id=$1`, camp)
				}
				reset()
				// what a refusal of an unknown word offers (400 unknown_action, "actions") is the table's list for this character
				if c, r := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"character_id": id, "action": "zzz_not_an_action"}); c != 400 || r["error"] != "unknown_action" {
					t.Errorf("%s: an unknown word: %d %v", stage, c, r)
				} else {
					offered := map[string]bool{}
					for _, o := range asList(r["actions"]) {
						om, _ := o.(map[string]interface{})
						offered[fmt.Sprint(om["action"])] = true
					}
					for _, ty := range actionTypesOpenTo(id) {
						if !offered[ty.Name] {
							t.Errorf("%s: %s is open to the character (actionTypesOpenTo) but the refusal's list of actions leaves it out", stage, ty.Name)
						}
						delete(offered, ty.Name)
					}
					for name := range offered {
						t.Errorf("%s: the refusal's list offers %s, which the table does not open to the character", stage, name)
					}
				}
				// the types the character has apart from the state a type is used in (While): those are put in their state first
				f := actorFactsOf(id)
				var types []actionType
				for _, ty := range actionTypes {
					if r := ty.refusal(&f); r == nil || r.Code == "wrong_state" {
						types = append(types, ty)
					}
				}
				for _, ty := range types {
					if ty.Send == sendPending || ty.Send == sendFamily {
						continue
					}
					reset()
					if ty.While != "" && ty.While != "not_raging" {
						setup, known := featureStateSetups[ty.Name]
						if !known {
							mu <- struct{}{}
							report = append(report, fmt.Sprintf("| %s | %d | %s | - | state not set up (%s) | |", cl.class, L, ty.Name, ty.While))
							<-mu
							continue
						}
						setup["character_id"] = id
						if c, r := pgCall(t, "POST", "/api/action", player, setup); c >= 400 {
							t.Errorf("%s: the state of %s (%v): %d %v", stage, ty.Name, setup["action"], c, r)
							continue
						}
						pgExec(t, `UPDATE characters SET action_used=false, bonus_action_used=false, attacks_remaining=NULL WHERE id=$1`, id)
					}
					path, method, body := "/api/action", "POST", ty.exampleBody()
					if ty.Send == sendRoute {
						parts := strings.SplitN(ty.Example, " ", 3)
						method, path = parts[0], parts[1]
						body = nil
						if len(parts) == 3 {
							json.Unmarshal([]byte(parts[2]), &body)
						}
					}
					if body == nil {
						body = map[string]interface{}{}
					}
					// a monster of the fight has a negative id in a request; the examples' character ids belong to other campaigns
					for k, v := range body {
						if f, ok := v.(float64); ok && (k == "target_id" || k == "target_character_id") {
							body[k] = map[bool]int{true: ogre, false: id}[f < 0]
						}
					}
					if ty.takes("target_id") && body["target_id"] == nil && ty.hasTag("attack") {
						body["target_id"] = ogre
					}
					body["character_id"] = id
					// the examples name a longsword, Sacred Flame and a Potion of Healing the character may not have: what it has
					switch ty.Name {
					case "attack", "offhand_attack":
						body["description"] = "I attack with what I am holding"
					case "cast":
						held := append(jsonListOf(id, "cantrips"), jsonListOf(id, "prepared_spells")...)
						if len(held) == 0 {
							mu <- struct{}{}
							report = append(report, fmt.Sprintf("| %s | %d | cast | - | no spell to cast | |", cl.class, L))
							<-mu
							continue
						}
						body["spell_slug"] = held[0]
					case "use_item":
						pgCall(t, "POST", "/api/gm/give-item", gm, map[string]interface{}{"campaign_id": camp, "character_id": id, "item_name": "Potion of Healing"})
					case "stabilize":
						continue // needs a second, dying party member: its own tests
					}
					if ty.Send != sendRoute {
						body["action"] = ty.Name
					}
					before := featureSweepState(camp, id)
					code, reply := pgCall(t, method, path, player, body)
					after := featureSweepState(camp, id)
					text := fmt.Sprint(reply)
					verdict := "resolved"
					switch {
					case code >= 500:
						verdict = "FAIL 5xx"
					case code >= 400 && featureNotYours(reply):
						verdict = "FAIL the table lists it, the resolver refuses it as not the character's"
					case code >= 400:
						verdict = "refused by state"
					case before == after && reply["result"] == nil && reply["message"] == nil:
						verdict = "FAIL changed nothing and said nothing"
					case before == after:
						verdict = "told only"
					}
					if dbg := os.Getenv("FEATURE_ACTIONS_DEBUG"); dbg == ty.Name {
						t.Logf("%s level %d %s: %d %s\nbefore %s\nafter  %s", cl.class, L, ty.Name, code, text, before, after)
					}
					// a result that reads as a refusal but isn't one ("Cannot ...") is played as an action: it spends the action's cost
					if code < 400 && !strings.HasPrefix(fmt.Sprint(reply["result"]), "Cannot ") && refusalWording.MatchString(fmt.Sprint(reply["result"])) {
						verdict = "FAIL a refusal worded as a result (not \"Cannot ...\"): treated as an action played"
					}
					short := text
					if len(short) > 160 {
						short = short[:160]
					}
					line := fmt.Sprintf("| %s | %d | %s | %d | %s | %s |", cl.class, L, ty.Name, code, verdict, strings.ReplaceAll(short, "|", "/"))
					mu <- struct{}{}
					report = append(report, line)
					<-mu
					if strings.HasPrefix(verdict, "FAIL") {
						t.Errorf("%s: %s: %s: %d %s", stage, ty.Name, verdict, code, short)
					}
					if code < 400 && ty.Send != sendRoute {
						// the same request again in the same turn, nothing reset: the features that may be used once a turn or have a use less
						// must refuse (a 4xx, or "Cannot ..."), never answer success having done nothing
						pgExec(t, `UPDATE characters SET action_used=false, bonus_action_used=false, reaction_used=false, attacks_remaining=NULL, slot_spent_turn='' WHERE id=$1`, id)
						code2, reply2 := pgCall(t, method, path, player, body)
						res2 := fmt.Sprint(reply2["result"])
						if code2 < 400 && !strings.HasPrefix(res2, "Cannot ") && refusalWording.MatchString(res2) {
							msg := res2
							if len(msg) > 140 {
								msg = msg[:140]
							}
							mu <- struct{}{}
							report = append(report, fmt.Sprintf("| %s | %d | %s | %d | FAIL again: a refusal worded as a result | %s |", cl.class, L, ty.Name, code2, strings.ReplaceAll(msg, "|", "/")))
							<-mu
							t.Errorf("%s: %s sent again: a refusal worded as a result (not \"Cannot ...\"): %d %s", stage, ty.Name, code2, msg)
						}
					}
				}
			}
			for L := 1; L <= 20; L++ {
				if L > 1 {
					stage := fmt.Sprintf("%s level %d", cl.class, L)
					if c, j := pgCall(t, "POST", "/api/gm/update-character", gm, map[string]interface{}{"campaign_id": camp, "character_id": id, "level": L}); c != 200 || j["error"] != nil {
						t.Fatalf("%s: level step: %d %v", stage, c, j)
					}
					followSetup(t, player, id, stage)
				}
				if levels[L] {
					sweep(L)
				}
			}
		})
	}
	t.Cleanup(func() {
		if out := os.Getenv("FEATURE_ACTIONS_OUT"); out != "" {
			sort.Strings(report)
			os.WriteFile(out, []byte("| class | level | type | code | verdict | reply |\n|---|---|---|---|---|---|\n"+strings.Join(report, "\n")+"\n"), 0o644)
		}
	})
}
