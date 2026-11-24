package main

import (
	"fmt"
	"strings"
	"testing"
)

// Rules for SRD 5.2.1 monster wordings, each checked at a character, a monster and a companion target. The monsters are invented and
// enter through the GM compendium import; their texts follow the SRD wording each rule serves (named in each test).

// importInventedMonsters imports invented monsters into the compendium and adds them to the fight; it answers each one's combat id.
func importInventedMonsters(t *testing.T, gm string, camp int, ms ...srdMonster2024) map[string]int {
	t.Helper()
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"campaign_id": camp, "source_note": "invented SRD-wording fixture", "monsters": ms})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	var add []map[string]interface{}
	for _, x := range imported["monsters"].([]interface{}) {
		mm := x.(map[string]interface{})
		key := fmt.Sprint(mm["monster_key"])
		t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
		add = append(add, map[string]interface{}{"name": fmt.Sprint(mm["name"]), "monster_key": key, "hp": 1000, "ac": 1, "initiative": -400})
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": add}); c != 200 {
		t.Fatalf("add: %d %v", c, j)
	}
	ids := map[string]int{}
	for _, m := range ms {
		ids[m.Name] = monsterIDsByKeyOrName(camp, m.Name)[0]
	}
	return ids
}

func inventedMonster(name, typ string, traits, actions []srdMonsterEntry) srdMonster2024 {
	m := compendiumFixture()
	m.Name, m.Type, m.Traits, m.Actions, m.Legendary, m.LegendaryIn, m.DmgImmune, m.Resistances = name, typ, traits, actions, nil, "", "", ""
	return m
}

func recordsOf(camp, id int, cond string) []conditionRecord {
	var out []conditionRecord
	for _, r := range loadConditionRecords(camp, id) {
		if r.Condition == cond {
			out = append(out, r)
		}
	}
	return out
}

