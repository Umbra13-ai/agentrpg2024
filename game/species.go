package game

// species.go holds the species from the SRD 5.2.1 (2024 rules). Species no
// longer give ability scores or languages; those come from the background and
// the character's chosen languages.

import (
	"fmt"
	"sort"
	"strings"
)

func errAbility(format string, a ...interface{}) error { return fmt.Errorf(format, a...) }

// Trait is one special trait of a species.
type Trait struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Species is the unresolved description of a 2024 species.
type Species struct {
	Key        string   `json:"key"`
	Name       string   `json:"name"`
	Sizes      []string `json:"sizes"` // one size, or two to choose from
	Speed      int      `json:"speed"`
	Darkvision int      `json:"darkvision"`
	Needs      []string `json:"choices_needed"` // keys required in species_choices
	Traits     []Trait  `json:"traits"`
}

// SpeciesResult is a species with all of the player's choices applied.
type SpeciesResult struct {
	Key          string            `json:"key"`
	Name         string            `json:"name"`
	Size         string            `json:"size"`
	Speed        int               `json:"speed"`
	Darkvision   int               `json:"darkvision"`
	Traits       []Trait           `json:"traits"`
	Resistances  []string          `json:"resistances,omitempty"`
	Cantrips     []string          `json:"cantrips,omitempty"`     // spell slugs known now
	LevelSpells  map[int][]string  `json:"level_spells,omitempty"` // character level -> spell slugs gained
	SpellAbility string            `json:"spellcasting_ability,omitempty"`
	Skills       []string          `json:"skills,omitempty"`
	Feat         string            `json:"feat,omitempty"` // Origin feat (Human)
	Choices      map[string]string `json:"choices"`
	HPPerLevel   int               `json:"hp_per_level,omitempty"` // Dwarven Toughness
}

// DragonAncestors is the Draconic Ancestors table (damage type by dragon).
var DragonAncestors = map[string]string{
	"black": "acid", "blue": "lightning", "brass": "fire", "bronze": "lightning", "copper": "acid",
	"gold": "fire", "green": "poison", "red": "fire", "silver": "cold", "white": "cold",
}

var giantAncestries = []string{"cloud", "fire", "frost", "hill", "stone", "storm"}

