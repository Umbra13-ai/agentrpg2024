package game

import "testing"

func TestKey(t *testing.T) {
	same := [][]string{
		{"Hunter’s Mark", "hunters-mark", "Hunters_Mark", "hunter's mark", "  HUNTERS MARK "},
		{"Blindness/Deafness", "blindness-deafness", "blindness_deafness", "Blindness / Deafness"},
		{"Path of the Berserker", "path-of-the-berserker", "path_of_the_berserker"},
		{"Sleight of Hand", "sleight_of_hand", "sleight-of-hand"},
		{"Thieves’ Tools", "thieves tools", "Thieves' Tools", "thieves-tools"},
		{"Circle of the Land (Arid)", "circle_of_the_land_arid"},
	}
	for _, g := range same {
		for _, s := range g[1:] {
			if Key(s) != Key(g[0]) {
				t.Errorf("Key(%q) = %q, Key(%q) = %q", s, Key(s), g[0], Key(g[0]))
			}
		}
	}
	if Slug("Arcanist’s Magic Aura") != "arcanists-magic-aura" || Slug("Enlarge/Reduce") != "enlarge-reduce" {
		t.Error("Slug")
	}
	opts := []Option{{Slug: "open-hand", Name: "Warrior of the Open Hand", Aliases: []string{"Open Hand"}}}
	for _, a := range []string{"open_hand", "Warrior of the Open Hand", "open hand", "OPEN-HAND"} {
		if o, ok := Match(a, opts); !ok || o.Slug != "open-hand" {
			t.Errorf("Match(%q)", a)
		}
	}
	if _, ok := Match("", opts); ok {
		t.Error("an empty answer matched")
	}
	if s, ok := MatchString("Potent Spellcasting", []string{"divine_strike", "potent_spellcasting"}); !ok || s != "potent_spellcasting" {
		t.Error("MatchString")
	}
}
