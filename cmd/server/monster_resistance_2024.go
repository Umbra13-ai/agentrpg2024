package main

// One locked owner for Legendary Resistance counts. Concentration decisions join
// their caller's transaction; automatic saves, the GM command and item effects
// spend through the same rule. General monster-state snapshots never write counts.
import "database/sql"

// Resistance state is read/spent under its row lock, through the same lifecycle
// transaction. Other fields are preserved; no read/overwrite of a stale count.
func monsterResistanceIn(tx *sql.Tx, lobby, id int, meta *monsterMeta, spend bool) (int, error) {
	if _, err := tx.Exec(`INSERT INTO combat_monster_state(lobby_id,monster_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, lobby, id); err != nil {
		return 0, err
	}
	var s monsterState
	if err := tx.QueryRow(`SELECT in_lair,lr_used FROM combat_monster_state WHERE lobby_id=$1 AND monster_id=$2 FOR UPDATE`, lobby, id).Scan(&s.InLair, &s.LRUsed); err != nil {
		return 0, err
	}
	_, total := legendaryUseTotals(meta, s)
	left := max(0, total-s.LRUsed)
	if spend && left > 0 {
		if _, err := tx.Exec(`UPDATE combat_monster_state SET lr_used=lr_used+1 WHERE lobby_id=$1 AND monster_id=$2`, lobby, id); err != nil {
			return 0, err
		}
		left--
	}
	return left, nil
}

func spendMonsterResistance(lobby, id int, meta *monsterMeta) (left int, spent bool, err error) {
	err = inTx(func(tx *sql.Tx) error {
		var err error
		left, err = monsterResistanceIn(tx, lobby, id, meta, false)
		if err != nil || left == 0 {
			return err
		}
		left, err = monsterResistanceIn(tx, lobby, id, meta, true)
		spent = err == nil
		return err
	})
	return
}