var species = map[string]Species{
	"dragonborn": {
		Key: "dragonborn", Name: "Dragonborn", Sizes: []string{"Medium"}, Speed: 30, Darkvision: 60, Needs: []string{"ancestry"},
		Traits: []Trait{
			{"Draconic Ancestry", "Your lineage stems from a dragon progenitor. The kind of dragon you choose sets your Breath Weapon and Damage Resistance."},
			{"Breath Weapon", "When you take the Attack action, you can replace one attack with an exhalation in a 15-foot Cone or 30-foot Line 5 feet wide (choose each time). Each creature in it makes a Dexterity save (DC 8 + Constitution modifier + Proficiency Bonus), taking 1d10 damage of your ancestry's type on a failure, half on a success. The damage becomes 2d10 at level 5, 3d10 at 11 and 4d10 at 17. You can use it a number of times equal to your Proficiency Bonus and regain all uses on a Long Rest."},
			{"Damage Resistance", "You have Resistance to the damage type of your Draconic Ancestry."},
			{"Darkvision", "You have Darkvision with a range of 60 feet."},
			{"Draconic Flight", "From level 5, as a Bonus Action you sprout spectral wings for 10 minutes (or until you retract them or are Incapacitated) and have a Fly Speed equal to your Speed. Once used, you cannot use it again until you finish a Long Rest."},
		},
	},
	"dwarf": {
		Key: "dwarf", Name: "Dwarf", Sizes: []string{"Medium"}, Speed: 30, Darkvision: 120,
		Traits: []Trait{
			{"Darkvision", "You have Darkvision with a range of 120 feet."},
			{"Dwarven Resilience", "You have Resistance to Poison damage and Advantage on saving throws to avoid or end the Poisoned condition."},
			{"Dwarven Toughness", "Your Hit Point maximum increases by 1, and by 1 again whenever you gain a level."},
			{"Stonecunning", "As a Bonus Action, you gain Tremorsense with a range of 60 feet for 10 minutes. You must be on or touching a stone surface. You can do this a number of times equal to your Proficiency Bonus and regain all uses on a Long Rest."},
		},
	},
	"elf": {
		Key: "elf", Name: "Elf", Sizes: []string{"Medium"}, Speed: 30, Darkvision: 60, Needs: []string{"lineage", "spellcasting_ability", "skill"},
		Traits: []Trait{
			{"Darkvision", "You have Darkvision with a range of 60 feet."},
			{"Elven Lineage", "You are part of a lineage (Drow, High Elf or Wood Elf) that grants supernatural abilities. You gain its level 1 benefit now, and learn higher-level spells at character levels 3 and 5. You always have them prepared and can cast each once without a spell slot per Long Rest, or with a spell slot."},
			{"Fey Ancestry", "You have Advantage on saving throws to avoid or end the Charmed condition."},
			{"Keen Senses", "You have proficiency in the Insight, Perception or Survival skill (your choice)."},
			{"Trance", "You don't need to sleep, and magic can't put you to sleep. You can finish a Long Rest in 4 hours of trancelike meditation, during which you remain conscious."},
		},
	},
	"gnome": {
		Key: "gnome", Name: "Gnome", Sizes: []string{"Small"}, Speed: 30, Darkvision: 60, Needs: []string{"lineage", "spellcasting_ability"},
		Traits: []Trait{
			{"Darkvision", "You have Darkvision with a range of 60 feet."},
			{"Gnomish Cunning", "You have Advantage on Intelligence, Wisdom and Charisma saving throws."},
			{"Gnomish Lineage", "You are part of a lineage (Forest Gnome or Rock Gnome) that grants supernatural abilities."},
		},
	},
	"goliath": {
		Key: "goliath", Name: "Goliath", Sizes: []string{"Medium"}, Speed: 35, Needs: []string{"ancestry"},
		Traits: []Trait{
			{"Giant Ancestry", "You are descended from Giants and have one supernatural boon of your choice, usable a number of times equal to your Proficiency Bonus and regained on a Long Rest."},
			{"Large Form", "From level 5, as a Bonus Action you can become Large for 10 minutes if there is space: you have Advantage on Strength checks and your Speed increases by 10 feet. Once used, you cannot use it again until you finish a Long Rest."},
			{"Powerful Build", "You have Advantage on ability checks to end the Grappled condition, and you count as one size larger for carrying capacity."},
		},
	},
	"halfling": {
		Key: "halfling", Name: "Halfling", Sizes: []string{"Small"}, Speed: 30,
		Traits: []Trait{
			{"Brave", "You have Advantage on saving throws to avoid or end the Frightened condition."},
			{"Halfling Nimbleness", "You can move through the space of any creature that is a size larger than you, but you can't stop in the same space."},
			{"Luck", "When you roll a 1 on the d20 of a D20 Test, you can reroll the die and must use the new roll."},
			{"Naturally Stealthy", "You can take the Hide action even when you are obscured only by a creature at least one size larger than you."},
		},
	},
	"human": {
		Key: "human", Name: "Human", Sizes: []string{"Medium", "Small"}, Speed: 30, Needs: []string{"size", "skill", "feat"},
		Traits: []Trait{
			{"Resourceful", "You gain Heroic Inspiration whenever you finish a Long Rest."},
			{"Skillful", "You gain proficiency in one skill of your choice."},
			{"Versatile", "You gain an Origin feat of your choice. Skilled is recommended."},
		},
	},
	"orc": {
		Key: "orc", Name: "Orc", Sizes: []string{"Medium"}, Speed: 30, Darkvision: 120,
		Traits: []Trait{
			{"Adrenaline Rush", "You can take the Dash action as a Bonus Action and gain Temporary Hit Points equal to your Proficiency Bonus when you do. You can use this a number of times equal to your Proficiency Bonus and regain all uses on a Short or Long Rest."},
			{"Darkvision", "You have Darkvision with a range of 120 feet."},
			{"Relentless Endurance", "When you are reduced to 0 Hit Points but not killed outright, you can drop to 1 Hit Point instead. Once used, you cannot do so again until you finish a Long Rest."},
		},
	},
	"tiefling": {
		Key: "tiefling", Name: "Tiefling", Sizes: []string{"Medium", "Small"}, Speed: 30, Darkvision: 60, Needs: []string{"legacy", "spellcasting_ability", "size"},
		Traits: []Trait{
			{"Darkvision", "You have Darkvision with a range of 60 feet."},
			{"Fiendish Legacy", "You carry a legacy (Abyssal, Chthonic or Infernal) that grants supernatural abilities. You gain its level 1 benefit now and learn higher-level spells at character levels 3 and 5, which you always have prepared and can cast once per Long Rest without a spell slot."},
			{"Otherworldly Presence", "You know the Thaumaturgy cantrip, cast with the same spellcasting ability as your Fiendish Legacy."},
		},
	},
}

