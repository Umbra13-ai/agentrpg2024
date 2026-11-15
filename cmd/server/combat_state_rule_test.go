package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

// order parses a turn order as it is stored.
func testOrder(t *testing.T, raw string) []map[string]interface{} {
	t.Helper()
	var o []map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatal(err)
	}
	return o
}

func orderIDs(f *fight) []int {
	ids := make([]int, len(f.Order))
	for i, e := range f.Order {
		ids[i] = asInt(e["id"])
	}
	return ids
}

// TestNextTurnSkipsCreaturesWithoutATurn: the next turn passes over dead characters and monsters at 0 Hit Points, wrapping into
// the next round (the cases of the earlier skip test, now the one rule every turn change uses).
func TestNextTurnSkipsCreaturesWithoutATurn(t *testing.T) {
	dead := map[int]bool{34: true, 35: true}
	noTurn := func(id int) bool { return dead[id] }
	for _, tc := range []struct {
		name                           string
		order                          string
		oldIndex, wantIndex, wantRound int
	}{
		{"skip dead", `[{"id":32},{"id":34},{"id":33}]`, 0, 2, 1},
		{"wrap past dead", `[{"id":34},{"id":33},{"id":32}]`, 2, 1, 2},
		{"consecutive dead", `[{"id":32},{"id":34},{"id":35},{"id":33}]`, 0, 3, 1},
	} {
		f := &fight{Round: 1, Index: tc.oldIndex, Order: testOrder(t, tc.order)}
		f.nextTurn(f.Index+1, noTurn)
		if f.Index != tc.wantIndex || f.Round != tc.wantRound {
			t.Errorf("%s: index %d round %d, want %d %d", tc.name, f.Index, f.Round, tc.wantIndex, tc.wantRound)
		}
	}
}

// TestNextTurnDropsThiefsReflexesAfterRoundOne: "two turns during the first round of any combat": the second turn's entry leaves
// the order when round 2 starts, and nothing else does.
func TestNextTurnDropsThiefsReflexesAfterRoundOne(t *testing.T) {
	f := &fight{Round: 1, Index: 2, Order: testOrder(t, `[{"id":5,"initiative":20},{"id":-1,"initiative":12},{"id":5,"name":"Thief (2nd turn)","initiative":10,"is_thiefs_reflexes_turn":true}]`)}
	f.nextTurn(f.Index+1, func(int) bool { return false })
	if !reflect.DeepEqual(orderIDs(f), []int{5, -1}) || f.Index != 0 || f.Round != 2 {
		t.Fatalf("after round 1: order %v index %d round %d", orderIDs(f), f.Index, f.Round)
	}
	f = &fight{Round: 1, Index: 0, Order: testOrder(t, `[{"id":5},{"id":5,"is_thiefs_reflexes_turn":true},{"id":-1}]`)}
	f.nextTurn(1, func(int) bool { return false })
	if len(f.Order) != 3 || f.Index != 1 {
		t.Fatalf("in round 1 the second turn stays: order %v index %d", orderIDs(f), f.Index)
	}
}

// TestNextTurnSkipsMonstersAtZero: a monster at 0 Hit Points has no turn (read from the locked order).
func TestNextTurnSkipsMonstersAtZero(t *testing.T) {
	order := testOrder(t, `[{"id":1},{"id":-1,"hp":0},{"id":-2,"hp":3}]`)
	f := &fight{Round: 1, Index: 0, Order: order}
	f.nextTurn(1, func(id int) bool { return id < 0 && asInt(f.Order[1]["hp"]) <= 0 && id == -1 })
	if f.Index != 2 {
		t.Fatalf("index %d, want 2", f.Index)
	}
}

