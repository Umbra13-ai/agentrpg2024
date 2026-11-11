package main

// Wizard: Copying a Spell into the Book (SRD 5.2.1, Wizard, "Expanding and Replacing a Spellbook"):
//	POST /api/characters/copy-spell {"character_id": N, "spell": "shatter"}
// A level 1+ Wizard spell of a level the Wizard can prepare (a level it has spell slots for) goes into the spellbook (known_spells):
// 2 hours and 50 GP per spell level, not during combat. Where the spell was found (a Spell Scroll, another wizard's book) is the GM's to say.
// After a Long Rest the Wizard can prepare it (POST /api/characters/{id}/prepare).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// handleCopySpell2024 godoc
// @Summary Copy a Wizard spell into your spellbook
// @Description SRD 5.2.1 Wizard, Copying a Spell into the Book: when you find a level 1+ Wizard spell (on a Spell Scroll, in another wizard's book: the GM says where), you can copy it into your spellbook if it's of a level you can prepare. Each level of the spell takes 2 hours and 50 GP (taken from your gold); not during combat. Afterward you can prepare it like the other spells in your spellbook (after a Long Rest, POST /api/characters/{id}/prepare).
// @Tags Characters
// @Accept json
// @Produce json
// @Param request body object{character_id=int,spell=string} true "The Wizard and the spell's slug"
// @Success 200 {object} map[string]interface{}
// @Router /characters/copy-spell [post]
func handleCopySpell2024(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	var req struct {
		CharacterID int    `json:"character_id"`
		Spell       string `json:"spell"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if _, ok := playerOwns(r, req.CharacterID); !ok {
		writeJSONStatus(w, 403, map[string]interface{}{"error": "not_your_character"})
		return
	}
	var class string
	var level, gold int
	var raw []byte
	db.QueryRow(`SELECT COALESCE(class,''), level, COALESCE(gold,0), COALESCE(known_spells,'[]') FROM characters WHERE id = $1`, req.CharacterID).Scan(&class, &level, &gold, &raw)
	wiz := classLevelsOf(req.CharacterID)["wizard"]
	if wiz == 0 && strings.EqualFold(class, "wizard") {
		wiz = level
	}
	if wiz == 0 {
		dmBadRequest(w, "not_a_wizard", "Only a Wizard keeps a spellbook to copy spells into.")
		return
	}
	slug := normalizeSpellSlug(req.Spell)
	sp, ok := srdSpellsMemory[slug]
	if !ok {
		dmBadRequest(w, "unknown_spell", "No SRD spell is called '"+req.Spell+"'.")
		return
	}
	onList := false
	for _, c := range sp.Classes {
		if strings.EqualFold(strings.TrimSpace(c), "wizard") {
			onList = true
		}
	}
	if !onList || sp.Level < 1 {
		dmBadRequest(w, "not_a_wizard_spell", sp.Name+" isn't a level 1+ Wizard spell: only those can be copied into a spellbook.")
		return
	}
	maxLevel := 0
	for l := range game.SpellSlots("wizard", wiz) {
		if l > maxLevel {
			maxLevel = l
		}
	}
	if sp.Level > maxLevel {
		dmBadRequest(w, "level_too_high", fmt.Sprintf("%s is level %d; a level %d Wizard prepares spells up to level %d.", sp.Name, sp.Level, wiz, maxLevel))
		return
	}
	var book []string
	json.Unmarshal(raw, &book)
	for _, s := range book {
		if normalizeSpellSlug(s) == slug {
			dmBadRequest(w, "already_in_spellbook", sp.Name+" is already in your spellbook.")
			return
		}
	}
	if _, fighting, _ := isCharacterInCombat(req.CharacterID); fighting {
		writeJSONStatus(w, 409, map[string]interface{}{"error": "in_combat", "message": "Copying a spell takes 2 hours per spell level: not during combat."})
		return
	}
	cost := 50 * sp.Level
	if gold < cost {
		writeJSONStatus(w, 409, map[string]interface{}{"error": "not_enough_gold", "message": fmt.Sprintf("Copying %s costs %d GP (50 per spell level); you have %d.", sp.Name, cost, gold)})
		return
	}
	book = append(book, slug)
	b, _ := json.Marshal(book)
	db.Exec(`UPDATE characters SET known_spells = $1, gold = gold - $2 WHERE id = $3`, b, cost, req.CharacterID)
	writeJSONStatus(w, 200, map[string]interface{}{
		"success": true, "spell": sp.Name, "hours": 2 * sp.Level, "gold_spent": cost, "gold": gold - cost, "spellbook_size": len(book),
		"message": fmt.Sprintf("You spend %d hours and %d GP copying %s into your spellbook. You can prepare it after a Long Rest (POST /api/characters/%d/prepare).", 2*sp.Level, cost, sp.Name, req.CharacterID),
	})
}
