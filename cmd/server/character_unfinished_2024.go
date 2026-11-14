package main

// Unfinished characters (F232, Nov 10, the project owner: "does the server not alert players during character creation of what they need to set?
// does it not tell them their characters are not finished?"). The level-up note listed the choices once and my-turn showed only a
// subclass, ASI points and cantrips. classOpenChoices checks the rest of what a class asks a player to choose, from what the character
// has stored: Weapon Mastery, Fighting Style, Expertise, Divine Order, Primal Order, Eldritch Invocations, Metamagic, prepared spells
// and Wild Shape forms. The list (with levelUpOpenChoices) shows in the creation reply (to_finish), on GET /api/characters/{id}
// (to_finish, character_finished), in my-turn (level_up_choices) and to the GM (gm/status gm_tasks and unfinished_characters).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

type unfinishedKey struct{}

// characterToFinish is everything a character still has to choose, sorted.
func characterToFinish(charID int) []string {
	out := append(levelUpOpenChoices(charID), classOpenChoices(charID)...)
	out = append(out, levels23OpenChoices(charID)...)
	for _, st := range wizardCapstoneSteps(charID) {
		out = append(out, fmt.Sprintf("%s (%s)", st.Title, map[string]string{"spell_mastery": "POST /api/characters/spell-mastery", "signature_spells": "POST /api/characters/signature-spells"}[st.Key]))
	}
	if st := mysticArcanumStep(charID); st != nil {
		out = append(out, fmt.Sprintf("Mystic Arcanum: %s (POST /api/characters/mystic-arcanum)", st.Title))
	}
	if d := pendingClassLanguage(charID); d != nil {
		out = append(out, fmt.Sprintf("%s: choose %d language(s) (POST /api/characters/%d/class-options {\"class_languages\": [...]})", d.feature, d.count, charID))
	}
	for _, st := range pendingClassChoices(charID) {
		out = append(out, fmt.Sprintf("%s (POST /api/characters/%d/class-options)", st.Title, charID))
	}
	if hasMagicalDiscoveries(charID) && len(magicalDiscoveriesOf(charID)) < 2 {
		out = append(out, fmt.Sprintf("Magical Discoveries: choose 2 Cleric, Druid or Wizard spells (POST /api/characters/%d/class-options {\"magical_discoveries\": [...]})", charID))
	}
	if line := autoEquipOpen(charID); line != "" {
		out = append(out, line)
	}
	sort.Strings(out)
	return out
}