// TestTurnCheck: a turn change is refused on an empty or broken order, a turn other than the expected one, a change already
// under way, and the caller's own check.
func TestTurnCheck(t *testing.T) {
	started := time.Now().Add(-5 * time.Hour)
	base := func() *fight {
		return &fight{Round: 2, Index: 1, StartedAt: started, Order: testOrder(t, `[{"id":-1},{"id":32},{"id":33}]`)}
	}
	for _, index := range []int{-1, 3, 9} {
		f := base()
		f.Index = index
		if code, _ := (turnHow{}).check(f); code != "invalid_combat_turn" {
			t.Errorf("index %d: %q", index, code)
		}
	}
	if code, _ := (turnHow{}).check(&fight{}); code != "no_combatants" {
		t.Errorf("empty order: %q", code)
	}
	if code, _ := (turnHow{}).check(base()); code != "" {
		t.Errorf("a usable turn: %q", code)
	}
	for name, ref := range map[string]turnRef{"round": {1, 1, started}, "index": {2, 2, started}, "clock": {2, 1, started.Add(time.Second)}} {
		ref := ref
		if code, _ := (turnHow{expect: &ref}).check(base()); code != "turn_already_advanced" {
			t.Errorf("another %s: %q", name, code)
		}
	}
	if code, _ := (turnHow{expect: &turnRef{2, 1, started}}).check(base()); code != "" {
		t.Errorf("the expected turn: %q", code)
	}
	held := base()
	held.claimHeld = true
	if code, _ := (turnHow{}).check(held); code != "turn_change_in_progress" {
		t.Errorf("a change under way: %q", code)
	}
	if code, _ := (turnHow{remove: 33}).check(base()); code != "turn_already_advanced" {
		t.Errorf("removing a creature whose turn it isn't: %q", code)
	}
	if code, _ := (turnHow{allow: func(*fight) (string, string) { return "not_player_turn", "" }}).check(base()); code != "not_player_turn" {
		t.Errorf("the caller's check: %q", code)
	}
}

// TestPlaceKeepsHolderAndEntries: a new combatant goes to its Initiative place; whose turn it is and every field of every other
// entry stay as they were (the earlier add test's cases).
func TestPlaceKeepsHolderAndEntries(t *testing.T) {
	for _, raw := range []string{
		`[{"id":-1,"name":"Dragon","initiative":20,"conditions":["stunned"],"future_metadata":{"remaining":2},"legendary_actions_used":2},{"id":-2,"name":"Goblin","initiative":10,"conditions":["prone"]}]`,
		`[{"id":-1,"name":"Goblin","initiative":20},{"id":-2,"name":"Goblin","initiative":10}]`,
	} {
		f := &fight{Round: 3, Index: 1, Order: testOrder(t, raw)}
		before := testOrder(t, raw)
		f.place(map[string]interface{}{"id": f.newMonsterID(), "name": "Goblin", "initiative": 30})
		if len(f.Order) != 3 || asInt(f.Order[0]["id"]) != -3 || asInt(f.current()["id"]) != -2 {
			t.Fatalf("order %v index %d", orderIDs(f), f.Index)
		}
		for _, b := range before {
			_, a := f.entry(asInt(b["id"]))
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("an entry changed: %v -> %v", b, a)
			}
		}
	}
	// ties go after the entries already there, Dexterity breaks them
	f := &fight{Order: testOrder(t, `[{"id":1,"initiative":15,"dex_score":12},{"id":2,"initiative":10}]`)}
	f.place(map[string]interface{}{"id": -1, "initiative": 15, "dex_score": 12})
	f.place(map[string]interface{}{"id": -2, "initiative": 15, "dex_score": 18})
	if !reflect.DeepEqual(orderIDs(f), []int{-2, 1, -1, 2}) || f.Index != 1 {
		t.Fatalf("order %v index %d", orderIDs(f), f.Index)
	}
}

// TestMonsterIDsAreNeverReusedInAFight: add, remove, add: the second monster never gets the first one's id, which would give it
// the removed monster's conditions and effects (audit AUD-39).
func TestMonsterIDsAreNeverReusedInAFight(t *testing.T) {
	f := &fight{Order: testOrder(t, `[{"id":1,"initiative":10}]`)}
	a := f.newMonsterID()
	f.place(map[string]interface{}{"id": a, "initiative": 5})
	b := f.newMonsterID()
	f.place(map[string]interface{}{"id": b, "initiative": 5})
	f.remove(b)
	c := f.newMonsterID()
	f.remove(a)
	d := f.newMonsterID()
	seen := map[int]bool{}
	for _, id := range []int{a, b, c, d} {
		if seen[id] || id >= 0 {
			t.Fatalf("ids %d %d %d %d: one given twice", a, b, c, d)
		}
		seen[id] = true
	}
}

