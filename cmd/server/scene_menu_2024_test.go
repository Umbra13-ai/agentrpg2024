package main

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"encoding/json"
)

// pgRawGet: a GET through the whole server, the body as written (the order of the keys matters here).
func pgRawGet(t *testing.T, path, token string) (int, []byte) {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r.RemoteAddr = fmt.Sprintf("10.8.%d.%d:1234", time.Now().UnixNano()%250, time.Now().UnixNano()/250%250)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	newServer("").Handler.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}

func actionWords(list interface{}) []string {
	var out []string
	l, _ := list.([]interface{})
	for _, e := range l {
		if m, ok := e.(map[string]interface{}); ok {
			if m["action"] == nil { // at 0 Hit Points the long list is the one entry for the death save, named by name
				out = append(out, fmt.Sprint(m["name"]))
				continue
			}
			out = append(out, fmt.Sprint(m["action"]))
		}
	}
	return out
}

// Plan v2.1, S3 (playtest S-CALLA-11, S-GM-18, S-ALDEN-CLARITY-01/04): my-turn's menu fits the scene and hides nothing. For every class
// at levels 1, 5, 11 and 20, in exploration, in a fight, at 0 Hit Points, with and without a hurt ally: now plus more is every type the
// character has (the long lists' actions, bonus actions and free parts), no word is in both, outside a fight no fight-only type leads,
// in a fight an attack's example names a real enemy, and the short view leaves out the long lists and keeps `now` first.
func TestPostgresMyTurnMenuFitsTheScene(t *testing.T) {
	testPostgres(t)
	gm, toks, camp, chars, _ := pgFightWith(t, "menu", 2)
	tok, char, other := toks[0], chars[0], chars[1]
	classes := []string{"barbarian", "bard", "cleric", "druid", "fighter", "monk", "paladin", "ranger", "rogue", "sorcerer", "warlock", "wizard"}
	get := func(view string) (map[string]interface{}, []byte) {
		t.Helper()
		path := fmt.Sprintf("/api/my-turn?character_id=%d%s", char, view)
		c, raw := pgRawGet(t, path, tok)
		if c != 200 {
			t.Fatalf("my-turn %s: %d %s", path, c, raw)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return m, raw
	}
	longWords := func(m map[string]interface{}) map[string]bool {
		out := map[string]bool{}
		opts, _ := m["your_options"].(map[string]interface{})
		for _, k := range []string{"actions", "bonus_actions", "free_actions"} {
			for _, w := range actionWords(opts[k]) {
				out[w] = true
			}
		}
		return out
	}
	scenes := []struct {
		name  string
		setup func()
	}{
		{"in a fight, the ally hurt", func() {
			pgExec(t, `UPDATE characters SET hp = max_hp, is_stable = false, is_dead = false WHERE id = $1`, char)
			pgExec(t, `UPDATE characters SET hp = GREATEST(1, max_hp / 2) WHERE id = $1`, other)
		}},
		{"in a fight, nobody hurt", func() {
			pgExec(t, `UPDATE characters SET hp = max_hp, is_stable = false, is_dead = false WHERE id IN ($1, $2)`, char, other)
		}},
		{"in a fight, at 0 Hit Points", func() {
			pgExec(t, `UPDATE characters SET hp = 0, is_stable = false, is_dead = false WHERE id = $1`, char)
			pgExec(t, `UPDATE characters SET hp = max_hp WHERE id = $1`, other)
		}},
	}
	for _, cl := range classes {
		for _, lv := range []int{1, 5, 11, 20} {
			pgExec(t, `UPDATE characters SET class = $2, level = $3, subclass = NULL WHERE id = $1`, char, cl, lv)
			for _, sc := range scenes {
				sc.setup()
				m, raw := get("")
				label := fmt.Sprintf("%s %d %s", cl, lv, sc.name)
				now, more := actionWords(m["now"]), actionWords(m["more"])
				seen := map[string]bool{}
				for _, w := range append(append([]string{}, now...), more...) {
					if seen[w] {
						t.Errorf("%s: %q is in now and more, or twice", label, w)
					}
					seen[w] = true
				}
				for w := range longWords(m) {
					if !seen[w] && w != "map_move" {
						t.Errorf("%s: %q is in the long lists but neither in now nor in more: the menu hid it", label, w)
					}
				}
				if sc.name == "in a fight, at 0 Hit Points" {
					for _, w := range now {
						if w != "death_save" && w != "stabilize" && !strings.Contains(strings.Join(now, ","), "death_save") {
							t.Errorf("%s: at 0 Hit Points now holds %v", label, now)
							break
						}
					}
					if len(now) > 3 {
						t.Errorf("%s: at 0 Hit Points now holds %d types: %v", label, len(now), now)
					}
				} else if sc.name == "in a fight, nobody hurt" {
					for _, w := range now {
						if at, _ := lookupActionType(w); at.Needs == needHurt || at.Needs == needDying {
							t.Errorf("%s: %q is first with nobody hurt", label, w)
						}
					}
				}
				if len(now) > 0 && !strings.HasPrefix(string(raw), `{"is_my_turn":`) {
					t.Errorf("%s: the reply doesn't start with is_my_turn: %.60s", label, raw)
				}
				if m["elsewhere"] == nil {
					t.Errorf("%s: no elsewhere list", label)
				}
			}
		}
	}

	// in a fight with an enemy: an attack's example names the real enemy, not the placeholder
	pgExec(t, `UPDATE characters SET class = 'fighter', level = 5, hp = max_hp WHERE id = $1`, char)
	m, _ := get("")
	var atk map[string]interface{}
	for _, e := range m["now"].([]interface{}) {
		if e.(map[string]interface{})["action"] == "attack" {
			atk = e.(map[string]interface{})
		}
	}
	enemies, _ := m["situation"].(map[string]interface{})["enemy_details"].([]interface{})
	if atk == nil || len(enemies) == 0 {
		t.Fatalf("a Fighter in a fight has an attack now: %v / %d enemies", atk, len(enemies))
	}
	first := fmt.Sprint(int(enemies[0].(map[string]interface{})["id"].(float64)))
	if !strings.Contains(fmt.Sprint(atk["use"]), `"target_id": `+first) {
		t.Errorf("the attack's example doesn't name the real enemy %s: %v", first, atk["use"])
	}

	// the short view: no long lists, a smaller reply, the menu first; a level 20 caster stays under the size cap
	pgExec(t, `UPDATE characters SET class = 'wizard', level = 20 WHERE id = $1`, char)
	full, fullRaw := get("")
	short, shortRaw := get("&view=short")
	opts, _ := short["your_options"].(map[string]interface{})
	for _, k := range []string{"actions", "bonus_actions", "free_actions"} {
		if opts[k] != nil {
			t.Errorf("the short view still has %s", k)
		}
	}
	if opts, _ := full["your_options"].(map[string]interface{}); opts["actions"] == nil {
		t.Errorf("the default view lost its actions list (kept until the guides have moved)")
	}
	if len(shortRaw) >= len(fullRaw) {
		t.Errorf("the short view (%d bytes) isn't shorter than the default (%d)", len(shortRaw), len(fullRaw))
	}
	const shortCap = 24000
	if len(shortRaw) > shortCap {
		t.Errorf("a level 20 Wizard's short view is %d bytes, over the cap of %d", len(shortRaw), shortCap)
	}
	const fullCap = 40000
	if len(fullRaw) > fullCap {
		t.Errorf("a level 20 Wizard's default view is %d bytes, over the cap of %d (the old lists stay until the guides have moved)", len(fullRaw), fullCap)
	}
	t.Logf("level 20 Wizard in a fight: default %d bytes, short %d bytes", len(fullRaw), len(shortRaw))

	// outside a fight no fight-only type leads, the exploration types do, and attack is in more with its reason
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/end", camp), gm, map[string]interface{}{}); c != 200 {
		t.Fatalf("combat end: %d %v", c, j)
	}
	pgExec(t, `UPDATE characters SET class = 'fighter', level = 5, hp = max_hp WHERE id = $1`, char)
	m, _ = get("")
	nowList, _ := m["now"].([]interface{})
	for _, e := range nowList {
		w := fmt.Sprint(e.(map[string]interface{})["action"])
		if at, _ := lookupActionType(w); at.Needs == needFight || at.Needs == needEnemy {
			t.Errorf("outside a fight %q is first", w)
		}
	}
	if len(nowList) == 0 || func() bool {
		t, _ := lookupActionType(fmt.Sprint(nowList[0].(map[string]interface{})["action"]))
		return t.Explore == 0
	}() {
		t.Errorf("outside a fight the exploration types lead: %v", actionWords(m["now"]))
	}
	foundAttack := false
	for _, e := range m["more"].([]interface{}) {
		if em := e.(map[string]interface{}); em["action"] == "attack" {
			foundAttack = true
			if !strings.Contains(fmt.Sprint(em["why_not_first"]), "no enemy") {
				t.Errorf("attack's reason outside a fight: %v", em["why_not_first"])
			}
		}
	}
	if !foundAttack {
		t.Errorf("attack isn't in more outside a fight: %v", actionWords(m["more"]))
	}
	for _, e := range nowList {
		if em := e.(map[string]interface{}); em["action"] == "search" && em["uses_story_action"] == nil {
			t.Errorf("outside a fight a search says it uses the story action: %v", em)
		}
	}
}
