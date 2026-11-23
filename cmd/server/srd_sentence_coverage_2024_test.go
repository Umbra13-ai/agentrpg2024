package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The coverage of the 330 SRD 5.2.1 stat blocks, one sentence at a time (owner, Nov 21): every sentence of every trait, action, bonus
// action, reaction and legendary action is applied by a named rule or flagged to the GM; nothing is silent.
//
// srdSentenceCoverage follows the route the server takes for each entry (the trait table, the attack owner, the save owner, the
// resolver's branches in resolveMonsterEffectText's order) and gives each sentence one verdict:
//
//	applied    a rule reads it: the route's owner, or a listed monster text pattern (monsterTextPatterns) that covers it
//	gm         the GM is shown it: the trait is the GM's in the stat block view (traitsForGM), the entry falls through to "the GM
//	           resolves it", the attack is refused with its reason, or the sentence is in the residual the reply flags
//	unhandled  a route takes the entry but no rule reads the sentence and the reply doesn't show it: silent
//
// Sentences are grouped into wording families: the sentence with its numbers, dice, DCs, damage types, conditions, abilities, sizes,
// the creature's own name and other entries' names replaced by placeholders (sentenceFamily).

var (
	familyDamage  = regexp.MustCompile(`\d+ \(\d+d\d+(?: ?[+−-] ?\d+)?\)`)
	familyDice    = regexp.MustCompile(`\(?\b\d+d\d+(?: ?[+−-] ?\d+)?\)?`)
	familyTypes   = regexp.MustCompile(`\b(Acid|Bludgeoning|Cold|Fire|Force|Lightning|Necrotic|Piercing|Poison|Psychic|Radiant|Slashing|Thunder)\b`)
	familyConds   = regexp.MustCompile(`\b(Blinded|Charmed|Deafened|Exhaustion|Frightened|Grappled|Incapacitated|Invisible|Paralyzed|Petrified|Poisoned|Prone|Restrained|Stunned|Unconscious)\b`)
	familyAbility = regexp.MustCompile(`\b(Strength|Dexterity|Constitution|Intelligence|Wisdom|Charisma)\b`)
	familySize    = regexp.MustCompile(`\b(Tiny|Small|Medium|Large|Huge|Gargantuan)\b`)
	familyCount   = regexp.MustCompile(`\b(one|two|three|four|five|six|twice|thrice)\b`)
	familyNumber  = regexp.MustCompile(`[+-]?\d+`)
)

// sentenceFamily: a sentence with its specifics replaced by placeholders (see above). entries are the stat block's entry names.
func sentenceFamily(sentence string, m srdMonster2024, entries []string) string {
	s := strings.NewReplacer("’", "'", "–", "-", "—", " - ").Replace(sentence)
	for _, e := range entries {
		if n := stripUsage(e); len(n) > 2 {
			s = regexp.MustCompile(`\b`+regexp.QuoteMeta(n)+`\b`).ReplaceAllString(s, "ENTRY")
		}
	}
	words := strings.Fields(strings.ToLower(m.Name))
	for _, n := range []string{strings.ToLower(m.Name), words[len(words)-1], "dragon", "swarm", "hag", "devil", "demon", "giant", "golem"} {
		s = regexp.MustCompile(`(?i)\bthe `+regexp.QuoteMeta(n)+`('s)?\b`).ReplaceAllString(s, "the X$1")
	}
	s = familyDamage.ReplaceAllString(s, "DMG")
	s = familyDice.ReplaceAllString(s, "DICE")
	s = familyTypes.ReplaceAllString(s, "TYPE")
	s = familyConds.ReplaceAllString(s, "COND")
	s = familyAbility.ReplaceAllString(s, "ABIL")
	s = familySize.ReplaceAllString(s, "SIZE")
	s = familyCount.ReplaceAllString(s, "NUM")
	s = familyNumber.ReplaceAllString(s, "N")
	return strings.Join(strings.Fields(s), " ")
}

// srdSentenceCoverage: the verdict of every sentence of the SRD catalog.
func srdSentenceCoverage() []sentenceVerdict {
	var out []sentenceVerdict
	for _, m := range srdMonsters2024() {
		var entryNames []string
		for _, g := range [][]srdMonsterEntry{m.Traits, m.Actions, m.BonusActions, m.Reactions, m.Legendary} {
			for _, e := range g {
				entryNames = append(entryNames, e.Name)
			}
		}
		for kind, group := range map[string][]srdMonsterEntry{"trait": m.Traits, "action": m.Actions, "bonus": m.BonusActions, "reaction": m.Reactions, "legendary": m.Legendary} {
			for _, e := range group {
				for _, v := range entryVerdicts(kind, e) {
					v.Monster, v.Kind, v.Entry = m.Name, kind, e.Name
					v.Family = kind + "| " + sentenceFamily(v.Sentence, m, entryNames)
					out = append(out, v)
				}
			}
		}
	}
	return out
}

