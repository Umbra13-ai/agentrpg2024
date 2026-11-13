package main

// The ten Metamagic options exactly as the SRD 5.2.1 prints them (Nov 13). The older table in warlock_helpers.go had eight,
// with older wording and costs; init() below replaces it so every listing and check uses the SRD.

func init() {
	srdMeta := map[string][3]string{
		"careful": {"Careful Spell", "1", "When you cast a spell that forces other creatures to make a saving throw, you can protect some of those creatures from the spell’s full force. To do so, spend 1 Sorcery Point and choose a number of those creatures up to your Charisma modifier (minimum of one creature). A chosen creature automatically succeeds on its saving throw against the spell, and it takes no damage if it would normally take half damage on a successful save."},
		"distant": {"Distant Spell", "1", "When you cast a spell that has a range of at least 5 feet, you can spend 1 Sorcery Point to double the spell’s range. Or when you cast a spell that has a range of Touch, you can spend 1 Sorcery Point to make the spell’s range 30 feet."},
		"empowered": {"Empowered Spell", "1", "When you roll damage for a spell, you can spend 1 Sorcery Point to reroll a number of the damage dice up to your Charisma modifier (minimum of one), and you must use the new rolls. You can use Empowered Spell even if you’ve already used a different Metamagic option during the casting of the spell."},
		"extended": {"Extended Spell", "1", "When you cast a spell that has a duration of 1 minute or longer, you can spend 1 Sorcery Point to double its duration to a maximum duration of 24 hours. If the affected spell requires Concentration, you have Advantage on any saving throw you make to maintain that Concentration."},
		"heightened": {"Heightened Spell", "2", "When you cast a spell that forces a creature to make a saving throw, you can spend 2 Sorcery Points to give one target of the spell Disadvantage on saves against the spell."},
		"quickened": {"Quickened Spell", "2", "When you cast a spell that has a casting time of an action, you can spend 2 Sorcery Points to change the casting time to a Bonus Action for this casting. You can’t modify a spell in this way if you’ve already cast a level 1+ spell on the current turn, nor can you cast a level 1+ spell on this turn after modifying a spell in this way."},
		"seeking": {"Seeking Spell", "1", "If you make an attack roll for a spell and miss, you can spend 1 Sorcery Point to reroll the d20, and you must use the new roll. You can use Seeking Spell even if you’ve already used a different Metamagic option during the casting of the spell."},
		"subtle": {"Subtle Spell", "1", "When you cast a spell, you can spend 1 Sorcery Point to cast it without any Verbal, Somatic, or Material components, except Material components that are consumed by the spell or that have a cost specified in the spell."},
		"transmuted": {"Transmuted Spell", "1", "When you cast a spell that deals a type of damage from the following list, you can spend 1 Sorcery Point to change that damage type to one of the other listed types: Acid, Cold, Fire, Lightning, Poison, Thunder."},
		"twinned": {"Twinned Spell", "1", "When you cast a spell, such as Charm Person, that can be cast with a higher-level spell slot to target an additional creature, you can spend 1 Sorcery Point to increase the spell’s effective level by 1."},
	}
	for slug, m := range srdMeta {
		cost := 1
		if m[1] == "2" {
			cost = 2
		}
		metamagicOptions[slug] = MetamagicOption{Slug: slug, Name: m[0], Cost: cost, Description: m[2]}
	}
}
