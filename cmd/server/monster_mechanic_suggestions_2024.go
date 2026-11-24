package main

// Which attached mechanic might cover a sentence of an imported entry that no rule of the server reads: the import report's
// suggest_mechanics (entryReport). A suggestion only, from the kind's own Hints in mechanicCatalog and the entries it attaches to
// (mechanicFits): the GM reads the kind (GET /api/gm/compendium/mechanics) and decides whether to attach it.

import "strings"

// mechanicSuggestions: the kinds whose Hints the sentence holds and that attach to an entry of this group, in the catalogue's order.
func mechanicSuggestions(group string, e srdMonsterEntry, sentence string) []map[string]interface{} {
	low := strings.ToLower(sentence)
	var out []map[string]interface{}
	for _, k := range mechanicCatalog {
		if fit, ok := mechanicFits[k.On]; !ok || fit.Check(group, e) != "" {
			continue
		}
		for _, h := range k.Hints {
			if strings.Contains(low, h) {
				out = append(out, map[string]interface{}{"kind": k.Kind, "does": k.Does})
				break
			}
		}
	}
	return out
}
