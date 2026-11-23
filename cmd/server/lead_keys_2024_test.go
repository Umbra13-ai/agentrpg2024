package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMarshalLeadingPutsTheNamedKeysFirst(t *testing.T) {
	m := map[string]interface{}{"zeta": 1, "alpha": []int{1, 2}, "now": []string{"a<b"}, "is_my_turn": true, "nested": map[string]interface{}{"b": 1, "a": 2}}
	b, err := marshalLeading(m, "is_my_turn", "now", "absent")
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.HasPrefix(got, `{"is_my_turn":true,"now":["a<b"],"alpha":[1,2],"nested":{"a":2,"b":1},"zeta":1}`) {
		t.Errorf("order or content: %s", got)
	}
	// the same content as json.Marshal
	var a, c map[string]interface{}
	plain, _ := json.Marshal(m)
	json.Unmarshal(b, &a)
	json.Unmarshal(plain, &c)
	ab, _ := json.Marshal(a)
	cb, _ := json.Marshal(c)
	if string(ab) != string(cb) {
		t.Errorf("content differs from json.Marshal:\n%s\n%s", ab, cb)
	}
	if b2, _ := marshalLeading(map[string]interface{}{}); string(b2) != "{}" {
		t.Errorf("empty: %s", b2)
	}
}
