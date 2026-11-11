package main

import "testing"

func TestOriginSpells(t *testing.T) {
	drow := map[string]string{"lineage": "drow", "spellcasting_ability": "cha", "skill": "perception"}
	d2 := originSpellsFor(recFor(t, "elf", drow), 2)
	if _, ok := d2["faerie-fire"]; ok {
		t.Error("Faerie Fire comes at level 3")
	}
	if c := d2["dancing-lights"]; c.FreeMax != 0 || c.Ability != "cha" || c.Source != "Drow lineage" {
		t.Errorf("the lineage cantrip: %+v", c)
	}
	d5 := originSpellsFor(recFor(t, "elf", drow), 5)
	for _, s := range []string{"faerie-fire", "darkness"} {
		if o := d5[s]; o.FreeMax != 1 || o.FreeKey != "lineage_spell:"+s || o.Ability != "cha" {
			t.Errorf("%s should be a free cast once per Long Rest with Charisma: %+v", s, o)
		}
	}
	inf := originSpellsFor(recFor(t, "tiefling", map[string]string{"legacy": "infernal", "spellcasting_ability": "int", "size": "medium"}), 5)
	if o := inf["hellish-rebuke"]; o.FreeMax != 1 || o.Source != "Infernal legacy" || o.Ability != "int" {
		t.Errorf("Infernal legacy level 3 spell: %+v", o)
	}
	if _, ok := inf["thaumaturgy"]; !ok {
		t.Error("every Tiefling knows Thaumaturgy")
	}
	g := originSpellsFor(recFor(t, "gnome", map[string]string{"lineage": "forest", "spellcasting_ability": "wis"}), 9)
	if o := g["speak-with-animals"]; o.FreeMax != 4 || o.Source != "Forest Gnome" {
		t.Errorf("Forest Gnome Speak with Animals: Proficiency Bonus (4 at level 9) free casts: %+v", o)
	}
	rec := recFor(t, "dwarf", nil)
	rec.Feats = []originFeat{{Slug: "magic_initiate", Cantrips: []string{"guidance", "light"}, Spell: "bless", Ability: "wis"}}
	mi := originSpellsFor(rec, 1)
	if o := mi["bless"]; o.FreeMax != 1 || o.FreeKey != "magic_initiate:bless" || o.Source != "Magic Initiate" {
		t.Errorf("Magic Initiate spell: %+v", o)
	}
	if o := mi["light"]; o.FreeMax != 0 || o.Ability != "wis" {
		t.Errorf("Magic Initiate cantrip: %+v", o)
	}
	if len(originSpellsFor(recFor(t, "halfling", nil), 20)) != 0 {
		t.Error("halflings have no spells")
	}
}

func TestCountLeveled(t *testing.T) {
	if srdSpellsMemory == nil {
		t.Skip("spells not loaded")
	}
	if _, ok := srdSpellsMemory["fire-bolt"]; !ok {
		t.Skip("spells not loaded")
	}
	if n := countLeveled([]string{"fire-bolt", "shield", "magic-missile"}); n != 2 {
		t.Errorf("cantrips don't count: got %d", n)
	}
}
