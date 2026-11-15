package main

// Repeatable Eldritch Invocations (game.InvocationRepeats): a Warlock may gain Agonizing Blast, Eldritch Spear, Repelling Blast
// and Lessons of the First Ones more than once, each copy with a different choice. A cantrip invocation's copies each name a
// cantrip (stored in invocation_cantrips, one per copy in order); Lessons' copies each give an Origin feat (the lessons_feat
// choice, choices_2024.go, which refuses a feat already taken). The invocation request, its options and the effects read these.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// invocationCopies: how many copies of the invocation the character has.
func invocationCopies(charID int, slug string) int {
	n := 0
	for _, s := range getCharacterInvocations(charID) {
		if s == slug {
			n++
		}
	}
	return n
}

func storedInvocationCantrips(charID int) map[string][]string {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(invocation_cantrips, '{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	m := map[string][]string{}
	json.Unmarshal(raw, &m)
	return m
}

func saveInvocationCantrips(charID int, m map[string][]string) {
	b, _ := json.Marshal(m)
	db.Exec(`UPDATE characters SET invocation_cantrips = $1 WHERE id = $2`, b, charID)
}

// eligibleInvocationCantrips: the cantrips a copy of this invocation may name: Warlock cantrips the character has that meet
// the invocation's need (damage, an attack roll, a range), Eldritch Blast first.
func eligibleInvocationCantrips(charID int, slug string) []string {
	rep, ok := game.InvocationRepeats[slug]
	if !ok || rep.Choice != "cantrip" {
		return nil
	}
	var out []string
	for s := range spellsHeld(charID) {
		sp, ok := srdSpellsMemory[s]
		if !ok || sp.Level != 0 || !onClassList(s, "warlock") {
			continue
		}
		if rep.Damage && sp.DamageDice == "" && len(sp.DamageParts) == 0 {
			continue
		}
		if rep.AttackRoll && !isSpellAttack(sp) {
			continue
		}
		if rep.MinRange > 0 && spellRangeFeet(sp.Range) < rep.MinRange && spellRangeFeet(sp.Range) != -1 {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i] == "eldritch-blast") != (out[j] == "eldritch-blast") {
			return out[i] == "eldritch-blast"
		}
		return out[i] < out[j]
	})
	return out
}

// invocationCantrips: the cantrip of each copy the character has. A copy taken before copies named their cantrip (or at
// creation, which names none) is given the first eligible cantrip no other copy has, Eldritch Blast first.
func invocationCantrips(charID int, slug string) []string {
	if rep, ok := game.InvocationRepeats[slug]; !ok || rep.Choice != "cantrip" {
		return nil
	}
	copies := invocationCopies(charID, slug)
	got := storedInvocationCantrips(charID)[slug]
	if len(got) > copies {
		got = got[:copies]
	}
	for len(got) < copies {
		pick := ""
		for _, c := range eligibleInvocationCantrips(charID, slug) {
			if !containsStr(got, c) {
				pick = c
				break
			}
		}
		if pick == "" {
			break
		}
		got = append(got, pick)
	}
	return got
}

// invocationOn: the character has this invocation on that cantrip (Agonizing Blast on Eldritch Blast, on Toll the Dead ...).
func invocationOn(charID int, slug, spellKey string) bool {
	return containsStr(invocationCantrips(charID, slug), normalizeSpellSlug(spellKey))
}

// chooseInvocationCantrip checks the cantrip a new copy names (or picks one when it names none) and says why when it can't.
func chooseInvocationCantrip(charID int, slug, asked string) (string, string) {
	rep := game.InvocationRepeats[slug]
	taken := invocationCantrips(charID, slug)
	var open []string
	for _, c := range eligibleInvocationCantrips(charID, slug) {
		if !containsStr(taken, c) {
			open = append(open, c)
		}
	}
	name := game.AvailableInvocations[slug].Name
	if strings.TrimSpace(asked) == "" {
		if len(open) == 0 {
			if len(taken) > 0 {
				return "", name + " needs " + rep.Needs + " that no other copy of it has already; you have none left."
			}
			return "", name + " needs " + rep.Needs + "; you have none."
		}
		return open[0], ""
	}
	key, ok := lookupSpell(asked)
	if !ok {
		return "", "'" + asked + "' is not a spell on this server. " + cantripOptions(open)
	}
	if containsStr(taken, key) {
		return "", name + " is already on " + titleWords(strings.ReplaceAll(key, "-", " ")) + ": each copy names a different cantrip. " + cantripOptions(open)
	}
	if !containsStr(open, key) {
		return "", titleWords(strings.ReplaceAll(key, "-", " ")) + " isn't " + rep.Needs + ". " + cantripOptions(open)
	}
	return key, ""
}

