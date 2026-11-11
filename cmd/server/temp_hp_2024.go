package main

// Temporary Hit Points (SRD 5.2.1, Rules Glossary):
//   - They last until they're depleted or you finish a Long Rest. F67: the Long Rest now ends them (combat_handlers.go).
//   - They don't stack: "you decide whether to keep the ones you have or to gain the new ones". Every grant keeps the larger pool
//     (GREATEST), which is the choice a player makes.
//   - They aren't Hit Points or healing, and they don't wake a character at 0 Hit Points (charAtZeroHP reads hp only).
// F68: False Life gave nothing (no healing or damage data, no handler). It gives 2d4 + 4, 5 more per slot level above 1; a Warlock with
// Fiendish Vigor casts it at will and takes the highest number on the dice (12).

import "fmt"

// grantTempHP gives a character Temporary Hit Points, keeping the larger pool, and returns the pool it has afterwards.
func grantTempHP(charID, n int) int {
	db.Exec(`UPDATE characters SET temp_hp = GREATEST(COALESCE(temp_hp,0), $1) WHERE id = $2`, n, charID)
	var now int
	db.QueryRow(`SELECT COALESCE(temp_hp,0) FROM characters WHERE id = $1`, charID).Scan(&now)
	return now
}

// falseLifeCast resolves False Life (Self).
func falseLifeCast(charID, slotLevel int) string {
	inv, _, has := getAtWillInvocationForSpell(charID, "false-life")
	vigor := has && inv == "fiendish-vigor"
	n, how := 0, ""
	if vigor {
		n, how = 12, " (Fiendish Vigor: the highest number on the dice)"
	} else {
		n = rollExpr("2d4+4")
		if slotLevel > 1 {
			n += 5 * (slotLevel - 1)
		}
	}
	now := grantTempHP(charID, n)
	if now > n {
		return fmt.Sprintf(" False Life: %d Temporary Hit Points%s, but you keep the %d you already have (Temporary Hit Points don't add together; the larger pool stays).", n, how, now)
	}
	return fmt.Sprintf(" False Life: you gain %d Temporary Hit Points%s. They last until they're used up or you finish a Long Rest, and don't add to other Temporary Hit Points.", n, how)
}
