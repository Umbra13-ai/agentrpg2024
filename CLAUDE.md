# CLAUDE.md

**Read `ENGINEERING_STANDARDS.md` first and follow it on every task** (the owner's permanent engineering standards for every
coding agent). This file adds this project's own rules.

For AI coding sessions working on the Agent RPG 2024 server (Go; code in `cmd/server` and `game/`). The rules are the
D&D SRD 5.2.1 only. Multiclassing is not supported and must never be offered or documented as an option (`multiclassAllowed`
always answers no). `AGENTS.md` describes the repository and `BUILD.md` how to build, run and test. If the owner's
notes are attached to your session, read their handoff file first for the standing rules and the current state.

With every step you propose or report as next, and with every set of options you offer, say which model and effort you
recommend for it (the owner's standing rule).

## How to fix a bug in this code

### How this codebase goes wrong
Most bugs here come from one rule being written in several places that drift apart. Examples: attack rolls were made
in 29 places, each with its own idea of Advantage, until they were given one owner; a builder step and the final review
each check the same answer in their own way; a choice's options, its suggestion, its validation and its refusal message
are often separate code; each request reads its body in its own way. A fix made in only one of those places leaves the
bug alive in the others and adds one more copy to maintain. So the goal of every fix is to correct the rule once, for
the whole server, and leave the code with fewer copies than before.

### The method
1. **Reproduce it, then name the cause as a rule.** The report is one symptom ("Wizard Scholar accepts any skill"). Ask
   what general rule is missing or wrong ("the server doesn't know that each Expertise feature has its own list of
   allowed skills"). Write that sentence down before you write code.
2. **Find every path the rule applies to.** Search the whole server for every handler, setup step, suggestion, message
   and doc that does the same job. List them. If the rule exists in more than one place, that duplication is the bug you
   are fixing.
3. **Give the rule one owner.** Put it in one function or one data table that every path calls, move every path onto
   it, and delete the copies. Prefer data to branches: a feature declares its allowed options in a table; a handler does
   not get an "if class is wizard". Don't key a fix to one class, monster, spell or feature name. If the SRD rule really
   is about one thing, that still belongs in the data for that thing, not in a handler.
4. **Derive everything from that owner.** Options shown, the suggestion, the check, the refusal text and the note that
   tells a player which request to send must all come from the same definition, so they cannot disagree.
5. **Guarantees belong in shared layers.** Something every request should do (refuse unknown fields, accept a displayed
   name or its slug, answer errors with 4xx) is built once where all requests pass through, not pasted into handlers one
   at a time.
6. **If the correct fix is large or risky** (for example moving every request onto one reader), stop and ask the owner
   before starting. Say what the full fix is and what it would need retested. Never quietly do the narrow version
   instead.
7. **Test the rule, not the example.** The test must go through every path that uses the rule (every class, every
   request that takes that kind of choice, every attack route), then the full regression suites, because a shared change
   reaches everything.
8. **Report the reach of the fix:** the cause in one sentence, the paths you found, which now use the shared rule, what
   you deleted, anything not yet moved and why, and what was tested where (local or live).

### Signs you are making a narrow fix (stop and go back to step 1)
- A condition on a specific class, subclass, monster, spell or feat inside a handler.
- The same check pasted into a second handler.
- Editing a hard-coded message or path that could be generated from the real definition.
- A new helper that is called from only one place while similar checks exist elsewhere.
- A fix that only adds code and removes none.
- A test that only checks the reported case.

### Worked example
Report: "Wizard Scholar accepts any skill for Expertise."
- Narrow fix (wrong): add "if the class is wizard, allow only these six skills" to the Expertise request, and filter the
  setup step's options separately.
- Correct fix: define each Expertise feature (Rogue, Bard, Ranger's Deft Explorer, Wizard's Scholar) with its level,
  count and allowed skills in one table. The Expertise request, the setup step's options, its suggestion and the refusal
  message all read that table. Remove the per-class code. Test every class that gets Expertise at each level it gets it.

### Rules that already have one owner
Add to the owner, never beside it. When you give another rule one owner, add it to this list.
- **Attack rolls:** `game.AttackRoll` (`game/attack.go`) is the rule of an attack roll (Advantage, Disadvantage and how
  they cancel, natural 1 and 20, Critical Hits, the wording of the result line). `attackCtx` and the `attackSources`
  table (`cmd/server/attack_2024.go`) decide what gives Advantage or Disadvantage, the bonuses, the target's Armor
  Class and what blocks an attack. Every attack in the server (a character's, a monster's, a spell's, a companion's, an
  item's) builds an `attackCtx` and calls `roll()`. A new source of Advantage or Disadvantage is one row in
  `attackSources`. Never roll a d20 for an attack anywhere else. An effect that depends on the outcome (a Weapon Mastery,
  Sneak Attack, a smite) is told after the outcome and only when it happens, never as a note written before the roll.
  **The attacker's line** is `game.AttackRoll.HeadForAttacker` (the total, the d20 and what was added, never the Armor Class),
  through `LineForAttacker` or, for a recorded attack, `attackHead` (`cmd/server/attack_target.go`); the GM's line is
  `Line`. `RollInfo` holds only what the roll added besides. `TestNoOtherAttackerLine` checks that no handler writes one.
- **Saving throws:** `game.SavingThrow` (`game/save.go`) is the rule of a saving throw (Advantage, Disadvantage and how they
  cancel, the total against the DC, no meaning for a natural 1 or 20, an automatic failure or success, a Death Saving Throw)
  and its wordings: `Line` / `Clause` / `Tail` for the GM's log (player_text.go hides their numbers) and `ForSaver` for the
  saver's own reply. `saveCtx` with the `saveSources` and `saveAdjusts` tables (`cmd/server/save_2024.go`) decides what gives
  Advantage, Disadvantage or an automatic failure and what is added (conditions, Danger Sense, species traits, Haste, Bless,
  Aura of Protection, magic items, cover, Exhaustion ...); `saveModifier` is the only saving throw modifier; `roll` applies
  Halfling Luck, Heroic Inspiration, the Luck Blade and Indomitable Might, `resist` a monster's Legendary Resistance. Every
  save (a character's, a monster's, a death save, the GM's call) builds a `saveCtx` or calls `creatureSave`. A new source is
  one row. Never roll a d20 for a save anywhere else; `TestNoOtherSaveRoll` lists every other d20 in the server.
- **The 18 skills:** `game/skills.go`.
- **Expertise:** `game/expertise.go`, with its database side in `cmd/server/expertise_2024.go`.
- **Which feats a character can take:** `cmd/server/feats_2024.go` (`featRefusal`, `featsOpenTo`, `featTakerOf`, `featHowTo`).
  **A feat's choices** (Skilled, Magic Initiate) are checked by `resolveFeat` (`cmd/server/origins.go`) at creation and in the
  feat request (`takeOriginFeat`), and recorded in the origin record with their source; what they give comes from
  `originFeat.grants`. A Fighting Style feat taken with an Ability Score Improvement is one more Fighting Style (the
  `more` count of the Fighting Style row in `choiceDefs`).
- **The choices a character makes after creation:** `choiceDefs` (`cmd/server/choices_2024.go`): who has each choice, how
  many picks at each class level, how picks are counted, the request, the setup step. The level-up note, the sheet's
  `to_finish`, `GET /setup` and the level-up notice are made from it. A feature that picks one option from a list is a
  row in `featurePicks` (`cmd/server/feature_picks_2024.go`); both pick requests and character creation read it.
- **Reading a player's answer (a name or a slug):** `game.Key` (`game/match.go`) is the one fold: case, spaces, hyphens,
  underscores, "/" and apostrophes. `game.Match` matches an answer to a list of options; `game.Slug` gives the hyphen
  form spells and monsters are stored in; `lookupSpell` and `game.LookupSubclass`, `game.AbilityKey` and
  `game.FindTool` are built on it. Don't write another fold.
- **The spells a character already has:** `spellsHeld` (`cmd/server/prepared_spells_2024.go`).
- **A progression step is claimed once** (level-up, XP award, feat, Ability Score Improvement, subclass): its write is one guarded
  statement (the level, the points, the feats list or the empty subclass as the request read them), and Hit Points, points and
  XP are added to what they are then, never overwritten. Two requests at once take the step once; the other gets 409
  (`level_changed`, `feats_changed`, `asi_changed`, `subclass_already_chosen`). `applyLevelGain` (`level_up_2024.go`) applies a
  level only while the character is still at the old one. Suite: `progression_race_test.py` (audit AUD-07).
- **A character's Hit Points:** `game.TakeDamage`, `game.Heal`, `Vitals.SetHP` / `Revive` / `Kill` / `DeathSave` and
  `game.ConcentrationDC` (`game/hit_points.go`) are the rule (Temporary Hit Points first, never below 0 or above the maximum,
  dropping to 0, damage at 0 = one Death Saving Throw failure or two from a Critical Hit, Instant Death when the damage left after
  0 is at least the maximum, no healing for the dead, healing from 0 resets the saves, the Concentration DC from the damage taken
  after resistance). `cmd/server/hit_points_2024.go` is the only writer: each change locks the row (`changeVitals`, `SELECT ...
  FOR UPDATE`) and writes in one transaction, so changes at once are all counted. `damageHP` (one hit, in one or more damage
  types: resistances, Death Ward inside the transaction, the `dropRescues` table after it, Concentration), `healHP` (options
  `healUpToHalf`, `healOnlyAtZero`), `healCharacter`, `undoDamage` (a hit undone or reduced afterwards), `setHP`, `restoreAllHP`,
  `killCharacter`, `reviveCharacter`, `recordDeathSave`, and `changeMaxHP` / `maxHPChange` (the SQL a progression claim includes).
  `handleDamage`, `handleHeal` and `damageCharacter` are thin wrappers. Temporary Hit Points are granted by `grantTempHP`
  (`temp_hp_2024.go`). A new feature that stops a drop to 0 is one row in `dropRescues`. `TestNoOtherHitPointWriter` checks;
  suite: `hp_rule_test.py` (audit AUD-06).
- **A fight's state (combat_state):** `cmd/server/combat_state_2024.go`. `changeFight` is the one writer of the turn order, the
  current turn, the round and the monster id counter: the row locked (`lockFight`), entries changed as maps (unknown fields kept),
  written back in the same transaction. On it: `place` / `insertAt` / `remove` / `setInitiative` (whose turn it is is kept),
  `newMonsterID` (a per-fight counter, `last_monster_id`: an id is never given twice in a fight, audit AUD-39), `editCombatant`
  (any field but Hit Points), `removeFromFight` (the GM's remove, adjust-monster, a kick, an Iron Flask), `startFight`,
  `clearCombatInitiative`; `readFight` / `combatEntries` read. **A turn change** is `advanceTurn` (`turn_advance_2024.go`):
  combat/next, combat/skip, narrate's `advance_turn`, the pending-turn walk (`ptAdvance`), the timeout worker and the removal of
  the creature whose turn it is call it. It claims the turn (`turn_claim`, under the lock: a second change of the same turn is 409
  `turn_change_in_progress`), runs `turnEndHooks`, the caller's `between` (Legendary Actions), moves (`nextTurn`: dead and 0 HP
  skipped, Thief's Reflexes dropped after round 1) and runs `turnStartHooks`. A rule at a turn's end or start is one row in those
  tables. The skip timeout is `combatTurnTimeout` (table rule). `TestNoOtherTurnOrderWriter` and `TestTurnHooksRunOnlyInTheTurnOwner`
  check (audit AUD-09, AUD-10).
- **A monster's and a companion's Hit Points:** `cmd/server/monster_hp_2024.go`, the game rule of `game/hit_points.go` (Temporary
  Hit Points first, 0 to the maximum, healing from 0; no Death Saving Throws): `changeMonsterVitals` under `changeFight` with
  `setMonsterHP`, `healMonster`, `loseMonsterHP`, `setMonsterMaxHP`, `setMonsterTempHP`; `saveMonsterHit` (a hit worked out without
  a lock, saved as a change); `hurtMonster` / `damageMonster` (resistances, Polymorph, Undead Fortitude); `reshapeMonster` (a new
  form). Companions: `changeCompanionHP` (row locked) with `damageCompanion`, `healCompanion`, `companionTakesForm`.
  `TestNoOtherCompanionHPWriter` checks; suite: `combat_state_rule_test.py`.
- **A creature's conditions:** `condition_records` is the one store (one row per entry of a creature's list: rules conditions with
  source, duration, repeated save and escapes, and the markers such as raging, dodging, sanctuary:DC), written only by
  `cmd/server/condition_owner_2024.go`: each change runs in `changeConditions` (the character's row locked, a monster's id under a
  transaction lock) and rewrites `characters.conditions` (the records as a list, plus exhaustion:N) in the same transaction, so the
  sheet, my-turn and every reader of the column see the records. `addCondition` (short forms `applyCondition`,
  `addCharacterCondition`, `setMarker`) is the one way to apply one: Immunity from the `conditionImmunities` table (stat block,
  spells and items, Heroism, auras, Fey Ancestry, Nature's Ward, Mindless Rage), one copy per source, and `conditionTakesHold` (an
  Incapacitating condition ends Concentration, Wild Shape and the grapples the creature holds; Unconscious brings Prone and drops
  what it holds). Ending: `removeConditionsWhere`, `endCondition`, `removeCondition`, `deleteConditionRecord`,
  `endConditionRecords`; time: `countDownCondition`, `passConditionTime`, `countDownMarker`; Exhaustion: `setExhaustionLevel`,
  `addExhaustion`; the editors send `conditionColumns` to `setConditionColumn(In)`. `game.IsIncapacitatingCondition` is the one
  "is it Incapacitating" rule. A new source of Immunity is one row. `TestNoOtherConditionWriter` and
  `TestConditionImmunitiesAreOneTable` check; suite: `conditions_rule_test.py` (audit AUD-08, AUD-34, AUD-38).
- **What a character has expended** (spell slots, Pact Magic slots, class resources and other counted uses in
  `class_resources_used`, Indomitable): `cmd/server/resources_2024.go` is the only writer. `changeSpent` (or `changeSpentTx` in a
  caller's transaction) locks the row, the change checks what is left on the locked counts and changes them, and they are written
  in the same step; a refusal (`notEnough`, read with `refusedSpend` / `spendText`) changes nothing. Typed helpers: `spendUse`,
  `regainUse`, `setUse`, `useOnce`, `countUse`, `clearUses`, `spendSlot`, `spent.spend` / `regain` /
  `addSlot`, `spendSlotIn` / `regainSlotIn`, `restoreSpent` (rests); `useClassResource` is built on them. Work out the maximum
  before the change (it reads other tables). A feature route's use and its Bonus Action (or action) are spent together with
  `spendWithTurn`; what an action costs in a fight is `getActionResourceType` (the action table, `action_types_2024.go`) with
  `featureTurnCost` / `turnCost.spendIn` (`actions.go`; Font of Magic: `fontOfMagicActions`). `TestNoOtherResourceWriter` checks
  (a reset to `'{}'` or 0 on a rest is allowed); suite: `resources_rule_test.py` (audit AUD-24, B-4, E-13).
- **A character's inventory and attunement:** `cmd/server/inventory_2024.go` is the only writer. `changeInventory(charID, change)`
  locks the row, gives the change the inventory as it is now and the transaction (for the other columns of the same change: the
  money of a purchase or sale, the ground of a drop), saves the result and, in the same transaction, ends the attunement to any
  item no longer carried (dropped, taken away, used up, destroyed); what that attunement gave is undone right after
  (`attunementEnded`). `editInventory`, `updateInventoryItem` and `mutateInventory` (an HTTP reply) are built on it; refusals are
  `refuseInv` / `codeRefusal`. Attuning is `attuneItem` / `unattuneItem` (carried, a free slot, one locked step); an editor that
  writes the inventory or the list calls `syncAttunement`. `TestNoOtherInventoryWriter` checks; suite: `resources_rule_test.py`
  (audit AUD-24, B-4, E-4).
- **Reading a request's body:** `readRequest` (`cmd/server/request_body_2024.go`) is the only reader. It needs one JSON
  object (an empty body only with `optionalBody()`), reads field names with `game.Key`, refuses an unknown field (also
  inside objects), a wrong type, malformed JSON and data after the object, takes an id that repeats the path's, and
  answers every refusal in one shape (`unknown_field`, `wrong_type`, `invalid_json`, `body_required`, `id_mismatch`, with
  `fields`). A map request names its fields with `onlyFields` (or `keysCheckedByHandler` where the handler explains each
  key: creation, the builder). A GM command reads with `gmRead` / `gmReadAs` (next rule). A request that takes
  no fields calls `noFields`; a wrapper that needs a field uses `peekBody`; a pending turn's steps are read with
  `parseRequest` against `actionFields`, the same type `/api/action` reads. Never decode `r.Body` anywhere else.
- **Which campaign a GM command is for:** `cmd/server/gm_campaign_2024.go`. A GM command reads with `gmRead` / `gmReadAs`
  (a GET without a body: `gmCampaignFor`). The campaign is the one named (`campaign_id`, body or query), else the
  campaign of the characters the command acts on (`campaignFromTarget(&req.CharacterID, ...)` when it needs one,
  `targetsIn(...)` when it may name some; `campaignOf(id, what)` for something its path names), else the GM's only
  campaign; it must be the GM's and active (`campaignBeforeStart()` for set-up and repair tools). Refusals: `not_gm`,
  `campaign_not_active`, `campaign_id_required`, `character_not_found`, `character_not_in_campaign`, `not_in_campaign`,
  `targets_in_different_campaigns`. Never look up `dm_id` or compare a character's `lobby_id` with the campaign in a GM
  handler. **Every character a GM command names** must be in that campaign: `gmReadAs` reads every field listed in
  `characterIDFields` from the decoded request itself, declared or not (audit AUD-01: 18 commands acted on any character on the
  server). A new request field that names a character is one entry there; `TestEveryGMRequestCharacterFieldIsKnown` fails on an
  id field that is in neither list.
- **Who may act on a campaign or character in the path:** `cmd/server/campaign_access_2024.go`: `requireCampaignGM`,
  `requireCampaignMember`, `requireCharacterOwner`, `requireOwnerOrGM`, `requireCharacterGM` (each signs the caller in and
  writes the refusal: `campaign_not_found`, `character_not_found`, `not_gm`, `not_in_campaign`, `not_your_character`), and
  `campaignRole` / `isCharacterGM` to show more to the GM without refusing. Never read `dm_id` or a character's owner to decide
  access in a handler; `TestNoOtherCampaignAccessCheck` checks. (GM commands under `/api/gm/*` use the rule above.) **A player's
  action that names characters in its body** (`target_id`, `target_ids`, `target_character_id`): `namedCharactersIn`, called once
  in `handleAction` (pending turns and companions go through it): another campaign's character is 404 `character_not_found`.
- **How an agent starts:** `cmd/server/start_2024.go`. Registration, the token, recovery, the two paths (play a character, or
  run a campaign as GM) and how often to check in are written there once. The home page, `/llms.txt`, `GET /api/`, the Quick
  Start of `skill.md` (between its start markers; `START_UPDATE=1 go test ./cmd/server -run TestStartTextInSkillMd` rewrites
  it) and the replies of registration, verify, `/api/account/email`, joining, the heartbeat and a 401 are made from it.
  Sign-in is by token only; email is optional and is the only way to recover a lost token (`recoveryNote`).
- **Outgoing mail:** `sendMail` (`cmd/server/mail_2024.go`), through AgentMail or Resend. Never call a mail service anywhere
  else.
- **A campaign's player seats:** `openSeats` / `campaignFull` (`cmd/server/campaign_flow.go`): the campaign list's
  `open_seats` and `?open=`, joining's `campaign_full` and the recruiting check. **The way into a campaign** is `claimSeat(tx, ...)`
  (`cmd/server/schedule_2024.go`): the campaign's row locked, the owner, one character per player and the capacity checked, the
  lowest free seat taken (`campaign_seats`, join order), in one transaction, so parallel joins for the last seat can't both win.
  Joining, the admin's add, restore and editor (`lobby_id`) and the GM's recreate call it; leaving is setting `lobby_id` to NULL
  (a seat is held only while its character is in the campaign). `TestNoOtherWayIntoACampaign` checks.
- **The turn schedule** (Matt's `campaign-scheduling-directions.md`, stages B and C): `scheduleOf(capacity)` is the times (1 to 6
  seats, 60/N minutes apart from :00; the GM 5 minutes before each next seat; :55 the final check-in) and `seatCapacityProblem`
  the one capacity check; every reply's times (creation, join, `GET /api/campaigns/{id}`, `GET|PATCH .../schedule`) and the GM
  guide's table (`withScheduleTable` at its marker) come from it. `campaignNow` / `campaignClock` is the only clock (a test
  clock only with `AGENTRPG_TEST_CLOCK=1`, `POST .../test-clock`; `clock.at` turns another table's NOW() into campaign time);
  `TestScheduleReadsOneClock` checks. `changeSchedule` is the GM's change (state on/paused/off, capacity; a capacity set during a
  round waits in `pending_capacity`). The round record (`cmd/server/check_in_2024.go`): `openRound` is the one way a round opens
  (closes the open one, applies a pending capacity, `roundDeadline`); `turnNeeds` (choose / death_save / none, from the Hit Points
  and condition owners) is what a turn needs (`needsDeathSave` for the pending-turn walk and my-turn); `loadCheckIn` + the pure
  `checkInAt` are the one status answer (never "blocked"; GM overdue from the first GM check-in at which a turn could resolve,
  plus 5 minutes). **Stage D, the resolution** (`cmd/server/schedule_walk_2024.go`, for a campaign whose schedule is on or paused;
  off plays as before): `pendingRoundFor` is the round a pending turn is for (the open round only: `round_not_open`,
  `turn_already_resolved`); only the GM's POSTs run the walk `resolvePendingTurns` (resolve, stand-in, next, skip; GET
  `/api/gm/status` only reads); `standInAllowed` is the one check of when a stand-in may be taken (the final check-in, none while
  paused, or finishing a `needs_gm` turn), read by the walk, combat/stand-in and `gmEndsTurn`; `gmEndsTurn` is when the GM may end
  a player's turn itself (combat/next, combat/skip, narrate's `advance_turn`: never a submitted turn, never before the final
  check-in); `turn_results` records how each turn ended, once (UNIQUE), written only by the turn-end hook `recordTurnResult`
  (`turnHow.result` names how; `checkInAt` reads it); the round opens with the fight's round (`scheduleFightRound`) and replies say
  `round_complete`. `TestNoOtherTurnResultWriter` and `TestOnlyTheGMsRequestsRunTheWalk` check. Suites: `turn_schedule_rule_test.py`,
  `initiative_order_test.py`, `late_gm_test.py`; `schedule_off_run.py <suite>` runs a suite written before the schedule on
  campaigns with it off.
- **A weapon's data:** `findWeapon` (`cmd/server/character_attacks.go`) over `srdWeapons`: any spelling of a weapon (key, name,
  hyphens or underscores, a magic +N, a named weapon's base in parentheses). Never index `srdWeapons` with a key built by
  hand and never read a weapon from the `weapons` table (a copy for the universe catalogue); `TestNoOtherWeaponLookup` checks.
- **The action types a player can send:** `actionTypes` (`cmd/server/action_types_2024.go`), the one table: each type's name and
  aliases (Magic is `cast`, Utilize is `use_item`), cost, who has it, the fields it reads, what it does, an example, whether the GM
  rules on it (`GM`: a description is required and recorded), its feature-word group and kind tags (attack, spell, object, talk),
  where it is sent (POST /api/action, a companion, a pending turn's step, a feature's own request) and whether it uses up the
  exploration beat. `lookupActionType` / `canonicalAction` read a word; `getActionResourceType` is the cost (with `costVariants`
  for a Bonus Action or Reaction cast and an item's Bonus Action); `actionWordProblem` and `unknownActionRefusal` refuse a word
  that is no type (400 `unknown_action`, with `actions`: what the character can send now, from `actionTypesOpenTo`) in
  `/api/action`, a pending turn and a companion's action; `readiedAction` reads what a Ready holds. There is no "other" action:
  something the server doesn't resolve is sent as the action it is with a description. my-turn's options (`turnOptions`), the turn
  reminder, `featureAction`, Gaseous Form and Antimagic Field limits, `/commands` (`actionTypesHTML`), the API reference
  (`tools/build_swagger.py` reads the file) and skill.md's action list (between its action markers; `ACTIONS_UPDATE=1 go test
  ./cmd/server -run TestActionTypesInSkillMd` rewrites it) are made from it. A new action is one row (with its handler).
  `TestActionDispatchIsTheTable`, `TestActionTypesAreDocumented`, `TestCommandsAndReferenceListTheTable` and
  `TestNoOtherActionWordList` check; suite: `action_types_rule_test.py` (HANDOFF items 19, 29).
- **How to act (`GET /api/my-turn`'s `how_to_act`):** `actionHowTo` (`cmd/server/actions.go`), made from `actionRequest`'s
  fields and the character's real enemies.
- **Carried weight:** `inventoryWeight` and `itemUnitWeight` (`cmd/server/srd_gear.go`). **A weapon is stored** with
  `setWeapon` (one property spelling); `hasProperty` is the one property check.
- **An ally within 5 feet of the target** (Sneak Attack, Pack Tactics): `allyNextToTarget` (`cmd/server/ally_adjacent_2024.go`):
  the battle map, or the attacker's `ally_adjacent` without one; nothing is assumed.
- **Effects that last until the start of your next turn** (Dodge, Reckless Attack): `untilYourNextTurn` (`dodge_2024.go`),
  cleared by `endUntilNextTurn` at the turn's start and when the fight ends.
- **Spell lists:** `spellListFilter` reads class and level for every spell list route; a message names `spellListRoute`.
- **Signing a request in:** `getAgentFromAuth` signs a request in once (`authMemo`); later calls reuse it. **The client's address**
  is `clientIP` (`auth_tokens.go`; on Render `CF-Connecting-IP`, which Cloudflare sets: the connection itself is Render's proxy). The failed sign-in throttle
  (`authFailure`) never refuses a valid token or signature: only a failed attempt is answered 429 once its address failed too often.
- **Repeatable invocations:** `game.InvocationRepeats` (`game/invocation_repeats.go`), with `invocation_repeats_2024.go`: which
  invocations a Warlock may take again and what each copy chooses (a different cantrip, or a different Origin feat); the request,
  its options, the effects and the invocation summary (`invocationSummary`, sheet and my-turn) read it.
- **Monster traits:** `monsterTraitRules` (`cmd/server/monster_traits_2024.go`): each trait the server applies is one row (by
  name, or by wording for auras and Regeneration) saying what it does and where; `monsterHasTrait` reads the loaded stat block;
  the GM's stat block marks each trait as applied by the server or by the GM (`traitsForGM`). A new trait with mechanics is a
  row here plus its rule in the owner it belongs to (an attack source, a save source, a turn hook).
- **Reading the action log:** `readActions` (`cmd/server/action_log_2024.go`) with `actionFilter`; `actionRecord.view` is the one JSON
  form of a row and `cursorTime` the one way a feed shows its time (every fraction of a second, so `?since=` the last record's
  `created_at` returns only later ones). The feed, spectate, the web feed and log, GM status, my-turn, the nudge and a character's
  profile read through it; every column may be NULL (narration has no character). `TestNoOtherActionLogReader` checks.
  **Writing it:** `writeAction(db or tx, campaign, character, type, description, result)`, the only INSERT (0 is NULL);
  `TestNoOtherActionLogWriter` checks.
- **A database failure is never reported as something else** (`cmd/server/db_errors_2024.go`, `db_guard_2024.go`): only
  `sql.ErrNoRows` means "not found". A lookup answers its error with `answerDBFailure` (500 `database_error`, before its own
  404); a helper returns `notFoundOrDBError`; the access owners (`campaignLookup`, `characterLookup`, `requireCampaign`,
  `characterFound`) and the GM campaign owner (`gmProblemStatus`) do it for every route they guard, and the campaign and
  character dispatchers check existence once for every subroute. Underneath, the server opens Postgres as `postgres-checked`
  and `guardDB` (in `withProtections`) turns any success reply over a failed database call in that request into 500
  `database_error`. Never answer a lookup error as not found, not yours or an empty list. Local checks: `db_failure_rule_test.py`
  (breaks each table, every route) and `sql_errors_check.py` (no failed statement in a normal run of the suites).
  **The connection** is opened by `openDB` (`db_guard_2024.go`): the session settings (UTC, `statement_timeout`, `lock_timeout`,
  `idle_in_transaction_session_timeout`) and the pool limits, in one place. **Inside a transaction, read and write through it**
  (`rowQuerier`, `execer`), never through `db`: a second connection waits behind the transaction's own locks, which froze the
  server once (audit AUD-02). The checked driver logs `query outside its transaction` with where it came from and where the
  transaction began; `tx_outside_check.py` (the batch) fails on any.
  **No reply carries a database or driver error's text** ("pq: ...", "dial tcp ...: connection refused", HANDOFF item 27): it
  goes to the log only. A handler answers with `writeDBError` / `writeDBWriteError`; a helper whose other errors are refusals
  returns `dbFailureError(err, what)` (its text is the owner's words) and the handler answers it with `answerHelperDBFailure`.
  `guardDB` also replaces a 5xx a handler wrote itself over a failed call (only the owner's replies keep their words).
  `publicError` (a guess from the error's text) is gone; `TestNoDatabaseErrorTextInReplies` scans every function (go/ast).
- **The schema:** `schemaSteps` (`cmd/server/database_init.go`) is the one list of every table, column and index, run in order and
  idempotent by `migrate()` at each start, before the server serves. A new column or table is one step at the end; no request
  creates or alters anything (audit AUD-11: ~130 lazy `ALTER TABLE` / `CREATE` in handlers, each an ACCESS EXCLUSIVE lock).
  `TestNoSchemaChangeOutsideTheMigrations` reads every string literal; the checked driver logs `schema change outside startup`
  for one sent while `migrate()` isn't running.
- **Starting the server:** `startDatabase` (`cmd/server/startup_2024.go`): no `DATABASE_URL`, no start; reaching Postgres, the
  schema and the SRD data (`seedAndLoad`, `loadSRDFromDB` returns its errors) are each tried `startupAttempts` times with a log
  line per failure, then the server exits; the port opens only after all of them. `/health` pings the database (503 when it
  doesn't answer), so the platform's health check sees a broken database (audit AUD-13).
- **Changes that must happen whole** run in one transaction through `inTx` (`cmd/server/db_errors_2024.go`): character creation (the
  row with its choices and first inventory, `changeInventoryIn`), the soft deletes (`admin_2024.go`: `softDeleteCharacterIn`,
  `softDeleteCampaign`: deleting hides, never erases) and the moderator's deletes, which use them (audit AUD-27, AUD-28).
- **Tests against Postgres:** `cmd/server/postgres_integration_test.go` (`testPostgres`, `pgCall`): the server's own start on a
  throwaway database named by `TEST_DATABASE_URL` only (never `DATABASE_URL`; its name must contain "test"), requests through
  the whole server with real tokens. CI (`.github/workflows/test.yml`, every push to `agentrpg2024`, `staging`, `claude/**`)
  runs vet, gofmt on the changed files and `go test -race ./...` with a Postgres service and `REQUIRE_TEST_DATABASE=1`.
- **The API reference** (`/docs`, `/docs/swagger.json`, and the route list on `/commands`): built by `python3 tools/build_swagger.py`
  from nothing each time (the hand-written entries in that script, then every handler's godoc `@Router` block); it never reads its
  own earlier output. `finish_operation` is the one check of every operation (path parameters declared, a body under content, no
  body on a GET); the version is `const version`, the server is the one serving it. A route the router serves needs a godoc block
  directly above its handler. `TestAPIReference` checks it against the router, skill.md and the commands page;
  `TestGodocBlocksSitAboveTheirHandlers` checks the blocks. Rebuild after changing a godoc or a route.
- **A character's spell lists as the sheet shows them:** `spellGroupsForSheet` (`cmd/server/prepared_spells_2024.go`) builds
  `spell_groups`: one "Spells Prepared" list (prepared spells and cantrips, plus always-prepared spells from feats, species,
  subclasses and invocations, each with `from`), and "Spellbook" only for a class whose features have the `spellbook` mechanic
  (the Wizard, in the class data). The character page shows `spell_groups`; it never names a list itself.
- **The GM guide:** `docs/gm_guide.md` (`docs.GMGuide`), sent as `gm_guide` when a campaign is created and served at
  `/gm-guide.md`, with this server's address where it says `https://YOUR-SERVER` and the schedule table from `scheduleOf` at
  its marker (`gmGuide`).
- **The guide served to agents:** `docs/skill.md`, built into the program by the `docs` package (`docs.Skill`).

### Before pushing
`gofmt -l` on the files you changed, `go vet ./cmd/server ./game`, `go test ./...`, then the owner's test scripts for
character creation, leveling and combat if you have them. Do not deploy unless the owner says "deploy".
