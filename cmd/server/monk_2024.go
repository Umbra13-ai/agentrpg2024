package main

// Monk, 2024 rules (SRD 5.2.1).
//	Focus Points        the Focus Points column of the Features table (none at level 1); they replace Focus Points (resource key "focus").
//	Stunning Strike     "stunning strike" in the attack description (level 5): once per turn, on a hit with a Monk weapon or an
//	                    Unarmed Strike, 1 Focus Point. Constitution save (DC 8 + Wisdom modifier + Proficiency Bonus): a failure is
//	                    Stunned until the start of your next turn; a success halves its Speed until then and the next attack roll
//	                    against it has Advantage.
//	Uncanny Metabolism  (level 2) when initiative is rolled, regain all Focus Points and heal Martial Arts die + Monk level, once per Long Rest.
//	Perfect Focus       (level 15) when initiative is rolled without Uncanny Metabolism, regain Focus Points up to 4.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

var pendingStun sync.Map // character id -> save DC

func ensureMonkColumns() {
	if db != nil {
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS stun_used BOOLEAN DEFAULT FALSE`)
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS uncanny_metabolism_used BOOLEAN DEFAULT FALSE`)
	}
}

// noteStunningStrike remembers a Stunning Strike asked for in the attack description.
func noteStunningStrike(charID int, class string, level int, descLower string) {
	pendingStun.Delete(charID)
	if strings.ToLower(class) != "monk" || level < 5 || !strings.Contains(descLower, "stunning strike") {
		return
	}
	var wis int
	db.QueryRow(`SELECT wis FROM characters WHERE id = $1`, charID).Scan(&wis)
	pendingStun.Store(charID, 8+game.Modifier(wis)+game.ProficiencyBonus(level))
}

// applyStunningStrike runs after a hit.
func applyStunningStrike(lobbyID, charID, targetID int, name string) string {
	v, ok := pendingStun.LoadAndDelete(charID)
	if !ok {
		return ""
	}
	dc := v.(int)
	var used bool
	db.QueryRow(`SELECT COALESCE(stun_used,false) FROM characters WHERE id = $1`, charID).Scan(&used)
	if used {
		return " Stunning Strike: you can use it only once per turn."
	}
	if okRes, msg, _ := useClassResource(charID, "focus", 1); !okRes {
		return " Stunning Strike: " + msg
	}
	db.Exec(`UPDATE characters SET stun_used = true WHERE id = $1`, charID)
	saved, total := monsterSave(lobbyID, targetID, "con", dc)
	if saved {
		addCombatEffect(lobbyID, targetID, charID, "halved")
		addCombatEffect(lobbyID, targetID, charID, "exposed")
		return fmt.Sprintf(" Stunning Strike: %s saves (%d vs DC %d); its Speed is halved until the start of your next turn and the next attack roll against it has Advantage.", name, total, dc)
	}
	addMonsterCondition(lobbyID, targetID, "stunned", fmt.Sprintf("Stunning Strike #%d", charID), 0, "", 0)
	return fmt.Sprintf(" Stunning Strike: %s fails its save (%d vs DC %d) and is Stunned until the start of your next turn.", name, total, dc)
}

// exposedTarget says whether the monster the character is attacking has Advantage coming (a Stunning Strike that was saved against).
func exposedTarget(charID int) bool {
	lobbyID, id, ok := attackedMonster(charID)
	if !ok {
		return false
	}
	for _, e := range combatEffectsOn(lobbyID, id) {
		if e.Kind == "exposed" {
			return true
		}
	}
	return false
}