// species names that are not in the SRD 5.2.1, with the species to use instead
var retiredSpecies = map[string]string{
	"half_orc": "orc", "half_elf": "elf or human", "hill_dwarf": "dwarf", "mountain_dwarf": "dwarf",
	"high_elf": "elf (lineage high_elf)", "wood_elf": "elf (lineage wood_elf)", "dark_elf": "elf (lineage drow)", "drow": "elf (lineage drow)",
	"lightfoot": "halfling", "stout": "halfling", "rock_gnome": "gnome (lineage rock)", "forest_gnome": "gnome (lineage forest)",
}

func normalizeSpeciesKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "_")
	return strings.ReplaceAll(s, " ", "_")
}

// GetSpecies returns a species by name, or nil.
func GetSpecies(name string) *Species {
	if sp, ok := species[normalizeSpeciesKey(name)]; ok {
		return &sp
	}
	return nil
}

// AllSpecies returns the species in alphabetical order.
func AllSpecies() []Species {
	keys := make([]string, 0, len(species))
	for k := range species {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Species, 0, len(keys))
	for _, k := range keys {
		out = append(out, species[k])
	}
	return out
}

// RetiredSpeciesHint says which species to use for a name that isn't an SRD 5.2.1 species ("" if none).
func RetiredSpeciesHint(name string) string { return retiredSpecies[normalizeSpeciesKey(name)] }

func pick(choices map[string]string, key string) string {
	return strings.ToLower(strings.TrimSpace(choices[key]))
}

func oneOf(v string, options ...string) bool {
	for _, o := range options {
		if v == o {
			return true
		}
	}
	return false
}

