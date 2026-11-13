package game

import "strings"

// ClassTableRow is one level's row of a class's features table (column name to value), or nil.
func ClassTableRow(class string, level int) map[string]string {
	rows, ok := classTables2024[strings.ToLower(class)]
	if !ok {
		return nil
	}
	return rows[level]
}
