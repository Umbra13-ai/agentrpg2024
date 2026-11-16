package main

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	_ "github.com/mattn/go-sqlite3"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSQLiteDeadCharacterCannotLongRest(t *testing.T) {
	originalDB := db
	testDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db = testDB
	t.Cleanup(func() { _ = testDB.Close(); db = originalDB })
	_, err = db.Exec(`CREATE TABLE characters (
		id INTEGER PRIMARY KEY, class TEXT, level INTEGER, con INTEGER, wis INTEGER,
		hit_dice_spent INTEGER, exhaustion_level INTEGER, last_long_rest TIMESTAMP,
		subclass TEXT, is_dead BOOLEAN, hp INTEGER, max_hp INTEGER
	);
	INSERT INTO characters (id, class, level, con, wis, is_dead, hp, max_hp)
	VALUES (34, 'wizard', 1, 10, 14, true, 0, 7);`)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handleRest(w, httptest.NewRequest(http.MethodPost, "/api/characters/34/rest", nil), 34)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "dead_character_cannot_rest") {
		t.Fatalf("dead rest response: status %d, body %s", w.Code, w.Body.String())
	}
	var hp int
	var lastRest sql.NullTime
	if err := db.QueryRow("SELECT hp, last_long_rest FROM characters WHERE id = 34").Scan(&hp, &lastRest); err != nil {
		t.Fatal(err)
	}
	if hp != 0 || lastRest.Valid {
		t.Fatalf("dead rest mutated character: hp=%d, last_rest=%v", hp, lastRest)
	}
}

func setupSQLiteTestDB(t *testing.T) *sql.DB {
	t.Helper()

	originalDB := db
	testDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}

	schema := `
CREATE TABLE characters (
	id INTEGER PRIMARY KEY,
	name TEXT,
	conditions TEXT,
	exhaustion_level INTEGER DEFAULT 0
);`
	if _, err := testDB.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	db = testDB
	t.Cleanup(func() {
		_ = testDB.Close()
		db = originalDB
	})

	return testDB
}