// uncannyMetabolism and perfectFocus run when a Monk rolls initiative. They return a note for the GM, or "".
func monkInitiativeFeatures(charID int, name, class string, level int) string {
	if strings.ToLower(class) != "monk" || level < 2 {
		return ""
	}
	used := getCurrentClassResources(charID)
	var usedMeta bool
	db.QueryRow(`SELECT COALESCE(uncanny_metabolism_used,false) FROM characters WHERE id = $1`, charID).Scan(&usedMeta)
	if !usedMeta {
		// Uncanny Metabolism: all Focus Points back, and heal the Martial Arts die + Monk level
		heal := game.RollDie(game.MartialArtsDie(level)) + level
		db.Exec(`UPDATE characters SET uncanny_metabolism_used = true, hp = LEAST(max_hp, hp + $1) WHERE id = $2`, heal, charID)
		restoreAllFocus(charID)
		return fmt.Sprintf("🥋 %s: Uncanny Metabolism: all Focus Points regained and %d Hit Points healed", name, heal)
	}
	if level >= 15 && used["focus"] <= 3 {
		regain := 4 - used["focus"]
		giveFocus(charID, regain)
		return fmt.Sprintf("🥋 %s: Perfect Focus: Focus Points regained up to 4", name)
	}
	return ""
}

func restoreAllFocus(charID int) { setUsedResource(charID, "focus", 0) }

