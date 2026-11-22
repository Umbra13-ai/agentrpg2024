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

type sentenceVerdict struct {
	Monster, Kind, Entry, Sentence, Family, Status, Rule string
}

var (
	familyDamage  = regexp.MustCompile(`\d+ \(\d+d\d+(?: ?[+−-] ?\d+)?\)`)
	familyDice    = regexp.MustCompile(`\(?\b\d+d\d+(?: ?[+−-] ?\d+)?\)?`)
	familyTypes   = regexp.MustCompile(`\b(Acid|Bludgeoning|Cold|Fire|Force|Lightning|Necrotic|Piercing|Poison|Psychic|Radiant|Slashing|Thunder)\b`)
	familyConds   = regexp.MustCompile(`\b(Blinded|Charmed|Deafened|Exhaustion|Frightened|Grappled|Incapacitated|Invisible|Paralyzed|Petrified|Poisoned|Prone|Restrained|Stunned|Unconscious)\b`)
	familyAbility = regexp.MustCompile(`\b(Strength|Dexterity|Constitution|Intelligence|Wisdom|Charisma)\b`)
	familySize    = regexp.MustCompile(`\b(Tiny|Small|Medium|Large|Huge|Gargantuan)\b`)
	familyCount   = regexp.MustCompile(`\b(one|two|three|four|five|six|twice|thrice)\b`)
	familyNumber  = regexp.MustCompile(`[+-]?\d+`)
	familyHeading = regexp.MustCompile(`(Hit or Miss:|Hit:|First Failure:|Second Failure:|Failure by \d+ or More:|Failure or Success:|Failure:|Success:|Trigger:|Response(?:—| -|:))`)
	familySplit   = regexp.MustCompile(`(?:[.!?])\s+(?:[A-Z])|\n`)
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

// entrySentences: an entry's text cut at its headings ("Hit:", "Failure:" ...) and its sentences; each keeps its heading.
func entrySentences(text string) []string {
	var out []string
	locs := familyHeading.FindAllStringIndex(text, -1)
	starts := []int{0}
	for _, l := range locs {
		starts = append(starts, l[0])
	}
	starts = append(starts, len(text))
	for i := 0; i+1 < len(starts); i++ {
		sec := text[starts[i]:starts[i+1]]
		head := ""
		if h := familyHeading.FindStringIndex(sec); h != nil && h[0] == 0 {
			head, sec = sec[:h[1]], sec[h[1]:]
		}
		prev := 0
		for _, l := range familySplit.FindAllStringIndex(sec, -1) {
			out = appendSentence(out, head, sec[prev:l[0]+1])
			prev = l[1] - 1
			if sec[l[1]-1] == '\n' {
				prev = l[1]
			}
		}
		out = appendSentence(out, head, sec[prev:])
	}
	return out
}

func appendSentence(out []string, head, s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return out
	}
	if head != "" {
		s = strings.TrimSpace(head + " " + s)
	}
	return append(out, s)
}

// coveringPattern: the first listed monster text pattern (by name) whose match covers at least half of the sentence, or a whole
// clause the owners cut out of it; "" when none.
func coveringPattern(sentence string, names []string) string {
	body := strings.TrimSpace(familyHeading.ReplaceAllString(sentence, ""))
	if body == "" {
		return "heading"
	}
	for _, n := range names {
		rx := monsterTextPatterns[n]
		for _, loc := range rx.FindAllStringIndex(body, -1) {
			if (loc[1]-loc[0])*2 >= len(body) {
				return n
			}
		}
		if rx.MatchString(strings.TrimSuffix(body, ".")) && len(body) < 60 {
			return n
		}
	}
	return ""
}

// srdSentenceCoverage: the verdict of every sentence of the SRD catalog.
func srdSentenceCoverage() []sentenceVerdict {
	names := make([]string, 0, len(monsterTextPatterns))
	for n := range monsterTextPatterns {
		names = append(names, n)
	}
	sort.Strings(names)
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
				for _, v := range entryCoverage(kind, e, names) {
					v.Monster, v.Kind, v.Entry = m.Name, kind, e.Name
					v.Family = kind + "| " + sentenceFamily(v.Sentence, m, entryNames)
					out = append(out, v)
				}
			}
		}
	}
	return out
}

