package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agentrpg/agentrpg/docs"
)

// When each class changes its prepared spells and cantrips is spellChanges (SRD 5.2.1 class features; HANDOFF item 9): the Wizard
// replaces a cantrip after a Long Rest, never on gaining a level; the five other cantrip casters on gaining a level.
func TestSpellChangesFollowTheSRD(t *testing.T) {
	want := map[string][2]string{
		"bard": {oneOnLevelUp, oneOnLevelUp}, "cleric": {allOnLongRest, oneOnLevelUp}, "druid": {allOnLongRest, oneOnLevelUp},
		"paladin": {oneOnLongRest, ""}, "ranger": {oneOnLongRest, ""}, "sorcerer": {oneOnLevelUp, oneOnLevelUp},
		"warlock": {oneOnLevelUp, oneOnLevelUp}, "wizard": {allOnLongRest, oneOnLongRest},
	}
	for c, w := range want {
		if got := spellChanges[c]; got.Spells != w[0] || got.Cantrips != w[1] {
			t.Errorf("%s: %+v, want spells %s, cantrips %s", c, got, w[0], w[1])
		}
	}
	if r := spellChangeRule("wizard", true); r != "a Wizard replaces one cantrip after a Long Rest" {
		t.Errorf("the Wizard's cantrip rule reads %q", r)
	}
}

// The guide and the prepare request's reference say the table's summary, word for word.
func TestSpellChangeSummaryInGuides(t *testing.T) {
	sum := spellChangeSummary()
	if !strings.Contains(docs.Skill, sum) {
		t.Errorf("docs/skill.md doesn't say the spell change rules as the server has them: %q", sum)
	}
	src, _ := os.ReadFile("character_spells.go")
	if !strings.Contains(string(src), sum) {
		t.Errorf("the prepare request's @Description doesn't say %q", sum)
	}
}

// Through the server: a Wizard gets its cantrip change from a Long Rest (one, however many rests) and none from a level; a Bard
// from a level. A prepare list whose every spell is always prepared is refused and changes nothing.
func TestPostgresSpellChangesAndAlwaysPrepared(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "pg caster")
	c, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Caster %d", time.Now().UnixNano()%1e9)))
	if c != 200 {
		t.Fatalf("create: %d %v", c, j)
	}
	id := int(j["character_id"].(float64))
	swaps := func() (spells, cantrips int, ready bool) {
		db.QueryRow(`SELECT COALESCE(spell_swaps,0), COALESCE(cantrip_swaps,0), COALESCE(prepare_ready,false) FROM characters WHERE id = $1`, id).Scan(&spells, &cantrips, &ready)
		return
	}
	reset := func() {
		pgExec(t, `UPDATE characters SET spell_swaps = 0, cantrip_swaps = 0, prepare_ready = false WHERE id = $1`, id)
	}

	reset()
	grantSpellChangesOnLevelUp(id, "wizard", 2)
	if s, ca, _ := swaps(); s != 0 || ca != 0 {
		t.Errorf("a Wizard's level-up gives no spell or cantrip change: %d, %d", s, ca)
	}
	grantSpellChangesOnLongRest(id, "wizard")
	grantSpellChangesOnLongRest(id, "wizard")
	if s, ca, ready := swaps(); s != 0 || ca != 1 || !ready {
		t.Errorf("a Wizard's Long Rests: a new list and one cantrip change: %d, %d, %v", s, ca, ready)
	}
	reset()
	grantSpellChangesOnLevelUp(id, "bard", 1)
	if s, ca, _ := swaps(); s != 1 || ca != 1 {
		t.Errorf("a Bard's level-up: one spell and one cantrip change: %d, %d", s, ca)
	}

	pgExec(t, `UPDATE characters SET class = 'cleric', subclass = 'life', level = 3, prepared_spells = '["guiding-bolt"]' WHERE id = $1`, id)
	c, j = pgCall(t, "POST", fmt.Sprintf("/api/characters/%d/prepare", id), tok, map[string]interface{}{"spells": []string{"bless"}})
	if c != 400 || j["error"] != "only_always_prepared" || !strings.Contains(strings.ToLower(fmt.Sprint(j["message"])), "bless") {
		t.Errorf("a list of only always-prepared spells: %d %v", c, j)
	}
	var list string
	db.QueryRow(`SELECT prepared_spells::text FROM characters WHERE id = $1`, id).Scan(&list)
	if !strings.Contains(list, "guiding-bolt") {
		t.Errorf("the refused list changed the prepared spells: %s", list)
	}
}
