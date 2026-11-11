package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"github.com/agentrpg/agentrpg/game"
)

// hasPactBoon checks if a character has a specific pact boon
// hasPactBoon: 2024 Pact of the Blade, Chain and Tome are Eldritch Invocations.
func hasPactBoon(characterID int, pact string) bool {
	return hasInvocation(characterID, "pact-of-the-"+strings.ToLower(pact))
}

// getCharacterInvocations returns the eldritch invocations a character has
func getCharacterInvocations(charID int) []string {
	var invocationsJSON []byte
	err := db.QueryRow("SELECT COALESCE(eldritch_invocations, '[]') FROM characters WHERE id = $1", charID).Scan(&invocationsJSON)
	if err != nil {
		return []string{}
	}
	var invocations []string
	json.Unmarshal(invocationsJSON, &invocations)
	return invocations
}

// hasInvocation checks if a character has a specific invocation
func hasInvocation(charID int, slug string) bool {
	invocations := getCharacterInvocations(charID)
	for _, inv := range invocations {
		if inv == slug {
			return true
		}
	}
	return false
}

// getInvocationSpellsUsed returns the list of once-per-rest invocation spells that have been used
func getInvocationSpellsUsed(charID int) []string {
	var usedJSON []byte
	err := db.QueryRow("SELECT COALESCE(invocation_spells_used, '[]') FROM characters WHERE id = $1", charID).Scan(&usedJSON)
	if err != nil {
		return []string{}
	}
	var used []string
	json.Unmarshal(usedJSON, &used)
	return used
}

// hasUsedInvocationSpell checks if a once-per-rest invocation spell has been used
func hasUsedInvocationSpell(charID int, invocationSlug string) bool {
	used := getInvocationSpellsUsed(charID)
	for _, slug := range used {
		if slug == invocationSlug {
			return true
		}
	}
	return false
}

// markInvocationSpellUsed marks a once-per-rest invocation spell as used
func markInvocationSpellUsed(charID int, invocationSlug string) {
	used := getInvocationSpellsUsed(charID)
	// Check if already marked (shouldn't happen, but safety check)
	for _, slug := range used {
		if slug == invocationSlug {
			return
		}
	}
	used = append(used, invocationSlug)
	usedJSON, _ := json.Marshal(used)
	db.Exec("UPDATE characters SET invocation_spells_used = $1 WHERE id = $2", usedJSON, charID)
}

// getOncePerRestInvocationForSpell finds the invocation slug that grants once-per-rest casting of a spell
// Returns (invocationSlug, spellSlug) if found, empty strings if not
func getOncePerRestInvocationForSpell(charID int, spellSlug string) (string, bool) {
	if spellSlug == "water-breathing" && hasInvocation(charID, "gift-of-the-depths") {
		return "gift-of-the-depths", true
	}
	invocations := getCharacterInvocations(charID)
	for _, invSlug := range invocations {
		if inv, ok := game.AvailableInvocations[invSlug]; ok {
			if spell, hasSpell := inv.Mechanics["once_per_rest_spell"]; hasSpell {
				if spell == spellSlug {
					return invSlug, true
				}
			}
		}
	}
	return "", false
}

// At-will invocation spells that can only target self
var atWillSelfOnlySpells = map[string]bool{
	"mage-armor": true, // armor-of-shadows
	"false-life": true, // fiendish-vigor
	"levitate":   true, // ascendant-step
	"jump":       true, // otherworldly-leap
}

// getAtWillInvocationForSpell returns the invocation slug if the character can cast this spell at will
// Returns (invocationSlug, isSelfOnly) if found
func getAtWillInvocationForSpell(charID int, spellSlug string) (string, bool, bool) {
	invocations := getCharacterInvocations(charID)
	for _, invSlug := range invocations {
		if inv, ok := game.AvailableInvocations[invSlug]; ok {
			if spell, hasSpell := inv.Mechanics["at_will_spell"]; hasSpell {
				if spell == spellSlug {
					isSelfOnly := atWillSelfOnlySpells[spellSlug]
					return invSlug, isSelfOnly, true
				}
			}
		}
	}
	return "", false, false
}

// getCharacterAtWillSpells returns all at-will spells available to a character from invocations
func getCharacterAtWillSpells(charID int) []map[string]interface{} {
	invocations := getCharacterInvocations(charID)
	atWillSpells := []map[string]interface{}{}

	for _, invSlug := range invocations {
		if inv, ok := game.AvailableInvocations[invSlug]; ok {
			if spellSlug, hasSpell := inv.Mechanics["at_will_spell"]; hasSpell {
				spellInfo := map[string]interface{}{
					"spell":           spellSlug,
					"invocation":      inv.Name,
					"invocation_slug": invSlug,
					"self_only":       atWillSelfOnlySpells[spellSlug],
				}
				// Add spell details if available
				if spell, found := srdSpellsMemory[spellSlug]; found {
					spellInfo["spell_name"] = spell.Name
					spellInfo["level"] = spell.Level
					spellInfo["school"] = spell.School
				}
				atWillSpells = append(atWillSpells, spellInfo)
			}
		}
	}
	return atWillSpells
}

