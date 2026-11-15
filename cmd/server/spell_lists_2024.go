package main

// The spell lists the API gives, and the route a message names for one: one owner.
//
// Three routes list spells: /api/universe/spells (every spell), /api/universe/spells/search (by name, school ...) and
// /api/universe/class-spells/{class}. All three read class and level the same way (spellListFilter), so a message can name
// any of them with a class and a level and get that list. A message that tells a player where a class's spells are uses
// spellListRoute, so it always names a route that answers with that list.

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// spellListFilter reads the class and level filters of a spell list request: class "" for any, level -1 for any. A level
// outside 0-9 or a class with no spell list is refused, naming what is accepted.
func spellListFilter(r *http.Request) (class string, level int, problem map[string]interface{}) {
	level = -1
	q := r.URL.Query()
	if v := strings.TrimSpace(q.Get("class")); v != "" {
		class = strings.ToLower(strings.ReplaceAll(v, " ", "-"))
		if len(getClassSpellList(class)) == 0 {
			return "", -1, map[string]interface{}{"error": "invalid_query", "fields": []string{"class"},
				"message": fmt.Sprintf("%q has no spell list: bard, cleric, druid, paladin, ranger, sorcerer, warlock or wizard.", v)}
		}
	}
	if v := strings.TrimSpace(q.Get("level")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 9 {
			return "", -1, map[string]interface{}{"error": "invalid_query", "fields": []string{"level"},
				"message": "level is a spell level, 0 (cantrips) to 9."}
		}
		level = n
	}
	return class, level, nil
}

// spellFilterSQL adds the class and level filters to a query on the spells table (aliased s), starting at placeholder n.
func spellFilterSQL(class string, level, n int) (string, []interface{}) {
	where, args := "", []interface{}{}
	if class != "" {
		where += fmt.Sprintf(" AND s.slug IN (SELECT spell_slug FROM class_spells WHERE class_slug = $%d)", n)
		args = append(args, class)
		n++
	}
	if level >= 0 {
		where += fmt.Sprintf(" AND s.level = $%d", n)
		args = append(args, level)
	}
	return where, args
}

// spellListRoute: the route that lists a class's spells, of one level (level -1: every level).
func spellListRoute(class string, level int) string {
	route := "GET /api/universe/class-spells/" + strings.ToLower(class)
	if level >= 0 {
		route += "?level=" + strconv.Itoa(level)
	}
	return route
}
