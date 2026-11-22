package main

import (
	"fmt"
	"strings"
	"testing"
)

// A monster effect's "Second Failure" (Brass Dragon Wyrmling Sleep Breath, Cockatrice Petrifying Bite) is the condition owner's one
// repeated save with a second stage (readSecondFailure, conditionSave.Then), at a character, a monster and a companion: the first failure
// is the first condition until the end of the target's next turn; a failed repeat turns it into the second for its duration (an
// Unconscious that "ends if it takes damage" wakes on damage). A severe failure that only adds Unconscious to the Failure's Poisoned
// (Pseudodragon Sting) is read as the severe branch: Unconscious while Poisoned, waking on damage. The texts are invented.
func TestPostgresSecondFailureStages(t *testing.T) {
	testPostgres(t)
	gm, _, camp, charID, petID, _ := companionReactionFixture(t)
	pet := companionIDBase + petID
	m := compendiumFixture()
	m.Name, m.Type, m.Traits, m.Legendary, m.LegendaryIn = "Stage Drake", "Dragon", nil, nil, ""
	m.Actions = []srdMonsterEntry{
		{Name: "Drowse Breath", Text: "Constitution Saving Throw: DC 40, each creature in a 15-foot Cone. Failure: The target has the Incapacitated condition until the end of its next turn, at which point it repeats the save. Second Failure: The target has the Unconscious condition for 1 minute. This effect ends for the target if it takes damage or a creature within 5 feet of it takes an action to wake it."},
		{Name: "Stone Nip", Text: "Constitution Saving Throw: DC 40, one creature within 5 feet. First Failure: The target has the Restrained condition. The target repeats the save at the end of its next turn if it is still Restrained, ending the effect on itself on a success. Second Failure: The target has the Petrified condition, instead of the Restrained condition, for 24 hours."},
		{Name: "Stone Stare", Text: "Constitution Saving Throw: DC 40, one creature within 30 feet. Failure: The target has the Restrained condition and repeats the save at the end of its next turn if it is still Restrained, ending the effect on itself on a success. Second Failure: The target has the Petrified condition instead of the Restrained condition."},
		{Name: "Numbing Breath", Text: "Constitution Saving Throw: DC 40, each creature in a 15-foot Cone. Failure: The target has the Incapacitated condition until the end of its next turn, when it repeats the save. Second Failure: The target has the Paralyzed condition, and it repeats the save at the end of each of its turns, ending the effect on itself on a success. After 2 minutes, it succeeds automatically."},
		{Name: "Dozing Sting", Text: "Constitution Saving Throw: DC 40, one creature within 5 feet. Failure: The target has the Poisoned condition for 1 hour. Failure by 5 or More: While Poisoned, the target also has the Unconscious condition, which ends early if the target takes damage or a creature within 5 feet of it takes an action to wake it."},
	}
	victim := compendiumFixture()
	victim.Name, victim.Type, victim.Traits, victim.Legendary, victim.LegendaryIn, victim.CondImmune = "Stage Victim", "Beast", nil, nil, "", ""
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented second-failure fixture", "monsters": []srdMonster2024{m, victim}})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	keys := map[string]string{}
	for _, x := range imported["monsters"].([]interface{}) {
		mm := x.(map[string]interface{})
		key := fmt.Sprint(mm["monster_key"])
		keys[fmt.Sprint(mm["name"])] = key
		t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	}
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Stage Drake", "monster_key": keys["Stage Drake"], "hp": 100, "ac": 1, "initiative": -900},
		{"name": "Stage Victim", "monster_key": keys["Stage Victim"], "hp": 500, "ac": 1, "initiative": -900}}})
	pgExec(t, `UPDATE characters SET hp=300, max_hp=300, temp_hp=0 WHERE id=$1`, charID)
	victimID := monsterIDsByKeyOrName(camp, "Stage Victim")[0]
	petRow, _ := loadCompanion(petID)
	changeFight(camp, func(f *fight) error {
		f.place(map[string]interface{}{"id": petRow.CombatID, "name": petRow.Name, "initiative": 5, "dex_score": 12, "is_companion": true, "owner_id": petRow.OwnerID})
		return nil
	})
	pgExec(t, `UPDATE companions SET hp=300, max_hp=300 WHERE id=$1`, petID)
	index := func(id int) int {
		f, _ := readFight(db, camp)
		i, _ := f.entry(id)
		return i
	}
	recOf := func(id int, cond string) *conditionRecord {
		for _, r := range loadConditionRecords(camp, id) {
			if r.Condition == cond {
				rec := r
				return &rec
			}
		}
		return nil
	}
	act := func(action, target string) string {
		_, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
			"monster_action": map[string]interface{}{"monster": "Stage Drake", "action": action, "target": target, "target_ids": nil, "confirm_targeting": true}})
		return fmt.Sprint(r["monster_action_result"])
	}
	hurt := func(id int) {
		switch {
		case id == charID:
			damageCharacter(charID, 1, "slashing")
		case id == pet:
			c, _ := loadCompanion(petID)
			damageCompanion(c, 1, "slashing")
		default:
			hurtMonster(camp, id, 1, "slashing", false)
		}
	}
	clear := func(id int) {
		removeConditionsWhere(camp, id, func(conditionRecord) bool { return true })
	}
	for _, tg := range []struct {
		name, target string
		id           int
	}{{"character", fmt.Sprint(charID), charID}, {"monster", "Stage Victim", victimID}, {"companion", "React Pet", pet}} {
		// Sleep Breath: Incapacitated, then Unconscious (ends on damage)
		res := act("Drowse Breath", tg.target)
		rec := recOf(tg.id, "incapacitated")
		if rec == nil || rec.DurationRounds != 1 || rec.EndSave == nil || rec.EndSave.Then != "unconscious" || rec.EndSave.ThenRounds != 10 || !rec.EndSave.ThenEndsOnDamage {
			t.Fatalf("%s: Drowse Breath's first failure: %+v (%s)", tg.name, rec, res)
		}
		out := endTurnEffects(camp, index(tg.id))
		if rec = recOf(tg.id, "unconscious"); rec == nil || rec.Notes != notesEndsOnDamage || recOf(tg.id, "incapacitated") != nil {
			t.Fatalf("%s: the second failure: %+v %v", tg.name, rec, out.Public)
		}
		hurt(tg.id)
		if recOf(tg.id, "unconscious") != nil {
			t.Errorf("%s: damage didn't wake it", tg.name)
		}
		clear(tg.id)
		// Stone Nip: Restrained, then Petrified for 24 hours
		act("Stone Nip", tg.target)
		if rec = recOf(tg.id, "restrained"); rec == nil || rec.EndSave == nil || rec.EndSave.Then != "petrified" || rec.EndSave.ThenRounds != 14400 {
			t.Fatalf("%s: Stone Nip's first failure: %+v", tg.name, rec)
		}
		endTurnEffects(camp, index(tg.id))
		if rec = recOf(tg.id, "petrified"); rec == nil || rec.DurationRounds != 14400 || recOf(tg.id, "restrained") != nil {
			t.Errorf("%s: Stone Nip's second failure: %+v", tg.name, rec)
		}
		clear(tg.id)
		// Stone Stare (the Basilisk's pattern): Restrained, then Petrified with no end
		act("Stone Stare", tg.target)
		endTurnEffects(camp, index(tg.id))
		if rec = recOf(tg.id, "petrified"); rec == nil || rec.DurationRounds != 0 || rec.EndSave != nil || recOf(tg.id, "restrained") != nil {
			t.Errorf("%s: Stone Stare's second failure: %+v", tg.name, rec)
		}
		clear(tg.id)
		// Numbing Breath (the silver dragons' pattern, other numbers): Incapacitated, then Paralyzed that keeps the repeated save, 2 minutes
		act("Numbing Breath", tg.target)
		if rec = recOf(tg.id, "incapacitated"); rec == nil || rec.EndSave == nil || rec.EndSave.Then != "paralyzed" || !rec.EndSave.ThenRepeats || rec.EndSave.ThenRounds != 20 {
			t.Fatalf("%s: Numbing Breath's first failure: %+v", tg.name, rec)
		}
		endTurnEffects(camp, index(tg.id))
		if rec = recOf(tg.id, "paralyzed"); rec == nil || rec.DurationRounds != 20 || rec.EndSave == nil || rec.EndSave.DC != 40 || rec.EndSave.Then != "" {
			t.Errorf("%s: Numbing Breath's second failure: %+v", tg.name, rec)
		}
		clear(tg.id)
		// Dozing Sting: a failure by 5 or more is Poisoned and Unconscious, waking on damage
		res = act("Dozing Sting", tg.target)
		if recOf(tg.id, "poisoned") == nil || recOf(tg.id, "unconscious") == nil || strings.Contains(res, "Failure by 5 or More") {
			t.Fatalf("%s: Dozing Sting: %v (%s)", tg.name, loadConditionRecords(camp, tg.id), res)
		}
		hurt(tg.id)
		if recOf(tg.id, "unconscious") != nil || recOf(tg.id, "poisoned") == nil {
			t.Errorf("%s: damage should wake it and leave it Poisoned: %v", tg.name, loadConditionRecords(camp, tg.id))
		}
		clear(tg.id)
	}
}