// entryCoverage: the verdicts of one entry's sentences along the route the server takes for it.
func entryCoverage(kind string, e srdMonsterEntry, names []string) []sentenceVerdict {
	sentences := entrySentences(e.Text)
	all := func(status, rule string) []sentenceVerdict {
		out := make([]sentenceVerdict, 0, len(sentences))
		for _, s := range sentences {
			out = append(out, sentenceVerdict{Sentence: s, Status: status, Rule: rule})
		}
		return out
	}
	// each sentence: read by the route's owner (own), in the residual shown to the GM, or read by a listed pattern
	bySentence := func(route string, own func(string) bool, residual string) []sentenceVerdict {
		out := make([]sentenceVerdict, 0, len(sentences))
		res := strings.ToLower(residual)
		for _, s := range sentences {
			body := strings.TrimSpace(familyHeading.ReplaceAllString(s, ""))
			v := sentenceVerdict{Sentence: s}
			switch {
			case body != "" && res != "" && strings.Contains(res, strings.ToLower(strings.TrimSuffix(body, "."))):
				v.Status, v.Rule = "gm", route+" residual"
			case strings.HasPrefix(s, "Hit or Miss:"):
				v.Status, v.Rule = "gm", "hitOrMissNote"
			case strings.HasPrefix(s, "Trigger:"):
				v.Status, v.Rule = "gm", "a Reaction's trigger: the GM sends the Reaction when it happens"
			case specialMoveSentence(body):
				v.Status, v.Rule = "applied", "readSpecialMove / grantSpecialMove"
			case own(s):
				v.Status, v.Rule = "applied", route
			default:
				if p := coveringPattern(s, names); p != "" {
					v.Status, v.Rule = "applied", p
				} else {
					v.Status, v.Rule = "unhandled", route
				}
			}
			out = append(out, v)
		}
		return out
	}
	if kind == "trait" {
		if r, ok := traitRuleFor(e); ok {
			if r.GM {
				return all("gm", "monsterTraitRules GM row")
			}
			return all("applied", "monsterTraitRules: "+r.Where)
		}
		if strings.HasPrefix(e.Name, "Spellcasting") {
			return all("applied", "monster spellcasting (resolveMonsterSpell)")
		}
		if _, ok := parseSaveEffect(e.Text); ok {
			return bySentence("save owner (trait)", saveOwnSentence, saveResidual(e.Text))
		}
		return all("gm", "trait the GM applies (traitsForGM)")
	}
	if kind == "reaction" {
		low := strings.ToLower(e.Text)
		if needs, _ := reactionSelfHitTrigger(e.Text); needs || reactACRe.MatchString(e.Text) || reactReduceRe.MatchString(e.Text) || reactUsesRe.MatchString(e.Text) ||
			strings.Contains(low, "casts counterspell or shield") || strings.Contains(low, "splits into two new") ||
			(strings.Contains(low, "swap places") && strings.Contains(low, "becomes the target")) || strings.Contains(low, "the wearer gains a +5 bonus to ac") {
			return bySentence("resolveMonsterReaction", func(s string) bool { return strings.HasPrefix(s, "Trigger:") || strings.HasPrefix(s, "Response") }, "")
		}
	}
	if forms, _ := shapeShiftForms(e.Text); len(forms) > 0 {
		// the reply says what a form changes besides its Speed (size, equipment, game statistics) is the GM's
		return bySentence("resolveShapeShift", func(s string) bool { return true }, "")
	}
	switch {
	case strings.HasPrefix(e.Name, "Multiattack"):
		return bySentence("multiattackPlan", func(s string) bool { return true }, "")
	case strings.HasPrefix(e.Name, "Spellcasting"):
		return bySentence("monster spellcasting (resolveMonsterSpell)", func(s string) bool { return true }, "")
	case isAttackEntry(e.Text):
		a := parseMonsterAttack(e.Name, e.Text)
		if _, problem := prepareMonsterAttack(a.monsterActionRow, "fire"); problem != "" {
			return all("gm", "attack refused: "+problem)
		}
		residual := unappliedHitRider(e.Text) + " " + gmJudgedAlternatives(a.monsterActionRow)
		if sv, ok := parseSaveEffect(e.Text); ok {
			residual += " " + sv.Extra
		}
		return bySentence("attack owner (parseMonsterAttack)", attackOwnSentence, residual)
	case len(splitSaveOptions(e.Text)) >= 2:
		opts := splitSaveOptions(e.Text)
		residual := saveOptionsIntro(e.Text, opts)
		for _, o := range opts {
			residual += " " + saveResidual(o.Text)
		}
		return bySentence("splitSaveOptions (the GM names the effect)", func(s string) bool {
			for _, o := range opts {
				if strings.Contains(s, o.Name) {
					return true
				}
			}
			return saveOwnSentence(s)
		}, residual)
	case mfxAttackRe.MatchString(e.Text):
		return bySentence("mfxAttackRe (one attack of another entry)", func(s string) bool { return mfxAttackRe.MatchString(s) }, "")
	case len(castChoiceList(e.Text)) >= 2 || mfxSpellRe.MatchString(e.Text):
		return bySentence("monster cast (resolveMonsterSpell)", func(s string) bool { return mfxSpellRe.MatchString(s) || strings.Contains(s, "casts") }, castRiders(e.Text))
	}
	if _, ok := parseSaveEffect(e.Text); ok {
		return bySentence("save owner (parseSaveEffect)", saveOwnSentence, saveResidual(e.Text))
	}
	if monsterLightToggleRe.MatchString(e.Text) {
		return bySentence("resolveLightToggle", func(string) bool { return true }, "")
	}
	if obscuringTextRe.MatchString(e.Text) {
		return bySentence("resolveObscuringText", func(string) bool { return true }, "")
	}
	if mfxUsesRe.MatchString(e.Text) {
		return bySentence("mfxUsesRe (uses another entry)", func(s string) bool { return mfxUsesRe.MatchString(s) }, "")
	}
	for _, n := range names {
		if monsterTextPatterns[n].MatchString(e.Text) && strings.Contains(n, "") {
			if p := coveringPattern(e.Text, names); p != "" {
				return bySentence(p, func(string) bool { return false }, "")
			}
		}
	}
	return all("gm", "resolveMonsterEffectText: the GM resolves it")
}

