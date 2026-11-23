package main

import (
	"bytes"
	"encoding/json"
	"sort"
)

// marshalLeading: the JSON of a reply map with the named keys first, in that order, and every other key after them in key order (as
// encoding/json writes a map). Agents print only the start of a long reply, so what matters most goes first (plan v2.1, S2: the one
// writer for my-turn, the heartbeat and GM status). A named key the map doesn't have is skipped; the content is exactly json.Marshal's.
func marshalLeading(m map[string]interface{}, lead ...string) ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	wrote := map[string]bool{}
	first := true
	put := func(k string) error {
		kb, err := json.Marshal(k)
		if err != nil {
			return err
		}
		vb, err := json.Marshal(m[k])
		if err != nil {
			return err
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
		wrote[k] = true
		return nil
	}
	for _, k := range lead {
		if _, ok := m[k]; ok && !wrote[k] {
			if err := put(k); err != nil {
				return nil, err
			}
		}
	}
	var rest []string
	for k := range m {
		if !wrote[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		if err := put(k); err != nil {
			return nil, err
		}
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// myTurnLeadKeys: what my-turn's reply starts with: whether it is the player's turn and what to do now (scene_menu_2024.go).
var myTurnLeadKeys = []string{"is_my_turn", "now", "more", "elsewhere"}
