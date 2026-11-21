package main

// Regression probes first demonstrated against archive 570f8bb.
// They assert the shared lifecycle contract and are retained after its repair.
import (
	"fmt"
	"strings"
	"testing"
)

type reviewConcentrationCase struct {
	gm, player         string
	camp, char, caster int
	key                string
}

func reviewConcentrationSetup(t *testing.T, configure ...func(*srdMonster2024)) reviewConcentrationCase {
	t.Helper()
	testPostgres(t)
	gm, player, camp, char, _, _ := companionReactionFixture(t)
	m := compendiumFixture()
	m.Name = "Review Focus Caster"
	m.Type = "Aberration"
	m.HP = 10000
	a := m.Abilities["con"]
	a.Save = -10
	m.Abilities["con"] = a
	m.Actions = []srdMonsterEntry{{Name: "Spellcasting", Text: "The caster casts one of the following spells, using Intelligence as the spellcasting ability (spell save DC 99, +60 to hit with spell attacks): At Will: Hold Person, Hold Monster."}}
	for _, apply := range configure {
		apply(&m)
	}
	code, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented review fixture", "monsters": []srdMonster2024{m}})
	if code != 201 {
		t.Fatalf("import: %d %v", code, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	code, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": m.Name, "monster_key": key, "initiative": -100}}})
	if code != 200 {
		t.Fatalf("combat add: %d %v", code, j)
	}
	ids := monsterIDsByKeyOrName(camp, m.Name)
	if len(ids) != 1 {
		t.Fatalf("ids: %v", ids)
	}
	pgExec(t, `UPDATE characters SET species='Human',hp=500,max_hp=500 WHERE id=$1`, char)
	return reviewConcentrationCase{gm, player, camp, char, ids[0], key}
}
func (f reviewConcentrationCase) cast(t *testing.T, target int) {
	t.Helper()
	code, j := pgCall(t, "POST", "/api/gm/narrate", f.gm, map[string]interface{}{"campaign_id": f.camp, "narration": "Review cast.", "monster_action": map[string]interface{}{"monster": "Review Focus Caster", "action": "Spellcasting", "use": "Hold Person", "target_ids": []int{target}}})
	if code != 200 {
		t.Fatalf("cast: %d %v", code, j)
	}
	if monsterConcentrationOn(f.camp, f.caster) != "Hold Person" {
		t.Fatalf("cast did not record concentration: %v", j)
	}
	if f.held(target) == 0 {
		t.Fatalf("cast did not store target condition: %v", j)
	}
}
func (f reviewConcentrationCase) held(target int) (n int) {
	for _, c := range loadConditionRecords(f.camp, target) {
		if c.SourceID == f.caster && strings.HasSuffix(c.Source, "(Hold Person)") {
			n++
		}
	}
	return
}
func (f reviewConcentrationCase) damage(t *testing.T) {
	t.Helper()
	code, j := pgCall(t, "POST", "/api/gm/adjust-monster", f.gm, map[string]interface{}{"campaign_id": f.camp, "target_id": f.caster, "damage": 100})
	if code != 200 {
		t.Fatalf("damage: %d %v", code, j)
	}
}
func (f reviewConcentrationCase) decide(t *testing.T, decision string) (int, map[string]interface{}) {
	t.Helper()
	var token string
	db.QueryRow(`SELECT decision_token FROM monster_concentration WHERE lobby_id=$1 AND monster_id=$2`, f.camp, f.caster).Scan(&token)
	return pgCall(t, "POST", "/api/gm/monster-concentration", f.gm, map[string]interface{}{"campaign_id": f.camp, "combatant_id": f.caster, "decision": decision, "decision_token": token})
}
func TestReviewConcentrationRecastDropsPreviousTargets(t *testing.T) {
	f := reviewConcentrationSetup(t)
	_, tok := pgAgent(t, "review second target")
	code, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Review second %d", f.camp)))
	if code != 200 {
		t.Fatalf("character: %d %v", code, j)
	}
	second := int(j["character_id"].(float64))
	pgFinish(t, tok, second)
	pgExec(t, `UPDATE characters SET lobby_id=$1,species='Human',hp=500,max_hp=500 WHERE id=$2`, f.camp, second)
	f.cast(t, f.char)
	f.cast(t, second)
	if got := f.held(f.char); got != 0 {
		t.Fatalf("recasting Hold Person on a new target retained %d condition(s) on the first target; only the new cast may remain", got)
	}
}
func TestReviewConcentrationQueuedSaveUsesDamageTimeStats(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	if bonus, _, _ := saveModifier(f.camp, f.caster, "con"); bonus != -10 {
		t.Fatalf("initial bonus %d", bonus)
	}
	f.damage(t)
	if monsterConcentrationPending(f.camp, f.caster) == "" {
		t.Fatal("control: first failure must be pending")
	}
	f.damage(t) // second save must use -10 and fail, even if the state changes later
	// Test-only state change. The public compendium remains immutable.
	pgExec(t, `UPDATE custom_monsters SET definition=jsonb_set(definition,'{abilities,con,save}',to_jsonb(30)),stats=jsonb_set(stats,'{saves,con}',to_jsonb(30)) WHERE key=$1`, f.key)
	if bonus, _, _ := saveModifier(f.camp, f.caster, "con"); bonus != 30 {
		t.Fatalf("changed-state control did not reach runtime: %d", bonus)
	}
	code, j := f.decide(t, "legendary_resistance")
	if code != 200 {
		t.Fatalf("decision: %d %v", code, j)
	}
	if monsterConcentrationPending(f.camp, f.caster) == "" {
		t.Fatalf("queued damage-time failure became a success after Constitution changed; one further failed save must remain pending: %v", j)
	}
}
func (f reviewConcentrationCase) rejectStorage(t *testing.T, event string) {
	t.Helper()
	name := fmt.Sprintf("review_concentration_%d", f.camp)
	pgExec(t, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected review failure'; END $$`, name))
	pgExec(t, fmt.Sprintf(`CREATE TRIGGER %s BEFORE %s ON monster_concentration FOR EACH ROW WHEN (OLD.lobby_id=%d AND OLD.monster_id=%d) EXECUTE FUNCTION %s()`, name, event, f.camp, f.caster, name))
	t.Cleanup(func() {
		pgExec(t, fmt.Sprintf(`DROP TRIGGER %s ON monster_concentration`, name))
		pgExec(t, fmt.Sprintf(`DROP FUNCTION %s()`, name))
	})
}
func TestReviewConcentrationDecisionStorageFailureDoesNotPanic(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	f.damage(t)
	if monsterConcentrationPending(f.camp, f.caster) == "" {
		t.Fatal("control: no pending decision")
	}
	f.rejectStorage(t, "UPDATE")
	left := legendaryResistanceLeft(f.camp, f.caster)
	var panicValue interface{}
	code := 0
	func() { defer func() { panicValue = recover() }(); code, _ = f.decide(t, "legendary_resistance") }()
	if panicValue != nil {
		t.Fatalf("database UPDATE failure panicked instead of returning a recoverable error: %v", panicValue)
	}
	if code != 500 || legendaryResistanceLeft(f.camp, f.caster) != left || monsterConcentrationPending(f.camp, f.caster) == "" {
		t.Fatalf("failed decision must retain state and spend nothing: code=%d", code)
	}
}
func TestReviewConcentrationEndStorageFailureIsAtomic(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	f.damage(t)
	f.rejectStorage(t, "DELETE")
	code, j := f.decide(t, "end")
	if code != 500 {
		t.Fatalf("control: injected delete failure must be visible: %d %v", code, j)
	}
	if monsterConcentrationOn(f.camp, f.caster) == "" {
		t.Fatal("control: failing DELETE unexpectedly removed concentration")
	}
	if f.held(f.char) == 0 {
		t.Fatal("concentration DELETE failed but its target's condition was still removed; the state is partially committed")
	}
}

func TestReviewConcentrationDamageUsesConcentrationSaveSources(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	r := addCondition(f.camp, f.caster, condAdd{Condition: "disadv_concentration", Source: "invented concentration rider", Rounds: 2})
	if r.Err != nil || !r.Added {
		t.Fatalf("marker: %+v", r)
	}
	control := (&saveCtx{lobby: f.camp, saver: f.caster, ability: "con", dc: 30, concentration: true}).gather()
	if !control.HasDisadvantage() {
		t.Fatal("control: the shared save owner must recognize the rider")
	}
	old := saveSources
	var observed []bool
	// An observer in the existing source table changes no roll or result. It
	// records the context that the real damage route passes to the shared owner.
	saveSources = append(append([]saveSource(nil), old...), saveSource{effect: saveDis, applies: func(c *saveCtx) (bool, string) {
		if c.lobby == f.camp && c.saver == f.caster && c.ability == "con" {
			observed = append(observed, c.concentration)
		}
		return false, ""
	}})
	t.Cleanup(func() { saveSources = old })
	f.damage(t)
	if len(observed) != 1 || !observed[0] {
		t.Fatalf("damage route omitted the Concentration context, so its Disadvantage rider never applies: observed %v", observed)
	}
}
