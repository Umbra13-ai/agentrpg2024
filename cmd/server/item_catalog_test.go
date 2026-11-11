package main

import (
	"encoding/json"
	"testing"
)

func TestParseCostGP(t *testing.T) {
	cases := map[string]float64{"15 gp": 15, "2 sp": 0.2, "5 cp": 0.05, "1 pp": 10, "": -1, "varies": -1}
	for in, want := range cases {
		if got := parseCostGP(in); got != want {
			t.Errorf("parseCostGP(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestBuildItemCatalog(t *testing.T) {
	rows := buildItemCatalog()
	counts := map[string]int{}
	seen := map[string]bool{}
	for _, r := range rows {
		counts[r.Category]++
		key := r.Category + "/" + r.Slug
		if r.Slug == "" || r.Name == "" {
			t.Errorf("row without a slug or name: %+v", r)
		}
		seen[key] = true
	}
	if counts["magic"] < 250 || counts["mount"] != 8 || counts["vehicle"] < 17 {
		t.Errorf("magic items, mounts or vehicles missing: %v", counts)
	}
	for _, c := range []string{"weapon", "armor", "gear", "tool", "magic", "mount", "vehicle"} {
		if counts[c] == 0 {
			t.Errorf("no %s items in the catalog", c)
		}
	}
	if seen["weapon/net"] || seen["weapon/crossbow-hand"] {
		t.Error("old spellings and the Net must not be catalog weapons")
	}
	for _, k := range []string{"weapon/dagger", "armor/chain-mail", "gear/backpack", "tool/thieves-tools", "magic/bag-of-holding", "magic/armor-1-2-or-3", "mount/warhorse", "vehicle/sailing-ship"} {
		if !seen[k] {
			t.Errorf("missing %s", k)
		}
	}
	if itemSlug("Thieves' Tools") != "thieves-tools" {
		t.Errorf("slug: %q", itemSlug("Thieves' Tools"))
	}
}

func TestCatalogSlugsAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, r := range buildItemCatalog() {
		key := r.Category + "/" + r.Slug
		if prev, dup := seen[key]; dup {
			t.Errorf("slug %s is used by %q and %q", key, prev, r.Name)
		}
		seen[key] = r.Name
	}
}

func TestMagicItemsAreComplete(t *testing.T) {
	var found srdMagicItem
	for _, m := range magicItemRowsForTest() {
		if m.Name == "Bag of Holding" {
			found = m
		}
	}
	if found.Rarity != "Uncommon" || found.RequiresAttunement || found.Description == "" {
		t.Errorf("Bag of Holding: %+v", found)
	}
}

func magicItemRowsForTest() []srdMagicItem {
	var items []srdMagicItem
	json.Unmarshal(srd521MagicItemsJSON, &items)
	return items
}
