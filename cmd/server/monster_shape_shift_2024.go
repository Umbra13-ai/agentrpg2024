package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// A monster's form (SRD 5.2.1 Shape-Shift: lycanthropes, the Vampire, the Mimic, the Imp, the Quasit ...). One owner:
//   - shapeShiftForms reads a Shape-Shift entry: the forms it can take ("into a Large wolf-humanoid hybrid or a Medium wolf": hybrid,
//     wolf) and its true form ("returns to its true humanoid form": humanoid; "its true blob form": blob; "its true form": true);
//   - the Shape-Shift entry, sent with monster_action.use naming a form, records it (combat effect shape_form on the monster, for the
//     fight); a use it doesn't offer is refused, nothing done;
//   - an entry named "(Wolf or Hybrid Form Only)" can be used only in one of those forms (formRefusal, read by monsterEntryRefusal
//     for every entry and by the Multiattack plan), the true form when it hasn't shape-shifted.
// What a form changes besides (its size, a Speed, "can't speak") is the GM's; the reply says so.

var (
	shapeShiftRe  = regexp.MustCompile(`(?i)shape-shifts (?:into|to resemble) (.+?),? or (?:it )?returns to its ([^.(]*?form)`)
	formGateRe    = regexp.MustCompile(`\(((?:[A-Z][a-z]+)(?: or [A-Z][a-z]+)*) Form Only\)`)
	formSizeWords = map[string]bool{"tiny": true, "small": true, "medium": true, "large": true, "huge": true, "gargantuan": true, "a": true, "an": true, "or": true}
)

// shapeShiftForms: the forms a Shape-Shift text offers and its true form ("" options when the text is no Shape-Shift).
func shapeShiftForms(text string) (forms []string, trueForm string) {
	m := shapeShiftRe.FindStringSubmatch(text)
	if m == nil {
		return nil, ""
	}
	phrases := regexp.MustCompile(`\([^)]*\)`).ReplaceAllString(m[1], "")
	phrases = strings.Split(phrases, " while ")[0] // "a Medium or Small object while retaining its game statistics"
	seen := map[string]bool{}
	for _, part := range regexp.MustCompile(`,| or `).Split(phrases, -1) {
		name := formNameOf(part)
		if name != "" && !seen[name] {
			seen[name] = true
			forms = append(forms, name)
		}
	}
	for _, w := range strings.Fields(strings.ToLower(m[2])) {
		if w != "true" && w != "form" && w != "its" {
			trueForm = w
		}
	}
	if trueForm == "" {
		trueForm = "true"
	}
	return forms, trueForm
}

// formNameOf: a form's name in one part of a Shape-Shift's list ("a Large wolf-humanoid hybrid": hybrid; "a raven": raven).
func formNameOf(part string) string {
	var kept []string
	for _, w := range strings.Fields(strings.ToLower(part)) {
		if !formSizeWords[w] {
			kept = append(kept, w)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	if strings.Contains(strings.Join(kept, " "), "hybrid") {
		return "hybrid"
	}
	return kept[len(kept)-1]
}

// formSpeeds: each form's own Speed line where the text gives one ("a raven (20 ft., Fly 60 ft.)": raven -> "20 ft., Fly 60 ft.";
// "Its game statistics are the same in each form, except for its Speed"); a form without one keeps the stat block's Speed.
func formSpeeds(text string) map[string]string {
	m := shapeShiftRe.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	out := map[string]string{}
	var parts []string // the list split at its commas and "or"s outside parentheses
	depth, cur, list := 0, "", m[1]
	for i := 0; i < len(list); i++ {
		switch c := list[i]; {
		case c == '(':
			depth++
		case c == ')':
			depth--
		case depth == 0 && c == ',':
			parts, cur = append(parts, cur), ""
			continue
		case depth == 0 && strings.HasPrefix(list[i:], " or "):
			parts, cur = append(parts, cur), ""
			i += len(" or ") - 1
			continue
		}
		cur += string(list[i])
	}
	parts = append(parts, cur)
	for _, part := range parts {
		speed := ""
		if open := strings.Index(part, "("); open >= 0 {
			if end := strings.LastIndex(part, ")"); end > open {
				speed = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part[open+1:end]), "Speed "))
				if !strings.Contains(speed, "ft") {
					speed = "" // a size or other note in parentheses, not a Speed
				}
			}
			part = part[:open]
		}
		if name := formNameOf(strings.TrimPrefix(strings.TrimSpace(part), "or ")); name != "" && speed != "" {
			out[name] = speed
		}
	}
	return out
}

// shapeShiftRecord: the monster's shape_form record, "form|speed" (the form's own Speed line, "" when it keeps the stat block's).
func shapeShiftRecord(lobbyID, id int) (form, speed string) {
	for _, e := range combatEffectsOn(lobbyID, id) {
		if e.Kind == "shape_form" && e.Text != "" {
			form, speed, _ = strings.Cut(e.Text, "|")
			return form, speed
		}
	}
	return "", ""
}

