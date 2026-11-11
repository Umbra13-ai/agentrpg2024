// Package game provides the core game rules (SRD 5.2.1).
// feats.go - feat definitions and mechanics
package game



// Feat represents a character feat from the SRD/PHB
type Feat struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Prerequisite string            `json:"prerequisite,omitempty"` // e.g., "str:13" or "spellcaster" or ""
	Benefits     []string          `json:"benefits"`
	AbilityBonus map[string]int    `json:"ability_bonus,omitempty"` // e.g., {"str": 1}
	Features     map[string]string `json:"features,omitempty"`      // Special features to track
}

// AvailableFeats contains all available feats - SRD only has Grappler, but we include common ones for gameplay
var AvailableFeats = map[string]Feat{
	"skilled": {
		Name:        "Skilled",
		Description: "Origin feat (2024). You gain proficiency in any combination of three skills or tools of your choice. Repeatable.",
		Benefits: []string{
			"You gain proficiency in any combination of three skills or tools of your choice",
		},
		Features: map[string]string{
			"origin_feat":   "true",
			"proficiencies": "3",
		},
	},
	"magic_initiate": {
		Name:        "Magic Initiate",
		Description: "Origin feat (2024). You learn a little magic from the Cleric, Druid or Wizard spell list. Repeatable, but you must choose a different list each time.",
		Benefits: []string{
			"Two Cantrips: you learn two cantrips of your choice from the Cleric, Druid or Wizard spell list",
			"Spellcasting ability: Intelligence, Wisdom or Charisma (choose when you select the feat)",
			"Level 1 Spell: choose a level 1 spell from the same list. You always have it prepared, can cast it once without a spell slot per Long Rest, and can also cast it with spell slots",
			"Spell Change: whenever you gain a level, you can replace one of the spells you chose for this feat with another of the same level from the same list",
		},
		Features: map[string]string{
			"origin_feat":  "true",
			"cantrips":     "2",
			"level1_spell": "1",
		},
	},
	"grappler": {
		Name:         "Grappler",
		Description:  "You've developed the skills necessary to hold your own in close-quarters grappling.",
		Prerequisite: "str:13",
		Benefits: []string{
			"You have advantage on attack rolls against a creature you are grappling",
			"You can use your action to try to pin a creature grappled by you. Make another grapple check. If you succeed, you and the creature are both restrained until the grapple ends",
		},
		Features: map[string]string{
			"grapple_advantage": "true",
			"can_pin":           "true",
		},
	},
	"alert": {
		Name:        "Alert",
		Description: "Origin feat (2024). Always on the lookout for danger.",
		Benefits: []string{
			"Initiative Proficiency: when you roll Initiative, you can add your Proficiency Bonus to the roll",
			"Initiative Swap: immediately after you roll Initiative, you can swap your Initiative with a willing ally in the same combat (neither of you can be Incapacitated)",
		},
		Features: map[string]string{
			"initiative_bonus": "proficiency",
			"initiative_swap":  "true",
		},
	},
	"lucky": {
		Name:        "Lucky",
		Description: "You have inexplicable luck that seems to kick in at just the right moment.",
		Benefits: []string{
			"You have 3 luck points. Whenever you make an attack roll, ability check, or saving throw, you can spend one luck point to roll an additional d20",
			"You can also spend one luck point when an attack roll is made against you to roll a d20 and choose whether to use your roll or the attacker's",
			"You regain all luck points after a long rest",
		},
		Features: map[string]string{
			"luck_points":     "3",
			"luck_points_max": "3",
		},
	},
	"tough": {
		Name:        "Tough",
		Description: "Your hit point maximum increases.",
		Benefits: []string{
			"Your hit point maximum increases by an amount equal to twice your level when you gain this feat",
			"Whenever you gain a level thereafter, your hit point maximum increases by an additional 2 hit points",
		},
		Features: map[string]string{
			"hp_bonus_per_level": "2",
		},
	},
	"sentinel": {
		Name:        "Sentinel",
		Description: "You have mastered techniques to take advantage of every drop in any enemy's guard.",
		Benefits: []string{
			"When you hit a creature with an opportunity attack, the creature's speed becomes 0 for the rest of the turn",
			"Creatures provoke opportunity attacks from you even if they take the Disengage action",
			"When a creature within 5 feet of you makes an attack against a target other than you, you can use your reaction to make a melee weapon attack against the attacking creature",
		},
		Features: map[string]string{
			"opportunity_stops_movement": "true",
			"ignore_disengage":           "true",
			"protect_allies_reaction":    "true",
		},
	},
	"war_caster": {
		Name:         "War Caster",
		Description:  "You have practiced casting spells in the midst of combat.",
		Prerequisite: "spellcaster",
		Benefits: []string{
			"You have advantage on Constitution saving throws that you make to maintain concentration on a spell when you take damage",
			"You can perform the somatic components of spells even when you have weapons or a shield in one or both hands",
			"When a hostile creature's movement provokes an opportunity attack from you, you can use your reaction to cast a spell at the creature rather than making an opportunity attack",
		},
		Features: map[string]string{
			"concentration_advantage": "true",
			"somatic_with_hands_full": "true",
			"spell_opportunity":       "true",
		},
	},
	"mobile": {
		Name:        "Mobile",
		Description: "You are exceptionally speedy and agile.",
		Benefits: []string{
			"Your speed increases by 10 feet",
			"When you use the Dash action, difficult terrain doesn't cost you extra movement",
			"When you make a melee attack against a creature, you don't provoke opportunity attacks from that creature for the rest of the turn, whether you hit or not",
		},
		Features: map[string]string{
			"speed_bonus":           "10",
			"ignore_difficult_dash": "true",
			"no_opportunity_melee":  "true",
		},
	},
	"observant": {
		Name:        "Observant",
		Description: "Quick to notice details of your environment, you gain the following benefits.",
		AbilityBonus: map[string]int{
			"int_or_wis": 1, // Player chooses INT or WIS
		},
		Benefits: []string{
			"Increase your Intelligence or Wisdom by 1, to a maximum of 20",
			"If you can see a creature's mouth while it is speaking a language you understand, you can interpret what it's saying by reading its lips",
			"You have a +5 bonus to your passive Wisdom (Perception) and passive Intelligence (Investigation) scores",
		},
		Features: map[string]string{
			"passive_bonus": "5",
			"read_lips":     "true",
		},
	},
	"resilient": {
		Name:        "Resilient",
		Description: "Choose one ability score. You gain proficiency in saving throws using that ability.",
		AbilityBonus: map[string]int{
			"chosen": 1, // Player chooses which ability
		},
		Benefits: []string{
			"Increase the chosen ability score by 1, to a maximum of 20",
			"You gain proficiency in saving throws using the chosen ability",
		},
		Features: map[string]string{
			"save_proficiency": "chosen", // Stored as actual ability when taken
		},
	},
	"savage_attacker": {
		Name:        "Savage Attacker",
		Description: "Origin feat (2024). You've trained to deal particularly damaging strikes.",
		Benefits: []string{
			"Once per turn when you hit a target with a weapon, you can roll the weapon's damage dice twice and use either roll against the target",
		},
		Features: map[string]string{
			"reroll_melee_damage": "true",
		},
	},
}

// HasFeatFeature checks if any of the character's feats grants a specific feature
// Used for War Caster somatic component bypass, etc.
func HasFeatFeature(feats []string, feature string) bool {
	for _, slug := range feats {
		if feat, ok := AvailableFeats[slug]; ok {
			if _, hasFeature := feat.Features[feature]; hasFeature {
				return true
			}
		}
	}
	return false
}

