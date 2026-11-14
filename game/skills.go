package game

import "strings"

// Skill is one row of the SRD 5.2.1 Skills table. This table is the only list of the skills in the server: every other list,
// name check and skill-to-ability lookup reads it.
type Skill struct {
	Name        string // as stored on a character: "sleight of hand"
	Ability     string // "dex"
	AbilityName string // "Dexterity"
	Examples    string // the table's Example Uses
}

// Skills: the 18 skills in the SRD's order.
var Skills = []Skill{
	{"acrobatics", "dex", "Dexterity", "Stay on your feet in a tricky situation, or perform an acrobatic stunt."},
	{"animal handling", "wis", "Wisdom", "Calm or train an animal, or get an animal to behave in a certain way."},
	{"arcana", "int", "Intelligence", "Recall lore about spells, magic items, and the planes of existence."},
	{"athletics", "str", "Strength", "Jump farther than normal, stay afloat in rough water, or break something."},
	{"deception", "cha", "Charisma", "Tell a convincing lie, or wear a disguise convincingly."},
	{"history", "int", "Intelligence", "Recall lore about historical events, people, nations, and cultures."},
	{"insight", "wis", "Wisdom", "Discern a person’s mood and intentions."},
	{"intimidation", "cha", "Charisma", "Awe or threaten someone into doing what you want."},
	{"investigation", "int", "Intelligence", "Find obscure information in books, or deduce how something works."},
	{"medicine", "wis", "Wisdom", "Diagnose an illness, or determine what killed the recently slain."},
	{"nature", "int", "Intelligence", "Recall lore about terrain, plants, animals, and weather."},
	{"perception", "wis", "Wisdom", "Using a combination of senses, notice something that’s easy to miss."},
	{"performance", "cha", "Charisma", "Act, tell a story, perform music, or dance."},
	{"persuasion", "cha", "Charisma", "Honestly and graciously convince someone of something."},
	{"religion", "int", "Intelligence", "Recall lore about gods, religious rituals, and holy symbols."},
	{"sleight of hand", "dex", "Dexterity", "Pick a pocket, conceal a handheld object, or perform legerdemain."},
	{"stealth", "dex", "Dexterity", "Escape notice by moving quietly and hiding behind things."},
	{"survival", "wis", "Wisdom", "Follow tracks, forage, find a trail, or avoid natural hazards."},
}

var skillsByKey = func() map[string]Skill {
	m := map[string]Skill{}
	for _, s := range Skills {
		m[s.Name] = s
		m[strings.ReplaceAll(s.Name, " ", "")] = s // "sleightofhand", "animalhandling"
	}
	return m
}()

// SkillNames: the 18 skill names in the SRD's order (a fresh list each call).
func SkillNames() []string {
	out := make([]string, len(Skills))
	for i, s := range Skills {
		out[i] = s.Name
	}
	return out
}

// SkillName folds a spelling to the form skills are stored in: lowercase, "_" and "-" read as spaces, single spaces
// ("Sleight_of-Hand" -> "sleight of hand"). It does not check that the result is a skill; LookupSkill does.
func SkillName(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.NewReplacer("_", " ", "-", " ").Replace(s))), " ")
}

// LookupSkill: the skill a name means, in any spelling ("Sleight of Hand", "sleight_of_hand", "sleight-of-hand",
// "sleightofhand").
func LookupSkill(s string) (Skill, bool) {
	sk, ok := skillsByKey[SkillName(s)]
	return sk, ok
}

// CanonSkill: the stored name of the skill a spelling means ("Sleight-of-Hand" -> "sleight of hand"); "" and false when it is
// not one of the 18 skills.
func CanonSkill(s string) (string, bool) {
	sk, ok := LookupSkill(s)
	return sk.Name, ok
}

// IsSkill reports whether a name is one of the 18 skills, in any spelling.
func IsSkill(name string) bool {
	_, ok := LookupSkill(name)
	return ok
}

// SkillSlug: a skill's name with underscores ("sleight_of_hand"), the form check requests and stat blocks use.
func SkillSlug(name string) string {
	return strings.ReplaceAll(SkillName(name), " ", "_")
}
