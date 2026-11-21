package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

// srdEntryText: one entry of the SRD stat blocks the server embeds (data/srd521_monsters.json).
func srdEntryText(t *testing.T, monster, entry string) string {
	t.Helper()
	var ms []struct {
		Name    string                        `json:"name"`
		Traits  []struct{ Name, Text string } `json:"traits"`
		Actions []struct{ Name, Text string } `json:"actions"`
		Bonus   []struct{ Name, Text string } `json:"bonus_actions"`
	}
	if err := json.Unmarshal(srd521MonstersJSON, &ms); err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.Name != monster {
			continue
		}
		for _, a := range append(append(m.Actions, m.Traits...), m.Bonus...) {
			if a.Name == entry {
				return a.Text
			}
		}
	}
	t.Fatalf("no %s / %s in the SRD data", monster, entry)
	return ""
}

// Entry 63: a Failure's (or a hit's) damage "at the start (or end) of each of ... turns" is never damage taken now; the
// "While <Condition>, ..." sentence is read as the condition's lasting effects, for every SRD stat block that has one.
func TestLastingEffectsAreReadNotDealtNow(t *testing.T) {
	type want struct {
		save      bool
		damageNow []string // dice dealt now
		noRegain  bool
		tick      string // the tick's dice ("" none)
		extra     string // must stay in the GM's text ("" nothing to check)
	}
	for k, w := range map[[2]string]want{
		{"Pit Fiend", "Bite"}:                       {save: true, noRegain: true, tick: "6d6"},
		{"Vrock", "Spores (Recharge 6)"}:            {save: true, tick: "1d10", extra: "Holy Water"},
		{"Bone Devil", "Infernal Sting"}:            {noRegain: true},
		{"Bearded Devil", "Beard"}:                  {noRegain: true},
		{"Water Elemental", "Whelm (Recharge 4–6)"}: {save: true, damageNow: []string{"4d8+4"}, extra: "start of each of the elemental"},
		{"Shambling Mound", "Engulf"}:               {save: true, extra: "start of each of its turns"},
		{"Invisible Stalker", "Vortex"}:             {save: true, damageNow: []string{"1d8+3"}},
		{"Kraken", "Swallow"}:                       {save: true, damageNow: []string{"3d8+10"}},
		{"Remorhaz", "Swallow"}:                     {save: true},
		{"Gelatinous Cube", "Engulf"}:               {save: true, damageNow: []string{"3d6"}},
	} {
		text := srdEntryText(t, k[0], k[1])
		var e saveEffect
		var ok bool
		if w.save {
			e, ok = parseSaveEffect(text)
		} else {
			e, ok = parseHitRider(text)
		}
		if !ok {
			t.Errorf("%s: not read", k)
			continue
		}
		var now []string
		for _, d := range e.Damage {
			now = append(now, d.Dice)
		}
		if fmt.Sprint(now) != fmt.Sprint(w.damageNow) && !(len(now) == 0 && len(w.damageNow) == 0) {
			t.Errorf("%s: damage now %v, want %v", k, now, w.damageNow)
		}
		gotRegain, gotTick := false, ""
		if e.Lasting != nil {
			gotRegain = e.Lasting.NoRegain
			if e.Lasting.Tick != nil {
				gotTick = e.Lasting.Tick.Dice
			}
		}
		if gotRegain != w.noRegain || gotTick != w.tick {
			t.Errorf("%s: lasting no-regain %v tick %q, want %v %q", k, gotRegain, gotTick, w.noRegain, w.tick)
		}
		if w.extra != "" && !strings.Contains(e.Extra, w.extra) {
			t.Errorf("%s: the GM's text lost %q: %q", k, w.extra, e.Extra)
		}
		if e.Lasting != nil && (strings.Contains(e.Extra, "regain Hit Points") || strings.Contains(e.Extra, "While Poisoned")) {
			t.Errorf("%s: an applied lasting effect is still the GM's: %q", k, e.Extra)
		}
	}
	// the Bite's poison lasts at most 1 minute and its repeated save ends it
	if e, _ := parseSaveEffect(srdEntryText(t, "Pit Fiend", "Bite")); e.Rounds != 10 || !e.EndSave {
		t.Errorf("Bite: rounds %d, end save %v", e.Rounds, e.EndSave)
	}
	// the whole catalog: no save effect deals a damage now whose clause ticks later
	var ms []struct {
		Name    string                        `json:"name"`
		Actions []struct{ Name, Text string } `json:"actions"`
	}
	json.Unmarshal(srd521MonstersJSON, &ms)
	tick := regexp.MustCompile(`\((\d+d\d+(?:\s*[+−-]\s*\d+)?)\) [A-Za-z]+ damage at the (?:start|end) of each of`)
	for _, m := range ms {
		for _, a := range m.Actions {
			e, ok := parseSaveEffect(a.Text)
			if !ok {
				continue
			}
			for _, tm := range tick.FindAllStringSubmatch(mfxSection(a.Text, "Failure:", "Success:"), -1) {
				n := 0
				for _, d := range mfxDamageRe.FindAllStringSubmatch(a.Text, -1) {
					if normDice(d[2]) == normDice(tm[1]) {
						n++
					}
				}
				dealt := 0
				for _, d := range e.Damage {
					if d.Dice == normDice(tm[1]) {
						dealt++
					}
				}
				if dealt >= n {
					t.Errorf("%s / %s deals its recurring %s now", m.Name, a.Name, tm[1])
				}
			}
		}
	}
}

