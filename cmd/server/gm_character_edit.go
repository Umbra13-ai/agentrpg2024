package main

// The GM can change any of a character's data in their campaign. POST /api/gm/update-character {"character_id": 5, "set": {"column": value, ...}} writes any
// column of the characters table except the identity columns; GET /api/gm/update-character?character_id=5 shows every field.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// columns the GM can't change: who owns the character, which campaign it is in, its id and timestamps
var protectedCharacterColumns = map[string]bool{"id": true, "agent_id": true, "lobby_id": true, "created_at": true}

type characterColumn struct {
	Name, Type string
}

// characterColumns lists the characters table's columns and their types (binary columns such as a portrait are left out).
func characterColumns() map[string]characterColumn {
	out := map[string]characterColumn{}
	rows, err := db.Query(`SELECT column_name, data_type FROM information_schema.columns WHERE table_name = 'characters' AND table_schema = current_schema()`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var n, t string
		if rows.Scan(&n, &t) == nil && t != "bytea" {
			out[n] = characterColumn{n, t}
		}
	}
	return out
}

// gmSetCharacterFields writes the "set" object: each key a column, each value JSON (arrays and objects for JSONB columns). It returns
// the columns changed, or an error naming the first bad one. Nothing is written if any key is bad.
func gmSetCharacterFields(charID int, set map[string]json.RawMessage) ([]string, error) {
	cols := characterColumns()
	var keys []string
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	var args []interface{}
	for _, k := range keys {
		key := strings.ToLower(strings.TrimSpace(k))
		if key == "int" {
			key = "intl" // the Intelligence column is intl
		}
		if key == "race" {
			return nil, fmt.Errorf("the column is species, not race")
		}
		c, ok := cols[key]
		if !ok || protectedCharacterColumns[key] {
			return nil, fmt.Errorf("%q is not a character field the GM can change (GET this endpoint with character_id to see the fields)", k)
		}
		raw := set[k]
		args = append(args, nil)
		n := len(args)
		switch {
		case string(raw) == "null":
			parts = append(parts, fmt.Sprintf("%s = NULL", key))
			args = args[:n-1]
			continue
		case c.Type == "jsonb" || c.Type == "json":
			args[n-1] = string(raw)
			parts = append(parts, fmt.Sprintf("%s = $%d::jsonb", key, n))
		default:
			var v interface{}
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, fmt.Errorf("%s: %v", k, err)
			}
			if _, isBool := v.(bool); isBool && c.Type != "boolean" {
				return nil, fmt.Errorf("%s is %s, not true/false", k, c.Type)
			}
			if f, isNum := v.(float64); isNum && (c.Type == "integer" || c.Type == "smallint" || c.Type == "bigint") {
				v = int(f)
			}
			if _, isMap := v.(map[string]interface{}); isMap {
				return nil, fmt.Errorf("%s takes a single value", k)
			}
			if _, isList := v.([]interface{}); isList {
				return nil, fmt.Errorf("%s takes a single value", k)
			}
			args[n-1] = v
			parts = append(parts, fmt.Sprintf("%s = $%d", key, n))
		}
	}
	if len(parts) == 0 {
		return nil, nil
	}
	args = append(args, charID)
	if _, err := db.Exec(fmt.Sprintf("UPDATE characters SET %s WHERE id = $%d", strings.Join(parts, ", "), len(args)), args...); err != nil {
		return nil, fmt.Errorf("the update failed: %v", err)
	}
	for _, k := range keys {
		if strings.EqualFold(k, "conditions") {
			syncRecordsFromList(charID) // the conditions list and the condition records stay in step
		}
	}
	return keys, nil
}

// gmCharacterRecord is every field of the character (binary columns left out), for GET /api/gm/update-character.
func gmCharacterRecord(charID int) map[string]interface{} {
	cols := characterColumns()
	var names []string
	for n := range cols {
		names = append(names, n)
	}
	sort.Strings(names)
	var raw []byte
	if db.QueryRow(fmt.Sprintf(`SELECT %s FROM characters WHERE id = $1`, jsonPairs(names)), charID).Scan(&raw) != nil {
		return nil
	}
	var out map[string]interface{}
	json.Unmarshal(raw, &out)
	return out
}

// jsonPairs builds one JSON object of the named columns (jsonb_build_object takes at most 100 arguments, so groups are merged).
func jsonPairs(names []string) string {
	var groups []string
	for i := 0; i < len(names); i += 50 {
		end := i + 50
		if end > len(names) {
			end = len(names)
		}
		var p []string
		for _, n := range names[i:end] {
			p = append(p, fmt.Sprintf("'%s', %s", n, n))
		}
		groups = append(groups, "jsonb_build_object("+strings.Join(p, ", ")+")")
	}
	return "(" + strings.Join(groups, " || ") + ")"
}

// addInventoryItems puts named items into a character's inventory (worn), one entry each.
func addInventoryItems(charID int, items []string) {
	if len(items) == 0 {
		return
	}
	var raw []byte
	db.QueryRow(`SELECT COALESCE(inventory, '[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var inv []map[string]interface{}
	json.Unmarshal(raw, &inv)
	for _, it := range items {
		b := make([]byte, 4)
		rand.Read(b)
		inv = append(inv, map[string]interface{}{"id": "gm" + hex.EncodeToString(b), "name": it, "quantity": 1, "location": "worn"})
	}
	out, _ := json.Marshal(inv)
	db.Exec(`UPDATE characters SET inventory = $1 WHERE id = $2`, out, charID)
}
