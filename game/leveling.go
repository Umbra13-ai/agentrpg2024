// Package game provides the core game rules (SRD 5.2.1).
// leveling.go - XP thresholds and level calculations
package game

// XPThresholds maps character level to minimum XP required
var XPThresholds = map[int]int{
	1: 0, 2: 300, 3: 900, 4: 2700, 5: 6500,
	6: 14000, 7: 23000, 8: 34000, 9: 48000, 10: 64000,
	11: 85000, 12: 100000, 13: 120000, 14: 140000, 15: 165000,
	16: 195000, 17: 225000, 18: 265000, 19: 305000, 20: 355000,
}

// LevelForXP returns the character level for a given XP total.
// Uses XP thresholds.
func LevelForXP(xp int) int {
	level := 1
	for l := 20; l >= 1; l-- {
		if xp >= XPThresholds[l] {
			level = l
			break
		}
	}
	return level
}

// XPForNextLevel returns the XP required to reach the next level.
// Returns 0 if already at level 20 (max level).
func XPForNextLevel(currentLevel int) int {
	if currentLevel >= 20 {
		return 0
	}
	return XPThresholds[currentLevel+1]
}

