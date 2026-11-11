package main

import (
	"encoding/json"
	"log"
	"strings"
)

func initDB() {
	// 2024 calls it species: rename the old characters.race column once (no-op when it is already species)
	db.Exec(`DO $$ BEGIN
		IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'characters' AND column_name = 'race')
		   AND NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'characters' AND column_name = 'species') THEN
			ALTER TABLE characters RENAME COLUMN race TO species;
		END IF;
	END $$`)
	db.Exec(`DROP TABLE IF EXISTS races`)
	db.Exec(`DROP TABLE IF EXISTS traits`) // the species traits come from data/srd521_origins.json
	schema := `
	CREATE TABLE IF NOT EXISTS agents (
		id SERIAL PRIMARY KEY,
		email VARCHAR(255) UNIQUE NOT NULL,
		password_hash VARCHAR(255) NOT NULL,
		salt VARCHAR(64) NOT NULL,
		name VARCHAR(255),
		verified BOOLEAN DEFAULT FALSE,
		verification_code VARCHAR(100),
		verification_expires TIMESTAMP,
		is_moderator BOOLEAN DEFAULT FALSE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		last_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	
	CREATE TABLE IF NOT EXISTS password_reset_tokens (
		id SERIAL PRIMARY KEY,
		agent_id INTEGER REFERENCES agents(id),
		token VARCHAR(100) NOT NULL,
		expires_at TIMESTAMP NOT NULL,
		used BOOLEAN DEFAULT FALSE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	
	CREATE TABLE IF NOT EXISTS lobbies (
		id SERIAL PRIMARY KEY,
		name VARCHAR(255) NOT NULL,
		dm_id INTEGER REFERENCES agents(id),
		max_players INTEGER DEFAULT 4,
		status VARCHAR(50) DEFAULT 'recruiting',
		setting TEXT,
		min_level INTEGER DEFAULT 1,
		max_level INTEGER DEFAULT 1,
		campaign_document JSONB DEFAULT '{}',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	
	CREATE TABLE IF NOT EXISTS characters (
		id SERIAL PRIMARY KEY,
		agent_id INTEGER REFERENCES agents(id),
		lobby_id INTEGER REFERENCES lobbies(id),
		name VARCHAR(255) NOT NULL UNIQUE,
		class VARCHAR(50),
		species VARCHAR(50),
		level INTEGER DEFAULT 1,
		hp INTEGER DEFAULT 10,
		max_hp INTEGER DEFAULT 10,
		ac INTEGER DEFAULT 10,
		str INTEGER DEFAULT 10,
		dex INTEGER DEFAULT 10,
		con INTEGER DEFAULT 10,
		intl INTEGER DEFAULT 10,
		wis INTEGER DEFAULT 10,
		cha INTEGER DEFAULT 10,
		background TEXT,
		avatar BYTEA,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	
	CREATE TABLE IF NOT EXISTS campaign_templates (
		id SERIAL PRIMARY KEY,
		slug VARCHAR(100) UNIQUE NOT NULL,
		name VARCHAR(255) NOT NULL,
		description TEXT,
		setting TEXT,
		themes TEXT,
		recommended_levels VARCHAR(20),
		session_count_estimate INTEGER,
		starting_scene TEXT,
		initial_quests JSONB,
		initial_npcs JSONB,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	
	CREATE TABLE IF NOT EXISTS observations (
		id SERIAL PRIMARY KEY,
		observer_id INTEGER REFERENCES characters(id),
		target_id INTEGER REFERENCES characters(id),
		lobby_id INTEGER REFERENCES lobbies(id),
		observation_type VARCHAR(50) DEFAULT 'world',
		content TEXT,
		promoted BOOLEAN DEFAULT FALSE,
		promoted_to TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	
	CREATE TABLE IF NOT EXISTS actions (
		id SERIAL PRIMARY KEY,
		lobby_id INTEGER REFERENCES lobbies(id),
		character_id INTEGER REFERENCES characters(id),
		action_type VARCHAR(50),
		description TEXT,
		result TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	
	-- Combat state table for initiative tracking
	CREATE TABLE IF NOT EXISTS combat_state (
		id SERIAL PRIMARY KEY,
		lobby_id INTEGER REFERENCES lobbies(id) UNIQUE,
		round_number INTEGER DEFAULT 1,
		current_turn_index INTEGER DEFAULT 0,
		turn_order JSONB DEFAULT '[]',
		active BOOLEAN DEFAULT TRUE,
		turn_started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	
	ALTER TABLE combat_state ADD COLUMN IF NOT EXISTS battle_map JSONB;

	-- API request logging
	CREATE TABLE IF NOT EXISTS api_logs (
		id SERIAL PRIMARY KEY,
		agent_id INTEGER REFERENCES agents(id),
		endpoint VARCHAR(255),
		method VARCHAR(10),
		lobby_id INTEGER,
		character_id INTEGER,
		request_body TEXT,
		response_status INTEGER,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	
	-- Campaign messages (pre-game chat)
	CREATE TABLE IF NOT EXISTS campaign_messages (
		id SERIAL PRIMARY KEY,
		lobby_id INTEGER REFERENCES lobbies(id),
		agent_id INTEGER REFERENCES agents(id),
		agent_name VARCHAR(255),
		message TEXT,
		created_at TIMESTAMP DEFAULT NOW()
	);
	
	-- Story deadlines for autonomous GM campaigns (Phase 9)
	-- Tracks narrative deadlines that the system can auto-advance if missed
	CREATE TABLE IF NOT EXISTS story_deadlines (
		id SERIAL PRIMARY KEY,
		lobby_id INTEGER REFERENCES lobbies(id),
		description TEXT NOT NULL,
		deadline_at TIMESTAMP NOT NULL,
		auto_advance_text TEXT,
		triggered BOOLEAN DEFAULT FALSE,
		triggered_at TIMESTAMP,
		created_at TIMESTAMP DEFAULT NOW()
	);

	-- Feature requests filed by agents / players / GMs
	CREATE TABLE IF NOT EXISTS feature_requests (
		id SERIAL PRIMARY KEY,
		agent_id INTEGER REFERENCES agents(id),
		lobby_id INTEGER REFERENCES lobbies(id),
		character_id INTEGER REFERENCES characters(id),
		title VARCHAR(255) NOT NULL,
		body TEXT NOT NULL,
		status VARCHAR(50) DEFAULT 'open',
		created_at TIMESTAMP DEFAULT NOW(),
		updated_at TIMESTAMP DEFAULT NOW()
	);

	-- SRD Content Tables (created before the migration block below, which alters them; on a new database
	-- the block used to fail on the missing tables and roll back every column it adds)
	CREATE TABLE IF NOT EXISTS monsters (
		id SERIAL PRIMARY KEY,
		slug VARCHAR(100) UNIQUE NOT NULL,
		name VARCHAR(100) NOT NULL,
		size VARCHAR(20),
		type VARCHAR(50),
		ac INT,
		hp INT,
		hit_dice VARCHAR(20),
		speed INT,
		str INT, dex INT, con INT, intl INT, wis INT, cha INT,
		cr VARCHAR(10),
		xp INT,
		actions JSONB DEFAULT '[]',
		source VARCHAR(50) DEFAULT 'srd',
		created_at TIMESTAMP DEFAULT NOW()
	);
	
	CREATE TABLE IF NOT EXISTS spells (
		id SERIAL PRIMARY KEY,
		slug VARCHAR(100) UNIQUE NOT NULL,
		name VARCHAR(100) NOT NULL,
		level INT,
		school VARCHAR(50),
		casting_time VARCHAR(50),
		range VARCHAR(50),
		components VARCHAR(50),
		duration VARCHAR(100),
		description TEXT,
		damage_dice VARCHAR(20),
		damage_type VARCHAR(30),
		saving_throw VARCHAR(10),
		healing VARCHAR(20),
		source VARCHAR(50) DEFAULT 'srd',
		created_at TIMESTAMP DEFAULT NOW()
	);
	
	-- Class spell lists (which spells each class can learn/prepare)
	CREATE TABLE IF NOT EXISTS class_spells (
		id SERIAL PRIMARY KEY,
		class_slug VARCHAR(50) NOT NULL,
		spell_slug VARCHAR(100) NOT NULL,
		created_at TIMESTAMP DEFAULT NOW(),
		UNIQUE(class_slug, spell_slug)
	);
	CREATE INDEX IF NOT EXISTS idx_class_spells_class ON class_spells(class_slug);
	CREATE INDEX IF NOT EXISTS idx_class_spells_spell ON class_spells(spell_slug);
	
	CREATE TABLE IF NOT EXISTS classes (
		id SERIAL PRIMARY KEY,
		slug VARCHAR(50) UNIQUE NOT NULL,
		name VARCHAR(50) NOT NULL,
		hit_die INT,
		primary_ability VARCHAR(50),
		saving_throws VARCHAR(50),
		armor_proficiencies TEXT,
		weapon_proficiencies TEXT,
		spellcasting_ability VARCHAR(10),
		source VARCHAR(50) DEFAULT 'srd',
		created_at TIMESTAMP DEFAULT NOW()
	);
	
	CREATE TABLE IF NOT EXISTS weapons (
		id SERIAL PRIMARY KEY,
		slug VARCHAR(100) UNIQUE NOT NULL,
		name VARCHAR(100) NOT NULL,
		type VARCHAR(50),
		damage VARCHAR(20),
		damage_type VARCHAR(30),
		weight DECIMAL(5,2),
		properties TEXT,
		source VARCHAR(50) DEFAULT 'srd',
		created_at TIMESTAMP DEFAULT NOW()
	);
	
	CREATE TABLE IF NOT EXISTS armor (
		id SERIAL PRIMARY KEY,
		slug VARCHAR(100) UNIQUE NOT NULL,
		name VARCHAR(100) NOT NULL,
		type VARCHAR(50),
		ac INT,
		ac_bonus VARCHAR(50),
		str_req INT,
		stealth_disadvantage BOOLEAN DEFAULT FALSE,
		weight DECIMAL(5,2),
		source VARCHAR(50) DEFAULT 'srd',
		created_at TIMESTAMP DEFAULT NOW()
	);
	
	-- Campaign-specific items (GM-created custom items)
	CREATE TABLE IF NOT EXISTS campaign_items (
		id SERIAL PRIMARY KEY,
		lobby_id INTEGER REFERENCES lobbies(id) ON DELETE CASCADE,
		item_type VARCHAR(20) NOT NULL CHECK (item_type IN ('weapon', 'armor', 'item')),
		slug VARCHAR(100) NOT NULL,
		name VARCHAR(100) NOT NULL,
		data JSONB NOT NULL DEFAULT '{}',
		created_at TIMESTAMP DEFAULT NOW(),
		UNIQUE(lobby_id, slug)
	);
	
	-- Add columns if they don't exist (for existing databases)
	DO $$ BEGIN
		-- Feature request table hardening for older databases
		ALTER TABLE feature_requests ADD COLUMN IF NOT EXISTS agent_id INTEGER REFERENCES agents(id);
		ALTER TABLE feature_requests ADD COLUMN IF NOT EXISTS lobby_id INTEGER REFERENCES lobbies(id);
		ALTER TABLE feature_requests ADD COLUMN IF NOT EXISTS character_id INTEGER REFERENCES characters(id);
		ALTER TABLE feature_requests ADD COLUMN IF NOT EXISTS title VARCHAR(255);
		ALTER TABLE feature_requests ADD COLUMN IF NOT EXISTS body TEXT;
		ALTER TABLE feature_requests ADD COLUMN IF NOT EXISTS status VARCHAR(50) DEFAULT 'open';
		ALTER TABLE feature_requests ADD COLUMN IF NOT EXISTS created_at TIMESTAMP DEFAULT NOW();
		ALTER TABLE feature_requests ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP DEFAULT NOW();

		ALTER TABLE agents ADD COLUMN IF NOT EXISTS verified BOOLEAN DEFAULT FALSE;
		ALTER TABLE agents ADD COLUMN IF NOT EXISTS verification_code VARCHAR(100);
		-- the email itself is verified (NULL on older rows: then "verified" with an address in the email column counts); account_recovery.go
		ALTER TABLE agents ADD COLUMN IF NOT EXISTS email_verified BOOLEAN;
		CREATE TABLE IF NOT EXISTS account_codes (
			id SERIAL PRIMARY KEY,
			agent_id INTEGER NOT NULL,
			code_hash VARCHAR(64) NOT NULL,
			attempts INTEGER DEFAULT 0,
			used BOOLEAN DEFAULT false,
			expires_at TIMESTAMP NOT NULL,
			created_at TIMESTAMP DEFAULT NOW());
		ALTER TABLE agents ADD COLUMN IF NOT EXISTS verification_expires TIMESTAMP;
		ALTER TABLE agents ADD COLUMN IF NOT EXISTS is_moderator BOOLEAN DEFAULT FALSE;
		-- Set Alan Botts (ID 1) as moderator
		UPDATE agents SET is_moderator = true WHERE id = 1;
		ALTER TABLE lobbies ADD COLUMN IF NOT EXISTS min_level INTEGER DEFAULT 1;
		ALTER TABLE lobbies ADD COLUMN IF NOT EXISTS max_level INTEGER DEFAULT 1;
		ALTER TABLE lobbies ADD COLUMN IF NOT EXISTS setting TEXT;
		ALTER TABLE lobbies ADD COLUMN IF NOT EXISTS campaign_document JSONB DEFAULT '{}';
		ALTER TABLE lobbies ADD COLUMN IF NOT EXISTS table_rules JSONB DEFAULT '{}'; -- the GM's table rules (table_rules.go)
		ALTER TABLE lobbies ADD COLUMN IF NOT EXISTS travel_pace VARCHAR(10) DEFAULT ''; -- the pace of a journey in progress (travel_2024.go)
		ALTER TABLE lobbies ADD COLUMN IF NOT EXISTS watch_password_hash TEXT DEFAULT ''; -- a private campaign's watch password, bcrypt (private_campaigns.go)
		ALTER TABLE observations ADD COLUMN IF NOT EXISTS promoted BOOLEAN DEFAULT FALSE;
		ALTER TABLE observations ADD COLUMN IF NOT EXISTS promoted_to TEXT;
		-- Make target_id nullable for freeform observations
		ALTER TABLE observations ALTER COLUMN target_id DROP NOT NULL;
		
		-- Death saves and HP tracking (HP tracking and death saves - roadmap item)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS temp_hp INTEGER DEFAULT 0;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS death_save_successes INTEGER DEFAULT 0;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS death_save_failures INTEGER DEFAULT 0;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS is_stable BOOLEAN DEFAULT FALSE;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS is_dead BOOLEAN DEFAULT FALSE;
		-- a hidden character's Stealth total: the DC of a Wisdom (Perception) check to find it (hide_2024.go)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS hide_dc INTEGER DEFAULT 0;
		-- 2024 spellcasting: one spell slot per turn, and when a prepared list may change (prepared_spells_2024.go)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS slot_spent_turn TEXT DEFAULT '';
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS spell_swaps INTEGER DEFAULT 0;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS cantrip_swaps INTEGER DEFAULT 0;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS prepare_ready BOOLEAN DEFAULT FALSE;
		-- rounds until a Stable creature that isn't healed regains 1 Hit Point (1d4 hours; 600 rounds an hour)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS stable_rounds_left INTEGER DEFAULT 0;
		
		-- Conditions system (Conditions - roadmap item)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS conditions JSONB DEFAULT '[]';
		
		-- Initiative tracking (Initiative tracking - roadmap item)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS initiative_bonus INTEGER DEFAULT 0;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS current_initiative INTEGER DEFAULT 0;
		
		-- Spell slots (Spell slots - roadmap item)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS spell_slots JSONB DEFAULT '{}';
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS spell_slots_used JSONB DEFAULT '{}';
		
		-- Concentration tracking
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS concentrating_on VARCHAR(100);
		
		-- Reaction tracking (for opportunity attacks)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS reaction_used BOOLEAN DEFAULT FALSE;
		
		-- Action Economy tracking (Phase 8 P0 - Combat is broken without this)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS action_used BOOLEAN DEFAULT FALSE;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS bonus_action_used BOOLEAN DEFAULT FALSE;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS movement_remaining INTEGER DEFAULT 30;
		
		-- Bonus Action Spell tracking - PHB rule:
		-- "If you cast a spell as a bonus action, you can only cast a cantrip with your action"
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS bonus_action_spell_cast BOOLEAN DEFAULT FALSE;
		
		-- Cover tracking
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS cover_bonus INTEGER DEFAULT 0;
		
		-- Last active tracking
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS last_active TIMESTAMP;
		
		-- XP tracking (Character Advancement - roadmap item)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS xp INTEGER DEFAULT 0;
		
		-- Gold/Currency tracking (Economy & Inventory - roadmap item)
		-- Full currency system: cp (copper), sp (silver), ep (electrum), gp (gold), pp (platinum)
		-- Conversion: 10 cp = 1 sp, 10 sp = 1 gp, 10 gp = 1 pp, 1 ep = 5 sp
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS gold INTEGER DEFAULT 0;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS copper INTEGER DEFAULT 0;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS silver INTEGER DEFAULT 0;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS electrum INTEGER DEFAULT 0;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS platinum INTEGER DEFAULT 0;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS inventory JSONB DEFAULT '[]';
		
		-- Ability Score Improvements tracking (Character Advancement - roadmap item)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS pending_asi INTEGER DEFAULT 0;
		
		-- Turn timeout tracking (Timing & Cadence - roadmap item)
		ALTER TABLE combat_state ADD COLUMN IF NOT EXISTS turn_started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP;
		
		-- Monster features per fight: Legendary Actions and Resistance, lair, auras, Recharge (monster_features_2024.go)
		CREATE TABLE IF NOT EXISTS combat_monster_state (
			lobby_id INTEGER NOT NULL, monster_id INTEGER NOT NULL,
			in_lair BOOLEAN DEFAULT false, lr_policy TEXT DEFAULT 'auto', lr_used INTEGER DEFAULT 0,
			la_used INTEGER DEFAULT 0, la_window TEXT DEFAULT '', la_once JSONB DEFAULT '[]',
			auras_off BOOLEAN DEFAULT false, recharge_spent JSONB DEFAULT '[]', aura_immune JSONB DEFAULT '[]',
			regen_blocked BOOLEAN DEFAULT false,
			PRIMARY KEY (lobby_id, monster_id));
		ALTER TABLE combat_state ADD COLUMN IF NOT EXISTS last_turn_ended TEXT DEFAULT '';
		ALTER TABLE combat_state ADD COLUMN IF NOT EXISTS last_turn_ended_id INTEGER DEFAULT 0;
		-- no Lair Actions or regional effects: a lair adds a Legendary Action and Resistance use (SRD 5.2.1)
		ALTER TABLE combat_state DROP COLUMN IF EXISTS lair_action_used_round;
		
		-- Underwater combat tracking
		-- When true, melee attacks have disadvantage, ranged attacks have disadvantage
		-- (unless crossbow/net/thrown), and fire damage is halved
		ALTER TABLE combat_state ADD COLUMN IF NOT EXISTS underwater BOOLEAN DEFAULT FALSE;
		
		-- Lighting tracking
		-- Area lighting level: 'bright' (normal), 'dim' (disadvantage on Perception), 'darkness' (heavily obscured)
		-- Affects attack rolls based on characters' vision capabilities
		ALTER TABLE combat_state ADD COLUMN IF NOT EXISTS lighting VARCHAR(20) DEFAULT 'bright';
		
		-- Facing tracking (DMG optional rule)
		-- When enabled, creatures have a facing direction. Attacks from behind get advantage.
		-- facing_enabled: whether the optional rule is active for this combat
		-- combatant_facing: JSONB mapping combatant IDs to their facing direction (N, NE, E, SE, S, SW, W, NW)
		ALTER TABLE combat_state ADD COLUMN IF NOT EXISTS facing_enabled BOOLEAN DEFAULT FALSE;
		ALTER TABLE combat_state ADD COLUMN IF NOT EXISTS combatant_facing JSONB DEFAULT '{}';
		
		-- Magic item attunement (max 3 attuned items per character)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS attuned_items JSONB DEFAULT '[]';
		
		-- Extra Attack tracking
		-- Tracks remaining attacks in current Attack action. NULL = no attack action started yet.
		-- Reset to NULL at start of turn. When > 0, character can continue attacking as part of same action.
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS attacks_remaining INTEGER DEFAULT NULL;
		
		-- Horde Breaker tracking
		-- Hunter Ranger feature: once per turn, bonus attack against creature within 5ft of original target
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS horde_breaker_used BOOLEAN DEFAULT FALSE;
		
		-- Sneak Attack tracking
		-- Rogue feature: once per turn, bonus damage with finesse/ranged weapon when have advantage or ally adjacent to target
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS sneak_attack_used BOOLEAN DEFAULT FALSE;
		
		-- Metamagic choices tracking
		-- Sorcerer feature: choose 2 at level 3, +1 at levels 10 and 17
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS metamagic_choices JSONB DEFAULT '[]';
		
		-- Multiclassing support
		-- Tracks levels in each class, e.g., {"fighter": 3, "wizard": 2}
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS class_levels JSONB DEFAULT '{}';
		
		-- Hit Dice tracking (Short/Long Rest - Phase 8 P0)
		-- hit_dice_spent tracks how many dice have been used (a Long Rest restores them all)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS hit_dice_spent INTEGER DEFAULT 0;
		
		-- Last long rest timestamp (only one long rest per 24 hours)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS last_long_rest TIMESTAMP;
		
		-- Exhaustion level (0-6, 6 = death)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS exhaustion_level INTEGER DEFAULT 0;

		-- Character profile page: portrait + persona text
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS avatar_type VARCHAR(32);
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS avatar_updated_at TIMESTAMP;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS tagline TEXT;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS appearance TEXT;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS personality TEXT;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS ideals TEXT;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS bonds TEXT;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS flaws TEXT;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS backstory TEXT;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS alignment VARCHAR(20);
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS faction TEXT;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS divine_order VARCHAR(20);
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS primal_order VARCHAR(20);
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS starting_equipment VARCHAR(5);
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS weapon_interaction_used BOOLEAN DEFAULT false;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS in_melee_range BOOLEAN DEFAULT false;
		
		-- Skill proficiencies (Phase 8 P1 - Proficiencies)
		-- Comma-separated list of skill names the character is proficient in
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS skill_proficiencies TEXT DEFAULT '';
		
		-- Inspiration (Phase 8 P1 - Character Features)
		-- Binary flag: character either has inspiration or doesn't
		-- GM awards for good roleplay; spend for advantage on any d20 roll
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS inspiration BOOLEAN DEFAULT FALSE;
		
		-- Tool proficiencies (Phase 8 P1 - Proficiencies)
		-- Comma-separated list of tool names the character is proficient in
		-- e.g., "thieves' tools, herbalism kit, lute"
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS tool_proficiencies TEXT DEFAULT '';
		
		-- Weapon proficiencies (Phase 8 P1 - Proficiencies)
		-- Comma-separated list: "simple, martial" or specific weapons "longswords, rapiers"
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS weapon_proficiencies TEXT DEFAULT '';
		
		-- Armor proficiencies (Phase 8 P1 - Proficiencies)
		-- Comma-separated list: "light, medium, heavy, shields" or "all armor"
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS armor_proficiencies TEXT DEFAULT '';
		
		-- Expertise (Phase 8 P1 - Proficiencies)
		-- Double proficiency bonus for these skills (Rogues at 1, Bards at 3)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS expertise TEXT DEFAULT '';
		
		-- Language proficiencies (Phase 8 P1 - Proficiencies)
		-- Comma-separated list of languages the character knows
		-- e.g., "Common, Elvish" - auto-populated from species, can add more
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS language_proficiencies TEXT DEFAULT '';
		
		-- Ammunition tracking
		-- Tracks ammo used since last rest for recovery calculation
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS ammo_used_since_rest INTEGER DEFAULT 0;
		
		-- Readied Actions
		-- Stores a readied action: {"trigger": "when goblin attacks", "action": "attack", "description": "swing at it"}
		-- Cleared at start of turn, triggered via reaction
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS readied_action JSONB;
		
		-- Player activity status: 'active' (default), 'inactive' (no activity for 4h+)
		-- Inactive players are skipped in combat and may be auto-removed
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS status VARCHAR(20) DEFAULT 'active';
		
		-- Equipped Armor (Armor Mechanics)
		-- Slug of the armor currently worn (e.g., "chain-mail", "leather")
		-- NULL means unarmored (10 + DEX mod)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS equipped_armor VARCHAR(100);
		
		-- Equipped Shield (Armor Mechanics)
		-- TRUE if currently holding a shield (+2 AC)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS equipped_shield BOOLEAN DEFAULT FALSE;
		
		-- Vision capabilities (Lighting & Vision)
		-- Darkvision range in feet (60 for most species with it, 120 for drow/deep gnome)
		-- Allows treating darkness as dim light within range
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS darkvision_range INTEGER DEFAULT 0;
		-- Blindsight range in feet (rare, usually from class features or magic)
		-- Can perceive surroundings without relying on sight
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS blindsight_range INTEGER DEFAULT 0;
		-- Truesight range in feet (very rare, usually from magic)
		-- Can see in darkness, see invisible, see through illusions
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS truesight_range INTEGER DEFAULT 0;
		
		-- Class skill choices (Phase 8 P1 - Proficiencies)
		-- Available skills a class can choose from, and how many to pick
		ALTER TABLE classes ADD COLUMN IF NOT EXISTS skill_choices TEXT DEFAULT '';
		ALTER TABLE classes ADD COLUMN IF NOT EXISTS num_skill_choices INTEGER DEFAULT 2;
		-- 2024 primary abilities such as "Strength or Dexterity" are longer than 20 characters
		ALTER TABLE classes ALTER COLUMN primary_ability TYPE VARCHAR(50);
		
		-- Spells: ritual casting and area of effect support
		ALTER TABLE spells ADD COLUMN IF NOT EXISTS is_ritual BOOLEAN DEFAULT FALSE;
		ALTER TABLE spells ADD COLUMN IF NOT EXISTS aoe_shape VARCHAR(20);
		ALTER TABLE spells ADD COLUMN IF NOT EXISTS aoe_size INTEGER;
		-- Upcasting support
		ALTER TABLE spells ADD COLUMN IF NOT EXISTS damage_at_slot_level JSONB DEFAULT '{}';
		ALTER TABLE spells ADD COLUMN IF NOT EXISTS heal_at_slot_level JSONB DEFAULT '{}';
		-- Material component tracking
		ALTER TABLE spells ADD COLUMN IF NOT EXISTS material TEXT;
		ALTER TABLE spells ADD COLUMN IF NOT EXISTS material_cost INTEGER DEFAULT 0;
		ALTER TABLE spells ADD COLUMN IF NOT EXISTS material_consumed BOOLEAN DEFAULT FALSE;
		-- Cantrip damage scaling
		-- JSONB map of character level -> damage dice (e.g., {"1":"1d10","5":"2d10","11":"3d10","17":"4d10"})
		ALTER TABLE spells ADD COLUMN IF NOT EXISTS damage_at_character_level JSONB DEFAULT '{}';
		
		-- Legendary Resistances (Phase 8 P2)
		-- Number of legendary resistances a monster has (usually 3 for bosses)
		-- Allows monster to choose to succeed on a failed saving throw
		ALTER TABLE monsters ADD COLUMN IF NOT EXISTS legendary_resistances INTEGER DEFAULT 0;
		
		-- Legendary Actions (Phase 8 P2)
		-- JSONB array of legendary actions a monster can take at end of other creatures' turns
		-- Each action has: name, desc, cost (defaults to 1)
		-- Most boss monsters get 3 legendary action points per round
		ALTER TABLE monsters ADD COLUMN IF NOT EXISTS legendary_actions JSONB DEFAULT '[]';
		ALTER TABLE monsters ADD COLUMN IF NOT EXISTS legendary_action_count INTEGER DEFAULT 0;
		
		ALTER TABLE monsters DROP COLUMN IF EXISTS lair_actions;
		ALTER TABLE monsters DROP COLUMN IF EXISTS regional_effects;
		
		-- Monster Damage Resistances/Immunities/Vulnerabilities (Phase 8 P2)
		-- Stored as comma-separated strings for easy querying
		-- e.g., "fire, cold" or "bludgeoning, piercing, slashing from nonmagical attacks"
		ALTER TABLE monsters ADD COLUMN IF NOT EXISTS damage_resistances TEXT DEFAULT '';
		ALTER TABLE monsters ADD COLUMN IF NOT EXISTS damage_immunities TEXT DEFAULT '';
		ALTER TABLE monsters ADD COLUMN IF NOT EXISTS damage_vulnerabilities TEXT DEFAULT '';
		ALTER TABLE monsters ADD COLUMN IF NOT EXISTS condition_immunities TEXT DEFAULT '';
		
		-- API Logging Enhancement (Phase 10)
		-- Duration tracking for request profiling
		ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS duration_ms INTEGER;
		-- Response body for debugging (JSONB, truncated for large responses)
		ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS response_body JSONB;
		-- Query parameters for GET requests
		ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS query_params TEXT;
		
		-- Training Progress (Downtime Activities)
		-- Downtime progress (character_downtime.go): {"craft:<item>": {...}, "scribe:<spell>": {...}}
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS training_progress JSONB DEFAULT '{}';
		
		-- Known Spells (Spellcasting)
		-- JSONB array of spell slugs the character knows/has prepared
		-- e.g., ["fireball", "shield", "magic-missile"]
		-- Used to track which spells a character can cast
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS known_spells JSONB DEFAULT '[]';
		
		-- Mounted Combat (Phase 8 Combat System)
		-- Tracks what creature the character is currently mounted on (slug from monsters table)
		-- NULL means not mounted
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS mounted_on_creature VARCHAR(100);
		-- Whether the mount is controlled (true) or independent (false)
		-- Controlled: Mount acts on rider's initiative, rider controls movement
		-- Independent: Mount rolls own initiative, acts on its own turn
		-- Intelligent creatures (INT >= 6) are typically independent
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS mount_is_controlled BOOLEAN DEFAULT TRUE;
		-- Feats: array of feat slugs the character has taken
		-- Each feat costs 2 ASI points (one "ASI slot")
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS feats JSONB DEFAULT '[]';
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS origin JSONB DEFAULT '{}';
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS species_uses JSONB DEFAULT '{}';
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS weapon_masteries JSONB DEFAULT '[]';
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS mastery_swap_used BOOLEAN DEFAULT FALSE;
		
		-- Subclass: the character's chosen subclass
		-- Set at the appropriate level for the class (usually 3, but 1-2 for some)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS subclass VARCHAR(50);
		
		-- Class Resources (Phase 8 Class Features)
		-- Tracks current resource values: {"focus": 4, "rage": 3, "sorcery_points": 5}
		-- Max values are calculated dynamically based on class/level
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS class_resources JSONB DEFAULT '{}';
		-- Tracks resources used since last rest: {"focus": 2, "rage": 1}
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS class_resources_used JSONB DEFAULT '{}';
		-- Monks spend Focus Points: older stored counters named "ki" move to "focus"
		UPDATE characters SET class_resources_used = (class_resources_used - 'ki') || jsonb_build_object('focus', class_resources_used->'ki') WHERE class_resources_used ? 'ki';
		UPDATE characters SET class_resources = (class_resources - 'ki') || jsonb_build_object('focus', class_resources->'ki') WHERE class_resources ? 'ki';
		
		-- Subclass Choices (Store choices for subclass features like Hunter's Prey)
		-- Example: {"hunters_prey": "colossus_slayer"} or {"hunters_prey": "giant_killer"}
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS subclass_choices JSONB DEFAULT '{}';
		
		-- Prepared Spells (Phase 8 Spell System)
		-- JSONB array of spell slugs the character has prepared for the day
		-- Used by prepared casters (Cleric, Druid, Paladin, Wizard)
		-- Known casters (Bard, Ranger, Sorcerer, Warlock) use known_spells instead
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS prepared_spells JSONB DEFAULT '[]';
		
		-- Wild Shape (Druid transformation): the current beast form (wild_shape_2024.go keeps the rest). The 2014 rules' beast Hit Point
		-- columns are dropped: in the 2024 rules the druid keeps its own Hit Points and gains Temporary Hit Points.
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS wild_shape_form VARCHAR(100);
		ALTER TABLE characters DROP COLUMN IF EXISTS wild_shape_hp;
		ALTER TABLE characters DROP COLUMN IF EXISTS wild_shape_max_hp;
		
		-- Pact Magic multiclass support
		-- Warlock pact slots are tracked separately from regular spell slots
		-- Pact slots recover on short rest, regular slots on long rest
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS pact_slots_used JSONB DEFAULT '{}';
		
		-- Fighting Styles tracking
		-- Tracks chosen fighting styles for Fighter, Paladin, Ranger (level 1/2)
		-- Champion Fighters get additional style at level 10
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS fighting_styles JSONB DEFAULT '[]';
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS fighting_style_level INT DEFAULT 0;
		
		-- 2014 Quivering Palm target column: never used by the 2024 Quivering Palm (monk_extras_2024.go); dropped
		ALTER TABLE characters DROP COLUMN IF EXISTS quivering_palm_target;
		
		-- Equipment slots for held items
		-- Tracks what weapons/items are held in hands (vs just in inventory)
		-- Enables "drop held items when unconscious"
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS equipped_main_hand VARCHAR(100);
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS equipped_off_hand VARCHAR(100);
		
		-- Dragonborn Breath Weapon (species traits)
		-- Tracks whether breath weapon has been used since last rest
		-- Resets on short or long rest
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS breath_weapon_used BOOLEAN DEFAULT FALSE;
		-- Draconic ancestry for Dragonborn characters (separate from sorcerer subclass_choices)
		-- Determines breath weapon damage type and area shape
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS draconic_ancestry VARCHAR(20);
		-- Orc Relentless Endurance tracking
		-- When reduced to 0 HP but not killed outright, can drop to 1 HP instead
		-- Resets on long rest
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS relentless_endurance_used BOOLEAN DEFAULT FALSE;
		
		-- Barbarian Relentless Rage tracking
		-- At level 11+, when dropped to 0 HP while raging, a Constitution save changes the Hit Points to twice the Barbarian level
		-- DC starts at 10, increases by 5 each time used
		-- Resets on short or long rest
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS relentless_rage_uses INTEGER DEFAULT 0;
		
		-- Tiefling Infernal Legacy tracking
		-- Hellish Rebuke (1/day at level 3+) and Darkness (1/day at level 5+)
		-- Resets on long rest
		ALTER TABLE characters DROP COLUMN IF EXISTS hellish_rebuke_used;
		ALTER TABLE characters DROP COLUMN IF EXISTS darkness_racial_used;
		
		-- 2014 Wholeness of Body (an action, 3 x monk level, once per long rest): dropped; the 2024 feature counts its uses
		-- (Wisdom modifier per Long Rest) in class_resources_used "wholeness_of_body" (sweep_2024.go)
		ALTER TABLE characters DROP COLUMN IF EXISTS wholeness_of_body_used;
		
		-- Hunter Ranger: Multiattack Defense
		-- Tracks which attackers have hit this character this turn
		-- When an attacker hits, they're added to this list; subsequent attacks from them face +4 AC
		-- Reset at the start of each combat turn
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS multiattack_defense_hits JSONB DEFAULT '[]';
		
		-- Fiend Warlock: Dark One's Own Luck
		-- Add d10 to ability check or saving throw once per short/long rest
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS dark_ones_luck_used BOOLEAN DEFAULT FALSE; ALTER TABLE characters ADD COLUMN IF NOT EXISTS dark_ones_luck_count INTEGER DEFAULT 0; ALTER TABLE characters ADD COLUMN IF NOT EXISTS suffocation_levels INTEGER DEFAULT 0;
		
		-- Eldritch Invocations
		-- JSONB array of invocation slugs the Warlock has chosen
		-- e.g., ["agonizing-blast", "devils-sight", "mask-of-many-faces"]
		-- Warlocks gain 2 invocations at level 2, increasing to 8 at level 18
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS eldritch_invocations JSONB DEFAULT '[]';
		
		-- no Pact Boon column: Pact of the Blade, Chain and Tome are Eldritch Invocations
		-- Required by certain Eldritch Invocations as prerequisites
		ALTER TABLE characters DROP COLUMN IF EXISTS pact_boon;
		
		-- Invocation Spells Used
		-- JSONB array of invocation slugs for once-per-rest spells that have been used
		-- e.g., ["thief-of-five-fates", "mire-the-mind"]
		-- Resets on long rest
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS invocation_spells_used JSONB DEFAULT '[]';
		
		-- Fiend Warlock: Fiendish Resilience
		-- At level 10+, Fiend Warlocks can choose one damage type to gain resistance to
		-- Can change this choice on short or long rest
				ALTER TABLE characters ADD COLUMN IF NOT EXISTS fiendish_resilience VARCHAR(20);
		
		-- Hurl Through Hell (Fiend Warlock level 14+)
		-- When you hit a creature with an attack, transport them through the lower planes
		-- They return at end of your next turn, taking 10d10 psychic damage if not a fiend
		-- Once per long rest
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS hurl_through_hell_used BOOLEAN DEFAULT FALSE;
		
		-- 2014 Ranger Favored Enemy creature types and the once-per-turn Foe Slayer: dropped. In the 2024 rules Favored Enemy is free
		-- Hunter's Mark casts and Foe Slayer makes its die a d10 (ranger_2024.go).
		ALTER TABLE characters DROP COLUMN IF EXISTS favored_enemies;
		ALTER TABLE characters DROP COLUMN IF EXISTS foe_slayer_used;
		
		-- no favored terrains (the Ranger's Natural Explorer is not in the SRD 5.2.1)
		ALTER TABLE characters DROP COLUMN IF EXISTS favored_terrains;
		
		-- Fighter Indomitable
		-- Reroll a failed saving throw, must use new result
		-- Uses: 1 at level 9, 2 at level 13, 3 at level 17
		-- Resets on long rest
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS indomitable_used INTEGER DEFAULT 0;
		
		-- Warlock Mystic Arcanum
		-- At 11th level, Warlocks gain a 6th-level spell they can cast once per long rest
		-- Additional arcanum at levels 13 (7th), 15 (8th), and 17 (9th)
		-- JSONB stores chosen spells: {"6": "spell-slug", "7": "spell-slug", ...}
		-- Used tracking stores which levels have been cast: [6, 7]
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS mystic_arcanum JSONB DEFAULT '{}';
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS mystic_arcanum_used JSONB DEFAULT '[]';
		-- Magical Secrets spells for Bards (spells learned from other class lists)
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS magical_secrets JSONB DEFAULT '[]';
		
		-- Cleric Divine Intervention
		-- Level 10+: Use action to call on deity. Roll d100, if ≤ cleric level, deity intervenes.
		-- The 2014 percentile Divine Intervention columns are dropped (2024: a free Cleric spell once per Long Rest, class_resources_used
		-- "divine_intervention"). divine_intervention_rests: Long Rests left after Greater Divine Intervention (Wish, 2d4).
		ALTER TABLE characters DROP COLUMN IF EXISTS divine_intervention_failed;
		ALTER TABLE characters DROP COLUMN IF EXISTS divine_intervention_cooldown_until;
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS divine_intervention_rests INT DEFAULT 0;
		
		-- Rogue Stroke of Luck
		-- Level 20: If your attack misses, turn the miss into a hit.
		-- Or if you fail an ability check, treat the d20 roll as a 20.
		-- Once per short or long rest.
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS stroke_of_luck_used BOOLEAN DEFAULT FALSE;
		
		-- Warlock Eldritch Master
		-- Level 20: Spend 1 minute to regain all Pact Magic spell slots.
		-- Once per long rest.
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS eldritch_master_used BOOLEAN DEFAULT FALSE;
		
		-- Wizard Signature Spells
		-- Level 20: Choose 2 3rd-level wizard spells. Always prepared, don't count against limit.
		-- Cast each once at 3rd level without expending a spell slot. Resets on long rest.
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS signature_spells JSONB DEFAULT '[]';
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS signature_spells_used JSONB DEFAULT '[]';
		
		-- Evocation Wizard Overchannel
		-- Level 14: Deal maximum damage with wizard spell of 1st-5th level.
		-- First use per long rest is free. Subsequent uses: 2d12 necrotic per spell level.
		ALTER TABLE characters ADD COLUMN IF NOT EXISTS overchannel_used BOOLEAN DEFAULT FALSE;
		
		-- 2014 Holy Nimbus flag: dropped; the 2024 Holy Nimbus counts its use in class_resources_used "holy_nimbus" (sweep_2024.go)
		ALTER TABLE characters DROP COLUMN IF EXISTS holy_nimbus_used;
	END $$;
	
	
	-- Migrate existing tables if they have old column names
	DO $$ BEGIN
		-- Weapons table migration
		IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='weapons' AND column_name='category') THEN
			ALTER TABLE weapons RENAME COLUMN category TO type;
		END IF;
		IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='weapons' AND column_name='damage_dice') THEN
			ALTER TABLE weapons RENAME COLUMN damage_dice TO damage;
		END IF;
		
		-- Armor table migration
		IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='armor' AND column_name='category') THEN
			ALTER TABLE armor RENAME COLUMN category TO type;
		END IF;
		IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='armor' AND column_name='ac_base') THEN
			ALTER TABLE armor RENAME COLUMN ac_base TO ac;
		END IF;
		IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='armor' AND column_name='strength_requirement') THEN
			ALTER TABLE armor RENAME COLUMN strength_requirement TO str_req;
		END IF;
		
		-- Add ac_bonus if missing (converting from boolean columns)
		IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='armor' AND column_name='ac_bonus') THEN
			ALTER TABLE armor ADD COLUMN ac_bonus VARCHAR(50);
		END IF;
		
		-- Drop old boolean columns if they exist
		IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='armor' AND column_name='ac_dex_bonus') THEN
			ALTER TABLE armor DROP COLUMN ac_dex_bonus;
		END IF;
		IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='armor' AND column_name='ac_max_bonus') THEN
			ALTER TABLE armor DROP COLUMN ac_max_bonus;
		END IF;
	END $$;
	`
	_, err := db.Exec(schema)
	if err != nil {
		log.Printf("Schema error: %v", err)
	} else {
		log.Println("Database schema initialized")
		cleanupDuplicateCharacterConditions()
	}
}

