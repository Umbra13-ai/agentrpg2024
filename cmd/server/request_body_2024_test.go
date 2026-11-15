package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

type testInner struct {
	Item     string `json:"item"`
	Quantity int    `json:"quantity"`
}

type testEmbedded struct {
	Note string `json:"note"`
}

type testRequest struct {
	testEmbedded
	TargetID  int         `json:"target_id"`
	Name      string      `json:"name"`
	Buy       []testInner `json:"buy"`
	Hidden    string      `json:"-"`
	Optional  *bool       `json:"optional,omitempty"`
	Anything  interface{} `json:"anything"`
	Free      map[string]interface{}
	unexposed int
}

func parseFor(t *testing.T, body string, v interface{}, spec bodySpec) *bodyProblem {
	t.Helper()
	return parseRequest([]byte(body), v, spec)
}

func TestRequestBodyRule(t *testing.T) {
	cases := []struct {
		name, body, path string
		optional         bool
		code             string // "" = accepted
		inMessage        string
	}{
		{"fields", `{"target_id": 3, "name": "x", "note": "embedded", "buy": [{"item": "rope", "quantity": 2}]}`, "", false, "", ""},
		{"key fold", `{"Target-ID": 3}`, "", false, "", ""},
		{"same field twice", `{"target_id": 3, "Target ID": 4}`, "", false, "invalid_json", "same field"},
		{"unknown", `{"targt": 3, "bogus": 1}`, "", false, "unknown_field", `"targt" (did you mean "target_id"?)`},
		{"unknown lists fields", `{"bogus": 1}`, "", false, "unknown_field", "this request takes anything, buy, Free, name, note, optional, target_id"},
		{"camel case", `{"targetId": 3}`, "", false, "unknown_field", `did you mean "target_id"`},
		{"json dash not a field", `{"Hidden": "x"}`, "", false, "unknown_field", ""},
		{"empty refused", ``, "", false, "body_required", "Send a JSON object"},
		{"null refused", `null`, "", false, "body_required", ""},
		{"empty optional", `  `, "", true, "", ""},
		{"null optional", `null`, "", true, "", ""},
		{"list", `[1, 2]`, "", false, "invalid_json", "not a list"},
		{"string", `"hi"`, "", false, "invalid_json", "not a string"},
		{"malformed", `{"target_id": 3,}`, "", false, "invalid_json", "isn't valid JSON"},
		{"cut off", `{"target_id": 3`, "", false, "invalid_json", "ends too soon"},
		{"trailing", `{"target_id": 3} {"x": 1}`, "", false, "invalid_json", "more after the JSON object"},
		{"wrong type", `{"target_id": "3"}`, "", false, "wrong_type", "target_id has the wrong type: send a whole number (you sent text)"},
		{"wrong type nested", `{"buy": [{"item": "rope", "quantity": "two"}]}`, "", false, "wrong_type", "buy.quantity"},
		{"unknown nested", `{"buy": [{"item": "rope", "qty": 2}]}`, "", false, "unknown_field", `"qty" in an object inside the body (did you mean "quantity"?)`},
		{"path id repeated", `{"character_id": 7, "name": "x"}`, "/api/characters/7/setup", false, "", ""},
		{"path id differs", `{"character_id": 8}`, "/api/characters/7/setup", false, "id_mismatch", "not the one in the path (7)"},
		{"path id wrong type", `{"character_id": "7"}`, "/api/characters/7/setup", false, "wrong_type", ""},
		{"campaign path id", `{"campaign_id": 4}`, "/api/campaigns/4/combat/end", false, "", ""},
		{"no path id elsewhere", `{"character_id": 7}`, "/api/action", false, "unknown_field", ""},
		{"any value field", `{"anything": [1, {"a": 2}], "Free": {"x": 1}}`, "", false, "", ""},
	}
	for _, c := range cases {
		var req testRequest
		p := parseFor(t, c.body, &req, bodySpec{optional: c.optional, path: c.path})
		got := ""
		if p != nil {
			got = p.Code
		}
		if got != c.code {
			t.Errorf("%s: code %q, want %q (%v)", c.name, got, c.code, p)
			continue
		}
		if p != nil {
			if c.inMessage != "" && !strings.Contains(p.Message, c.inMessage) {
				t.Errorf("%s: message %q lacks %q", c.name, p.Message, c.inMessage)
			}
			if !strings.HasSuffix(p.Message, "Nothing was changed.") {
				t.Errorf("%s: message doesn't end with Nothing was changed: %q", c.name, p.Message)
			}
			if p.Status != 400 {
				t.Errorf("%s: status %d", c.name, p.Status)
			}
			if len(p.Fields) == 0 {
				t.Errorf("%s: no fields listed", c.name)
			}
		}
	}

	// values land in the struct, under their folded names too
	var req testRequest
	if p := parseFor(t, `{"Target-ID": 3, "note": "n", "buy": [{"item": "rope", "quantity": 2}]}`, &req, bodySpec{}); p != nil || req.TargetID != 3 || req.Note != "n" || len(req.Buy) != 1 || req.Buy[0].Quantity != 2 {
		t.Errorf("decode: %v %+v", p, req)
	}
}

