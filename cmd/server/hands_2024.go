package main

// Hands for spell components (SRD 5.2.1, "Components"). A Somatic component needs "at least one of their hands"; a Material component
// needs "a hand free to access them, but it can be the same hand used to perform Somatic components", and a Spellcasting Focus must be
// held "unless its description says otherwise" (a Holy Symbol: an Amulet is worn or held, an Emblem is borne on fabric or a Shield, a
// Reliquary is held). So a caster with a weapon in one hand and a Shield in the other frees a hand first (sheathe or drop the weapon:
// one weapon change a turn) or has the War Caster feat (Somatic components with weapons or a Shield in hand).
//
// the project owner, Nov 8 2026: "the character needs to free a hand or have the war caster feat ... there should be a campaign house rule setting
// to allow a GM to either turn this rule on or off": the table rule spell_hands ("required", the default, or "ignored").
//
// War Caster isn't in the SRD 5.2.1 (it is a Player's Handbook feat), so the server's feat list offers it only from a private server's
// local content (feats.json, see local_content.go). Its benefits work for any character whose feats list "war_caster":
//	Concentration    Advantage on Constitution saves to keep Concentration
//	Somatic          Somatic components with weapons or a Shield in one or both hands
//	Reactive Spell   when a creature provokes an Opportunity Attack by leaving its reach, a spell (casting time an action, aimed at that
//	                 creature only) instead: POST /api/action {"action": "opportunity_attack", "target_id": -N, "spell_slug": "..."}

import (
	"fmt"
	"strings"
)

func init() {
	tableRuleDefs = append(tableRuleDefs, spellHandsTableRule())
}

func spellHandsTableRule() tableRuleDef {
	return tableRuleDef{Key: "spell_hands", Title: "Hands for spell components", Default: "required", Options: []tableRuleOption{
		{"required", "A free hand is needed (the rules)", "A spell with a Somatic component needs a hand that holds no weapon or Shield (the War Caster feat lets them stay in hand), or the hand holding the caster's Spellcasting Focus when the spell's Material component has no cost. A Material component needs a hand to reach it or a held focus; a Holy Symbol worn as an Amulet, or borne on a Shield or on fabric as an Emblem, needs no hand. Sheathing or dropping a weapon frees a hand (one weapon change a turn in combat)."},
		{"ignored", "Hands aren't checked", "What a caster holds never blocks a spell's components: weapons and a Shield stay in hand."},
	}}
}

// castHands: what a character's hands hold.
type castHands struct {
	Main, Off string // the weapons or items in hand ("" for an empty hand)
	Shield    bool
	Blade     bool // a Flame Blade (spells_more_2024.go)
}

func handsOf(charID int) castHands {
	var h castHands
	db.QueryRow(`SELECT COALESCE(equipped_main_hand,''), COALESCE(equipped_off_hand,''), COALESCE(equipped_shield,false) FROM characters WHERE id = $1`,
		charID).Scan(&h.Main, &h.Off, &h.Shield)
	h.Main, h.Off = strings.TrimSpace(h.Main), strings.TrimSpace(h.Off)
	h.Blade = flameBladeHeld(charID)
	return h
}

// free counts the empty hands. A Two-Handed weapon is held in one hand while it isn't swung, so it fills one.
func (h castHands) free() int {
	n := 2
	for _, full := range []bool{h.Main != "", h.Off != "", h.Shield, h.Blade} {
		if full {
			n--
		}
	}
	if n < 0 {
		return 0
	}
	return n
}

func (h castHands) describe() string {
	var parts []string
	if h.Main != "" {
		parts = append(parts, h.Main)
	}
	if h.Off != "" {
		parts = append(parts, h.Off)
	}
	if h.Shield {
		parts = append(parts, "a Shield")
	}
	if h.Blade {
		parts = append(parts, "the Flame Blade")
	}
	return joinNames(parts)
}

// focusInHand: a hand holds a Spellcasting Focus the caster can use for the spell (a staff or wand, a Holy Symbol held, an
// instrument, a kit's flagged focus). A Component Pouch needs a free hand to reach into, so it never counts here.
func (h castHands) focusInHand(class string, inventory []map[string]interface{}) bool {
	for _, held := range []string{h.Main, h.Off} {
		if held == "" {
			continue
		}
		item := map[string]interface{}{"name": held}
		for _, it := range inventory {
			if n, _ := it["name"].(string); strings.EqualFold(strings.TrimSpace(n), held) {
				item = it
				break
			}
		}
		for _, k := range itemFocusKinds(item) {
			if k != "any" && k == classFocusKind(class) {
				return true
			}
		}
	}
	return false
}

// wornFocus: a Cleric's or Paladin's Holy Symbol that needs no hand: an Amulet worn, or an Emblem borne on a Shield or on fabric.
func wornFocus(class string, inventory []map[string]interface{}, shield bool) bool {
	if classFocusKind(class) != focusDivine {
		return false
	}
	for _, it := range inventory {
		n := strings.ToLower(fmt.Sprint(it["name"]))
		loc := strings.ToLower(fmt.Sprint(it["location"]))
		divine := strings.Contains(n, "holy symbol") || strings.Contains(n, "emblem")
		if k, _ := it["focus_kind"].(string); strings.EqualFold(k, focusDivine) {
			divine = true
		}
		if !divine || strings.Contains(n, "reliquary") {
			continue // a Reliquary is held
		}
		if strings.Contains(n, "emblem") && shield {
			return true // borne on the Shield
		}
		if loc == "worn" {
			return true // an Amulet worn, or an Emblem on a tabard or banner
		}
	}
	return false
}

