// Package game provides the core game rules (SRD 5.2.1).
// warlock.go - Eldritch Invocations (SRD 5.2.1; the pacts of the Blade, Chain and Tome are invocations)
package game

// InvocationPrerequisites represents the requirements for an Eldritch Invocation
type InvocationPrerequisites struct {
	Level          int    `json:"level,omitempty"`           // Minimum warlock level
	Pact           string `json:"pact,omitempty"`            // Required pact boon (blade, chain, tome)
	RequiresSpell  string `json:"requires_spell,omitempty"`  // Must know a specific spell
	RequiresPatron string `json:"requires_patron,omitempty"` // Must have specific patron
}

// EldritchInvocation represents a Warlock Eldritch Invocation
type EldritchInvocation struct {
	Slug          string                  `json:"slug"`
	Name          string                  `json:"name"`
	Description   string                  `json:"description"`
	Prerequisites InvocationPrerequisites `json:"prerequisites,omitempty"`
	Mechanics     map[string]string       `json:"mechanics,omitempty"` // Mechanical effects
}

// AvailableInvocations contains all SRD Eldritch Invocations
var AvailableInvocations = map[string]EldritchInvocation{
	"agonizing-blast": {
		Slug:          "agonizing-blast",
		Name:          "Agonizing Blast",
		Description:   "When you cast eldritch blast, add your Charisma modifier to the damage it deals on a hit.",
		Prerequisites: InvocationPrerequisites{RequiresSpell: "eldritch-blast"},
		Mechanics:     map[string]string{"agonizing_blast": "true"},
	},
	"armor-of-shadows": {
		Slug:        "armor-of-shadows",
		Name:        "Armor of Shadows",
		Description: "You can cast mage armor on yourself at will, without expending a spell slot or material components.",
		Mechanics:   map[string]string{"at_will_spell": "mage-armor"},
	},
	"beast-speech": {
		Slug:        "beast-speech",
		Name:        "Beast Speech",
		Description: "You can cast speak with animals at will, without expending a spell slot.",
		Mechanics:   map[string]string{"at_will_spell": "speak-with-animals"},
	},
	"beguiling-influence": {
		Slug:        "beguiling-influence",
		Name:        "Beguiling Influence",
		Description: "You gain proficiency in the Deception and Persuasion skills.",
		Mechanics:   map[string]string{"grant_proficiency": "deception,persuasion"},
	},
	"devils-sight": {
		Slug:        "devils-sight",
		Name:        "Devil's Sight",
		Description: "You can see normally in darkness, both magical and nonmagical, to a distance of 120 feet.",
		Mechanics:   map[string]string{"devils_sight": "120"},
	},
	"eldritch-sight": {
		Slug:        "eldritch-sight",
		Name:        "Eldritch Sight",
		Description: "You can cast detect magic at will, without expending a spell slot.",
		Mechanics:   map[string]string{"at_will_spell": "detect-magic"},
	},
	"eldritch-spear": {
		Slug:          "eldritch-spear",
		Name:          "Eldritch Spear",
		Description:   "When you cast eldritch blast, its range is 300 feet.",
		Prerequisites: InvocationPrerequisites{RequiresSpell: "eldritch-blast"},
		Mechanics:     map[string]string{"eldritch_spear": "300"},
	},
	"eyes-of-the-rune-keeper": {
		Slug:        "eyes-of-the-rune-keeper",
		Name:        "Eyes of the Rune Keeper",
		Description: "You can read all writing.",
		Mechanics:   map[string]string{"read_all_writing": "true"},
	},
	"fiendish-vigor": {
		Slug:        "fiendish-vigor",
		Name:        "Fiendish Vigor",
		Description: "You can cast false life on yourself at will as a 1st-level spell, without expending a spell slot or material components.",
		Mechanics:   map[string]string{"at_will_spell": "false-life"},
	},
	"gaze-of-two-minds": {
		Slug:        "gaze-of-two-minds",
		Name:        "Gaze of Two Minds",
		Description: "You can use your action to touch a willing humanoid and perceive through its senses until the end of your next turn. As long as the creature is on the same plane of existence as you, you can use your action on subsequent turns to maintain this connection, extending the duration until the end of your next turn.",
		Mechanics:   map[string]string{"gaze_of_two_minds": "true"},
	},
	"mask-of-many-faces": {
		Slug:        "mask-of-many-faces",
		Name:        "Mask of Many Faces",
		Description: "You can cast disguise self at will, without expending a spell slot.",
		Mechanics:   map[string]string{"at_will_spell": "disguise-self"},
	},
	"misty-visions": {
		Slug:        "misty-visions",
		Name:        "Misty Visions",
		Description: "You can cast silent image at will, without expending a spell slot or material components.",
		Mechanics:   map[string]string{"at_will_spell": "silent-image"},
	},
	"repelling-blast": {
		Slug:          "repelling-blast",
		Name:          "Repelling Blast",
		Description:   "When you hit a creature with eldritch blast, you can push the creature up to 10 feet away from you in a straight line.",
		Prerequisites: InvocationPrerequisites{RequiresSpell: "eldritch-blast"},
		Mechanics:     map[string]string{"repelling_blast": "10"},
	},
	"thief-of-five-fates": {
		Slug:        "thief-of-five-fates",
		Name:        "Thief of Five Fates",
		Description: "You can cast bane once using a warlock spell slot. You can't do so again until you finish a long rest.",
		Mechanics:   map[string]string{"once_per_rest_spell": "bane"},
	},
	// Level 5+ invocations
	"mire-the-mind": {
		Slug:          "mire-the-mind",
		Name:          "Mire the Mind",
		Description:   "You can cast slow once using a warlock spell slot. You can't do so again until you finish a long rest.",
		Prerequisites: InvocationPrerequisites{Level: 5},
		Mechanics:     map[string]string{"once_per_rest_spell": "slow"},
	},
	"one-with-shadows": {
		Slug:          "one-with-shadows",
		Name:          "One with Shadows",
		Description:   "When you are in an area of dim light or darkness, you can use your action to become invisible until you move or take an action or a reaction.",
		Prerequisites: InvocationPrerequisites{Level: 5},
		Mechanics:     map[string]string{"one_with_shadows": "true"},
	},
	"sign-of-ill-omen": {
		Slug:          "sign-of-ill-omen",
		Name:          "Sign of Ill Omen",
		Description:   "You can cast bestow curse once using a warlock spell slot. You can't do so again until you finish a long rest.",
		Prerequisites: InvocationPrerequisites{Level: 5},
		Mechanics:     map[string]string{"once_per_rest_spell": "bestow-curse"},
	},
	// Level 7+ invocations
	"sculptor-of-flesh": {
		Slug:          "sculptor-of-flesh",
		Name:          "Sculptor of Flesh",
		Description:   "You can cast polymorph once using a warlock spell slot. You can't do so again until you finish a long rest.",
		Prerequisites: InvocationPrerequisites{Level: 7},
		Mechanics:     map[string]string{"once_per_rest_spell": "polymorph"},
	},
	// Level 9+ invocations
	"ascendant-step": {
		Slug:          "ascendant-step",
		Name:          "Ascendant Step",
		Description:   "You can cast levitate on yourself at will, without expending a spell slot or material components.",
		Prerequisites: InvocationPrerequisites{Level: 9},
		Mechanics:     map[string]string{"at_will_spell": "levitate"},
	},
	"minions-of-chaos": {
		Slug:          "minions-of-chaos",
		Name:          "Minions of Chaos",
		Description:   "You can cast conjure elemental once using a warlock spell slot. You can't do so again until you finish a long rest.",
		Prerequisites: InvocationPrerequisites{Level: 9},
		Mechanics:     map[string]string{"once_per_rest_spell": "conjure-elemental"},
	},
	"otherworldly-leap": {
		Slug:          "otherworldly-leap",
		Name:          "Otherworldly Leap",
		Description:   "You can cast jump on yourself at will, without expending a spell slot or material components.",
		Prerequisites: InvocationPrerequisites{Level: 9},
		Mechanics:     map[string]string{"at_will_spell": "jump"},
	},
	"whispers-of-the-grave": {
		Slug:          "whispers-of-the-grave",
		Name:          "Whispers of the Grave",
		Description:   "You can cast speak with dead at will, without expending a spell slot.",
		Prerequisites: InvocationPrerequisites{Level: 9},
		Mechanics:     map[string]string{"at_will_spell": "speak-with-dead"},
	},
	// Level 12+ invocations
	"lifedrinker": {
		Slug:          "lifedrinker",
		Name:          "Lifedrinker",
		Description:   "When you hit a creature with your pact weapon, the creature takes extra necrotic damage equal to your Charisma modifier (minimum 1).",
		Prerequisites: InvocationPrerequisites{Level: 12, Pact: "blade"},
		Mechanics:     map[string]string{"lifedrinker": "true"},
	},
	// Level 15+ invocations
	"master-of-myriad-forms": {
		Slug:          "master-of-myriad-forms",
		Name:          "Master of Myriad Forms",
		Description:   "You can cast alter self at will, without expending a spell slot.",
		Prerequisites: InvocationPrerequisites{Level: 15},
		Mechanics:     map[string]string{"at_will_spell": "alter-self"},
	},
	"visions-of-distant-realms": {
		Slug:          "visions-of-distant-realms",
		Name:          "Visions of Distant Realms",
		Description:   "You can cast arcane eye at will, without expending a spell slot.",
		Prerequisites: InvocationPrerequisites{Level: 15},
		Mechanics:     map[string]string{"at_will_spell": "arcane-eye"},
	},
	"witch-sight": {
		Slug:          "witch-sight",
		Name:          "Witch Sight",
		Description:   "You can see the true form of any shapechanger or creature concealed by illusion or transmutation magic while the creature is within 30 feet of you and within line of sight.",
		Prerequisites: InvocationPrerequisites{Level: 15},
		Mechanics:     map[string]string{"witch_sight": "30"},
	},
}


// GetMaxInvocations returns how many invocations a Warlock can have at their level
func GetMaxInvocations(warlockLevel int) int {
	// 2024: the Eldritch Invocations column of the Warlock's Features table
	if n := ClassTableInt("warlock", warlockLevel, "Eldritch Invocations"); n > 0 {
		return n
	}
	switch {
	case warlockLevel >= 18:
		return 8
	case warlockLevel >= 15:
		return 7
	case warlockLevel >= 12:
		return 6
	case warlockLevel >= 9:
		return 5
	case warlockLevel >= 7:
		return 4
	case warlockLevel >= 5:
		return 3
	case warlockLevel >= 2:
		return 2
	default:
		return 0
	}
}

