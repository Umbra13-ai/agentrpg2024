package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Per-day uses (monster_day_uses.go): "(1/Day)" actions and the "N/Day Each" and shared "N/Day" lists of a Spellcasting entry are counted in
// the monster's persisted state; a use past the limit is refused with nothing rolled, "At Will" never is, and a refused try spends nothing.
func TestPostgresMonsterDayUses(t *testing.T) {
	testPostgres(t)
	// the spells the test casts are its own (other tests replace the shared spell cache): two roll a save, the rest the server hands to the GM
	saved := map[string]SRDSpell{}
	for k, v := range srdSpellsMemory {
		saved[k] = v
	}
	t.Cleanup(func() { srdSpellsMemory = saved })
	srdSpellsMemory = map[string]SRDSpell{}
	for k, v := range saved {
		srdSpellsMemory[k] = v
	}
	srdSpellsMemory["fireball"] = SRDSpell{Name: "Fireball", Level: 3, SavingThrow: "Dexterity", DamageDice: "8d6", DamageType: "Fire"}
	srdSpellsMemory["scrying"] = SRDSpell{Name: "Scrying", Level: 5, SavingThrow: "Wisdom"}
	srdSpellsMemory["fly"] = SRDSpell{Name: "Fly", Level: 3}
	srdSpellsMemory["teleport"] = SRDSpell{Name: "Teleport", Level: 7}
	srdSpellsMemory["light"] = SRDSpell{Name: "Light", Level: 0}
	srdSpellsMemory["mage-hand"] = SRDSpell{Name: "Mage Hand", Level: 0}
	_, gm := pgAgent(t, "day use gm")
	_, player := pgAgent(t, "day use player")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Day uses", "max_players": 2})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Day Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var goblin srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Goblin Warrior" {
			goblin = m
		}
	}
	caster := goblin
	caster.Name = "Test Caster"
	caster.Actions = []srdMonsterEntry{
		{Name: "Smoke Bomb (1/Day)", Text: "Dexterity Saving Throw: DC 30, each creature in a 10-foot Emanation. Failure: 5 (1d6 + 2) Fire damage. Success: Half damage."},
		{Name: "Spit (1/Day)", Text: "Ranged Attack Roll: +0, range 30 feet. Hit: 5 (1d6 + 2) Poison damage."},
		{Name: "Chill Mist (1/Day)", Text: "Dexterity Saving Throw: DC 1, each creature in a 10-foot Emanation. Failure: 3 (1d6) Cold damage. Success: No damage."},
		{Name: "Dread Gaze (1/Day)", Text: "Wisdom Saving Throw: DC 30, one creature. Failure: The target has the Frightened condition until the end of its next turn."},
		{Name: "Spellcasting", Text: "The caster casts one of the following spells, using Intelligence as the spellcasting ability (spell save DC 17): At Will: Light, Mage Hand 2/Day Each: Fireball, Fly 1/Day: Scrying, Teleport"},
	}
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variant for per-day use tests", "monsters": []srdMonster2024{caster}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	_, added := pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Test Caster", "monster_key": key, "initiative": 20}}})
	creature := fmt.Sprint(added["combatants_added"].([]interface{})[0].(map[string]interface{})["creature_id"])
	if !strings.HasPrefix(creature, "cr-") {
		t.Fatalf("a monster added to a fight has no creature_id: %v", added)
	}
	pgExec(t, `UPDATE characters SET hp=1000,max_hp=1000 WHERE id=$1`, char)
	who := "Test Caster"
	var preview bool
	use := func(action, spell, target string) string {
		t.Helper()
		ma := map[string]interface{}{"monster": who, "action": action}
		if preview {
			ma["apply_damage"] = false
		}
		if spell != "" {
			ma["use"] = spell
		}
		if target != "" {
			ma["target"] = target
		}
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": ma})
		return fmt.Sprint(r["monster_action_result"])
	}
	refused := func(res string) bool {
		return strings.Contains(res, "allows") && strings.Contains(res, "Nothing was rolled")
	}
	hero := fmt.Sprint(char)
	// a (1/Day) action: a try with no target is refused for that reason and spends nothing; the first real use is allowed, the second is not
	if res := use("Smoke Bomb (1/Day)", "", ""); refused(res) {
		t.Fatalf("a try with no target spent the use: %s", res)
	}
	if res := use("Smoke Bomb (1/Day)", "", hero); refused(res) || !strings.Contains(res, "saving throw") {
		t.Fatalf("first Smoke Bomb: %s", res)
	}
	if res := use("Smoke Bomb (1/Day)", "", hero); !refused(res) {
		t.Fatalf("second Smoke Bomb was not refused: %s", res)
	}
	// an executed use counts whatever came of it: an attack that misses, a save that succeeds for no damage, a condition-only effect
	pgExec(t, `UPDATE characters SET ac=1000 WHERE id=$1`, char)
	for _, a := range []string{"Spit (1/Day)", "Chill Mist (1/Day)", "Dread Gaze (1/Day)"} {
		var before int
		db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, char).Scan(&before)
		res := use(a, "", hero)
		if refused(res) || strings.Contains(res, "Not counted") {
			t.Fatalf("%s was executed but not counted: %s", a, res)
		}
		if again := use(a, "", hero); !refused(again) {
			t.Fatalf("%s: the use of an executed action (hp before %d) was not spent: %s / %s", a, before, res, again)
		}
	}
	// "2/Day Each": two Fireballs, the third refused; Fly has its own count
	for i := 1; i <= 2; i++ {
		if res := use("Spellcasting", "Fireball", hero); refused(res) {
			t.Fatalf("Fireball %d refused: %s", i, res)
		}
	}
	if res := use("Spellcasting", "Fireball", hero); !refused(res) {
		t.Fatalf("third Fireball was not refused: %s", res)
	}
	if res := use("Spellcasting", "Fly", hero); refused(res) {
		t.Fatalf("Fly shares Fireball's count: %s", res)
	}
	// "At Will" is never refused
	for i := 0; i < 5; i++ {
		if res := use("Spellcasting", "Light", hero); refused(res) {
			t.Fatalf("At Will Light refused on use %d: %s", i+1, res)
		}
	}
	// "1/Day:" (not Each) is one use for the whole list: Scrying (the server rolls its save) spends it for Teleport too
	if res := use("Spellcasting", "Scrying", hero); refused(res) || !strings.Contains(res, "Day use counted for Test Caster: 1 of 1/Day") {
		t.Fatalf("first 1/Day spell: %s", res)
	}
	if res := use("Spellcasting", "Teleport", hero); !refused(res) {
		t.Fatalf("the shared 1/Day use was spent by Scrying, but Teleport was not refused: %s", res)
	}
	// persisted: the counts are in the stored state, not in memory
	f, _ := readFight(db, camp)
	id := 0
	for _, e := range f.Order {
		if sprintAny(e["name"]) == "Test Caster" {
			id = asInt(e["id"])
		}
	}
	if d := loadMonsterState(camp, id).DayUses; d["action:smoke bomb"] != 1 || d["spell:fireball"] != 2 || d["tier:1/day"] != 1 || d["spell:light"] != 0 || d["spell:fly"] != 1 { // Fly is the GM's to resolve: its use counted all the same (I-WL-2)
		t.Fatalf("stored day uses: %v", d)
	}
	// the counts last from one fight to the next: the same creature, linked on purpose (also under another name), keeps them
	pgCall(t, "POST", cp+"/combat/end", gm, nil)
	var stored []byte
	db.QueryRow(`SELECT uses FROM monster_day_uses WHERE campaign_id=$1 AND creature_id=$2`, camp, creature).Scan(&stored)
	if !strings.Contains(string(stored), "action:smoke bomb") {
		t.Fatalf("the counts did not outlast the fight: %s", stored)
	}
	startFight := func(entries ...map[string]interface{}) map[string]interface{} {
		t.Helper()
		pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
		c, out := pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": entries})
		if c != 200 {
			t.Fatalf("combat/add %v: %d %v", entries, c, out)
		}
		return out
	}
	// a creature with the same name that is not linked is a NEW creature: it does not share the count
	startFight(map[string]interface{}{"name": "Test Caster", "monster_key": key, "initiative": 20})
	if res := use("Smoke Bomb (1/Day)", "", hero); refused(res) {
		t.Fatalf("a new creature with the same name shared the old one's count: %s", res)
	}
	pgCall(t, "POST", cp+"/combat/end", gm, nil)
	// a bad link is refused: unknown id, other stat block
	for _, bad := range []map[string]interface{}{
		{"name": "Test Caster", "monster_key": key, "initiative": 20, "creature_id": "cr-nope"},
		{"name": "Test Caster", "monster_key": "goblin-warrior", "initiative": 20, "creature_id": creature},
	} {
		pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
		if c, out := pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{bad}}); c != 400 {
			t.Fatalf("a bad creature_id was accepted: %d %v", c, out)
		}
		pgCall(t, "POST", cp+"/combat/end", gm, nil)
	}
	// linked under another name: the returning creature has its uses (a rename restores nothing by itself, a link does)
	startFight(map[string]interface{}{"name": "Renamed Caster", "monster_key": key, "initiative": 20, "creature_id": creature})
	who = "Renamed Caster"
	if res := use("Smoke Bomb (1/Day)", "", hero); !refused(res) {
		t.Fatalf("the linked creature got its use back: %s", res)
	}
	if res := use("Spellcasting", "Fireball", hero); !refused(res) {
		t.Fatalf("the linked creature got Fireball back: %s", res)
	}
	// the same creature cannot be added twice to one fight
	if c, out := pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Twin", "monster_key": key, "initiative": 5, "creature_id": creature}}}); c != 400 {
		t.Fatalf("one creature twice in a fight: %d %v", c, out)
	}
	// two creatures with one name in one fight do not share uses
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Renamed Caster", "monster_key": key, "initiative": 5}}})
	f, _ = readFight(db, camp)
	var ids []int
	for _, e := range f.Order {
		if sprintAny(e["name"]) == "Renamed Caster" {
			ids = append(ids, asInt(e["id"]))
		}
	}
	if len(ids) != 2 || dayCreatureID(camp, ids[0]) == dayCreatureID(camp, ids[1]) {
		t.Fatalf("two creatures with one name share an identity: %v", ids)
	}
	// a preview (apply_damage false) counts nothing; a hand-off to the GM is the GM using it: counted, the second refused (I-WL-2)
	pgCall(t, "POST", cp+"/combat/end", gm, nil)
	startFight(map[string]interface{}{"name": "Fresh Caster", "monster_key": key, "initiative": 20})
	who = "Fresh Caster"
	preview = true
	for i := 0; i < 2; i++ {
		if res := use("Smoke Bomb (1/Day)", "", hero); refused(res) || !strings.Contains(res, "Not counted as a day use: apply_damage is false") {
			t.Fatalf("a preview should not be counted: %s", res)
		}
	}
	preview = false
	if res := use("Spellcasting", "Teleport", hero); refused(res) || !strings.Contains(res, "Day use counted for Fresh Caster: 1 of 1/Day") {
		t.Fatalf("a hand-off to the GM should be counted: %s", res)
	}
	if res := use("Spellcasting", "Teleport", hero); !refused(res) {
		t.Fatalf("a second 1/Day hand-off was not refused: %s", res)
	}
	f, _ = readFight(db, camp)
	for _, e := range f.Order {
		if sprintAny(e["name"]) == "Fresh Caster" {
			id = asInt(e["id"])
		}
	}
	// the GM gives a use back, then it is allowed again
	pgCall(t, "POST", "/api/gm/monster-features", gm, map[string]interface{}{"campaign_id": camp, "combatant_id": id, "day_uses": map[string]int{"tier:1/day": 0}})
	if res := use("Spellcasting", "Teleport", hero); refused(res) {
		t.Fatalf("the use the GM gave back was refused: %s", res)
	}
	// a confirmed Smoke Bomb says it was counted; new_day gives every count back
	if res := use("Smoke Bomb (1/Day)", "", hero); !strings.Contains(res, "Day use counted for Fresh Caster: 1 of 1/Day") {
		t.Fatalf("a confirmed use should say it was counted: %s", res)
	}
	if res := use("Smoke Bomb (1/Day)", "", hero); !refused(res) {
		t.Fatalf("second Smoke Bomb was not refused: %s", res)
	}
	if c, out := pgCall(t, "POST", "/api/gm/monster-features", gm, map[string]interface{}{"campaign_id": camp, "new_day": true}); c != 200 {
		t.Fatalf("new_day: %d %v", c, out)
	}
	if res := use("Smoke Bomb (1/Day)", "", hero); refused(res) {
		t.Fatalf("after new_day Smoke Bomb was still refused: %s", res)
	}
	// a rejected target is not a use: the attack and the save effect name no one the fight knows
	pgCall(t, "POST", "/api/gm/monster-features", gm, map[string]interface{}{"campaign_id": camp, "new_day": true})
	for _, a := range []string{"Spit (1/Day)", "Dread Gaze (1/Day)"} {
		use(a, "", "Nobody Here")
		if u := loadDayUses(camp, id); len(u) != 0 {
			t.Fatalf("%s at a target that is not there was counted: %v", a, u)
		}
	}

	// A route refusal is structured: the command lease may refuse an overlapping
	// command before the day owner is reached. Never count a missing result as an
	// executed action. Keep a separate owner concurrency control below so this
	// request lease cannot hide a broken resource claim.
	for _, action := range []string{"Chill Mist (1/Day)", "Spit (1/Day)"} {
		type response struct {
			status int
			body   map[string]interface{}
		}
		results := make(chan response, 8)
		start := make(chan struct{})
		for i := 0; i < 8; i++ {
			go func() {
				<-start
				code, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x", "monster_action": map[string]interface{}{"monster": who, "action": action, "target": hero}})
				results <- response{code, r}
			}()
		}
		close(start)
		ran := 0
		for i := 0; i < 8; i++ {
			res := <-results
			switch {
			case res.status == 409 && res.body["error"] == "combat_operation_in_progress":
			case narrateAnswered(res.status, res.body) && refused(fmt.Sprint(res.body["monster_action_result"])): // a refusal is 409 monster_action_refused (narrate runs the action first)
			case res.status == 200 && strings.Contains(fmt.Sprint(res.body["monster_action_result"]), "Day use counted"):
				ran++
			case res.status == 200 && action == "Spit (1/Day)":
				attack, ok := res.body["monster_attack"].(map[string]interface{})
				if !ok || asInt(attack["attacks"]) != 1 || attack["applied"] != true {
					t.Errorf("missing executed attack receipt: %v", res.body)
				} else {
					ran++
				}
			default:
				t.Errorf("unexpected concurrent %s response: %d %v", action, res.status, res.body)
			}
		}
		key := map[string]string{"Chill Mist (1/Day)": "action:chill mist", "Spit (1/Day)": "action:spit"}[action]
		if ran != 1 || loadDayUses(camp, id)[key] != 1 {
			t.Fatalf("eight concurrent %s uses: %d ran, stored %v", action, ran, loadDayUses(camp, id))
		}
	}
	claims := make(chan bool, 8)
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() { <-start; claims <- reserveDayUse(camp, id, "test:independent-owner", 1) }()
	}
	close(start)
	taken := 0
	for i := 0; i < 8; i++ {
		if <-claims {
			taken++
		}
	}
	if taken != 1 || loadDayUses(camp, id)["test:independent-owner"] != 1 {
		t.Fatalf("owner concurrency without the request lease: %d claims, stored %v", taken, loadDayUses(camp, id))
	}

}