// meetsInvocationPrerequisites checks if a character meets the prereqs for an invocation
func meetsInvocationPrerequisites(charID int, invocation game.EldritchInvocation) (bool, string) {
	var class string
	var level int
	var knownSpellsJSON []byte
	err := db.QueryRow(`
		SELECT class, level, COALESCE(known_spells, '[]')
		FROM characters WHERE id = $1
	`, charID).Scan(&class, &level, &knownSpellsJSON)
	if err != nil {
		return false, "Character not found"
	}

	// Must have Warlock levels; prerequisites count the Warlock level (a multiclass character's, not its total level)
	if wl := classLevelsOf(charID)["warlock"]; wl > 0 {
		class, level = "warlock", wl
	}
	if strings.ToLower(class) != "warlock" {
		return false, "Only Warlocks can take Eldritch Invocations"
	}

	// Check level prerequisite
	if invocation.Prerequisites.Level > 0 && level < invocation.Prerequisites.Level {
		return false, fmt.Sprintf("Requires Warlock level %d (you are level %d)", invocation.Prerequisites.Level, level)
	}

	// Check spell prerequisite (e.g., must know eldritch blast)
	if invocation.Prerequisites.RequiresSpell != "" {
		var knownSpells []string
		json.Unmarshal(knownSpellsJSON, &knownSpells)
		hasSpell := false
		for _, spell := range knownSpells {
			if spell == invocation.Prerequisites.RequiresSpell {
				hasSpell = true
				break
			}
		}
		if !hasSpell {
			return false, fmt.Sprintf("Requires knowing %s", invocation.Prerequisites.RequiresSpell)
		}
	}

	// Check pact boon prerequisite
	if invocation.Prerequisites.Pact != "" {
		if !hasPactBoon(charID, invocation.Prerequisites.Pact) {
			pactName := "the " + titleCase(invocation.Prerequisites.Pact)
			return false, fmt.Sprintf("Requires Pact of %s", pactName)
		}
	}

	return true, ""
}

// MetamagicOption represents a Sorcerer Metamagic option
type MetamagicOption struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Cost        int    `json:"cost"`         // Sorcery points cost (0 = variable)
	CostFormula string `json:"cost_formula"` // For variable costs like Twinned Spell
	Description string `json:"description"`
}

// All 8 SRD Metamagic options
var metamagicOptions = map[string]MetamagicOption{
	"careful": {
		Slug:        "careful",
		Name:        "Careful Spell",
		Cost:        1,
		Description: "When you cast a spell that forces other creatures to make a saving throw, you can protect some of those creatures from the spell's full force. Choose a number of creatures up to your Charisma modifier (minimum of one). A chosen creature automatically succeeds on its saving throw against the spell.",
	},
	"distant": {
		Slug:        "distant",
		Name:        "Distant Spell",
		Cost:        1,
		Description: "When you cast a spell that has a range of 5 feet or greater, you can double the range of the spell. When you cast a spell that has a range of touch, you can make the range of the spell 30 feet.",
	},
	"empowered": {
		Slug:        "empowered",
		Name:        "Empowered Spell",
		Cost:        1,
		Description: "When you roll damage for a spell, you can reroll a number of the damage dice up to your Charisma modifier (minimum of one). You must use the new rolls. You can use Empowered Spell even if you have already used a different Metamagic option during the casting of the spell.",
	},
	"extended": {
		Slug:        "extended",
		Name:        "Extended Spell",
		Cost:        1,
		Description: "When you cast a spell that has a duration of 1 minute or longer, you can double its duration, to a maximum duration of 24 hours.",
	},
	"heightened": {
		Slug:        "heightened",
		Name:        "Heightened Spell",
		Cost:        3,
		Description: "When you cast a spell that forces a creature to make a saving throw to resist its effects, you can give one target of the spell disadvantage on its first saving throw made against the spell.",
	},
	"quickened": {
		Slug:        "quickened",
		Name:        "Quickened Spell",
		Cost:        2,
		Description: "When you cast a spell that has a casting time of 1 action, you can change the casting time to 1 bonus action for this casting.",
	},
	"subtle": {
		Slug:        "subtle",
		Name:        "Subtle Spell",
		Cost:        1,
		Description: "When you cast a spell, you can cast it without any somatic or verbal components.",
	},
	"twinned": {
		Slug:        "twinned",
		Name:        "Twinned Spell",
		Cost:        0, // Variable: spell level (minimum 1 for cantrips)
		CostFormula: "spell_level",
		Description: "When you cast a spell that targets only one creature and doesn't have a range of self, you can target a second creature in range with the same spell. To be eligible, a spell must be incapable of targeting more than one creature at the spell's current level.",
	},
}

// getMaxMetamagicChoices returns how many metamagic options a sorcerer can know at a given level
// getMaxMetamagicChoices: 2024 Sorcerer: 2 Metamagic options at level 2, 2 more at level 10 and 2 more at level 17.
func getMaxMetamagicChoices(level int) int {
	switch {
	case level < 2:
		return 0
	case level < 10:
		return 2
	case level < 17:
		return 4
	}
	return 6
}

// hasMetamagic checks if a sorcerer has learned a specific metamagic option
func hasMetamagic(charID int, slug string) bool {
	var choicesJSON []byte
	err := db.QueryRow("SELECT COALESCE(metamagic_choices, '[]') FROM characters WHERE id = $1", charID).Scan(&choicesJSON)
	if err != nil {
		return false
	}
	var choices []string
	json.Unmarshal(choicesJSON, &choices)
	for _, c := range choices {
		if c == slug {
			return true
		}
	}
	return false
}

// getMetamagicChoices returns the metamagic options a character has learned
func getMetamagicChoices(charID int) []string {
	var choicesJSON []byte
	err := db.QueryRow("SELECT COALESCE(metamagic_choices, '[]') FROM characters WHERE id = $1", charID).Scan(&choicesJSON)
	if err != nil {
		return []string{}
	}
	var choices []string
	json.Unmarshal(choicesJSON, &choices)
	return choices
}