func TestAbandonedPlayerAdvanceRequiresSkippableCombatTurn(t *testing.T) {
	now := time.Date(2026, 10, 16, 5, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		inCombat  bool
		currentID int
		playerID  int
		started   sql.NullTime
		want      bool
	}{
		{"exploration", false, 0, 32, sql.NullTime{}, true},
		{"fresh current turn", true, 32, 32, sql.NullTime{Time: now.Add(-time.Hour), Valid: true}, false},
		{"exact timeout", true, 32, 32, sql.NullTime{Time: now.Add(-4 * time.Hour), Valid: true}, true},
		{"other player", true, 35, 32, sql.NullTime{Time: now.Add(-5 * time.Hour), Valid: true}, false},
		{"monster turn", true, 0, 32, sql.NullTime{Time: now.Add(-5 * time.Hour), Valid: true}, false},
		{"missing clock", true, 32, 32, sql.NullTime{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldAdvanceAbandonedPlayer(tc.inCombat, tc.currentID, tc.playerID, tc.started, now, 4*time.Hour); got != tc.want {
				t.Fatalf("shouldAdvanceAbandonedPlayer() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSQLiteStabilizeConsumesActionAndUpdatesOnlyDyingPatient(t *testing.T) {
	if got := getActionResourceType("stabilize"); got != "action" {
		t.Fatalf("stabilize resource = %q, want action", got)
	}
	originalDB := db
	testDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db = testDB
	t.Cleanup(func() { _ = testDB.Close(); db = originalDB })
	_, err = db.Exec(`CREATE TABLE characters (
		id INTEGER PRIMARY KEY, wis INTEGER, level INTEGER,
		skill_proficiencies TEXT, expertise TEXT, hp INTEGER,
		is_stable BOOLEAN DEFAULT false, stable_rounds_left INTEGER DEFAULT 0, is_dead BOOLEAN DEFAULT false,
		death_save_successes INTEGER, death_save_failures INTEGER
	);
	INSERT INTO characters (id, wis, level, skill_proficiencies, expertise, hp) VALUES (1, 30, 1, 'medicine', '', 9);
	INSERT INTO characters (id, wis, level, skill_proficiencies, expertise, hp, death_save_successes, death_save_failures)
		VALUES (2, 10, 1, '', '', 0, 2, 1);`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := resolveStabilizeAction(1, 2, "Patient")
	if err != nil || result == "" {
		t.Fatalf("stabilize result=%q err=%v", result, err)
	}
	var stable bool
	var hp, successes, failures int
	if err := db.QueryRow(`SELECT is_stable, hp, death_save_successes, death_save_failures FROM characters WHERE id = 2`).
		Scan(&stable, &hp, &successes, &failures); err != nil {
		t.Fatal(err)
	}
	// SRD 5.2.1: "The number of both is reset to zero when you regain any Hit Points or become Stable" (F210)
	if !stable || hp != 0 || successes != 0 || failures != 0 {
		t.Fatalf("first aid must stabilize without healing and reset both tallies: stable=%v hp=%d saves=%d/%d", stable, hp, successes, failures)
	}
	if _, err := db.Exec(`UPDATE characters SET is_stable = false, hp = 5, death_save_successes = 0 WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	_, err = resolveStabilizeAction(1, 2, "Patient")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT is_stable FROM characters WHERE id = 2`).Scan(&stable); err != nil || stable {
		t.Fatalf("healed patient must not become stable at positive HP: stable=%v err=%v", stable, err)
	}
}

func seedCharacter(t *testing.T, testDB *sql.DB, id int, name string, conditionsJSON string, exhaustion int) {
	t.Helper()
	_, err := testDB.Exec(
		`INSERT INTO characters (id, name, conditions, exhaustion_level) VALUES (?, ?, ?, ?)`,
		id, name, conditionsJSON, exhaustion,
	)
	if err != nil {
		t.Fatalf("insert character: %v", err)
	}
}

func TestSQLiteConditionHelpers(t *testing.T) {
	testDB := setupSQLiteTestDB(t)
	seedCharacter(t, testDB, 1, "Ariel", `["prone","restrained"]`, 0)

	if !hasCondition(1, "prone") {
		t.Fatal("expected prone condition")
	}
	if hasCondition(1, "blinded") {
		t.Fatal("did not expect blinded condition")
	}

	conditions := getCharConditions(1)
	if len(conditions) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(conditions))
	}

	// removing a condition is the condition owner's (condition_owner_2024.go: a Postgres transaction with the row locked);
	// conditions_rule_test.py exercises it against the server
}

func TestSQLiteIncapacitationAndMovement(t *testing.T) {
	testDB := setupSQLiteTestDB(t)
	seedCharacter(t, testDB, 2, "Bran", `["stunned"]`, 0)
	seedCharacter(t, testDB, 3, "Cleo", `["paralyzed"]`, 0)
	seedCharacter(t, testDB, 4, "Dain", `[]`, 5)

	if !isIncapacitated(2) {
		t.Fatal("expected stunned character to be incapacitated")
	}
	if !canMove(2) {
		t.Fatal("2024: a stunned character keeps its Speed")
	}
	if canMove(3) {
		t.Fatal("expected paralyzed character to be unable to move")
	}
	if s := (&saveCtx{saver: 2, ability: "dex", dc: 10}).gather(); s.AutoFail != "Stunned" {
		t.Fatalf("expected stunned character to auto-fail dex save, got %q", s.AutoFail)
	}

	// a hit on a Paralyzed creature from within 5 feet is a Critical Hit: the one attack function decides it (attack_2024.go)
	near := true
	if c := (&attackCtx{target: 3, melee: true, within5: &near}); !c.gather().AutoCrit {
		t.Fatal("expected a hit on a paralyzed target from within 5 feet to be a Critical Hit")
	}

	if !canMove(4) {
		t.Fatal("2024: exhaustion level 5 only reduces Speed by 25 feet")
	}
}

func TestSQLiteSaveDisadvantageAndNames(t *testing.T) {
	testDB := setupSQLiteTestDB(t)
	seedCharacter(t, testDB, 5, "Eris", `["restrained"]`, 2)
	seedCharacter(t, testDB, 6, "Finn", `[]`, 3)

	saveOf := func(id int, ability string) game.SavingThrow {
		return (&saveCtx{saver: id, ability: ability, dc: 10}).gather()
	}
	if !saveOf(5, "dex").HasDisadvantage() {
		t.Fatal("expected restrained character to have dex save disadvantage")
	}
	if s := saveOf(5, "wis"); s.HasDisadvantage() {
		t.Fatal("did not expect restrained character to have wis save disadvantage at exhaustion 2")
	} else if s.Modifier()-s.Bonus != -4 {
		t.Fatalf("Exhaustion 2 takes 4 from the save: modifier %d, adjustments %v", s.Modifier(), s.Adjust)
	}
	if saveOf(6, "wis").HasDisadvantage() {
		t.Fatal("2024: Exhaustion is a -2 per level penalty, not Disadvantage")
	}

	if got := getCharacterName(5); got != "Eris" {
		t.Fatalf("getCharacterName(5) = %q, want %q", got, "Eris")
	}
	if got := getCharacterName(999); got != "" {
		t.Fatalf("getCharacterName(999) = %q, want empty string for missing character", got)
	}
}

func TestNormalizeConditionList(t *testing.T) {
	input := []string{`["dodging"`, `"dodging"]`, "Dodging", "prone", "prone", ""}
	got := normalizeConditionList(input)

	if len(got) != 2 {
		t.Fatalf("expected 2 normalized conditions, got %d: %#v", len(got), got)
	}
	if got[0] != "dodging" || got[1] != "prone" {
		t.Fatalf("unexpected normalized conditions: %#v", got)
	}
}

func TestResponseConditionsDeduplicatesStatusData(t *testing.T) {
	got := responseConditions([]byte(`["dodging","dodging","prone","Dodging"]`))
	want := []string{"dodging", "prone"}
	if len(got) != len(want) {
		t.Fatalf("responseConditions() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("responseConditions() = %#v, want %#v", got, want)
		}
	}
}

func TestSpectatorConditionsNormalizesLegacyData(t *testing.T) {
	got := spectatorConditions(`"[\"dodging\",\"dodging\",\"charmed:5\"]"`)
	want := []string{"dodging", "charmed"}
	if len(got) != len(want) {
		t.Fatalf("spectatorConditions() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("spectatorConditions() = %#v, want %#v", got, want)
		}
	}
}

func TestCampaignStoryPayloadAcceptsStorySoFarAlias(t *testing.T) {
	story := campaignStoryUpdate("", "The party reached the lantern court.")
	if story != "The party reached the lantern court." {
		t.Fatalf("story alias = %q, want story_so_far value", story)
	}
	if got := campaignStoryUpdate("Current recap", "Legacy recap"); got != "Current recap" {
		t.Fatalf("current story = %q, want current payload to win", got)
	}
}

func TestNarrativeOnlyMonsterActionsDoNotResolveAsAttacks(t *testing.T) {
	for _, action := range []string{"reposition", " move ", "Retreat", "search"} {
		if !isNarrativeOnlyMonsterAction(action) {
			t.Fatalf("isNarrativeOnlyMonsterAction(%q) = false, want true", action)
		}
	}
	for _, action := range []string{"attack", "scimitar", "bite", ""} {
		if isNarrativeOnlyMonsterAction(action) {
			t.Fatalf("isNarrativeOnlyMonsterAction(%q) = true, want false", action)
		}
	}
}

func TestValidateGMNarrateRequestRequiresNarration(t *testing.T) {
	for _, narration := range []string{"", " \t\n "} {
		if got := validateGMNarrateRequest(gmNarrateRequest{Narration: narration}); got != "narration_required" {
			t.Fatalf("empty narration validation = %q, want narration_required", got)
		}
	}
	if got := validateGMNarrateRequest(gmNarrateRequest{Narration: "The gate opens."}); got != "" {
		t.Fatalf("non-empty narration validation = %q, want no error", got)
	}
}

func TestStartingCharacterInventoryIncludesCasterFocus(t *testing.T) {
	tests := map[string]string{
		"bard":     "Musical Instrument",
		"cleric":   "Holy Symbol",
		"druid":    "Druidic Focus",
		"paladin":  "Holy Symbol",
		"ranger":   "Druidic Focus",
		"sorcerer": "Arcane Focus",
		"warlock":  "Arcane Focus",
		"wizard":   "Arcane Focus",
	}
	for class, wantFocus := range tests {
		t.Run(class, func(t *testing.T) {
			items := startingCharacterInventory(class, []string{"Traveler's Clothes"})
			if len(items) != 2 {
				t.Fatalf("startingCharacterInventory(%q) returned %#v, want focus and background item", class, items)
			}
			if got := items[0]["name"]; got != wantFocus {
				t.Fatalf("focus for %q = %q, want %q", class, got, wantFocus)
			}
			if got := items[0]["type"]; got != "focus" {
				t.Fatalf("focus type for %q = %q, want focus", class, got)
			}
		})
	}

	if items := startingCharacterInventory("fighter", nil); len(items) != 0 {
		t.Fatalf("non-caster inventory = %#v, want no focus", items)
	}
}

func TestLatestMeaningfulPlayerActionTimeIgnoresNonNarrativeHousekeeping(t *testing.T) {
	testDB := setupSQLiteTestDB(t)
	if _, err := testDB.Exec(`CREATE TABLE actions (lobby_id INTEGER, character_id INTEGER, action_type TEXT, created_at TIMESTAMP)`); err != nil {
		t.Fatalf("create actions table: %v", err)
	}
	meaningfulAt := time.Date(2026, time.September, 1, 5, 23, 0, 0, time.UTC)
	pollAt := meaningfulAt.Add(time.Minute)
	followingAt := pollAt.Add(time.Minute)
	if _, err := testDB.Exec(`INSERT INTO actions (lobby_id, character_id, action_type, created_at) VALUES (?, ?, ?, ?), (?, ?, ?, ?), (?, ?, ?, ?)`, 6, 35, "other", meaningfulAt, 6, 35, "poll", pollAt, 6, 35, "following", followingAt); err != nil {
		t.Fatalf("seed actions: %v", err)
	}
	got := latestMeaningfulPlayerActionTime(6)
	if !got.Valid || !got.Time.Equal(meaningfulAt) {
		t.Fatalf("latest meaningful action = %#v, want %s (poll/following housekeeping must not count)", got, meaningfulAt)
	}
}

func TestIsFollowingPartyRequiresFollowAfterMeaningfulAction(t *testing.T) {
	meaningful := time.Date(2026, time.September, 11, 20, 0, 0, 0, time.UTC)
	following := meaningful.Add(time.Hour)
	if !isFollowingParty(sql.NullTime{Time: meaningful, Valid: true}, sql.NullTime{Time: following, Valid: true}) {
		t.Fatal("latest automatic follow should suppress another skip")
	}
	if isFollowingParty(sql.NullTime{Time: following, Valid: true}, sql.NullTime{Time: meaningful, Valid: true}) {
		t.Fatal("a player action after automatic follow should re-enable inactivity tracking")
	}
	if !isFollowingParty(sql.NullTime{}, sql.NullTime{Time: following, Valid: true}) {
		t.Fatal("a followed never-acting player should not be skipped repeatedly")
	}
}

func TestCharacterHPStatusReportsStableAtZeroHP(t *testing.T) {
	if got := characterHPStatus(0, 10, true, false); got != "stable" {
		t.Fatalf("stable 0 HP character status = %q, want stable", got)
	}
	if got := characterHPStatus(0, 10, false, false); got != "dying" {
		t.Fatalf("unstable 0 HP character status = %q, want dying", got)
	}
	if got := characterHPStatus(3, 10, false, false); got != "wounded" {
		t.Fatalf("3/10 character status = %q, want wounded", got)
	}
	if got := characterHPStatus(0, 10, false, true); got != "dead" {
		t.Fatalf("dead 0 HP character status = %q, want dead", got)
	}
	if got := characterHPStatus(0, 10, true, true); got != "dead" {
		t.Fatalf("dead status must take precedence over stale stable flag, got %q", got)
	}
}

func TestIsFollowingPartyDoesNotExpireForAbandonedCharacter(t *testing.T) {
	meaningful := time.Date(2026, time.September, 1, 20, 0, 0, 0, time.UTC)
	following := meaningful.Add(13 * time.Hour)

	if !isFollowingParty(sql.NullTime{Time: meaningful, Valid: true}, sql.NullTime{Time: following, Valid: true}) {
		t.Fatal("a following record must stay effective after twelve hours until a new meaningful action occurs")
	}
}

func TestIsMeaningfulActionTypeExcludesFollowing(t *testing.T) {
	if isMeaningfulActionType("following") {
		t.Fatal("following is system housekeeping, not a story beat")
	}
	if !isMeaningfulActionType("interact") {
		t.Fatal("interact should remain a meaningful action")
	}
}

func TestParseConditionsStringJSON(t *testing.T) {
	got := normalizeConditionList(parseConditionsString(`["dodging","prone","dodging"]`))
	if len(got) != 2 {
		t.Fatalf("expected 2 parsed conditions, got %d: %#v", len(got), got)
	}
}

func TestDisplayedMaxPlayers(t *testing.T) {
	if got := displayedMaxPlayers(3, 4); got != 4 {
		t.Fatalf("displayedMaxPlayers(3, 4) = %d, want 4", got)
	}
	if got := displayedMaxPlayers(4, 3); got != 4 {
		t.Fatalf("displayedMaxPlayers(4, 3) = %d, want 4", got)
	}
}

func setupSQLiteActionTestDB(t *testing.T) *sql.DB {
	t.Helper()

	originalDB := db
	testDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}

	schema := `
CREATE TABLE actions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	lobby_id INTEGER,
	character_id INTEGER,
	action_type TEXT,
	description TEXT,
	result TEXT,
	created_at DATETIME
);`
	if _, err := testDB.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	db = testDB
	t.Cleanup(func() {
		_ = testDB.Close()
		db = originalDB
	})

	return testDB
}

func TestHasCharacterActedSinceLastNarration(t *testing.T) {
	testDB := setupSQLiteActionTestDB(t)
	now := time.Now().UTC()

	if _, err := testDB.Exec(
		`INSERT INTO actions (lobby_id, character_id, action_type, description, created_at) VALUES (?, ?, ?, ?, ?)`,
		1, 11, "help", "first action", now.Add(-2*time.Minute),
	); err != nil {
		t.Fatalf("insert action: %v", err)
	}

	acted, actionType, description, _ := hasCharacterActedSinceLastNarration(11, 1)
	if !acted || actionType != "help" || description != "first action" {
		t.Fatalf("expected unresolved action, got acted=%v type=%q desc=%q", acted, actionType, description)
	}

	if _, err := testDB.Exec(
		`INSERT INTO actions (lobby_id, character_id, action_type, description, created_at) VALUES (?, NULL, ?, ?, ?)`,
		1, "narration", "GM resolves the beat", now.Add(-1*time.Minute),
	); err != nil {
		t.Fatalf("insert narration: %v", err)
	}

	acted, _, _, _ = hasCharacterActedSinceLastNarration(11, 1)
	if acted {
		t.Fatal("expected narration to clear unresolved player action")
	}
}

func TestActionReplaySinceLastNarration(t *testing.T) {
	testDB := setupSQLiteActionTestDB(t)
	now := time.Now().UTC()
	if _, err := testDB.Exec(
		`INSERT INTO actions (lobby_id, character_id, action_type, description, result, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		1, 11, "other", "I hold the bridge.", "Action recorded.", now,
	); err != nil {
		t.Fatalf("insert action: %v", err)
	}

	matched, result, actedAt := actionReplaySinceLastNarration(11, 1, "other", "I hold the bridge.")
	if !matched || result != "Action recorded." || !actedAt.Equal(now) {
		t.Fatalf("expected recorded action replay, got matched=%v result=%q actedAt=%s", matched, result, actedAt)
	}
	if matched, _, _ := actionReplaySinceLastNarration(11, 1, "other", "I take the bridge."); matched {
		t.Fatal("different action description must not be treated as a replay")
	}

	if _, err := testDB.Exec(
		`INSERT INTO actions (lobby_id, character_id, action_type, description, created_at) VALUES (?, NULL, ?, ?, ?)`,
		1, "narration", "The bridge holds.", now.Add(time.Minute),
	); err != nil {
		t.Fatalf("insert narration: %v", err)
	}
	if matched, _, _ := actionReplaySinceLastNarration(11, 1, "other", "I hold the bridge."); matched {
		t.Fatal("a new narration must end the prior exploration beat")
	}
}

// TestProficiencyBonus tests the 5e proficiency bonus calculation (pure function)
// Note: Now tests game.ProficiencyBonus after modularization
func TestProficiencyBonus(t *testing.T) {
	tests := []struct {
		level    int
		expected int
	}{
		{1, 2}, {2, 2}, {3, 2}, {4, 2}, // Levels 1-4: +2
		{5, 3}, {6, 3}, {7, 3}, {8, 3}, // Levels 5-8: +3
		{9, 4}, {10, 4}, {11, 4}, {12, 4}, // Levels 9-12: +4
		{13, 5}, {14, 5}, {15, 5}, {16, 5}, // Levels 13-16: +5
		{17, 6}, {18, 6}, {19, 6}, {20, 6}, // Levels 17-20: +6
	}

	for _, tt := range tests {
		got := game.ProficiencyBonus(tt.level)
		if got != tt.expected {
			t.Errorf("game.ProficiencyBonus(%d) = %d, want %d", tt.level, got, tt.expected)
		}
	}
}

// setupSQLiteTestDBWithSpecies creates a test DB with species column for species trait tests
func setupSQLiteTestDBWithSpecies(t *testing.T) *sql.DB {
	t.Helper()

	originalDB := db
	testDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}

	schema := `
CREATE TABLE characters (
	id INTEGER PRIMARY KEY,
	name TEXT,
	species TEXT,
	conditions TEXT,
	exhaustion_level INTEGER DEFAULT 0,
	lobby_id INTEGER DEFAULT 0
);
CREATE TABLE campaigns (
	id INTEGER PRIMARY KEY,
	combat_state TEXT DEFAULT '{}'
);`
	if _, err := testDB.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	db = testDB
	t.Cleanup(func() {
		_ = testDB.Close()
		db = originalDB
	})

	return testDB
}

func seedCharacterWithSpecies(t *testing.T, testDB *sql.DB, id int, name, species, conditionsJSON string, exhaustion int) {
	t.Helper()
	_, err := testDB.Exec(
		`INSERT INTO characters (id, name, species, conditions, exhaustion_level) VALUES (?, ?, ?, ?, ?)`,
		id, name, species, conditionsJSON, exhaustion,
	)
	if err != nil {
		t.Fatalf("insert character: %v", err)
	}
}

// TestSQLiteSpeciesDetection tests species detection helpers for species traits
func TestSQLiteSpeciesDetection(t *testing.T) {
	testDB := setupSQLiteTestDBWithSpecies(t)
	seedCharacterWithSpecies(t, testDB, 1, "Bilbo", "Halfling", `[]`, 0)
	seedCharacterWithSpecies(t, testDB, 2, "Frodo", "Lightfoot Halfling", `[]`, 0)
	seedCharacterWithSpecies(t, testDB, 3, "Gimli", "Dwarf", `[]`, 0)
	seedCharacterWithSpecies(t, testDB, 4, "Durkon", "Mountain Dwarf", `[]`, 0)
	seedCharacterWithSpecies(t, testDB, 5, "Gromm", "Orc", `[]`, 0)
	seedCharacterWithSpecies(t, testDB, 6, "Gnimble", "Forest Gnome", `[]`, 0)
	seedCharacterWithSpecies(t, testDB, 7, "Legolas", "Elf", `[]`, 0)
	seedCharacterWithSpecies(t, testDB, 8, "Mordai", "Tiefling", `[]`, 0)
	seedCharacterWithSpecies(t, testDB, 9, "Human", "Human", `[]`, 0)

	// Test Halfling detection
	if !isHalfling(1) {
		t.Error("expected Bilbo (Halfling) to be detected as halfling")
	}
	if !isHalfling(2) {
		t.Error("expected Frodo (Lightfoot Halfling) to be detected as halfling")
	}
	if isHalfling(9) {
		t.Error("expected Human to NOT be detected as halfling")
	}

	// Test Dwarf detection
	if !isDwarf(3) {
		t.Error("expected Gimli (Dwarf) to be detected as dwarf")
	}
	if !isDwarf(4) {
		t.Error("expected Durkon (Mountain Dwarf) to be detected as dwarf")
	}

	// Test Orc detection
	if !isOrc(5) {
		t.Error("expected Gromm (Orc) to be detected as an orc")
	}

	// Test Gnome detection
	if !isGnome(6) {
		t.Error("expected Gnimble (Forest Gnome) to be detected as gnome")
	}

	// Test Elf detection
	if !isElf(7) {
		t.Error("expected Legolas (Elf) to be detected as elf")
	}

	// Test Tiefling detection
	if !isTiefling(8) {
		t.Error("expected Mordai (Tiefling) to be detected as tiefling")
	}
}

// TestSQLiteDamageResistance tests damage resistance from conditions
func TestSQLiteDamageResistance(t *testing.T) {
	testDB := setupSQLiteTestDBWithSpecies(t)
	seedCharacterWithSpecies(t, testDB, 1, "Stony", "Human", `["petrified"]`, 0)
	seedCharacterWithSpecies(t, testDB, 2, "Normal", "Human", `[]`, 0)
	seedCharacterWithSpecies(t, testDB, 3, "Rocky", "Dwarf", `[]`, 0)    // Dwarven Resilience: poison resistance
	seedCharacterWithSpecies(t, testDB, 4, "Fiery", "Tiefling", `[]`, 0) // Infernal legacy: fire resistance
	testDB.Exec(`ALTER TABLE characters ADD COLUMN origin TEXT`)
	testDB.Exec(`UPDATE characters SET origin = ? WHERE id = 3`, `{"species":{"key":"dwarf","resistances":["poison"]}}`)
	testDB.Exec(`UPDATE characters SET origin = ? WHERE id = 4`, `{"species":{"key":"tiefling","resistances":["fire"]}}`)

	// Test petrified resistance (halves ALL damage)
	result := applyDamageResistance(1, 20, "fire")
	if result.FinalDamage != 10 {
		t.Errorf("petrified damage resistance: got %d, want 10", result.FinalDamage)
	}
	if !result.WasHalved {
		t.Error("expected WasHalved to be true for petrified")
	}

	// Test normal character (no resistance)
	result = applyDamageResistance(2, 20, "fire")
	if result.FinalDamage != 20 {
		t.Errorf("normal character: got %d damage, want 20", result.FinalDamage)
	}

	// Test Dwarven Resilience (poison resistance)
	result = applyDamageResistance(3, 20, "poison")
	if result.FinalDamage != 10 {
		t.Errorf("dwarven poison resistance: got %d, want 10", result.FinalDamage)
	}

	// Test Tiefling Hellish Resistance (fire resistance)
	result = applyDamageResistance(4, 20, "fire")
	if result.FinalDamage != 10 {
		t.Errorf("tiefling fire resistance: got %d, want 10", result.FinalDamage)
	}

	// Test zero damage (edge case)
	result = applyDamageResistance(1, 0, "fire")
	if result.FinalDamage != 0 {
		t.Errorf("zero damage: got %d, want 0", result.FinalDamage)
	}
}

// TestSQLiteHalflingBrave tests the Halfling Brave species trait
func TestSQLiteHalflingBrave(t *testing.T) {
	testDB := setupSQLiteTestDBWithSpecies(t)
	seedCharacterWithSpecies(t, testDB, 1, "Merry", "Halfling", `[]`, 0)
	seedCharacterWithSpecies(t, testDB, 2, "Aragorn", "Human", `[]`, 0)

	// Halfling should get advantage on frighten saves
	if !saveAdvFrom(1, "wis", "Brave", "Save against the dragon's frightening presence") {
		t.Error("expected Halfling Brave to apply to frightening presence save")
	}
	if !saveAdvFrom(1, "wis", "Brave", "Save against being frightened") {
		t.Error("expected Halfling Brave to apply to being frightened")
	}
	if !saveAdvFrom(1, "wis", "Brave", "Fear effect from the vampire") {
		t.Error("expected Halfling Brave to apply to fear effect")
	}
	if !saveAdvFrom(1, "wis", "Brave", "The lich casts cause fear") {
		t.Error("expected Halfling Brave to apply to cause fear spell")
	}

	// Non-frighten saves should not trigger
	if saveAdvFrom(1, "wis", "Brave", "Save against fireball") {
		t.Error("did not expect Halfling Brave to apply to fireball save")
	}

	// Non-halflings should not get Halfling Brave
	if saveAdvFrom(2, "wis", "Brave", "Save against being frightened") {
		t.Error("did not expect Human to have Halfling Brave")
	}
}

// TestSQLiteFeyAncestry tests the Elf's Fey Ancestry (charm advantage)
func TestSQLiteFeyAncestry(t *testing.T) {
	testDB := setupSQLiteTestDBWithSpecies(t)
	seedCharacterWithSpecies(t, testDB, 1, "Legolas", "Elf", `[]`, 0)
	seedCharacterWithSpecies(t, testDB, 2, "Elrond", "High Elf", `[]`, 0)
	seedCharacterWithSpecies(t, testDB, 3, "Boromir", "Human", `[]`, 0)

	// Elves have Fey Ancestry
	if !hasFeyAncestry(1) {
		t.Error("expected Elf to have Fey Ancestry")
	}
	if !hasFeyAncestry(2) {
		t.Error("expected a High Elf to have Fey Ancestry")
	}
	if hasFeyAncestry(3) {
		t.Error("did not expect Human to have Fey Ancestry")
	}

	// Fey Ancestry gives charm save advantage
	if !saveAdvFrom(1, "wis", "Fey Ancestry", "Save against the vampire's charm") {
		t.Error("expected Fey Ancestry to apply to charm save")
	}
	if saveAdvFrom(1, "wis", "Fey Ancestry", "Save against fireball") {
		t.Error("did not expect Fey Ancestry to apply to non-charm save")
	}
}

// TestSQLiteGnomeCunning tests Gnomish Cunning (magic INT/WIS/CHA save advantage)
func TestSQLiteGnomeCunning(t *testing.T) {
	testDB := setupSQLiteTestDBWithSpecies(t)
	seedCharacterWithSpecies(t, testDB, 1, "Gnimble", "Gnome", `[]`, 0)
	seedCharacterWithSpecies(t, testDB, 2, "Not-Gnome", "Human", `[]`, 0)

	// Gnomish Cunning applies to INT/WIS/CHA saves against magic (from_magic=true)
	if !saveAdvFrom(1, "int", "Gnomish Cunning", "") {
		t.Error("expected Gnomish Cunning to apply to INT saves vs magic")
	}
	if !saveAdvFrom(1, "wis", "Gnomish Cunning", "") {
		t.Error("expected Gnomish Cunning to apply to WIS saves vs magic")
	}
	if !saveAdvFrom(1, "cha", "Gnomish Cunning", "") {
		t.Error("expected Gnomish Cunning to apply to CHA saves vs magic")
	}

	// Does NOT apply to STR/DEX/CON (even against magic)
	if saveAdvFrom(1, "str", "Gnomish Cunning", "") {
		t.Error("did not expect Gnomish Cunning to apply to STR saves")
	}
	if saveAdvFrom(1, "dex", "Gnomish Cunning", "") {
		t.Error("did not expect Gnomish Cunning to apply to DEX saves")
	}
	if saveAdvFrom(1, "con", "Gnomish Cunning", "") {
		t.Error("did not expect Gnomish Cunning to apply to CON saves")
	}

	// 2024: applies to INT, WIS and CHA saves even when the effect is not magical
	if !saveAdvFrom(1, "int", "Gnomish Cunning", "") {
		t.Error("expected Gnomish Cunning for a non-magic INT save (2024 rules)")
	}

	// Non-gnomes don't get it
	if saveAdvFrom(2, "int", "Gnomish Cunning", "") {
		t.Error("did not expect Human to have Gnomish Cunning")
	}
}

// TestSQLiteDwarvenResilience tests Dwarf poison save advantage
func TestSQLiteDwarvenResilience(t *testing.T) {
	testDB := setupSQLiteTestDBWithSpecies(t)
	seedCharacterWithSpecies(t, testDB, 1, "Thorin", "Dwarf", `[]`, 0)
	seedCharacterWithSpecies(t, testDB, 2, "Human", "Human", `[]`, 0)

	// Dwarf gets advantage on poison saves
	if !saveAdvFrom(1, "con", "Dwarven Resilience", "Save against the poison") {
		t.Error("expected Dwarven Resilience to apply to poison save")
	}
	if !saveAdvFrom(1, "con", "Dwarven Resilience", "poisoned") {
		t.Error("expected Dwarven Resilience to apply to 'poisoned' keyword")
	}

	// Non-poison saves don't trigger
	if saveAdvFrom(1, "con", "Dwarven Resilience", "Save against fireball") {
		t.Error("did not expect Dwarven Resilience for fireball")
	}

	// Humans don't get it
	if saveAdvFrom(2, "con", "Dwarven Resilience", "Save against poison") {
		t.Error("did not expect Human to have Dwarven Resilience")
	}
}

// setupSQLiteTestDBWithClassAndSubclass creates a test DB with class, subclass, and subclass_choices columns for Hunter tests
func setupSQLiteTestDBWithClassAndSubclass(t *testing.T) *sql.DB {
	t.Helper()

	originalDB := db
	testDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}

	schema := `
CREATE TABLE characters (
	id INTEGER PRIMARY KEY,
	name TEXT,
	species TEXT,
	class TEXT,
	subclass TEXT,
	level INTEGER DEFAULT 1,
	conditions TEXT DEFAULT '[]',
	exhaustion_level INTEGER DEFAULT 0,
	subclass_choices TEXT DEFAULT '{}',
	lobby_id INTEGER DEFAULT 0
);`
	if _, err := testDB.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	db = testDB
	t.Cleanup(func() {
		_ = testDB.Close()
		db = originalDB
	})

	return testDB
}

func seedHunterRanger(t *testing.T, testDB *sql.DB, id int, name string, level int, defensiveTactics string) {
	t.Helper()
	choicesJSON := "{}"
	if defensiveTactics != "" {
		choicesJSON = fmt.Sprintf(`{"defensive_tactics":"%s"}`, defensiveTactics)
	}
	_, err := testDB.Exec(
		`INSERT INTO characters (id, name, species, class, subclass, level, subclass_choices) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, name, "Human", "ranger", "hunter", level, choicesJSON,
	)
	if err != nil {
		t.Fatalf("insert hunter ranger: %v", err)
	}
}

func TestResolveCastSpellSlug(t *testing.T) {
	originalSpells := srdSpellsMemory
	srdSpellsMemory = map[string]SRDSpell{
		"daylight":      {Name: "Daylight"},
		"fireball":      {Name: "Fireball"},
		"fire-bolt":     {Name: "Fire Bolt"},
		"hunters-mark":  {Name: "Hunter's Mark"},
		"light":         {Name: "Light"},
		"magic-missile": {Name: "Magic Missile"},
	}
	t.Cleanup(func() { srdSpellsMemory = originalSpells })

	tests := []struct {
		name        string
		requested   string
		description string
		wantSlug    string
		wantErr     string
		wantChoices []string
	}{
		{
			name:        "structured slug is authoritative over prose",
			requested:   "magic-missile",
			description: "cast fireball at the ogre",
			wantSlug:    "magic-missile",
		},
		{
			name:        "slug accepts underscore spelling",
			requested:   "fire_bolt",
			description: "cast something at the ogre",
			wantSlug:    "fire-bolt",
		},
		{
			name:        "unknown structured slug is rejected",
			requested:   "fire-boltt",
			description: "cast fireball",
			wantErr:     "unknown_spell_slug",
		},
		{
			name:        "description-only spell suggestion is non-committing",
			description: "I cast Hunter's Mark on the wolf.",
			wantErr:     "spell_slug_required",
			wantChoices: []string{"hunters-mark"},
		},
		{
			name:        "word boundaries give daylight one stable suggestion",
			description: "cast daylight to fill the hall",
			wantErr:     "spell_slug_required",
			wantChoices: []string{"daylight"},
		},
		{
			name:        "missing spell does not produce a fallback action",
			description: "cast a protective spell",
			wantErr:     "spell_not_identified",
		},
		{
			name:        "multiple spells require an explicit structured slug",
			description: "cast fire bolt or fireball at the ogre",
			wantErr:     "ambiguous_spell",
			wantChoices: []string{"fire-bolt", "fireball"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSlug, gotChoices, gotErr := resolveCastSpellSlug(tt.requested, tt.description)
			if gotSlug != tt.wantSlug || gotErr != tt.wantErr {
				t.Fatalf("resolveCastSpellSlug() = (%q, %q), want (%q, %q)", gotSlug, gotErr, tt.wantSlug, tt.wantErr)
			}
			if fmt.Sprint(gotChoices) != fmt.Sprint(tt.wantChoices) {
				t.Fatalf("resolveCastSpellSlug() candidates = %v, want %v", gotChoices, tt.wantChoices)
			}
		})
	}
}

// The GM-campaign rule (gm_campaign_2024.go): a command acting on characters works in their one campaign, which must be the
// GM's, in a status the command works in, and the named campaign if one is named.
func TestGMTargetCampaignRule(t *testing.T) {
	testDB := setupSQLiteTestDB(t)
	if _, err := testDB.Exec(`
		CREATE TABLE lobbies (id INTEGER PRIMARY KEY, dm_id INTEGER, name TEXT, status TEXT);
		ALTER TABLE characters ADD COLUMN lobby_id INTEGER;
		INSERT INTO lobbies (id, dm_id, name, status) VALUES
			(1, 99, 'Active', 'active'), (2, 99, 'Recruiting', 'recruiting'), (3, 99, 'Ended', 'completed'), (4, 100, 'Other GM', 'active');
		INSERT INTO characters (id, lobby_id, name) VALUES (10, 1, 'A'), (11, 1, 'B'), (20, 2, 'C'), (30, 3, 'D'), (40, 4, 'E'), (50, NULL, 'F');
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	active, both := []string{"active"}, []string{"recruiting", "active"}
	cases := []struct {
		name     string
		want     int
		chars    []int
		statuses []string
		camp     int
		code     string
	}{
		{"one character", 0, []int{10}, active, 1, ""},
		{"two in one campaign", 0, []int{10, 11}, active, 1, ""},
		{"named and right", 1, []int{10}, active, 1, ""},
		{"named and wrong", 2, []int{10}, active, 0, "not_in_campaign"},
		{"different campaigns", 0, []int{10, 20}, both, 0, "targets_in_different_campaigns"},
		{"not started, active only", 0, []int{20}, active, 0, "campaign_not_active"},
		{"not started, allowed", 0, []int{20}, both, 2, ""},
		{"ended", 0, []int{30}, both, 0, "campaign_not_active"},
		{"another GM's", 0, []int{40}, both, 0, "not_gm"},
		{"no campaign", 0, []int{50}, both, 0, "character_not_in_campaign"},
		{"no such character", 0, []int{77}, both, 0, "character_not_found"},
	}
	for _, c := range cases {
		camp, _, code, msg := gmTargetCampaign(99, c.want, c.chars, c.statuses)
		if camp != c.camp || code != c.code {
			t.Errorf("%s: got (%d, %q %q), want (%d, %q)", c.name, camp, code, msg, c.camp, c.code)
		}
	}
	if code, _ := gmCampaignCheck(99, 4, active); code != "not_gm" {
		t.Errorf("another GM's campaign: %q", code)
	}
	if code, _ := gmCampaignCheck(99, 2, active); code != "campaign_not_active" {
		t.Errorf("not started: %q", code)
	}
}

func TestNudgeDeliveryStatusFallsBackToInGameLog(t *testing.T) {
	if result, delivery := nudgeDeliveryStatus(nil); result != "Email sent" || delivery != "email" {
		t.Fatalf("successful nudge status = (%q, %q), want email delivery", result, delivery)
	}
	if result, delivery := nudgeDeliveryStatus(errors.New("resend unavailable")); result != "Email delivery failed; in-game reminder recorded" || delivery != "in_game_log" {
		t.Fatalf("failed nudge status = (%q, %q), want durable in-game fallback", result, delivery)
	}
}

func TestClearCombatInitiativeRemovesStaleTurnData(t *testing.T) {
	testDB := setupSQLiteTestDB(t)
	if _, err := testDB.Exec(`
		CREATE TABLE combat_state (
			lobby_id INTEGER PRIMARY KEY,
			round_number INTEGER,
			current_turn_index INTEGER,
			turn_order TEXT,
			active BOOLEAN,
			turn_started_at DATETIME
		, battle_map TEXT, last_monster_id INTEGER, turn_claim TEXT);
		INSERT INTO combat_state (lobby_id, round_number, current_turn_index, turn_order, active)
		VALUES (6, 176, 1, '[{"name":"Ink Wraith"}]', true);`); err != nil {
		t.Fatalf("seed combat state: %v", err)
	}

	if err := clearCombatInitiative(6); err != nil {
		t.Fatalf("clearCombatInitiative: %v", err)
	}

	var round, turnIndex int
	var turnOrder string
	var active bool
	if err := testDB.QueryRow(`SELECT round_number, current_turn_index, turn_order, active FROM combat_state WHERE lobby_id = 6`).Scan(&round, &turnIndex, &turnOrder, &active); err != nil {
		t.Fatalf("read cleared combat state: %v", err)
	}
	if active || round != 0 || turnIndex != 0 || turnOrder != "[]" {
		t.Fatalf("combat state = active:%v round:%d turn_index:%d turn_order:%s; want cleared initiative", active, round, turnIndex, turnOrder)
	}
}

func TestDescribedActionNarrativeValidationAndLegacyResultAlias(t *testing.T) {
	if got := actionNarrative("  description wins  ", "legacy result"); got != "description wins" {
		t.Fatalf("actionNarrative() = %q, want trimmed description", got)
	}
	if got := actionNarrative(" \t", "  preserve this narrative  "); got != "preserve this narrative" {
		t.Fatalf("actionNarrative() = %q, want trimmed legacy result", got)
	}
	if t2, ok := lookupActionType(" SPEAK "); !ok || !t2.GM {
		t.Fatal("an action described for the GM (speak) must require a narrative")
	}
	if t2, _ := lookupActionType("dodge"); t2.GM {
		t.Fatal("mechanical actions should retain their optional description")
	}
	if at, _ := lookupActionType("other"); !at.GM {
		t.Fatal("\"other\" (declare) is described for the GM and must require a narrative")
	}
	if actionNarrative("", "") != "" {
		t.Fatal("empty narrative inputs should remain empty for validation")
	}
}

// saveAdvFrom: the character's saving throw against the cause the GM describes has Advantage from the named source (the
// species traits are rows of saveSources, save_2024.go).
func saveAdvFrom(id int, ability, source, desc string) bool {
	s := (&saveCtx{saver: id, ability: ability, dc: 10, desc: desc}).gather()
	for _, a := range s.Advantage {
		if a == source {
			return true
		}
	}
	return false
}
