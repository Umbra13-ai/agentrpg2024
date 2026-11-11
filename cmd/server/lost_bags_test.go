package main

import "testing"

func sampleInventory() []invItem {
	return []invItem{
		{"id": "i1", "name": "Backpack", "type": "container", "location": "worn"},
		{"id": "i2", "name": "Pouch", "type": "container", "location": "i1"},
		{"id": "i3", "name": "Arrow", "quantity": 20.0, "location": "i1"},
		{"id": "i4", "name": "Gold dust", "location": "i2"},
		{"id": "i5", "name": "Dagger", "location": "worn"},
		{"id": "i6", "name": "Arcane Focus, Crystal", "location": "i2", "spellcasting_focus": true},
	}
}

func TestLostBagHidesEverythingInsideItAtAnyDepth(t *testing.T) {
	inv := sampleInventory()
	inv[0]["lost"] = true // the backpack
	usable := usableInventory(inv)
	if len(usable) != 1 || itemStr(usable[0], "name") != "Dagger" {
		t.Errorf("only the dagger should be usable, got %v", usable)
	}
	if len(inv) != 6 {
		t.Errorf("nothing may be removed from the stored inventory, have %d items", len(inv))
	}
}

func TestLostPouchOnlyHidesItsOwnContents(t *testing.T) {
	inv := sampleInventory()
	inv[1]["lost"] = true // just the pouch
	names := map[string]bool{}
	for _, it := range usableInventory(inv) {
		names[itemStr(it, "name")] = true
	}
	if !names["Backpack"] || !names["Arrow"] || !names["Dagger"] {
		t.Errorf("backpack, arrows and dagger should still be usable: %v", names)
	}
	if names["Pouch"] || names["Gold dust"] || names["Arcane Focus, Crystal"] {
		t.Errorf("pouch and its contents should be hidden: %v", names)
	}
}

func TestNothingLostMeansEverythingUsable(t *testing.T) {
	if got := len(usableInventory(sampleInventory())); got != 6 {
		t.Errorf("usable = %d, want 6", got)
	}
}

func TestUsableInventorySharesItemsSoStoredListStaysWhole(t *testing.T) {
	inv := sampleInventory()
	inv[1]["lost"] = true
	usable := usableInventory(inv)
	for _, it := range usable {
		if itemStr(it, "name") == "Arrow" {
			it["quantity"] = 19.0 // spend one arrow through the filtered view
		}
	}
	if itemQty(inv[2]) != 19 {
		t.Errorf("a change through the usable view must reach the stored list, got %d", itemQty(inv[2]))
	}
}

func TestUsableInventoryAnyMatchesTypedVersion(t *testing.T) {
	inv := sampleInventory()
	inv[0]["lost"] = true
	any := make([]interface{}, 0, len(inv))
	for _, it := range inv {
		any = append(any, map[string]interface{}(it))
	}
	if got := usableInventoryAny(any); len(got) != 1 {
		t.Errorf("got %d items, want 1", len(got))
	}
}

func TestSheetLayoutHidesLostBagsButGMViewShowsThem(t *testing.T) {
	inv := sampleInventory()
	inv[0]["lost"] = true
	inv[0]["lost_reason"] = "swept away by the river"

	sheet := buildEquipmentLayout(inv, "", false, "", "", false)
	if n := len(sheet["lost"].([]map[string]interface{})); n != 0 {
		t.Errorf("the public sheet must not list lost bags, got %d", n)
	}
	if n := len(sheet["containers"].([]map[string]interface{})); n != 0 {
		t.Errorf("a lost backpack must not appear among the bags, got %d", n)
	}
	for _, it := range sheet["worn"].([]map[string]interface{}) {
		if it["name"] == "Arrow" {
			t.Error("contents of a lost bag leaked into the sheet")
		}
	}

	gm := buildEquipmentLayout(inv, "", false, "", "", true)
	lost := gm["lost"].([]map[string]interface{})
	if len(lost) != 1 || lost[0]["lost_reason"] != "swept away by the river" {
		t.Errorf("the GM should see the lost backpack with its reason, got %v", lost)
	}
	contents := lost[0]["contents"].([]map[string]interface{})
	if len(contents) == 0 {
		t.Error("the GM view should show what is inside the lost bag")
	}
}