// formSpeedText: the Speed line of the form the monster has taken ("" in its true form or a form that keeps its Speed), read by the
// movement owner (monster_move_2024.go, creature_movement_2024.go).
func formSpeedText(lobbyID, id int) string {
	_, speed := shapeShiftRecord(lobbyID, id)
	return speed
}

// shapeShiftUseRefusal: a Shape-Shift whose use names none of its forms is refused before anything is done ("" otherwise, or when
// the text is no Shape-Shift).
func shapeShiftUseRefusal(entryName, text, use string) string {
	forms, trueForm := shapeShiftForms(text)
	if len(forms) == 0 {
		return ""
	}
	if _, ok := shapeShiftChoice(forms, trueForm, use); ok {
		return ""
	}
	return fmt.Sprintf("%s: name the form in use (%s, or %s: its true form). Nothing was done.", stripUsage(entryName), strings.Join(forms, ", "), trueForm)
}

// shapeShiftChoice: the form use names ("true form" or "true" is the true form).
func shapeShiftChoice(forms []string, trueForm, use string) (string, bool) {
	if strings.EqualFold(strings.TrimSpace(use), "true form") || strings.EqualFold(strings.TrimSpace(use), "true") {
		use = trueForm
	}
	return game.MatchString(use, append(append([]string{}, forms...), trueForm))
}

// entryForms: the forms an entry is limited to ("Bite (Wolf or Hybrid Form Only)": wolf, hybrid); nil when it has no such limit.
func entryForms(name string) []string {
	m := formGateRe.FindStringSubmatch(name)
	if m == nil {
		return nil
	}
	var out []string
	for _, f := range strings.Split(m[1], " or ") {
		out = append(out, strings.ToLower(strings.TrimSpace(f)))
	}
	return out
}

// shapeShiftEntry: the stat block's Shape-Shift entry, if it has one.
func shapeShiftEntry(meta *monsterMeta) (srdMonsterEntry, bool) {
	if meta == nil {
		return srdMonsterEntry{}, false
	}
	for _, group := range [][]srdMonsterEntry{meta.Actions, meta.BonusActions, meta.Traits} {
		for _, e := range group {
			if shapeShiftRe.MatchString(e.Text) {
				return e, true
			}
		}
	}
	return srdMonsterEntry{}, false
}

// monsterForm: the form the monster is in now (its true form until it shape-shifts).
func monsterForm(lobbyID, id int, meta *monsterMeta) string {
	if form, _ := shapeShiftRecord(lobbyID, id); form != "" {
		return form
	}
	if e, ok := shapeShiftEntry(meta); ok {
		_, t := shapeShiftForms(e.Text)
		return t
	}
	return ""
}

// formRefusal: why the monster can't use the entry in its present form ("" when it can, or the entry has no form limit).
func formRefusal(lobbyID, id int, meta *monsterMeta, entryName string) string {
	allowed := entryForms(entryName)
	if len(allowed) == 0 {
		return ""
	}
	now := monsterForm(lobbyID, id, meta)
	for _, f := range allowed {
		if f == now {
			return ""
		}
	}
	return fmt.Sprintf("%s can use %s only in its %s form (it is in its %s form now; its Shape-Shift changes it, monster_action use naming the form). Nothing was rolled.",
		creatureName(lobbyID, id), stripUsage(formGateRe.ReplaceAllString(entryName, "")), strings.Join(allowed, " or "), now)
}

// resolveShapeShift applies a Shape-Shift entry: use names the form (one of its forms, or its true form). handled is false when the
// text is no Shape-Shift.
func resolveShapeShift(lobbyID, monsterID int, name, entryName, text, use string, apply bool) (line string, handled bool) {
	forms, trueForm := shapeShiftForms(text)
	if len(forms) == 0 {
		return "", false
	}
	chosen, ok := shapeShiftChoice(forms, trueForm, use)
	if !ok { // refused before anything ran (shapeShiftUseRefusal in monsterEntryRefusal's caller); said again for a direct call
		return shapeShiftUseRefusal(entryName, text, use), true
	}
	speed := ""
	if chosen != trueForm {
		speed = formSpeeds(text)[chosen]
	}
	if apply {
		db.Exec(`DELETE FROM combat_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = 'shape_form'`, lobbyID, monsterID)
		if chosen != trueForm {
			db.Exec(`INSERT INTO combat_effects (lobby_id, target_id, source_id, kind, text) VALUES ($1,$2,$2,'shape_form',$3)`, lobbyID, monsterID, chosen+"|"+speed)
		}
	}
	what := "its Speed is the stat block's"
	if speed != "" {
		what = "its Speed is now " + speed
	}
	return fmt.Sprintf("%s uses %s: it is in its %s form now (%s; what else the form changes, its size, is the GM's; equipment isn't transformed).", name, entryName, chosen, what), true
}
