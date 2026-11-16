package main

// Who may use an action type is said once (HANDOFF item 31, Who step 2, approved by Matt on Nov 16): the action table's Who and
// While (actionType.refusal, action_types_2024.go). Every feature's resolver asks it (actionRefusal, or companionRefusal for a
// companion's own action), and the turn's options and the refusal of an unknown action list what it allows. Before step 2 each
// resolver had its own gate (feature_gates_2024.go, deleted); step 1's comparison proved the table and the 61 gates agreed for every
// class at every level, every SRD subclass and every feat, invocation, item, effect, companion, choice and state the table names.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// whoFacts: a character as the rule reads it, for the tests.
func whoFacts(class, sub string, level int) actorFacts {
	no := func(string) bool { return false }
	return actorFacts{class: class, subclass: sub, level: level, choices: map[string]string{}, hasFeat: no, hasInvocation: no,
		hasEffect: no, heldAttuned: func(string) (bool, bool) { return false, false }, companions: func() []companion { return nil }}
}

func mustType(t *testing.T, name string) actionType {
	t.Helper()
	at, ok := lookupActionType(name)
	if !ok {
		t.Fatalf("%s is not in the action table", name)
	}
	return at
}

// The refusal names the first thing the character lacks, with a code for each kind of condition the table can set.
func TestWhoRefusalCodes(t *testing.T) {
	code := func(name string, f actorFacts) string {
		r := mustType(t, name).refusal(&f)
		if r == nil {
			return ""
		}
		if !strings.Contains(r.Message, mustType(t, name).Title) {
			t.Errorf("%s: the refusal doesn't name the feature: %q", name, r.Message)
		}
		return r.Code
	}
	cases := []struct {
		name, want string
		f          func() actorFacts
	}{
		{"frenzy", "wrong_class", func() actorFacts { return whoFacts("fighter", "", 5) }},
		{"frenzy", "level_too_low", func() actorFacts { return whoFacts("barbarian", "berserker", 2) }},
		{"frenzy", "wrong_subclass", func() actorFacts { return whoFacts("barbarian", "", 3) }},
		{"frenzy", "", func() actorFacts { return whoFacts("barbarian", "berserker", 3) }},
		{"horde_breaker", "choice_required", func() actorFacts { return whoFacts("ranger", "hunter", 3) }},
		{"horde_breaker", "", func() actorFacts {
			f := whoFacts("ranger", "hunter", 3)
			f.choices = map[string]string{"hunters_prey": "horde_breaker"}
			return f
		}},
		{"improve_fate", "feat_required", func() actorFacts { return whoFacts("rogue", "", 19) }},
		{"improve_fate", "", func() actorFacts {
			f := whoFacts("rogue", "", 19)
			f.hasFeat = func(s string) bool { return s == "boon_of_fate" }
			return f
		}},
		{"gaze_of_two_minds", "invocation_required", func() actorFacts { return whoFacts("warlock", "", 5) }},
		{"bonus_attack", "item_not_in_hand", func() actorFacts { return whoFacts("fighter", "", 5) }},
		{"bonus_attack", "not_attuned", func() actorFacts {
			f := whoFacts("fighter", "", 5)
			f.heldAttuned = func(string) (bool, bool) { return true, false }
			return f
		}},
		{"flame_blade", "effect_required", func() actorFacts { return whoFacts("druid", "", 5) }},
		{"healing_touch", "companion_required", func() actorFacts {
			f := whoFacts("paladin", "", 5)
			f.companions = func() []companion {
				return []companion{{Kind: "steed", Form: "otherworldly-steed", CreatureType: "fey", Status: "active"}}
			}
			return f
		}},
		{"extend_rage", "wrong_state", func() actorFacts { return whoFacts("barbarian", "", 1) }},
		{"rage", "wrong_state", func() actorFacts {
			f := whoFacts("barbarian", "", 1)
			f.raging = true
			return f
		}},
		{"use_bardic_inspiration", "wrong_state", func() actorFacts { return whoFacts("fighter", "", 1) }},
	}
	for _, c := range cases {
		if got := code(c.name, c.f()); got != c.want {
			t.Errorf("%s: refusal code %q, want %q", c.name, got, c.want)
		}
	}
	steed := companion{Kind: "steed", Name: "Steed", Form: "otherworldly-steed", CreatureType: "fey", Status: "active"}
	if companionRefusal("fey_step", steed) != nil || companionRefusal("healing_touch", steed) == nil || companionRefusal("dismiss_familiar", steed) == nil {
		t.Error("a companion's own action is checked against the companion it was sent for")
	}
	until := time.Now().Add(-time.Minute)
	if bardicDieHeld(0, nil, time.Now()) || bardicDieHeld(8, &until, time.Now()) || !bardicDieHeld(8, nil, time.Now()) {
		t.Error("a Bardic Inspiration die is held until it fades")
	}
}