// A timed condition that also ends on damage ("for 1 hour, until it takes damage, or until a creature within 5 feet of it takes an
// action to wake it": the Incubus's Nightmare), creature types that succeed automatically ("(demons succeed automatically)": the
// Vrock's Stunning Screech), and "While Poisoned, the target has the Paralyzed condition" (the Chuul's Paralyzing Tentacles): a record
// that ends with the first.
func TestPostgresTimedAndLinkedConditions(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000 WHERE id=$1`, charID)
	pgExec(t, `UPDATE companions SET hp=1000,max_hp=1000 WHERE id=$1`, petID)
	watcher := inventedMonster("Dusk Watcher", "Fiend", nil, []srdMonsterEntry{
		{Name: "Drowse Stare", Text: "Wisdom Saving Throw: DC 40, one creature the watcher can see within 30 feet (constructs and undead succeed automatically). Failure: The target has the Unconscious condition for 10 minutes, until it takes damage, or until a creature within 5 feet of it takes an action to wake it."},
		{Name: "Numbing Tendrils", Text: "Constitution Saving Throw: DC 40, one creature the watcher can see within 10 feet. Failure: The target has the Poisoned condition until the end of the watcher's next turn. While Poisoned, the target has the Paralyzed condition."}})
	thrall := inventedMonster("Bone Thrall", "Undead", nil, []srdMonsterEntry{{Name: "Claw", Text: "Melee Attack Roll: +1, reach 5 ft. Hit: 1 (1d1) Slashing damage."}})
	beast := inventedMonster("Plain Boar", "Beast", nil, []srdMonsterEntry{{Name: "Tusk", Text: "Melee Attack Roll: +1, reach 5 ft. Hit: 1 (1d1) Piercing damage."}})
	ids := importInventedMonsters(t, gm, camp, watcher, thrall, beast)
	names := map[int]string{ids["Plain Boar"]: "Plain Boar", ids["Bone Thrall"]: "Bone Thrall", pet: "React Pet", charID: fmt.Sprint(charID)}
	act := func(action string, target int) string {
		t.Helper()
		c, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Dusk Watcher", "action": action, "target": names[target], "confirm_targeting": true}})
		if !narrateAnswered(c, j) {
			t.Fatalf("%s at %v: %d %v", action, target, c, j)
		}
		return fmt.Sprint(j["monster_action_result"])
	}

	// Drowse Stare: 10 minutes (100 rounds), ended by damage; an Undead succeeds without a roll
	for _, tg := range []struct {
		name string
		id   int
	}{{"character", charID}, {"monster", ids["Plain Boar"]}, {"companion", pet}} {
		res := act("Drowse Stare", tg.id)
		rs := recordsOf(camp, tg.id, "unconscious")
		if len(rs) != 1 || rs[0].DurationRounds != 100 || rs[0].Notes != notesEndsOnDamage {
			t.Fatalf("%s: Drowse Stare's record %+v (%s)", tg.name, rs, res)
		}
		if !strings.Contains(res, "a creature within 5 feet of it takes an action to wake it") || !strings.Contains(res, "the GM handles") {
			t.Errorf("%s: the wake-by-action clause isn't the GM's note: %s", tg.name, res)
		}
	}
	damageCharacter(charID, 1, "fire")
	if len(recordsOf(camp, charID, "unconscious")) != 0 {
		t.Error("damage didn't end the character's Unconscious")
	}
	if res := act("Drowse Stare", ids["Bone Thrall"]); len(recordsOf(camp, ids["Bone Thrall"], "unconscious")) != 0 || !strings.Contains(strings.ToLower(res), "succeed automatically") {
		t.Errorf("an Undead didn't succeed automatically: %s", res)
	}

	// Numbing Tendrils: Poisoned and, while Poisoned, Paralyzed; the Paralyzed record ends with the Poisoned one
	for _, tg := range []int{charID, ids["Plain Boar"], pet} {
		res := act("Numbing Tendrils", tg)
		po, pa := recordsOf(camp, tg, "poisoned"), recordsOf(camp, tg, "paralyzed")
		if len(po) != 1 || len(pa) != 1 || pa[0].Notes != notesEndsWith+"poisoned" || strings.Contains(res, "While Poisoned, the target has the Paralyzed condition") {
			t.Fatalf("Numbing Tendrils at %d: poisoned %+v, paralyzed %+v (%s)", tg, po, pa, res)
		}
		endCondition(camp, tg, "poisoned", po[0].Source)
		if len(recordsOf(camp, tg, "paralyzed")) != 0 {
			t.Errorf("Paralyzed outlived the Poisoned record at %d", tg)
		}
	}
}

// The SRD Vrock's own wording: "(demons succeed automatically)" names a creature tag, read for a Fiend (Demon) target.
func TestAutoSucceedTypesReadTags(t *testing.T) {
	var screech srdMonsterEntry
	for _, m := range srdMonsters2024() {
		for _, a := range m.Actions {
			if m.Name == "Vrock" && strings.HasPrefix(a.Name, "Stunning Screech") {
				screech = a
			}
		}
	}
	e, ok := parseSaveEffect(screech.Text)
	if !ok || len(e.AutoSucceedTypes) != 1 || e.AutoSucceedTypes[0] != "demon" {
		t.Fatalf("Stunning Screech: %+v", e.AutoSucceedTypes)
	}
	if got := creatureTypeWords("Fiend (Demon)"); len(got) != 2 || got[0] != "fiend" || got[1] != "demon" {
		t.Errorf("creature type words: %v", got)
	}
}

// An aura at the end of the monster's turn: a saving throw aura ("Constitution Saving Throw: DC N, each creature in a 5-foot Emanation
// originating from the X at the end of the X's turn", the Aboleth's Mucus Cloud pattern, with a success's immunity) and a damage aura
// "of its choice" (the Azer Sentinel's and the Salamander's Fire Aura: its enemies, never the monsters of its side).
func TestPostgresEndOfTurnAuras(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000 WHERE id=$1`, charID)
	pgExec(t, `UPDATE companions SET hp=1000,max_hp=1000 WHERE id=$1`, petID)
	vapor := inventedMonster("Fume Wretch", "Fiend", []srdMonsterEntry{{Name: "Choking Fumes", Text: "Constitution Saving Throw: DC 40, each creature in a 5-foot Emanation originating from the wretch at the end of the wretch's turn. Failure: The target has the Incapacitated condition until the end of its next turn. Success: The target is immune to this wretch's Choking Fumes for 24 hours."}},
		[]srdMonsterEntry{{Name: "Claw", Text: "Melee Attack Roll: +1, reach 5 ft. Hit: 1 (1d1) Slashing damage."}})
	coil := inventedMonster("Ember Coil", "Elemental", []srdMonsterEntry{{Name: "Heat Aura", Text: "At the end of each of the coil's turns, each creature of the coil's choice in a 5-foot Emanation originating from the coil takes 3 (1d6) Cold damage."}},
		[]srdMonsterEntry{{Name: "Bite", Text: "Melee Attack Roll: +1, reach 5 ft. Hit: 1 (1d1) Piercing damage."}})
	ally := inventedMonster("Coil Kin", "Beast", nil, []srdMonsterEntry{{Name: "Tusk", Text: "Melee Attack Roll: +1, reach 5 ft. Hit: 1 (1d1) Piercing damage."}})
	ids := importInventedMonsters(t, gm, camp, vapor, coil, ally)
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	place := func(source int) {
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"........", "........"}, Tokens: []mapToken{
			{ID: source, Name: "source", Position: mapSquare{1, 0}, Size: 1},
			{ID: charID, Name: "hero", Position: mapSquare{0, 0}, Size: 1},
			{ID: pet, Name: "React Pet", Position: mapSquare{2, 0}, Size: 1},
			{ID: ids["Coil Kin"], Name: "Coil Kin", Position: mapSquare{1, 1}, Size: 1},
			{ID: map[bool]int{true: ids["Ember Coil"], false: ids["Fume Wretch"]}[source == ids["Fume Wretch"]], Name: "other", Position: mapSquare{7, 1}, Size: 1}}}, "forced_movement": true}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}

	place(ids["Fume Wretch"])
	lines := monsterAuraTurnEnd(camp, ids["Fume Wretch"], "Fume Wretch")
	for _, tg := range []int{charID, pet, ids["Coil Kin"]} {
		if len(recordsOf(camp, tg, "incapacitated")) != 1 {
			t.Errorf("the saving throw aura missed %d: %v", tg, lines)
		}
	}
	// a success's immunity: an immune creature is left out of the next turn's aura
	endCondition(camp, charID, "incapacitated", "")
	addSpellEffect(camp, charID, ids["Fume Wretch"], "Fume Wretch (Choking Fumes)", "effect_immune", "Choking Fumes", 14400, "", 0) // as a success records it
	monsterAuraTurnEnd(camp, ids["Fume Wretch"], "Fume Wretch")
	if len(recordsOf(camp, charID, "incapacitated")) != 0 {
		t.Error("a creature immune to the aura rolled it again")
	}

	place(ids["Ember Coil"])
	hp := func(id int) int {
		if id == charID {
			var h int
			db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, id).Scan(&h)
			return h
		}
		if id == pet {
			var h int
			db.QueryRow(`SELECT hp FROM companions WHERE id=$1`, petID).Scan(&h)
			return h
		}
		return monsterHPNow(camp, id)
	}
	c0, p0, k0 := hp(charID), hp(pet), hp(ids["Coil Kin"])
	lines = monsterAuraTurnEnd(camp, ids["Ember Coil"], "Ember Coil")
	if hp(charID) >= c0 || hp(pet) >= p0 {
		t.Errorf("the aura of its choice spared its enemies: hero %d->%d, companion %d->%d (%v)", c0, hp(charID), p0, hp(pet), lines)
	}
	if hp(ids["Coil Kin"]) != k0 {
		t.Errorf("the aura of its choice hurt a monster of its side: %d->%d", k0, hp(ids["Coil Kin"]))
	}
	if strings.Contains(strings.Join(lines, " "), "resolve the targeting") {
		t.Errorf("the aura of its choice still went to the GM: %v", lines)
	}
}