func TestLostBagsListsOnlyOutermost(t *testing.T) {
	inv := sampleInventory()
	inv[0]["lost"] = true
	inv[1]["lost"] = true // pouch is inside the lost backpack
	got := lostBags(inv)
	if len(got) != 1 || got[0]["name"] != "Backpack" {
		t.Errorf("only the backpack should be listed, got %v", got)
	}
}

func TestLostFocusCannotCastButOwnedFocusCan(t *testing.T) {
	inv := sampleInventory()
	inv[0]["lost"] = true
	if msg := checkSpellComponents("V, S, M", nil, usableInventory(inv), false, nil, nil, "wizard", 3); msg == "" {
		t.Error("a focus in a lost bag must not satisfy material components")
	}
	inv[0]["lost"] = false
	if msg := checkSpellComponents("V, S, M", nil, usableInventory(inv), false, nil, nil, "wizard", 3); msg != "" {
		t.Errorf("the same focus in a found bag should work: %q", msg)
	}
}

func TestAttacksIgnoreWeaponsInLostBags(t *testing.T) {
	inv := []invItem{
		{"id": "i1", "name": "Backpack", "type": "container", "location": "worn", "lost": true},
		{"id": "i2", "name": "longsword", "location": "i1"},
		{"id": "i3", "name": "dagger", "location": "worn"},
	}
	sb := map[string]interface{}{}
	addCombatBlock(sb, combatInput{Class: "fighter", Level: 1, Str: 16, Dex: 10, WeaponProfs: "simple, martial", Inventory: inv,
		MainHand: "longsword", OffHand: "dagger", BaseSpeed: 30})
	names := map[string]bool{}
	for _, a := range sb["attacks"].([]sbEntry) {
		names[a.Name] = true
	}
	if names["Longsword"] || !names["Dagger"] {
		t.Errorf("attacks should list the dagger but not the lost longsword: %v", names)
	}
}

func TestEquippedWeaponInLostBagIsNotAnAttackOrEquipped(t *testing.T) {
	inv := []invItem{
		{"id": "i1", "name": "Backpack", "type": "container", "location": "worn", "lost": true},
		{"id": "i2", "name": "javelin", "location": "i1"},
	}
	if !allMatchingLost(inv, "Javelin") || !allMatchingLost(inv, "javelin") {
		t.Error("the javelin is only in a lost bag")
	}
	if allMatchingLost(inv, "Longsword") {
		t.Error("an item that is not in the inventory is not lost")
	}
	sb := map[string]interface{}{}
	addCombatBlock(sb, combatInput{Class: "fighter", Level: 1, Str: 16, Dex: 10, WeaponProfs: "simple, martial",
		Inventory: inv, MainHand: "javelin", BaseSpeed: 30})
	for _, a := range sb["attacks"].([]sbEntry) {
		if a.Name == "Javelin" {
			t.Error("an equipped weapon in a lost bag must not be listed")
		}
	}
	// the layout does not show it as held either
	layout := buildEquipmentLayout(inv, "", false, "javelin", "", false)
	if n := len(layout["held"].([]map[string]interface{})); n != 0 {
		t.Errorf("held should be empty, got %d", n)
	}
}

func TestAllMatchingLostNeedsEveryCopyLost(t *testing.T) {
	inv := []invItem{
		{"id": "i1", "name": "Backpack", "type": "container", "lost": true},
		{"id": "i2", "name": "dagger", "location": "i1"},
		{"id": "i3", "name": "dagger", "location": "worn"},
	}
	if allMatchingLost(inv, "dagger") {
		t.Error("one dagger is still carried, so the dagger is not lost")
	}
}
