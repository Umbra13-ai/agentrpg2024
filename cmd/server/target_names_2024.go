package main

// Targets by name (F249, Nov 11): the action's "target" field ("Kobold 4", as my-turn's situation.enemies shows it, or "-4", or a
// character's name) resolves to an id; a damage spell cast in combat with no target is refused (nothing spent) instead of printing
// damage it never applied.

import (
	"fmt"
	"strconv"
	"strings"
)

// resolveTargetName: the id of the combatant or character called t (0 if none), and the names there are.
func resolveTargetName(lobbyID, charID int, t string) (int, []string) {
	want := strings.ToLower(strings.TrimSpace(t))
	if n, err := strconv.Atoi(want); err == nil && n != 0 {
		return n, nil
	}
	var names []string
	var partial []int
	for _, e := range combatEntries(lobbyID) {
		nm := fmt.Sprint(e["name"])
		names = append(names, nm)
		if want == "" {
			continue
		}
		if strings.ToLower(nm) == want {
			return asInt(e["id"]), names
		}
		if strings.Contains(strings.ToLower(nm), want) {
			partial = append(partial, asInt(e["id"]))
		}
	}
	if want == "" {
		return 0, names
	}
	if len(partial) == 1 {
		return partial[0], names
	}
	var id int
	db.QueryRow(`SELECT id FROM characters WHERE lobby_id = $1 AND LOWER(name) = $2`, lobbyID, want).Scan(&id)
	if id == 0 { // a character's first name or another word of it, when only one character has it ("Aldren")
		rows, err := db.Query(`SELECT id FROM characters WHERE lobby_id = $1 AND (LOWER(name) LIKE $2||' %' OR LOWER(name) LIKE '% '||$2 OR LOWER(name) LIKE '% '||$2||' %')`, lobbyID, want)
		if err == nil {
			var ids []int
			for rows.Next() {
				var x int
				rows.Scan(&x)
				ids = append(ids, x)
			}
			rows.Close()
			if len(ids) == 1 {
				id = ids[0]
			}
		}
	}
	return id, names
}

// descNamesCombatant: the description names a creature of the fight (the old way to aim a spell).
func descNamesCombatant(lobbyID int, desc string) bool {
	d := strings.ToLower(desc)
	for _, e := range combatEntries(lobbyID) {
		if nm := strings.ToLower(fmt.Sprint(e["name"])); nm != "" && strings.Contains(d, nm) {
			return true
		}
	}
	return false
}

// needsCreatureTarget: a damage spell aimed at one creature (an attack or a save), not an area, self or a weapon it creates.
func needsCreatureTarget(sp SRDSpell) bool {
	if sp.DamageDice == "" || sp.Healing != "" || sp.AoEShape != "" || strings.HasPrefix(strings.ToLower(sp.Range), "self") {
		return false
	}
	switch normalizeSpellSlug(sp.Name) {
	case "spiritual-weapon", "flame-blade", "shillelagh", "magic-stone", "flaming-sphere", "cloud-of-daggers", "moonbeam", "spike-growth", "heat-metal":
		return false
	}
	d := strings.ToLower(sp.Description)
	return sp.SavingThrow != "" || strings.Contains(d, "spell attack")
}

// combatantNamedIn: the monster the description names (the longest name that appears, so "Kobold 10" isn't "Kobold 1"); 0 if none.
func combatantNamedIn(lobbyID int, desc string) int {
	d := strings.ToLower(desc)
	best, bestLen := 0, 0
	for _, e := range combatEntries(lobbyID) {
		if m, _ := e["is_monster"].(bool); !m {
			continue
		}
		nm := strings.ToLower(fmt.Sprint(e["name"]))
		if nm != "" && len(nm) > bestLen && wordIn(d, nm) {
			best, bestLen = asInt(e["id"]), len(nm)
		}
	}
	return best
}

// wordIn: s appears in d as whole words.
func wordIn(d, s string) bool {
	for from := 0; ; {
		i := strings.Index(d[from:], s)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(s)
		okL := i == 0 || !isAlnum(d[i-1])
		okR := end >= len(d) || !isAlnum(d[end])
		if okL && okR {
			return true
		}
		from = i + 1
	}
}

func isAlnum(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' }