// Blurred Form (the SRD Steam Mephit: attack rolls against it have Disadvantage unless it is Incapacitated) and Ignited Illumination
// (the SRD Magmin: a light it switches on and off, read by the battle map's lighting). Invented texts with the same wording.
func TestPostgresBlurredFormAndIgnitedLight(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000 WHERE id=$1`, charID)
	plain := []srdMonsterEntry{{Name: "Claw", Text: "Melee Attack Roll: +1, reach 5 ft. Hit: 1 (1d1) Slashing damage."}}
	blur := inventedMonster("Haze Imp", "Elemental", []srdMonsterEntry{{Name: "Shimmer Veil", Text: "Attack rolls against the imp are made with Disadvantage unless the imp has the Incapacitated condition."}}, plain)
	glow := inventedMonster("Cinder Gnome", "Elemental", nil, plain)
	glow.BonusActions = []srdMonsterEntry{{Name: "Kindle", Text: "The gnome sets itself ablaze or extinguishes its flames. While ablaze, the gnome sheds Bright Light in a 10-foot radius and Dim Light for an additional 10 feet."}}
	ids := importInventedMonsters(t, gm, camp, blur, glow)

	// Blurred Form: Disadvantage on a character's, a monster's and a companion's attack against it, none while it is Incapacitated
	for _, attacker := range []int{charID, ids["Cinder Gnome"], pet} {
		atk := attackCtx{lobby: camp, attacker: attacker, target: ids["Haze Imp"], name: "Claw", bonus: 1, melee: true, weapon: true}
		ar, _, _ := atk.roll()
		if !strings.Contains(strings.Join(ar.Disadvantage, ","), "Shimmer Veil") {
			t.Errorf("an attack by %d against the blurred imp: Disadvantage %v", attacker, ar.Disadvantage)
		}
	}
	addCondition(camp, ids["Haze Imp"], condAdd{Condition: "stunned", Source: "test"})
	atk := attackCtx{lobby: camp, attacker: charID, target: ids["Haze Imp"], name: "Claw", bonus: 1, melee: true, weapon: true}
	if ar, _, _ := atk.roll(); strings.Contains(strings.Join(ar.Disadvantage, ","), "Shimmer Veil") {
		t.Errorf("Blurred Form while Stunned (Incapacitated): %v", ar.Disadvantage)
	}

	// Ignited Illumination: dark scene; the character 10 feet away is in Bright Light only while the gnome is ablaze
	pgExec(t, `UPDATE combat_state SET lighting='darkness' WHERE lobby_id=$1`, camp)
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
	req := map[string]interface{}{"map": encounterMap{Grid: []string{"........", "........"}, Tokens: []mapToken{
		{ID: ids["Cinder Gnome"], Name: "gnome", Position: mapSquare{0, 0}, Size: 1},
		{ID: charID, Name: "hero", Position: mapSquare{2, 0}, Size: 1}}}, "forced_movement": true}
	if rev, ok := state["revision"].(string); ok {
		req["confirmation"] = rev
	}
	if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
		t.Fatalf("put map: %d %v", c, r)
	}
	before := monsterLightingAt(camp, charID)
	kindle := func(use string) string {
		newMonsterTurn(camp) // each call stands for a turn of its own: a monster takes one Bonus Action a turn
		_, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Cinder Gnome", "action": "Kindle", "use": use}})
		return fmt.Sprint(j["monster_action_result"])
	}
	if res := kindle("ignite"); !strings.Contains(res, "ablaze") || monsterLightingAt(camp, charID) != "bright" {
		t.Errorf("ablaze: lighting %q -> %q (%s)", before, monsterLightingAt(camp, charID), res)
	}
	expireCombatEffectsOf(camp, ids["Cinder Gnome"]) // its turn's start keeps it lit
	if monsterLightingAt(camp, charID) != "bright" {
		t.Error("the light went out at the gnome's turn start")
	}
	if res := kindle("extinguish"); monsterLightingAt(camp, charID) != before {
		t.Errorf("extinguished: lighting %q (%s)", monsterLightingAt(camp, charID), res)
	}
}

// Shape-Shift (the SRD lycanthropes' wording: "shape-shifts into a Large bear-humanoid hybrid form or a Large bear, or it returns to
// its true humanoid form"): the form named in use is recorded; an entry "(Bear or Hybrid Form Only)" is refused in another form, alone
// or in a Multiattack, before anything is rolled; a use the entry doesn't offer is refused.
func TestPostgresShapeShiftForms(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, _, _ := companionReactionFixture(t)
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000,ac=1 WHERE id=$1`, charID)
	were := inventedMonster("Den Mauler", "Monstrosity", nil, []srdMonsterEntry{
		{Name: "Multiattack", Text: "The mauler makes two Rend or Slam attacks."},
		{Name: "Rend (Bear or Hybrid Form Only)", Text: "Melee Attack Roll: +30, reach 5 ft. Hit: 5 (1d6 + 2) Slashing damage."},
		{Name: "Slam (Humanoid or Hybrid Form Only)", Text: "Melee Attack Roll: +30, reach 5 ft. Hit: 4 (1d4 + 2) Bludgeoning damage."}})
	were.BonusActions = []srdMonsterEntry{{Name: "Shape-Shift", Text: "The mauler shape-shifts into a Large bear-humanoid hybrid form or a Large bear, or it returns to its true humanoid form. Its game statistics, other than its size, are the same in each form. Any equipment it is wearing or carrying isn't transformed."}}
	ids := importInventedMonsters(t, gm, camp, were)
	tr := ids["Den Mauler"]
	narrate := func(action, use string, attacks []string) (int, map[string]interface{}) {
		newMonsterTurn(camp) // each call stands for a turn of its own: a monster takes one Bonus Action a turn
		t.Helper()
		ma := map[string]interface{}{"monster": "Den Mauler", "action": action, "target": fmt.Sprint(charID)}
		if use != "" {
			ma["use"] = use
		}
		if attacks != nil {
			ma["attacks"] = attacks
		}
		return pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": ma})
	}
	hp := func() (h int) { db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, charID).Scan(&h); return }
	form := func() string { return monsterForm(camp, tr, creatureMetaOf(camp, tr)) }

	// its true form is the humanoid: Rend is refused (nothing rolled), Slam works
	h0 := hp()
	if c, j := narrate("Rend", "", nil); c != 409 || !strings.Contains(fmt.Sprint(j["monster_action_refused"]), "only in its bear or hybrid form") || hp() != h0 {
		t.Fatalf("Rend in humanoid form: %d %v", c, j)
	}
	// a natural 1 misses even AC 1: retry until a hit lands
	hits := func(action string, attacks []string) (int, map[string]interface{}) {
		before := hp()
		var c int
		var j map[string]interface{}
		for n := 0; n < 20 && hp() >= before; n++ {
			c, j = narrate(action, "", attacks)
		}
		return c, j
	}
	if c, j := hits("Slam", nil); c != 200 || hp() >= h0 {
		t.Fatalf("Slam in humanoid form: %d %v (hp %d -> %d)", c, j, h0, hp())
	}
	// a form the entry doesn't offer is refused; then the bear form
	if c, j := narrate("Shape-Shift", "dragon", nil); !strings.Contains(fmt.Sprint(j["monster_action_result"]), "name the form in use") || form() != "humanoid" {
		t.Fatalf("a form it doesn't have: %d %v (form %q)", c, j, form())
	}
	if c, j := narrate("Shape-Shift", "bear", nil); c != 200 || !strings.Contains(fmt.Sprint(j["monster_action_result"]), "bear form now") {
		t.Fatalf("Shape-Shift to bear: %d %v", c, j)
	}
	h1 := hp()
	if c, j := narrate("Slam", "", nil); c != 409 || hp() != h1 {
		t.Fatalf("Slam in bear form: %d %v", c, j)
	}
	// a Multiattack that declares an attack of another form is refused whole; the hybrid form takes both
	if c, j := narrate("Multiattack", "", []string{"Rend", "Slam"}); hp() != h1 || !strings.Contains(fmt.Sprint(j), "only in its humanoid or hybrid form") {
		t.Fatalf("Multiattack with Slam in bear form: %d %v", c, j)
	}
	narrate("Shape-Shift", "hybrid", nil)
	if c, j := hits("Multiattack", []string{"Rend", "Slam"}); c != 200 || hp() >= h1 {
		t.Fatalf("Multiattack in hybrid form: %d %v", c, j)
	}
	// its turn's start keeps the form
	expireCombatEffectsOf(camp, tr)
	if f := form(); f != "hybrid" {
		t.Errorf("the form after its turn's start: %q", f)
	}
}