// The CR 0-1 save actions' timing and limited-use guards, read from the SRD 5.2.1 stat blocks the server embeds: the duration and the
// repeated save the condition owner gets (the route tests of the readers: TestPostgresSecondFailureStages, the rider-duration and
// save-follow-up suites), the Recharge tag (rechargeRe: refused until it recharges) and the N/Day tag (monsterDayUse: reserved per use).
func TestCR01SaveActionTimingGuards(t *testing.T) {
	for _, c := range []struct {
		monster, entry string
		check          func(e saveEffect) bool
		recharge       bool
		perDay         int
	}{
		{"Lion", "Roar", func(e saveEffect) bool {
			return e.UntilStart && len(e.Conditions) == 1 && e.Conditions[0] == "frightened"
		}, false, 0},
		{"Pirate", "Enthralling Panache", func(e saveEffect) bool { return e.UntilStart && len(e.Conditions) == 1 && e.Conditions[0] == "charmed" }, false, 0},
		{"Brass Dragon Wyrmling", "Sleep Breath", func(e saveEffect) bool {
			return e.Rounds == 1 && e.EndSave && e.Stage != nil && e.Stage.Then == "unconscious" && e.Stage.ThenRounds == 10 && e.Stage.EndsOnDamage
		}, false, 0},
		{"Copper Dragon Wyrmling", "Slowing Breath", func(e saveEffect) bool {
			return e.Rounds == 1 && e.SpeedMode == "halved" && slicesContain(e.DisadvMode, "noreactions")
		}, false, 0},
		{"Harpy", "Luring Song", func(e saveEffect) bool { return e.EndSave && len(e.Conditions) == 1 && e.Conditions[0] == "charmed" }, false, 0},
		{"Pseudodragon", "Sting", func(e saveEffect) bool {
			return e.Rounds == 600 && e.SeverePoison != nil && e.SeverePoison.Margin == 5 && e.SeverePoison.Rounds == 600 && !strings.Contains(e.Extra, "Failure by")
		}, false, 0},
		{"Cockatrice", "Petrifying Bite", func(e saveEffect) bool {
			return e.EndSave && e.Stage != nil && e.Stage.Then == "petrified" && e.Stage.ThenRounds == 14400 && e.Extra == ""
		}, false, 0},
		{"Dust Mephit", "Blinding Breath (Recharge 6)", func(e saveEffect) bool { return e.UntilEnd && len(e.Conditions) == 1 }, true, 0},
		{"Steam Mephit", "Steam Breath (Recharge 6)", func(e saveEffect) bool { return e.UntilEnd && e.SpeedMode != "" }, true, 0},
		{"Giant Spider", "Web (Recharge 5–6)", func(e saveEffect) bool { return len(e.Conditions) == 1 && e.Conditions[0] == "restrained" }, true, 0},
		{"Swarm of Ravens", "Cacophony (Recharge 6)", func(e saveEffect) bool { return e.UntilStart && len(e.Conditions) == 1 }, true, 0},
		{"Dretch", "Fetid Cloud (1/Day)", func(e saveEffect) bool { return e.Rounds == 1 && slicesContain(e.DisadvMode, "noreactions") }, false, 1},
		{"Quasit", "Scare (1/Day)", func(e saveEffect) bool { return e.Rounds == 10 && e.EndSave }, false, 1},
	} {
		text := srdEntryText(t, c.monster, c.entry)
		e, ok := parseSaveEffect(text)
		if !ok || !c.check(e) {
			t.Errorf("%s %s: the timing read: %+v", c.monster, c.entry, e)
		}
		if got := rechargeRe.MatchString(c.entry); got != c.recharge {
			t.Errorf("%s %s: Recharge tag read %v", c.monster, c.entry, got)
		}
		if _, n := monsterDayUse(srdMonsterEntry{Name: c.entry, Text: text}, ""); n != c.perDay {
			t.Errorf("%s %s: N/Day read %d, want %d", c.monster, c.entry, n, c.perDay)
		}
	}
}

func slicesContain(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
