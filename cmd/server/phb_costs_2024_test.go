package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The costs Matt approved on Nov 16 after checking the 2024 PHB (HANDOFF item 31a): Rage can't be ended at will; a Death Saving
// Throw takes no action and is made once a turn, at its start; Magical Cunning is a 1-minute rite; erasing a name from Gift of the
// Protectors is a Magic action and writing one the writer's action; ending Quivering Palm is an action or one attack of the Attack
// action, ending it harmlessly takes none; a refused action spends nothing.
func TestPHBCosts(t *testing.T) {
	if _, ok := lookupActionType("end_rage"); ok {
		t.Error("end_rage: the 2024 Rage ends only when it isn't extended, on Heavy armor or Incapacitated")
	}
	for name, want := range map[string]string{"death_save": costFree, "magical_cunning": costFree, "gift_of_the_protectors": costAction, "quivering_palm": costFree} {
		if got := getActionResourceType(name); got != want {
			t.Errorf("%s costs %s, want %s", name, got, want)
		}
	}
	for desc, want := range map[string]string{"end": "attack", "end it now": "attack", "end harmlessly": "quivering_palm", "start": "quivering_palm"} {
		if got := actionCostForms["quivering_palm"](desc); got != want {
			t.Errorf("Quivering Palm %q is spent as %q, want %q", desc, got, want)
		}
	}
	if getActionResourceType("attack") != costAction {
		t.Error("ending Quivering Palm is spent as an attack of the Attack action")
	}
	if !isRefusalResult("Cannot use Frenzy: it is a Berserker Barbarian feature from level 3.") || isRefusalResult("You hit the goblin.") {
		t.Error("a result that starts with \"Cannot \" is a refusal and spends nothing")
	}
	if r := mustType(t, "frenzy").refusal(func() *actorFacts { f := whoFacts("fighter", "", 3); return &f }()); r == nil || !isRefusalResult(r.Message) {
		t.Error("every refusal from the action table reads as a refusal")
	}
}

// The Death Saving Throw is made once a turn: a second roll in the same turn is refused; the turn's start clears it.
func TestPostgresDeathSaveOnceATurn(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "pg dying")
	c, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Dying %d", time.Now().UnixNano()%1e9)))
	if c != 200 {
		t.Fatalf("create: %d %v", c, j)
	}
	id := int(j["character_id"].(float64))
	pgExec(t, `UPDATE characters SET hp = 0, is_stable = false, is_dead = false WHERE id = $1`, id)
	first, already := rollDeathSave(id)
	if already || !strings.Contains(first, "Death saving throw") {
		t.Fatalf("the first Death Saving Throw of the turn is rolled: %q", first)
	}
	if second, already := rollDeathSave(id); !already || !isRefusalResult(second) {
		t.Errorf("a second Death Saving Throw in the same turn is refused: %q", second)
	}
	pgExec(t, `UPDATE characters SET hp = 0, is_stable = false, is_dead = false, death_save_successes = 0, death_save_failures = 0 WHERE id = $1`, id)
	resetTurnFlags(id, 30)
	if _, already := rollDeathSave(id); already {
		t.Error("the next turn's Death Saving Throw is rolled again")
	}
}

// A declaration ("other", Matt Nov 16): the GM rules on it, it is never free in a fight, and a spell cast in one is refused with
// the request to send.
func TestDeclarationControls(t *testing.T) {
	if actionCostForms["declare"]("I hold the line") != "declare" || actionCostForms["declare"]("as a bonus action I shout") != "declare_bonus" {
		t.Error("a declaration takes the action, or the Bonus Action when it says so")
	}
	for slug, name := range map[string]string{"sacred-flame": "Sacred Flame", "healing-word": "Healing Word"} { // without the database
		if _, ok := srdSpellsMemory[slug]; !ok {
			srdSpellsMemory[slug] = SRDSpell{Name: name}
			t.Cleanup(func() { delete(srdSpellsMemory, slug) })
		}
	}
	// speaking of casting without a spell is the character's own words (I-CALLA-15)
	for _, d := range []string{"With nothing left in her to cast, Calla kneels and says a plain harvest prayer to Chauntea aloud", "the fisherman casts his net"} {
		if why := declarationCheck(nil, d); why != "" {
			t.Errorf("a declaration that names no spell goes to the GM: %q refused: %s", d, why)
		}
	}
	for _, d := range []string{"Calla casts Sacred Flame at the rat swarm", "I spend a spell slot to heal Aldren", "casting Healing Word on myself"} {
		if declarationProblem(0, d) == "" {
			t.Errorf("a declaration that casts a spell is refused: %q", d)
		}
	}
	if why := declarationProblem(0, "Calla casts Sacred Flame at the rat swarm"); !strings.Contains(why, `"action": "cast"`) {
		t.Errorf("the refusal names the request to send: %q", why)
	}
	cleric := []actionType{mustType(t, "divine_spark"), mustType(t, "turn_undead"), mustType(t, "speak")}
	for _, d := range []string{"Calla uses Divine Spark to heal Aldren", "I spend my Channel Divinity on Aldren", "I turn undead"} {
		if declarationCheck(cleric, d) == "" {
			t.Errorf("a declaration that uses a feature the character has, or its resource, is refused: %q", d)
		}
	}
	for _, d := range []string{"I take point along the wall at Cuth's pace and stop at the last cover", "Calla keeps her Light covered and whispers to Millie", "the rage in his eyes cools as he lowers his sword"} {
		if why := declarationCheck(cleric, d); why != "" {
			t.Errorf("a turn in the character's own words goes to the GM: %q refused: %s", d, why)
		}
	}
}