// TestSRDMonsterSentenceCoverage: every sentence is applied or flagged to the GM; a sentence nothing reads and nothing shows (unhandled)
// is allowed only while its family is listed in srdUnhandledFamilies, the work still to do, which must shrink: a listed family that is
// now covered fails too. SRD_COVERAGE_OUT receives the family table (Markdown).
func TestSRDMonsterSentenceCoverage(t *testing.T) {
	vs := srdSentenceCoverage()
	type fam struct {
		monsters map[string]bool
		count    int
		status   map[string]int
		rules    map[string]int
		example  string
	}
	fams := map[string]*fam{}
	totals := map[string]int{}
	for _, v := range vs {
		f := fams[v.Family]
		if f == nil {
			f = &fam{monsters: map[string]bool{}, status: map[string]int{}, rules: map[string]int{}, example: v.Monster + " / " + v.Entry + ": " + v.Sentence}
			fams[v.Family] = f
		}
		f.monsters[v.Monster] = true
		f.count++
		f.status[v.Status]++
		f.rules[v.Rule]++
		totals[v.Status]++
	}
	keys := make([]string, 0, len(fams))
	for k := range fams {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := fams[keys[i]], fams[keys[j]]
		if len(a.monsters) != len(b.monsters) {
			return len(a.monsters) > len(b.monsters)
		}
		return keys[i] < keys[j]
	})
	var report strings.Builder
	report.WriteString("# SRD 5.2.1 monster coverage, by wording family\n\nGenerated by `SRD_COVERAGE_OUT=$PWD/docs/SRD_MONSTER_COVERAGE.md go test ./cmd/server -run TestSRDMonsterSentenceCoverage`" +
		" (srd_sentence_coverage_2024_test.go): every sentence of the 330 stat blocks, its route and verdict. A family is a sentence with its" +
		" numbers, dice, damage types, conditions, abilities, sizes, its creature's name and other entries' names replaced by placeholders." +
		" \"GM-flagged\" means the reply or the GM's stat block view shows it to the GM; the work to automate is the GM-flagged families, highest count first.\n\n")
	fmt.Fprintf(&report, "Sentences: %d; applied %d, flagged to the GM %d, unhandled %d. Families: %d.\n\n", len(vs), totals["applied"], totals["gm"], totals["unhandled"], len(fams))
	report.WriteString("| family | stat blocks | sentences | status | owner | example |\n|---|---|---|---|---|---|\n")
	for _, k := range keys {
		f := fams[k]
		status := "applied"
		switch {
		case f.status["unhandled"] > 0:
			status = "unhandled"
		case f.status["gm"] > 0 && f.status["applied"] > 0:
			status = "applied / GM-flagged"
		case f.status["gm"] > 0:
			status = "GM-flagged"
		}
		var rules []string
		for r := range f.rules {
			rules = append(rules, r)
		}
		sort.Strings(rules)
		fmt.Fprintf(&report, "| %s | %d | %d | %s | %s | %s |\n", strings.ReplaceAll(k, "|", "/"), len(f.monsters), f.count, status, strings.ReplaceAll(strings.Join(rules, "; "), "|", "/"), strings.ReplaceAll(f.example, "|", "/"))
		_, listed := srdUnhandledFamilies[k]
		if f.status["unhandled"] > 0 && !listed {
			t.Errorf("silent: %d sentence(s) of %q are read by no rule and not shown to the GM (e.g. %s)", f.status["unhandled"], k, f.example)
		}
		if f.status["unhandled"] == 0 && listed {
			t.Errorf("srdUnhandledFamilies lists %q, which is now covered: remove it", k)
		}
	}
	for k := range srdUnhandledFamilies {
		if fams[k] == nil {
			t.Errorf("srdUnhandledFamilies lists %q, which is no family of the SRD catalog", k)
		}
	}
	t.Logf("SRD sentence coverage: %d sentences, applied %d, flagged to the GM %d, unhandled %d (%d families listed as work to do)",
		len(vs), totals["applied"], totals["gm"], totals["unhandled"], len(srdUnhandledFamilies))
	if out := os.Getenv("SRD_COVERAGE_OUT"); out != "" {
		if err := os.WriteFile(out, []byte(report.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// srdUnhandledFamilies: the families whose sentences no rule reads yet and no reply shows (the remaining work, highest count first).
var srdUnhandledFamilies = map[string]string{}
