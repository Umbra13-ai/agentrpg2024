package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// A Reaction that answers the creature's own failed saving throw (Monster Manual Psychic Gray Ooze, Mind Corrosion: "Trigger: The ooze
// fails a saving throw against a spell or another magical effect created by a creature. Response: The triggering creature takes 3 (1d6)
// Psychic damage."). saveCtx.resist calls failedSaveReaction once a save against a spell or a magical effect has failed and the creature
// that made it is known (the saveCtx's source): the Reaction is spent (one a round, none while it can't take Reactions) and the damage
// goes through the Hit Point owner of the creature that caused the save.

var failedSaveReactionRe = regexp.MustCompile(`Trigger: The [a-z -]+ fails a saving throw against a spell or (?:an)?other magical effect created by a creature\. Response: The triggering creature takes (\d+) \((\d+d\d+(?:\s*[+−-]\s*\d+)?)\) ([A-Za-z]+) damage`)

// failedSaveReaction: the saver's Reaction against source after a failed save (a line for the reply notes; "" when none).
func failedSaveReaction(lobbyID, saver, source int) string {
	if saver >= 0 || source == 0 || source == saver {
		return "" // a monster's Reaction (a companion's own Reactions are the companion owner's)
	}
	meta := creatureMetaOf(lobbyID, saver)
	if meta == nil {
		return ""
	}
	for _, rx := range meta.Reactions {
		m := failedSaveReactionRe.FindStringSubmatch(rx.Text)
		if m == nil {
			continue
		}
		name := creatureName(lobbyID, saver)
		key := fmt.Sprintf("%d:%d", lobbyID, saver)
		round := currentRound(lobbyID)
		if v, ok := monsterReactionRound.Load(key); ok && v.(int) == round {
			return ""
		}
		if cantTakeReactions(lobbyID, saver) {
			return ""
		}
		monsterReactionRound.Store(key, round)
		dmg := game.RollDamage(normDice(m[2]), false)
		dtype := strings.ToLower(m[3])
		var dealt string
		switch {
		case isCompanionCombatID(source):
			if c, ok := activeCompanionInCampaign(lobbyID, source); ok {
				dealt = damageCompanion(c, dmg, dtype)
			}
		case source > 0:
			dealt = creatureName(lobbyID, source) + " " + damageCharacter(source, dmg, dtype)
		default:
			if h, ok := hurtMonster(lobbyID, source, dmg, dtype, false); ok {
				dealt = fmt.Sprintf("%s takes %d %s damage", h.Name, h.Dealt, dtype)
			}
		}
		if dealt == "" {
			return ""
		}
		return fmt.Sprintf("%s's %s (its Reaction): %s.", name, rx.Name, dealt)
	}
	return ""
}