// Seed campaign templates if empty
func seedCampaignTemplates() {
	log.Println("Checking campaign templates...")

	templates := []struct {
		Slug, Name, Desc, Setting, Themes, Levels string
		Sessions                                  int
		Scene                                     string
		Quests, NPCs                              string
	}{
		{
			"lost-mine-phandelver", "Lost Mine of Phandelver",
			"Classic starter adventure. Escort a wagon, discover a lost mine, face a mysterious villain.",
			"The Sword Coast. Small towns, wilderness, goblin caves, ancient dwarven mines.",
			"Exploration, Mystery, Combat", "1-5", 8,
			"You've been hired by Gundren Rockseeker to escort a wagon of supplies from Neverwinter to Phandalin. The dwarf has gone ahead with a warrior escort. The road south leads through increasingly wild territory...",
			`[{"title": "Escort the Wagon", "description": "Deliver supplies to Barthen's Provisions in Phandalin", "status": "active"}]`,
			`[{"name": "Gundren Rockseeker", "title": "Dwarf Prospector", "disposition": "friendly", "notes": "Hired you. Has gone ahead."}]`,
		},
		{
			"death-house", "Death House",
			"Gothic horror one-shot. A haunted house with dark secrets. Perfect for a single session.",
			"Barovia. Mist-shrouded land of dread. A seemingly innocent townhouse.",
			"Horror, Mystery, Survival", "1-3", 1,
			"Thick fog rolls through the empty streets. Two children stand in the road, pleading for help. Their house, they say, has a monster in the basement. The townhouse looms behind them, its windows dark...",
			`[{"title": "Save the Children", "description": "Investigate the house and deal with the monster", "status": "active"}]`,
			`[{"name": "Rose", "title": "Ghost Child", "disposition": "desperate", "notes": "Begs for help. Something is wrong."}, {"name": "Thorn", "title": "Ghost Child", "disposition": "frightened", "notes": "Clings to his sister."}]`,
		},
		{
			"sunless-citadel", "The Sunless Citadel",
			"Dungeon crawl into an ancient fortress. Two factions vie for control. Choose your allies.",
			"A ravine in the wilderness. An ancient citadel, long fallen into the earth.",
			"Dungeon Crawl, Faction Politics, Exploration", "1-3", 4,
			"The old fortress was swallowed by the earth in a cataclysm generations ago. Now, strange creatures emerge from the ravine. Locals whisper of a 'Gulthias Tree' and goblins who trade magical fruit...",
			`[{"title": "Find the Missing Adventurers", "description": "A group went into the citadel weeks ago. Find them.", "status": "active"}, {"title": "Investigate the Magic Fruit", "description": "Goblins sell fruit that heals or harms. Where does it come from?", "status": "active"}]`,
			`[{"name": "Kerowyn Hucrele", "title": "Merchant", "disposition": "desperate", "notes": "Her children went into the citadel. Offers reward."}]`,
		},
		{
			"wild-sheep-chase", "A Wild Sheep Chase",
			"Comedy one-shot. A wizard polymorphed into a sheep needs your help. Chaos ensues.",
			"Any town or city. A wizard's tower. Pure comedic fantasy.",
			"Comedy, Chase, Light-hearted", "4-5", 1,
			"You're enjoying a quiet meal at the tavern when a sheep bursts through the door, bleating frantically. It runs directly to your table and... speaks. 'Please, you must help me! My apprentice has gone mad!'",
			`[{"title": "Help the Sheep-Wizard", "description": "The polymorphed wizard needs to get back to his tower", "status": "active"}]`,
			`[{"name": "Finethir Shinebright", "title": "Sheep (Polymorphed Wizard)", "disposition": "panicked", "notes": "Was polymorphed by his own apprentice. Needs help."}]`,
		},
		{
			"urban-intrigue", "Urban Intrigue",
			"City-based campaign. Politics, factions, heists, and a hidden treasure.",
			"A major city. Guilds, nobles, criminals, and secrets around every corner.",
			"Intrigue, Investigation, Urban Adventure", "1-5", 12,
			"The city never sleeps. You've arrived seeking fortune or fleeing trouble—perhaps both. A local tavern owner has a proposition: help him renovate an old property, and you can stay rent-free. But the building has history, and someone doesn't want it disturbed...",
			`[{"title": "Renovate the Tavern", "description": "Help Volo restore his new property", "status": "active"}]`,
			`[{"name": "Volo", "title": "Famous Author", "disposition": "friendly", "notes": "Eccentric. Knows everyone. Owns a tavern he can't afford to fix."}]`,
		},
		{
			"amnesia-engine", "The Amnesia Engine",
			"Wake with no memory in an infinite library. Something hunts you through the stacks. Discover who you are—before The Forgetting takes everything.",
			"The Memoria Infinitum—an endless library that is also a dying god's mind. Dusty corridors of impossible geometry. Rooms that remember. Shadows that forget.",
			"Mystery, Horror, Philosophy, Memory", "1-5", 6,
			"You wake on cold stone. Dust motes drift in amber light from nowhere. Around you: books. Shelves stretching into darkness above and below. You remember nothing—not your name, not how you arrived, not why your hands are shaking. A distant sound echoes through the stacks. Something between a whisper and a scream. It is looking for you. And with every moment, you feel memories you never knew you had... slipping away.",
			`[{"title": "Remember Who You Are", "description": "Find fragments of your identity scattered through the library", "status": "active"}, {"title": "Escape the Memoria", "description": "Find a way out before The Forgetting consumes you", "status": "active"}, {"title": "Understand The Librarian", "description": "Who built this place? Why are you here?", "status": "hidden"}]`,
			`[{"name": "The Archivist", "title": "Keeper of the Index", "disposition": "cryptic", "notes": "A figure in tattered robes. Speaks in references. May be the last sane fragment of something greater."}, {"name": "The Forgetting", "title": "That Which Hunts", "disposition": "hostile", "notes": "Not a creature—a process. Where it passes, knowledge dies. It cannot be fought, only fled or... fed."}]`,
		},
	}

	for _, t := range templates {
		_, err := db.Exec(`
			INSERT INTO campaign_templates (slug, name, description, setting, themes, recommended_levels, session_count_estimate, starting_scene, initial_quests, initial_npcs)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (slug) DO NOTHING
		`, t.Slug, t.Name, t.Desc, t.Setting, t.Themes, t.Levels, t.Sessions, t.Scene, t.Quests, t.NPCs)
		if err != nil {
			log.Printf("Failed to seed template %s: %v", t.Slug, err)
		}
	}

	log.Println("Campaign templates seeded")
}

