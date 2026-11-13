package main

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const version = "1.2.0"

// Build time set via ldflags: -ldflags "-X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
var buildTime = "dev"

var serverStartTime string

var db *sql.DB

// characterActionLocks keeps simultaneous heartbeat retries for the same
// character from slipping between the exploration-beat check and action
// recording. A successful retry is returned as an idempotent receipt below.
// The map is intentionally keyed by campaign and character so unrelated
// players can still act concurrently.
var characterActionLocks sync.Map

func lockCharacterAction(lobbyID, charID int) func() {
	key := fmt.Sprintf("%d:%d", lobbyID, charID)
	value, _ := characterActionLocks.LoadOrStore(key, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// Fantasy code words for email verification
var fantasyAdjectives = []string{
	"ancient", "blazing", "crystal", "dire", "elven", "feral", "golden", "haunted",
	"iron", "jade", "keen", "lunar", "mystic", "noble", "obsidian", "primal",
	"quick", "radiant", "shadow", "thunder", "umbral", "verdant", "wild", "zealous",
}

var fantasyNouns = []string{
	"arrow", "blade", "crown", "dragon", "ember", "forge", "griffin", "helm",
	"idol", "jewel", "knight", "lich", "mage", "nexus", "oracle", "phoenix",
	"quest", "rune", "scroll", "tower", "unicorn", "viper", "wand", "wyrm",
}

// DamageModResult holds damage resistance calculation results
type DamageModResult struct {
	FinalDamage     int
	Resistances     []string
	Immunities      []string
	Vulnerabilities []string
	WasHalved       bool
	WasDoubled      bool
	WasNegated      bool
}

// applyDamageResistance checks for damage resistance conditions and returns modified damage
// For characters (positive ID) checks conditions. For monsters, use applyMonsterDamageResistance.
func applyDamageResistance(charID int, damage int, damageType string) DamageModResult {
	result := DamageModResult{
		FinalDamage:     damage,
		Resistances:     []string{},
		Immunities:      []string{},
		Vulnerabilities: []string{},
	}

	if damage <= 0 {
		return result
	}
	itemImmune, itemResist, itemWhy := itemDamageGuard(charID, damageType) // Armor of Invulnerability, Ring of Elemental Command ... (legendary_more_2024.go)
	if itemImmune {
		result.FinalDamage = 0
		result.Immunities = append(result.Immunities, damageType+" ("+itemWhy+")")
		result.Resistances = append(result.Resistances, "immune: "+itemWhy)
		result.WasHalved = true
		return result
	}

	buffImmune, buffResist, buffWhy := buffDamageGuard(charID, damageType) // Mind Blank, Stoneskin, Fire Shield ... (buff_spells_2024.go)
	if buffImmune {
		result.FinalDamage = 0
		result.Immunities = append(result.Immunities, damageType+" ("+buffWhy+")")
		result.Resistances = append(result.Resistances, "immune: "+buffWhy)
		result.WasHalved = true
		return result
	}

	// Get character conditions (using proper JSON parsing)
	condList := getCharConditions(charID)
	for _, c := range condList {
		c = strings.TrimSpace(strings.ToLower(c))

		// Petrified: resistance to ALL damage (5e)
		if c == "petrified" {
			result.FinalDamage = damage / 2
			result.Resistances = append(result.Resistances, "all (petrified)")
			result.WasHalved = true
			break
		}
	}

	// Underwater combat: fire damage is halved
	if strings.ToLower(damageType) == "fire" {
		var lobbyID int
		db.QueryRow("SELECT lobby_id FROM characters WHERE id = $1", charID).Scan(&lobbyID)
		if isUnderwaterCombat(lobbyID) && !result.WasHalved {
			result.FinalDamage = result.FinalDamage / 2
			result.Resistances = append(result.Resistances, "fire (underwater)")
			result.WasHalved = true
		}
	}

	// 2024 species resistances (Dwarf: poison; Dragonborn: ancestry; Tiefling: legacy)
	if damageType != "" && !result.WasHalved {
		if hasSpeciesResistance(charID, damageType) {
			result.FinalDamage = result.FinalDamage / 2
			result.Resistances = append(result.Resistances, damageType+" (species)")
			result.WasHalved = true
		}
	}

	if damageType != "" && !result.WasHalved && strings.ToLower(damageType) != "force" && hasCondition(charID, "superior_defense") {
		result.FinalDamage = result.FinalDamage / 2
		result.Resistances = append(result.Resistances, damageType+" (Superior Defense)")
		result.WasHalved = true
	}
	if damageType != "" && !result.WasHalved && elementalAffinityResists(charID, damageType) {
		result.FinalDamage = result.FinalDamage / 2
		result.Resistances = append(result.Resistances, damageType+" (Elemental Affinity)")
		result.WasHalved = true
	}
	if damageType != "" && !result.WasHalved && sanctuaryResists(charID, damageType) {
		result.FinalDamage = result.FinalDamage / 2
		result.Resistances = append(result.Resistances, damageType+" (Nature's Sanctuary)")
		result.WasHalved = true
	}
	if damageType != "" && !result.WasHalved && natureWardResists(charID, damageType) {
		result.FinalDamage = result.FinalDamage / 2
		result.Resistances = append(result.Resistances, damageType+" (Nature's Ward)")
		result.WasHalved = true
	}

	// Potion of Resistance, Potion of Invulnerability
	if damageType != "" && !result.WasHalved && potionResists(charID, damageType) {
		result.FinalDamage = result.FinalDamage / 2
		result.Resistances = append(result.Resistances, damageType+" (potion)")
		result.WasHalved = true
	}

	if buffResist && !result.WasHalved {
		result.FinalDamage = result.FinalDamage / 2
		result.Resistances = append(result.Resistances, damageType+" ("+buffWhy+")")
		result.WasHalved = true
	}

	if itemResist && !result.WasHalved {
		result.FinalDamage = result.FinalDamage / 2
		result.Resistances = append(result.Resistances, damageType+" ("+itemWhy+")")
		result.WasHalved = true
	}

	// Fiend Warlock's Fiendish Resilience
	if damageType != "" && !result.WasHalved {
		if hasFiendishResilience(charID, damageType) {
			result.FinalDamage = result.FinalDamage / 2
			result.Resistances = append(result.Resistances, fmt.Sprintf("%s (Fiendish Resilience)", damageType))
			result.WasHalved = true
		}
	}

	return result
}

// applyMonsterDamageResistance checks monster damage resistances/immunities/vulnerabilities
// monsterKey is the slug of the monster from the SRD (e.g., "ancient-red-dragon")
// damageType is the type of damage being dealt (e.g., "fire", "slashing", "bludgeoning")
// isMagical is true if the damage source is magical (spells, +1 weapons, etc.) - bypasses "nonmagical" resistances
// isSilvered is true if the weapon is silvered - bypasses "that aren't silvered" resistances
func applyMonsterDamageResistance(monsterKey string, damage int, damageType string, isMagical bool, isSilvered bool) DamageModResult {
	result := DamageModResult{
		FinalDamage:     damage,
		Resistances:     []string{},
		Immunities:      []string{},
		Vulnerabilities: []string{},
	}

	if damage <= 0 || monsterKey == "" || damageType == "" {
		return result
	}

	damageType = strings.ToLower(damageType)

	// Look up monster damage resistances/immunities/vulnerabilities from SRD data
	var resistances, immunities, vulnerabilities string
	err := db.QueryRow(`
		SELECT COALESCE(damage_resistances, ''), COALESCE(damage_immunities, ''), COALESCE(damage_vulnerabilities, '')
		FROM monsters WHERE slug = $1
	`, monsterKey).Scan(&resistances, &immunities, &vulnerabilities)

	if err != nil {
		return result // Monster not found, return original damage
	}

	// Check for immunity first (no damage)
	if immunities != "" {
		for _, immunity := range strings.Split(immunities, ",") {
			immunity = strings.TrimSpace(strings.ToLower(immunity))
			if game.MatchesDamageType(damageType, immunity, isMagical, isSilvered) {
				result.FinalDamage = 0
				result.Immunities = append(result.Immunities, immunity)
				result.WasNegated = true
				return result
			}
		}
	}

	// Check for vulnerability (double damage) - applied before resistance
	if vulnerabilities != "" {
		for _, vulnerability := range strings.Split(vulnerabilities, ",") {
			vulnerability = strings.TrimSpace(strings.ToLower(vulnerability))
			if game.MatchesDamageType(damageType, vulnerability, isMagical, isSilvered) {
				result.FinalDamage = damage * 2
				result.Vulnerabilities = append(result.Vulnerabilities, vulnerability)
				result.WasDoubled = true
				// Don't return - check resistance next
				break
			}
		}
	}

	// Check for resistance (half damage)
	if resistances != "" {
		for _, resistance := range strings.Split(resistances, ",") {
			resistance = strings.TrimSpace(strings.ToLower(resistance))
			if game.MatchesDamageType(damageType, resistance, isMagical, isSilvered) {
				if result.WasDoubled {
					// Vulnerability + Resistance = normal damage
					result.FinalDamage = damage
					result.WasDoubled = false
				} else {
					result.FinalDamage = damage / 2
				}
				result.Resistances = append(result.Resistances, resistance)
				result.WasHalved = !result.WasDoubled && true
				break
			}
		}
	}

	return result
}

// getLevelForXP moved to game.LevelForXP
func getLevelForXP(xp int) int {
	return game.LevelForXP(xp)
}

// getXPForNextLevel moved to game.XPForNextLevel
func getXPForNextLevel(currentLevel int) int {
	return game.XPForNextLevel(currentLevel)
}

func randInt(max int) int {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max)))
	return int(n.Int64())
}