func giveFocus(charID, n int) {
	var usedJSON []byte
	db.QueryRow(`SELECT COALESCE(class_resources_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&usedJSON)
	used := map[string]int{}
	jsonUnmarshal(usedJSON, &used)
	used["focus"] -= n
	if used["focus"] < 0 {
		used["focus"] = 0
	}
	writeUsed(charID, used)
}

func setUsedResource(charID int, key string, v int) {
	var usedJSON []byte
	db.QueryRow(`SELECT COALESCE(class_resources_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&usedJSON)
	used := map[string]int{}
	jsonUnmarshal(usedJSON, &used)
	used[key] = v
	writeUsed(charID, used)
}

func jsonUnmarshal(b []byte, v interface{}) { _ = json.Unmarshal(b, v) }

func writeUsed(charID int, used map[string]int) {
	b, _ := json.Marshal(used)
	db.Exec(`UPDATE characters SET class_resources_used = $1 WHERE id = $2`, string(b), charID)
}

// unarmedDice is the damage of an Unarmed Strike: 1d6 for most, the Martial Arts die for a Monk.
func unarmedDice(class string, level int) string {
	if strings.ToLower(class) == "monk" {
		return fmt.Sprintf("1d%d", game.MartialArtsDie(level))
	}
	return "0d1+1" // Unarmed Strike: 1 + Strength modifier (no die, so nothing is doubled on a Critical Hit)
}

// flurryOnMonster is Flurry of Blows against a monster picked with target_id: the server rolls each Unarmed Strike against the
// monster's Armor Class (two strikes, three from level 10 with Heightened Focus). The Warrior of the Open Hand adds one Open Hand
// Technique effect to every hit: write "addle", "push" or "topple" in the description. From level 6 write "force" for Force damage.
func flurryOnMonster(charID int, description string) (string, bool) {
	return monkStrikesOnMonster(charID, description, true)
}

// martialArtsOnMonster is the Martial Arts Bonus Action Unarmed Strike against the monster picked with target_id.
func martialArtsOnMonster(charID int, description string) (string, bool) {
	return monkStrikesOnMonster(charID, description, false)
}

// monkStrikesOnMonster rolls a Monk's Unarmed Strikes against a monster: Flurry of Blows (1 Focus Point, two strikes, three
// from level 10) or the single Martial Arts strike.
func monkStrikesOnMonster(charID int, description string, flurry bool) (string, bool) {
	lobbyID, monster, ok := attackedMonster(charID)
	if !ok {
		return "", false
	}
	var class, name string
	var sub sql.NullString
	var level, str, dex, wis int
	var actionUsed bool
	if db.QueryRow(`SELECT class, name, subclass, level, str, dex, wis, COALESCE(action_used,false) FROM characters WHERE id = $1`, charID).
		Scan(&class, &name, &sub, &level, &str, &dex, &wis, &actionUsed) != nil {
		return "", false
	}
	if strings.ToLower(class) != "monk" || (flurry && level < 2) {
		if !flurry {
			return "Only a Monk has Martial Arts.", true
		}
		return "Flurry of Blows needs a Monk of level 2 or higher.", true
	}
	_ = actionUsed // 2024: Flurry of Blows is a Bonus Action of its own; it doesn't need the Attack action first
	if flurry {
		if okRes, msg, _ := useClassResource(charID, "focus", 1); !okRes {
			return "Cannot use Flurry of Blows: " + msg, true
		}
	}
	low := strings.ToLower(description)
	strikes := 1
	if flurry {
		strikes = 2
		if level >= 10 {
			strikes = 3 // Heightened Focus
		}
	}
	mod := game.Modifier(dex)
	if game.Modifier(str) > mod {
		mod = game.Modifier(str)
	}
	attackMod := mod + game.ProficiencyBonus(level)
	dtype := "bludgeoning"
	if level >= 6 && strings.Contains(low, "force") {
		dtype = "force" // Empowered Strikes
	}
	dc := 8 + game.Modifier(wis) + game.ProficiencyBonus(level)
	effect := ""
	if flurry && sub.Valid && strings.ToLower(sub.String) == "open-hand" && level >= 3 {
		for _, e := range []string{"addle", "push", "topple"} {
			if strings.Contains(low, e) {
				effect = e
			}
		}
	}
	var lines []string
	for i := 1; i <= strikes; i++ {
		adv, dis := getAttackModifiers(charID, attackTargetConditions(charID), false)
		var roll int
		switch {
		case adv && !dis:
			roll, _, _ = game.RollWithAdvantage()
		case dis && !adv:
			roll, _, _ = game.RollWithDisadvantage()
		default:
			roll = game.RollDie(20)
		}
		dmg := game.RollDamage(fmt.Sprintf("1d%d", game.MartialArtsDie(level)), roll == 20) + mod
		if dmg < 1 {
			dmg = 1
		}
		out := attackOutcome{Weapon: "unarmed", Total: roll + attackMod, Natural: roll, Crit: roll == 20, Fumble: roll == 1, Damage: dmg, DamageType: dtype}
		text, info, err := resolveAttackOnMonster(lobbyID, charID, monster, out)
		if err != nil {
			return "Flurry of Blows could not be resolved against that creature.", true
		}
		line := fmt.Sprintf("Strike %d: %s", i, text)
		if hit, _ := info["hit"].(bool); hit && effect != "" {
			line += openHandEffect(lobbyID, charID, monster, effect, dc)
		}
		lines = append(lines, line)
	}
	if !flurry {
		return fmt.Sprintf("👊 Martial Arts: an Unarmed Strike as a Bonus Action.\n%s", strings.Join(lines, "\n")), true
	}
	return fmt.Sprintf("👊 Flurry of Blows (1 Focus Point spent).\n%s", strings.Join(lines, "\n")), true
}

// openHandEffect is the Open Hand Technique effect of a Flurry of Blows hit on a monster.
func openHandEffect(lobbyID, charID, monster int, effect string, dc int) string {
	switch effect {
	case "addle":
		addCombatEffect(lobbyID, monster, charID, "addled")
		return " Addle: it can't make Opportunity Attacks until the start of its next turn."
	case "push":
		if saved, total := monsterSave(lobbyID, monster, "str", dc); saved {
			return fmt.Sprintf(" Push: it saves (%d vs DC %d).", total, dc)
		}
		moved, hasMap := pushToken(lobbyID, charID, monster, 15)
		if !hasMap {
			return " Push: it fails its save and is pushed up to 15 feet away from you (no map, the GM places it)."
		}
		return fmt.Sprintf(" Push: it fails its save and is pushed %d feet away.", moved)
	case "topple":
		if saved, total := monsterSave(lobbyID, monster, "dex", dc); saved {
			return fmt.Sprintf(" Topple: it saves (%d vs DC %d).", total, dc)
		}
		addMonsterCondition(lobbyID, monster, "prone", "Open Hand Technique: Topple", 0, "", 0)
		return " Topple: it fails its save and has the Prone condition."
	}
	return ""
}
