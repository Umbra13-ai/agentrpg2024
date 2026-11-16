package main

// resourceLeft: how many uses of a class resource a character has left, and its maximum (my-turn's options: action_types_2024.go).

import "encoding/json"

func resourceLeft(charID int, class string, level int, key string) (left, max int) {
	var cha int
	var raw []byte
	db.QueryRow(`SELECT cha, COALESCE(class_resources_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&cha, &raw)
	used := map[string]int{}
	json.Unmarshal(raw, &used)
	max = classResourceMax(charID, class, level, key, (cha-10)/2)
	return max - used[key], max
}
