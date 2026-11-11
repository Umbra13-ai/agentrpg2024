package main

// What a GM may see and a player may not: monster AC, hit points and stat blocks.
// Players see a monster's name, type and a descriptive health ("wounded", "bloodied").
// They never see its Armor Class, exact hit points, abilities, actions or resistances.
// The GM gets all of it: GET /api/campaigns/{id}/combat and GET /api/gm/status list
// every combatant with `ac`, `hp`, `max_hp` and a `stat_block`, and
// GET /api/universe/monsters/{slug} returns the full stat block to a GM.

import (
	"encoding/json"
	"net/http"
	"regexp"
)

// requestIsGM reports whether the caller is signed in and runs at least one campaign (or is a moderator).
func requestIsGM(r *http.Request) bool {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		return false
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM lobbies WHERE dm_id = $1`, agentID).Scan(&n)
	return n > 0
}

// requestIsGMOf reports whether the caller is the GM of this campaign.
func requestIsGMOf(r *http.Request, campaignID int) bool {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		return false
	}
	var dm int
	db.QueryRow(`SELECT COALESCE(dm_id, 0) FROM lobbies WHERE id = $1`, campaignID).Scan(&dm)
	return dm == agentID
}

// monsterStatBlock is the full stat block of a monster, for the GM.
func monsterStatBlock(slug string) map[string]interface{} {
	if slug == "" {
		return nil
	}
	var name, size, mtype, hitDice, cr, resist, immune, vuln, condImm string
	var ac, hp, speed, str, dex, con, intl, wis, cha, xp int
	var actions []byte
	err := db.QueryRow(`SELECT name, COALESCE(size,''), COALESCE(type,''), COALESCE(ac,0), COALESCE(hp,0), COALESCE(hit_dice,''), COALESCE(speed,0),
		COALESCE(str,10), COALESCE(dex,10), COALESCE(con,10), COALESCE(intl,10), COALESCE(wis,10), COALESCE(cha,10), COALESCE(cr,''), COALESCE(xp,0), COALESCE(actions,'[]'),
		COALESCE(damage_resistances,''), COALESCE(damage_immunities,''), COALESCE(damage_vulnerabilities,''), COALESCE(condition_immunities,'')
		FROM monsters WHERE slug = $1`, slug).
		Scan(&name, &size, &mtype, &ac, &hp, &hitDice, &speed, &str, &dex, &con, &intl, &wis, &cha, &cr, &xp, &actions, &resist, &immune, &vuln, &condImm)
	if err != nil {
		return nil
	}
	m := map[string]interface{}{"name": name, "size": size, "type": mtype, "ac": ac, "hp": hp, "hit_dice": hitDice, "speed": speed,
		"str": str, "dex": dex, "con": con, "int": intl, "wis": wis, "cha": cha, "cr": cr, "xp": xp, "actions": json.RawMessage(actions)}
	if resist != "" {
		m["damage_resistances"] = resist
	}
	if immune != "" {
		m["damage_immunities"] = immune
	}
	if vuln != "" {
		m["damage_vulnerabilities"] = vuln
	}
	if condImm != "" {
		m["condition_immunities"] = condImm
	}
	return m
}

// gmCombatants lists every combatant with the numbers only the GM may see.
func gmCombatants(turnOrderJSON []byte, lobbyID int) []map[string]interface{} {
	var entries []struct {
		ID         int    `json:"id"`
		Name       string `json:"name"`
		Initiative int    `json:"initiative"`
		IsMonster  bool   `json:"is_monster"`
		MonsterKey string `json:"monster_key"`
		HP         int    `json:"hp"`
		MaxHP      int    `json:"max_hp"`
		AC         int    `json:"ac"`
	}
	json.Unmarshal(turnOrderJSON, &entries)
	out := []map[string]interface{}{}
	for _, e := range entries {
		c := map[string]interface{}{"id": e.ID, "name": e.Name, "initiative": e.Initiative, "is_monster": e.IsMonster}
		if e.IsMonster {
			if cs := monsterConditionViews(lobbyID, e.ID); len(cs) > 0 {
				c["conditions"] = cs
			}
			if ef := combatEffectViews(lobbyID, e.ID); len(ef) > 0 {
				c["effects"] = ef
			}
		}
		if sp := spellEffectViews(lobbyID, e.ID); len(sp) > 0 {
			c["spell_effects"] = sp
		}
		if !e.IsMonster {
			if dk := deckEffects(e.ID); len(dk) > 0 {
				c["deck_effects"] = dk // lasting Mysterious Deck cards: the Flames' devil, the Rogue, the Donjon ...
			}
		}
		if e.IsMonster {
			c["ac"], c["hp"], c["max_hp"] = e.AC, e.HP, e.MaxHP
			if e.MonsterKey != "" {
				c["monster_key"] = e.MonsterKey
				if sb := monsterStatBlock(e.MonsterKey); sb != nil {
					c["stat_block"] = sb
				}
			}
		} else {
			var hp, maxHP, ac int
			if db.QueryRow(`SELECT hp, max_hp, ac FROM characters WHERE id = $1`, e.ID).Scan(&hp, &maxHP, &ac) == nil {
				c["ac"], c["hp"], c["max_hp"] = ac, hp, maxHP
			}
		}
		out = append(out, c)
	}
	return out
}

var revealsStats = regexp.MustCompile(`(?i)\b(?:AC|armor class)\s*(?:of\s*)?\d+\b|\b\d+\s*(?:hp|hit points)\b|\b(?:hp|hit points)\s*[:=]?\s*\d+\b`)

// statLeakWarning warns a GM whose narration seems to give away a number players should not see.
func statLeakWarning(narration string) string {
	if revealsStats.MatchString(narration) {
		return "Your narration seems to include an Armor Class or hit point number. Players should learn only whether an attack hit or missed and how a creature looks (wounded, bloodied), never its AC or exact hit points. The narration was posted as written; edit it with the GM tools if it was not meant to be shown."
	}
	return ""
}
