package main

// A stat block entry that lets the monster swap part of it for something else (SRD 5.2.1): "It can replace one attack with a use of
// (A) Sleep Breath or (B) Spellcasting to cast Scorching Ray", "It can replace one Fireball with Hold Monster (level 7 version) or
// Wall of Fire", "It can replace the Claw attack with a use of Fire Breath if available". replaceRuleOf reads that sentence once for
// every entry; the GM picks an option with monster_action's "use" (the option's name: "Sleep Breath", "Scorching Ray",
// "Hold Monster", "Tail"); the GM's stat block lists each entry's options (replaceChoicesView); a "use" that names neither an option
// nor what it replaces is refused before anything is rolled (replaceRefusal); a Multiattack with an option makes its attacks less
// the replaced ones and then the option (resolveMultiattackReplacement), and an entry that casts a spell several times casts the
// option once in place of one copy (the spell branch of resolveMonsterEffectText). Playtest I-GM-20/21 (feedback log 69): the GM
// asked for Hold Monster in the description, the server couldn't read the choice, and two Fireballs were cast.

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

// replaceRule: what an entry's replacement sentence says.
type replaceRule struct {
	Replaced string // the part replaced: an attack's name ("Claw"), a spell ("Fireball"), or "" for any attack
	Count    int    // how many of it ("two attacks": 2)
	Options  []replaceOption
}

// replaceOption: one thing the monster may do instead.
type replaceOption struct {
	Name  string // what "use" names: the spell, else the entry
	Entry string // the stat block entry used ("Spellcasting", "Sleep Breath", "Tail"); "" for a spell cast directly
	Spell string // the spell cast, if any
	Level int    // "(level 3 version)"
}

var (
	replaceSentenceRe = regexp.MustCompile(`(?i)\bcan replace (one|two|three|any|the) (.+?) with (.+?)(?: if available)?\.`)
	replaceMarkerRe   = regexp.MustCompile(`\([A-Z]\)\s*`)
	replaceLevelRe    = regexp.MustCompile(`\s*\(level (\d) version\)`)
)

// replaceRuleOf reads an entry's replacement sentence; ok is false when it has none.
func replaceRuleOf(text string) (replaceRule, bool) {
	m := replaceSentenceRe.FindStringSubmatch(normQuotes(text))
	if m == nil {
		return replaceRule{}, false
	}
	rule := replaceRule{Count: 1}
	if n, ok := countWords[strings.ToLower(m[1])]; ok {
		rule.Count = n
	}
	replaced := strings.TrimSpace(m[2])
	for _, suffix := range []string{" attacks", " attack"} {
		replaced = strings.TrimSuffix(replaced, suffix)
	}
	if !strings.EqualFold(replaced, "attack") && !strings.EqualFold(replaced, "attacks") {
		rule.Replaced = replaced
	}
	for _, part := range strings.Split(replaceMarkerRe.ReplaceAllString(m[3], ""), " or ") {
		o := strings.TrimSpace(part)
		for _, prefix := range []string{"a use of ", "a ", "an "} {
			if strings.HasPrefix(strings.ToLower(o), prefix) {
				o = o[len(prefix):]
				break
			}
		}
		var opt replaceOption
		if lv := replaceLevelRe.FindStringSubmatch(o); lv != nil {
			opt.Level, _ = strconv.Atoi(lv[1])
			o = replaceLevelRe.ReplaceAllString(o, "")
		}
		if i := strings.Index(o, " to cast "); i >= 0 {
			opt.Entry, opt.Spell = o[:i], o[i+len(" to cast "):]
		} else if strings.HasSuffix(o, " attack") {
			opt.Entry = strings.TrimSuffix(o, " attack")
		} else if _, isSpell := srdSpellsMemory[normalizeSpellSlug(o)]; isSpell {
			opt.Spell = o
		} else {
			opt.Entry = o
		}
		opt.Name = opt.Spell
		if opt.Name == "" {
			opt.Name = opt.Entry
		}
		if opt.Name != "" {
			rule.Options = append(rule.Options, opt)
		}
	}
	return rule, len(rule.Options) > 0
}

// option: the option "use" names (by its name, its entry or its spell, in any spelling).
func (rule replaceRule) option(use string) (replaceOption, bool) {
	k := game.Key(use)
	for _, o := range rule.Options {
		if k == game.Key(o.Name) || (o.Spell != "" && k == game.Key(o.Spell)) || (o.Entry != "" && k == game.Key(o.Entry)) {
			return o, true
		}
	}
	return replaceOption{}, false
}

// names: the options' names, for the GM.
func (rule replaceRule) names() []string {
	var out []string
	for _, o := range rule.Options {
		out = append(out, o.Name)
	}
	return out
}

