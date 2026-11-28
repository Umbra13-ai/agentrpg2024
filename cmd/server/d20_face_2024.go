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

// d20Twos: the forms of a roll of two d20s (Advantage or Disadvantage) and the die that counted: "d20s 12 and 7: 12", "d20s 12 and 7
// with Advantage (Help): 12", "d20(12,7→12)", and "Advantage (Help), d20s 12 and 7", where the kept die follows from the kind.
var (
	d20TwoKept   = regexp.MustCompile(`\bd20s (\d{1,2}) and (\d{1,2})(?: with (?:Advantage|Disadvantage) \([^)]*\))?: (\d{1,2})\b`)
	d20TwoArrow  = regexp.MustCompile(`\bd20\((\d{1,2}),(\d{1,2})→(\d{1,2})\)`)
	d20TwoByKind = regexp.MustCompile(`\b(Advantage|Disadvantage) \([^)]*\), d20s (\d{1,2}) and (\d{1,2})\b`)
)

// d20Ones: the forms of a roll of one d20: "d20(12)", "d20 12", "d20: 12".
var d20Ones = []*regexp.Regexp{
	regexp.MustCompile(`\bd20\((\d{1,2})\)`),
	regexp.MustCompile(`\bd20:? (\d{1,2})\b`),
}

// d20Unclear: forms whose dice are not plain (a reroll, a floor, several d20s listed with no kept one).
var d20Unclear = regexp.MustCompile(`\bd20\([^)]*(Lucky|→10\))|\bd20s: `)

var d20Mention = regexp.MustCompile(`\bd20s?\b[ :(]`)

// rolledD20s is the one reader of a roll line's d20s: the dice in the line's order and which one counted, when the line holds exactly
// one d20 roll (one die, or two for Advantage or Disadvantage) and says, or its kind shows, which die counted.
func rolledD20s(line string) (dice []int, kept int, ok bool) {
	if d20Unclear.MatchString(line) || len(d20Mention.FindAllStringIndex(line, -1)) != 1 {
		return nil, 0, false
	}
	num := func(s string) int { n, _ := strconv.Atoi(s); return n }
	two := func(a, b, k int) ([]int, int, bool) {
		for i, d := range []int{a, b} {
			if d == k && d >= 1 && d <= 20 && a >= 1 && a <= 20 && b >= 1 && b <= 20 {
				return []int{a, b}, i, true
			}
		}
		return nil, 0, false
	}
	if m := d20TwoKept.FindStringSubmatch(line); m != nil {
		return two(num(m[1]), num(m[2]), num(m[3]))
	}
	if m := d20TwoArrow.FindStringSubmatch(line); m != nil {
		return two(num(m[1]), num(m[2]), num(m[3]))
	}
	if m := d20TwoByKind.FindStringSubmatch(line); m != nil {
		a, b := num(m[2]), num(m[3])
		k := max(a, b)
		if m[1] == "Disadvantage" {
			k = min(a, b)
		}
		return two(a, b, k)
	}
	for _, re := range d20Ones {
		if m := re.FindStringSubmatch(line); m != nil {
			if n := num(m[1]); n >= 1 && n <= 20 {
				return []int{n}, 0, true
			}
			return nil, 0, false
		}
	}
	return nil, 0, false
}

// naturalD20: the d20 that counted in a roll line.
func naturalD20(line string) (int, bool) {
	dice, kept, ok := rolledD20s(line)
	if !ok {
		return 0, false
	}
	return dice[kept], true
}

// d20Die is one die the roll box shows: its face's picture and whether it is the one that counted.
type d20Die struct {
	Src  string `json:"src"`
	Kept bool   `json:"kept"`
}

// d20Dice: the dice a roll line's box shows, in the line's order (both for Advantage or Disadvantage, the kept one marked); none when the
// line names no single roll or the site lacks a face's picture (the box then shows the plain d20).
func d20Dice(line string) []d20Die {
	dice, kept, ok := rolledD20s(line)
	if !ok {
		return nil
	}
	var out []d20Die
	for i, n := range dice {
		name := fmt.Sprintf("d20-%d.webp", n)
		if _, err := fs.Stat(siteAssets, "data/site_assets/"+name); err != nil {
			return nil
		}
		out = append(out, d20Die{Src: "/assets/" + name, Kept: i == kept})
	}
	return out
}

// d20FaceURL: the picture of the face that counted, "" when there is none.
func d20FaceURL(line string) string {
	for _, d := range d20Dice(line) {
		if d.Kept {
			return d.Src
		}
	}
	return ""
}