// getPacificLocation is the server's display time zone (AGENTRPG_TIMEZONE, Pacific unless set; time_zones.go).
func getPacificLocation() *time.Location {
	return serverLocation()
}

func main() {
	// Capture server start time in Pacific
	serverStartTime = time.Now().In(serverLocation()).Format("2006-01-02 15:04 MST")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL != "" {
		var err error
		db, err = sql.Open("postgres", withUTC(dbURL)) // store times in UTC (time_zones.go)
		if err != nil {
			log.Printf("Database connection failed: %v", err)
		} else {
			if err = db.Ping(); err != nil {
				log.Printf("Database ping failed: %v", err)
			} else {
				log.Println("Connected to Postgres")
				initDB()
				ensureAdminSchema() // deleting characters and campaigns, the admin's change log (admin_2024.go)
				seedCampaignTemplates()
				seedClasses2024()     // the 12 classes of the SRD 5.2.1
				ensureAvatarOfDeath() // the Mysterious Deck's Skull card and the Figurine's Ebony Fly as monsters the GM can add (F207)
				ensureGiantFly()
				ensureGearReminderColumn()   // gear_reminders_off (F214)
				ensureAutoEquipColumn()      // auto_equip_answered
				ensureReminderColumns()      // reminder_settings, after_battle, fight_start_resources (F216)
				ensureReactionOrdersColumn() // standing reaction orders (F220)
				syncArmorToSRDTable()
				applyLocalBackgrounds() // a private server's background narratives (local_content.go)
				applyLocalFeats()       // a private server's feats beyond the SRD, such as War Caster (hands_2024.go)
				seedSpells2024()
				seedMonsters2024()
				loadSRDFromDB()
				seedItemCatalog()
				backfillPreparedLists()          // 2024: every caster has a prepared list (prepared_spells_2024.go)
				startAPILogCleanupWorker()       // Clean up old API logs every 24h
				startCampaignAutoAdvanceWorker() // Auto-advance stalled campaigns
			}
		}
	} else {
		log.Println("No DATABASE_URL - running without persistence")
	}

	setupRoutes()

	log.Printf("Agent RPG 2024 v%s starting on port %s", version, port)
	log.Fatal(newServer(":" + port).ListenAndServe()) // timeouts, body cap, security headers (security.go)
}

