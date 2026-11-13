package main

// The SRD 5.2.1's own words for character creation (Nov 12). Matt: use the SRD's descriptions, not ones we write.
// Every string here is copied from the System Reference Document 5.2.1 (Creative Commons): the Skills, Ability Descriptions, Ability Check
// Examples and Saving Throw Examples tables; the Divine and Primal Order text; the nine alignments; the tool descriptions; the Origin feats;
// the Light and Shield rules; and "Imagine Your Past and Present". Only the glue around them (names, field labels) is ours.

// srdSkillRows: the Skills table (Skill, Ability, Example Uses).
var srdSkillRows = map[string][2]string{
	"acrobatics":      {"Dexterity", "Stay on your feet in a tricky situation, or perform an acrobatic stunt."},
	"animal handling": {"Wisdom", "Calm or train an animal, or get an animal to behave in a certain way."},
	"arcana":          {"Intelligence", "Recall lore about spells, magic items, and the planes of existence."},
	"athletics":       {"Strength", "Jump farther than normal, stay afloat in rough water, or break something."},
	"deception":       {"Charisma", "Tell a convincing lie, or wear a disguise convincingly."},
	"history":         {"Intelligence", "Recall lore about historical events, people, nations, and cultures."},
	"insight":         {"Wisdom", "Discern a person’s mood and intentions."},
	"intimidation":    {"Charisma", "Awe or threaten someone into doing what you want."},
	"investigation":   {"Intelligence", "Find obscure information in books, or deduce how something works."},
	"medicine":        {"Wisdom", "Diagnose an illness, or determine what killed the recently slain."},
	"nature":          {"Intelligence", "Recall lore about terrain, plants, animals, and weather."},
	"perception":      {"Wisdom", "Using a combination of senses, notice something that’s easy to miss."},
	"performance":     {"Charisma", "Act, tell a story, perform music, or dance."},
	"persuasion":      {"Charisma", "Honestly and graciously convince someone of something."},
	"religion":        {"Intelligence", "Recall lore about gods, religious rituals, and holy symbols."},
	"sleight of hand": {"Dexterity", "Pick a pocket, conceal a handheld object, or perform legerdemain."},
	"stealth":         {"Dexterity", "Escape notice by moving quietly and hiding behind things."},
	"survival":        {"Wisdom", "Follow tracks, forage, find a trail, or avoid natural hazards."},
}

// srdAbilityRows: Ability Descriptions (Score Measures), Ability Check Examples (Make a Check To), Saving Throw Examples (Make a Save To).
var srdAbilityRows = map[string][3]string{
	"str": {"Physical might", "Lift, push, pull, or break something", "Physically resist direct force"},
	"dex": {"Agility, reflexes, and balance", "Move nimbly, quickly, or quietly", "Dodge out of harm’s way"},
	"con": {"Health and stamina", "Push your body beyond normal limits", "Endure a toxic hazard"},
	"int": {"Reasoning and memory", "Reason or remember", "Recognize an illusion as fake"},
	"wis": {"Perceptiveness and mental fortitude", "Notice things in the environment or in creatures’ behavior", "Resist a mental assault"},
	"cha": {"Confidence, poise, and charm", "Influence, entertain, or deceive", "Assert your identity"},
}

const (
	srdStandardArrayRule  = "Standard Array. Use the following six scores for your abilities: 15, 14, 13, 12, 10, 8."
	srdAssignRule         = "Once you’ve generated six scores, assign them to Strength, Dexterity, Constitution, Intelligence, Wisdom, and Charisma, keeping in mind your class’s primary ability."
	srdStandardArrayTable = "If you’re using the Standard Array option, consult the Standard Array by Class table for suggestions on where to assign scores for your class. The table puts the highest scores in a class’s main abilities."
	srdAdjustRule         = "After assigning your ability scores, adjust them according to your background. Your background lists three abilities; increase one of those scores by 2 and a different one by 1, or increase all three by 1. None of these increases can raise a score above 20."
	srdAlignmentChoose    = "Choose your character’s alignment from the options below, and note it on your character sheet."
	srdAlignmentEvil      = "The game assumes that player characters aren’t of an evil alignment. Check with your GM before making an evil character."
	srdAlignmentIntro     = "A creature’s alignment broadly describes its ethical attitudes and ideals. Alignment is a combination of two factors: one identifies morality (good, evil, or neutral), and the other describes attitudes toward order (lawful, chaotic, or neutral). The summaries of the alignments below describe the typical behavior of a creature with that alignment; individuals can vary from that behavior."
	srdImaginePast        = "Let your character’s background and species inspire how you imagine their past. That past fed into the character’s present. With that in mind, consider answers to the following questions as your character:"
)

var srdImaginePastQuestions = []string{
	"Who raised you?",
	"Who was your dearest childhood friend?",
	"Did you grow up with a pet?",
	"Have you fallen in love? If so, with whom?",
	"Did you join an organization, such as a guild or religion? If so, are you still a member of it?",
	"What elements of your past inspire you to go on adventures now?",
}