func firstMonster(t *testing.T, camp int, name string) int {
	t.Helper()
	f, _ := readFight(db, camp)
	for _, e := range f.Order {
		if sprintAny(e["name"]) == name {
			return asInt(e["id"])
		}
	}
	t.Fatalf("no %s in the fight", name)
	return 0
}

// A "(N/Day)" Reaction counts in the same ledger, and only when it took effect: a Split refused for the creature's size is not a use.
func TestPostgresMonsterReactionDayUses(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "react day gm")
	_, player := pgAgent(t, "react day player")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Reaction day uses", "max_players": 2})
	if c != 200 {
		t.Fatalf("campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("React Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var goblin srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Goblin Warrior" {
			goblin = m
		}
	}
	mk := func(name, size string) string {
		m := goblin
		m.Name, m.Size, m.HP = name, size, 40
		m.Reactions = []srdMonsterEntry{{Name: "Warding (1/Day)", Text: "Trigger: The mage is hit by an attack roll. Response: The mage casts Counterspell or Shield, using the same slot."}, {Name: "Split (1/Day)", Text: "Trigger: The ooze takes damage. Response: The ooze splits into two new creatures one size smaller, each with half its Hit Points."}}
		c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variant for reaction day-use tests", "monsters": []srdMonster2024{m}})
		if c != 201 {
			t.Fatalf("import: %d %v", c, j)
		}
		return fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	}
	small, medium := mk("Small Splitter", "Small"), mk("Medium Splitter", "Medium")
	reactWith := func(who, action string) string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": who, "action": action}})
		return fmt.Sprint(r["monster_action_result"])
	}
	react := func(who string) string { return reactWith(who, "Split (1/Day)") }
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	_, added := pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Small Splitter", "monster_key": small, "initiative": 20}, {"name": "Medium Splitter", "monster_key": medium, "initiative": 10}}})
	creatures := map[string]string{}
	for _, a := range added["combatants_added"].([]interface{}) {
		m := a.(map[string]interface{})
		creatures[fmt.Sprint(m["name"])] = fmt.Sprint(m["creature_id"])
	}
	usesOf := func(creature string) map[string]int {
		var raw []byte
		db.QueryRow(`SELECT uses FROM monster_day_uses WHERE campaign_id=$1 AND creature_id=$2`, camp, creature).Scan(&raw)
		out := map[string]int{}
		json.Unmarshal(raw, &out)
		return out
	}
	// a reaction with no trigger (nothing hit it) is answered but is not used up
	if res := reactWith("Medium Splitter", "Warding (1/Day)"); !strings.Contains(res, "Shield has no trigger") {
		t.Fatalf("Warding with no hit: %s", res)
	}
	if u := usesOf(creatures["Medium Splitter"]); len(u) != 0 {
		t.Fatalf("a reaction with no trigger was counted: %v", u)
	}
	if res := react("Small Splitter"); !strings.Contains(res, "can split only while Large or Medium") {
		t.Fatalf("a Small creature split: %s", res)
	}
	if u := usesOf(creatures["Small Splitter"]); len(u) != 0 {
		t.Fatalf("a refused reaction was counted: %v", u)
	}
	if res := react("Medium Splitter"); strings.Contains(res, "can split only") || strings.Contains(res, "Not counted") {
		t.Fatalf("the Medium creature did not split: %s", res)
	}
	if u := usesOf(creatures["Medium Splitter"]); u["action:split"] != 1 {
		t.Fatalf("the executed reaction was not counted in the ledger: %v", u)
	}
	pgCall(t, "POST", cp+"/combat/end", gm, nil)
	if u := usesOf(creatures["Medium Splitter"]); u["action:split"] != 1 {
		t.Fatalf("the reaction's count did not outlast the fight: %v", u)
	}
}

