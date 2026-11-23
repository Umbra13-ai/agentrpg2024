package main

// What the server does with each sentence of a stat-block entry (owner, Nov 22): one verdict, asked by the SRD coverage test
// (TestSRDMonsterSentenceCoverage), by the import preview (POST /api/gm/compendium/monsters/preview) and by the GM's stat block view
// (monsterStatBlock's entry_rules), so a GM reads at import, and again in play, the same answer the tests hold the SRD to.
//
// entryVerdicts follows the route the server takes for an entry (the trait table, the attack owner, the save owner, the resolver's
// branches in resolveMonsterEffectText's order) and gives each sentence one verdict:
//
//	applied    a rule reads it: the route's owner, or a listed monster text pattern (monsterTextPatterns) that covers it
//	gm         the GM is shown it: the trait is the GM's in the stat block view (traitsForGM), the entry falls through to "the GM
//	           resolves it", the attack is refused with its reason, or the sentence is in the residual the reply flags
//	unhandled  a route takes the entry but no rule reads the sentence and the reply doesn't show it: silent
//
// The wording it reads is the SRD's: a sentence worded another way is the GM's, and the report says so.

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

// entryHeadingRe: the headings that open a section of an entry ("Hit:", "Failure:", "Trigger:" ...); entrySplitRe cuts a section into
// sentences.
var (
	entryHeadingRe = regexp.MustCompile(`(Hit or Miss:|Hit:|First Failure:|Second Failure:|Failure by \d+ or More:|Failure or Success:|Failure:|Success:|Trigger:|Response(?:—| -|:))`)
	entrySplitRe   = regexp.MustCompile(`(?:[.!?])\s+(?:[A-Z])|\n`)
)

// sentenceVerdict is what the server does with one sentence of an entry (Monster, Kind, Entry and Family are filled by the SRD test).
type sentenceVerdict struct {
	Monster, Kind, Entry, Sentence, Family, Status, Rule string
}

