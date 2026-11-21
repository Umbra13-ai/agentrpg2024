package main

import "testing"

// The Graze on a miss: the Hit Points left after damage, and after damage an absorbing monster turned into healing.
func TestGrazeHP(t *testing.T) {
	cases := []struct {
		hp, max, gd, want int
		absorbed          bool
	}{
		{50, 100, 4, 46, false},
		{3, 100, 4, 0, false},
		{50, 100, -4, 54, true},
		{99, 100, -4, 100, true}, // never above the maximum
		{50, 100, 0, 50, false},
	}
	for _, c := range cases {
		got, abs := grazeHP(c.hp, c.max, c.gd)
		if got != c.want || abs != c.absorbed {
			t.Errorf("grazeHP(%d,%d,%d) = %d,%v; want %d,%v", c.hp, c.max, c.gd, got, abs, c.want, c.absorbed)
		}
	}
}

// The decision a Graze makes for an absorbing monster, through the shared extra-damage owner and the imported monster.
func TestPostgresGrazeAbsorbed(t *testing.T) {
	camp, id := importedAbsorber(t, "Cold")
	f, err := readFight(db, camp)
	if err != nil {
		t.Fatal(err)
	}
	_, e := f.entry(id)
	gd, how := adjustMonsterExtra(sprintAny(e["monster_key"]), "cold", 4)
	if gd != -4 || how != "absorbed" {
		t.Fatalf("Graze of 4 Cold on a Cold-absorbing monster: %d %q, want -4 absorbed", gd, how)
	}
	if hp, abs := grazeHP(10, 100, gd); hp != 14 || !abs {
		t.Fatalf("hp %d absorbed %v, want 14 true", hp, abs)
	}
	if gd, _ := adjustMonsterExtra(sprintAny(e["monster_key"]), "slashing", 4); gd < 0 {
		t.Fatalf("a Slashing Graze was absorbed: %d", gd)
	}
}
