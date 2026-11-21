package main

// What players may read of a result: hit or miss, damage, conditions, success or failure, but not a monster's numbers.
// The stored text keeps them for the GM; every log, feed and action reply a player or spectator reads goes through playerText.
//	"Greatclub: 11 vs AC 18 - MISS"                     -> "Greatclub: MISS"          (a monster's attack total)
//	"Shortbow: 9 with Disadvantage (long range) vs AC 16 - MISS" -> "Shortbow: with Disadvantage (long range) - MISS"
//	"Goblin 1 fails its DEX save (3 vs DC 18)"          -> "Goblin 1 fails its DEX save"
//	"Aria fails the Dexterity saving throw (12 vs DC 15; d20 9 + 3) [Bless +2]" -> "Aria fails the Dexterity saving throw [Bless +2]"
//	"Constitution save 15 vs DC 14, success"            -> "Constitution save, success"
//	"repeats the Wisdom saving throw ... and fails (2)" -> "... and fails"
//	"tries to escape: 15 (d20 13 + 2) vs DC 17, ..."    -> "tries to escape: ..."

import (
	"regexp"
	"strings"
)

// gmNoteRe: a bracketed note the server writes for the GM inside a result ("[GM, not applied automatically: ...]", "[GM: ...]",
// "[GM, not automated: ...]"). It can carry stat-block text the players must not read (a rider's wording, an alternative damage
// condition), so playerText removes it and the GM keeps it in the notices (gmNotesIn). Notes do not nest brackets.
var gmNoteRe = regexp.MustCompile(`\s?\[GM[,:][^\]]*\]`)

// splitGMNotes separates a result into the text the shared log may keep and the GM's private notes. The result's owner calls it
// BEFORE anything is stored in the shared log (the stored text is what every reader sees); playerText stays as defense in depth.
func splitGMNotes(s string) (public string, notes []string) {
	return strings.TrimSpace(gmNoteRe.ReplaceAllString(s, "")), gmNotesIn(s)
}

// gmNotesIn lists the GM notes inside a result, without their brackets.
func gmNotesIn(s string) []string {
	var out []string
	for _, m := range gmNoteRe.FindAllString(s, -1) {
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(m), "["), "]"))
	}
	return out
}

var playerTextRules = []struct {
	re   *regexp.Regexp
	repl string
}{
	// an attack roll's result line as game.AttackRoll.Line writes it (game/attack.go; change the two together):
	// "<total>[ with Advantage (why)| with Disadvantage (why)| (Advantage from .. cancel: one d20)][ [Bane -2]] vs AC <ac> - "
	{regexp.MustCompile(`(:\s)-?\d+((?: with (?:Advantage|Disadvantage))?(?: \([^)]*\))*(?: \[[^\]]*\])*) vs AC \d+ - `), "$1$2 - "},
	{regexp.MustCompile(`(:\s) - `), "$1"},
	// a monster attack without a known target: "Scimitar: 14 to hit - ..."
	{regexp.MustCompile(`(:\s)-?\d+((?: with (?:Advantage|Disadvantage))?(?: \([^)]*\))*(?: \[[^\]]*\])*) to hit - `), "$1$2 "},
	// saving throws against a DC; the first is the result line as game.SaveResult.Tail writes it (game/save.go; change the two
	// together): " (<total> vs DC <dc>; d20 <n> + <bonus>)"
	{regexp.MustCompile(` \(-?\d+ vs DC \d+(?:; d20[^)]*)?\)`), ""},
	{regexp.MustCompile(`((?:save|saving throw)):? -?\d+ vs DC \d+`), "$1"},
	{regexp.MustCompile(`: -?\d+ \(d20 -?\d+ \+ -?\d+\) vs DC \d+, `), ": "},
	{regexp.MustCompile(` and (succeeds|fails) \(-?\d+\)`), " and $1"},
	{regexp.MustCompile(`(succeeds|fails|saves) \(-?\d+\)`), "$1"},
	{regexp.MustCompile(`: -?\d+ vs DC \d+, `), ": "},
	{regexp.MustCompile(`: {2,}`), ": "},
}

// playerText removes monster attack totals, Armor Class numbers and saving throw totals from a result.
func playerText(s string) string {
	s = gmNoteRe.ReplaceAllString(s, "")
	for _, r := range playerTextRules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}
