package main

import "testing"

func TestBodyPlaceOf(t *testing.T) {
	cases := []struct {
		name, place string
		limited     bool
	}{
		{"Boots of Elvenkind", "feet", true}, {"Slippers of Spider Climbing", "feet", true}, {"Gauntlets of Ogre Power", "hands", true},
		{"Gloves of Thievery", "hands", true}, {"Bracers of Defense", "wrists", true}, {"Bracers of Archery", "wrists", true},
		{"Hat of Disguise", "head", true}, {"Helm of Brilliance", "head", true}, {"Headband of Intellect", "head", true},
		{"Circlet of Blasting", "head", true}, {"Cap of Water Breathing", "head", true}, {"Cloak of Protection", "shoulders", true},
		{"Cape of the Mountebank", "shoulders", true}, {"Ring of Protection", "fingers", false}, {"Necklace of Adaptation", "neck", false},
		{"Amulet of Health", "neck", false}, {"Periapt of Wound Closure", "neck", false}, {"Belt of Dwarvenkind", "waist", false},
		{"Robe of Eyes", "body", false}, {"Goggles of Night", "eyes", false}, {"Wand of Web", "", false}, {"Arrows", "", false},
	}
	for _, c := range cases {
		p, lim, _ := bodyPlaceOf(c.name)
		if p != c.place || lim != c.limited {
			t.Errorf("%s: got %q limited=%v, want %q limited=%v", c.name, p, lim, c.place, c.limited)
		}
	}
}