func TestRequestBodyEnvelope(t *testing.T) {
	// an envelope field is decoded beside the request, and refused with the wrong type
	var req struct {
		Name string `json:"name"`
	}
	var cid int
	spec := bodySpec{extra: map[string]interface{}{"campaign_id": &cid}}
	if p := parseRequest([]byte(`{"name": "x", "Campaign-ID": 12}`), &req, spec); p != nil || cid != 12 || req.Name != "x" {
		t.Errorf("envelope: %v %d %+v", p, cid, req)
	}
	if p := parseRequest([]byte(`{"campaign_id": "12"}`), &req, spec); p == nil || p.Code != "wrong_type" {
		t.Errorf("envelope wrong type: %v", p)
	}
	// a request that declares the field gets it too
	var both struct {
		CampaignID int `json:"campaign_id"`
	}
	cid = 0
	if p := parseRequest([]byte(`{"campaign_id": 5}`), &both, bodySpec{extra: map[string]interface{}{"campaign_id": &cid}}); p != nil || cid != 5 || both.CampaignID != 5 {
		t.Errorf("declared envelope: %v %d %+v", p, cid, both)
	}
	// the envelope is listed among the fields
	if p := parseRequest([]byte(`{"x": 1}`), &req, spec); p == nil || !strings.Contains(p.Message, "campaign_id") {
		t.Errorf("envelope listed: %v", p)
	}
}

func TestRequestBodyMaps(t *testing.T) {
	m := map[string]interface{}{}
	if p := parseRequest([]byte(`{"a": 1, "B": 2}`), &m, bodySpec{fields: []string{"a", "b"}}); p != nil || m["a"] == nil || m["b"] == nil {
		t.Errorf("map fields: %v %v", p, m)
	}
	m = map[string]interface{}{}
	if p := parseRequest([]byte(`{"a": 1, "c": 2}`), &m, bodySpec{fields: []string{"a", "b"}}); p == nil || p.Code != "unknown_field" {
		t.Errorf("map unknown: %v", p)
	}
	m = map[string]interface{}{}
	if p := parseRequest([]byte(`{"a": 1, "c": 2}`), &m, bodySpec{anyKey: true}); p != nil || len(m) != 2 {
		t.Errorf("map checked by handler: %v %v", p, m)
	}
	raw := map[string]json.RawMessage{}
	if p := parseRequest([]byte(`{"steps": [{"action": "dodge"}]}`), &raw, bodySpec{fields: []string{"steps"}}); p != nil || len(raw["steps"]) == 0 {
		t.Errorf("raw map: %v %v", p, raw)
	}
}

func TestReadRequestKeepsBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/action", strings.NewReader(`{"name": "x"}`))
	w := httptest.NewRecorder()
	var req struct {
		Name string `json:"name"`
	}
	if !readRequest(w, r, &req) || req.Name != "x" {
		t.Fatalf("read: %d %s", w.Code, w.Body.String())
	}
	if string(peekBody(r)) != `{"name": "x"}` {
		t.Errorf("peek after read: %q", peekBody(r))
	}
	var again struct {
		Name string `json:"name"`
	}
	if !readRequest(httptest.NewRecorder(), r, &again) || again.Name != "x" {
		t.Errorf("second read failed")
	}
	// too large
	big := httptest.NewRequest("POST", "/api/action", strings.NewReader(`{"name": "`+strings.Repeat("x", maxRequestBody+10)+`"}`))
	w = httptest.NewRecorder()
	if readRequest(w, big, &req) || w.Code != 413 {
		t.Errorf("too large: %d", w.Code)
	}
	// no body at all, optional
	nob := httptest.NewRequest("POST", "/api/action", nil)
	if !readRequest(httptest.NewRecorder(), nob, &req, optionalBody()) {
		t.Errorf("no body, optional: refused")
	}
	w = httptest.NewRecorder()
	if readRequest(w, nob, &req) || w.Code != 400 {
		t.Errorf("no body, required: %d", w.Code)
	}
}