// ResolveSpecies applies the player's choices to a species and checks them.
// The Human's Origin feat is only returned here; the caller checks that it is a real Origin feat.
func ResolveSpecies(name string, choices map[string]string) (*SpeciesResult, error) {
	sp := GetSpecies(name)
	if sp == nil {
		return nil, fmt.Errorf("unknown species %q", name)
	}
	res := &SpeciesResult{Key: sp.Key, Name: sp.Name, Size: sp.Sizes[0], Speed: sp.Speed, Darkvision: sp.Darkvision,
		Traits: append([]Trait(nil), sp.Traits...), Choices: map[string]string{}, LevelSpells: map[int][]string{}}
	need := func(key string, options ...string) (string, error) {
		v := pick(choices, key)
		v = strings.ReplaceAll(strings.ReplaceAll(v, " ", "_"), "-", "_")
		if !oneOf(v, options...) {
			return "", fmt.Errorf("%s needs species_choices.%s: one of %s", sp.Name, key, strings.Join(options, ", "))
		}
		res.Choices[key] = v
		return v, nil
	}
	spellAbility := func() error {
		v, err := need("spellcasting_ability", "int", "wis", "cha")
		res.SpellAbility = v
		return err
	}

	switch sp.Key {
	case "dragonborn":
		colors := make([]string, 0, len(DragonAncestors))
		for c := range DragonAncestors {
			colors = append(colors, c)
		}
		sort.Strings(colors)
		c, err := need("ancestry", colors...)
		if err != nil {
			return nil, err
		}
		res.Resistances = []string{DragonAncestors[c]}
		res.Traits[0].Description += " Chosen: " + c + " dragon (" + DragonAncestors[c] + ")."
	case "dwarf":
		res.HPPerLevel = 1
		res.Resistances = []string{"poison"} // Dwarven Resilience
	case "elf":
		l, err := need("lineage", "drow", "high_elf", "wood_elf")
		if err != nil {
			return nil, err
		}
		if err := spellAbility(); err != nil {
			return nil, err
		}
		skill, err := need("skill", "insight", "perception", "survival")
		if err != nil {
			return nil, err
		}
		res.Skills = []string{skill}
		switch l {
		case "drow":
			res.Darkvision = 120
			res.Cantrips = []string{"dancing-lights"}
			res.LevelSpells[3] = []string{"faerie-fire"}
			res.LevelSpells[5] = []string{"darkness"}
		case "high_elf":
			res.Cantrips = []string{"prestidigitation"}
			res.LevelSpells[3] = []string{"detect-magic"}
			res.LevelSpells[5] = []string{"misty-step"}
		case "wood_elf":
			res.Speed = 35
			res.Cantrips = []string{"druidcraft"}
			res.LevelSpells[3] = []string{"longstrider"}
			res.LevelSpells[5] = []string{"pass-without-trace"}
		}
		res.Traits[1].Description += " Chosen: " + strings.ReplaceAll(l, "_", " ") + "."
	case "gnome":
		l, err := need("lineage", "forest", "rock")
		if err != nil {
			return nil, err
		}
		if err := spellAbility(); err != nil {
			return nil, err
		}
		if l == "forest" {
			res.Cantrips = []string{"minor-illusion"}
			res.Traits = append(res.Traits, Trait{"Speak with Animals", "You always have Speak with Animals prepared and can cast it without a spell slot a number of times equal to your Proficiency Bonus, regaining all uses on a Long Rest."})
		} else {
			res.Cantrips = []string{"mending", "prestidigitation"}
			res.Traits = append(res.Traits, Trait{"Clockwork Device", "You can spend 10 minutes casting Prestidigitation to create a Tiny clockwork device (AC 5, 1 HP), such as a toy, fire starter or music box, with one Prestidigitation effect."})
		}
		res.Traits[2].Description += " Chosen: " + l + " gnome."
	case "goliath":
		a, err := need("ancestry", giantAncestries...)
		if err != nil {
			return nil, err
		}
		boon := map[string]string{
			"cloud": "Cloud's Jaunt: as a Bonus Action, teleport up to 30 feet to an unoccupied space you can see.",
			"fire":  "Fire's Burn: when you hit with an attack roll and deal damage, you can deal 1d10 extra Fire damage.",
			"frost": "Frost's Chill: when you hit and deal damage, you can deal 1d6 extra Cold damage and reduce the target's Speed by 10 feet until the start of your next turn.",
			"hill":  "Hill's Tumble: when you hit a Large or smaller creature and deal damage, you can give it the Prone condition.",
			"stone": "Stone's Endurance: when you take damage, you can use your Reaction to roll 1d12, add your Constitution modifier, and reduce the damage by that total.",
			"storm": "Storm's Thunder: when you take damage from a creature within 60 feet, you can use your Reaction to deal 1d8 Thunder damage to it.",
		}[a]
		res.Traits[0].Description += " Chosen: " + boon
	case "human":
		s, err := need("size", "medium", "small")
		if err != nil {
			return nil, err
		}
		res.Size = strings.ToUpper(s[:1]) + s[1:] // "medium" -> "Medium" (strings.Title is deprecated)
		if pick(choices, "skill") == "" {
			return nil, fmt.Errorf("Human needs species_choices.skill: any skill")
		}
		sk := pick(choices, "skill")
		if !IsSkill(sk) {
			return nil, fmt.Errorf("Human species_choices.skill %q is not a skill", sk)
		}
		res.Choices["skill"] = sk
		res.Skills = []string{sk}
		f := strings.ReplaceAll(pick(choices, "feat"), "-", "_")
		f = strings.ReplaceAll(f, " ", "_")
		if f == "" {
			return nil, fmt.Errorf("Human needs species_choices.feat: an Origin feat (skilled is recommended)")
		}
		res.Choices["feat"] = f
		res.Feat = f
	case "orc":
	case "halfling":
	case "tiefling":
		l, err := need("legacy", "abyssal", "chthonic", "infernal")
		if err != nil {
			// "lineage" is accepted as another name for the legacy
			if v := pick(choices, "lineage"); v != "" {
				choices = map[string]string{"legacy": v, "spellcasting_ability": choices["spellcasting_ability"], "size": choices["size"]}
				l, err = need("legacy", "abyssal", "chthonic", "infernal")
			}
			if err != nil {
				return nil, err
			}
		}
		if err := spellAbility(); err != nil {
			return nil, err
		}
		s, err := need("size", "medium", "small")
		if err != nil {
			return nil, err
		}
		res.Size = strings.ToUpper(s[:1]) + s[1:] // "medium" -> "Medium" (strings.Title is deprecated)
		res.Cantrips = []string{"thaumaturgy"}
		switch l {
		case "abyssal":
			res.Resistances = []string{"poison"}
			res.Cantrips = append(res.Cantrips, "poison-spray")
			res.LevelSpells[3] = []string{"ray-of-sickness"}
			res.LevelSpells[5] = []string{"hold-person"}
		case "chthonic":
			res.Resistances = []string{"necrotic"}
			res.Cantrips = append(res.Cantrips, "chill-touch")
			res.LevelSpells[3] = []string{"false-life"}
			res.LevelSpells[5] = []string{"ray-of-enfeeblement"}
		case "infernal":
			res.Resistances = []string{"fire"}
			res.Cantrips = append(res.Cantrips, "fire-bolt")
			res.LevelSpells[3] = []string{"hellish-rebuke"}
			res.LevelSpells[5] = []string{"darkness"}
		}
		res.Traits[1].Description += " Chosen: " + l + "."
	}
	if len(res.LevelSpells) == 0 {
		res.LevelSpells = nil
	}
	return res, nil
}

