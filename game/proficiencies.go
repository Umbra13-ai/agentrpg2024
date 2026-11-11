package game

// Weapon, armor and tool proficiencies granted by class and multiclassing,
// following the 2024 rules (SRD 5.2.1 "Core ... Traits" tables and each
// class's "As a Multiclass Character" entry).
// The classes table in the database has empty proficiency columns, so these
// tables are the source of truth. Entries are stored lowercase. Weapons use
// "simple", "martial", or a martial weapon with a property requirement (see
// the constants below). Armor uses "light", "medium", "heavy" and "shields".
// In the 2024 rules species grant no weapon or tool proficiencies, and
// subclasses in the SRD grant none, so only classes appear here.

import (
	"strings"
)

// Weapon proficiency tokens for the 2024 classes that get only some martial weapons.
const (
	WeaponMartialLight        = "martial weapons that have the light property"
	WeaponMartialFinesseLight = "martial weapons that have the finesse or light property"
)

// Proficiencies is a set of weapon, armor and tool proficiencies.
type Proficiencies struct {
	Weapons []string
	Armor   []string
	Tools   []string
}

var (
	simpleOnly    = []string{"simple"}
	simpleMartial = []string{"simple", "martial"}
)

// classStartProficiencies are what a character gets for choosing the class as
// their first class. Tool proficiencies that depend on a choice (Bard
// instruments, Monk tool) are not listed here; see ToolChoicesFor.
var classStartProficiencies = map[string]Proficiencies{
	"barbarian": {Weapons: simpleMartial, Armor: []string{"light", "medium", "shields"}},
	"bard":      {Weapons: simpleOnly, Armor: []string{"light"}},
	"cleric":    {Weapons: simpleOnly, Armor: []string{"light", "medium", "shields"}},
	"druid":     {Weapons: simpleOnly, Armor: []string{"light", "shields"}, Tools: []string{"herbalism kit"}},
	"fighter":   {Weapons: simpleMartial, Armor: []string{"light", "medium", "heavy", "shields"}},
	"monk":      {Weapons: []string{"simple", WeaponMartialLight}},
	"paladin":   {Weapons: simpleMartial, Armor: []string{"light", "medium", "heavy", "shields"}},
	"ranger":    {Weapons: simpleMartial, Armor: []string{"light", "medium", "shields"}},
	"rogue":     {Weapons: []string{"simple", WeaponMartialFinesseLight}, Armor: []string{"light"}, Tools: []string{"thieves' tools"}},
	"sorcerer":  {Weapons: simpleOnly},
	"warlock":   {Weapons: simpleOnly, Armor: []string{"light"}},
	"wizard":    {Weapons: simpleOnly},
}

// classMulticlassProficiencies are what a character gains by taking a level in
// the class when it is not their first class (each class's "As a Multiclass
// Character" entry). Skill and tool choices are in ToolChoicesFor.
var classMulticlassProficiencies = map[string]Proficiencies{
	"barbarian": {Weapons: []string{"martial"}, Armor: []string{"shields"}},
	"bard":      {Armor: []string{"light"}},
	"cleric":    {Armor: []string{"light", "medium", "shields"}},
	"druid":     {Armor: []string{"light", "shields"}},
	"fighter":   {Weapons: []string{"martial"}, Armor: []string{"light", "medium", "shields"}},
	"monk":      {},
	"paladin":   {Weapons: []string{"martial"}, Armor: []string{"light", "medium", "shields"}},
	"ranger":    {Weapons: []string{"martial"}, Armor: []string{"light", "medium", "shields"}},
	"rogue":     {Armor: []string{"light"}, Tools: []string{"thieves' tools"}},
	"sorcerer":  {},
	"warlock":   {Armor: []string{"light"}},
	"wizard":    {},
}

// ClassProficiencies returns the proficiencies for a first class.
func ClassProficiencies(class string) Proficiencies {
	return classStartProficiencies[strings.ToLower(strings.TrimSpace(class))]
}

// MulticlassProficiencies returns the proficiencies gained by multiclassing into a class.
func MulticlassProficiencies(class string) Proficiencies {
	return classMulticlassProficiencies[strings.ToLower(strings.TrimSpace(class))]
}

// ClassOptionProficiencies covers the level 1 class choices that change
// proficiencies: a Cleric's Divine Order of Protector and a Druid's Primal
// Order of Warden each grant Martial weapons, plus Heavy and Medium armor.
func ClassOptionProficiencies(class, divineOrder, primalOrder string) Proficiencies {
	c := strings.ToLower(strings.TrimSpace(class))
	switch {
	case c == "cleric" && strings.EqualFold(strings.TrimSpace(divineOrder), "protector"):
		return Proficiencies{Weapons: []string{"martial"}, Armor: []string{"heavy"}}
	case c == "druid" && strings.EqualFold(strings.TrimSpace(primalOrder), "warden"):
		return Proficiencies{Weapons: []string{"martial"}, Armor: []string{"medium"}}
	}
	return Proficiencies{}
}

// CharacterInput is everything that determines a character's proficiencies.
type CharacterInput struct {
	FirstClass  string
	ClassLevels map[string]int
	DivineOrder string
	PrimalOrder string
}