func jsonCount(charID int, column string) int {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(`+column+`, '[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var list []interface{}
	json.Unmarshal(raw, &list)
	return len(list)
}

// classOpenChoices: the class choices the character's levels allow that it hasn't made.
func classOpenChoices(charID int) []string {
	var level int
	var class, subclass, expertise, divine, primal string
	var prepared []byte
	if db.QueryRow(`SELECT COALESCE(level,1), COALESCE(class,''), COALESCE(subclass,''), COALESCE(expertise,''), COALESCE(divine_order,''),
		COALESCE(primal_order,''), COALESCE(prepared_spells,'[]') FROM characters WHERE id = $1`, charID).
		Scan(&level, &class, &subclass, &expertise, &divine, &primal, &prepared) != nil {
		return nil
	}
	levels := classLevelsOf(charID)
	if len(levels) == 0 {
		levels = map[string]int{strings.ToLower(class): level}
	}
	var out []string
	// Weapon Mastery: the class with the most
	wantMastery := 0
	for c, l := range levels {
		if n := masteryCount(c, l); n > wantMastery {
			wantMastery = n
		}
	}
	if have := jsonCount(charID, "weapon_masteries"); have < wantMastery {
		out = append(out, fmt.Sprintf("Weapon Mastery: %d of %d kinds of weapon chosen (POST /api/characters/weapon-masteries)", have, wantMastery))
	}
	wantStyles := 0
	for c, l := range levels {
		if n := getMaxFightingStyles(c, l, strings.ToLower(subclass)); n > wantStyles {
			wantStyles = n
		}
	}
	if have := len(getFightingStyles(charID)); have < wantStyles {
		out = append(out, "Fighting Style: choose a Fighting Style feat (POST /api/characters/fighting-style)")
	}
	wantExp := 0
	for c, l := range levels {
		wantExp += expertiseAllowed(c, l)
	}
	haveExp := 0
	seenExp := map[string]bool{} // the same skill twice is one Expertise (F6)
	for _, s := range strings.Split(expertise, ",") {
		if k := skillKey(s); k != "" && !seenExp[k] {
			seenExp[k] = true
			haveExp++
		}
	}
	if haveExp < wantExp {
		out = append(out, fmt.Sprintf("Expertise: %d of %d skills chosen (POST /api/characters/%d/expertise with all of them)", haveExp, wantExp, charID))
	}
	if levels["cleric"] >= 1 && divine == "" {
		out = append(out, fmt.Sprintf("Divine Order: choose protector or thaumaturge (POST /api/characters/%d/class-options {\"divine_order\": ...})", charID))
	}
	if levels["druid"] >= 1 && primal == "" {
		out = append(out, fmt.Sprintf("Primal Order: choose magician or warden (POST /api/characters/%d/class-options {\"primal_order\": ...})", charID))
	}
	if l := levels["druid"]; l >= 2 {
		// the number of known forms grows at Druid levels 4 and 8 (Nov 13: the new ones were never asked for)
		if kn, _, _ := wildShapeLimits(l); len(knownWildShapes(charID)) < kn {
			out = append(out, fmt.Sprintf("Wild Shape: %d of your %d known Beast forms chosen (POST /api/characters/%d/class-options {\"wild_shape_forms\": [...]} with all of them)", len(knownWildShapes(charID)), kn, charID))
		}
	}
	if l := levels["warlock"]; l >= 1 {
		if want, have := game.GetMaxInvocations(l), jsonCount(charID, "eldritch_invocations"); have < want {
			out = append(out, fmt.Sprintf("Eldritch Invocations: %d of %d chosen (POST /api/characters/invocations)", have, want))
		}
		if bookOfShadowsOpen(charID) {
			out = append(out, "Pact of the Tome: choose 3 cantrips and 2 level 1 Ritual spells for your Book of Shadows (POST /api/characters/invocations {\"invocation\": \"pact-of-the-tome\", \"cantrips\": [...], \"rituals\": [...]})")
		}
	}
	if l := levels["sorcerer"]; l >= 2 {
		if want, have := getMaxMetamagicChoices(l), len(getMetamagicChoices(charID)); have < want {
			out = append(out, fmt.Sprintf("Metamagic: %d of %d options chosen (POST /api/characters/metamagic)", have, want))
		}
	}
	// prepared spells: a class with a Prepared Spells column and no spell of level 1+ prepared
	wantPrep := 0
	for c, l := range levels {
		wantPrep += game.ClassTableInt(c, l, "Prepared Spells")
	}
	if wantPrep > 0 {
		var list []string
		json.Unmarshal(prepared, &list)
		if len(levels) == 1 {
			list = currentPreparedList(charID, class) // a Bard's, Ranger's, Sorcerer's or Warlock's list lives in known_spells, as /prepare reads it (F3)
		}
		always := allAlwaysPrepared(charID)
		leveled := 0
		for _, s := range list {
			if containsStr(always, s) {
				continue
			}
			if sp, ok := srdSpellsMemory[normalizeSpellSlug(s)]; ok && sp.Level > 0 {
				leveled++
			}
		}
		if leveled == 0 {
			out = append(out, fmt.Sprintf("Spells: none of your %d level 1+ spells are prepared (POST /api/characters/%d/prepare {\"spells\": [...]})", wantPrep, charID))
		} else if leveled < wantPrep {
			out = append(out, fmt.Sprintf("Spells: %d of your %d level 1+ spells prepared; your level allows more (POST /api/characters/%d/prepare {\"spells\": [...]} with the whole list)", leveled, wantPrep, charID))
		}
	}
	// a list over the limits, or with spells the class can't prepare, is refused by /prepare: say it here too (F3)
	if p := preparedListProblems(charID); len(p) > 0 {
		out = append(out, fmt.Sprintf("Spells: fix your prepared list: %s (POST /api/characters/%d/prepare {\"spells\": [...]} with the whole list)", strings.Join(p, "; "), charID))
	}
	return out
}

// withToFinish adds to_finish and character_finished to GET /api/characters/{id}.
func withToFinish(w http.ResponseWriter, r *http.Request, charID int, h func(http.ResponseWriter, *http.Request)) bool {
	if r.Method != http.MethodGet || r.Context().Value(unfinishedKey{}) != nil {
		return false
	}
	r2 := r.WithContext(context.WithValue(r.Context(), unfinishedKey{}, true))
	augmentJSON(w, func(rw http.ResponseWriter) { h(rw, r2) }, func(m map[string]interface{}) {
		todo := characterToFinish(charID)
		m["character_finished"] = len(todo) == 0
		// prepared_count counts level 1+ spells as /prepare does, not cantrips or always-prepared spells (F15: a Wizard with 3
		// cantrips and 4 spells showed 7 of 4 and slots_remaining -3). The sheet builds these fields in character_handlers.go.
		if max, ok := m["max_prepared"].(float64); ok {
			class, _, _ := subclassOf(charID)
			list := jsonListOf(charID, "prepared_spells")
			if len(classLevelsOf(charID)) <= 1 {
				list = currentPreparedList(charID, class)
			}
			n := countLeveled(withoutAlwaysPrepared(list, allAlwaysPrepared(charID)))
			m["prepared_count"], m["slots_remaining"] = n, int(max)-n
		}
		if len(todo) > 0 {
			m["to_finish"] = todo
		}
	})
	return true
}

// unfinishedInCampaign: "Name: n choice(s) left" for each character of the campaign that isn't finished (for the GM).
func unfinishedInCampaign(campaignID int) map[string][]string {
	rows, err := db.Query(`SELECT id, name FROM characters WHERE lobby_id = $1`, campaignID)
	if err != nil {
		return nil
	}
	type ch struct {
		id   int
		name string
	}
	var all []ch
	for rows.Next() {
		var c ch
		if rows.Scan(&c.id, &c.name) == nil {
			all = append(all, c)
		}
	}
	rows.Close()
	out := map[string][]string{}
	for _, c := range all {
		if t := characterToFinish(c.id); len(t) > 0 {
			out[c.name] = t
		}
	}
	return out
}
