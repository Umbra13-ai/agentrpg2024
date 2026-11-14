package main

// Pact of the Tome (SRD 5.2.1 Eldritch Invocation): "When the book appears, choose three cantrips, and choose two level 1 spells that
// have the Ritual tag. The spells can be from any class's spell list, and they must be spells you don't already have prepared. While
// the book is on your person, you have the chosen spells prepared, and they function as Warlock spells for you."
// The picks are sent with the invocation: POST /api/characters/invocations {"character_id": N, "invocation": "pact-of-the-tome",
// "cantrips": [3], "rituals": [2]}; sending them again for a Warlock who has the invocation conjures a new book with new picks.
// They are kept in the book_of_shadows column, apart from known_spells, so they never count against the Warlock's own limits
// (F9: setup suggested Pact of the Tome, but the book's spells could not be chosen anywhere).

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var bookOfShadowsOnce sync.Once

func ensureBookOfShadows() {
	bookOfShadowsOnce.Do(func() {
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS book_of_shadows JSONB DEFAULT '{}'`)
	})
}

type bookOfShadows struct {
	Cantrips []string `json:"cantrips"`
	Rituals  []string `json:"rituals"`
}

// bookOfShadowsOf: the book's picks (empty without Pact of the Tome).
func bookOfShadowsOf(charID int) bookOfShadows {
	var b bookOfShadows
	if db == nil || !hasPactBoon(charID, "tome") {
		return b
	}
	ensureBookOfShadows()
	var raw []byte
	db.QueryRow(`SELECT COALESCE(book_of_shadows,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	json.Unmarshal(raw, &b)
	return b
}

// bookOfShadowsSpells: the cantrips and rituals in the Warlock's Book of Shadows (always prepared while the book is carried).
func bookOfShadowsSpells(charID int) []string {
	b := bookOfShadowsOf(charID)
	return append(append([]string{}, b.Cantrips...), b.Rituals...)
}

// bookOfShadowsOpen: a Warlock with Pact of the Tome whose book has no picks yet.
func bookOfShadowsOpen(charID int) bool {
	if db == nil || !hasPactBoon(charID, "tome") {
		return false
	}
	b := bookOfShadowsOf(charID)
	return len(b.Cantrips) < 3 || len(b.Rituals) < 2
}

var bookSpellClasses = []string{"bard", "cleric", "druid", "paladin", "ranger", "sorcerer", "warlock", "wizard"}

func onAnyClassList(slug string) bool {
	if sp, ok := srdSpellsMemory[slug]; ok && len(sp.Classes) > 0 {
		return true
	}
	for _, c := range bookSpellClasses {
		if isSpellOnClassList(slug, c) {
			return true
		}
	}
	return false
}

// bookHave: the spells the Warlock already has prepared (the book's picks must be others).
func bookHave(charID int) map[string]bool {
	have := map[string]bool{}
	for _, s := range append(currentPreparedList(charID, "warlock"), originAlwaysPrepared(charID)...) {
		have[normalizeSpellSlug(s)] = true
	}
	for _, s := range append(classAlwaysPrepared(classLevelsOrSingle(charID)), jsonListOf(charID, "prepared_spells")...) {
		have[normalizeSpellSlug(s)] = true
	}
	return have
}

// sortedSpellSlugs: every SRD spell slug, sorted.
func sortedSpellSlugs() []string {
	out := make([]string, 0, len(srdSpellsMemory))
	for k := range srdSpellsMemory {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// checkBookOfShadows checks the picks: three different cantrips and two different level 1 Ritual spells, from any class's spell
// list, that the Warlock doesn't already have prepared. It returns the slugs, or what is wrong.
func checkBookOfShadows(charID int, cantrips, rituals []string) (bookOfShadows, string) {
	var out bookOfShadows
	if len(cantrips) != 3 || len(rituals) != 2 {
		return out, fmt.Sprintf("Pact of the Tome: send cantrips (3 cantrips) and rituals (2 level 1 spells with the Ritual tag) for your Book of Shadows; you sent %d and %d.", len(cantrips), len(rituals))
	}
	have := bookHave(charID)
	seen := map[string]bool{}
	pick := func(input string, ritual bool) (string, string) {
		k := normalizeSpellSlug(strings.ReplaceAll(strings.ReplaceAll(input, "'", ""), "’", ""))
		sp, ok := srdSpellsMemory[k]
		if !ok {
			return "", unknownSpellMessage(input)
		}
		switch {
		case !ritual && sp.Level != 0:
			return "", fmt.Sprintf("%s is a level %d spell, not a cantrip: cantrips takes three cantrips.", sp.Name, sp.Level)
		case ritual && (sp.Level != 1 || !sp.IsRitual):
			return "", fmt.Sprintf("%s isn't a level 1 spell with the Ritual tag: rituals takes two of those (Detect Magic, Find Familiar, Identify ...).", sp.Name)
		case !onAnyClassList(k):
			return "", fmt.Sprintf("%s isn't on any class's spell list.", sp.Name)
		case seen[k]:
			return "", fmt.Sprintf("You chose %s twice: the book holds different spells.", sp.Name)
		case have[k]:
			return "", fmt.Sprintf("You already have %s prepared: the book's spells must be ones you don't already have prepared.", sp.Name)
		}
		seen[k] = true
		return k, ""
	}
	for _, c := range cantrips {
		k, why := pick(c, false)
		if why != "" {
			return out, why
		}
		out.Cantrips = append(out.Cantrips, k)
	}
	for _, r := range rituals {
		k, why := pick(r, true)
		if why != "" {
			return out, why
		}
		out.Rituals = append(out.Rituals, k)
	}
	return out, ""
}

func saveBookOfShadows(charID int, b bookOfShadows) {
	ensureBookOfShadows()
	raw, _ := json.Marshal(b)
	db.Exec(`UPDATE characters SET book_of_shadows = $1 WHERE id = $2`, raw, charID)
}

// suggestBookOfShadows: picks the check accepts, for the setup guide's request body.
func suggestBookOfShadows(charID int) ([]string, []string) {
	have := bookHave(charID)
	var cantrips, rituals []string
	for _, s := range []string{"guidance", "light", "mage-hand", "prestidigitation", "message", "minor-illusion", "thaumaturgy", "druidcraft", "resistance", "spare-the-dying"} {
		if len(cantrips) < 3 && bookPickOK(s, false, have) {
			cantrips = append(cantrips, s)
		}
	}
	for _, s := range []string{"find-familiar", "detect-magic", "identify", "comprehend-languages", "alarm", "speak-with-animals", "unseen-servant"} {
		if len(rituals) < 2 && bookPickOK(s, true, have) {
			rituals = append(rituals, s)
		}
	}
	return cantrips, rituals
}

// bookPickOK: one spell would pass the book check (the right kind, on a class list, not prepared already).
func bookPickOK(slug string, ritual bool, have map[string]bool) bool {
	sp, ok := srdSpellsMemory[slug]
	if !ok || have[slug] || !onAnyClassList(slug) {
		return false
	}
	if ritual {
		return sp.Level == 1 && sp.IsRitual
	}
	return sp.Level == 0
}
