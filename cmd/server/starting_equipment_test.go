package main

import (
	"strings"
	"testing"
)

// Pack weights from the SRD 5.2.1 gear table.
func TestPackContentsWeighTheSRDTotals(t *testing.T) {
	want := map[string]float64{
		"burglar's pack": 42, "diplomat's pack": 39, "dungeoneer's pack": 55, "entertainer's pack": 58.5,
		"explorer's pack": 55, "priest's pack": 29, "scholar's pack": 22,
	}
	for name, total := range want {
		p, ok := packContents[name]
		if !ok {
			t.Fatalf("missing pack %q", name)
		}
		sum, _ := catalogWeightFor(p.Container)
		for _, it := range p.Items {
			w, ok := catalogWeightFor(it.Name)
			if !ok && !isZeroWeightItem(it.Name) {
				t.Errorf("%s: %q has no weight", name, it.Name)
			}
			sum += w * float64(it.Qty)
		}
		if sum != total {
			t.Errorf("%s weighs %v, SRD says %v", name, sum, total)
		}
	}
}

// Items the SRD lists with no weight.
func isZeroWeightItem(name string) bool {
	switch strings.ToLower(name) {
	case "bell", "ink", "ink pen", "paper", "parchment", "perfume", "candle":
		return true
	}
	return false
}

// Gold left over for each class's kit (option A) from the Core ... Traits tables.
func TestStartingGoldMatchesTheSRD(t *testing.T) {
	a := map[string]int{"barbarian": 15, "bard": 19, "cleric": 7, "druid": 9, "fighter": 4, "monk": 11,
		"paladin": 9, "ranger": 7, "rogue": 8, "sorcerer": 28, "warlock": 15, "wizard": 5}
	gold := map[string]int{"barbarian": 75, "bard": 90, "cleric": 110, "druid": 50, "monk": 50, "paladin": 150,
		"ranger": 150, "rogue": 100, "sorcerer": 50, "warlock": 100, "wizard": 55}
	for class, want := range a {
		if got := classStartingEquipment[class]["a"].Gold; got != want {
			t.Errorf("%s option A gold = %d, want %d", class, got, want)
		}
	}
	for class, want := range gold {
		if got := classStartingEquipment[class]["b"]; got.Gold != want || len(got.Items) != 0 {
			t.Errorf("%s gold-only option = %+v, want %d gold", class, got, want)
		}
	}
	f := classStartingEquipment["fighter"]
	if f["b"].Gold != 11 || f["c"].Gold != 155 || len(f["c"].Items) != 0 {
		t.Errorf("fighter options B/C = %+v / %+v", f["b"], f["c"])
	}
}

func TestEveryClassKitBuilds(t *testing.T) {
	for class, opts := range classStartingEquipment {
		for key, o := range opts {
			if len(o.Items) == 0 {
				continue
			}
			res, err := buildStartingEquipment(class, key, "lute")
			if err != nil {
				t.Errorf("%s %s: %v", class, key, err)
				continue
			}
			ids := map[string]bool{}
			for _, it := range res.Items {
				id := itemStr(it, "id")
				if ids[id] {
					t.Errorf("%s %s: duplicate id %s", class, key, id)
				}
				ids[id] = true
			}
			for _, it := range res.Items { // every location is empty, worn, held, or a real bag
				loc := itemStr(it, "location")
				if loc != "" && loc != "worn" && loc != "held" && !ids[loc] {
					t.Errorf("%s %s: %q is in unknown location %q", class, key, itemStr(it, "name"), loc)
				}
			}
		}
	}
}

