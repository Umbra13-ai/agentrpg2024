package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/agentrpg/agentrpg/game"
)

// The result line of a saving throw (game.SaveResult.Line and Tail) and the pattern that hides the numbers from players
// (player_text.go) are written for each other: every form of the line loses its numbers and keeps its words. The saver's own
// line (ForSaver) keeps them.
func TestPlayerTextHidesEverySaveLineForm(t *testing.T) {
	digits := regexp.MustCompile(`\d`)
	forms := []game.SavingThrow{
		{Ability: "dex", DC: 15, Bonus: 3},
		{Ability: "dex", DC: 15, Bonus: -2, Advantage: []string{"Danger Sense", "Haste"}},
		{Ability: "wis", DC: 15, Bonus: 0, Disadvantage: []string{"the Fool card"}},
		{Ability: "con", DC: 18, Bonus: 5, Advantage: []string{"War Caster"}, Disadvantage: []string{"Staggering Blow"}},
		{Ability: "str", DC: 12, Bonus: 1, Adjust: []game.RollAdjust{{Amount: 3, Why: "Bless +3"}, {Amount: -2, Why: "Exhaustion -2"}}},
		{Ability: "str", DC: 12, Bonus: 1, AutoFail: "Paralyzed"},
	}
	for _, s := range forms {
		for _, nat := range []int{1, 7, 20} {
			r := s.Judge(nat)
			if nat == 7 {
				r = r.Replace(9, "Halfling Luck")
			}
			for _, line := range []string{r.Line("Goblin") + ".", "Goblin repeats the Wisdom saving throw at the end of its turn and fails" + r.Tail() + ": it goes on."} {
				got := playerText(line)
				shown := regexp.MustCompile(`\[[^\]]*\]`).ReplaceAllString(strings.ReplaceAll(got, "one d20", ""), "") // "[Bless +3]" stays: it is a die of its own
				if digits.MatchString(shown) {
					t.Errorf("a player would read numbers in %q (from %q)", got, line)
				}
				for _, why := range append(append([]string{}, s.Advantage...), s.Disadvantage...) {
					if !strings.Contains(got, why) {
						t.Errorf("the reason %q is gone from %q", why, got)
					}
				}
			}
			if own := r.ForSaver(""); r.Rolled() && playerText(own) != own {
				t.Errorf("the saver's own line lost its numbers: %q -> %q", own, playerText(own))
			}
		}
	}
}

// Every source row has a rule and a name, so a result can always say why a save had Advantage, Disadvantage or failed.
func TestSaveSourcesAreNamed(t *testing.T) {
	counts := map[int]int{}
	for i, s := range saveSources {
		if s.applies == nil {
			t.Fatalf("save source %d has no rule", i)
		}
		counts[s.effect]++
	}
	if counts[saveAdv] < 20 || counts[saveDis] < 4 || counts[saveFail] < 1 {
		t.Errorf("save sources by kind: %v", counts)
	}
	if len(saveAdjusts) < 6 {
		t.Errorf("save adjustments: %d", len(saveAdjusts))
	}
}

// The one saving throw function: no handler rolls a d20 for a saving throw itself. Every d20 rolled in the server outside
// game.SavingThrow and game.AttackRoll must be in a function listed here, with what it is for; a saving throw builds a saveCtx
// (save_2024.go) and calls roll or resist. Before Nov 15 2026 saves were hand-rolled in about twenty places.
func TestNoOtherSaveRoll(t *testing.T) {
	notASave := map[string]string{
		"actions.go:resolveActionWithBonusDash":         "Dexterity (Sleight of Hand) check to pick a lock",
		"actions.go:resolveStabilizeAction":             "Wisdom (Medicine) check",
		"actions_checks_2024.go:resolveCheckAction":     "ability checks",
		"attack_target.go:resolveAttackOnMonster":       "the Luck Blade's reroll of an attack roll, judged by game.AttackRoll",
		"buff_spells_2024.go:buffCheckRoll":             "ability checks",
		"buff_spells_2024.go:dispelSpellEffects":        "Dispel Magic's ability check",
		"combat_handlers.go:handleCombatAdd":            "Initiative",
		"combat_handlers.go:handleCombatStart":          "Initiative",
		"combat_handlers.go:handleRoll":                 "the public dice roller",
		"condition_records.go:handleConditionEscape":    "an escape check",
		"gm_2024.go:handleGMEscapeGrapple2024":          "an escape check",
		"gm_checks.go:handleGMContestedCheck":           "contested checks",
		"gm_checks.go:handleGMSkillCheck":               "skill checks",
		"gm_checks.go:handleGMToolCheck":                "tool checks",
		"gm_group_check_2024.go:handleGMGroupCheck":     "group checks",
		"gm_spells.go:handleGMDispelMagic":              "Dispel Magic's ability check",
		"hide_2024.go:attemptHide":                      "a Dexterity (Stealth) check",
		"hide_2024.go:attemptMonsterHide":               "a monster's Dexterity (Stealth) check",
		"legendary_items_2024.go:addMonstersToFight":    "Initiative",
		"legendary_items_2024.go:sphereOfAnnihilation":  "an Intelligence (Arcana) check",
		"magic_item_use_2024.go:planItemCast":           "a Spell Scroll's ability check",
		"magic_item_use_2024.go:spendCharges":           "the d20 for an item's last charge",
		"spell_resolution.go:resolveSpellOnMonster":     "Seeking Spell's reroll of an attack roll, judged by game.AttackRoll",
		"time_conditions_2024.go:handleGMRoll":          "the GM's dice roller",
		"toolbox_more_2024.go:jumpAction":               "a Strength (Athletics) check",
		"check_2024.go:rollCheckD20":                    "the one ability check d20 (TestNoOtherCheckRoll)",
		"monster_attach_2024.go:handleAttachDetach2024": "the published detach action's ability check",
	}
	d20 := regexp.MustCompile(`RollDie\(20\)|RollWith(Dis)?[Aa]dvantage\(`)
	funcName := regexp.MustCompile(`^func (?:\([^)]*\) )?(\w+)`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		fn := ""
		for n, line := range strings.Split(string(b), "\n") {
			if m := funcName.FindStringSubmatch(line); m != nil {
				fn = m[1]
			}
			if d20.MatchString(line) && !strings.HasPrefix(strings.TrimSpace(line), "//") {
				if _, ok := notASave[f+":"+fn]; !ok {
					t.Errorf("%s:%d (%s) rolls a d20 itself: a saving throw is a saveCtx (save_2024.go); anything else goes in this test's list with what it is", f, n+1, fn)
				}
			}
		}
	}
	// a saving throw modifier is worked out in one place (saveModifier, saveProficiency)
	own := regexp.MustCompile("saving_throws FROM classes")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "class_extras_2024.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		for n, line := range strings.Split(string(b), "\n") {
			if own.MatchString(line) {
				t.Errorf("%s:%d reads a class's saving throws itself: use saveModifier (save_2024.go)", f, n+1)
			}
		}
	}
}

// A Death Saving Throw and an ordinary save are told apart: a save of an unknown ability is no death save.
func TestDeathSaveIsNamed(t *testing.T) {
	c := saveCtx{ability: game.DeathSave}
	if c.is("wis", game.DeathSave) != true {
		t.Error("the death save marker")
	}
	if shortAbility("death") != "" || shortAbility("Wisdom") != "wis" {
		t.Error("shortAbility")
	}
}
