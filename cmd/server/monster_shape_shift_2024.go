package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// A monster's form (SRD 5.2.1 and Monster Manual Shape-Shift: lycanthropes, the Vampire, the Mimic, the Imp, the Quasit, the
// Jackalwere ...). One owner:
//   - shapeShiftForms reads a Shape-Shift entry: the forms it can take ("into a Large wolf-humanoid hybrid or a Medium wolf": hybrid,
//     wolf) and its true form ("returns to its true humanoid form": humanoid; "its true form (that of a Small jackal)": jackal);
//   - the Shape-Shift entry, sent with monster_action.use naming a form, records it (combat effect shape_form on the monster, for the
//     fight); a use it doesn't offer is refused, nothing done;
//   - an entry named "(Wolf or Hybrid Form Only)" can be used only in one of those forms (formRefusal, read by monsterEntryRefusal
//     for every entry and by the Multiattack plan), the true form when it hasn't shape-shifted.
// What a form changes besides (its size, a Speed, "can't speak") is the GM's; the reply says so.

var (
	shapeShiftRe  = regexp.MustCompile(`(?i)shape-shifts (?:into|to resemble) (.+?),? or (?:it )?returns to its ([^.(]*?form)(?: \(that of an? (?:(?:Tiny|Small|Medium|Large|Huge|Gargantuan) )?([a-z-]+)\))?`)
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
		words := strings.Fields(strings.ToLower(part))
		var kept []string
		for _, w := range words {
			if !formSizeWords[w] {
				kept = append(kept, w)
			}
		}
		if len(kept) == 0 {
			continue
		}
		name := kept[len(kept)-1]
		if strings.Contains(strings.Join(kept, " "), "hybrid") {
			name = "hybrid"
		}
		if !seen[name] {
			seen[name] = true
			forms = append(forms, name)
		}
	}
	trueForm = strings.ToLower(m[3])
	if trueForm == "" {
		for _, w := range strings.Fields(strings.ToLower(m[2])) {
			if w != "true" && w != "form" && w != "its" {
				trueForm = w
			}
		}
	}
	if trueForm == "" {
		trueForm = "true"
	}
	return forms, trueForm
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
	for _, e := range combatEffectsOn(lobbyID, id) {
		if e.Kind == "shape_form" && e.Text != "" {
			return e.Text
		}
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
	options := append(append([]string{}, forms...), trueForm)
	if strings.EqualFold(strings.TrimSpace(use), "true form") || strings.EqualFold(strings.TrimSpace(use), "true") {
		use = trueForm
	}
	chosen, ok := game.MatchString(use, options)
	if !ok {
		return fmt.Sprintf("%s: name the form in use (%s, or %s: its true form). Nothing was done.", entryName, strings.Join(forms, ", "), trueForm), true
	}
	if apply {
		db.Exec(`DELETE FROM combat_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = 'shape_form'`, lobbyID, monsterID)
		db.Exec(`INSERT INTO combat_effects (lobby_id, target_id, source_id, kind, text) VALUES ($1,$2,$2,'shape_form',$3)`, lobbyID, monsterID, chosen)
	}
	return fmt.Sprintf("%s uses %s: it is in its %s form now (what the form changes besides, its size or Speed, is the GM's; equipment isn't transformed).", name, entryName, chosen), true
}