// A Legendary Action's own "(N/Day)" limit and the limited attack it calls are enforced through the shared entries.
func TestPostgresLegendaryDayUses(t *testing.T) {
	testPostgres(t)
	savedSpells := srdSpellsMemory
	srdSpellsMemory = map[string]SRDSpell{}
	for k, v := range savedSpells {
		srdSpellsMemory[k] = v
	}
	t.Cleanup(func() { srdSpellsMemory = savedSpells })
	_, gm := pgAgent(t, "legend day gm")
	_, player := pgAgent(t, "legend day player")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Legend day uses", "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	m := compendiumFixture()
	m.Name = "Day Wyrm"
	m.Actions = []srdMonsterEntry{{Name: "Gnaw (1/Day)", Text: "Melee Attack Roll: +9, reach 5 ft. Hit: 7 (2d6) Slashing damage."}, {Name: "Bite", Text: "Melee Attack Roll: +9, reach 5 ft. Hit: 7 (2d6) Piercing damage."},
		{Name: "Howl (1/Day)", Text: "Dexterity Saving Throw: DC 1, one creature. Failure: 3 (1d6) Thunder damage. Success: No damage."}}
	m.Legendary = []srdMonsterEntry{
		{Name: "Lunge", Text: "The wyrm makes one Gnaw attack."},
		{Name: "Dive (1/Day)", Text: "The wyrm makes one Bite attack."},
		{Name: "Cry", Text: "The wyrm uses Howl."},
		{Name: "Conjure", Text: "The wyrm casts one of the following spells, using Intelligence as the spellcasting ability (spell save DC 17): 1/Day Each: Fireball"},
	}
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "original test", "monsters": []srdMonster2024{m}})
	if c != 201 {
		t.Fatalf("import %d %v", c, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	pgExec(t, `UPDATE lobbies SET status='active',schedule_state='off' WHERE id=$1`, camp)
	pgFight(t, camp)
	status, j := pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Legend target %d", camp)))
	if status != 200 {
		t.Fatalf("legendary-use fixture character: %d %v", status, j)
	}
	char := int(j["character_id"].(float64))
	pgExec(t, `UPDATE characters SET lobby_id=$1,hp=1000,max_hp=1000,ac=1 WHERE id=$2`, camp, char)
	c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Day Wyrm", "monster_key": key, "initiative": 1}}})
	if c != 200 {
		t.Fatalf("add %d %v", c, j)
	}
	id := firstMonster(t, camp, "Day Wyrm")
	request := httptest.NewRequest("POST", "/api/gm/legendary-action", nil)
	take := func(action, window string) (string, string) {
		pgExec(t, `UPDATE combat_monster_state SET la_used=0, la_window='' WHERE lobby_id=$1`, camp)
		res, code, msg := takeLegendaryAction(request, camp, legendaryRequest{CombatantID: id, Action: action, Target: fmt.Sprint(char)}, window, char)
		return code + msg, fmt.Sprint(res["result"])
	}
	// the called attack (Gnaw, 1/Day): the first Lunge makes it, the second is refused by the shared attack entry
	if code, res := take("Lunge", "1:1"); code != "" || !strings.Contains(res, "MISS") && !strings.Contains(res, "HIT") {
		t.Fatalf("first Lunge: %q %s", code, res)
	}
	if loadDayUses(camp, id)["action:gnaw"] != 1 {
		t.Fatalf("the called limited attack was not counted: %v", loadDayUses(camp, id))
	}
	if _, res := take("Lunge", "1:2"); !strings.Contains(res, "Nothing was rolled") {
		t.Fatalf("the second Lunge made the 1/Day attack again: %s", res)
	}
	// a limited action a Legendary Action calls ("uses Howl"): the first Cry resolves it, the second is refused by the shared effect entry
	if code, res := take("Cry", "1:5"); code != "" || !strings.Contains(res, "saving throw") {
		t.Fatalf("first Cry: %q %s", code, res)
	}
	if loadDayUses(camp, id)["action:howl"] != 1 {
		t.Fatalf("the called limited effect was not counted: %v", loadDayUses(camp, id))
	}
	if _, res := take("Cry", "1:6"); !strings.Contains(res, "Nothing was rolled") {
		t.Fatalf("the second Cry used the 1/Day effect again: %s", res)
	}
	// a limited spell a Legendary Action casts (1/Day Each): the first Conjure casts it, the second is refused
	srdSpellsMemory["fireball"] = SRDSpell{Name: "Fireball", Level: 3, SavingThrow: "Dexterity", DamageDice: "8d6", DamageType: "Fire"}
	castSpell := func(window string) (string, string) {
		pgExec(t, `UPDATE combat_monster_state SET la_used=0, la_window='' WHERE lobby_id=$1`, camp)
		res, code, msg := takeLegendaryAction(request, camp, legendaryRequest{CombatantID: id, Action: "Conjure", Target: fmt.Sprint(char), Use: "Fireball"}, window, char)
		return code + msg, fmt.Sprint(res["result"])
	}
	if code, res := castSpell("1:7"); code != "" || !strings.Contains(res, "saving throw") {
		t.Fatalf("first Conjure: %q %s", code, res)
	}
	if loadDayUses(camp, id)["spell:fireball"] != 1 {
		t.Fatalf("the called limited spell was not counted: %v", loadDayUses(camp, id))
	}
	if _, res := castSpell("1:8"); !strings.Contains(res, "Nothing was rolled") {
		t.Fatalf("the second Conjure cast the 1/Day spell again: %s", res)
	}
	// the Legendary Action's own limit: the first Dive is taken, the second is refused with a reason and spends no Legendary Action use
	if code, res := take("Dive (1/Day)", "1:3"); code != "" || !strings.Contains(res, "Bite") {
		t.Fatalf("first Dive: %q %s", code, res)
	}
	if code, _ := take("Dive (1/Day)", "1:4"); !strings.HasPrefix(code, "day_limit") {
		t.Fatalf("the second Dive was not refused: %q", code)
	}
	// an Opportunity Attack (the map route) uses the same attack entry: the spent 1/Day Gnaw is refused and the Reaction is not used up
	monsterReactionRound.Delete(fmt.Sprintf("%d:%d", camp, id))
	if lines := resolveOpportunityAttacks(request, camp, char, []int{id}); len(lines) != 1 || !strings.Contains(lines[0], "Nothing was rolled") {
		t.Fatalf("an Opportunity Attack made the spent 1/Day attack: %v", lines)
	}
	if _, used := monsterReactionRound.Load(fmt.Sprintf("%d:%d", camp, id)); used {
		t.Fatalf("a refused Opportunity Attack used up the Reaction")
	}
	saveDayUses(camp, id, nil)
	if lines := resolveOpportunityAttacks(request, camp, char, []int{id}); len(lines) != 1 || strings.Contains(lines[0], "Nothing was rolled") || loadDayUses(camp, id)["action:gnaw"] != 1 {
		t.Fatalf("an Opportunity Attack with a fresh 1/Day attack: %v %v", lines, loadDayUses(camp, id))
	}
	if loadMonsterState(camp, id).LAUsed != 0 {
		t.Fatalf("a refused Legendary Action used up a Legendary Action use")
	}
}

// A rest-based recharge ("Recharges after a Short or Long Rest", "Recharges after a Long Rest", "Recharges at the Next Dawn", and the SRD's
// "(Recharge after a Short or Long Rest)"): one use until the GM says the creatures rested (monster-features rest short or long, or new_day), on
// every route that runs an entry, with the retry refused before anything is rolled.
func TestPostgresRestRechargeActions(t *testing.T) {
	testPostgres(t)
	for name, want := range map[string]string{
		"Second Wind (Recharges after a Short or Long Rest)": "short", "Phantasms (Recharge after a Short or Long Rest)": "short",
		"Magic Boon (Recharges after a Long Rest)": "long", "Revivify (Recharges at the Next Dawn)": "long", "Croak (Recharge 5-6)": "", "Roar (1/Day)": "",
	} {
		if got := restRechargePeriod(name); got != want {
			t.Errorf("restRechargePeriod(%q) = %q, want %q", name, got, want)
		}
	}
	gm, _, camp, hero, id := earlyRulesFixture(t, "Test Rest Knight", []srdMonsterEntry{
		{Name: "Second Wind (Recharges after a Short or Long Rest)", Text: "The knight regains 10 (3d6) Hit Points."},
		{Name: "Phantasms (Recharge after a Short or Long Rest)", Text: "The knight casts the Mirror Image spell, requiring no spell components and using Wisdom as the spellcasting ability."},
		{Name: "Magic Boon (Recharges after a Long Rest)", Text: "The knight gives one ally a boon."},
		{Name: "Revivify (Recharges at the Next Dawn)", Text: "The knight casts Revivify."},
	})
	use := func(action string) string {
		t.Helper()
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", id), "action": action, "target": fmt.Sprint(hero)}})
		return fmt.Sprint(r["monster_action_result"], r["message"], r["error"])
	}
	spent := func(res string) bool { return strings.Contains(res, "already used") }
	actions := []string{"Second Wind", "Phantasms", "Magic Boon", "Revivify"}
	for _, a := range actions {
		if res := use(a); spent(res) {
			t.Fatalf("the first use of %s was refused: %s", a, res)
		}
		if res := use(a); !spent(res) || !strings.Contains(res, "Nothing was rolled") {
			t.Errorf("the second use of %s: %s", a, res)
		}
	}
	rest := func(kind string) {
		t.Helper()
		if c, j := pgCall(t, "POST", "/api/gm/monster-features", gm, map[string]interface{}{"campaign_id": camp, "rest": kind}); c != 200 {
			t.Fatalf("rest %s: %d %v", kind, c, j)
		}
	}
	rest("short") // the short-rest entries only
	for a, back := range map[string]bool{"Second Wind": true, "Phantasms": true, "Magic Boon": false, "Revivify": false} {
		if res := use(a); spent(res) == back {
			t.Errorf("after a Short Rest %s (available %v): %s", a, back, res)
		}
	}
	rest("long")
	for _, a := range []string{"Magic Boon", "Revivify"} {
		if res := use(a); spent(res) {
			t.Errorf("after a Long Rest %s was refused: %s", a, res)
		}
	}
	for _, a := range actions { // all spent again: a new day gives them back too
		use(a)
	}
	pgCall(t, "POST", "/api/gm/monster-features", gm, map[string]interface{}{"campaign_id": camp, "new_day": true})
	for _, a := range actions {
		if res := use(a); spent(res) {
			t.Errorf("after a new day %s was refused: %s", a, res)
		}
	}
	if c, _ := pgCall(t, "POST", "/api/gm/monster-features", gm, map[string]interface{}{"campaign_id": camp, "rest": "nap"}); c != 400 {
		t.Errorf("an unknown rest: %d", c)
	}
}
