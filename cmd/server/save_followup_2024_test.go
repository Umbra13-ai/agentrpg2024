package main

import (
	"fmt"
	"strings"
	"testing"
)

// The follow-up of a failed save (readRider): the repeated save in either word order ("repeats the save at the end of each of its
// turns" / "At the end of each of its turns, the target repeats the save": SRD 5.2.1 Quasit Scare, Djinni Create Whirlwind), the limit
// "After 1 minute, it succeeds automatically" as the condition's duration (Quasit, Doppelganger, Chuul, Pit Fiend), and a residual that
// never repeats an applied rider to the GM (Slowing Breath, Ice Devil Ice Spear, Dretch Fetid Cloud).
func TestSaveFollowUpParsing(t *testing.T) {
	for _, c := range []struct {
		text    string
		endSave bool
		rounds  int
		extra   string // what the GM reads ("" nothing)
	}{
		{"Wisdom Saving Throw: DC 10, one creature. Failure: The target has the Frightened condition. At the end of each of its turns, the target repeats the save, ending the effect on itself on a success. After 1 minute, it succeeds automatically.", true, 10, ""},
		{"Wisdom Saving Throw: DC 10, one creature. Failure: The target has the Frightened condition, and it repeats the save at the end of each of its turns, ending the effect on itself on a success. After 1 minute, it succeeds automatically.", true, 10, ""},
		{"Wisdom Saving Throw: DC 10, one creature. Failure: The target has the Frightened condition for 1 minute. It repeats the save at the end of each of its turns, ending the effect on itself on a success.", true, 10, ""},
		{"Constitution Saving Throw: DC 11, each creature in a 15-foot Cone. Failure: The target can’t take Reactions; its Speed is halved; and it can take either an action or a Bonus Action on its turn, not both. This effect lasts until the end of its next turn.", false, 1,
			"it can take either an action or a Bonus Action on its turn, not both"},
	} {
		e, ok := parseSaveEffect(c.text)
		if !ok || e.EndSave != c.endSave || e.Rounds != c.rounds || e.Extra != c.extra {
			t.Errorf("%q:\n endSave %v rounds %d extra %q; want %v %d %q", c.text, e.EndSave, e.Rounds, e.Extra, c.endSave, c.rounds, c.extra)
		}
	}
	// a limit sentence of ANOTHER effect of the same text (Sphinx of Valor's Roar) stays with the GM
	e, _ := parseSaveEffect("Wisdom Saving Throw: DC 20, each enemy in a 500-foot Emanation. Failure: The target has the Frightened condition for 1 minute. Wisdom Saving Throw: DC 20, each enemy. Failure: The target has the Paralyzed condition, and it repeats the save at the end of each of its turns, ending the effect on itself on a success. After 1 minute, it succeeds automatically.")
	if e.Rounds != 10 || !strings.Contains(e.Extra, "After 1 minute, it succeeds automatically") {
		t.Errorf("another effect's limit was dropped from the GM's text: rounds %d extra %q", e.Rounds, e.Extra)
	}
	// a hit's Speed rider applied from a sentence it shares with unapplied clauses (SRD 5.2.1 Ice Devil Ice Spear)
	h, ok := parseHitRider("Melee Attack Roll: +10, reach 10 ft. Hit: 10 (2d8 + 1) Piercing damage plus 10 (3d6) Cold damage. Until the end of its next turn, the target can’t take a Bonus Action or Reaction, its Speed decreases by 10 feet, and it can move or take one action on its turn, not both.")
	if !ok || h.SpeedMode != "minus" || strings.Contains(h.Extra, "Speed") || !strings.Contains(h.Extra, "can move or take one action") {
		t.Errorf("Ice Spear: speed %q extra %q", h.SpeedMode, h.Extra)
	}
}

// The repeated save in the reversed word order and the 1-minute limit reach the stored condition through the route: POST /api/gm/narrate
// a Scare-pattern action (invented text) at a character; its Frightened record carries the repeated save and 10 rounds, and the reply
// gives the GM nothing of it to apply.
func TestPostgresRepeatedSaveWording(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, _, _ := companionReactionFixture(t)
	m := compendiumFixture()
	m.Name, m.Type, m.Traits, m.Legendary, m.LegendaryIn = "Scare Imp", "Fiend", nil, nil, ""
	m.Actions = []srdMonsterEntry{{Name: "Fright", Text: "Wisdom Saving Throw: DC 40, one creature. Failure: The target has the Frightened condition. At the end of each of its turns, the target repeats the save, ending the effect on itself on a success. After 1 minute, it succeeds automatically."}}
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented repeated-save fixture", "monsters": []srdMonster2024{m}})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	key := imported["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Scare Imp", "monster_key": key, "hp": 50, "ac": 1, "initiative": -600}}})
	_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
		"monster_action": map[string]interface{}{"monster": "Scare Imp", "action": "Fright", "target": fmt.Sprint(charID)}})
	res := fmt.Sprint(r["monster_action_result"])
	var rec *conditionRecord
	for _, c := range loadConditionRecords(camp, charID) {
		if c.Condition == "frightened" {
			c := c
			rec = &c
		}
	}
	if rec == nil || rec.EndSave == nil || rec.EndSave.DC != 40 || rec.DurationRounds != 10 {
		t.Fatalf("Frightened not stored with its repeated save and 1-minute limit: %+v (%s)", rec, res)
	}
	if strings.Contains(res, "succeeds automatically") {
		t.Errorf("the applied limit was also given to the GM: %s", res)
	}
}
