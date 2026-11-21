package main

import (
	"fmt"
	"strings"
	"testing"
)

// SRD 5.2.1 Troll Limb Regeneration: "The limb regains 5 Hit Points at the start of each of its turns. If the limb takes Acid or Fire
// damage, this trait doesn't function on the limb's next turn. The limb dies only if it starts its turn with 0 Hit Points and doesn't
// regenerate." Through POST /api/campaigns/{id}/combat/next: a limb at 0 Hit Points keeps its turn and regenerates; Fire damage taken
// at 0 makes its next turn start a death (it stays at 0 and has no turn after that). The Shield Guardian's "if it has at least 1 Hit
// Point" is the control: at 0 it has no turn and doesn't regenerate.
func TestPostgresRegenerationFromZero(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, _, _ := companionReactionFixture(t)
	cp := fmt.Sprintf("/api/campaigns/%d/combat", camp)
	if c, j := pgCall(t, "POST", cp+"/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Limb", "monster_key": "troll-limb", "initiative": -900},
		{"name": "Guardian", "monster_key": "shield-guardian", "initiative": -950}}}); c != 200 {
		t.Fatalf("add: %d %v", c, j)
	}
	limb := monsterIDsByKeyOrName(camp, "Limb")[0]
	guard := monsterIDsByKeyOrName(camp, "Guardian")[0]
	// next turns until it is this creature's (at most the whole order twice); "" when it never came
	turnOf := func(id int) string { // the reply and the log written since the call began
		t.Helper()
		var mark int
		db.QueryRow(`SELECT COALESCE(MAX(id),0) FROM actions`).Scan(&mark)
		for n := 0; n < 12; n++ {
			_, j := pgCall(t, "POST", cp+"/next", gm, map[string]interface{}{})
			f, err := readFight(db, camp)
			if err != nil {
				t.Fatal(err)
			}
			if asInt(f.current()["id"]) == id {
				var logged string
				db.QueryRow(`SELECT COALESCE(string_agg(COALESCE(description,'')||' '||COALESCE(result,''), ' | '),'') FROM actions WHERE lobby_id=$1 AND id>$2`, camp, mark).Scan(&logged)
				return fmt.Sprint(j) + " LOG: " + logged
			}
		}
		return ""
	}
	setMonsterHP(camp, guard, 0)

	// at 0 Hit Points the limb keeps its turn and regenerates 5
	setMonsterHP(camp, limb, 0)
	if res := turnOf(limb); res == "" || monsterEntryVitals(camp, limb).HP != 5 || !strings.Contains(res, "regenerates 5") {
		t.Fatalf("the limb at 0 Hit Points: HP %d: %s", monsterEntryVitals(camp, limb).HP, res)
	}
	// the guardian at 0 never had a turn on the way, and is still at 0
	if monsterEntryVitals(camp, guard).HP != 0 {
		t.Errorf("the guardian regenerated at 0: %d", monsterEntryVitals(camp, guard).HP)
	}
	if res := turnOf(guard); res != "" {
		t.Errorf("the guardian at 0 Hit Points had a turn: %s", res)
	}

	// Fire damage at 0: the next turn start brings no regeneration, and it dies
	setMonsterHP(camp, limb, 0)
	damageMonster(camp, limb, 4, "fire")
	res := turnOf(limb)
	if res == "" || monsterEntryVitals(camp, limb).HP != 0 || !strings.Contains(res, "dies") {
		t.Fatalf("the blocked limb at 0: HP %d: %s", monsterEntryVitals(camp, limb).HP, res)
	}
	if res := turnOf(limb); res != "" || monsterEntryVitals(camp, limb).HP != 0 {
		t.Errorf("the dead limb had another turn (HP %d): %s", monsterEntryVitals(camp, limb).HP, res)
	}
}

// The catalog: the Regeneration entries that keep a creature's turn at 0 Hit Points are exactly the two trolls.
func TestRegenerationFromZeroOverTheCatalog(t *testing.T) {
	var got []string
	for _, m := range srdMonsters2024() {
		for _, tr := range m.Traits {
			if regenRe.MatchString(tr.Text) && regenFromZeroRe.MatchString(tr.Text) {
				got = append(got, m.Name)
			}
		}
	}
	if strings.Join(got, ",") != "Troll,Troll Limb" {
		t.Errorf("regeneration from 0: %v", got)
	}
}
