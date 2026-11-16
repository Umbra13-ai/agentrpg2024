package main

import "testing"

// TestActionCosts: what an action type costs comes from the action table alone (direct actions, pending-turn steps, Command and
// Haste limits, feature routes), through getActionResourceType. Speaking, one object and dropping something are free; Horde Breaker
// is part of the attack it follows (SRD 5.2.1: "you can make another attack"), so it takes no action of its own.
func TestActionCosts(t *testing.T) {
	for typ, want := range map[string]string{
		"cast": "action", "magic": "action", "attack": "action", "utilize": "action", "use_item": "action", "Study": "action",
		"divine_spark": "action", "turn_undead": "action", "tireless": "action",
		"speak": "free", "interact": "free", "drop": "free", "horde_breaker": "free", "frenzy_attack": "free", "action_surge": "free",
		"cunning_action": "bonus_action", "second_wind": "bonus_action", "wholeness_of_body": "bonus_action", "font_of_magic": "bonus_action",
		"font_of_magic_convert": "free", "opportunity_attack": "reaction", "countercharm": "reaction", "move": "movement", "stand": "movement",
		"breath": "companion",
		// cost variants (a Bonus Action or Reaction spell, an item used with a Bonus Action)
		"bonus_cast": "bonus_action", "reaction_cast": "reaction", "item_bonus": "bonus_action",
		// a declaration ("other") takes the action in a fight, its Bonus Action form the Bonus Action (Matt, Nov 16: no free actions)
		"other": "action", "declare": "action", "free": "action", "declare_bonus": "bonus_action",
		// a word that is no type is refused before anything is spent; asked anyway, it is never free
		"frobnicate": "action",
	} {
		if got := getActionResourceType(typ); got != want {
			t.Errorf("getActionResourceType(%q) = %q, want %q", typ, got, want)
		}
	}
	for _, at := range actionTypes {
		if got := getActionResourceType(at.Name); got != at.Cost {
			t.Errorf("getActionResourceType(%q) = %q, the table says %q", at.Name, got, at.Cost)
		}
	}
	for action, want := range map[string]string{"bonus_cast": "cast", "reaction_cast": "cast", "opportunity_attack": "attack", "bonus_attack": "attack",
		"martial_arts": "attack", "item_bonus": "use_item", "utilize": "use_item", "dodge": "dodge"} {
		if got := featureAction(action); got != want {
			t.Errorf("featureAction(%q) = %q, want %q", action, got, want)
		}
	}
}
