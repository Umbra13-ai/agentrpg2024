package game

import (
	"strconv"
	"strings"
)

// ClassTableValue is the printed value of a column of a class's Features table at a level ("Cantrips", "Prepared Spells",
// "Rages", "Channel Divinity", ...). ok is false when the class or column does not exist.
func ClassTableValue(class string, level int, column string) (string, bool) {
	rows, ok := classTables2024[strings.ToLower(class)]
	if !ok {
		return "", false
	}
	v, ok := rows[level][column]
	return v, ok
}

// ClassTableInt is ClassTableValue as a number: "—" and anything that is not a number count as 0.
func ClassTableInt(class string, level int, column string) int {
	v, ok := ClassTableValue(class, level, column)
	if !ok {
		return 0
	}
	s := strings.TrimPrefix(strings.TrimSpace(v), "+")
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(s[:end]) // "+10 ft." is 10 (F105: Unarmored Movement read as 0)
	return n
}

// slotsFromTable reads the spell slot columns (1-9) of a class's table; ok is false for a class without them.
func slotsFromTable(class string, level int) (map[int]int, bool) {
	rows, ok := classTables2024[strings.ToLower(class)]
	if !ok {
		return nil, false
	}
	row, ok := rows[level]
	if !ok {
		return nil, false
	}
	slots := map[int]int{}
	found := false
	for l := 1; l <= 9; l++ {
		if v, has := row[strconv.Itoa(l)]; has {
			found = true
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				slots[l] = n
			}
		}
	}
	return slots, found
}

// PreparedSpells2024 is the number of prepared spells in the class's Features table (0 when it has no such column).
func PreparedSpells2024(class string, level int) int {
	return ClassTableInt(class, level, "Prepared Spells")
}
