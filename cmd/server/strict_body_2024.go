package main

// One rule for every level-up and setup request (ASI, feat, subclass, subclass choices, Metamagic, Mystic Arcanum, Signature
// Spells, Spell Mastery, level-up ...): a field the request doesn't take is refused with the fields it does take, and nothing is
// changed. Before, these requests decoded leniently and silently ignored a misspelled field (the setup checker, Nov 14).

import (
	"net/http"
	"reflect"
	"sort"
	"strings"
)

// requestFields lists the JSON field names of a request struct (v is a pointer to it).
func requestFields(v interface{}) []string {
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	for i := 0; i < t.NumField(); i++ {
		tag := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if tag != "" && tag != "-" {
			out = append(out, tag)
		}
	}
	sort.Strings(out)
	return out
}

// decodeRequest reads the body into v with decodeStrict. On a problem it answers 400 and returns false. allowEmpty lets an empty
// body through (a request whose fields are all optional, such as level-up).
func decodeRequest(w http.ResponseWriter, r *http.Request, v interface{}, allowEmpty bool) bool {
	fields := requestFields(v)
	desc := "only these fields: " + strings.Join(fields, ", ")
	msg := decodeStrict(r, v, desc)
	if msg == "" {
		return true
	}
	if allowEmpty && strings.HasPrefix(msg, "Send a JSON body") {
		return true
	}
	if strings.HasPrefix(msg, "Unknown field ") {
		bad := strings.Trim(strings.TrimSuffix(strings.SplitN(strings.TrimPrefix(msg, "Unknown field "), ":", 2)[0], ""), `"`)
		if c := closestOf(bad, func(yield func(string)) {
			for _, f := range fields {
				yield(f)
			}
		}); c != "" {
			msg = strings.TrimSuffix(msg, ".") + ` (did you mean "` + c + `"?).`
		}
		msg += " Nothing was changed."
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "unknown_field", "message": msg, "fields": fields})
		return false
	}
	writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json", "message": msg + " Nothing was changed.", "fields": fields})
	return false
}

// nameKey folds a displayed name and a slug to one key: "Hunter's Prey", "hunters_prey" and "hunters-prey" all give "hunters_prey".
// Choices are matched with it, so the name the server shows is accepted as well as the slug (setup checker, Nov 14: the displayed
// Metamagic, Hunter's Prey and Mystic Arcanum names were refused).
func nameKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("'", "", "’", "", "(", "", ")", "").Replace(s)
	s = strings.NewReplacer("-", " ", "_", " ").Replace(s)
	return strings.Join(strings.Fields(s), "_")
}