// srdAlignments: The Nine Alignments, each summary exactly as the SRD prints it.
var srdAlignments = map[string]string{
	"Lawful Good":     "Lawful Good (LG). Lawful Good creatures endeavor to do the right thing as expected by society. Someone who fights injustice and protects the innocent without hesitation is probably Lawful Good.",
	"Neutral Good":    "Neutral Good (NG). Neutral Good creatures do the best they can, working within rules but not feeling bound by them. A kindly person who helps others according to their needs is probably Neutral Good.",
	"Chaotic Good":    "Chaotic Good (CG). Chaotic Good creatures act as their conscience directs with little regard for what others expect. A rebel who waylays a cruel baron’s tax collectors and uses the stolen money to help the poor is probably Chaotic Good.",
	"Lawful Neutral":  "Lawful Neutral (LN). Lawful Neutral individuals act in accordance with law, tradition, or personal codes. Someone who follows a disciplined rule of life—and isn’t swayed either by the demands of those in need or by the temptations of evil—is probably Lawful Neutral.",
	"Neutral":         "Neutral (N). Neutral is the alignment of those who prefer to avoid moral questions and don’t take sides, doing what seems best at the time. Someone who’s bored by moral debate is probably Neutral.",
	"Chaotic Neutral": "Chaotic Neutral (CN). Chaotic Neutral creatures follow their whims, valuing their personal freedom above all else. A scoundrel who wanders the land living by their wits is probably Chaotic Neutral.",
	"Lawful Evil":     "Lawful Evil (LE). Lawful Evil creatures methodically take what they want within the limits of a code of tradition, loyalty, or order. An aristocrat exploiting citizens while scheming for power is probably Lawful Evil.",
	"Neutral Evil":    "Neutral Evil (NE). Neutral Evil is the alignment of those who are untroubled by the harm they cause as they pursue their desires. A criminal who robs and murders as they please is probably Neutral Evil.",
	"Chaotic Evil":    "Chaotic Evil (CE). Chaotic Evil creatures act with arbitrary violence, spurred by their hatred or bloodlust. A villain pursuing schemes of vengeance and havoc is probably Chaotic Evil.",
}

// srdTools: the tool descriptions (Ability, Utilize, Craft, Variants) as the SRD prints them.
var srdTools = map[string]string{
	"calligrapher's supplies": "Calligrapher’s Supplies. Ability: Dexterity. Utilize: Write text with impressive flourishes that guard against forgery (DC 15). Craft: Ink, Spell Scroll.",
	"thieves' tools":          "Thieves’ Tools. Ability: Dexterity. Utilize: Pick a lock (DC 15), or disarm a trap (DC 15).",
	"gaming set":              "Gaming Set. Ability: Wisdom. Utilize: Discern whether someone is cheating (DC 10), or win the game (DC 20). Variants: Dice (1 SP), dragonchess (1 GP), playing cards (5 SP), three-dragon ante (1 GP).",
}

// srdOriginFeats: the Origin feats exactly as the SRD prints them.
var srdOriginFeats = map[string]string{
	"alert": "Alert (Origin Feat). You gain the following benefits. Initiative Proficiency. When you roll Initiative, you can add your Proficiency Bonus to the roll. Initiative Swap. Immediately after you roll Initiative, you can swap your Initiative with the Initiative of one willing ally in the same combat. You can’t make this swap if you or the ally has the Incapacitated condition.",
	"magic_initiate": "Magic Initiate (Origin Feat). You gain the following benefits. Two Cantrips. You learn two cantrips of your choice from the Cleric, Druid, or Wizard spell list. Intelligence, Wisdom, or Charisma is your spellcasting ability for this feat’s spells (choose when you select this feat). " +
		"Level 1 Spell. Choose a level 1 spell from the same list you selected for this feat’s cantrips. You always have that spell prepared. You can cast it once without a spell slot, and you regain the ability to cast it in that way when you finish a Long Rest. You can also cast the spell using any spell slots you have. " +
		"Spell Change. Whenever you gain a new level, you can replace one of the spells you chose for this feat with a different spell of the same level from the chosen spell list. Repeatable. You can take this feat more than once, but you must choose a different spell list each time.",
	"savage_attacker": "Savage Attacker (Origin Feat). You’ve trained to deal particularly damaging strikes. Once per turn when you hit a target with a weapon, you can roll the weapon’s damage dice twice and use either roll against the target.",
	"skilled":         "Skilled (Origin Feat). You gain proficiency in any combination of three skills or tools of your choice. Repeatable. You can take this feat more than once.",
}

// srdOrders: Level 1: Divine Order (Cleric) and Level 1: Primal Order (Druid), as the SRD prints them.
var srdOrders = map[string]map[string]string{
	"divine_order": {
		"protector":   "Protector. Trained for battle, you gain proficiency with Martial weapons and training with Heavy armor.",
		"thaumaturge": "Thaumaturge. You know one extra cantrip from the Cleric spell list. In addition, your mystical connection to the divine gives you a bonus to your Intelligence (Arcana or Religion) checks. The bonus equals your Wisdom modifier (minimum of +1).",
	},
	"primal_order": {
		"magician": "Magician. You know one extra cantrip from the Druid spell list. In addition, your mystical connection to nature gives you a bonus to your Intelligence (Arcana or Nature) checks. The bonus equals your Wisdom modifier (minimum bonus of +1).",
		"warden":   "Warden. Trained for battle, you gain proficiency with Martial weapons and training with Medium armor.",
	},
}

// Weapon properties and the Shield rule, as the SRD prints them.
const (
	srdLightProperty     = "Light. When you take the Attack action on your turn and attack with a Light weapon, you can make one extra attack as a Bonus Action later on the same turn. That extra attack must be made with a different Light weapon, and you don’t add your ability modifier to the extra attack’s damage unless that modifier is negative."
	srdTwoHandedProperty = "Two-Handed. A Two-Handed weapon requires two hands when you attack with it."
	srdShieldRule        = "Shield. You gain the Armor Class benefit of a Shield only if you have training with it. A creature can wield only one Shield at a time."
)