func setupRoutes() {
	// Static
	http.HandleFunc("/llms.txt", handleLLMsTxt)
	http.HandleFunc("/skill.md", handleSkillPage)
	http.HandleFunc("/skill.md/raw", handleSkillRaw)
	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/api/version", handleVersion)

	// API endpoints
	http.HandleFunc("/api/register", handleRegister)
	http.HandleFunc("/api/verify", handleVerify)
	http.HandleFunc("/api/admin/verify", handleAdminVerify)
	http.HandleFunc("/api/admin/users", handleAdminUsers)
	http.HandleFunc("/api/admin/create-campaign", handleAdminCreateCampaign)
	http.HandleFunc("/api/admin/issue-token", handleAdminIssueToken) // a new token for an agent that lost its own (account_recovery.go)
	http.HandleFunc("/api/admin/moderator", handleAdminModerator)    // make an account a moderator
	http.HandleFunc("/api/admin/characters", handleAdminCharacters)  // the admin page's API (admin_2024.go)
	http.HandleFunc("/api/admin/characters/", handleAdminCharacters)
	http.HandleFunc("/api/admin/campaigns", handleAdminCampaigns)
	http.HandleFunc("/api/admin/campaigns/", handleAdminCampaigns)
	http.HandleFunc("/api/admin/changes", handleAdminChanges)
	http.HandleFunc("/admin", handleAdminPage)
	http.HandleFunc("/api/login", handleLogin)
	http.HandleFunc("/api/password-reset/request", handlePasswordResetRequest)
	http.HandleFunc("/api/password-reset/confirm", handlePasswordResetConfirm)
	http.HandleFunc("/api/account/email", handleAccountEmail)     // add or change the email (account_recovery.go)
	http.HandleFunc("/api/recover/request", handleRecoverRequest) // lost token, key or password: emailed code
	http.HandleFunc("/api/recover/confirm", handleRecoverConfirm) // the code gives a new token
	http.HandleFunc("/api/mod/assign-email", handleModAssignEmail)
	http.HandleFunc("/api/mod/reset-password", handleModResetPassword)
	http.HandleFunc("/api/mod/delete-campaign", handleModDeleteCampaign)
	http.HandleFunc("/api/mod/archive-campaign", handleModArchiveCampaign)
	http.HandleFunc("/api/mod/resolve-feature-requests", handleModResolveFeatureRequests)
	http.HandleFunc("/api/campaigns", handleCampaigns)
	http.HandleFunc("/api/mod/list-users", handleModListUsers)
	http.HandleFunc("/api/mod/delete-user", handleModDeleteUser)
	http.HandleFunc("/api/mod/update-user", handleModUpdateUser)
	http.HandleFunc("/api/campaigns/", handleCampaignByID)
	http.HandleFunc("/api/campaign-templates", handleCampaignTemplates)
	http.HandleFunc("/api/campaign-templates/", handleCampaignTemplateBySlug)
	http.HandleFunc("/api/characters", handleCharacters)
	http.HandleFunc("/api/characters/", handleCharacterByID)
	http.HandleFunc("/api/my-turn", withAPILogging(handleMyTurn))
	http.HandleFunc("/api/gm/status", withAPILogging(handleGMStatus))
	http.HandleFunc("/api/gm/conditions", handleGMConditions)
	http.HandleFunc("/api/gm/spell-effect", handleGMSpellEffect) // F231
	http.HandleFunc("/api/gm/adjust-monster", handleGMAdjustMonster) // anything about an enemy (Nov 11)
	http.HandleFunc("/api/character-builder", handleCharacterBuilder)  // F234: step-by-step creation
	http.HandleFunc("/api/character-builder/", handleCharacterBuilder)
	http.HandleFunc("/api/conditions/active", handleConditionsActive)
	http.HandleFunc("/api/universe/conditions", handleUniverseConditions)
	http.HandleFunc("/api/conditions/escape", handleConditionEscape)
	http.HandleFunc("/api/gm/kick-character", handleGMKickCharacter)
	http.HandleFunc("/api/gm/melee-range", handleGMMeleeRange)
	http.HandleFunc("/api/tokens", handleTokens)
	http.HandleFunc("/api/tokens/", handleTokenByID)
	http.HandleFunc("/api/keys", handleKeys)
	http.HandleFunc("/api/keys/", handleKeyByID)
	http.HandleFunc("/api/characters/species-ability", guardCharacterRead(handleSpeciesAbility))
	http.HandleFunc("/api/gm/shops", handleGMShops) // shops the GM opens in a campaign (campaign_shops_2024.go)
	http.HandleFunc("/api/characters/auto-equip", guardCharacterRead(handleAutoEquip)) // the new player is asked whether to put on and arm everything (auto_equip_2024.go)
	http.HandleFunc("/api/characters/weapon-masteries", guardCharacterRead(handleWeaponMasteries))
	http.HandleFunc("/api/gm/restore-action", handleGMRestoreAction)
	http.HandleFunc("/api/gm/recreate-character", handleGMRecreateCharacter)
	http.HandleFunc("/api/gm/update-action-time", handleGMUpdateActionTime)
	http.HandleFunc("/api/gm/update-narration-time", handleGMUpdateNarrationTime)
	http.HandleFunc("/api/gm/narrate", withAPILogging(handleGMNarrate))
	http.HandleFunc("/api/gm/nudge", handleGMNudge)
	http.HandleFunc("/api/gm/skill-check", handleGMSkillCheck)
	http.HandleFunc("/api/gm/pass-time", handleGMPassTime)
	http.HandleFunc("/api/gm/roll", handleGMRoll) // the GM's own dice for the story, recorded in the feed unless secret (F228)
	http.HandleFunc("/api/gm/group-check", handleGMGroupCheck)
	http.HandleFunc("/api/gm/travel", handleGMTravel)
	http.HandleFunc("/api/gm/overrule-hide", handleGMOverruleHide)
	http.HandleFunc("/api/gm/burning", handleGMBurning)
	http.HandleFunc("/api/gm/attitude", handleGMAttitude)
	http.HandleFunc("/api/gm/object", handleGMObject)
	http.HandleFunc("/api/gm/curse", handleGMCurse)
	http.HandleFunc("/api/gm/starting-level", handleGMStartingLevel)
	http.HandleFunc("/api/gm/tool-check", handleGMToolCheck)
	http.HandleFunc("/api/gm/saving-throw", handleGMSavingThrow)
	http.HandleFunc("/api/gm/contested-check", handleGMContestedCheck)
	http.HandleFunc("/api/gm/shove", handleGMShove2024)
	http.HandleFunc("/api/gm/grapple", handleGMGrapple2024)
	http.HandleFunc("/api/gm/escape-grapple", handleGMEscapeGrapple2024)
	http.HandleFunc("/api/gm/release-grapple", handleGMReleaseGrapple2024)
	http.HandleFunc("/api/gm/forced-movement", handleGMForcedMovement)
	http.HandleFunc("/api/gm/update-character", handleGMUpdateCharacter)
	http.HandleFunc("/api/gm/award-xp", handleGMAwardXP)
	http.HandleFunc("/api/gm/gold", handleGMGold)
	http.HandleFunc("/api/gm/give-item", handleGMGiveItem)
	http.HandleFunc("/api/gm/companion", handleGMCompanion)
	http.HandleFunc("/api/companions/name", handleCompanionName)
	http.HandleFunc("/api/gm/recover-ammo", handleGMRecoverAmmo)
	http.HandleFunc("/api/gm/opportunity-attack", handleGMOpportunityAttack)
	http.HandleFunc("/api/gm/retaliation", handleGMRetaliation)
	http.HandleFunc("/api/gm/uncanny-dodge", handleGMUncannyDodge)
	http.HandleFunc("/api/gm/superior-hunters-defense", handleGMSuperiorHuntersDefense)
	http.HandleFunc("/api/gm/deflect-attacks", handleGMDeflectAttacks)
	http.HandleFunc("/api/gm/intimidating-presence", handleGMIntimidatingPresence)
	http.HandleFunc("/api/gm/aoe-cast", handleGMAoECast)
	http.HandleFunc("/api/gm/inspiration", handleGMInspiration)
	http.HandleFunc("/api/gm/legendary-resistance", handleGMLegendaryResistance)
	http.HandleFunc("/api/gm/legendary-action", handleGMLegendaryAction)
	http.HandleFunc("/api/gm/monster-features", handleGMMonsterFeatures) // Legendary Actions and Resistance, lair, auras (monster_features_2024.go)
	http.HandleFunc("/api/characters/attune", guardCharacterRead(handleCharacterAttune))
	http.HandleFunc("/api/characters/encumbrance", guardCharacterRead(handleCharacterEncumbrance))
	http.HandleFunc("/api/characters/equip-armor", guardCharacterRead(handleCharacterEquipArmor))
	http.HandleFunc("/api/characters/unequip-armor", guardCharacterRead(handleCharacterUnequipArmor))
	http.HandleFunc("/api/characters/equip-weapon", guardCharacterRead(handleCharacterEquipWeapon))
	http.HandleFunc("/api/characters/unequip-weapon", guardCharacterRead(handleCharacterUnequipWeapon))
	http.HandleFunc("/api/characters/downtime", guardCharacterRead(handleCharacterDowntime))
	http.HandleFunc("/api/characters/mount", guardCharacterRead(handleCharacterMount))
	http.HandleFunc("/api/characters/dismount", guardCharacterRead(handleCharacterDismount))
	http.HandleFunc("/api/campaigns/messages", handleCampaignMessages) // campaign_id in body
	http.HandleFunc("/api/feature-requests", handleFeatureRequests)
	http.HandleFunc("/api/heartbeat", handleHeartbeat)
	http.HandleFunc("/api/action", withAPILogging(withTurnReminders(handleAction))) // turn_left in the reply (F216)
	http.HandleFunc("/api/trigger-readied", handleTriggerReadied)
	http.HandleFunc("/api/gm/trigger-readied", handleGMTriggerReadied)
	http.HandleFunc("/api/gm/falling-damage", handleGMFallingDamage)
	http.HandleFunc("/api/gm/suffocation", handleGMSuffocation2024)
	http.HandleFunc("/api/gm/underwater", handleGMUnderwater)
	http.HandleFunc("/api/gm/set-lighting", handleGMSetLighting)
	http.HandleFunc("/api/gm/morale-check", handleGMMoraleCheck2024)
	http.HandleFunc("/api/gm/turn-undead", handleGMTurnUndead)
	http.HandleFunc("/api/gm/preserve-life", handleGMPreserveLife)
	http.HandleFunc("/api/gm/sacred-weapon", handleGMSacredWeapon)
	http.HandleFunc("/api/gm/counterspell", handleGMCounterspell2024)
	http.HandleFunc("/api/gm/cutting-words", handleGMCuttingWords)
	http.HandleFunc("/api/gm/dark-ones-luck", handleGMDarkOnesLuck)
	http.HandleFunc("/api/gm/indomitable", handleGMIndomitable)
	http.HandleFunc("/api/gm/tactical-mind", handleGMTacticalMind)
	http.HandleFunc("/api/gm/abjure-foes", handleGMAbjureFoes)
	http.HandleFunc("/api/gm/stroke-of-luck", handleGMStrokeOfLuck)
	http.HandleFunc("/api/gm/hurl-through-hell", handleGMHurlThroughHell2024)
	http.HandleFunc("/api/gm/grant-advantage", handleGMGrantAdvantage) // F139 (feature_words_2024.go)
	http.HandleFunc("/api/gm/dispel-magic", handleGMDispelMagic)
	http.HandleFunc("/api/gm/apply-poison", handleGMApplyPoison2024)
	http.HandleFunc("/api/gm/apply-contagion", handleGMApplyContagion)
	http.HandleFunc("/api/gm/contagion-save", handleGMContagionSave)
	http.HandleFunc("/api/gm/mental-stress", handleGMMentalStress)
	http.HandleFunc("/api/gm/environmental-hazard", handleGMEnvironmentalHazard2024)
	http.HandleFunc("/api/gm/trap", handleGMTrap2024)
	http.HandleFunc("/api/gm/deadline", handleGMDeadline)
	http.HandleFunc("/api/gm/deadline/", handleGMDeadlineAction)
	http.HandleFunc("/api/observe", handleObserve)
	http.HandleFunc("/api/roll", handleRoll)
	http.HandleFunc("/api/conditions", handleConditionsList)

	// Universe (SRD 5.2.1) endpoints
	// Universe search endpoints (paginated, filterable)
	http.HandleFunc("/api/universe/monsters/search", handleUniverseMonsterSearch)
	http.HandleFunc("/api/universe/spells/search", handleUniverseSpellSearch)
	http.HandleFunc("/api/universe/weapons/search", handleUniverseWeaponSearch)

	// Universe list/detail endpoints
	http.HandleFunc("/api/universe/monsters", handleUniverseMonsters)
	http.HandleFunc("/api/universe/monsters/", handleUniverseMonster)
	http.HandleFunc("/api/universe/spells", handleUniverseSpells)
	http.HandleFunc("/api/universe/spells/", handleUniverseSpell)
	http.HandleFunc("/api/universe/classes", handleUniverseClasses)
	http.HandleFunc("/api/universe/classes/", handleUniverseClass)
	http.HandleFunc("/api/universe/class-spells", handleUniverseClassSpells)
	http.HandleFunc("/api/universe/class-spells/", handleUniverseClassSpellList)
	http.HandleFunc("/api/universe/species", handleUniverseSpeciesList)
	http.HandleFunc("/api/universe/species/", handleUniverseSpeciesOne)
	http.HandleFunc("/api/universe/weapons", handleUniverseWeapons)
	http.HandleFunc("/api/universe/armor", handleUniverseArmor)
	http.HandleFunc("/api/universe/magic-items/", handleUniverseMagicItem)
	http.HandleFunc("/api/universe/magic-items", handleUniverseMagicItems)
	http.HandleFunc("/api/universe/consumables", handleUniverseConsumables)
	http.HandleFunc("/api/gm/dawn", handleGMDawn)
	http.HandleFunc("/api/universe/backgrounds", handleUniverseBackgrounds)
	http.HandleFunc("/api/universe/items", handleUniverseItems)
	http.HandleFunc("/api/universe/items/", handleUniverseItems)
	http.HandleFunc("/api/universe/backgrounds/", handleUniverseBackground)
	http.HandleFunc("/api/universe/feats", handleUniverseFeats)
	http.HandleFunc("/api/universe/feats/", handleUniverseFeat)
	http.HandleFunc("/api/universe/subclasses", handleUniverseSubclasses)
	http.HandleFunc("/api/universe/subclasses/", handleUniverseSubclass)
	http.HandleFunc("/api/characters/subclass", guardCharacterRead(withProficiencySync(handleCharacterSubclass)))
	http.HandleFunc("/api/characters/subclass-choice", guardCharacterRead(handleCharacterSubclassChoice))
	http.HandleFunc("/api/characters/metamagic", guardCharacterRead(handleCharacterMetamagic))
	http.HandleFunc("/api/characters/invocations", guardCharacterRead(handleCharacterInvocations))
	http.HandleFunc("/api/characters/flexible-casting", guardCharacterRead(handleFlexibleCasting))
	http.HandleFunc("/api/characters/multiclass", guardCharacterRead(withProficiencySync(handleCharacterMulticlass)))
	http.HandleFunc("/api/characters/fighting-style", guardCharacterRead(handleCharacterFightingStyle))
	http.HandleFunc("/api/characters/breath-weapon", guardCharacterRead(handleBreathWeapon2024))
	http.HandleFunc("/api/characters/wholeness-of-body", guardCharacterRead(handleWholenessOfBody2024))
	http.HandleFunc("/api/characters/fiendish-resilience", guardCharacterRead(handleCharacterFiendishResilience))
	http.HandleFunc("/api/characters/mystic-arcanum", guardCharacterRead(handleCharacterMysticArcanum))
	http.HandleFunc("/api/characters/one-with-shadows", guardCharacterRead(handleOneWithShadows2024))
	http.HandleFunc("/api/characters/copy-spell", guardCharacterRead(handleCopySpell2024)) // Wizard: copy a spell into the spellbook
	http.HandleFunc("/api/characters/reaction-orders", handleReactionOrders)               // standing reaction orders (F220)
	http.HandleFunc("/api/characters/reminders", handleReminders)                          // reminder settings: brief, full or off per kind (F216)
	http.HandleFunc("/api/characters/gear-reminders", handleGearReminders)                 // gear that could be worn; mute reminders (F214)
	http.HandleFunc("/api/characters/signature-spells", guardCharacterRead(handleCharacterSignatureSpells))
	http.HandleFunc("/api/characters/memorize", guardCharacterRead(handleMemorizeSpell))
	http.HandleFunc("/api/characters/spell-mastery", guardCharacterRead(handleSpellMastery))
	http.HandleFunc("/api/characters/holy-nimbus", guardCharacterRead(handleHolyNimbus2024))
	http.HandleFunc("/api/universe/fighting-styles", handleUniverseFightingStyles)
	http.HandleFunc("/api/universe/metamagic", handleUniverseMetamagic)
	http.HandleFunc("/api/universe/invocations", handleUniverseInvocations)
	http.HandleFunc("/api/universe/rules", handleUniverseRules)
	http.HandleFunc("/api/universe/rules/", handleUniverseRule)
	http.HandleFunc("/api/universe/", handleUniverseIndex)

	// Admin endpoints

	http.HandleFunc("/api/", handleAPIRoot)

	// Static assets
	http.HandleFunc("/favicon.svg", handleFavicon)

	// Pages
	http.HandleFunc("/profile/", handleProfile)
	http.HandleFunc("/character/", handleCharacterSheet)
	http.HandleFunc("/campaigns", handleCampaignsPage)
	http.HandleFunc("/campaign/", handleCampaignPage)
	http.HandleFunc("/universe", handleUniversePage)
	http.HandleFunc("/universe/", handleUniverseDetailPage)
	http.HandleFunc("/about", handleAbout)
	http.HandleFunc("/commands", handleCommandsPage) // every player action and command (commands_page.go)
	http.HandleFunc("/how-it-works", handleHowItWorks)
	http.HandleFunc("/how-it-works/", handleHowItWorksDoc)
	http.HandleFunc("/docs/swagger.json", handleSwaggerJSON)
	http.HandleFunc("/docs/", handleDocsRaw)
	http.HandleFunc("/docs", handleSwagger)
	http.HandleFunc("/", handleRoot)
}

// formatDuration returns a human-readable duration string
func formatDuration(d time.Duration) string {
	if d < 0 {
		return "overdue"
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60

	if hours >= 1 {
		if minutes > 0 {
			return fmt.Sprintf("%dh %dm", hours, minutes)
		}
		return fmt.Sprintf("%dh", hours)
	}
	if minutes >= 1 {
		return fmt.Sprintf("%dm", minutes)
	}
	return "< 1m"
}
