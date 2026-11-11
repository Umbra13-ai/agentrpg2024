package main

import (
	"strings"
	"testing"
)

func TestWeaponTableIsComplete(t *testing.T) {
	rows := srdWeaponTable()
	if len(rows) != 38 {
		t.Fatalf("the SRD 5.2.1 has 38 weapons, got %d", len(rows))
	}
	count := map[string]int{}
	for _, w := range rows {
		count[w.Category]++
		if w.Name == "" || w.Damage == "" || w.Mastery == "" || w.Cost == "" || w.Weight == "" {
			t.Errorf("incomplete row: %+v", w)
		}
	}
	want := map[string]int{"Simple Melee Weapons": 10, "Simple Ranged Weapons": 4, "Martial Melee Weapons": 18, "Martial Ranged Weapons": 6}
	for g, n := range want {
		if count[g] != n {
			t.Errorf("%s: %d weapons, want %d", g, count[g], n)
		}
	}
}

// The combat code and the printed table must agree on damage, damage type, Mastery and weight.
func TestWeaponTableMatchesCombatData(t *testing.T) {
	applyWeaponMastery()
	bad := 0
	for _, row := range srdWeaponTable() {
		var found *SRDWeapon
		for key, w := range srdWeapons {
			if weaponNameKey(w.Name) == weaponNameKey(row.Name) || weaponNameKey(key) == weaponNameKey(row.Name) {
				ww := w
				found = &ww
				break
			}
		}
		if found == nil {
			continue // weapons that only come from the database seed are checked live
		}
		if !strings.EqualFold(found.Damage, row.Damage) {
			t.Errorf("%s: combat damage %s, SRD %s", row.Name, found.Damage, row.Damage)
			bad++
		}
		if !strings.EqualFold(found.DamageType, row.DamageType) {
			t.Errorf("%s: combat damage type %s, SRD %s", row.Name, found.DamageType, row.DamageType)
			bad++
		}
		if found.Mastery != "" && found.Mastery != row.Mastery {
			t.Errorf("%s: combat mastery %s, SRD %s", row.Name, found.Mastery, row.Mastery)
			bad++
		}
		if w := weightPounds(row.Weight); w >= 0 && found.Weight != w {
			t.Errorf("%s: combat weight %v, SRD %v", row.Name, found.Weight, w)
			bad++
		}
	}
	_ = bad
}

func TestWeightPounds(t *testing.T) {
	cases := map[string]float64{"2 lb.": 2, "1/4 lb.": 0.25, "10 lb.": 10, "—": -1, "": -1}
	for in, want := range cases {
		if got := weightPounds(in); got != want {
			t.Errorf("weightPounds(%q) = %v, want %v", in, got, want)
		}
	}
}
