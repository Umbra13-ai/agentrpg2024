package main

// Improvised weapons (SRD 5.2.1 Rules Glossary): an object wielded as a makeshift weapon (broken glass, a table leg, a frying pan), a
// Ranged weapon used to make a melee attack, or a Melee weapon without the Thrown property that is thrown. No Proficiency Bonus;
// 1d4 damage of a type that suits the object; thrown, a normal range of 20 feet and a long range of 60 feet. The GM may rule that an
// object that resembles a weapon works as that weapon (a table leg as a Club): the player then names the weapon instead.

import "strings"

// improvisedObjects are everyday things a character might grab and swing or throw.
var improvisedObjects = []string{"improvised", "chair", "stool", "bottle", "broken glass", "table leg", "frying pan", "pan", "tankard", "mug",
	"rock", "stone", "brick", "plank", "board", "candlestick", "lantern", "shovel", "pitchfork", "rolling pin", "book", "skull", "bone",
	"branch", "log", "pot", "kettle", "poker", "chain", "rope", "crate", "bucket", "helmet", "shield bash", "torch"}

// improvisedWeapon decides whether an attack is made with an improvised weapon and returns that weapon. weaponKey/hasWeapon are the
// weapon the description named or the character holds.
func improvisedWeapon(description, weaponKey string, weapon SRDWeapon, hasWeapon bool) (SRDWeapon, bool) {
	d := " " + strings.ToLower(description) + " "
	throws := strings.Contains(d, "throw") || strings.Contains(d, "hurl") || strings.Contains(d, "toss") || strings.Contains(d, "lob ")
	thrownProp := false
	if hasWeapon {
		for _, p := range weapon.Properties {
			if strings.HasPrefix(strings.ToLower(p), "thrown") {
				thrownProp = true
			}
		}
	}
	name := ""
	switch {
	case hasWeapon && weapon.Type == "ranged" && (strings.Contains(d, "melee") || strings.Contains(d, "bash") || strings.Contains(d, "club ") || strings.Contains(d, "whip") || strings.Contains(d, "butt of") || strings.Contains(d, "swing")):
		name = weapon.Name + " used in melee"
	case hasWeapon && weapon.Type == "melee" && throws && !thrownProp:
		name = "thrown " + weapon.Name
	case !hasWeapon || weaponKey == "":
		for _, o := range improvisedObjects {
			if strings.Contains(d, o) {
				name = o
				break
			}
		}
	}
	if name == "" {
		return SRDWeapon{}, false
	}
	dtype := "bludgeoning"
	switch {
	case strings.Contains(d, "glass") || strings.Contains(d, "broken bottle") || strings.Contains(d, "shard"):
		dtype = "slashing"
	case strings.Contains(d, "pitchfork") || strings.Contains(d, "spike") || strings.Contains(d, "nail"):
		dtype = "piercing"
	case hasWeapon && strings.HasPrefix(name, "thrown "):
		dtype = weapon.DamageType
	}
	props := []string{}
	if throws {
		props = append(props, "thrown (20/60)")
	}
	if name == "improvised" {
		name = "object"
	}
	return SRDWeapon{Name: "Improvised Weapon (" + name + ")", Category: "improvised", Type: "melee", Damage: "1d4", DamageType: dtype, Properties: props}, true
}