// replaceRefusal: the words refusing a "use" on an entry with a replacement sentence that names neither one of its options nor
// what it replaces ("" when the use is fine or the entry has no such sentence).
func replaceRefusal(entry srdMonsterEntry, use string) string {
	rule, ok := replaceRuleOf(entry.Text)
	if !ok || strings.TrimSpace(use) == "" {
		return ""
	}
	if _, ok := rule.option(use); ok {
		return ""
	}
	if rule.Replaced != "" && game.Key(use) == game.Key(rule.Replaced) {
		return ""
	}
	return fmt.Sprintf("%s: use %q is not one of its choices. It can replace %s with %s (send \"use\": one of them), or leave use out. Nothing was rolled.",
		stripUsage(entry.Name), use, replacedWords(rule), strings.Join(quoteAll(rule.names()), " or "))
}

func replacedWords(rule replaceRule) string {
	what := "attack"
	if rule.Replaced != "" {
		what = rule.Replaced
	}
	if rule.Count > 1 {
		return fmt.Sprintf("%d %ss", rule.Count, what)
	}
	return "one " + what
}

func quoteAll(s []string) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = strconv.Quote(x)
	}
	return out
}

// replaceChoicesView: each entry of the stat block with a replacement sentence, as the GM's stat block shows it.
func replaceChoicesView(meta *monsterMeta) []map[string]interface{} {
	if meta == nil {
		return nil
	}
	var out []map[string]interface{}
	for _, group := range [][]srdMonsterEntry{meta.Actions, meta.BonusActions, meta.Reactions, meta.Legendary} {
		for _, e := range group {
			rule, ok := replaceRuleOf(e.Text)
			if !ok {
				continue
			}
			out = append(out, map[string]interface{}{
				"action":   e.Name,
				"replaces": replacedWords(rule),
				"choices":  rule.names(),
				"how":      fmt.Sprintf(`monster_action {"action": %q, "use": %q}: the option in place of %s`, stripUsage(e.Name), rule.Options[0].Name, replacedWords(rule)),
			})
		}
	}
	return out
}

// multiattackReplacing: while a Multiattack with a replacement option resolves, the attacks it gives up (campaign -> replaceRule;
// read by resolveMonsterAttack's Multiattack plan, the same pattern as monsterAreaPoint).
var multiattackReplacing sync.Map

// withoutReplaced: a Multiattack's plan less the attacks the option replaces (the named attack, else the last ones).
func withoutReplaced(campaignID int, plan []monsterActionRow) []monsterActionRow {
	v, ok := multiattackReplacing.Load(campaignID)
	if !ok {
		return plan
	}
	rule := v.(replaceRule)
	out := append([]monsterActionRow{}, plan...)
	for n := 0; n < rule.Count && len(out) > 0; n++ {
		drop := len(out) - 1
		if rule.Replaced != "" {
			drop = -1
			for i := len(out) - 1; i >= 0; i-- {
				if strings.EqualFold(stripUsage(out[i].Name), rule.Replaced) {
					drop = i
					break
				}
			}
			if drop < 0 {
				break
			}
		}
		out = append(out[:drop], out[drop+1:]...)
	}
	return out
}

// resolveMultiattackReplacement: a Multiattack whose "use" names one of its options: the attacks less the replaced ones, then the
// option (an attack of the stat block, or another entry with the spell it casts). refusal: the option can't be taken (not
// recharged ...), nothing rolled.
func resolveMultiattackReplacement(r *http.Request, lobbyID, id int, meta *monsterMeta, monster string, rule replaceRule, o replaceOption,
	target string, targetIDs []int, apply bool) (string, string) {
	optEntry := o.Entry
	if optEntry == "" { // a spell named directly: the stat block's Spellcasting casts it
		optEntry = "Spellcasting"
	}
	entry, _, ok := findMonsterEntry(meta, optEntry)
	if !ok {
		return "", fmt.Sprintf("%s has no %s to replace an attack with. Nothing was rolled.", creatureName(lobbyID, id), optEntry)
	}
	if why := monsterEntryRefusal(lobbyID, id, entry); why != "" {
		return "", why
	}
	multiattackReplacing.Store(lobbyID, rule)
	text, _, ok := resolveMonsterAttack(r, lobbyID, monster, "Multiattack", target, apply)
	multiattackReplacing.Delete(lobbyID)
	if !ok {
		text = fmt.Sprintf("%s makes its other attacks (the GM rolls them).", creatureName(lobbyID, id))
	}
	lines := []string{text, fmt.Sprintf("In place of %s: %s.", replacedWords(rule), o.Name)}
	if isAttackEntry(entry.Text) {
		t, _, ok := resolveMonsterAttack(r, lobbyID, monster, entry.Name, target, apply)
		if !ok {
			t = fmt.Sprintf("%s makes one %s attack (the GM rolls it).", creatureName(lobbyID, id), stripUsage(entry.Name))
		}
		lines = append(lines, t)
	} else {
		t, _, why := resolveMonsterActionByName(r, lobbyID, monster, entry.Name, target, targetIDs, apply, o.Spell)
		if why != "" {
			t = why
		}
		lines = append(lines, t)
	}
	return strings.Join(lines, " "), ""
}
