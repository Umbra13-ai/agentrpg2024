package main

// What a GM learns about a monster before it is saved, and what the server fills in for it (owner, Nov 22). customMonsterReport is the one
// answer, in the preview, in an import's and an edit's reply, so a GM reads the same thing wherever it asks: what the server fills in
// that the GM left out (normalizeCustomMonster), what the server will get wrong or count as 0 unless the wording changes
// (customMonsterWarnings), and, entry by entry and sentence by sentence, what a rule of the server reads and what the GM resolves in
// play (entryReports: the verdicts the SRD tests hold, monster_entry_verdicts_2024.go).

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// normalizeCustomMonster fills in what the definition left out, in one place (import, edit and preview all call it before they validate):
// a saving throw bonus left out (not written, where the ability modifier is not 0) is the ability modifier, which is what a stat block prints for
// an ability the creature is not proficient in; a written 0 stays (a proficient save of a creature whose modifier is -2). It returns what it changed, for the GM to read.
func normalizeCustomMonster(m *srdMonster2024) []string {
	var notes []string
	for _, k := range abilityOrder {
		a, ok := m.Abilities[k]
		if !ok {
			continue
		}
		if mod := game.Modifier(a.Score); !a.saveGiven && a.Save == 0 && mod != 0 {
			a.Save = mod
			m.Abilities[k] = a
			notes = append(notes, fmt.Sprintf("%s saving throw left out: used the ability modifier (%+d).", strings.ToUpper(k), mod))
		}
	}
	if m.Initiative.Mod == 0 && m.Initiative.Score == 0 && m.Abilities["dex"].Score != 0 {
		notes = append(notes, fmt.Sprintf("initiative left out: used the Dexterity modifier (%+d).", game.Modifier(m.Abilities["dex"].Score)))
	}
	return notes
}

// customMonsterWarnings: what the server will not do for this stat block as written, and how to word it so it does.
func customMonsterWarnings(m srdMonster2024) []string {
	var warnings []string
	meta := metaFromMonster("preview", m)
	if len(m.Legendary) > 0 && meta.LAUses == 0 {
		warnings = append(warnings, "This monster has Legendary Actions but legendary_intro gives no count the server reads, so it has 0 uses a round. Word it as in the SRD: \"Legendary Action Uses: 3 (4 in Lair).\"")
	}
	for _, t := range m.Traits {
		if strings.HasPrefix(strings.ToLower(t.Name), "legendary resistance") && meta.LR == 0 {
			warnings = append(warnings, fmt.Sprintf("The trait %q gives no count the server reads, so it has 0 uses a day. Name it as in the SRD: \"Legendary Resistance (3/Day, or 4/Day in Lair)\".", t.Name))
		}
		if r, ok := traitRuleFor(t); ok && r.Name != "" && !r.GM {
			warnings = append(warnings, fmt.Sprintf("The trait %q is applied by the server's rule of that name (%s). Its text is not read: make sure it means the same.", t.Name, r.Does))
		}
	}
	for _, g := range [][]srdMonsterEntry{m.Actions, m.BonusActions, m.Reactions, m.Legendary} { // an attack play refuses whole, said before play
		for _, e := range g {
			if isAttackEntry(e.Text) {
				if problem := attackEntryRefusal(e); problem != "" {
					warnings = append(warnings, fmt.Sprintf("The attack %q will be refused in play, nothing rolled: %s Write each damage the server reads as \"plus N (dice) Type damage\"; put a conditional or charge-spending extra damage in its own sentence (\"If ..., the target takes an extra 5 (2d4) Piercing damage.\"): the attack then rolls and the GM resolves that sentence.", e.Name, problem))
				}
			}
		}
	}
	if strings.TrimSpace(m.Speed) == "" {
		warnings = append(warnings, "speed is empty: the server counts it as 0 feet.")
	}
	for _, k := range abilityOrder {
		if a, ok := m.Abilities[k]; ok && a.Save < game.Modifier(a.Score) {
			warnings = append(warnings, fmt.Sprintf("%s saving throw %+d is lower than the ability modifier %+d: a stat block's save is the modifier plus any proficiency.", strings.ToUpper(k), a.Save, game.Modifier(a.Score)))
		}
	}
	if w := personalPronounWarning(m); w != "" {
		warnings = append(warnings, w)
	}
	return warnings
}

// personalPronounRe: "he", "his", "she", "her" ... in an entry. The SRD calls every creature "it" ("until the start of its next turn"), and the
// server reads that wording only, so a rule that reads a duration or a target misses a sentence written with he or she (I-ALDENQA-1: "Until
// the start of his next turn" was handed to the GM but never listed as an open item). Read to warn, never as a rule.
var personalPronounRe = regexp.MustCompile(`(?i)\b(he|him|his|himself|she|her|hers|herself)\b`)

