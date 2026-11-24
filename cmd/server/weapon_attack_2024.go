package main

// A character's attack bonus and damage modifier with a weapon (or an Unarmed Strike): the one rule (feedback log 38: the sheet
// showed a Longbow +9 because it worked the bonus out itself and left out Archery's +2; the off-hand and opportunity attacks each had
// their own copy too). weaponAttackMods is read by the main attack and the off-hand attack (actions.go), the opportunity attack
// (gm_reactions.go) and the character sheet's attack lines (character_attacks.go). It holds what lasts: the ability used (Strength;
// Dexterity for a ranged weapon; the better of the two with Finesse or Martial Arts; the spellcasting ability with Pact of the Blade),
// proficiency, a magic weapon's +N and the Archery Fighting Style. What belongs to one moment (Rage, magic ammunition, Sacred Weapon,
// Advantage and the rest of the roll) stays with the attack that has it; the roll itself is attackCtx's (attack_2024.go).

import (
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// weaponUse: who attacks with what.
type weaponUse struct {
	charID      int // 0: no stored character (only the scores below count)
	class       string
	level       int
	str, dex    int
	profs       string // the character's weapon proficiencies
	key         string // the weapon's key ("" or hasWeapon false: an Unarmed Strike)
	weapon      SRDWeapon
	hasWeapon   bool
	improvised  bool
	offHand     bool
	description string // the player's words (a "+1" named there)
}

// weaponMods: the attack's lasting bonuses.
type weaponMods struct {
	Attack       int      // the attack bonus
	Damage       int      // the damage modifier (the ability and a magic weapon's +N)
	Ability      int      // the ability modifier alone (Graze, Cleave, an off-hand attack without the style)
	UsesStrength bool     // the attack uses Strength (Rage)
	PactBlade    bool     // the Pact of the Blade weapon
	Proficient   bool     //
	Plus         int      // a magic weapon's +N
	Archery      bool     // Archery's +2 is in Attack
	Parts        []string // the attack bonus, part by part ("Dexterity +5", "proficiency +4", "Archery +2")
}

func weaponAttackMods(u weaponUse) weaponMods {
	strMod, dexMod := game.Modifier(u.str), game.Modifier(u.dex)
	m := weaponMods{Ability: strMod, UsesStrength: true}
	ability := "Strength"
	useDex := func() {
		m.Ability, m.UsesStrength, ability = dexMod, false, "Dexterity"
	}
	if u.hasWeapon {
		if u.weapon.Type == "ranged" || (hasProperty(u.weapon.Properties, "finesse") && dexMod > strMod) {
			useDex()
		}
	}
	stored := u.charID > 0
	monk := strings.EqualFold(u.class, "monk") && (!u.hasWeapon || isMonkWeapon(u.class, u.weapon)) // the scores alone: no armor known
	if dexMod > strMod && ((stored && martialArtsApplies(u.charID, u.class, u.hasWeapon, u.weapon)) || (!stored && monk)) {
		useDex() // Martial Arts: a Monk's Unarmed Strike and Monk weapons use the better of Strength and Dexterity
	}
	if stored && !u.improvised {
		m.PactBlade = pactBladeWeapon(u.charID, u.class, u.hasWeapon, u.weapon) // Pact of the Blade: the spellcasting ability, proficiency
		if s := game.Modifier(spellModScore(u.charID)); m.PactBlade && s > m.Ability {
			m.Ability, m.UsesStrength, ability = s, false, "Charisma (Pact of the Blade)"
		}
	}
	m.Attack, m.Damage = m.Ability, m.Ability
	m.Parts = append(m.Parts, fmt.Sprintf("%s %s", ability, signedBonus(m.Ability)))

	m.Proficient = !u.hasWeapon || isWeaponProficient(u.profs, u.key) // every creature is proficient with its Unarmed Strike
	if u.improvised {
		m.Proficient = false
	}
	if m.PactBlade {
		m.Proficient = true
	}
	if m.Proficient {
		p := game.ProficiencyBonus(u.level)
		m.Attack += p
		m.Parts = append(m.Parts, fmt.Sprintf("proficiency %s", signedBonus(p)))
	}
	if stored && u.hasWeapon && !u.improvised {
		if m.Plus = weaponPlusFor(u.charID, u.key, u.offHand); m.Plus > 0 { // Weapon, +1, +2 or +3; a named weapon's bonus
			m.Attack += m.Plus
			m.Damage += m.Plus
			m.Parts = append(m.Parts, fmt.Sprintf("magic weapon %s", signedBonus(m.Plus)))
		}
	}
	if stored && u.hasWeapon && !u.improvised { // a corroded weapon: -1 to attack rolls for each point (item_state_2024.go)
		if c := weaponCorrosionOf(u.charID, u.key, u.offHand); c > 0 {
			m.Attack -= c
			m.Parts = append(m.Parts, fmt.Sprintf("corroded -%d", c))
		}
	}
	if stored && u.hasWeapon && u.weapon.Type == "ranged" && hasFightingStyle(u.charID, "archery") {
		m.Archery = true // Archery: +2 to attack rolls with Ranged weapons
		m.Attack += 2
		m.Parts = append(m.Parts, "Archery +2")
	}
	return m
}

// toHit: the attack bonus as the sheet shows it: "+11 to hit (Dexterity +5, proficiency +4, Archery +2)".
func (m weaponMods) toHit() string {
	return signedBonus(m.Attack) + " to hit (" + strings.Join(m.Parts, ", ") + ")"
}
