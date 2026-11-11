// Package game provides the core game rules (SRD 5.2.1).
// backgrounds.go holds the character backgrounds from the SRD 5.2.1 (2024 rules).
package game

import (
	"sort"
	"strings"
)

// BgItem is one line of a background's starting equipment (option A).
type BgItem struct {
	Name   string `json:"name"`
	Qty    int    `json:"qty"`
	Quiver bool   `json:"in_quiver,omitempty"`   // goes in the quiver
	Choice bool   `json:"chosen_tool,omitempty"` // the slot filled by the player's chosen tool
}

// Background is a 2024 background: three ability scores, an Origin feat, two
// skills, one tool and a choice between an equipment package and 50 GP.
type Background struct {
	Key           string   `json:"key"`
	Name          string   `json:"name"`
	AbilityScores []string `json:"ability_scores"` // three abilities (str, dex, con, int, wis, cha); +2/+1 or +1/+1/+1
	Feat          string   `json:"feat"`           // Origin feat slug
	FeatNote      string   `json:"feat_note,omitempty"`
	Skills        []string `json:"skill_proficiencies"` // two skills
	Tool          string   `json:"tool_proficiency,omitempty"`
	ToolCategory  string   `json:"tool_choice_category,omitempty"` // "gaming": the player picks the kind
	Equipment     []BgItem `json:"equipment_a"`
	GoldA         int      `json:"gold_a"` // gold that comes with package A
	GoldB         int      `json:"gold_b"` // option B is gold only
	Description   string   `json:"description"` // a narrative for the background: empty in the SRD; a private server can add one (local content)
}

// BackgroundAltGold is what option B (gold instead of the package) gives.
const BackgroundAltGold = 50

var backgrounds = map[string]Background{
	"acolyte": {
		Key: "acolyte", Name: "Acolyte",
		AbilityScores: []string{"int", "wis", "cha"},
		Feat:          "magic_initiate", FeatNote: "Cleric",
		Skills: []string{"insight", "religion"},
		Tool:   "calligrapher's supplies",
		Equipment: []BgItem{
			{Name: "Calligrapher's Supplies", Qty: 1}, {Name: "Book (prayers)", Qty: 1}, {Name: "Holy Symbol", Qty: 1},
			{Name: "Parchment", Qty: 10}, {Name: "Robe", Qty: 1},
		},
		GoldA: 8, GoldB: BackgroundAltGold,
	},
	"criminal": {
		Key: "criminal", Name: "Criminal",
		AbilityScores: []string{"dex", "con", "int"},
		Feat:          "alert",
		Skills:        []string{"sleight of hand", "stealth"},
		Tool:          "thieves' tools",
		Equipment: []BgItem{
			{Name: "Dagger", Qty: 2}, {Name: "Thieves' Tools", Qty: 1}, {Name: "Crowbar", Qty: 1},
			{Name: "Pouch", Qty: 2}, {Name: "Clothes, Traveler's", Qty: 1},
		},
		GoldA: 16, GoldB: BackgroundAltGold,
	},
	"sage": {
		Key: "sage", Name: "Sage",
		AbilityScores: []string{"con", "int", "wis"},
		Feat:          "magic_initiate", FeatNote: "Wizard",
		Skills: []string{"arcana", "history"},
		Tool:   "calligrapher's supplies",
		Equipment: []BgItem{
			{Name: "Quarterstaff", Qty: 1}, {Name: "Calligrapher's Supplies", Qty: 1}, {Name: "Book (history)", Qty: 1},
			{Name: "Parchment", Qty: 8}, {Name: "Robe", Qty: 1},
		},
		GoldA: 8, GoldB: BackgroundAltGold,
	},
	"soldier": {
		Key: "soldier", Name: "Soldier",
		AbilityScores: []string{"str", "dex", "con"},
		Feat:          "savage_attacker",
		Skills:        []string{"athletics", "intimidation"},
		ToolCategory:  "gaming",
		Equipment: []BgItem{
			{Name: "Spear", Qty: 1}, {Name: "Shortbow", Qty: 1}, {Name: "Arrow", Qty: 20, Quiver: true},
			{Name: "Quiver", Qty: 1}, {Name: "Gaming Set", Qty: 1, Choice: true}, {Name: "Healer's Kit", Qty: 1},
			{Name: "Clothes, Traveler's", Qty: 1},
		},
		GoldA: 14, GoldB: BackgroundAltGold,
	},
}

// normalizeBackgroundKey turns "Folk Hero", "folk-hero" and "folk_hero" into one form.
func normalizeBackgroundKey(slug string) string {
	s := strings.ToLower(strings.TrimSpace(slug))
	s = strings.ReplaceAll(s, "-", "_")
	return strings.ReplaceAll(s, " ", "_")
}

// GetBackground returns background info by name or slug, or nil if not found.
func GetBackground(slug string) *Background {
	bg, ok := backgrounds[normalizeBackgroundKey(slug)]
	if !ok {
		return nil
	}
	return &bg
}

// SetBackgroundDescriptions gives backgrounds a narrative ({"acolyte": "..."}); the SRD has none (a private server's local content).
func SetBackgroundDescriptions(m map[string]string) {
	for k, text := range m {
		key := normalizeBackgroundKey(k)
		if bg, ok := backgrounds[key]; ok {
			bg.Description = text
			backgrounds[key] = bg
		}
	}
}

// GetAllBackgrounds returns all available backgrounds.
func GetAllBackgrounds() map[string]Background {
	return backgrounds
}

// BackgroundSlugs returns the background keys in alphabetical order.
func BackgroundSlugs() []string {
	slugs := make([]string, 0, len(backgrounds))
	for slug := range backgrounds {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	return slugs
}

// BackgroundCount returns the number of available backgrounds.
func BackgroundCount() int {
	return len(backgrounds)
}

// ValidateAbilityIncreases checks a background's ability score increases:
// +2 and +1 on two of its three abilities, or +1 on all three. Keys are
// short ability names (str, dex, con, int, wis, cha).
func ValidateAbilityIncreases(bg *Background, inc map[string]int) error {
	allowed := map[string]bool{}
	for _, a := range bg.AbilityScores {
		allowed[a] = true
	}
	var plus2, plus1 int
	for k, v := range inc {
		if !allowed[strings.ToLower(k)] {
			return errAbility("%s is not one of this background's abilities (%s)", k, strings.Join(bg.AbilityScores, ", "))
		}
		switch v {
		case 2:
			plus2++
		case 1:
			plus1++
		case 0:
		default:
			return errAbility("an increase must be +2 or +1, not %d", v)
		}
	}
	if (plus2 == 1 && plus1 == 1) || (plus2 == 0 && plus1 == 3) {
		return nil
	}
	return errAbility("increase one ability by 2 and another by 1, or all three by 1")
}