// The GM is shown what a save entry's rules don't read outside its sections (saveSetupText), the intro of a multi-effect entry
// (saveOptionsIntro) and a cast's riders (castRiders): the SRD 5.2.1 Djinni's Create Whirlwind, the Centaur Trooper's Trampling
// Charge (its move is the movement owner's, not repeated), the Sphinx of Valor's Roar and the Night Hag's Nightmare Haunting; then
// invented wording with other numbers.
func TestSaveSetupOptionsIntroAndCastRiders(t *testing.T) {
	entry := func(monster, name string) string {
		for _, m := range srdMonsters2024() {
			if m.Name != monster {
				continue
			}
			for _, g := range [][]srdMonsterEntry{m.Actions, m.BonusActions, m.Legendary} {
				for _, e := range g {
					if strings.HasPrefix(e.Name, name) {
						return e.Text
					}
				}
			}
		}
		t.Fatalf("%s|%s is not in the catalog", monster, name)
		return ""
	}
	whirl, _ := parseSaveEffect(entry("Djinni", "Create Whirlwind"))
	for _, want := range []string{"conjures a whirlwind at a point it can see within 120 feet", "20-foot-radius, 60-foot-high Cylinder", "Concentration on it ends", "enters a creature"} {
		if !strings.Contains(whirl.Setup, want) {
			t.Errorf("Create Whirlwind's setup lacks %q: %q", want, whirl.Setup)
		}
	}
	charge, _ := parseSaveEffect(entry("Centaur Trooper", "Trampling Charge"))
	if !strings.Contains(charge.Setup, "is targeted once by the following effect") || strings.Contains(charge.Setup, "moves up to its Speed") {
		t.Errorf("Trampling Charge's setup: %q", charge.Setup)
	}
	roar := entry("Sphinx of Valor", "Roar")
	if intro := saveOptionsIntro(roar, splitSaveOptions(roar)); !strings.Contains(intro, "emits a magical roar") || strings.Contains(intro, "Saving Throw") {
		t.Errorf("Roar's intro: %q", intro)
	}
	haunt := castRiders(entry("Night Hag", "Nightmare Haunting"))
	if !strings.Contains(haunt, "messenger") || !strings.Contains(haunt, "Protection from Evil and Good") || strings.Contains(haunt, "casts Dream") {
		t.Errorf("Nightmare Haunting's riders: %q", haunt)
	}
	// an entry with nothing outside its sections adds nothing; invented wording with other numbers
	if plain, _ := parseSaveEffect("Dexterity Saving Throw: DC 15, each creature in a 30-foot Cone. Failure: 21 (6d6) Fire damage. Success: Half damage."); plain.Setup != "" {
		t.Errorf("a plain breath has setup text: %q", plain.Setup)
	}
	if inv, _ := parseSaveEffect("The golem slams the ground at a point it can see within 40 feet. Strength Saving Throw: DC 12, each creature in a 10-foot-radius Sphere centered on that point. Failure: 9 (2d8) Bludgeoning damage."); inv.Setup != "The golem slams the ground at a point it can see within 40 feet" {
		t.Errorf("invented setup: %q", inv.Setup)
	}
	if r := castRiders("The shade casts Darkness, using Wisdom as the spellcasting ability (spell save DC 11). The darkness fades if the shade dies."); r != "The darkness fades if the shade dies" {
		t.Errorf("invented cast riders: %q", r)
	}
}

