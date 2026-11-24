package main

// A stat block pasted as text, read into the structured monster an import takes (owner, Nov 22). It reads the layout only: the name, the
// size, type and alignment line, AC, Initiative, HP and Speed, the six abilities with their modifiers and saving throws, the labelled
// lines (Skills, Resistances, Vulnerabilities, Immunities, Gear, Senses, Languages, CR), the section headings and each entry's
// "Name. Text". What an entry says is not read here: that is the verdict of monster_entry_verdicts_2024.go, which the preview gives
// next. The layout is the one the SRD 5.2.1 prints (TestStatBlockTextRoundTrip writes each of its 330 stat blocks out in it and reads
// them back). The reader returns the monster and what it left out or changed, for the GM to confirm.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

var (
	textACRe        = regexp.MustCompile(`\bAC (\d+)`)
	textInitRe      = regexp.MustCompile(`Initiative ([+-]?\d+) \((\d+)\)`)
	textHPRe        = regexp.MustCompile(`\bHP (\d+)(?: \(([^)]*)\))?`)
	textSpeedRe     = regexp.MustCompile(`Speed (.*)$`)
	textAbilityRe   = regexp.MustCompile(`\b(Str|Dex|Con|Int|Wis|Cha)\s+(\d+)(?:\s+([+-]\d+)\s+([+-]\d+))?`)
	textGridHeadRe  = regexp.MustCompile(`^(?i:MOD|SAVE)\b`)
	textLabelRe     = regexp.MustCompile(`^(Skills|Resistances|Vulnerabilities|Immunities|Gear|Senses|Languages|CR)\b\s*(.*)$`)
	textCRRe        = regexp.MustCompile(`^([\d/]+) \((?:XP )?([\d,]+)(?: XP)?(?:, or ([\d,]+)(?: XP)? in lair)?; PB \+(\d+)\)`)
	textSectionRe   = regexp.MustCompile(`^(Traits|Actions|Bonus Actions|Reactions|Legendary Actions)\s*:?$`)
	textSizeRe      = regexp.MustCompile(`^((?:Tiny|Small|Medium|Large|Huge|Gargantuan)(?: or (?:Tiny|Small|Medium|Large|Huge|Gargantuan))*)\s+(.*)$`)
	textEntryHeadRe = regexp.MustCompile(`^([^.:\n]{1,150}?)\.\s+(\S.*)$`)
	textEntryDotRe  = regexp.MustCompile(`^([^.:\n]{1,150}?)\.$`)
	textParenRe     = regexp.MustCompile(`\([^)]*\)`)
)

// textNameSmallWords: the words of an entry's name that are not capitalized ("Swarm of Grasping Hands").
var textNameSmallWords = map[string]bool{"of": true, "the": true, "and": true, "or": true, "in": true, "a": true, "an": true, "to": true, "for": true, "with": true, "on": true, "at": true, "as": true}

// looksLikeEntryName: an entry's name is Title Case words, with the parenthesis ("(Recharge 5-6)", "(3/Day, or 4/Day in Lair)") set aside;
// it holds no colon, so "Failure: The target is cursed." is a sentence of an entry, not a new one.
func looksLikeEntryName(s string) bool {
	s = strings.TrimSpace(textParenRe.ReplaceAllString(s, ""))
	if s == "" {
		return false
	}
	for _, w := range strings.Fields(s) {
		r := []rune(w)[0]
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || textNameSmallWords[strings.ToLower(w)] {
			continue
		}
		return false
	}
	return true
}

// splitTypeLine: "Large Aberration, Lawful Evil" -> size, type (with its tags), alignment; the comma that splits is the first outside
// parentheses ("Humanoid (Elf, Half-Elf), Neutral").
func splitTypeLine(line string) (size, typ, align string) {
	depth, cut := 0, -1
	for i, r := range line {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 && cut < 0 {
				cut = i
			}
		}
	}
	head := strings.TrimSpace(line)
	if cut >= 0 {
		head, align = strings.TrimSpace(line[:cut]), strings.TrimSpace(line[cut+1:])
	}
	if m := textSizeRe.FindStringSubmatch(head); m != nil {
		return m[1], strings.TrimSpace(m[2]), align
	}
	return "", head, align
}

