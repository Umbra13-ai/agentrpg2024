package main

// The server decides whether a player's attack hits a monster.
// A player names a target with `target_id` on POST /api/action (the negative id of
// an enemy from GET /api/my-turn, situation.enemies). The server compares the
// attack roll with the monster's Armor Class, applies the damage to its hit points
// (with its resistances, immunities and vulnerabilities) and tells the player only
// HIT or MISS, the damage, and how the creature looks. The AC and the exact hit
// points stay with the server and the GM.
// Without a target_id the old behaviour remains: the player gets the roll and the
// GM decides.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

// attackOutcome is what an attack roll produced, recorded by resolveAction for the action handler.
type attackOutcome struct {
	Weapon     string
	Total      int // attack roll plus modifiers
	Natural    int // the d20
	Crit       bool
	Fumble     bool
	Roll       game.AttackRoll // the roll as the one attack function made it (attack_2024.go)
	Judged     bool            // the roll was made against the target's Armor Class, so Hit is known
	Hit        bool
	Damage     int
	DamageType string
	RollInfo   string             // what the roll added besides the attacker's line (Lucky, Reckless Attack, a reroll): never its description
	Out        game.AttackOutcome // the roll's outcome as the one attack function gave it (both d20s with Advantage)
	SneakUsed  bool
	SneakNote  string // " (+38 Sneak Attack, 10d6)": shown to the attacker after the damage
	Dice       string // the weapon's damage dice and the modifier added, for Savage Attacker
	DiceMod    int
	KnockOut   bool // a melee attack meant to knock the target out instead of killing it
}

var lastAttacks sync.Map // character id -> attackOutcome

// attackHead is the attacker's line of a recorded attack up to the outcome (game.AttackRoll.HeadForAttacker: the total, the
// d20 and what was added, never the Armor Class), then what the roll added besides. A d20 changed after the roll (a Luck Blade
// reroll) is shown as the d20 that counts.
func attackHead(label string, o attackOutcome) string {
	out := o.Out
	if out.Natural != o.Natural || out.Total != o.Total {
		out = game.AttackOutcome{Natural: o.Natural, Dice: [2]int{o.Natural, 0}, Total: o.Total}
	}
	return o.Roll.HeadForAttacker("Attack with "+label, out) + o.RollInfo
}

var underwaterMiss sync.Map // character id -> a ranged attack underwater beyond normal range (an automatic miss)

// hasSwimSpeed: the character has a Swim Speed (no SRD species has one; a Wild Shape form can).
func hasSwimSpeed(charID int) bool {
	var speed string
	db.QueryRow(`SELECT COALESCE(m.speed_text,'') FROM characters c JOIN monsters m ON m.slug = c.wild_shape_form WHERE c.id = $1`, charID).Scan(&speed)
	return strings.Contains(strings.ToLower(speed), "swim") || hasInvocation(charID, "gift-of-the-depths") || elementalSwim(charID) || // Ring of Elemental Command (water)
		itemSpeeds(charID)["swim"] > 0 // a Swim Speed from a worn item (F204)
}

// underwaterFire: anything underwater has Resistance to Fire damage.
func underwaterFire(lobbyID int, dtype string, dmg int) (int, bool) {
	if strings.EqualFold(dtype, "fire") && lobbyID != 0 && isUnderwaterCombat(lobbyID) {
		return dmg / 2, true
	}
	return dmg, false
}

func recordAttackOutcome(charID int, o attackOutcome) {
	empoweredStrike(charID, &o) // Empowered Strikes (monk_fixes_2024.go, F110)
	lastAttacks.Store(charID, o)
}

func takeAttackOutcome(charID int) (attackOutcome, bool) {
	v, ok := lastAttacks.LoadAndDelete(charID)
	if !ok {
		return attackOutcome{}, false
	}
	return v.(attackOutcome), true
}

// attackDamageType is the damage type of a weapon attack (unarmed strikes are bludgeoning).
func attackDamageType(hasWeapon bool, w SRDWeapon) string {
	if hasWeapon && w.DamageType != "" {
		return strings.ToLower(w.DamageType)
	}
	return "bludgeoning"
}

func asInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// enemyTargets lists the living monsters in the campaign's combat: id and name only.
func enemyTargets(lobbyID int) []map[string]interface{} {
	var raw []byte
	var active bool
	if db.QueryRow(`SELECT turn_order, active FROM combat_state WHERE lobby_id = $1`, lobbyID).Scan(&raw, &active) != nil || !active {
		return nil
	}
	var entries []map[string]interface{}
	json.Unmarshal(raw, &entries)
	var out []map[string]interface{}
	hidden := hiddenMonsterIDs(lobbyID)
	for _, e := range entries {
		if m, _ := e["is_monster"].(bool); m && asInt(e["hp"]) > 0 && !hidden[asInt(e["id"])] {
			ent := map[string]interface{}{"id": asInt(e["id"]), "name": e["name"]}
			if cs := append(append(conditionNames(lobbyID, asInt(e["id"])), combatEffectNames(lobbyID, asInt(e["id"]))...), spellEffectNames(lobbyID, asInt(e["id"]))...); len(cs) > 0 {
				ent["conditions"] = cs // what everyone at the table can see; never the source or the DCs
			}
			out = append(out, ent)
		}
	}
	return out
}

// validateAttackTarget checks a target_id before anything is spent.
func validateAttackTarget(lobbyID, targetID int) (bool, string) {
	targets := enemyTargets(lobbyID)
	if targets == nil {
		if _, fighting := ptLoadCombat(lobbyID); fighting {
			return false, "Every enemy in this fight is down (or out of sight): there is nothing left to target. The GM ends the fight."
		}
		return false, "There is no combat going on, so there is nothing to target. Leave out target_id and describe the attack."
	}
	var ids []string
	for _, t := range targets {
		if asInt(t["id"]) == targetID {
			return true, ""
		}
		ids = append(ids, fmt.Sprintf("%v (%v)", t["id"], t["name"]))
	}
	sort.Strings(ids)
	return false, "target_id must be the id of a living enemy: " + strings.Join(ids, ", ") + ". See situation.enemies in GET /api/my-turn."
}

// adjustMonsterDamage applies a monster's resistances, immunities and vulnerabilities.
// The attack is treated as nonmagical unless the text of the resistance says otherwise.
func adjustMonsterDamage(monsterKey, damageType string, dmg int) (int, string) {
	if monsterKey == "" || damageType == "" || dmg <= 0 {
		return dmg, ""
	}
	var resist, immune, vuln string
	if db.QueryRow(`SELECT COALESCE(damage_resistances,''), COALESCE(damage_immunities,''), COALESCE(damage_vulnerabilities,'') FROM monsters WHERE slug = $1`, monsterKey).
		Scan(&resist, &immune, &vuln) != nil {
		return dmg, ""
	}
	has := func(list string) bool { return strings.Contains(strings.ToLower(list), damageType) }
	switch {
	case has(immune):
		return 0, "immune"
	case has(vuln):
		return dmg * 2, "vulnerable"
	case has(resist):
		return dmg / 2, "resistant"
	}
	return dmg, ""
}