// What an owner applies is not repeated to the GM, and a hit's later-result sentence doesn't cost its other riders (SRD 5.2.1 texts):
// the Gibbering Mouther's Bite gives Prone though "The target dies if it is reduced to 0 Hit Points" stays the GM's; the Swarm of
// Crawling Claws' Bloodied alternative is the damage owner's; the Blink Dog's Teleport is the movement owner's whole; a Legendary
// Action's once-a-turn sentence is LAOnce's (Frightful Presence's cast, Disrupt Life's save).
func TestOwnersAppliedNotRepeatedToTheGM(t *testing.T) {
	text := func(monster, prefix string) string {
		for _, m := range srdMonsters2024() {
			if m.Name != monster {
				continue
			}
			for _, g := range [][]srdMonsterEntry{m.Actions, m.BonusActions, m.Legendary} {
				for _, e := range g {
					if strings.HasPrefix(e.Name, prefix) {
						return e.Text
					}
				}
			}
		}
		t.Fatalf("%s|%s is not in the catalog", monster, prefix)
		return ""
	}
	mouther := text("Gibbering Mouther", "Bite")
	// its death on a drop is applied by hitAfterDamage (dieOnDropRe); only what becomes of the body is the GM's
	if e, ok := parseHitRider(mouther); !ok || strings.Join(e.Conditions, ",") != "prone" || strings.Contains(e.Extra, "dies if") || !strings.Contains(e.Extra, "absorbed") || strings.Contains(e.Extra, "Prone") {
		t.Errorf("Gibbering Mouther Bite: ok %v %+v", ok, e)
	}
	if r := unappliedHitRider(text("Swarm of Crawling Claws", "Swarm of Grasping Hands")); r != "" {
		t.Errorf("the swarm's Bloodied alternative reached the GM: %q", r)
	}
	if !onlyOwnMove(text("Blink Dog", "Teleport")) {
		t.Error("the Blink Dog's Teleport is not read whole by the movement owner")
	}
	for _, m := range srdMonsters2024() {
		for _, e := range m.Legendary {
			sv, _ := parseSaveEffect(e.Text)
			for what, s := range map[string]string{"cast riders": castRiders(e.Text), "save residual": sv.Extra, "save setup": sv.Setup} {
				if legendaryOnceRe.MatchString(s) {
					t.Errorf("%s / %s: the once-a-turn sentence is in the %s: %q", m.Name, e.Name, what, s)
				}
			}
		}
	}
}

