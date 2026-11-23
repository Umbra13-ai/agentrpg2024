package main

import (
	"encoding/json"
	"net/http"
)

// guide_short_view_2024.go: the short view of the character builder's and the setup guide's replies (owner, Nov 23: agents pay per
// request and per byte, and every reply repeated the whole text of every background, species and spell). One owner of which view a reply
// takes (guideView) and of what the short view keeps (shortenGuide). The first reply is full (a draft's start; a character's first setup
// read, setup_full_seen), later ones short unless the request says ?view=full; ?view=short asks for it any time. The short view keeps
// every id, name, count, request, suggestion and follow-up; it leaves out the long descriptions (an option's text, option_details, a
// step's explanation) and, in setup, the details of the steps that aren't next (each open step keeps its request).

// guideView: "full" or "short" for this request. first: this is the reader's first reply of the guide.
func guideView(r *http.Request, first bool) string {
	switch r.URL.Query().Get("view") {
	case "full":
		return "full"
	case "short":
		return "short"
	}
	if first {
		return "full"
	}
	return "short"
}

// guideLongKeys: the descriptions the short view leaves out wherever they are.
var guideLongKeys = map[string]bool{"option_details": true, "explain": true, "choices_remaining_note": true, "what_it_does": true}

// guideKeptKeys: what an option keeps even when it is long (what to send, and what a pick brings with it).
var guideKeptKeys = map[string]bool{"follow_up": true, "uses_up_class_skills": true, "request": true, "body": true, "field": true}

// shortenGuide changes a builder or setup reply in place into the short view.
func shortenGuide(v map[string]interface{}) {
	nextKey := ""
	if n, ok := v["next"].(map[string]interface{}); ok {
		nextKey, _ = n["key"].(string)
	} else if n, ok := v["next"].(*setupStep); ok && n != nil {
		nextKey = n.Key
	}
	if steps, ok := v["steps"].([]setupStep); ok { // setup: a step that isn't next keeps everything but its explanation and its options
		short := []interface{}{}
		for _, st := range steps {
			var e map[string]interface{}
			if b, err := json.Marshal(st); err != nil || json.Unmarshal(b, &e) != nil {
				continue
			}
			if st.Key == nextKey {
				e = map[string]interface{}{"key": st.Key, "title": st.Title, "done": st.Done, "details": "in next", "request": st.Request}
			} else {
				delete(e, "why")
				delete(e, "choices")
			}
			short = append(short, e)
		}
		v["steps"] = short
	}
	shortenValue(v)
	v["view"] = "short"
	v["full_view"] = "Add ?view=full for every option's whole text (each background, species, spell and step described)."
}

// shortenValue walks a reply: the long keys go, and each option of an "options" list keeps its short fields.
func shortenValue(x interface{}) interface{} {
	switch t := x.(type) {
	case map[string]interface{}:
		for k, val := range t {
			if guideLongKeys[k] {
				delete(t, k)
				continue
			}
			if k == "options" {
				if list, ok := val.([]map[string]interface{}); ok {
					for _, o := range list {
						shortenOption(o)
					}
					continue
				}
				if list, ok := val.([]interface{}); ok {
					for _, o := range list {
						if m, ok := o.(map[string]interface{}); ok {
							shortenOption(m)
						}
					}
					continue
				}
			}
			t[k] = shortenValue(val)
		}
		return t
	case []interface{}:
		for i := range t {
			t[i] = shortenValue(t[i])
		}
		return t
	case []map[string]interface{}:
		for i := range t {
			shortenValue(t[i])
		}
		return t
	case *setupStep, setupStep, *builderStep, builderStep, []builderStep, []setupStep:
		// a typed step: through JSON, so its fields are shortened like any map's
		var m interface{}
		if b, err := json.Marshal(t); err == nil && json.Unmarshal(b, &m) == nil {
			return shortenValue(m)
		}
	}
	return x
}

// shortenOption keeps an option's short fields: a text over 140 characters and a long nested value go, unless guideKeptKeys names it.
func shortenOption(o map[string]interface{}) {
	for k, val := range o {
		if guideKeptKeys[k] {
			continue
		}
		if guideLongKeys[k] {
			delete(o, k)
			continue
		}
		switch t := val.(type) {
		case string:
			if len(t) > 140 {
				delete(o, k)
			}
		case map[string]interface{}, []interface{}:
			if b, _ := json.Marshal(t); len(b) > 300 {
				delete(o, k)
			}
		}
	}
}

// answerGuideView gives a builder or setup reply its view, in place: short (shortenGuide) or full, which says that later replies are short.
func answerGuideView(r *http.Request, v map[string]interface{}, first bool) {
	if guideView(r, first) == "short" {
		shortenGuide(v)
		return
	}
	v["view"] = "full"
	v["later_replies"] = "This guide's later replies are short: the long descriptions are left out. Add ?view=full to any request for the whole text."
}

// setupFirstRead: the character's setup guide has not been read in full yet; it marks it read (the first read is the full one).
func setupFirstRead(charID int) bool {
	res, err := db.Exec(`UPDATE characters SET setup_full_seen = true WHERE id = $1 AND NOT COALESCE(setup_full_seen, false)`, charID)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}
