package main

// Bard, 2024 rules (SRD 5.2.1): Bardic Inspiration.
//	bardic_inspiration        a Bonus Action: another creature within 60 feet gains one Bardic Inspiration die (name them: "bardic inspiration on Kestrel").
//	                          It lasts an hour. Uses: the Charisma modifier (minimum 1) per Long Rest; level 5 (Font of Inspiration) every rest.
//	use_bardic_inspiration    when the creature fails a D20 Test it can roll the die and add the number to the d20: write the failed total and
//	                          the DC ("use bardic inspiration total 11 dc 14") and the server says whether it now succeeds.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

func ensureBardColumns() {
	if db != nil {
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS bardic_die INTEGER DEFAULT 0`)
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS bardic_die_until TIMESTAMPTZ`)
	}
}

// giveBardicInspiration is the Bonus Action of a Bard.
func giveBardicInspiration(charID int, description string) string {
	var class string
	var level, lobby int
	if db.QueryRow(`SELECT class, level, COALESCE(lobby_id,0) FROM characters WHERE id = $1`, charID).Scan(&class, &level, &lobby) != nil {
		return "Character not found."
	}
	if strings.ToLower(class) != "bard" {
		return "Only a Bard has Bardic Inspiration."
	}
	// F251: the recipient from target / target_id, else any character the description names (first name too: "on Aldren")
	target := 0
	if v, ok := featureTargets.Load(charID); ok {
		for _, id := range v.([]int) {
			if id > 0 && id != charID {
				target = id
				break
			}
		}
	}
	if target == 0 {
		for _, id := range parseTargetsFromDescription(description, charID) {
			if id != charID {
				target = id
				break
			}
		}
	}
	if target <= 0 {
		return "Cannot inspire: name another creature (\"target\": \"Aldren\", target_id, or \"bardic inspiration on Aldren\"). Nothing was spent."
	}
	var tname string
	var tlobby int
	if db.QueryRow(`SELECT name, COALESCE(lobby_id,0) FROM characters WHERE id = $1`, target).Scan(&tname, &tlobby) != nil || tlobby != lobby {
		return "Cannot inspire: that creature is not in your party. Nothing was spent."
	}
	if okRes, msg, _ := useClassResource(charID, "bardic_inspiration", 1); !okRes {
		return "Cannot inspire: " + msg
	}
	die := game.BardicInspirationDie(level)
	db.Exec(`UPDATE characters SET bardic_die = $1, bardic_die_until = $2 WHERE id = $3`, die, time.Now().Add(time.Hour), target)
	return fmt.Sprintf("🎶 Bardic Inspiration: %s gains a d%d for the next hour. When they fail a D20 Test they can roll it and add the number to the d20 (use_bardic_inspiration).", tname, die)
}

var totalDCRe = regexp.MustCompile(`(?i)total\D*(-?\d+).*?dc\D*(\d+)`)

// useBardicInspiration spends the die a creature was given.
func useBardicInspiration(charID int, description string) string {
	var die int
	var until *time.Time
	if db.QueryRow(`SELECT COALESCE(bardic_die,0), bardic_die_until FROM characters WHERE id = $1`, charID).Scan(&die, &until) != nil || die == 0 {
		return "You have no Bardic Inspiration die."
	}
	if until != nil && time.Now().After(*until) {
		db.Exec(`UPDATE characters SET bardic_die = 0, bardic_die_until = NULL WHERE id = $1`, charID)
		return "Your Bardic Inspiration die has faded (it lasts an hour)."
	}
	roll := game.RollDie(die)
	db.Exec(`UPDATE characters SET bardic_die = 0, bardic_die_until = NULL WHERE id = $1`, charID)
	if m := totalDCRe.FindStringSubmatch(description); m != nil {
		total, _ := strconv.Atoi(m[1])
		dc, _ := strconv.Atoi(m[2])
		verdict := "still fails"
		if total+roll >= dc {
			verdict = "now succeeds"
		}
		return fmt.Sprintf("🎶 Bardic Inspiration: d%d = %d; %d + %d = %d against DC %d: it %s.", die, roll, total, roll, total+roll, dc, verdict)
	}
	return fmt.Sprintf("🎶 Bardic Inspiration: d%d = %d. Add %d to the d20 of the failed test.", die, roll, roll)
}