// Feats work through the table: every feat or Epic Boon the table names is a feat a character can take (availableFeats), stored
// under the same slug the rule asks for (hasSpecificFeat).
func TestWhoFeatsAreRealFeats(t *testing.T) {
	n := 0
	for _, at := range actionTypes {
		if f := at.Who.Feat; f != "" {
			n++
			if _, ok := availableFeats[f]; !ok {
				t.Errorf("%s: the table asks for the feat %q, which is not in availableFeats (feats_2024.go)", at.Name, f)
			}
		}
	}
	if n == 0 {
		t.Fatal("the table names no feat")
	}
}

// Every type the table limits is checked by its resolver through the table: its name is asked of actionRefusal (or passed to the
// helper that asks it), or it is a companion's own action, checked with companionRefusal. No other gate remains.
func TestEveryLimitedActionAsksTheTable(t *testing.T) {
	asked := map[string]bool{}
	quoted := regexp.MustCompile(`"([a-z_]+)"`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "actionRefusal(") || strings.Contains(line, "useWisdomFeature(") || strings.Contains(line, `feature = "`) || strings.Contains(line, `feature := "`) {
				for _, m := range quoted.FindAllStringSubmatch(line, -1) {
					asked[m[1]] = true
				}
			}
		}
	}
	notAFeatureGate := map[string]string{
		"death_save": "the dying state is the Hit Points owner's (recordDeathSave) and the turn schedule's (turnNeeds)",
	}
	for _, at := range actionTypes {
		limited := at.Who != (actionWho{}) || at.While != ""
		if !limited || notAFeatureGate[at.Name] != "" || at.Who.Companion != "" {
			continue
		}
		if !asked[at.Name] {
			t.Errorf("%s: the table limits who has it, but no resolver asks actionRefusal(%q, ...)", at.Name, at.Name)
		}
	}
	if _, err := os.Stat("feature_gates_2024.go"); err == nil {
		t.Error("feature_gates_2024.go is back: who has a feature is the action table's Who, asked through actionRefusal")
	}
}

// Feats through the whole server: a character with an Epic Boon in its feats uses the Boon's action; one without it is refused by
// the table's rule, saying which feat it needs, and nothing is spent.
func TestPostgresFeatActionsAskTheTable(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "pg feat gm")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Feats", "max_players": 2})
	if c != 200 {
		t.Fatalf("create: %d %v", c, j)
	}
	lobby := int(j["campaign_id"].(float64))
	if c, j := pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/schedule", lobby), gm, map[string]interface{}{"state": "off"}); c != 200 {
		t.Fatalf("schedule off: %d %v", c, j)
	}
	_, tok := pgAgent(t, "pg feat player")
	c, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Boon %d", time.Now().UnixNano()%1e9)))
	if c != 200 {
		t.Fatalf("create character: %d %v", c, j)
	}
	id := int(j["character_id"].(float64))
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", lobby), tok, map[string]interface{}{"character_id": id}); c != 200 {
		t.Fatalf("join: %d %v", c, j)
	}
	pgFinish(t, tok, id)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), tok, nil) // every player and the GM are ready before the start
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), gm, nil)
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", lobby), gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	c, j = pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "improve_fate", "description": "I bend fate"})
	if !strings.Contains(fmt.Sprint(j), "needs the Boon Of Fate feat.") {
		t.Errorf("without the Boon, the table's rule refuses Improve Fate: %d %v", c, j)
	}
	pgExec(t, `UPDATE characters SET feats = '["boon_of_fate"]'::jsonb, level = 19 WHERE id = $1`, id)
	c, j = pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "improve_fate", "description": "I bend fate"})
	if c >= 400 || strings.Contains(fmt.Sprint(j), "needs the Boon") {
		t.Errorf("with the Boon in its feats the character uses Improve Fate: %d %v", c, j)
	}
}
