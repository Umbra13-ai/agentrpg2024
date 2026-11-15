package game

import "strings"

// Key folds a name a player sends to the one form every option is compared in, so a displayed name and its slug are the
// same answer: lowercase; apostrophes and brackets dropped; spaces, hyphens, underscores and "/" all separate words, joined
// with "_". "Hunter’s Mark", "hunters-mark" and "Hunters_Mark" are "hunters_mark"; "Blindness/Deafness" and
// "blindness-deafness" are "blindness_deafness". This is the only fold for player answers in the server: every lookup of a
// spell, a feature, an option, a subclass or a field name goes through it (or through Match, which uses it).
func Key(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("'", "", "’", "", "‘", "", "(", "", ")", "").Replace(s)
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '-' || r == '_' || r == '/' || r == '\t' || r == '\n'
	}), "_")
}

// Slug: Key with hyphens, the form spell, monster and item slugs are stored in ("Hunter’s Mark" -> "hunters-mark").
func Slug(s string) string { return strings.ReplaceAll(Key(s), "_", "-") }

// Option is one answer a choice accepts: its slug (what is stored), the name it is shown with, and other names that mean it.
type Option struct {
	Slug    string
	Name    string
	Aliases []string
}

// Match finds the option an answer means: its slug, its name or an alias, in any spelling Key folds together.
func Match(answer string, opts []Option) (Option, bool) {
	k := Key(answer)
	if k == "" {
		return Option{}, false
	}
	for _, o := range opts {
		if Key(o.Slug) == k || (o.Name != "" && Key(o.Name) == k) {
			return o, true
		}
		for _, a := range o.Aliases {
			if Key(a) == k {
				return o, true
			}
		}
	}
	return Option{}, false
}

// MatchString: Match over plain slugs ("protector", "colossus_slayer"), each also known by its slug's words.
func MatchString(answer string, slugs []string) (string, bool) {
	k := Key(answer)
	for _, s := range slugs {
		if k != "" && Key(s) == k {
			return s, true
		}
	}
	return "", false
}

// Abilities: the six ability scores, short name and full name, in the SRD's order. AbilityKey reads a player's answer
// against it.
var Abilities = []Option{
	{Slug: "str", Name: "Strength"}, {Slug: "dex", Name: "Dexterity"}, {Slug: "con", Name: "Constitution"},
	{Slug: "int", Name: "Intelligence", Aliases: []string{"intl"}}, {Slug: "wis", Name: "Wisdom"}, {Slug: "cha", Name: "Charisma"},
}

// AbilityKey: the short name of the ability an answer means ("Strength", "STR", "str" -> "str"); false when it is none.
func AbilityKey(s string) (string, bool) {
	o, ok := Match(s, Abilities)
	return o.Slug, ok
}