// personalPronounWarning: the entries that call the creature he or she, with what to write instead.
func personalPronounWarning(m srdMonster2024) string {
	var named []string
	for _, group := range [][]srdMonsterEntry{m.Traits, m.Actions, m.BonusActions, m.Reactions, m.Legendary} {
		for _, e := range group {
			if w := personalPronounRe.FindString(e.Text); w != "" {
				named = append(named, fmt.Sprintf("%s (%q)", e.Name, w))
			}
		}
	}
	if len(named) == 0 {
		return ""
	}
	return "These entries say he or she: " + strings.Join(named, ", ") + ". The server reads the SRD's wording, which calls every creature \"it\" " +
		"(\"until the start of its next turn\", \"the target\"), so a sentence written with he or she is yours to run in play and its duration " +
		"is not listed as an open item. Write it and its (or the creature's name) and preview again."
}

// customMonsterReport: the notes, warnings and summary of one monster; with entries true, every entry's sentences too.
func customMonsterReport(m srdMonster2024, notes []string, entries bool) map[string]interface{} {
	reports := entryReports(metaFromMonster("preview", m))
	applied, gm, silent := 0, 0, 0
	for _, r := range reports {
		applied += len(r["applied"].([]map[string]interface{}))
		for _, g := range r["gm_resolves"].([]map[string]interface{}) {
			gm++
			if g["silent"] == true {
				silent++
			}
		}
	}
	out := map[string]interface{}{
		"notes":    nonNil(notes),
		"warnings": nonNil(customMonsterWarnings(m)),
		"summary":  map[string]interface{}{"sentences_applied": applied, "sentences_for_the_gm": gm, "sentences_the_server_will_not_mention": silent},
	}
	if entries {
		out["entries"] = reports
	}
	return out
}

// handleGMCompendiumPreview godoc
// @Summary Preview what the server does with a monster before importing it
// @Description Saves nothing. Send monsters (1–25 stat blocks, as an import takes them) or texts (1–25 stat blocks pasted as text in the SRD 5.2.1 layout: the name, the size, type and alignment line, AC, Initiative, HP, Speed, the ability grid, the labelled lines, then Traits, Actions, Bonus Actions, Reactions and Legendary Actions with each entry as "Name. Text"); not both. A text comes back as the monster it was read as (monster, with read_notes for what could not be read or was changed): check it, correct it, and import that. For each monster it says whether it would import (valid, problem), what the server fills in that you left out (notes), what it will count as 0 or apply by name unless the wording changes (warnings), and, for each entry, which sentences a rule of the server reads (applied_by) and which you resolve in play (gm_resolves; silent true: the server does not mention it either, so remember it). The server reads the SRD 5.2.1 wording: reword an entry as the guide at /gm-monsters.md says and preview again. campaign_id names a campaign you run (needed when you run more than one).
// @Tags GM
// @Router /gm/compendium/monsters/preview [post]
func handleGMCompendiumPreview(w http.ResponseWriter, r *http.Request) {
	agent, ok := compendiumOwnerStart(w, r)
	if !ok {
		return
	}
	var req struct {
		Monsters []srdMonster2024 `json:"monsters"` // 1–25 stat blocks, as in an import
		Texts    []string         `json:"texts"`    // or 1–25 stat blocks pasted as text, up to 100000 bytes each
	}
	if _, ok := gmReadAs(w, r, agent, &req, campaignBeforeStart()); !ok {
		return
	}
	n := len(req.Monsters) + len(req.Texts)
	if (len(req.Monsters) > 0 && len(req.Texts) > 0) || n < 1 || n > 25 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_monster", "message": "Preview 1–25 stat blocks at a time: send monsters or texts, not both."})
		return
	}
	var readNotes [][]string
	fromText := len(req.Texts) > 0
	if fromText {
		readNotes = make([][]string, len(req.Texts))
		for i, t := range req.Texts {
			if len(t) > 100000 {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_monster", "message": fmt.Sprintf("texts[%d] is over 100000 bytes.", i)})
				return
			}
			m, notes := readStatBlockText(t)
			req.Monsters = append(req.Monsters, m)
			readNotes[i] = notes
		}
	}
	out := []map[string]interface{}{}
	for i := range req.Monsters {
		asRead := req.Monsters[i]
		notes := normalizeCustomMonster(&req.Monsters[i])
		m := req.Monsters[i]
		problem := validateCustomMonster(m)
		e := map[string]interface{}{"name": m.Name, "valid": problem == "", "problem": problem}
		if fromText {
			e["monster"], e["read_notes"] = asRead, nonNil(readNotes[i])
		}
		if problem == "" {
			for k, v := range customMonsterReport(m, notes, true) {
				e[k] = v
			}
		}
		out = append(out, e)
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"monsters": out, "saved": false, "token_rule": monsterTokenRuleText})
}