func TestFighterKitIsEquippedAndPacked(t *testing.T) {
	res, err := buildStartingEquipment("fighter", "a", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.EquippedArmor != "chain-mail" || res.MainHand != "greatsword" || res.Shield {
		t.Errorf("fighter A: armor=%q main=%q shield=%v", res.EquippedArmor, res.MainHand, res.Shield)
	}
	byName := map[string]invItem{}
	for _, it := range res.Items {
		byName[itemStr(it, "name")] = it
	}
	if loc := itemStr(byName["Chain Mail"], "location"); loc != "worn" {
		t.Errorf("chain mail location = %q", loc)
	}
	if loc := itemStr(byName["Greatsword"], "location"); loc != "held" {
		t.Errorf("greatsword location = %q", loc)
	}
	pack := byName["Backpack"]
	if pack == nil || itemStr(pack, "pack") != "Dungeoneer's Pack" || itemStr(pack, "location") != "worn" {
		t.Fatalf("backpack = %v", pack)
	}
	if itemStr(byName["Rations"], "location") != itemStr(pack, "id") || itemQty(byName["Rations"]) != 10 {
		t.Errorf("rations should be 10 inside the backpack: %v", byName["Rations"])
	}
}

func TestFighterOptionBHasQuiverOfArrows(t *testing.T) {
	res, err := buildStartingEquipment("fighter", "b", "")
	if err != nil {
		t.Fatal(err)
	}
	var quiver, arrows invItem
	for _, it := range res.Items {
		switch itemStr(it, "name") {
		case "Quiver":
			quiver = it
		case "Arrow":
			arrows = it
		}
	}
	if quiver == nil || arrows == nil || itemStr(arrows, "location") != itemStr(quiver, "id") || itemQty(arrows) != 20 {
		t.Errorf("20 arrows should be in the quiver: quiver=%v arrows=%v", quiver, arrows)
	}
	if res.EquippedArmor != "studded-leather-armor" {
		t.Errorf("armor = %q", res.EquippedArmor)
	}
}

func TestWizardKitHasFocusSpellbookAndGold(t *testing.T) {
	res, err := buildStartingEquipment("wizard", "a", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Gold != 5 {
		t.Errorf("gold = %d", res.Gold)
	}
	var staff, book invItem
	for _, it := range res.Items {
		switch itemStr(it, "name") {
		case "Quarterstaff":
			staff = it
		case "Spellbook":
			book = it
		}
	}
	if staff == nil || !itemBool(staff, "spellcasting_focus") || itemStr(staff, "focus_kind") != "arcane" {
		t.Errorf("quarterstaff should be the arcane focus: %v", staff)
	}
	if book == nil {
		t.Fatal("no spellbook")
	}
	fillItemWeights(res.Items)
	if itemNum(book, "weight") != 3 {
		t.Errorf("spellbook weight = %v", itemNum(book, "weight"))
	}
}

func TestGoldOnlyOptionGivesNoItems(t *testing.T) {
	res, err := buildStartingEquipment("barbarian", "b", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 0 || res.Gold != 75 {
		t.Errorf("barbarian B = %d items, %d gold", len(res.Items), res.Gold)
	}
}

func TestBardAndMonkNeedTheirChosenTool(t *testing.T) {
	if _, err := buildStartingEquipment("monk", "a", ""); err == nil || err.Error() != "tool_choice_required" {
		t.Errorf("monk without a tool: %v", err)
	}
	res, err := buildStartingEquipment("monk", "a", "smith's tools")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range res.Items {
		if itemStr(it, "name") == "Smith's Tools" {
			found = true
		}
	}
	if !found {
		t.Error("monk kit should include the chosen Smith's Tools")
	}
	if _, err := buildStartingEquipment("bard", "a", "lute"); err != nil {
		t.Errorf("bard with lute: %v", err)
	}
}

func TestInvalidStartingOption(t *testing.T) {
	if _, err := buildStartingEquipment("wizard", "z", ""); err == nil {
		t.Error("expected an error for option z")
	}
	if _, err := buildStartingEquipment("jester", "a", ""); err == nil {
		t.Error("expected an error for an unknown class")
	}
}

func TestChosenToolForClass(t *testing.T) {
	if got := chosenToolForClass("bard", "thieves' tools, drum, lute"); got != "drum" {
		t.Errorf("bard = %q", got)
	}
	if got := chosenToolForClass("monk", "herbalism kit, smith's tools"); got != "smith's tools" {
		t.Errorf("monk = %q", got)
	}
	if got := chosenToolForClass("wizard", "lute"); got != "" {
		t.Errorf("wizard = %q", got)
	}
	// full names (BUGS 45) give the same tool, and the kit item keeps the plain name
	if got := chosenToolForClass("bard", "thieves' tools, musical instrument (drum), musical instrument (lute)"); got != "drum" {
		t.Errorf("bard (full names) = %q", got)
	}
	res, err := buildStartingEquipment("bard", "a", "musical instrument (lute)")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range res.Items {
		if itemStr(it, "name") == "Lute" {
			found = true
		}
	}
	if !found {
		t.Error("the bard kit should hold a Lute")
	}
}

func TestToolWeightsFromTheHandbook(t *testing.T) {
	for name, want := range map[string]float64{"Thieves' Tools": 1, "Smith's Tools": 8, "Lute": 2, "Herbalism Kit": 3, "Disguise Kit": 3, "Tinker's Tools": 10} {
		if got, ok := catalogWeightFor(name); !ok || got != want {
			t.Errorf("%s = %v, %v; want %v", name, got, ok, want)
		}
	}
}
