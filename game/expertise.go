package game

import (
	"fmt"
	"sort"
	"strings"
)

// ExpertiseGrant is one class feature that gives Expertise (SRD 5.2.1). ExpertiseGrants is the only place the server says which
// class gets Expertise, at which level, in how many skills and in which skills: creation, the Expertise request, the setup step,
// the level-up notes and the "still to choose" count all read it.
type ExpertiseGrant struct {
	Class       string
	Level       int
	Feature     string   // the class feature's name in the SRD: "Expertise", "Deft Explorer", "Scholar"
	Count       int      // how many skills
	Skills      []string // the only skills it can be taken in; nil: any skill the character is proficient in
	Recommended []string // the SRD's recommendation, where it gives one
}

var ExpertiseGrants = []ExpertiseGrant{
	{Class: "bard", Level: 2, Feature: "Expertise", Count: 2, Recommended: []string{"performance", "persuasion"}},
	{Class: "bard", Level: 9, Feature: "Expertise", Count: 2},
	{Class: "ranger", Level: 2, Feature: "Deft Explorer", Count: 1},
	{Class: "ranger", Level: 9, Feature: "Expertise", Count: 2},
	{Class: "rogue", Level: 1, Feature: "Expertise", Count: 2, Recommended: []string{"sleight of hand", "stealth"}},
	{Class: "rogue", Level: 6, Feature: "Expertise", Count: 2},
	{Class: "wizard", Level: 2, Feature: "Scholar", Count: 1, Skills: []string{"arcana", "history", "investigation", "medicine", "nature", "religion"}},
}

// ExpertiseGrantsFor: the grants a character has, given its level in each class, in the table's order.
func ExpertiseGrantsFor(classLevels map[string]int) []ExpertiseGrant {
	var out []ExpertiseGrant
	for _, g := range ExpertiseGrants {
		for c, l := range classLevels {
			if strings.EqualFold(c, g.Class) && l >= g.Level {
				out = append(out, g)
			}
		}
	}
	return out
}

// ExpertiseCount: how many skills the character's classes give Expertise in.
func ExpertiseCount(classLevels map[string]int) int {
	n := 0
	for _, g := range ExpertiseGrantsFor(classLevels) {
		n += g.Count
	}
	return n
}

// ExpertiseAllowed: how many skills one class gives Expertise in by a level.
func ExpertiseAllowed(class string, level int) int {
	return ExpertiseCount(map[string]int{strings.ToLower(class): level})
}

// ExpertiseGrantsAtLevel: the grants a class gains exactly at a level (for the level-up note and notice).
func ExpertiseGrantsAtLevel(class string, level int) []ExpertiseGrant {
	var out []ExpertiseGrant
	for _, g := range ExpertiseGrants {
		if strings.EqualFold(class, g.Class) && g.Level == level {
			out = append(out, g)
		}
	}
	return out
}

func (g ExpertiseGrant) allows(skill string) bool {
	if g.Skills == nil {
		return true
	}
	for _, s := range g.Skills {
		if s == skill {
			return true
		}
	}
	return false
}

// Describe: what the grant lets the player choose, in words ("Scholar: Expertise in 1 of Arcana, History, Investigation, Medicine,
// Nature or Religion that you are proficient in").
func (g ExpertiseGrant) Describe() string {
	n := fmt.Sprintf("%d skills", g.Count)
	if g.Count == 1 {
		n = "1 skill"
	}
	if g.Skills == nil {
		return fmt.Sprintf("%s: Expertise in %s you are proficient in", g.Feature, n)
	}
	return fmt.Sprintf("%s: Expertise in %d of %s that you are proficient in", g.Feature, g.Count, orList(g.Skills))
}

func orList(skills []string) string {
	t := make([]string, len(skills))
	for i, s := range skills {
		t[i] = TitleSkill(s)
	}
	if len(t) < 2 {
		return strings.Join(t, "")
	}
	return strings.Join(t[:len(t)-1], ", ") + " or " + t[len(t)-1]
}

