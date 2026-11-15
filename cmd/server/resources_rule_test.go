package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// columnWriters lists the statements outside the owner files that assign one of the columns in an UPDATE of characters, and the
// SET clauses put together in Go that name one ("spell_slots_used = $1"). allowed(file, column, assignment) may excuse one.
func columnWriters(t *testing.T, columns []string, owners map[string]bool, allowed func(file, col, rhs string) bool) []string {
	update := regexp.MustCompile(`(?is)UPDATE\s+characters\b(.*?)\bWHERE\b`)
	var found []string
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || owners[f] {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, col := range columns {
			assign := regexp.MustCompile(`(?is)(?:\bSET\s+|,\s*)` + col + `\s*=\s*([^,]*)`)
			for _, m := range update.FindAllStringSubmatchIndex(src, -1) {
				for _, a := range assign.FindAllStringSubmatch(src[m[2]:m[3]], -1) {
					if allowed != nil && allowed(f, col, strings.TrimSpace(a[1])) {
						continue
					}
					found = append(found, f+":"+strconv.Itoa(strings.Count(src[:m[0]], "\n")+1)+" writes "+col)
				}
			}
			built := regexp.MustCompile(`"` + col + ` = `)
			for n, l := range strings.Split(src, "\n") {
				if built.MatchString(l) {
					found = append(found, f+":"+strconv.Itoa(n+1)+" builds a SET clause for "+col)
				}
			}
		}
	}
	return found
}

// TestNoOtherResourceWriter: what a character has expended (spell slots, Pact Magic slots, class resources and other counted uses,
// Indomitable) is changed only by the owner (resources_2024.go: changeSpent, with the row locked). Before Nov 15 2026 about 60
// places read a whole column, checked and changed it in Go and wrote it back without a lock (audit AUD-24, B-4, E-13). Two
// statements are excused: a reset to nothing ('{}' or 0, the rests: no value is read, so nothing is lost), and the start-up
// migration of the old "ki" key (database_init.go).
func TestNoOtherResourceWriter(t *testing.T) {
	cols := []string{"spell_slots_used", "pact_slots_used", "class_resources_used", "class_resources", "indomitable_used"}
	reset := regexp.MustCompile(`^('\{\}'|0)\s*$`)
	for _, w := range columnWriters(t, cols, map[string]bool{"resources_2024.go": true}, func(file, col, rhs string) bool {
		return reset.MatchString(strings.Fields(rhs + " ")[0]) || file == "database_init.go" && strings.Contains(rhs, "'ki'")
	}) {
		t.Errorf("%s: change what a character has expended through the owner (resources_2024.go)", w)
	}
}

// TestNoOtherInventoryWriter: a character's inventory and its attunement are changed only by the owner (inventory_2024.go:
// changeInventory, with the row locked; attunement ends there when an item leaves the inventory). Before Nov 15 2026 about 20
// places wrote the inventory whole without a lock (items given at once were lost) and attunement was never checked against it
// (audit AUD-24, B-4, E-4).
func TestNoOtherInventoryWriter(t *testing.T) {
	owners := map[string]bool{"inventory_2024.go": true}
	for _, w := range columnWriters(t, []string{"inventory", "attuned_items"}, owners, nil) {
		t.Errorf("%s: change the inventory or attunement through the owner (inventory_2024.go)", w)
	}
	// the storage functions are the owner's own
	call := regexp.MustCompile(`\b(loadInventoryTx|saveInventoryTx|attunedListTx|writeAttunedTx)\(`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || owners[f] {
			continue
		}
		b, _ := os.ReadFile(f)
		for n, l := range strings.Split(string(b), "\n") {
			if call.MatchString(l) {
				t.Errorf("%s:%d reads or writes the inventory's storage itself: use changeInventory (inventory_2024.go)", f, n+1)
			}
		}
	}
}

// TestAttunementFollowsTheItems: an attunement holds only while the item is carried; one per item; a destroyed item is gone,
// an item in a lost bag is still the character's.
func TestAttunementFollowsTheItems(t *testing.T) {
	inv := []invItem{
		{"id": "i1", "name": "Ring of Protection"},
		{"id": "i2", "name": "Cloak of Protection", "destroyed": true},
		{"id": "i3", "name": "Horn of Valhalla (Silver)"},
		{"id": "i4", "name": "Bag", "lost": true, "type": "container"},
		{"id": "i5", "name": "Amulet of Health", "location": "i4"},
	}
	kept, ended := attunedCarried([]string{"Ring of Protection", "Ring of Protection", "Cloak of Protection", "Horn of Valhalla",
		"amulet of health", "Belt of Giant Strength"}, inv)
	if strings.Join(kept, "|") != "Ring of Protection|Horn of Valhalla|amulet of health" {
		t.Errorf("kept %v", kept)
	}
	if strings.Join(ended, "|") != "Ring of Protection|Cloak of Protection|Belt of Giant Strength" {
		t.Errorf("ended %v", ended)
	}
}

// TestSpentCounts: a spend is refused when too little is left; a regain never goes below none expended and leaves an extra slot
// (a negative count) alone; a slot spend and a regain count by level.
func TestSpentCounts(t *testing.T) {
	s := spent{Slots: map[string]int{"1": 1}, Pact: map[string]int{}, Uses: map[string]int{"focus": 3}}
	if err := s.spend("focus", 2, 4); err == nil {
		t.Error("spent 2 of 1 left")
	} else if r, ok := refusedSpend(err); !ok || r.Left != 1 {
		t.Errorf("refusal %v", err)
	}
	if err := s.spend("focus", 1, 4); err != nil || s.Uses["focus"] != 4 {
		t.Errorf("spend: %v %v", err, s.Uses)
	}
	s.regain("focus", 10)
	if s.Uses["focus"] != 0 {
		t.Errorf("regain below 0: %v", s.Uses)
	}
	s.addSlot(2)
	s.regain("x", 1)
	if s.Slots["2"] != -1 || s.Uses["x"] != 0 {
		t.Errorf("extra slot: %v %v", s.Slots, s.Uses)
	}
	if spendSlotIn(s.Slots, 1, 1, "none") == nil {
		t.Error("spent a second level 1 slot of 1")
	}
	if !regainSlotIn(s.Slots, 1) || regainSlotIn(s.Slots, 1) {
		t.Error("regained a slot that wasn't expended")
	}
}
