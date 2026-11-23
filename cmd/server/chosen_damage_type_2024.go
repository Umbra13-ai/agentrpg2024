package main

import (
	"regexp"
	"strings"
)

// chosen_damage_type_2024.go: a spell whose damage is "of a type you choose" (Sorcerous Burst: "1d8 damage of a type you choose: Acid, Cold, Fire,
// Lightning, Poison, Psychic, or Thunder"; Chromatic Orb: "Choose Acid, Cold, Fire, Lightning, Poison, or Thunder for the type of orb you create").
// The spell's own text offers the types (spellOfferedTypes); the caster names one in the description (typeNamedIn); the cast sets it as the spell's
// damage type before anything is rolled, so resistance and immunity read it like any other type. A cast that names none is refused with the list, never
// dealt as untyped damage (FUNC-07: the untyped damage went through Cold immunity). One owner for every route that casts it.

var offeredTypesRe = regexp.MustCompile(`(?:type you choose:|Choose) ((?:[A-Z][a-z]+, )+(?:or )?[A-Z][a-z]+)`)

// spellOfferedTypes: the damage types a spell lets its caster choose from, read from its text; nil for a spell that deals one fixed type (or none).
func spellOfferedTypes(spell SRDSpell) []string {
	if spell.DamageDice == "" || spell.DamageType != "" {
		return nil
	}
	m := offeredTypesRe.FindStringSubmatch(spell.Description)
	if m == nil {
		return nil
	}
	var out []string
	for _, t := range regexp.MustCompile(`, or |, | or `).Split(m[1], -1) {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// typeNamedIn returns the offered type the description names (a whole word), "" when it names none.
func typeNamedIn(desc string, offered []string) string {
	desc = strings.ToLower(desc)
	for _, t := range offered {
		if hasWord(desc, t) {
			return t
		}
	}
	return ""
}

// withChosenType sets a chosen-type spell's damage type from the cast's description. why is the refusal when the caster named none.
func withChosenType(spell SRDSpell, desc string) (SRDSpell, string) {
	offered := spellOfferedTypes(spell)
	if len(offered) == 0 {
		return spell, ""
	}
	t := typeNamedIn(desc, offered)
	if t == "" {
		return spell, "Cannot cast " + spell.Name + ": name the damage type in the description (" + strings.Join(offered, ", ") + "), for example \"Cast " + spell.Name + " choosing " + offered[0] + " damage\". Nothing was spent."
	}
	spell.DamageType = t
	return spell, ""
}
