package main

import (
	"regexp"
	"strings"
	"testing"
)

// splitNameTailRe: a text that begins with the end of its entry's name ("5–6). ...").
var splitNameTailRe = regexp.MustCompile(`^[^A-Za-z(]*\)`)

func TestMonsters2024Data(t *testing.T) {
	ms := srdMonsters2024()
	if len(ms) != 330 {
		t.Fatalf("expected 330 SRD stat blocks, got %d", len(ms))
	}
	seen := map[string]bool{}
	for _, m := range ms {
		if m.Name == "" || m.AC == 0 || m.HP == 0 || m.CR == "" || m.Size == "" || m.Type == "" || len(m.Abilities) != 6 {
			if m.Name != "Shrieker Fungus" || m.AC == 0 {
				t.Errorf("incomplete stat block: %+v", m.Name)
			}
		}
		if seen[monsterSlug(m.Name)] {
			t.Errorf("duplicate slug %s", m.Name)
		}
		seen[monsterSlug(m.Name)] = true
		// an entry's name is whole: "Petrifying Gaze (Recharge 5–6)" was once split at a line break, its recharge left in the text
		for _, g := range [][]srdMonsterEntry{m.Traits, m.Actions, m.BonusActions, m.Reactions, m.Legendary} {
			for _, e := range g {
				if strings.Count(e.Name, "(") != strings.Count(e.Name, ")") || splitNameTailRe.MatchString(e.Text) {
					t.Errorf("%s: entry %q is split (its text begins %.20q)", m.Name, e.Name, e.Text)
				}
			}
		}
	}
	var ab *srdMonster2024
	for i := range ms {
		if ms[i].Name == "Aboleth" {
			ab = &ms[i]
		}
	}
	if ab == nil || ab.Abilities["con"].Save != 6 || ab.Abilities["int"].Save != 8 || ab.Abilities["str"].Mod != 5 || ab.Initiative.Mod != 7 {
		t.Fatalf("Aboleth saves or modifiers wrong: %+v", ab)
	}
	acts := oldStyleActions(ab.Actions)
	if acts[1]["name"] != "Tentacle" || acts[1]["attack_bonus"] != 9 || acts[1]["damage_dice"] != "2d6+5" || acts[1]["damage_type"] != "Bludgeoning" {
		t.Errorf("Tentacle attack not parsed: %v", acts[1])
	}
	if monsterSlug("Goblin Warrior") != "goblin-warrior" || monsterSlug("Mummy Lord's Aide") != "mummy-lords-aide" {
		t.Error("monsterSlug")
	}
}

func TestEveryMonsterHasAppearanceAndLore(t *testing.T) {
	lore := monsterLore()
	for _, m := range srdMonsters2024() {
		l, ok := lore[monsterSlug(m.Name)]
		if !ok || len(l.Description) < 20 || len(l.Lore) < 20 {
			t.Errorf("%s has no appearance and lore text", m.Name)
		}
	}
}
