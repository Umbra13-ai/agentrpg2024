package main

// The level-up choices that had no place on the character record (setup checker, Nov 14): subclass choices (Hunter's Prey,
// Defensive Tactics, Circle of the Land, Bonus Proficiencies, Primal Knowledge, Blessed Strikes ...), Metamagic, Wild Shape forms
// and Magical Discoveries. GET /api/characters/{id} lists them as class_choices, and the public sheet shows them.

import (
	"sort"
	"strings"
)

// choiceFeatureNames: the feature name for a subclass_choices key that titleWords would spell wrongly.
var choiceFeatureNames = map[string]string{
	"hunters_prey": "Hunter's Prey", "circle_land": "Circle of the Land", "fighting_style_cantrips": "Fighting Style cantrips",
}

func choiceLabel(s string) string {
	return titleWords(strings.NewReplacer("_", " ", "-", " ").Replace(s))
}

// classChoicesRecord lists each choice as {"feature", "choice"}, in a stable order.
func classChoicesRecord(charID int) []map[string]interface{} {
	var out []map[string]interface{}
	sc := subclassChoicesOf(charID)
	var keys []string
	for k, v := range sc {
		if strings.TrimSpace(v) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		name := choiceFeatureNames[k]
		for _, d := range classLangDefs { // the class feature's own name ("Thieves’ Cant languages", "Deft Explorer languages")
			if d.key == k {
				name = d.feature + " languages"
			}
		}
		if name == "" {
			name = choiceLabel(k)
		}
		var vals []string
		for _, v := range strings.Split(sc[k], ",") {
			if v = strings.TrimSpace(v); v != "" {
				vals = append(vals, choiceLabel(v))
			}
		}
		out = append(out, map[string]interface{}{"feature": name, "choice": strings.Join(vals, ", "), "key": k})
	}
	if mm := getMetamagicChoices(charID); len(mm) > 0 {
		var names []string
		for _, s := range mm {
			if o, ok := metamagicOptions[s]; ok {
				names = append(names, o.Name)
			} else {
				names = append(names, choiceLabel(s))
			}
		}
		out = append(out, map[string]interface{}{"feature": "Metamagic", "choice": strings.Join(names, ", "), "key": "metamagic"})
	}
	if ws := knownWildShapes(charID); len(ws) > 0 {
		var names []string
		for _, s := range ws {
			names = append(names, choiceLabel(s))
		}
		out = append(out, map[string]interface{}{"feature": "Wild Shape forms", "choice": strings.Join(names, ", "), "key": "wild_shape_forms"})
	}
	if md := magicalDiscoveriesOf(charID); len(md) > 0 {
		var names []string
		for _, s := range md {
			names = append(names, spellName(s))
		}
		out = append(out, map[string]interface{}{"feature": "Magical Discoveries", "choice": strings.Join(names, ", "), "key": "magical_discoveries"})
	}
	return out
}