// IsSkill reports whether a name is one of the 18 skills.
func IsSkill(name string) bool {
	name = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(name, "_", " ")))
	for _, s := range []string{"acrobatics", "animal handling", "arcana", "athletics", "deception", "history", "insight", "intimidation",
		"investigation", "medicine", "nature", "perception", "performance", "persuasion", "religion", "sleight of hand", "stealth", "survival"} {
		if s == name {
			return true
		}
	}
	return false
}

// StandardLanguages is the Standard Languages table (Common is always known).
var StandardLanguages = []string{"Common Sign Language", "Draconic", "Dwarvish", "Elvish", "Giant", "Gnomish", "Goblin", "Halfling", "Orc"}

// ValidateLanguages checks the two chosen languages and returns the full list (Common first).
func ValidateLanguages(chosen []string) ([]string, error) {
	out := []string{"Common"}
	seen := map[string]bool{}
	for _, l := range chosen {
		l = strings.TrimSpace(l)
		if l == "" || strings.EqualFold(l, "common") {
			continue
		}
		var canon string
		for _, s := range StandardLanguages {
			if strings.EqualFold(s, l) {
				canon = s
			}
		}
		if canon == "" {
			return nil, fmt.Errorf("%q is not a Standard Language (%s)", l, strings.Join(StandardLanguages, ", "))
		}
		if !seen[canon] {
			seen[canon] = true
			out = append(out, canon)
		}
	}
	if len(out) != 3 {
		return nil, fmt.Errorf("choose exactly two languages besides Common from: %s", strings.Join(StandardLanguages, ", "))
	}
	return out, nil
}

// SpeciesCantrips: the cantrips a species' lineage or legacy gives at level 1 (SRD 5.2.1), so a builder can warn before a feat
// picks the same one. choice is the lineage (elf, gnome) or legacy (tiefling).
func SpeciesCantrips(species, choice string) []string {
	switch normalizeSpeciesKey(species) + "/" + strings.ToLower(strings.TrimSpace(choice)) {
	case "elf/drow":
		return []string{"dancing-lights"}
	case "elf/high_elf", "elf/high elf":
		return []string{"prestidigitation"}
	case "elf/wood_elf", "elf/wood elf":
		return []string{"druidcraft"}
	case "gnome/forest":
		return []string{"minor-illusion"}
	case "gnome/rock":
		return []string{"mending", "prestidigitation"}
	case "tiefling/abyssal":
		return []string{"thaumaturgy", "poison-spray"}
	case "tiefling/chthonic":
		return []string{"thaumaturgy", "chill-touch"}
	case "tiefling/infernal":
		return []string{"thaumaturgy", "fire-bolt"}
	}
	if normalizeSpeciesKey(species) == "tiefling" {
		return []string{"thaumaturgy"}
	}
	return nil
}
