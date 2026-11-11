package main

import (
	"strings"
	"testing"
)

func TestBrutalStrike2024(t *testing.T) {
	if brutalStrikeDice(9) != "1d10" || brutalStrikeDice(17) != "2d10" {
		t.Error("Brutal Strike is 1d10, 2d10 from level 17")
	}
	if brutalStrikeWanted("barbarian", 8, "reckless brutal strike", true) || brutalStrikeWanted("barbarian", 9, "brutal strike", false) || brutalStrikeWanted("fighter", 9, "reckless brutal strike", true) {
		t.Error("Brutal Strike needs a Barbarian of level 9+ who attacked recklessly")
	}
	if !brutalStrikeWanted("Barbarian", 9, "reckless, brutal strike", true) {
		t.Error("a level 9 reckless Barbarian can Brutal Strike")
	}
	_, note := brutalStrikeHit(1, 9, "brutal strike staggering blow", false)
	if strings.Contains(note, "Staggering") {
		t.Error("Staggering Blow comes with Improved Brutal Strike at level 13")
	}
	_, note = brutalStrikeHit(1, 13, "brutal strike staggering blow", false)
	if !strings.Contains(note, "Staggering Blow") {
		t.Error("level 13 can choose Staggering Blow")
	}
	_, note = brutalStrikeHit(1, 13, "brutal strike hamstring blow and staggering blow", false)
	if strings.Contains(note, "Staggering") {
		t.Error("only one effect before level 17")
	}
}

func TestHitOnlyNotes(t *testing.T) {
	in := "RECKLESS! [Frenzy +9] [Brutal Strike +3] [Hamstring Blow: slows] [Mastery Cleave: x]"
	out := hitOnlyNotes.ReplaceAllString(in, "")
	if strings.Contains(out, "Frenzy") || strings.Contains(out, "Brutal") || strings.Contains(out, "Hamstring") || !strings.Contains(out, "Mastery Cleave") {
		t.Errorf("hit-only notes not removed cleanly: %q", out)
	}
}

func TestVexEndsAtTheEndOfTheAttackersNextTurn(t *testing.T) {
	vexMarks.Store(7, vexMark{Lobby: 1, Target: -1})
	vexTurnEnded(7) // the end of the turn in which Vex was gained
	if _, ok := vexMarks.Load(7); !ok {
		t.Fatal("Vex must last until the end of the attacker's NEXT turn")
	}
	vexTurnEnded(8) // someone else's turn ends: nothing changes
	if _, ok := vexMarks.Load(7); !ok {
		t.Fatal("another creature's turn must not use up Vex")
	}
	vexTurnEnded(7) // the end of the next turn
	if _, ok := vexMarks.Load(7); ok {
		t.Fatal("Vex must be gone after the attacker's next turn ends")
	}
	vexMarks.Store(9, vexMark{Lobby: 5, Target: -2})
	clearVexMarks(5)
	if _, ok := vexMarks.Load(9); ok {
		t.Fatal("Vex marks are cleared when the campaign's combat starts or ends")
	}
}