// specialMoveSentence: a sentence the movement owner reads as the entry's own move (grantSpecialMove runs on every entry).
func specialMoveSentence(s string) bool {
	_, ok := readSpecialMove(s, func(string) int { return 30 })
	return ok
}

// attackOwnSentence: the attack line and the Hit's damage sentence, read by parseMonsterAttack.
// Any other sentence of the Hit is read by the hit's owners (parseHitRider, hitAfterDamage, the save owner for "subjected to the
// following effect"): what they don't apply is the residual unappliedHitRider reports, checked before this.
func attackOwnSentence(s string) bool {
	return strings.Contains(s, "Attack Roll:") || strings.HasPrefix(s, "Hit:") || saveOwnSentence(s)
}

// saveOwnSentence: the save line, its damage and half damage, read by parseSaveEffect.
// A sentence of a Failure, Success or staged section is read by parseSaveEffect: what it doesn't apply is its Extra (the residual,
// checked before this). The save line itself is read too. Sentences before the save line (an area's description, a trigger) are not.
func saveOwnSentence(s string) bool {
	if mfxSaveRe.MatchString(s) {
		return true
	}
	for _, h := range []string{"Failure", "Success:", "First Failure:", "Second Failure:"} {
		if strings.HasPrefix(s, h) {
			return true
		}
	}
	return false
}

func saveResidual(text string) string {
	e, _ := parseSaveEffect(text)
	return e.Extra + " " + e.Setup
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
	report.WriteString("| family | stat blocks | sentences | status | owner |\n|---|---|---|---|---|\n")
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
		fmt.Fprintf(&report, "| %s | %d | %d | %s | %s |\n", strings.ReplaceAll(k, "|", "/"), len(f.monsters), f.count, status, strings.ReplaceAll(strings.Join(rules, "; "), "|", "/"))
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