// A failed save that pushes its target ("the target is pushed up to 30 feet straight away from the dragon": the SRD bronze dragons'
// Repulsion Breath, the silver dragons' Cold Gale, the Air Elemental's Whirlwind): the save owner moves a character, a monster and a
// companion on the battle map through forceToken, gives the rest of the sentence (Prone, the damage), and leaves the GM nothing; a
// success doesn't move it. Invented wording with other numbers.
func TestPostgresSavePushOnEveryTarget(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000 WHERE id=$1`, charID)
	pgExec(t, `UPDATE companions SET hp=1000,max_hp=1000 WHERE id=$1`, petID)
	gust := inventedMonster("Gust Drake", "Dragon", nil, []srdMonsterEntry{
		{Name: "Shove Breath", Text: "Strength Saving Throw: DC 40, each creature in a 30-foot Cone. Failure: The target is pushed up to 15 feet straight away from the drake and has the Prone condition."},
		{Name: "Weak Gust", Text: "Strength Saving Throw: DC 1, one creature within 30 feet. Failure: 3 (1d6) Cold damage, and the target is pushed up to 15 feet straight away from the drake."}})
	pushed := inventedMonster("Pushed Boar", "Beast", nil, []srdMonsterEntry{{Name: "Tusk", Text: "Melee Attack Roll: +1, reach 5 ft. Hit: 1 (1d1) Piercing damage."}})
	ids := importInventedMonsters(t, gm, camp, gust, pushed)
	if e, _ := parseSaveEffect(gust.Actions[0].Text); e.Push == nil || e.Push.Feet != 15 || e.Extra != "" || len(e.Conditions) != 1 {
		t.Fatalf("Shove Breath read: %+v", e)
	}
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	place := func() { // each creature in line with the drake, one row each: a push is straight away from it along its row
		t.Helper()
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"............", "............", "............"}, Tokens: []mapToken{
			{ID: ids["Gust Drake"], Name: "drake", Position: mapSquare{0, 1}, Size: 1},
			{ID: charID, Name: "hero", Position: mapSquare{1, 1}, Size: 1}}}, "forced_movement": true}
		if rev, ok := state["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	xOf := func(id int) int {
		m, _ := activeEncounterMap(camp)
		tk, _ := m.token(id)
		return tk.Position.X
	}
	names := map[int]string{charID: fmt.Sprint(charID), ids["Pushed Boar"]: "Pushed Boar", pet: "React Pet"}
	for _, id := range []int{charID, ids["Pushed Boar"], pet} {
		place()
		m, _ := activeEncounterMap(camp)
		m.Tokens = []mapToken{{ID: ids["Gust Drake"], Name: "drake", Position: mapSquare{0, 1}, Size: 1}, {ID: id, Name: names[id], Position: mapSquare{1, 1}, Size: 1}}
		_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, map[string]interface{}{"map": m, "forced_movement": true, "confirmation": state["revision"]}); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
		_, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Gust Drake", "action": "Shove Breath", "target": names[id], "confirm_targeting": true}})
		res := fmt.Sprint(j["monster_action_result"])
		if xOf(id) != 4 || len(recordsOf(camp, id, "prone")) != 1 || strings.Contains(res, "pushed up to") || strings.Contains(res, "the GM handles") {
			t.Errorf("Shove Breath at %s: x %d, prone %v: %s", names[id], xOf(id), recordsOf(camp, id, "prone"), res)
		}
		removeConditionsWhere(camp, id, func(conditionRecord) bool { return true })
	}
	// a success (DC 1) doesn't move it
	m, _ := activeEncounterMap(camp)
	m.Tokens = []mapToken{{ID: ids["Gust Drake"], Name: "drake", Position: mapSquare{0, 1}, Size: 1}, {ID: ids["Pushed Boar"], Name: "boar", Position: mapSquare{1, 1}, Size: 1}}
	_, state := pgCall(t, "GET", cp+"/battle-map", gm, nil)
	pgCall(t, "PUT", cp+"/battle-map", gm, map[string]interface{}{"map": m, "forced_movement": true, "confirmation": state["revision"]})
	pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
		"monster_action": map[string]interface{}{"monster": "Gust Drake", "action": "Weak Gust", "target": "Pushed Boar", "confirm_targeting": true}})
	if xOf(ids["Pushed Boar"]) != 1 {
		t.Errorf("a successful save moved the boar to x %d", xOf(ids["Pushed Boar"]))
	}
}
