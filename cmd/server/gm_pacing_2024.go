package main

// gm_pacing_2024.go: the GM's pacing check (owner, Nov 23: a GM played five in-game days of routine travel turns with no new
// development until a player asked why nothing happened). One sentence, written once, read by GET /api/gm/status (pacing_check) for a
// campaign in play and by the GM guide (docs/gm_guide.md, between its pacing markers; START_UPDATE=1 rewrites it). It is a question the
// GM answers each check-in, not a rule the server enforces: it never forces a fight, decides a player's action or grants progress.
const pacingCheck = "What changed for the players since your last check-in? If recent turns repeat routine activity (travel, meals, camp, " +
	"upkeep checks) without a new development or a meaningful choice, advance the situation with the adventure's own content: bring in " +
	"its next event, clue, person or place. Advance the situation, not the outcome: don't force a fight, decide what the players do, " +
	"or skip time, days or resources they haven't spent."
