package main

// evasion_2024.go: which creature has Evasion (SRD 5.2.1: a Monk or Rogue of level 7 or more; a stat block's trait, the Assassin), for
// a Dexterity saving throw. What Evasion does to the damage is game.SaveDamage; every route that deals a saving throw's damage (a
// character's spell at a character or a monster, a stat block's save effect at a character, a monster or a companion, the GM's area
// cast) asks creatureEvasion and hands the answer to game.SaveDamage, so a creature has the same Evasion whoever makes it save.
// Go: TestSaveDamageAndEvasion, TestPostgresEvasionOnEverySaveRoute.

// creatureEvasion: the creature (a character id > 0, a fight monster id < 0) has Evasion and the save is a Dexterity saving throw.
func creatureEvasion(lobbyID, id int, ability string) bool {
	if saveAbilityKey(ability) != "dex" {
		return false
	}
	switch {
	case id > 0:
		return hasEvasion(id)
	case id < 0:
		return monsterHasTrait(lobbyID, id, "Evasion")
	}
	return false
}
