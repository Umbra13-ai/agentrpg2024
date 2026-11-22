package main

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every branch of every alternation of a monster text pattern must serve SRD 5.2.1 wording too (owner, Nov 21): a pattern that
// matches the SRD as a whole could still keep a branch written for book text. For each alternation, a variant of the pattern with
// only that branch must match an SRD stat block piece, or the pattern's named witness. Lists of game vocabulary (sizes, abilities,
// damage types, conditions, creature types, units, number words, apostrophes, articles and pronouns) are exempt: their branches name
// terms, not wordings.

// patternVocabulary: the words whose alternations are exempt.
var patternVocabulary = func() map[string]bool {
	words := map[string]bool{}
	for _, list := range []string{
		"tiny small medium large huge gargantuan",
		"strength dexterity constitution intelligence wisdom charisma str dex con int wis cha",
		"acid bludgeoning cold fire force lightning necrotic piercing poison psychic radiant slashing thunder",
		"blinded charmed deafened exhaustion frightened grappled incapacitated invisible paralyzed petrified poisoned prone restrained stunned unconscious",
		"aberration aberrations beast beasts celestial celestials construct constructs dragon dragons elemental elementals fey fiend fiends giant giants humanoid humanoids monstrosity monstrosities ooze oozes plant plants undead",
		"feet foot ft ft. inch inches minute minutes hour hours round rounds day days mile miles",
		"one two three four five six seven eight nine ten twice",
		"' ’ s",
		"a an the its it itself them their his her this that these those",
		"is are was be become becomes has have",
		"swim fly climb burrow walk",
		"melee ranged",
		"start end",
		"and or plus",
		"creature target enemy ally",
		"bonus action reaction",
		"advantage disadvantage",
		"until before", // riderUntil, shared by every rider pattern: "until the end of ..." (Disadvantage riders) and "before the start of ..." (the exposed rider) are both SRD
		"hit miss",
		"attack attacks",
	} {
		for _, w := range strings.Fields(list) {
			words[w] = true
		}
	}
	return words
}()

// sourceAlternations: every group of the pattern's source that holds a top-level "|", with the byte offsets of its content and its
// branches as written (no factoring); escapes and character classes are skipped.
func sourceAlternations(src string) (groups [][2]int, branches [][]string) {
	type open struct {
		start int // first byte of the group's content
		cuts  []int
	}
	var stack []open
	top := open{start: 0}
	inClass := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '\\':
			i++
		case inClass:
			if c == ']' {
				inClass = false
			}
		case c == '[':
			inClass = true
		case c == '(':
			start := i + 1
			if strings.HasPrefix(src[start:], "?:") {
				start += 2
			} else if strings.HasPrefix(src[start:], "?i:") {
				start += 3
			} else if strings.HasPrefix(src[start:], "?i)") || strings.HasPrefix(src[start:], "?m)") || strings.HasPrefix(src[start:], "?s)") {
				i += 3
				continue
			}
			stack = append(stack, open{start: start})
		case c == ')':
			if len(stack) == 0 {
				continue
			}
			g := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if len(g.cuts) > 0 {
				groups = append(groups, [2]int{g.start, i})
				var parts []string
				prev := g.start
				for _, cut := range g.cuts {
					parts = append(parts, src[prev:cut])
					prev = cut + 1
				}
				branches = append(branches, append(parts, src[prev:i]))
			}
		case c == '|':
			if len(stack) == 0 {
				top.cuts = append(top.cuts, i)
			} else {
				stack[len(stack)-1].cuts = append(stack[len(stack)-1].cuts, i)
			}
		}
	}
	if len(top.cuts) > 0 {
		groups = append(groups, [2]int{0, len(src)})
		var parts []string
		prev := 0
		for _, cut := range top.cuts {
			parts = append(parts, src[prev:cut])
			prev = cut + 1
		}
		branches = append(branches, append(parts, src[prev:]))
	}
	return
}

