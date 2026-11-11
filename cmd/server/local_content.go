package main

// Local content: text a private server may show that can't be published with the code because it isn't in the SRD 5.2.1
// (CC-BY-4.0), such as the 2024 Player's Handbook's species and background introductions and its wording of a few spells.
// It lives in a folder outside the repository named by the environment variable AGENTRPG_LOCAL_CONTENT; without it the
// server shows the SRD text only. Files (all optional):
//
//	species_intro.json   {"elf": "...", ...}         the introduction of each species, shown on the species pages
//	backgrounds.json     {"acolyte": "...", ...}     the narrative of each background (pages and the character sheet)
//	spells.json          {"telekinesis": {"replace": [["SRD text", "new text"]], "school": "...", "higher_level": "...",
//	                      "damage_at_slot_level": {"4": "2d8"}}, ...}   changes to spells, applied when they are loaded

import (
	_ "embed"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

func localContentDir() string { return strings.TrimSpace(os.Getenv("AGENTRPG_LOCAL_CONTENT")) }

// loadLocalJSON reads one file of the local content folder into v; false when there is no folder or no such file.
func loadLocalJSON(name string, v interface{}) bool {
	dir := localContentDir()
	if dir == "" {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return false
	}
	if err := json.Unmarshal(b, v); err != nil {
		log.Printf("local content %s: %v", name, err)
		return false
	}
	return true
}

var (
	speciesIntroOnce sync.Once
	speciesIntros    map[string]string
)

// speciesIntro is a species' introduction from the local content ("" without it).
func speciesIntro(name string) string {
	speciesIntroOnce.Do(func() { loadLocalJSON("species_intro.json", &speciesIntros) })
	return speciesIntros[strings.ToLower(strings.TrimSpace(name))]
}

// applyLocalBackgrounds gives the backgrounds the narratives of the local content (called at startup).
func applyLocalBackgrounds() {
	var m map[string]string
	if loadLocalJSON("backgrounds.json", &m) {
		game.SetBackgroundDescriptions(m)
		log.Printf("Local content: %d background narratives", len(m))
	}
}

type localSpellChange struct {
	Replace           [][2]string       `json:"replace"`
	School            string            `json:"school"`
	HigherLevel       string            `json:"higher_level"`
	DamageAtSlotLevel map[string]string `json:"damage_at_slot_level"`
}

// spellCorrections2024: the Player's Handbook's corrections to four SRD spells (Telekinesis, Animal Shapes, Flaming Sphere's school,
// Conjure Minor Elementals' damage at higher levels), applied on every server (the project owner, Nov 10: wording changes to SRD spells are fine).
//
//go:embed data/spell_corrections_2024.json
var spellCorrections2024 []byte

// applyLocalSpellChanges changes the SRD spells, before they are stored: the corrections above, then the local content's changes.
func applyLocalSpellChanges(spells []srdSpell2024) {
	var fixes map[string]localSpellChange
	if json.Unmarshal(spellCorrections2024, &fixes) == nil {
		applySpellChanges(spells, fixes, "Spell corrections")
	}
	var changes map[string]localSpellChange
	if !loadLocalJSON("spells.json", &changes) {
		return
	}
	applySpellChanges(spells, changes, "Local content")
}

func applySpellChanges(spells []srdSpell2024, changes map[string]localSpellChange, label string) {
	n := 0
	for i := range spells {
		c, ok := changes[spells[i].Slug]
		if !ok {
			continue
		}
		n++
		for _, r := range c.Replace {
			spells[i].Description = strings.ReplaceAll(spells[i].Description, r[0], r[1])
			var blocks []interface{}
			if json.Unmarshal(spells[i].DescriptionBlocks, &blocks) == nil {
				for j, b := range blocks {
					if s, ok := b.(string); ok {
						blocks[j] = strings.ReplaceAll(s, r[0], r[1])
					}
				}
				spells[i].DescriptionBlocks, _ = json.Marshal(blocks)
			}
		}
		if c.School != "" {
			spells[i].School = c.School
		}
		if c.HigherLevel != "" {
			spells[i].HigherLevel = c.HigherLevel
		}
		if len(c.DamageAtSlotLevel) > 0 {
			spells[i].DamageAtSlotLevel = c.DamageAtSlotLevel
		}
	}
	log.Printf("%s: %d spells changed", label, n)
}
