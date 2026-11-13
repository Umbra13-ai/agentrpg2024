package main

// Options from the 2024 Player's Handbook that are not in the SRD 5.2.1, so not on this server (Nov 13). When a player asks for one,
// the error says so plainly and lists what this server has, instead of only "not found".

import (
	"fmt"
	"strings"
)

var phbOnly = map[string][]string{
	"background": {"artisan", "charlatan", "entertainer", "farmer", "guard", "guide", "hermit", "merchant", "noble", "sailor", "scribe", "wayfarer"},
	"species":    {"aasimar"},
	"subclass": {"wild_heart", "world_tree", "zealot", "dance", "glamour", "valor", "light", "trickery", "war", "moon", "sea", "stars",
		"battle_master", "eldritch_knight", "psi_warrior", "mercy", "shadow", "elements", "glory", "ancients", "vengeance",
		"beast_master", "fey_wanderer", "gloom_stalker", "arcane_trickster", "assassin", "soulknife", "aberrant", "clockwork",
		"wild_magic", "archfey", "celestial", "great_old_one", "abjurer", "diviner", "illusionist"},
	"spell": {"power_word_fortify", "arcane_vigor", "toll_the_dead", "mind_sliver"},
	"feat": {"crafter", "healer", "lucky", "musician", "tavern_brawler", "tough", "actor", "athlete", "charger", "chef", "crossbow_expert",
		"crusher", "defensive_duelist", "dual_wielder", "durable", "elemental_adept", "fey_touched", "great_weapon_master", "heavily_armored",
		"heavy_armor_master", "inspiring_leader", "keen_mind", "lightly_armored", "mage_slayer", "martial_weapon_training", "medium_armor_master",
		"moderately_armored", "mounted_combatant", "observant", "piercer", "poisoner", "polearm_master", "resilient", "ritual_caster",
		"sentinel", "shadow_touched", "sharpshooter", "shield_master", "skill_expert", "skulker", "slasher", "speedy", "spell_sniper",
		"telekinetic", "telepathic", "war_caster", "weapon_master", "boon_of_energy_resistance", "boon_of_fortitude", "boon_of_recovery",
		"boon_of_skill", "boon_of_speed", "blind_fighting", "dueling", "interception", "protection", "thrown_weapon_fighting", "unarmed_fighting"},
}

// phbNote: a sentence saying the input is a 2024 Player's Handbook option not in the SRD ("" when it isn't one we know).
func phbNote(kind, input string) string {
	k := strings.ToLower(strings.TrimSpace(input))
	k = strings.NewReplacer(" ", "_", "-", "_", "'", "", "’", "").Replace(k)
	for _, prefix := range []string{"path_of_the_", "college_of_", "circle_of_the_", "oath_of_the_", "oath_of_", "way_of_the_", "warrior_of_the_", "warrior_of_", "the_", "_domain", "_patron"} {
		k = strings.TrimPrefix(k, prefix)
	}
	k = strings.TrimSuffix(strings.TrimSuffix(k, "_domain"), "_patron")
	for _, x := range phbOnly[kind] {
		if x == k {
			return fmt.Sprintf("%q is in the 2024 Player's Handbook but not in the SRD 5.2.1, and this server has only what the SRD has.", input)
		}
	}
	return ""
}

// notOneOf: the builder's problem text for a background or species that isn't on the server, with the options it has.
func notOneOf(kind, input string, opts []map[string]interface{}) string {
	var names []string
	for _, o := range opts {
		names = append(names, fmt.Sprint(o[kind]))
	}
	msg := fmt.Sprintf("%q is not a %s on this server.", input, kind)
	if note := phbNote(kind, input); note != "" {
		msg = note
	}
	return msg + " Choose one of: " + strings.Join(names, ", ") + "."
}

// unknownSpellMessage: the error for a spell the server doesn't have.
func unknownSpellMessage(input string) string {
	if note := phbNote("spell", input); note != "" {
		return note + " Check /api/universe/spells for the spells this server has."
	}
	return fmt.Sprintf("Spell '%s' is not in the SRD 5.2.1, so not on this server. Check /api/universe/spells for the spells it has.", input)
}