// TestRemoveKeepsTheTurn: taking a combatant out keeps whose turn it is and every field of the others; every entry of the id
// goes (a Thief's second turn too); taking out the current one leaves the index on the next (the earlier remove test's cases).
func TestRemoveKeepsTheTurn(t *testing.T) {
	dragon := `{"id":-2,"name":"Dragon","initiative":15,"dex_score":10,"is_monster":true,"monster_key":"adult-red-dragon","hp":99,"max_hp":256,"ac":19,"conditions":["slowed"],"custom":{"phase":2}}`
	for _, tc := range []struct {
		name                        string
		order                       string
		index, wantIndex, wantRound int
	}{
		{"before current", `[{"id":-1,"name":"Minion"},` + dragon + `]`, 1, 0, 3},
		{"after current", `[` + dragon + `,{"id":-1,"name":"Minion"}]`, 0, 0, 3},
		{"current wraps", `[` + dragon + `,{"id":-1,"name":"Minion"}]`, 1, 0, 4},
	} {
		f := &fight{Round: 3, Index: tc.index, Order: testOrder(t, tc.order)}
		removed, wasCurrent := f.remove(-1)
		if wasCurrent {
			f.nextTurn(f.Index, func(int) bool { return false }) // the turn passes (advanceTurn with remove)
		}
		if len(removed) != 1 || !reflect.DeepEqual(f.Order, testOrder(t, "["+dragon+"]")) || f.Index != tc.wantIndex || f.Round != tc.wantRound {
			t.Errorf("%s: order %v index %d round %d", tc.name, f.Order, f.Index, f.Round)
		}
	}
	f := &fight{Round: 1, Index: 2, Order: testOrder(t, `[{"id":5},{"id":-1},{"id":7},{"id":5,"is_thiefs_reflexes_turn":true}]`)}
	if removed, cur := f.remove(5); len(removed) != 2 || cur || !reflect.DeepEqual(orderIDs(f), []int{-1, 7}) || f.Index != 1 {
		t.Fatalf("Thief removed: %d entries, current %v, order %v index %d", len(removed), cur, orderIDs(f), f.Index)
	}
}

// TestSetInitiativeKeepsTheTurn: a new Initiative moves the combatant, not the turn.
func TestSetInitiativeKeepsTheTurn(t *testing.T) {
	f := &fight{Index: 1, Order: testOrder(t, `[{"id":1,"initiative":20},{"id":2,"initiative":15},{"id":-1,"initiative":10}]`)}
	f.setInitiative(-1, 25)
	if !reflect.DeepEqual(orderIDs(f), []int{-1, 1, 2}) || asInt(f.current()["id"]) != 2 {
		t.Fatalf("order %v index %d", orderIDs(f), f.Index)
	}
}

// TestMonsterVitalsFollowTheHitPointRule: a monster's damage and healing are game/hit_points.go's rule: Temporary Hit Points
// first, never below 0 or above the maximum, healing from 0; no Death Saving Throws are written to the entry.
func TestMonsterVitalsFollowTheHitPointRule(t *testing.T) {
	e := map[string]interface{}{"id": -1, "hp": 10.0, "max_hp": 20.0, "temp_hp": 4.0, "ac": 13.0}
	v := monsterVitals(e)
	r := game.TakeDamage(v, 7, false, false)
	putMonsterVitals(e, v, r.After)
	if asInt(e["hp"]) != 7 || asInt(e["temp_hp"]) != 0 || asInt(e["ac"]) != 13 || len(e) != 5 {
		t.Fatalf("after 7 damage: %v", e)
	}
	v = monsterVitals(e)
	r = game.TakeDamage(v, 50, false, false)
	putMonsterVitals(e, v, r.After)
	if asInt(e["hp"]) != 0 || len(e) != 5 {
		t.Fatalf("after 50 damage: %v", e)
	}
	v = monsterVitals(e)
	h, _ := game.Heal(v, 100, 0)
	putMonsterVitals(e, v, h)
	if asInt(e["hp"]) != 20 {
		t.Fatalf("healed from 0 past the maximum: %v", e)
	}
}