var branchSyntaxRe = regexp.MustCompile(`\\.|\(\?[:i]+|[()?*+]|\.|\[[^\]]*\]|\{\d+(?:,\d*)?\}|\^|\$`)

// vocabularyBranch: the branch, read as words, names only game vocabulary (or nothing: an optional part).
func vocabularyBranch(b string) bool {
	words := strings.Fields(strings.ToLower(branchSyntaxRe.ReplaceAllString(b, " ")))
	for _, w := range words {
		w = strings.Trim(w, ",;:-'’")
		if w != "" && !patternVocabulary[w] && !patternVocabulary[strings.TrimSuffix(w, "s")] && !wordFragment(w) {
			return false
		}
	}
	return true
}

// wordFragment: a piece of a vocabulary word the source splits ("f(?:oo|ee)t": oo, ee).
func wordFragment(w string) bool {
	for v := range patternVocabulary {
		if len(w) <= 3 && len(v) > len(w) && strings.Contains(v, w) {
			return true
		}
	}
	return false
}

// alternationVariants: for each alternation of the pattern whose branches aren't all vocabulary, one variant per branch.
func alternationVariants(pattern string) (variants []string, branchOf []string) {
	flags := "" // a leading flag group applies to every branch, so it stays outside the split
	if m := regexp.MustCompile(`^\(\?[imsU]+\)`).FindString(pattern); m != "" {
		flags, pattern = m, pattern[len(m):]
	}
	groups, branches := sourceAlternations(pattern)
	for gi, g := range groups {
		vocab := true
		for _, b := range branches[gi] {
			vocab = vocab && vocabularyBranch(b)
		}
		if vocab {
			continue
		}
		for _, b := range branches[gi] {
			if strings.TrimSpace(b) == "" {
				continue
			}
			variants = append(variants, flags+pattern[:g[0]]+b+pattern[g[1]:])
			branchOf = append(branchOf, b)
		}
	}
	return
}

// branchWitnesses: a branch kept for safety with no SRD wording, and the invented sentence a test feeds it (the sentence must be in a
// _test.go file and the branch's variant must match it). Only refusals belong here: a branch that keeps the server from applying
// something, never one that applies a rule.
var branchWitnesses = map[string]map[string]string{
	"hitRiderChoiceRe": {
		"choice of":             "the target has the brute's choice of (A) the Charmed condition",
		"determined by rolling": "the target has a condition determined by rolling 1d4",
	},
}

// TestMonsterPatternBranchesServeSRDWording: see above. A branch with no SRD match is reported with its pattern; archive the branch,
// or name an invented witness for it in patternWitnesses (the witness then has to match every branch it stands for).
func TestMonsterPatternBranchesServeSRDWording(t *testing.T) {
	pieces := srdTextPieces()
	keys := make([]string, 0, len(pieces))
	for k := range pieces {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var allTests string
	for _, src := range testSources(t) {
		allTests += src
	}
	names := make([]string, 0, len(monsterTextPatterns))
	for n := range monsterTextPatterns {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, witnessed := patternWitnesses[name]; witnessed {
			continue // a clause-level pattern: its witness is checked whole by TestMonsterPatternsServeSRDWording
		}
		rx := monsterTextPatterns[name]
		variants, branches := alternationVariants(rx.String())
		for i, v := range variants {
			vrx, err := regexp.Compile(v)
			if err != nil {
				continue
			}
			if w, ok := branchWitnesses[name][branches[i]]; ok {
				if !vrx.MatchString(w) || !strings.Contains(allTests, w) {
					t.Errorf("%s: the branch witness %q isn't read by the branch or isn't in a test", name, w)
				}
				continue
			}
			if firstSRDMatch(vrx, pieces, keys) == "" {
				t.Errorf("%s: the branch %q matches no SRD 5.2.1 stat block", name, branches[i])
			}
		}
	}
}