// entrySentences: an entry's text cut at its headings ("Hit:", "Failure:" ...) and its sentences; each keeps its heading.
func entrySentences(text string) []string {
	var out []string
	locs := entryHeadingRe.FindAllStringIndex(text, -1)
	starts := []int{0}
	for _, l := range locs {
		starts = append(starts, l[0])
	}
	starts = append(starts, len(text))
	for i := 0; i+1 < len(starts); i++ {
		sec := text[starts[i]:starts[i+1]]
		head := ""
		if h := entryHeadingRe.FindStringIndex(sec); h != nil && h[0] == 0 {
			head, sec = sec[:h[1]], sec[h[1]:]
		}
		prev := 0
		for _, l := range entrySplitRe.FindAllStringIndex(sec, -1) {
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

var (
	patternNamesOnce sync.Once
	patternNames     []string
)

// sortedPatternNames: the names of monsterTextPatterns in the order coveringPattern asks them.
func sortedPatternNames() []string {
	patternNamesOnce.Do(func() {
		for n := range monsterTextPatterns {
			patternNames = append(patternNames, n)
		}
		sort.Strings(patternNames)
	})
	return patternNames
}

// coveringPattern: the first listed monster text pattern (by name) whose match covers at least half of the sentence, or a whole
// clause the owners cut out of it; "" when none.
func coveringPattern(sentence string) string {
	body := strings.TrimSpace(entryHeadingRe.ReplaceAllString(sentence, ""))
	if body == "" {
		return "heading"
	}
	for _, n := range sortedPatternNames() {
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

// entryVerdicts: the verdicts of one entry's sentences along the route the server takes for it. kind is trait, action, bonus,
// reaction or legendary. A sentence a mechanic the GM attached covers is applied by that mechanic's owner (monster_mechanics_2024.go).
func entryVerdicts(kind string, e srdMonsterEntry) []sentenceVerdict {
	out := textVerdicts(kind, e)
	for i := range out {
		if k := mechanicCovers(e, out[i].Sentence); k != "" {
			r, _ := mechanicKindOf(k)
			out[i].Status, out[i].Rule = "applied", "attached mechanic "+k+": "+r.AppliedBy
		}
	}
	return out
}

// textVerdicts: the verdicts the entry's words get from the server's text rules.
func textVerdicts(kind string, e srdMonsterEntry) []sentenceVerdict {
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
			body := strings.TrimSpace(entryHeadingRe.ReplaceAllString(s, ""))
			v := sentenceVerdict{Sentence: s}
			switch {
			case body != "" && res != "" && strings.Contains(res, strings.ToLower(strings.TrimSuffix(body, "."))):
				v.Status, v.Rule = "gm", route+" residual"
			case strings.HasPrefix(s, "Hit or Miss:"):
				v.Status, v.Rule = "gm", "hitOrMissNote"
			case chargeSentenceRe.MatchString(body):
				v.Status, v.Rule = "applied", "chargeRewrite (creature_movement_2024.go)"
			case strings.HasPrefix(s, "Trigger:"):
				v.Status, v.Rule = "gm", "a Reaction's trigger: the GM sends the Reaction when it happens"
			case specialMoveSentence(body):
				v.Status, v.Rule = "applied", "readSpecialMove / grantSpecialMove"
			case own(s):
				v.Status, v.Rule = "applied", route
			default:
				if p := coveringPattern(s); p != "" {
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
		uncharged, _ := chargeRewrite(e.Text, 0, true, func(string) bool { return true }) // the routes rewrite a charge before the residual
		residual := unappliedHitRider(uncharged) + " " + gmJudgedAlternatives(a.monsterActionRow)
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
	if readSwallow(e.Text) != nil {
		return bySentence("resolveMonsterSwallow", func(string) bool { return true }, "")
	}
	if entryPullRe.MatchString(strings.TrimSpace(e.Text)) {
		return bySentence("resolveMonsterPull (creature_movement_2024.go)", func(string) bool { return true }, "")
	}
	if onlyOwnMove(e.Text) {
		return bySentence("readSpecialMove / grantSpecialMove", func(string) bool { return true }, "")
	}
	if monsterLightToggleRe.MatchString(e.Text) {
		return bySentence("resolveLightToggle", func(string) bool { return true }, "")
	}
	if obscuringTextRe.MatchString(e.Text) {
		return bySentence("resolveObscuringText", func(string) bool { return true }, "")
	}
	if usesAnotherEntry(e.Text) != nil {
		return bySentence("usesAnotherEntry (uses another entry)", func(s string) bool { return usesAnotherEntry(s) != nil }, "")
	}
	for _, n := range sortedPatternNames() {
		if monsterTextPatterns[n].MatchString(e.Text) {
			if p := coveringPattern(e.Text); p != "" {
				return bySentence(p, func(string) bool { return false }, "")
			}
		}
	}
	out := all("gm", "resolveMonsterEffectText: the GM resolves it") // gm-one-shot: the report's label for that fall-through, not a handover (the handover itself is monsterHandoverEntry in resolveMonsterEffectText)
	for i := range out {
		if legendaryOnceRe.MatchString(out[i].Sentence) {
			out[i].Status, out[i].Rule = "applied", "legendaryOnceRe (LAOnce)"
		}
	}
	return out
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

// ---- The report a GM reads (the import preview and the GM's stat block): the verdicts of an entry in the GM's words.

type entryKey struct{ kind, name, text, mechanics string }

var entryReportMemo sync.Map // entryKey -> map[string]interface{}: a verdict depends on the entry's words and attached mechanics only

// entryReport: one entry's verdicts as the GM reads them. applied lists what a rule of the server reads (and which); gm_resolves what the
// GM resolves in play; a sentence in gm_resolves with silent true is one the server neither reads nor mentions in the reply, so the GM must
// remember it.
func entryReport(kind string, e srdMonsterEntry) map[string]interface{} {
	k := entryKey{kind, e.Name, e.Text, mechanicsKey(e)}
	if v, ok := entryReportMemo.Load(k); ok {
		return v.(map[string]interface{})
	}
	applied := []map[string]interface{}{}
	gm := []map[string]interface{}{}
	for _, v := range entryVerdicts(kind, e) {
		switch v.Status {
		case "applied":
			applied = append(applied, map[string]interface{}{"sentence": v.Sentence, "applied_by": v.Rule})
		case "gm":
			gm = append(gm, map[string]interface{}{"sentence": v.Sentence, "why": v.Rule, "silent": false})
		default:
			gm = append(gm, map[string]interface{}{"sentence": v.Sentence, "why": "no rule of the server reads this sentence (" + v.Rule + ")", "silent": true})
		}
	}
	r := map[string]interface{}{"name": e.Name, "kind": kind, "applied": applied, "gm_resolves": gm}
	if mv := mechanicsView(e); len(mv) > 0 {
		r["mechanics"] = mv
	}
	entryReportMemo.Store(k, r)
	return r
}

// entryReports: every entry of a stat block (traits, actions, Bonus Actions, Reactions, Legendary Actions) with its report.
func entryReports(meta *monsterMeta) []map[string]interface{} {
	if meta == nil {
		return nil
	}
	out := []map[string]interface{}{}
	for _, g := range []struct {
		kind    string
		entries []srdMonsterEntry
	}{{"trait", meta.Traits}, {"action", meta.Actions}, {"bonus", meta.BonusActions}, {"reaction", meta.Reactions}, {"legendary", meta.Legendary}} {
		for _, e := range g.entries {
			out = append(out, entryReport(g.kind, e))
		}
	}
	return out
}