// Entry 64: the conditions a spell ends are one table; a pick spell ends the one named, else the first the target has.
func TestSpellEndsConditionsTable(t *testing.T) {
	for key, row := range spellEndsConditions {
		if _, ok := srdSpellsMemory[key]; !ok && len(srdSpellsMemory) > 0 {
			t.Errorf("%s is not an SRD spell", key)
		}
		for _, c := range row.Conditions {
			if !ruleConditions[c] {
				t.Errorf("%s ends %q, which is not a rules condition", key, c)
			}
		}
	}
	if got := spellEndsWords("lesser-restoration"); got != "Paralyzed, Poisoned, Blinded or Deafened" {
		t.Errorf("words: %q", got)
	}
}

// Entry 63 through the server: the Pit Fiend's Bite poisons, deals no poison damage now, records that the target can't regain Hit
// Points and takes 6d6 at the start of each of its turns; the tick goes through the Hit Point owner with its dice; healing gives
// nothing; conditions_detail says so; the poison's end ends both. Entry 64: Heal ends the Poisoned condition first and then heals;
// Lesser and Greater Restoration and Power Word Heal end theirs from the table. Chill Touch's rider stops a monster's healing.
func TestPostgresPoisonThatStopsHealing(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "pg poison gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Poison", "max_players": 2})
	lobby := int(j["campaign_id"].(float64))
	pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/schedule", lobby), gm, map[string]interface{}{"state": "off"})
	_, tok := pgAgent(t, "pg poison player")
	_, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Poisoned %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", lobby), tok, map[string]interface{}{"character_id": char})
	pgFinish(t, tok, char)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), tok, nil)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), gm, nil)
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", lobby), gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/start", lobby), gm, map[string]interface{}{}); c != 200 {
		t.Fatalf("combat start: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", lobby), gm, map[string]interface{}{
		"combatants": []map[string]interface{}{{"name": "Test Fiend", "monster_key": "pit-fiend"}}}); c != 200 {
		t.Fatalf("add: %d %v", c, j)
	}
	fiend := 0
	for _, e := range combatEntries(lobby) {
		if id := asInt(e["id"]); id < 0 {
			fiend = id
		}
	}
	// a human with Constitution 1: no poison Resistance, and every DC 21 Constitution save fails
	pgExec(t, `UPDATE characters SET species = 'human', con = 1, max_hp = 900, hp = 900 WHERE id = $1`, char)
	hp := func() int {
		var h int
		db.QueryRow(`SELECT hp FROM characters WHERE id = $1`, char).Scan(&h)
		return h
	}
	bite := func() string {
		t.Helper()
		for i := 0; i < 15; i++ {
			before := hp()
			c, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": lobby, "narration": "The fiend bites.",
				"monster_action": map[string]interface{}{"monster": "Test Fiend", "action": "Bite", "target": fmt.Sprint(char)}})
			if c != 200 {
				t.Fatalf("narrate: %d %v", c, j)
			}
			res := fmt.Sprint(j["monster_action_result"])
			if !strings.Contains(res, "HIT") {
				continue
			}
			if lost := before - hp(); lost > 44 { // 3d6 + 8 Piercing (6d6 + 8 on a Critical Hit); the poison's 6d6 is not dealt now
				t.Errorf("the Bite took %d Hit Points: its poison was dealt now: %s", lost, res)
			}
			return res
		}
		t.Fatal("fifteen Bites without a hit")
		return ""
	}
	res := bite()
	if !strings.Contains(res, "can't regain Hit Points") || !strings.Contains(res, "6d6") {
		t.Errorf("the reply doesn't say what the poison does: %s", res)
	}
	if !conditionHeldFrom(lobby, char, "poisoned", fiend) {
		t.Fatalf("no Poisoned record from the fiend (%d)", fiend)
	}
	// can't regain Hit Points: the Hit Point owner gives nothing
	pgExec(t, `UPDATE characters SET hp = 500 WHERE id = $1`, char)
	if h, err := healHP(char, 30); err != nil || h.Healed != 0 || hp() != 500 {
		t.Errorf("healed while Poisoned by the Bite: %+v %v (hp %d)", h, err, hp())
	}
	if cant, why := cantRegainHP(lobby, char); !cant || !strings.Contains(why, "Poisoned") {
		t.Errorf("cantRegainHP: %v %q", cant, why)
	}
	// conditions_detail says what the record does
	found := false
	for _, v := range conditionsDetail(lobby, char, 30, false) {
		if v["condition"] == "poisoned" {
			eff := fmt.Sprint(v["effects"])
			found = strings.Contains(eff, "Can't regain Hit Points while Poisoned") && strings.Contains(eff, "6d6") && strings.Contains(eff, "Constitution saving throw")
			if _, leak := v["source"]; leak {
				t.Errorf("the player's view shows the source: %v", v)
			}
		}
	}
	if !found {
		t.Errorf("conditions_detail doesn't show the poison's effects: %v", conditionsDetail(lobby, char, 30, false))
	}
	// the tick at the start of the target's turn: through the Hit Point owner, the dice shown
	before := hp()
	lines := strings.Join(recurringDamageTurnStart(lobby, char, "the target"), " ")
	if !strings.Contains(lines, "6d6 = ") || hp() >= before || before-hp() > 36 {
		t.Errorf("the tick: %q (hp %d → %d)", lines, before, hp())
	}
	// the fiend's own turn neither ticks the target's poison nor ends its effects
	expireCombatEffectsOf(lobby, fiend)
	if cant, _ := cantRegainHP(lobby, char); !cant {
		t.Error("the source's turn start ended the poison's no-healing")
	}
	// the poison ends (the repeated save, Lesser Restoration ...): both effects with it
	pgExec(t, `UPDATE characters SET hp = 500 WHERE id = $1`, char)
	endCondition(lobby, char, "poisoned", "")
	if h, _ := healHP(char, 30); h.Healed != 30 {
		t.Errorf("no healing after the poison ended: %+v", h)
	}
	before = hp()
	lines = strings.Join(recurringDamageTurnStart(lobby, char, "the target"), " ")
	if hp() != before || !strings.Contains(lines, "poisoned condition is over") {
		t.Errorf("the tick after the poison ended: %q (hp %d → %d)", lines, before, hp())
	}

	// entry 64: Heal ends the Poisoned condition first, then heals the 70
	bite()
	pgExec(t, `UPDATE characters SET hp = 500 WHERE id = $1`, char)
	out := applySpellHealing(char, "heal", 70, 70, "")
	if hp() != 570 || !strings.Contains(out, "Poisoned condition ends") || conditionHeldFrom(lobby, char, "poisoned", fiend) {
		t.Errorf("Heal: %q (hp %d)", out, hp())
	}
	// a healing spell without the row (Cure Wounds) ends nothing, and the poison still stops it
	bite()
	pgExec(t, `UPDATE characters SET hp = 500 WHERE id = $1`, char)
	out = applySpellHealing(char, "cure-wounds", 10, 10, "")
	if hp() != 500 || !strings.Contains(out, "can't regain Hit Points") {
		t.Errorf("Cure Wounds on the poisoned target: %q (hp %d)", out, hp())
	}
	// Lesser Restoration: the condition named, else the first of the row the target has
	applyCondition(lobby, char, "blinded", "test", 0, "", 0)
	if out := lesserRestoration(char, lobby, "cure the poison"); !strings.Contains(out, "Poisoned condition") || !hasCond(getCharConditions(char), "blinded") {
		t.Errorf("Lesser Restoration (named): %q %v", out, getCharConditions(char))
	}
	if out := lesserRestoration(char, lobby, ""); !strings.Contains(out, "Blinded condition") {
		t.Errorf("Lesser Restoration (first held): %q", out)
	}
	if out := lesserRestoration(char, lobby, "end the deafness"); !strings.Contains(out, "doesn't have the Deafened") {
		t.Errorf("Lesser Restoration (named, not held): %q", out)
	}
	// Greater Restoration's conditions come from the same table
	applyCondition(lobby, char, "charmed", "test", 0, "", 0)
	if out := greaterRestoration(char, lobby, "", nil); !strings.Contains(out, "Charmed condition") || hasCond(getCharConditions(char), "charmed") {
		t.Errorf("Greater Restoration: %q", out)
	}
	if out := greaterRestoration(char, lobby, "petrified", nil); !strings.Contains(out, "doesn't have the Petrified") {
		t.Errorf("Greater Restoration (named, not held): %q", out)
	}
	// Power Word Heal: every condition of its row
	for _, c := range []string{"stunned", "frightened", "poisoned"} {
		applyCondition(lobby, char, c, "test", 0, "", 0)
	}
	if ended, _ := endSpellConditions(lobby, char, "power-word-heal", ""); len(ended) != 3 {
		t.Errorf("Power Word Heal ended %v", ended)
	}
	// Mass Heal (no healing amount the server applies): its conditions end on the targets it names
	applyCondition(lobby, char, "deafened", "test", 0, "", 0)
	if out := spellEndsOnTargets(char, "mass-heal", "on me"); !strings.Contains(out, "Deafened") || hasCond(getCharConditions(char), "deafened") {
		t.Errorf("Mass Heal: %q", out)
	}

	// a monster under Chill Touch's rider can't regain Hit Points (the one rule for every creature)
	setMonsterHP(lobby, fiend, 100)
	addCombatEffect(lobby, fiend, char, "no_healing")
	if _, after, err := healMonster(lobby, fiend, 50); err != nil || after.HP != 100 {
		t.Errorf("a monster healed under Chill Touch: %v %v", after, err)
	}
	expireCombatEffectsOf(lobby, char) // the caster's next turn ends it
	if _, after, err := healMonster(lobby, fiend, 50); err != nil || after.HP != 150 {
		t.Errorf("no healing after Chill Touch ended: %v %v", after, err)
	}
}
