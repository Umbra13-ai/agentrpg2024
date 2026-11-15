package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoOtherHitPointWriter: every change to a character's Hit Points or Temporary Hit Points is made by the Hit Point owner
// (hit_points_2024.go; grantTempHP and loseTempHP in temp_hp_2024.go). Before Nov 15 2026 about 55 statements in 26 files wrote
// characters.hp, each its own read-compute-write without a lock, and many without the rules that come with a change (audit AUD-06).
// The rule of a change of the maximum is maxHPChange, which a statement that claims a progression step includes.
func TestNoOtherHitPointWriter(t *testing.T) {
	update := regexp.MustCompile(`(?is)UPDATE\s+characters\b(.*?)\bWHERE\b`)
	assign := regexp.MustCompile(`(?i)(\bSET\s+|,\s*)(temp_)?hp\s*=`)
	built := regexp.MustCompile(`"(temp_)?hp = `) // a SET clause put together in Go ("hp = $%d")
	owners := map[string]bool{"hit_points_2024.go": true, "temp_hp_2024.go": true}
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || owners[f] {
			continue
		}
		b, _ := os.ReadFile(f)
		src := string(b)
		for _, m := range update.FindAllStringSubmatchIndex(src, -1) {
			if assign.MatchString(src[m[2]:m[3]]) {
				line := strings.Count(src[:m[0]], "\n") + 1
				t.Errorf("%s:%d writes a character's Hit Points itself: use the Hit Point owner (hit_points_2024.go)", f, line)
			}
		}
		for n, l := range strings.Split(src, "\n") {
			if built.MatchString(l) {
				t.Errorf("%s:%d builds a Hit Point SET clause: use the Hit Point owner (hit_points_2024.go)", f, n+1)
			}
		}
	}
}

// TestMaxHPChangeClause: the SQL of a change of the maximum keeps a dead character's Hit Points, never goes below 1 or above
// the new maximum, and ends dying for a character raised from 0 (its saves reset), reading the row as it was.
func TestMaxHPChangeClause(t *testing.T) {
	with := maxHPChange("$3", true)
	for _, want := range []string{"max_hp = GREATEST(1, max_hp + $3)", "CASE WHEN COALESCE(is_dead,false) THEN hp",
		"LEAST(GREATEST(1, max_hp + $3), hp + $3)", "death_save_failures = CASE WHEN", "is_stable = CASE WHEN"} {
		if !strings.Contains(with, want) {
			t.Errorf("maxHPChange(current) lacks %q: %s", want, with)
		}
	}
	if without := maxHPChange("$1", false); without != "max_hp = GREATEST(1, max_hp + $1), hp = LEAST(hp, GREATEST(1, max_hp + $1))" {
		t.Errorf("maxHPChange(cap only) = %s", without)
	}
}