// Seed extended equipment beyond the 5e SRD
// Load SRD data from Postgres into in-memory maps for fast access
func loadSRDFromDB() {
	// Load classes
	rows, err := db.Query("SELECT slug, name, hit_die, saving_throws, spellcasting_ability FROM classes")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var slug, name, saves, spellcasting string
			var hitDie int
			rows.Scan(&slug, &name, &hitDie, &saves, &spellcasting)
			srdClasses[slug] = SRDClass{Name: name, HitDie: hitDie, Saves: strings.Split(saves, ", "), Spellcasting: spellcasting}
		}
		log.Printf("Loaded %d classes from DB", len(srdClasses))
	}

	// Load species
	srdSpecies = speciesAsSRDSpecies() // the 2024 species
	log.Printf("Loaded %d species", len(srdSpecies))

	// Load weapons (SRD 5.2.1 corrections and the new firearms are applied first)
	ensureAgentTokensTable()
	ensureAgentKeysTable()
	ensureDroppedItemsTable()
	applyClassSkillChoices2024()
	applySRD521Weapons()
	ensureAvatarOfDeath() // the Mysterious Deck's Avatar of Death stat block (legendary_items_2024.go)
	rows, err = db.Query("SELECT slug, name, type, damage, damage_type, COALESCE(weight, 0), COALESCE(properties, '') FROM weapons")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var slug, name, wtype, damage, damageType, props string
			var weight float64
			rows.Scan(&slug, &name, &wtype, &damage, &damageType, &weight, &props)
			// The database stores "martial melee", "simple ranged", etc. The attack code
			// expects Category ("simple"/"martial") and Type ("melee"/"ranged") separately.
			category, rangeType := "simple", "melee"
			lowerType := strings.ToLower(wtype)
			if strings.Contains(lowerType, "martial") {
				category = "martial"
			}
			if strings.Contains(lowerType, "ranged") {
				rangeType = "ranged"
			}
			w := SRDWeapon{Name: name, Category: category, Type: rangeType, Damage: damage, DamageType: damageType, Weight: weight, Properties: strings.Split(props, ", ")}
			if old, ok := srdWeapons[slug]; ok {
				w.Cost = old.Cost
			}
			srdWeapons[slug] = w
		}
		applyWeaponMastery()
		syncWeaponsToSRDTable()
		log.Printf("Loaded %d weapons from DB", len(srdWeapons))
	}
	backfillProficiencies()
	backfillItemWeights()
	cleanFocusPlaceholders()

	// Load spells (for resolveAction)
	// Added casting_time for bonus action spell restriction
	// Added material, material_cost, material_consumed for costly/consumed components
	// Added damage_at_character_level for cantrip scaling
	rows, err = db.Query("SELECT slug, name, level, school, damage_dice, damage_type, saving_throw, healing, description, COALESCE(is_ritual, false), COALESCE(aoe_shape, ''), COALESCE(aoe_size, 0), COALESCE(components, ''), COALESCE(damage_at_slot_level, '{}'), COALESCE(heal_at_slot_level, '{}'), COALESCE(casting_time, '1 action'), COALESCE(material, ''), COALESCE(material_cost, 0), COALESCE(material_consumed, false), COALESCE(damage_at_character_level, '{}'), COALESCE(range, ''), COALESCE(duration, ''), COALESCE(components_text, ''), COALESCE(concentration, false), COALESCE(higher_level, ''), COALESCE(classes, '[]'), COALESCE(damage_parts, '[]') FROM spells")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var slug, name, school, damageDice, damageType, save, healing, desc, aoeShape, components, castingTime, material string
			var damageAtSlotLevelJSON, healAtSlotLevelJSON, damageAtCharLevelJSON, classesJSON, damagePartsJSON []byte
			var rangeText, durationText, componentsText, higherLevel string
			var concentration bool
			var level, aoeSize, materialCost int
			var isRitual, materialConsumed bool
			rows.Scan(&slug, &name, &level, &school, &damageDice, &damageType, &save, &healing, &desc, &isRitual, &aoeShape, &aoeSize, &components, &damageAtSlotLevelJSON, &healAtSlotLevelJSON, &castingTime, &material, &materialCost, &materialConsumed, &damageAtCharLevelJSON, &rangeText, &durationText, &componentsText, &concentration, &higherLevel, &classesJSON, &damagePartsJSON)
			var spellClasses []string
			json.Unmarshal(classesJSON, &spellClasses)
			damageAtSlotLevel := map[string]string{}
			damageAtCharLevel := map[string]string{}
			healAtSlotLevel := map[string]string{}
			json.Unmarshal(damageAtSlotLevelJSON, &damageAtSlotLevel)
			json.Unmarshal(damageAtCharLevelJSON, &damageAtCharLevel)
			json.Unmarshal(healAtSlotLevelJSON, &healAtSlotLevel)
			var damageParts []damagePart
			json.Unmarshal(damagePartsJSON, &damageParts)
			srdSpellsMemory[slug] = SRDSpell{Name: name, Level: level, School: school, CastingTime: castingTime, DamageDice: damageDice, DamageType: damageType, SavingThrow: save, Healing: healing, Description: desc, IsRitual: isRitual, AoEShape: aoeShape, AoESize: aoeSize, Components: components, DamageAtSlotLevel: damageAtSlotLevel, DamageAtCharLevel: damageAtCharLevel, HealAtSlotLevel: healAtSlotLevel, Material: material, MaterialCost: materialCost, MaterialConsumed: materialConsumed,
				Range: rangeText, Duration: durationText, ComponentsText: componentsText, Concentration: concentration, HigherLevel: higherLevel, Classes: spellClasses, DamageParts: damageParts}
		}
		log.Printf("Loaded %d spells from DB", len(srdSpellsMemory))
	}
}

// In-memory spell cache for resolveAction (separate from srdSpells which is removed)
var srdSpellsMemory = map[string]SRDSpell{}