func cantripOptions(open []string) string {
	if len(open) == 0 {
		return "You have no cantrip left for it."
	}
	return "Choose one of: " + strings.Join(open, ", ") + "."
}

// addInvocationCantrip records the cantrip of the copy just added; dropInvocationCantrip forgets the last copy's when one is
// replaced.
func addInvocationCantrip(charID int, slug, cantrip string) {
	m := storedInvocationCantrips(charID)
	have := invocationCantrips(charID, slug) // the earlier copies, as they count now
	if len(have) > 0 {
		have = have[:len(have)-1] // the list already counts the new copy (with a picked cantrip): name it
	}
	m[slug] = append(have, cantrip)
	saveInvocationCantrips(charID, m)
}

func dropInvocationCantrip(charID int, slug string) {
	m := storedInvocationCantrips(charID)
	if l := m[slug]; len(l) > 0 {
		m[slug] = l[:len(l)-1]
		saveInvocationCantrips(charID, m)
	}
}

// invocationRepeatInfo: what a repeatable invocation's options say about it: that it repeats, what each copy needs and, for
// a cantrip invocation, the cantrips left for another copy.
func invocationRepeatInfo(charID int, slug string) map[string]interface{} {
	rep, ok := game.InvocationRepeats[slug]
	if !ok {
		return nil
	}
	info := map[string]interface{}{"repeatable": true, "each_copy": rep.Needs}
	if rep.Choice == "cantrip" {
		taken := invocationCantrips(charID, slug)
		open := []string{}
		for _, c := range eligibleInvocationCantrips(charID, slug) {
			if !containsStr(taken, c) {
				open = append(open, c)
			}
		}
		info["cantrips_taken"] = taken
		info["cantrips_open"] = open
		info["how"] = `POST /api/characters/invocations {"character_id": N, "invocation": "` + slug + `", "cantrip": "<one of cantrips_open>"}`
	}
	return info
}

// invocationSummary: each invocation the character has, as the sheet and GET /api/my-turn show it (one entry per copy; a
// cantrip invocation's copy names its cantrip).
func invocationSummary(charID int) []map[string]interface{} {
	out := []map[string]interface{}{}
	copyNo := map[string]int{}
	for _, slug := range getCharacterInvocations(charID) {
		inv, ok := game.AvailableInvocations[slug]
		if !ok {
			continue
		}
		e := map[string]interface{}{"slug": slug, "name": inv.Name, "description": inv.Description, "mechanics": inv.Mechanics}
		if spell, ok := inv.Mechanics["once_per_rest_spell"]; ok {
			used := hasUsedInvocationSpell(charID, slug)
			e["once_per_rest"], e["grants_spell"], e["used"] = true, spell, used
			if !used {
				e["tip"] = "You can cast " + spell + " once using a warlock spell slot!"
			}
		}
		if cs := invocationCantrips(charID, slug); copyNo[slug] < len(cs) {
			c := cs[copyNo[slug]]
			e["cantrip"] = c
			if slug == "eldritch-spear" {
				if sp, ok := srdSpellsMemory[c]; ok {
					e["effect"] = fmt.Sprintf("%s's range increases by 30 feet per Warlock level: %d feet", sp.Name, spellRangeFeet(sp.Range)+eldritchSpearBonus(charID, c))
				}
			}
		}
		copyNo[slug]++
		if slug == "witch-sight" {
			e["passive"], e["range"], e["effect"] = true, "30 feet", "You have Truesight with a range of 30 feet (shown under vision on the sheet)"
		}
		out = append(out, e)
	}
	return out
}
