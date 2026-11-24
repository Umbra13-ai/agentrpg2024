package main

// The GM's area cast names each monster as the fight does, in its save line and its damage line alike (I-ALDENQA-3: the save line said
// "Monster 1").

import (
	"fmt"
	"strings"
	"testing"
)

func TestPostgresAreaCastNamesTheMonsters(t *testing.T) {
	testPostgres(t)
	gm, _, camp, hero, id := earlyRulesFixture(t, "Test Sowling", []srdMonsterEntry{{Name: "Nip", Text: "Melee Attack Roll: +4, reach 5 ft. Hit: 1 Bludgeoning damage."}})
	c, j := pgCall(t, "POST", "/api/gm/aoe-cast", gm, map[string]interface{}{"campaign_id": camp, "caster_id": hero, "spell_slug": "burning-hands", "dc": 13, "target_ids": []int{id}})
	if c != 200 {
		t.Fatalf("aoe-cast: %d %v", c, j)
	}
	got := fmt.Sprint(j["targets"])
	if !strings.Contains(got, "target_name:Test Sowling") || !strings.Contains(got, "save:Test Sowling ") || strings.Contains(got, "Monster 1") {
		t.Errorf("names: %s", got)
	}
}