// componentLetters reads "V, S, M" (with or without the material in parentheses).
func componentLetters(comps string) (v, s, m bool) {
	c := comps
	if i := strings.Index(c, "("); i >= 0 {
		c = c[:i]
	}
	for _, part := range strings.Split(c, ",") {
		switch strings.ToUpper(strings.TrimSpace(part)) {
		case "V":
			v = true
		case "S":
			s = true
		case "M":
			m = true
		}
	}
	return
}

func hasWarCaster(feats []string) bool {
	for _, f := range feats {
		if strings.EqualFold(strings.ReplaceAll(strings.TrimSpace(f), "-", "_"), "war_caster") {
			return true
		}
	}
	return false
}

func charHasWarCaster(charID int) bool {
	return hasSpecificFeat(charID, "war_caster") || hasSpecificFeat(charID, "war-caster")
}

// castHandsProblem says why the caster's hands can't provide the spell's Somatic and Material components ("" when they can).
// focusForMaterial: the Material component has no cost and isn't consumed, so a focus can stand in for it.
func castHandsProblem(lobbyID, charID int, comps string, focusForMaterial bool, inventory []map[string]interface{}, class string, feats, metamagic []string) string {
	if tableRule(lobbyID, "spell_hands") == "ignored" {
		return ""
	}
	var form string
	if db != nil && db.QueryRow(`SELECT COALESCE(wild_shape_form,'') FROM characters WHERE id = $1`, charID).Scan(&form) == nil && form != "" {
		return "" // Beast Spells: in Wild Shape the Druid makes the gestures with the beast's body; it holds nothing (F122)
	}
	_, hasS, hasM := componentLetters(comps)
	for _, mm := range metamagic {
		if mm == "subtle" {
			hasS = false // Subtle Spell: no Somatic (or Verbal) component
		}
	}
	if !hasS && !hasM {
		return ""
	}
	h := handsOf(charID)
	if h.free() > 0 {
		return "" // a free hand makes the gestures and reaches the components or holds the focus
	}
	focusHand := h.focusInHand(class, inventory)
	if hasS && !hasWarCaster(feats) && !(focusHand && hasM && focusForMaterial) {
		return fmt.Sprintf("Cannot cast - the spell has a Somatic component (S) and you have no free hand (you hold %s; a Spellcasting Focus in hand makes the gestures only for a spell with a Material component that has no cost; a Sorcerer's Subtle Spell removes the Somatic component). Free a hand first: sheathe or drop the weapon (POST /api/characters/unequip-weapon, one weapon change a turn) or take off the Shield (unequip-armor). The GM's table rule spell_hands can turn this check off.", h.describe())
	}
	if hasM && !(focusForMaterial && (focusHand || wornFocus(class, inventory, h.Shield))) {
		what := "reach the spell's Material component or hold your Spellcasting Focus"
		if !focusForMaterial {
			what = "hold the spell's costly Material component"
		}
		return fmt.Sprintf("Cannot cast - the spell has a Material component (M) and no hand is free to %s (you hold %s; a Holy Symbol worn as an Amulet or borne on a Shield as an Emblem needs no hand). Free a hand first (unequip-weapon or unequip-armor). The GM's table rule spell_hands can turn this check off.", what, h.describe())
	}
	return ""
}

// reactiveSpellProblem: War Caster's Reactive Spell. "" when the character can cast the spell at the creature leaving its reach.
func reactiveSpellProblem(charID int, slug string, targetID int, targetIDs []int) string {
	if !charHasWarCaster(charID) {
		return "A spell instead of an Opportunity Attack needs the War Caster feat (Reactive Spell). Make the Opportunity Attack with a melee attack (leave out spell_slug). Your Reaction was not used."
	}
	sp, ok := srdSpellsMemory[normalizeSpellSlug(slug)]
	if !ok {
		return fmt.Sprintf("Reactive Spell: no spell %q. Your Reaction was not used.", slug)
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(sp.CastingTime)), "action") {
		return fmt.Sprintf("Reactive Spell: %s has a casting time of %s; War Caster's spell must have a casting time of one action. Your Reaction was not used.", sp.Name, sp.CastingTime)
	}
	if sp.AoEShape != "" || len(targetIDs) > 1 || (len(targetIDs) == 1 && targetIDs[0] != targetID) {
		return fmt.Sprintf("Reactive Spell: the spell must target only the creature leaving your reach; %s %s. Your Reaction was not used.", sp.Name,
			map[bool]string{true: "covers an area", false: "names other targets"}[sp.AoEShape != ""])
	}
	return ""
}

// applyLocalFeats adds a private server's feats (local content feats.json: {"war_caster": {Feat}}) to the feat list. The SRD 5.2.1
// feats stay as they are.
func applyLocalFeats() {
	var m map[string]Feat
	if !loadLocalJSON("feats.json", &m) {
		return
	}
	for slug, f := range m {
		if _, srd := availableFeats[slug]; srd || f.Name == "" {
			continue
		}
		availableFeats[slug] = f
	}
}
