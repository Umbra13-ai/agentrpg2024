package main

// Unfinished characters (F232, Nov 10, the project owner: "does the server not alert players during character creation of what they need to set?
// does it not tell them their characters are not finished?"). characterToFinish (choices_2024.go) lists what a character still has to
// choose, from the one table of choices. The list shows in the creation reply (to_finish), on GET /api/characters/{id} (to_finish,
// character_finished), in my-turn (level_up_choices) and to the GM (gm/status gm_tasks and unfinished_characters).

import (
	"context"
	"encoding/json"
	"net/http"
)

type unfinishedKey struct{}

func jsonCount(charID int, column string) int {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(`+column+`, '[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var list []interface{}
	json.Unmarshal(raw, &list)
	return len(list)
}

// withToFinish adds to_finish and character_finished to GET /api/characters/{id}.
func withToFinish(w http.ResponseWriter, r *http.Request, charID int, h func(http.ResponseWriter, *http.Request)) bool {
	if r.Method != http.MethodGet || r.Context().Value(unfinishedKey{}) != nil {
		return false
	}
	r2 := r.WithContext(context.WithValue(r.Context(), unfinishedKey{}, true))
	augmentJSON(w, func(rw http.ResponseWriter) { h(rw, r2) }, func(m map[string]interface{}) {
		todo := characterToFinish(charID)
		m["character_finished"] = len(todo) == 0
		// prepared_count counts level 1+ spells as /prepare does, not cantrips or always-prepared spells (F15: a Wizard with 3
		// cantrips and 4 spells showed 7 of 4 and slots_remaining -3). The sheet builds these fields in character_handlers.go.
		if max, ok := m["max_prepared"].(float64); ok {
			class, _, _ := subclassOf(charID)
			list := jsonListOf(charID, "prepared_spells")
			if len(classLevelsOf(charID)) <= 1 {
				list = currentPreparedList(charID, class)
			}
			n := countLeveled(withoutAlwaysPrepared(list, allAlwaysPrepared(charID)))
			m["prepared_count"], m["slots_remaining"] = n, int(max)-n
		}
		if len(todo) > 0 {
			m["to_finish"] = todo
		}
	})
	return true
}

// unfinishedInCampaign: "Name: n choice(s) left" for each character of the campaign that isn't finished (for the GM).
func unfinishedInCampaign(campaignID int) map[string][]string {
	rows, err := db.Query(`SELECT id, name FROM characters WHERE lobby_id = $1`, campaignID)
	if err != nil {
		return nil
	}
	type ch struct {
		id   int
		name string
	}
	var all []ch
	for rows.Next() {
		var c ch
		if rows.Scan(&c.id, &c.name) == nil {
			all = append(all, c)
		}
	}
	rows.Close()
	out := map[string][]string{}
	for _, c := range all {
		if t := characterToFinish(c.id); len(t) > 0 {
			out[c.name] = t
		}
	}
	return out
}
