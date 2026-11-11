package main

import "testing"

func TestSpellEffectHelpers(t *testing.T) {
	if n := spellExtraTargets(1, 1, 3); n != 3 {
		t.Errorf("Bless at level 1: %d targets, want 3", n)
	}
	if n := spellExtraTargets(3, 1, 3); n != 5 {
		t.Errorf("Bless at level 3: %d targets, want 5", n)
	}
	if n := spellExtraTargets(2, 1, 1); n != 2 {
		t.Errorf("Command at level 2: %d targets, want 2", n)
	}
	if got := joinNames([]string{"A", "B", "C"}); got != "A, B and C" {
		t.Errorf("joinNames = %q", got)
	}
	if m := commandWordRe.FindStringSubmatch("cast command: grovel!"); m == nil || m[1] != "grovel" {
		t.Errorf("command word not found: %v", m)
	}
	if commandWordRe.MatchString("cast command at the orc") {
		t.Error("no command word should be found")
	}
	for _, w := range []string{"approach", "drop", "flee", "grovel", "halt"} {
		if commandText[w] == "" || commandTextYou[w] == "" {
			t.Errorf("no text for command %s", w)
		}
	}
}

func TestMonsterNormalRange(t *testing.T) {
	if n := monsterNormalRange("Ranged Attack Roll: +4, range 80/320 ft. Hit: 5 (1d6 + 2)"); n != 80 {
		t.Errorf("shortbow normal range %d, want 80", n)
	}
	if n := monsterNormalRange("Melee Attack Roll: +4, reach 5 ft."); n != 0 {
		t.Errorf("melee attack normal range %d, want 0", n)
	}
	if n := monsterNormalRange("Ranged Attack Roll: +5, range 60 ft."); n != 0 {
		t.Errorf("a range with no long range has no Disadvantage band, got %d", n)
	}
}

func TestSpellRollAdjustWithoutDB(t *testing.T) {
	if adj, note := spellRollAdjust(0, 5, "attack"); adj != 0 || note != "" {
		t.Errorf("no lobby: %d %q", adj, note)
	}
	if spellACAdjust(0, 5) != 0 || outlined(0, 5) {
		t.Error("no lobby: no AC change and not outlined")
	}
}