// ProficienciesFor combines every source for one character.
func ProficienciesFor(in CharacterInput) Proficiencies {
	var out Proficiencies
	add := func(p Proficiencies) {
		out.Weapons = append(out.Weapons, p.Weapons...)
		out.Armor = append(out.Armor, p.Armor...)
		out.Tools = append(out.Tools, p.Tools...)
	}
	first := strings.ToLower(strings.TrimSpace(in.FirstClass))
	add(ClassProficiencies(first))
	for c, lvl := range in.ClassLevels {
		c = strings.ToLower(strings.TrimSpace(c))
		if lvl > 0 && c != first {
			add(MulticlassProficiencies(c))
		}
	}
	add(ClassOptionProficiencies(first, in.DivineOrder, in.PrimalOrder))
	return out
}

// MergeProficiencyList adds new entries to a comma-separated list, keeping the
// existing order and skipping anything already present (ignoring case).
func MergeProficiencyList(existing string, add []string) string {
	have := map[string]bool{}
	var out []string
	for _, p := range ParseProficiencyList(existing) {
		key := strings.ToLower(p)
		if !have[key] {
			have[key] = true
			out = append(out, key)
		}
	}
	for _, p := range add {
		key := strings.ToLower(strings.TrimSpace(p))
		if key != "" && !have[key] {
			have[key] = true
			out = append(out, key)
		}
	}
	return strings.Join(out, ", ")
}

// ---- tool proficiencies that depend on a choice ----

// ToolChoice is a tool proficiency the player picks: Count tools from the
// listed categories.
type ToolChoice struct {
	Source     string   `json:"source"`
	Count      int      `json:"count"`
	Categories []string `json:"categories"`
	Options    []string `json:"options"`
}

func choiceOptions(categories []string) []string {
	var out []string
	for _, c := range categories {
		out = append(out, ToolsInCategory(c)...)
	}
	return out
}

// ToolChoicesFor lists the tool choices a character owes: a Bard chooses three
// Musical Instruments, a Monk one Artisan's Tools or Musical Instrument, and a
// Bard multiclass chooses one Musical Instrument.
func ToolChoicesFor(firstClass string, classLevels map[string]int) []ToolChoice {
	first := strings.ToLower(strings.TrimSpace(firstClass))
	var out []ToolChoice
	switch first {
	case "bard":
		out = append(out, ToolChoice{Source: "Bard", Count: 3, Categories: []string{ToolInstrument}, Options: choiceOptions([]string{ToolInstrument})})
	case "monk":
		cats := []string{ToolArtisan, ToolInstrument}
		out = append(out, ToolChoice{Source: "Monk", Count: 1, Categories: cats, Options: choiceOptions(cats)})
	}
	for c, lvl := range classLevels {
		if lvl > 0 && strings.ToLower(strings.TrimSpace(c)) == "bard" && first != "bard" {
			out = append(out, ToolChoice{Source: "Bard (multiclass)", Count: 1, Categories: []string{ToolInstrument}, Options: choiceOptions([]string{ToolInstrument})})
		}
	}
	return out
}

// PendingToolChoices returns the choices still to be made, given the tool
// proficiencies the character already has. Tools the character already owns
// in a choice's categories count toward it.
func PendingToolChoices(choices []ToolChoice, ownedCSV string) []ToolChoice {
	owned := ParseProficiencyList(ownedCSV)
	pool := make([]string, 0, len(owned))
	for _, o := range owned {
		pool = append(pool, NormalizeToolName(o))
	}
	var pending []ToolChoice
	for _, ch := range choices {
		need := ch.Count
		keep := pool[:0:0]
		for _, o := range pool {
			matched := false
			if need > 0 {
				if t, ok := FindTool(o); ok {
					for _, cat := range ch.Categories {
						if t.Category == cat {
							matched = true
						}
					}
				}
			}
			if matched {
				need--
			} else {
				keep = append(keep, o)
			}
		}
		pool = keep
		if need > 0 {
			p := ch
			p.Count = need
			pending = append(pending, p)
		}
	}
	return pending
}

// ValidateToolChoices checks a player's picks against the pending choices and
// returns the normalised tool names, or an error message.
func ValidateToolChoices(picks []string, pending []ToolChoice) ([]string, string) {
	if len(pending) == 0 {
		return nil, "no_tool_choices_pending"
	}
	total := 0
	for _, p := range pending {
		total += p.Count
	}
	var names []string
	seen := map[string]bool{}
	for _, raw := range picks {
		t, ok := FindTool(raw)
		if !ok {
			return nil, "unknown_tool: " + raw
		}
		if seen[t.Name] {
			return nil, "duplicate_tool: " + t.Name
		}
		seen[t.Name] = true
		names = append(names, t.FullName())
	}
	if len(names) == 0 || len(names) > total {
		return nil, "wrong_number_of_tools"
	}
	// every pick must fit some pending choice
	left := make([]ToolChoice, len(pending))
	copy(left, pending)
	for _, n := range names {
		t, _ := FindTool(n)
		placed := false
		for i := range left {
			if left[i].Count <= 0 {
				continue
			}
			for _, cat := range left[i].Categories {
				if t.Category == cat {
					left[i].Count--
					placed = true
					break
				}
			}
			if placed {
				break
			}
		}
		if !placed {
			return nil, "tool_not_allowed_here: " + t.Name
		}
	}
	return names, ""
}