// resolveAttackOnMonster compares the attack with the target's AC, applies the damage and
// returns the text a player may see and a small structured summary.
//
// The hit is worked out from the fight as it is (no lock held while the many rules below read and write), then saveMonsterHit
// applies only this hit's own changes under a short lock: the Hit Points it took (from the monster's Hit Points as they are by
// then, so two attacks at once both count) and any field the rules changed on the target. Before Nov 15 2026 a transaction
// held the fight's row for the whole hit while helpers read and wrote through other connections (audit AUD-02, B-19).
func resolveAttackOnMonster(lobbyID, charID, targetID int, o attackOutcome, cleaveTarget ...int) (string, map[string]interface{}, error) {
	var raw []byte
	if err := db.QueryRow(`SELECT turn_order FROM combat_state WHERE lobby_id = $1 AND active = true`, lobbyID).Scan(&raw); err != nil {
		return "", nil, err
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return "", nil, err
	}
	idx := -1
	for i, e := range entries {
		if asInt(e["id"]) == targetID {
			idx = i
		}
	}
	if idx < 0 {
		return "", nil, fmt.Errorf("target_not_found")
	}
	t := entries[idx]
	t0 := copyEntry(t) // the target as read: saveMonsterHit writes only what this hit changed
	name := fmt.Sprint(t["name"])
	hp, maxHP := asInt(t["hp"]), asInt(t["max_hp"])
	hpStart := hp // for a monster's Reaction to this hit (Parry, Shield, Deflect Missile: monster_reactions_2024.go)
	// Hit or miss was decided by the one attack function when the roll was made (attack_2024.go): the Armor Class, Help,
	// "exposed", Sundering Blow and the Mace of Smiting are its business, not this stage's. A roll that was made without a
	// target is judged here by the same rule.
	if !o.Judged {
		o.Roll.AC = armorClassOf(lobbyID, targetID)
		j := o.Roll.Judge(o.Natural)
		o.Hit, o.Crit, o.Judged = j.Hit, j.Critical, true
	}
	ac := o.Roll.AC
	if energyBowHeld(charID) {
		o.DamageType = "force" // the Energy Bow's arrow of golden energy (F177)
	}
	hit := o.Hit
	if _, asked := luckOnAttack.LoadAndDelete(charID); asked && !hit && luckReroll(charID, true) { // the Luck Blade's Luck
		r := game.RollDie(20)
		j := o.Roll.Judge(r)
		o.Total, o.Natural, o.Crit, o.Fumble = j.Total, r, j.Critical, r == 1
		o.RollInfo += fmt.Sprintf(" [Luck Blade: the miss is rerolled: %d]", r)
		if o.Damage == 0 && o.Dice != "" {
			o.Damage = game.RollDamage(o.Dice, o.Crit) + o.DiceMod
		} else if o.Crit && o.Dice != "" {
			o.Damage += game.RollDamage(o.Dice, false) // a critical hit's extra dice
		}
		hit = j.Hit
	}
	if _, beyond := underwaterMiss.LoadAndDelete(charID); beyond {
		hit, o.Crit = false, false // underwater: a ranged weapon attack beyond its normal range automatically misses
		o.RollInfo += " (underwater, beyond normal range: an automatic miss)"
	}
	peerless := false
	if !hit && peerlessAim(charID) { // Boon of Combat Prowess, Peerless Aim: a miss becomes a hit
		hit, peerless = true, true
		if o.Fumble && o.Damage == 0 && o.Dice != "" { // a natural 1 rolled no damage: roll it now (F59)
			o.Damage = game.RollDamage(o.Dice, false) + o.DiceMod
			if o.Damage < 1 {
				o.Damage = 1
			}
			fd, fn := fumbleHitRiders(charID, o.Weapon) // F138: Lifedrinker, Eldritch Smite on the hit too
			o.Damage += fd
			o.RollInfo += fn
		}
		o.Fumble = false
	}
	mu, hasMastery := takeMasteryUse(charID)
	weaponLabel := o.Weapon // O-9: the reply names the item in hand ("Mace of Disruption", "Longsword +3"), not only its base weapon
	if v, ok := attackHeldItem.Load(charID); ok {
		if h, _ := v.(string); h != "" && !strings.EqualFold(h, o.Weapon) && o.Weapon != "unarmed" {
			weaponLabel = h
			if !strings.Contains(strings.ToLower(h), strings.ToLower(o.Weapon)) {
				weaponLabel = h + " (" + o.Weapon + ")" // "Vorpal Sword (Longsword)", "Rod of Lordly Might (Battleaxe)": the form matters
			}
		}
	}
	if !hit {
		o.RollInfo = withdrawHitEffects(charID, o.RollInfo) // Frenzy and Brutal Strike need a hit
		riderDamage.Delete(charID)
		pendingGiantBoon.Delete(charID)
		refundMissedSpends(charID) // Divine Smite, Paladin's Smite, Eldritch Smite are cast on a hit
	} else {
		frenzyPending.Delete(charID)
		keepOnceAttack(charID) // the hit keeps Lifedrinker's and Primal Strike's once-a-turn use (F147)
		clearPendingSpends(charID)
	}
	info := map[string]interface{}{"target_id": targetID, "target": name, "hit": hit}
	var text string
	if !hit {
		text = attackHead(weaponLabel, o) + ". MISS."
		if o.Fumble {
			text = attackHead(weaponLabel, o) + ". MISS (a natural 1)."
		}
		if v, ok := attackExtraNote.LoadAndDelete(charID); ok { // Defender's transfer and a hurled hammer happen on a miss too
			text += v.(string)
		}
		if o.SneakUsed { // a missed attack did not use up the Sneak Attack for the turn
			db.Exec(`UPDATE characters SET sneak_attack_used = false WHERE id = $1`, charID)
		}
		if hasMastery && mu.Name == "Graze" && mu.Mod > 0 {
			gd, gh := adjustMonsterDamage(fmt.Sprint(t["monster_key"]), o.DamageType, mu.Mod)
			if gd > 0 {
				hp -= gd
				grazeNote := ""
				if hp <= 0 {
					hp, grazeNote = undeadFortitude(lobbyID, t, hp, gd, o.DamageType, false) // Zombie (dodge_2024.go)
				}
				if hp < 0 {
					hp = 0
				}
				t["hp"] = hp
				saved, err := saveMonsterHit(lobbyID, targetID, t0, t, hpStart-hp)
				if err != nil {
					return "", nil, err
				}
				hp = saved
				text += fmt.Sprintf(" Graze: %s still takes %d %s damage%s.", name, gd, o.DamageType, map[string]string{"resistant": " (it resists)", "vulnerable": " (it is vulnerable)"}[gh]) + grazeNote
				info["graze_damage"] = gd
				if hp == 0 {
					text += fmt.Sprintf(" %s is defeated!", name)
				}
			}
		}
		text += studiedAttacks(lobbyID, charID, targetID)
		info["status"] = game.MonsterHealth(hp, maxHP)
		return text, info, nil
	}
	riders := takeRiders(charID)
	riderSum := 0
	for _, v := range riders {
		riderSum += v
	}
	base := o.Damage - riderSum
	if base < 0 {
		base = 0
	}
	savageText := ""
	if o.Dice != "" {
		base, savageText = savageAttackerReroll(charID, o.Dice, o.Crit, o.DiceMod, base)
	}
	ignoreRes, owExtra := boonOffense(charID, o.DamageType, o.Natural)
	base += owExtra // Boon of Irresistible Offense, Overwhelming Strike
	dmg, how := adjustMonsterDamage(fmt.Sprint(t["monster_key"]), o.DamageType, base)
	if how != "resistant" && how != "immune" {
		if d, ok := underwaterFire(lobbyID, o.DamageType, dmg); ok {
			dmg, how = d, "resistant"
		}
	}
	dmg, how = petrifiedDamage(lobbyID, targetID, dmg, how) // a Petrified creature resists all damage (F196)
	if ignoreRes && how == "resistant" {                    // Overcome Defenses
		dmg, how = base, ""
	}
	heldV, _ := attackHeldItem.LoadAndDelete(charID)
	heldItem, _ := heldV.(string)
	_, vorpalAttuned := attunedTo(charID, "vorpal sword")
	vorpal := vorpalHeld(heldItem) && vorpalAttuned                 // without attunement, only its nonmagical benefits
	if vorpal && how == "resistant" && o.DamageType == "slashing" { // the Vorpal Sword ignores Resistance to Slashing damage
		dmg, how = base, ""
	}
	extraDmg, riderText := adjustRiders(fmt.Sprint(t["monster_key"]), riders)
	if extraDmg > 0 && monsterPetrified(lobbyID, targetID) {
		extraDmg /= 2 // F196
	}
	defer noteRegenerationBlock(lobbyID, targetID, o.DamageType) // Troll: no Regeneration on its next turn after Acid or Fire
	for rt := range riders {
		defer noteRegenerationBlock(lobbyID, targetID, rt)
	}
	hp -= dmg + extraDmg
	vorpalNote := ""
	if vorpal && o.Natural == 20 && hp > 0 { // a roll of 20: the beheading (named_weapons_2024.go)
		cut, killed, note := vorpalBehead(lobbyID, targetID, fmt.Sprint(t["monster_key"]), name)
		vorpalNote = note
		if killed {
			hp = 0
		} else {
			hp -= cut
			extraDmg += cut
		}
		info["vorpal"] = strings.TrimSpace(note)
	}
	if heldItem != "" && hp > 0 { // Holy Avenger, the Rod's blade and powers, the Hammer of Thunderbolts (legendary_more_2024.go)
		wx, dies, wtext := weaponHitEffects(lobbyID, charID, targetID, fmt.Sprint(t["monster_key"]), name, heldItem, o.Crit, o.Natural, hp)
		hp -= wx
		extraDmg += wx
		if dies {
			hp = 0
		}
		vorpalNote += wtext
	}
	if hp > 0 {
		vorpalNote += remarkableAthleteNote(charID, o.Crit)                                                           // Champion (fighter_fixes_2024.go, F94)
		if dx, dt := divineStrikeHit(lobbyID, charID, fmt.Sprint(t["monster_key"]), o.Crit, ""); dx > 0 || dt != "" { // Blessed Strikes (cleric_fixes_2024.go, F82)
			hp -= dx
			extraDmg += dx
			vorpalNote += dt
		}
		if dx, dt := enlargeHit(lobbyID, charID, dmg+extraDmg); dx != 0 { // Enlarge/Reduce (spells_more_2024.go)
			hp -= dx
			extraDmg += dx
			vorpalNote += dt
		}
		if dx, dt := divineFavorHit(charID, fmt.Sprint(t["monster_key"]), o.Weapon, o.Crit); dx > 0 { // Divine Favor (buff_spells_2024.go)
			hp -= dx
			extraDmg += dx
			vorpalNote += dt
		}
		if dx, dt := helmFlamesHit(charID, fmt.Sprint(t["monster_key"]), o.Crit); dx > 0 { // Helm of Brilliance (F152)
			hp -= dx
			extraDmg += dx
			vorpalNote += dt
		}
		if dx, dt := bracersOfArchery(charID, o.Weapon); dx > 0 { // Bracers of Archery (items_c4_2024.go)
			hp -= dx
			extraDmg += dx
			vorpalNote += dt
		}
	}
	if v, ok := attackExtraNote.LoadAndDelete(charID); ok {
		vorpalNote += v.(string)
	}
	wakeNote := ""
	if dmg+extraDmg > 0 {
		wakeNote = wakeOnDamage(lobbyID, targetID, name) // Hypnotic Pattern, Sleep
	}
	if hp <= 0 {
		var pnote string
		hp, pnote = polymorphDrop(lobbyID, t, hp) // a Polymorph's beast form at 0: the true form, the extra damage carried over
		vorpalNote += pnote
	}
	if hp <= 0 {
		var unote string
		hp, unote = undeadFortitude(lobbyID, t, hp, dmg+extraDmg, o.DamageType, o.Crit) // Zombie (dodge_2024.go)
		if unote != "" {
			vorpalNote += " [" + strings.TrimSuffix(strings.TrimSpace(unote), ".") + "]" // the sentence goes on after it
		}
	}
	if hp <= 0 {
		vorpalNote += killEffectNote(charID) // Dark One's Blessing (F134)
	}
	if hp < 0 {
		hp = 0
	}
	t["hp"] = hp
	saved, err := saveMonsterHit(lobbyID, targetID, t0, t, hpStart-hp)
	if err != nil {
		return "", nil, err
	}
	hp = saved
	status := game.MonsterHealth(hp, maxHP)
	label := "HIT!"
	if o.Crit {
		label = "CRITICAL HIT!"
	}
	if peerless {
		label += " (Peerless Aim turns the miss into a hit)"
	}
	text = fmt.Sprintf("%s. %s %s takes %d %s damage", attackHead(weaponLabel, o), label, name, dmg, o.DamageType)
	text += o.SneakNote + savageText
	switch how {
	case "immune":
		text = fmt.Sprintf("%s. %s But %s is immune to %s damage", attackHead(weaponLabel, o), label, name, o.DamageType)
	case "resistant":
		text += " (it resists)"
	case "vulnerable":
		text += " (it is vulnerable)"
	}
	text += riderText + vorpalNote + hunterLore(charID, targetID, fmt.Sprint(t["monster_key"])) + wakeNote
	knockedOut := false
	if hp == 0 && o.KnockOut {
		// Knocking Out a Creature: a melee attack that would reduce it to 0 Hit Points can leave it at 1 Hit Point, Unconscious
		// (it starts a Short Rest; it wakes when it regains Hit Points or someone succeeds on a DC 10 Wisdom (Medicine) check to give first aid).
		hp, knockedOut = 1, true
		status = game.MonsterHealth(hp, maxHP)
		if err := setMonsterHP(lobbyID, targetID, 1); err == nil {
			applyCondition(lobbyID, targetID, "unconscious", "knocked out", 600, "", 0)
		}
	}
	if knockedOut {
		text += fmt.Sprintf(". %s is knocked out: it drops Unconscious at 1 Hit Point (it wakes after a Short Rest, if it regains Hit Points, or with first aid: DC 10 Wisdom (Medicine)).", name)
	} else if hp == 0 {
		text += fmt.Sprintf(". %s is defeated!", name)
	} else {
		text += fmt.Sprintf(". %s looks %s.", name, status)
	}
	info["damage"], info["damage_type"], info["status"], info["defeated"] = dmg+extraDmg, o.DamageType, status, hp == 0
	if knockedOut {
		info["knocked_out"] = true
	}
	if hasMastery && hp > 0 {
		text += masteryOnHit(lobbyID, charID, targetID, name, fmt.Sprint(t["monster_key"]), mu, dmg)
	}
	if hasMastery && mu.Name == "Cleave" {
		if len(cleaveTarget) > 0 && cleaveTarget[0] != 0 {
			text += cleaveAttack(lobbyID, charID, targetID, cleaveTarget[0], o, mu)
		} else if !cleaveUsedThisTurn(charID) {
			text += " (Cleave: with this weapon a hit lets you attack a second creature within 5 feet of the first, once per turn: send cleave_target_id with the attack.)"
		}
	}
	text += superiorHuntersPrey(lobbyID, charID, targetID)
	text += applyGiantBoon(lobbyID, charID, targetID, dmg+extraDmg, hp > 0) // Goliath: Fire's Burn, Frost's Chill, Hill's Tumble
	if hp <= 0 {
		pendingCunning.Delete(charID)
		pendingBrutal.Delete(charID)
	}
	if hp > 0 {
		text += applyCunningStrike(lobbyID, charID, targetID, name, fmt.Sprint(t["monster_key"]))
		text += applyStunningStrike(lobbyID, charID, targetID, name)
		text += applyBrutalEffects(lobbyID, charID, targetID, name, fmt.Sprint(t["monster_key"]))
	}
	noteCharHitOnMonster(lobbyID, targetID, charHitOnMonster{CharID: charID, Total: o.Total, AC: ac, Nat20: o.Natural == 20 && !peerless,
		Melee: weaponIsMelee(o.Weapon), DamageType: o.DamageType, Weapon: o.Weapon, HPBefore: hpStart, HPAfter: hp})
	return text, info, nil
}

// wantsKnockOut: the player says the attack is meant to knock the target out rather than kill it.
func wantsKnockOut(description string) bool {
	d := strings.ToLower(description)
	if strings.Contains(d, "cunning strike") { // the Rogue's Cunning Strike has its own "knock out" effect
		d = strings.NewReplacer("knock out", "", "knock-out", "").Replace(d)
	}
	for _, k := range []string{"knock out", "knock it out", "knock him out", "knock her out", "knock them out", "knocking out", "knock-out", "nonlethal", "non-lethal", "subdue", "take alive", "take it alive", "take him alive", "take her alive", "take them alive"} {
		if strings.Contains(d, k) {
			return true
		}
	}
	return false
}