// ---------------------------------------------------------------- source tests

func serverSources(t *testing.T) map[string]string {
	t.Helper()
	files, _ := filepath.Glob("*.go")
	out := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[f] = string(b)
	}
	return out
}

// TestNoOtherTurnOrderWriter: only the combat-state owner writes a fight's turn order, whose turn it is, the round, the turn clock,
// the monster id counter and the mark of a turn change (combat_state_2024.go, turn_advance_2024.go). Before Nov 15 2026 the turn
// order had 14 writers, 9 without a lock, and the turn was advanced in four places (audit AUD-09, AUD-10, AUD-24 C-7, AUD-39).
func TestNoOtherTurnOrderWriter(t *testing.T) {
	stmt := regexp.MustCompile("(?is)(UPDATE\\s+combat_state\\b[^`]*|INSERT\\s+INTO\\s+combat_state\\b[^`]*)")
	cols := regexp.MustCompile(`(?i)\b(turn_order|current_turn_index|round_number|turn_started_at|last_monster_id|turn_claim)\b`)
	owners := map[string]bool{"combat_state_2024.go": true, "turn_advance_2024.go": true, "database_init.go": true}
	for f, src := range serverSources(t) {
		if owners[f] {
			continue
		}
		for _, m := range stmt.FindAllStringIndex(src, -1) {
			if c := cols.FindString(src[m[0]:m[1]]); c != "" {
				t.Errorf("%s:%d writes combat_state.%s itself: use the combat-state owner (changeFight, advanceTurn)", f, strings.Count(src[:m[0]], "\n")+1, c)
			}
		}
	}
}

// TestTurnHooksRunOnlyInTheTurnOwner: the rules of a turn's end and start are called only from the hook tables of
// turn_advance_2024.go, so every way a turn changes runs all of them, once (audit AUD-10: each of the four paths had its own subset).
func TestTurnHooksRunOnlyInTheTurnOwner(t *testing.T) {
	hooks := []string{"endTurnEffects", "startTurnEffects", "moreTurnHooks", "survivorRegen", "decrementSacredWeapon", "decrementHolyNimbus",
		"decrementCountercharm", "clearAllMultiattackDefenseHits", "clearOAWindows", "setLastTurnEnded", "nextLivingCombatTurn"}
	for f, src := range serverSources(t) {
		if f == "turn_advance_2024.go" {
			continue
		}
		for _, h := range hooks {
			call := regexp.MustCompile(`(?m)^[^/\n]*[^\w.]` + h + `\(`)
			def := regexp.MustCompile(`(?m)^func ` + h + `\(`)
			for _, m := range call.FindAllStringIndex(src, -1) {
				if !def.MatchString(src[m[0]:m[1]]) {
					t.Errorf("%s:%d calls %s: a turn's rules run only from turn_advance_2024.go's hook tables (advanceTurn)", f, strings.Count(src[:m[0]], "\n")+1, h)
				}
			}
		}
	}
}

// TestNoOtherCompanionHPWriter: a companion's Hit Points change only through the companion Hit Point owner (monster_hp_2024.go).
func TestNoOtherCompanionHPWriter(t *testing.T) {
	update := regexp.MustCompile("(?is)UPDATE\\s+companions\\b[^`]*")
	assign := regexp.MustCompile(`(?i)(\bSET\s+|,\s*)(max_)?hp\s*=`)
	for f, src := range serverSources(t) {
		if f == "monster_hp_2024.go" {
			continue
		}
		for _, m := range update.FindAllStringIndex(src, -1) {
			if assign.MatchString(src[m[0]:m[1]]) {
				t.Errorf("%s:%d writes a companion's Hit Points itself: use the companion Hit Point owner (monster_hp_2024.go)", f, strings.Count(src[:m[0]], "\n")+1)
			}
		}
	}
}