// TitleSkill: a skill's name as the SRD prints it ("Sleight of Hand").
func TitleSkill(s string) string {
	words := strings.Fields(SkillName(s))
	for i, w := range words {
		if w != "of" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// ExpertiseSummary: which classes get Expertise and when, from the table ("Bard at levels 2 and 9; Ranger at 2 (Deft Explorer)
// and 9; Rogue at 1 and 6; Wizard at 2 (Scholar)").
func ExpertiseSummary() string {
	var classes []string
	by := map[string][]string{}
	for _, g := range ExpertiseGrants {
		if _, ok := by[g.Class]; !ok {
			classes = append(classes, g.Class)
		}
		l := fmt.Sprint(g.Level)
		if g.Feature != "Expertise" {
			l += " (" + g.Feature + ")"
		}
		by[g.Class] = append(by[g.Class], l)
	}
	sort.Strings(classes)
	var parts []string
	for _, c := range classes {
		lv := by[c]
		word := "level "
		if len(lv) > 1 {
			word = "levels "
		}
		parts = append(parts, strings.ToUpper(c[:1])+c[1:]+" at "+word+strings.Join(lv, " and "))
	}
	return strings.Join(parts, "; ")
}

// ExpertiseOptions: the skills the character could take Expertise in: proficient, allowed by one of its grants, and not already
// Expertise from another source. In the SRD's order.
func ExpertiseOptions(classLevels map[string]int, held, other map[string]bool) []string {
	grants := ExpertiseGrantsFor(classLevels)
	var out []string
	for _, s := range Skills {
		if !held[s.Name] || other[s.Name] {
			continue
		}
		for _, g := range grants {
			if g.allows(s.Name) {
				out = append(out, s.Name)
				break
			}
		}
	}
	return out
}

// CheckExpertise checks a character's class Expertise picks against its grants: each a skill, different, one the character is
// proficient in, not one it already has Expertise in from another source (other), and together a set the grants can give (a
// grant with a skill list takes only those). complete also requires every grant to be filled. It returns the picks in their
// stored names, or an error code and a message that says what the character can choose.
func CheckExpertise(classLevels map[string]int, held, other map[string]bool, picks []string, complete bool) (chosen []string, code, msg string) {
	grants := ExpertiseGrantsFor(classLevels)
	total := 0
	var what []string
	for _, g := range grants {
		total += g.Count
		what = append(what, g.Describe())
	}
	if total == 0 {
		return nil, "no_expertise", "Your class gives no Expertise at this level (" + ExpertiseSummary() + ")."
	}
	can := "You can choose: " + strings.Join(what, "; ") + ". Your options: " + strings.Join(ExpertiseOptions(classLevels, held, other), ", ") + "."
	for _, p := range picks {
		k, ok := CanonSkill(p)
		switch {
		case !ok:
			return nil, "invalid_skill", fmt.Sprintf("%q isn't a skill. %s", p, can)
		case containsSkill(chosen, k):
			return nil, "duplicate_skill", fmt.Sprintf("You chose %s twice: choose different skills. %s", TitleSkill(k), can)
		case !held[k]:
			return nil, "not_proficient", fmt.Sprintf("Expertise needs a skill you are proficient in; %q isn't one. %s", p, can)
		case other[k]:
			return nil, "already_expertise", fmt.Sprintf("You already have Expertise in %s from another source; a skill can't have it twice. %s", TitleSkill(k), can)
		}
		chosen = append(chosen, k)
	}
	if len(chosen) > total || complete && len(chosen) != total {
		return nil, "invalid_count", fmt.Sprintf("You have Expertise in %d skill(s) at this level; you sent %d. Send all %d (the list replaces your choices). %s", total, len(chosen), total, can)
	}
	if bad := unplacedExpertise(grants, chosen); bad != "" {
		return nil, "skill_not_allowed", fmt.Sprintf("%s can't take one of your Expertise picks as chosen. %s", TitleSkill(bad), can)
	}
	return chosen, "", ""
}

func containsSkill(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// unplacedExpertise matches each pick to a slot of a grant that allows it; "" when every pick has its own slot, else a pick
// that has none.
func unplacedExpertise(grants []ExpertiseGrant, chosen []string) string {
	var slots []ExpertiseGrant
	for _, g := range grants {
		for i := 0; i < g.Count; i++ {
			slots = append(slots, g)
		}
	}
	owner := make([]int, len(slots)) // which pick holds each slot
	for i := range owner {
		owner[i] = -1
	}
	var place func(p int, seen []bool) bool
	place = func(p int, seen []bool) bool {
		for s, g := range slots {
			if seen[s] || !g.allows(chosen[p]) {
				continue
			}
			seen[s] = true
			if owner[s] < 0 || place(owner[s], seen) {
				owner[s] = p
				return true
			}
		}
		return false
	}
	// picks with the fewest slots first, so the message names the pick that is really the problem
	for p := range chosen {
		if !place(p, make([]bool, len(slots))) {
			return chosen[p]
		}
	}
	return ""
}

// SuggestExpertise fills the character's Expertise: the picks it has (those still valid), then each grant's SRD recommendation,
// then prefs, then any option, never a set its grants can't give.
func SuggestExpertise(classLevels map[string]int, held, other map[string]bool, current, prefs []string) []string {
	total := ExpertiseCount(classLevels)
	var out []string
	try := func(s string) {
		k, ok := CanonSkill(s)
		if !ok || len(out) >= total || containsSkill(out, k) {
			return
		}
		if _, code, _ := CheckExpertise(classLevels, held, other, append(append([]string{}, out...), k), false); code == "" {
			out = append(out, k)
		}
	}
	for _, s := range current {
		try(s)
	}
	// a grant with its own skill list first: its picks can't go anywhere else
	for _, g := range ExpertiseGrantsFor(classLevels) {
		if g.Skills != nil {
			n := 0
			for _, s := range out {
				if g.allows(s) {
					n++
				}
			}
			for _, s := range g.Skills {
				if n < g.Count && g.allows(s) && held[s] && !containsSkill(out, s) {
					before := len(out)
					try(s)
					n += len(out) - before
				}
			}
		}
	}
	for _, g := range ExpertiseGrantsFor(classLevels) {
		for _, s := range g.Recommended {
			try(s)
		}
	}
	for _, s := range prefs {
		try(s)
	}
	for _, s := range ExpertiseOptions(classLevels, held, other) {
		try(s)
	}
	return out
}
