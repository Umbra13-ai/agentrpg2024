package main

// d20_face_2024.go: the face of the d20 a roll line shows (owner, Nov 28: "whatever d20 value is rolled, it shows a die with that value").
// One reader, naturalD20, reads the die that counted from every form the server writes it in (game.AttackRoll and game.SavingThrow's
// "d20 12" and "d20s 12 and 7: 12", the checks' "d20: 12", the older "d20(12)" and "d20(12,7→12)"); d20FaceURL gives that face's picture
// (data/site_assets/d20-<n>.webp) when the site has it. A line that holds no d20, several (a Multiattack), or a die it can't read for
// certain (a Lucky reroll, a floor, an Influence check's three) shows the plain d20 (/assets/d20.webp): never a guessed face.

import (
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
)

// d20Forms: each way a line names the d20 that counted; the last group is that die.
var d20Forms = []*regexp.Regexp{
	regexp.MustCompile(`\bd20s \d{1,2} and \d{1,2}(?: with (?:Advantage|Disadvantage) \([^)]*\))?: (\d{1,2})\b`), // d20s 12 and 7: 12 / d20s 12 and 7 with Advantage (Help): 12
	regexp.MustCompile(`\bd20\((?:\d{1,2},\d{1,2}→)?(\d{1,2})\)`),                                                // d20(12) / d20(12,7→12)
	regexp.MustCompile(`\bd20:? (\d{1,2})\b`),                                                                    // d20 12 / d20: 12
}

// d20Unclear: forms whose face is not one plain die (a reroll, a floor, several d20s listed with no kept one).
var d20Unclear = regexp.MustCompile(`\bd20\([^)]*(Lucky|→10\))|\bd20s: `)

var d20Mention = regexp.MustCompile(`\bd20s?\b[ :(]`)

// naturalD20: the d20 that counted in a roll line, when the line holds exactly one d20 roll and says which die counted.
func naturalD20(line string) (int, bool) {
	if d20Unclear.MatchString(line) || len(d20Mention.FindAllStringIndex(line, -1)) != 1 {
		return 0, false
	}
	for _, re := range d20Forms {
		if m := re.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			if n >= 1 && n <= 20 {
				return n, true
			}
			return 0, false
		}
	}
	return 0, false
}

// d20FaceURL: the picture of the face a roll line's d20 shows, "" when the line names no single die or the site has no picture of it.
func d20FaceURL(line string) string {
	n, ok := naturalD20(line)
	if !ok {
		return ""
	}
	name := fmt.Sprintf("d20-%d.webp", n)
	if _, err := fs.Stat(siteAssets, "data/site_assets/"+name); err != nil {
		return ""
	}
	return "/assets/" + name
}