// onlyConditionNames: whether every item of a comma list is a rules condition (an Immunities line without a ";").
func onlyConditionNames(s string) bool {
	items := 0
	for _, p := range strings.Split(s, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		items++
		if !isRuleCondition(p) && p != "exhaustion" {
			return false
		}
	}
	return items > 0
}

func textInt(s string) int {
	n, _ := strconv.Atoi(strings.ReplaceAll(strings.TrimPrefix(s, "+"), ",", ""))
	return n
}

// readStatBlockText reads one stat block. The notes say what it could not read or changed; the monster still has to pass the import's
// own validation.
func readStatBlockText(text string) (srdMonster2024, []string) {
	var m srdMonster2024
	var notes []string
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		lines = append(lines, strings.TrimSpace(l))
	}
	i := 0
	next := func() (string, bool) {
		for i < len(lines) && lines[i] == "" {
			i++
		}
		if i >= len(lines) {
			return "", false
		}
		l := lines[i]
		i++
		return l, true
	}
	var ok bool
	if m.Name, ok = next(); !ok {
		return m, []string{"The text is empty."}
	}
	line, _ := next()
	size, typ, align := splitTypeLine(line)
	if size == "" {
		notes = append(notes, fmt.Sprintf("The line under the name (%q) does not start with a size (Tiny to Gargantuan), so size is empty.", line))
	}
	if first, _, or := strings.Cut(size, " or "); or {
		notes = append(notes, fmt.Sprintf("The size %q is two sizes: used %s.", size, first))
		size = first
	}
	m.Size, m.Type, m.Alignment = size, typ, align

	// AC, Initiative, HP and Speed, possibly on one line or wrapped: read together
	var header []string
	for i < len(lines) {
		l := lines[i]
		if l != "" && (strings.HasPrefix(l, "Str") && textAbilityRe.MatchString(l) || textGridHeadRe.MatchString(l) || textLabelRe.MatchString(l) || textSectionRe.MatchString(l)) {
			break
		}
		if l != "" {
			header = append(header, l)
		}
		i++
	}
	h := strings.ReplaceAll(strings.Join(header, " "), "\u2212", "-") // a printed minus sign is a hyphen in a number
	if x := textACRe.FindStringSubmatch(h); x != nil {
		m.AC = textInt(x[1])
	} else {
		notes = append(notes, "No AC found (write \"AC 17\").")
	}
	if x := textInitRe.FindStringSubmatch(h); x != nil {
		m.Initiative.Mod, m.Initiative.Score = textInt(x[1]), textInt(x[2])
	}
	if x := textHPRe.FindStringSubmatch(h); x != nil {
		m.HP, m.HPFormula = textInt(x[1]), x[2]
	} else {
		notes = append(notes, "No HP found (write \"HP 150 (20d10 + 40)\").")
	}
	if x := textSpeedRe.FindStringSubmatch(h); x != nil {
		m.Speed = strings.TrimSpace(x[1])
	}

	// the six abilities, until the first labelled line or section
	m.Abilities = map[string]abilityLine{}
	for i < len(lines) {
		l := lines[i]
		if textLabelRe.MatchString(l) || textSectionRe.MatchString(l) {
			break
		}
		i++
		if textGridHeadRe.MatchString(l) {
			continue
		}
		for _, x := range textAbilityRe.FindAllStringSubmatch(strings.ReplaceAll(l, "\u2212", "-"), -1) {
			a := abilityLine{Score: textInt(x[2])}
			a.Mod = game.Modifier(a.Score)
			if x[3] != "" {
				a.Mod, a.Save, a.saveGiven = textInt(x[3]), textInt(x[4]), true
			}
			m.Abilities[strings.ToLower(x[1])] = a
		}
	}
	for _, k := range abilityOrder {
		if _, has := m.Abilities[k]; !has {
			notes = append(notes, fmt.Sprintf("No %s score found (write \"%s 21 +5 +5\": score, modifier, saving throw).", strings.ToUpper(k[:1])+k[1:], strings.ToUpper(k[:1])+k[1:]))
		}
	}

	// the labelled lines
	labelled := map[string]string{}
	cur := ""
	for i < len(lines) {
		l := lines[i]
		if textSectionRe.MatchString(l) {
			break
		}
		i++
		if l == "" {
			continue
		}
		if x := textLabelRe.FindStringSubmatch(l); x != nil {
			cur = x[1]
			labelled[cur] = x[2]
		} else if cur != "" {
			labelled[cur] += " " + l
		}
	}
	for k, v := range labelled {
		labelled[k] = strings.Join(strings.Fields(v), " ")
	}
	m.Skills, m.Vulnerable, m.Resistances = labelled["Skills"], labelled["Vulnerabilities"], labelled["Resistances"]
	if imm := labelled["Immunities"]; strings.Contains(imm, ";") {
		d, c, _ := strings.Cut(imm, ";")
		m.DmgImmune, m.CondImmune = strings.TrimSpace(d), strings.TrimSpace(c)
	} else if onlyConditionNames(imm) {
		m.CondImmune = imm
	} else {
		m.DmgImmune = imm
	}
	m.Gear, m.Senses, m.Languages = labelled["Gear"], labelled["Senses"], labelled["Languages"]
	if cr := labelled["CR"]; textCRRe.MatchString(cr) {
		x := textCRRe.FindStringSubmatch(cr)
		m.CR, m.XP, m.PB = x[1], textInt(x[2]), textInt(x[4])
		if x[3] != "" {
			m.XPLair = textInt(x[3])
		}
	} else {
		m.CR = cr
		notes = append(notes, fmt.Sprintf("The CR line %q is not read (write \"CR 10 (XP 5,900, or 7,200 in lair; PB +4)\").", cr))
	}

	// the sections and their entries
	section := ""
	var entry *srdMonsterEntry
	var group *[]srdMonsterEntry
	prevEnds := false // the last line of the entry ended a sentence
	groups := map[string]*[]srdMonsterEntry{"Traits": &m.Traits, "Actions": &m.Actions, "Bonus Actions": &m.BonusActions, "Reactions": &m.Reactions, "Legendary Actions": &m.Legendary}
	for ; i < len(lines); i++ {
		l := lines[i]
		if l == "" {
			continue
		}
		if x := textSectionRe.FindStringSubmatch(l); x != nil {
			section, group, entry = x[1], groups[x[1]], nil
			continue
		}
		if group == nil {
			notes = append(notes, fmt.Sprintf("Text before the first section was not read: %q.", l))
			continue
		}
		name, rest := "", ""
		if x := textEntryHeadRe.FindStringSubmatch(l); x != nil && looksLikeEntryName(x[1]) {
			name, rest = x[1], x[2]
		} else if x := textEntryDotRe.FindStringSubmatch(l); x != nil && looksLikeEntryName(x[1]) {
			name = x[1]
		}
		switch {
		case name != "":
			*group = append(*group, srdMonsterEntry{Name: name, Text: rest})
			entry = &(*group)[len(*group)-1]
		case entry != nil:
			sep := " "
			if prevEnds {
				sep = "\n"
			}
			entry.Text += sep + l
		case section == "Legendary Actions":
			m.LegendaryIn = strings.TrimSpace(m.LegendaryIn + " " + l)
		default:
			notes = append(notes, fmt.Sprintf("Text in %s before its first entry was not read: %q.", section, l))
		}
		last := l[len(l)-1]
		prevEnds = last == '.' || last == '!' || last == '?'
		if entry != nil && group != nil && len(*group) > 0 {
			entry = &(*group)[len(*group)-1] // append may have moved the slice
		}
	}
	return m, notes
}
