package main

// A compendium monster that calls itself he or she (owner, Nov 25: "Yes" to reading a monster's own he/his/she/her as "its"; I-ALDENQA-1). The
// SRD calls every creature "it" ("until the start of its next turn") and never uses he or she, so every rule that reads a stat block's words
// is written for "it". readAsIt is the one place the other wording is read: the monster's definition stays as the GM wrote it (the stat block,
// the editor), and the two views the rules read are made from it after the fold: the play rows (customMonsterStats) and the stat block the
// owners read (metaFromMonster). No reader of a sentence learns about pronouns.

import (
	"regexp"
	"strings"
)

// personalPronounRe: "he", "his", "she", "her" ... in an entry.
var personalPronounRe = regexp.MustCompile(`(?i)\b(he|him|his|himself|she|her|hers|herself)\b`)

// herObjectNext: the words after which "her" is the creature itself, not its possession ("attacks her and", "toward her,"): a "her" followed
// by any other word is possessive ("her next turn": its next turn).
var herObjectNext = map[string]bool{"and": true, "or": true, "but": true, "to": true, "with": true, "as": true, "if": true, "until": true,
	"in": true, "on": true, "at": true, "from": true, "by": true, "for": true, "of": true, "into": true, "than": true, "that": true,
	"the": true, "a": true, "an": true, "when": true, "while": true, "before": true, "after": true, "again": true, "away": true}

var herWithNextRe = regexp.MustCompile(`(?i)\bher\b(\s+([a-z']+))?`)

// itsWording: one text with he or she read as it.
func itsWording(text string) string {
	if !personalPronounRe.MatchString(text) {
		return text
	}
	text = herWithNextRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := herWithNextRe.FindStringSubmatch(m)
		word := "its"
		if sub[2] == "" || herObjectNext[strings.ToLower(sub[2])] {
			word = "it"
		}
		return sameCase(m[:3], word) + m[3:]
	})
	return personalPronounRe.ReplaceAllStringFunc(text, func(w string) string {
		switch strings.ToLower(w) {
		case "he", "she", "him":
			return sameCase(w, "it")
		case "his", "hers":
			return sameCase(w, "its")
		case "himself", "herself":
			return sameCase(w, "itself")
		}
		return w // "her" was read above
	})
}

// sameCase: word written as the original's first letter was (He -> It).
func sameCase(orig, word string) string {
	if orig != "" && orig[:1] != strings.ToLower(orig[:1]) {
		return strings.ToUpper(word[:1]) + word[1:]
	}
	return word
}

// readAsIt: the monster as the rules read it, every entry and the Legendary Actions' intro with he or she read as it. The caller's monster is
// not changed (its entries are copied).
func readAsIt(m srdMonster2024) srdMonster2024 {
	fold := func(list []srdMonsterEntry) []srdMonsterEntry {
		if list == nil {
			return nil
		}
		out := make([]srdMonsterEntry, len(list))
		for i, e := range list {
			e.Text = itsWording(e.Text)
			out[i] = e
		}
		return out
	}
	m.Traits, m.Actions, m.BonusActions, m.Reactions, m.Legendary = fold(m.Traits), fold(m.Actions), fold(m.BonusActions), fold(m.Reactions), fold(m.Legendary)
	m.LegendaryIn = itsWording(m.LegendaryIn)
	return m
}
