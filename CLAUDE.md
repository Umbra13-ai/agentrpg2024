# CLAUDE.md

**Read `ENGINEERING_STANDARDS.md` first and follow it on every task** (the owner's permanent engineering standards for every
coding agent). This file adds this project's own rules.

For AI coding sessions working on the Agent RPG 2024 server (Go; code in `cmd/server` and `game/`). The built-in rules and public catalog use the D&D SRD 5.2.1. **Monster rules target the 330 SRD 5.2.1 stat blocks
(`cmd/server/data/srd521_monsters.json`) only** (owner, Nov 21): every monster text pattern must match an SRD stat block or an
invented test monster (`TestMonsterPatternsServeSRDWording`); never add a rule, comment, test or doc built from Monster Manual text
outside the SRD (the earlier 2024 Monster Manual work is archived outside the repository). GMs may import their own structured
monsters into the separate shared compendium in the existing database (a generic feature); importing text does not implement
unfamiliar rules, and wording no rule reads is flagged to the GM. **Structured mechanics a GM attaches to an entry are generic and allowed** (owner, Nov 23:
`mechanicCatalog`, `monster_mechanics_2024.go`), on an imported monster or on a copy of an SRD monster as a house rule; SRD-only stays
the rule for text patterns. Multiclassing is not supported and must never be offered or documented as an option (`multiclassAllowed`
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
- **Legendary Resistance counts:** `monster_resistance_2024.go` owns locked counts for automatic saves, GM commands, item effects and Concentration decisions. General monster-state snapshots never overwrite those counts.
- **Tracked monster Concentration:** `monster_concentration_2024.go` owns cast identity, damage-time `game.SaveResult` queues, decision identity and transactional decisions/cleanup. `resolveMonsterSpell` starts the cast before effects. Cleanup calls `changeConditionsIn` in the same transaction; never write condition rows directly or reroll stored outcomes. The pending-dependent progression boundary is still incomplete; see `docs/MONSTER_REPAIR_LOG.md`.
- **A monster's saving-throw effect on its targets:** parseSaveEffect and applySaveEffect in
  cmd/server/monster_effects_2024.go; a monster as the target goes through
  applySaveEffectOnMonster (cmd/server/monster_save_targets.go), which uses saveCtx, hurtMonster
  and addCondition. No other code rolls a stat block's "Saving Throw: DC N" effect.
  What a single target must be is read from the entry's header by `monsterSaveTargetIssue` (`singleSaveTarget`: range, sight, "one
  Frightened creature", "one living creature ... that has 0 Hit Points"), refused before anything is rolled or spent. A failure's Hit
  Point threshold ("If the target has 20 Hit Points or fewer, it drops to 0 Hit Points. Otherwise, ...": `dropsToZero`, asked by every
  target route before the damage) and "The target dies, and the wisp regains ..." (`killOnFailure`) are the same owner's.
  Go: `TestPostgresSaveTargetGatesThresholdAndDeath`.
  A save that waits for a rest ("Whenever the Poisoned target finishes a Long Rest, it is subjected to the following effect": Otyugh
  Bite, `restSaveRe`) is never rolled with the hit that sets it up: the hit is read without it (`parseHitRider`), the effect is the GM's
  text. A GM-chosen damage type ("damage of the type chosen for the Draconic Origin trait": Half-Dragon) is `withChosenDamageType`, read
  for an attack (`prepareMonsterAttack`) and a save entry (`resolveMonsterEffectText`) alike; refused before anything is rolled or spent
  until `use` names a type. Go: `TestPostgresChosenDamageTypeOnSave`, `TestPostgresRestSaveIsNotRolledWithTheHit`.
  An effect resolver that stops because the GM must name something (a target, a spell or effect the entry offers, a damage type) says so
  with `blockedBeforeRoll` (`monster_day_uses.go`): nothing rolled or spent, answered as a refusal (narrate: 409, nothing posted; staged test 2.10).
  An action that offers several named effects (splitSaveOptions) is refused until the GM names one in `use`; a size-gated
  condition (saveEffect.conditionsFor) and a staged "Second Failure" go through the same owner, never a per-monster branch.
  The conditions a stat-block hit gives (parseHitRider, giveConditions) are read and applied by that same owner.
  A condition's time is read once (`readRider`): "for N minutes/hours" (`conditionDuration`; never an immunity's "for 24 hours"),
  ", until it takes damage," (Incubus, Nalfeshnee: a condition ended by `wakeOnDamageSleep`); "(demons succeed automatically)" (Vrock) is
  `AutoSucceedTypes`, read by `rollForTarget` for every target kind against the type and its tags (`creatureTypeWords`). **An aura at the
  end of the monster's turn** (`runMonsterEndAuras`): damage (`endAuraRe`) or a saving throw (`saveEndAuraRe`, `endSaveAura`, a success's
  immunity honored); "of its choice" is its enemies (`chosenAuraTargets`). Go: `TestPostgresTimedAndLinkedConditions`,
  `TestPostgresEndOfTurnAuras`.
- **A monster's form** (`cmd/server/monster_shape_shift_2024.go`): `shapeShiftForms` reads a Shape-Shift entry (its forms and true form);
  the entry with `use` naming a form records it (`resolveShapeShift`, combat effect `shape_form` = "form|its Speed line", kept across its
  turns; `shapeShiftUseRefusal` refuses another word before anything runs); the form's own Speed (`formSpeeds`, read back by
  `formSpeedText` for `monsterWalkSpeed`, the Speed list and the movement owner) is part of the same record; an entry named
  "(Wolf or Hybrid Form Only)" is refused in another form by `formRefusal`, read by `monsterEntryRefusal` and the Multiattack plan.
  Go: `TestPostgresShapeShiftForms`.
  **A creature's trait that gives attacks against it Disadvantage** (Blurred Form) is `attackedWithDisadvantage`
  (`monster_condition_traits_2024.go`), one row of `attackSources`. **A light a monster switches on and off** (Ignited Illumination) is
  `resolveLightToggle` (combat effect `ablaze`), read by `monsterLightRadii` in the lighting owner.
  Go: `TestPostgresBlurredFormAndIgnitedLight`.
- **A monster's per-day uses** (`monster_day_uses.go`): reserved in one locked step (`reserveDayUse`), kept when the request ran the entry, the server's effect or a hand-off to the GM
  (`execRecord.usedSince`; owner, Nov 23, I-WL-2), given back only for a refusal, a `blockedBeforeRoll` stop or a reaction with no trigger. Go: `TestPostgresMonsterDayUses`.
- **The GM's pacing check** (`gm_pacing_2024.go`, owner, Nov 23): `pacingCheck`, GET /api/gm/status `pacing_check` for a campaign in play and the GM guide's pacing block
  (`TestPacingCheckInGMGuide`, `START_UPDATE=1`). A question, never a gate: it does not force a fight, a player's choice or progress.
- **The GM's open items** (`cmd/server/gm_open_items_2024.go`, `gm_handover_2024.go`; owner, Nov 22): a lasting mechanic the server hands to the GM is listed in
  every status the GM reads until the GM resolves it or the server sees it end; a one-shot (resolved in the same breath) is never listed.
  `handOverToGM` is the one writer of a new item (idempotent on creature, entry and targets), `resolveGMItems`, `resolveGMItemsEnding` (the end
  hooks: `endMonsterConcentration`, `removeFromFight`, `clearCombatInitiative`) and `snoozeGMItem` the others; `gmOpenItems` is the one reader, a pure
  read that asks the owners the game asks (`gmItemLive`: Concentration, the creature in the fight, the round), so GET /api/gm/status changes no
  row. A lasting spell is a row of `gmLastingSpells` (`monsterHandoverSpell`), a stat-block remainder is read by `entryLastingRe`
  (`monsterHandoverEntry`, `saveEffect.handOverOnFailure`); a site that says "the GM resolves it" either calls them or carries `// gm-one-shot: why`
  (`TestEveryGMHandoverIsKindedOrOneShot`). Snooze hides an item until the next round or N check-ins; routes GET, POST and DELETE
  `/api/gm/open-items`; status `open_items` + one task line, the heartbeat's GM block the count. A hit's unapplied remainder is `gmNotApplied` (the bracket and, when its wording names a duration, the item: monster_attacks.go and the
  Multiattack resume handlers); the resume handlers' bracketed notes reach the GM as notices at `writeAction`'s boundary. An entry whose own words say it lasts (a duration, "until it dies", "under the wraith's control": Animate Trees, Create Specter, a curse's "or until the incubus dies") is listed by `entryLastingRe`; Ink Cloud and an attach are the server's (an obscured area, an attach record), not items. A trait the server doesn't apply (no `monsterTraitRules` row, or a GM row) whose words say it acts every turn or on a trigger (`recurringTraitRe`: Barbed Hide, Berserk, Fire Form, Multiple Heads, Loathsome Limbs, Vampire Weakness, Freeze, Mucus Cloud, Reflective Carapace, Reactive; `recurringGMTraits`) is listed when its monster gets its combat id (`listRecurringTraits`: combat/add, an item's summons, a split; `TestEveryNewMonsterListsItsTraits`) and ends with the creature. Go: `TestPostgresGMOpenItems`, `TestPostgresGMOpenItemsFromStatBlockEntries`, `TestRecurringGMTraitsOverTheCatalog`.
- **A creature's Speed** (`cmd/server/speed_owner_2024.go`): `characterSpeed` asks the one owner for conditions (Speed 0), spells (Slow, Haste),
  weapon-mastery effects and stat-block Speed riders (`speed_halved`, `speed_zero`, `speed_minus` markers); a character's movement budget
  follows a Speed that changes mid-turn (`settleMovement`). Effects whose result depends on an order the rules supplied do not give are never computed: the GM rules
  (`speed_ruling` on POST /api/gm/conditions). A monster attack on another monster is `monsterAttacksMonster` (same attack, damage and
  condition owners as for a character target).
- **A stat-block rider's duration** (`cmd/server/rider_duration_2024.go`): `riderUntil` / `readRiderEnd` / `riderEndFor` read "until the end of its
  next turn", "until the start (or end) of the <source>'s next turn" once, and `riderEnd.markerAdd` records it, for every Speed rider
  (`readSpeed` / `giveSpeed`), roll rider (`readDisadvantage` / `giveDisadvantage`: the "exposed" next-attack Advantage, Disadvantage on attack
  rolls or Concentration saves, read by `attackSources` / `saveSources`) and "can't take Reactions". A rider for one roll is a marker used
  up by `takeCondMarker` in `attackSources`: "Disadvantage on the next attack roll it makes before the end of its next turn" (Ettin,
  `disadv_next_attack`, on the target) and "the pirate has Advantage on the next attack roll it makes before the end of this turn" (Pirate
  Captain, `adv_next_attack`, on the hit's own attacker, gone with its turn). Go: `TestPostgresNextAttackDisadvantage`,
  `TestPostgresAttackerNextAttackAdvantage`. A new
  rider kind is one pattern built with `riderPattern` plus the owner that reads its marker; never write the duration into its pattern.
  Every target kind gets them through `giveConditions` (a companion's hit too). Go: `TestPostgresRiderDurationsOnEveryTarget`.
- **A trait that acts on a monster's death** (Death Burst, `cmd/server/monster_death_trigger_2024.go`): the writers of a monster's Hit Points
  call `monsterDiedIn`, which records the trigger in their transaction; `drainDeathTriggers` resolves it after the request (`withProtections`)
  and at each turn start, through `applySaveEffect`. Never roll a death effect inside a damage transaction. Go: `TestPostgresDeathBurst`.
- **A standard action a stat-block entry takes** ("takes the Disengage or Hide action", `cmd/server/monster_standard_actions_2024.go`):
  `resolveMonsterStandardAction` (a choice named in `use`; Disengage recorded as the `disengaged` combat effect that
  `noteMonstersLeavingReach` reads). An entry whose creature "can take the Hide action" at the end of its movement (Weretiger Prowl) rolls the Hide only when `use` names it (`optionalStandardRe`); an entry that is only its creature's own move is `onlyOwnMove` (probed with `anySpeed`, so "half its Speed" is read). Go: `TestPostgresMonsterNimbleEscape`, `TestPostgresOptionalHideAfterMove`.
- **An area that blocks sight** (SRD 5.2.1 Heavily Obscured; Magical Darkness): `areaBlocksSight` (`cmd/server/obscured_areas_2024.go`)
  is the one answer to "an area keeps the viewer from seeing the target", read by the attack roll's unseen-attacker and unseen-target
  rows and by a monster action that needs a creature it can see. Fog, ink and smoke are `obscured_area` rows (`obscuredAreas` reads and
  sweeps them, `startObscuredArea` writes): a Sphere on a point or a Cube on its maker, on the battle map, else the creatures named
  inside (the cast's targets, `POST /api/gm/obscured-area` `inside`); they end with their time, their source's Concentration
  (`endMonsterConcentrationIn` deletes them; a character's `concentrating_on`) or the GM (`end`: a strong wind or current); Blindsight
  perceives through them. A spell that makes one is a row of `obscuringSpells` (a character's and a monster's cast); a stat block entry
  is read by `obscuringTextRe` (Ink Cloud). Magical Darkness keeps its rows (`monster_darkness_2024.go`), read through the same answer: an Emanation from its source's token, or the Darkness spell's 15-foot-radius Sphere at the point the GM aims (`castMagicalDarknessSpell`, row `at`, `originToken`; without a battle map or a point the cast stays the GM's open item). Go: `TestPostgresMonsterDarknessSpellAtAPoint`.
  **A creature's own senses and light traits** (`creature_senses_2024.go`): Darkvision "(unimpeded by magical Darkness)" in its Senses
  (the devils, `creatureUnimpededDarkvision`, read by the Magical Darkness sight model), Sunlight Sensitivity (`lightPenaltyNow` while `inSunlight`: the GM's lighting `sunlight`; rows in
  `attackSources`, `checkCtx.sources` and `saveSources`, and the monster Hide). **A monster's Hide** is rolled by `attemptMonsterHide` (`hide_2024.go`: a
  light-gated Hide such as Shadow Stealth, its Stealth, Pass without Trace for any creature); a creature stops being hidden when it
  makes an attack roll (`endHidden`, in `attackCtx.roll`). Telepathy and other communication or sense-only traits are `GM` rows of
  `monsterTraitRules`. Go: `TestPostgresObscuredAreasAndSenses`, `TestObscuringAndSenseWordings`.
- **A monster attack's range on the battle map:** `monsterAttackRange` (`cmd/server/map_rules.go`) for every target route (character, monster,
  companion): refusal beyond reach or long range, Disadvantage beyond normal range, within 5 feet. Go: `TestPostgresMonsterAttackRangeOnEveryTarget`.
  The same per-attack gate refuses an attack the stat block bars against a creature the attacker has Grappled (`grappledTargetLock`,
  `grappledTargetLockRe`: "can't be targeted by the crocodile's Tail"), map or no map, a Multiattack's later attack included. Go:
  `TestPostgresGrappledTargetBarsAnAttack`.
- **A creature's special and forced movement:** `cmd/server/creature_movement_2024.go`, read from the entries' wording.
  - A failed save that pushes ("the target is pushed up to 30 feet straight away from the dragon": Repulsion Breath, Cold Gale, the Air
    Elemental's Whirlwind) is `saveEffect.Push` (`savePushRe`), applied by `applySavePush` in `finishTarget` through `forceToken` for
    every target kind. Go: `TestPostgresSavePushOnEveryTarget`.
  - Each own move of the turn is recorded (`recordCreatureMove`: the GM's map update without forced_movement, a player's path, a
    companion's path).
  - A charge is spent by the attack roll (`chargeAtRoll`) and applied per hit (`hitChargeDesc`; the alternative amount is
    `condCharged`; without a map the monster_action's `charged`).
  - `forceToken` is the one forced movement of a token (`pushToken` is built on it); a hit's push or pull is `hitForcedMove`,
    through `hitAfterDamage`.
  - An entry that moves its creature is `readSpecialMove` / `grantSpecialMove`, spent by the GM's map update
    (`takeSpecialMoves`: Opportunity Attacks, the Speed budget, no charge for a teleport).
  - "Immediately after dealing damage to a creature that is already Bloodied" is `movementEntryRefusal`.
  - Dragging a grappled creature is `dragCost` (Abduct).
  - An entry that pulls the creatures the monster holds ("pulls each creature Grappled by it up to 30 feet straight toward it": Roper Reel;
    "pulls one creature within 30 feet of itself that is Restrained by its Web Strand ...": Ettercap Reel) is `resolveMonsterPull`
    (`entryPullRe`), reading the held creatures from the condition records and moving each through `forceToken`. Go: `TestPostgresReelPullsHeldCreatures`.
  - A swallow is `readSwallow` / `resolveMonsterSwallow` (`monster_swallow_2024.go`): the damage once at the end of its next turn, then
    disgorged (Giant Frog), or at the end of each of its turns (`swallowEachRe`, Giant Toad); the locked attack is `swallowLockoutRe`.
    Go: `TestPostgresSwallowLifecycle`, `TestPostgresSwallowAtEachTurnEnd`.
  - Falling damage is `fallDamageDice` (the GM's falling damage).
  - Shape-Shift Speeds are the form owner's (`formSpeeds` / `formSpeedText`, `monster_shape_shift_2024.go`).
  - Sharing space (Swarm, Incorporeal Movement) is `markMapSpaces` in `map_spaces_2024.go`, with the turn-end hook
    `objectEndDamage`.
  - Go: `TestChargeWordingsOverTheCatalog`, `TestPostgresChargeOnEveryTarget`, `TestPostgresHitPushOnEveryTarget`,
    `TestPostgresEntryMovesAndRampageGate`, `TestPostgresSwarmAndIncorporealSpaces`, `TestPostgresShapeShiftAndDrag`.
- **Who in a fight is the party's enemy** (I-CALLA-29, Nov 24; `cmd/server/attitudes_2024.go`): a creature of the fight is hostile unless the GM
  recorded it Friendly or Indifferent (`hostileInFight`, read from `fightAttitudes`); `setAttitude` is the one writer (POST /api/gm/attitude and
  combat/add's `attitude`), and a combatant's attitude ends with its fight (`clearFightAttitudes` in `startFight` and `clearCombatInitiative`: a new
  fight gives its ids again from -1). my-turn lists the hostile creatures in `situation.enemies` and the others in `situation.others` (with their
  attitude), and `partyHints` follows them (`fightSides`: "press the attack" only with an enemy; a badly hurt Friendly or Indifferent creature is
  named). A Friendly creature stays a target (`enemyTargets`): attacking it is the player's choice. Not moved: a monster's own rules that pick
  "its enemies" (`chosenAuraTargets` takes the characters). Go: `TestPartyHintsFollowTheFightsSides`, `TestPostgresFightSidesInMyTurn`.
- **A mechanic a GM attaches to a stat-block entry** (owner, Nov 23; `cmd/server/monster_mechanics_2024.go`): `mechanicCatalog` is the one table of
  the kinds (what each does, the entries it attaches to, its parameters and limits, an example, the owner that applies it); `mechanicProblem`
  (through `validateCustomMonster`: import, edit, preview) refuses a wrong one from the row's own words; `mechanicsView` and `entryVerdicts` (a
  sentence its `covers` holds is applied by the mechanic) give the report and the stat block; `rulesText` is what the text rules read (the
  covered words left out: the play rows, `oldStyleActions`, the preflight and the Multiattack check); `attachedMechanic` is how an owner finds
  one; GET `/api/gm/compendium/mechanics` serves the table (`?srd=` a stat block to copy for a house rule). Which entries a kind fits is `mechanicFits` (with its
  check); a check of the entry a kind needs besides its parameters is the row's `check`. An entry's attack row is `entryActionRow` for every
  reader (the play rows, the preflight, the import's Multiattack check, the report). Kinds: `drop_to_zero`, read with the SRD wording as one
  `dropRule` by `droppedToZeroOnHit` (a monster target the hit defeats included); `chosen_damage_type` (the row's `ChosenTypes`, written
  in by `prepareMonsterAttack` through `withListedDamageType` from the GM's `use`, refused before any roll otherwise); `random_choice`
  (`rollForOption`, in `resolveMonsterEffectText`'s pick of one of an entry's effects when `use` names none); `random_condition`
  (`randomConditionOnHit` in `hitAfterDamage`: the rolled condition given by the hit condition owner from the SRD rider sentence it stands
  for, `conditionTimeWords`); `forced_move` (`mechanicHitMove`: the movement owner's `hitMove`, which `textHitMoves` reads from the SRD
  wording, applied by `hitForcedMove`); `light_penalty` (`mechanicLightPenalty`: the light owner's `lightPenalty`, which
  `textLightPenalty` reads from Sunlight Sensitivity, combined by `lightPenaltyNow` for `attackSources`, `checkCtx.sources` and
  `saveSources`, the reason the trait's own name); `light_turn_damage` (`lightTurnDamage` in `monsterFeaturesTurnStart`, through
  `damageMonster`); `obscuring_zone` (`obscuringZone` at the start of `resolveMonsterEffectText`, through `placeArea` /
  `startObscuredArea`; the GM's `point` from `monsterAreaPoint`); `no_healing_aura` (`noHealingAuraOver` in `cantRegainHP`, on the battle
  map); `curse` (`curseOnHit` in `hitAfterDamage`: `curseSentences` writes the SRD's curse sentences, which the curse owner reads, `readCurse` /
  `giveCurse`); `engulf` (a save entry: a row of `failureKinds`, set in `resolveMonsterEffectText` and given by `giveEngulf` in `finishTarget` for
  every target kind; `engulfSentences` writes Restrained with what lasts while it does, and the record carries the escape, `Adjacent`);
  `max_hp_drain` (`mechanicHPMaxDrain`, read by `drainHPMaximum` beside the SRD wording); `attacked_with_disadvantage` (read by
  `attackedWithDisadvantage` beside Blurred Form); `condition_on_hit` (`conditionOnHit` in `hitAfterDamage`; `conditionHitSentence`, shared
  with `random_condition`, writes the SRD's rider sentence: the time, "until it takes damage", the size gate). `roll_rider` (`rollRiderOnHit`;
  `rollRiderSentence` writes the SRD's roll rider sentence the roll rider owner reads, `exposed` with its one end). `evasion` (`traitMechanicOf` in
  `creatureEvasion`); `regain_from_damage` (`regainOnHit` through `drainRegain`); `burning` (`burningOnHit`, or a failed save:
  `failureKinds`, the one list of what a failed save's attached mechanics give, `saveEffect.OnFailure` given in `finishTarget`; engulf is the
  other row); `grapple` (`grappleOnHit`; `grappleSentence` writes the SRD grapple sentence, its escape DC, size gate and Restrained until the
  grapple ends). A hit finds its entry's
  mechanics by the entry's name (`attachedMechanic`, `hitAfterDamage`'s `entry`), never by its text, which two entries can share. `resolveMonsterEffectText` reads an entry with mechanics as its `rulesText` (`metaEntry`), and an entry they cover whole is only used. A trait an attached mechanic covers is applied in the GM's trait view (`traitsForGM`) and never an open item
  (`recurringGMTraits` reads `rulesText`). Not done: Resistance in a light (the damage owners read Resistances by the monster's key alone). A new kind is one row plus
  its rule in the owner it belongs to. Go: `TestMechanicProblems`, `TestCoveredSentencesAreApplied`, `TestChosenTypeAndRandomChoiceProblems`,
  `TestRandomConditionAndForcedMoveProblems`, `TestPostgresRandomConditionAndForcedMoveOnEveryTarget`, `TestLightMechanicProblems`,
  `TestPostgresLightMechanicsInPlay`, `TestZoneMechanicProblems`, `TestPostgresZoneMechanicsInPlay`, `TestCurseMechanicProblems`,
  `TestPostgresCurseMechanicOnEveryTarget`, `TestEngulfMechanicProblems`, `TestPostgresEngulfMechanicInPlay`,
  `TestConditionOnHitProblems`, `TestPostgresConditionOnHitEndsOnDamage`,
  `TestRollRiderSentencesReadBack`, `TestPostgresRollRiderOnHit`, `TestPostgresEvasionAndRegainMechanics`, `TestPostgresBurningMechanic`, `TestPostgresGrappleMechanic`, `TestPostgresAttachedDropToZeroOnEveryTarget`, `TestPostgresMechanicsInPreviewAndCatalogue`, `TestPostgresChosenTypeAndRandomChoiceInPlay`.
- **What a hit does after its damage:** `hitAfterDamage` (`cmd/server/hit_after_damage_2024.go`), called once by every hit route with the
  attacker: the drain below, "it starts burning" (the Burning owner, `burning_2024.go`), a push or pull (`hitForcedMove`) and the hit's
  record for an entry used right after it (`recordLastHit`). A new effect of this kind is one call there. Go: `TestPostgresBurningOnHitEveryTarget`.
- **A Hit Point maximum drain** (Life Drain, `cmd/server/hp_max_drain_2024.go`): one reader `hpMaxDrainOf` (all the damage taken, or one
  type: "the Necrotic damage taken") and one writer `reduceHPMaximum` (`reduceMaxHP` for a character, restored by `restoreAllHP`;
  `reduceMonsterMaxHP`, `reduceCompanionMaxHP`). A hit: `drainHPMaximum` through `hitAfterDamage`, with what each damage type dealt
  (`hpDamage.ByType`, the monster cursor's `Dealt`, the companion route's). A saving throw effect: `saveEffect.Drain`, applied by
  `drainAfterDamage` after each damage part on the character, monster and companion save routes, with the source's regain
  (`drainRegain`, the monster HP owner). Go: `TestPostgresLifeDrainOnEveryTarget`.
- **A creature's ability scores** (`cmd/server/ability_owner_2024.go`): `changeAbilities` (`changeAbilitiesIn` in a caller's transaction,
  then `abilitiesChanged`) is the one writer of a character's six score columns and `item_base_scores`: the row locked, the scores
  changed and written in one transaction; a new Constitution modifier changes the Hit Point maximum by the level (the Hit Point owner)
  unless `abilityHow.KeepHP` (Wild Shape). On it: the Ability Score Improvement and the feat claim (their guarded claims run inside the
  change), the capstones (`capstoneScores`), potions, tomes and attunement items, the Deck's cards, Wild Shape, the GM's and the admin's
  editors (`setAbilityScores`). **A reduction** (Shadow Draining Swipe: "the target's Strength score decreases by 1d4. The target dies if
  this reduces that score to 0") is `reduceAbility`: an `ability_reductions` row with its source; a character's column is lowered (every
  reader of the column reads the reduced score); a monster's or companion's score is its stat block's less its open reductions
  (`creatureAbilityScore`, `monsterAbilityShift`: read by `saveModifier`, `creatureStats` and the grapple escape). The 2024 text gives it
  no end: Greater Restoration ends it (`endAbilityReductions`, `restoreAbilityScore`). A hit's decrease is read by `abilityDecreaseOnHit`
  through `hitAfterDamage`. `TestNoOtherAbilityScoreWriter` checks; Go: `TestPostgresAbilityDrainOnEveryTarget`.
- **The state of one item a character carries** (`cmd/server/item_state_2024.go`), kept on the item in the inventory and written only
  through the inventory writer: **corrosion** (`corrodeItem`: -1 to a weapon's attack rolls, read by `weaponAttackMods`; -1 to the AC an
  armor offers, read by `characterACWith`; destroyed at -5 or at AC 10; a magic item spared, `itemIsMagic`; `metalArmor` /
  `nonMetalWeapons` for "a nonmagical metal object") from a hit (`corrodeArmorOnHit` in `hitAfterDamage`: Gray Ooze, Black Pudding), a
  trait the attacker touches (`corrodeOnContact`, stage 8 of `runCharacterHitTail`: Corrosive Form, the ammunition destroyed) and a
  failed save (`saveEffect.ItemPenalty`, `corrodeCarriedObject`: Rust Monster Antennae); Mending removes it (`itemRepairSpells`,
  `mendItem`). Go: `TestPostgresItemCorrosion`.
- **A condition's second stage** (SRD 5.2.1 Sleep; a monster's "Second Failure:"): `conditionSave.Then` / `ThenRounds` /
  `ThenEndsOnDamage` / `ThenMagicalSleep` / `ThenRepeats` on the record's repeated save; `condition_turns.go` turns the record into the
  second condition on a failed repeat (with no end for "Petrified instead of the Restrained condition": Basilisk, Gorgon, Medusa; keeping
  the repeated save for "After 1 minute, it succeeds automatically": the silver dragons). A spell's stage is a row of `spellStages` (`endSaveFor`, `spell_effects.go`), read by a character's and a monster's
  cast; a monster text's is read by `readSecondFailure` (`monster_effects_2024.go`). An Unconscious that "ends if it takes damage" is
  `notesEndsOnDamage`, ended by `wakeOnDamageSleep` (which also ends a monster's cast of Sleep, `sourceIsSpell`). A monster's cast of
  a spell that ends conditions reads `spellEndsConditions` (`monsterCastEndsConditions`; a heal ends them first in
  `monsterCastHealing`). Go: `TestPostgresSleepSecondFailure`, `TestPostgresSecondFailureStages`, `TestPostgresMonsterCastEndsConditions`.
- **Monster attack damage definitions:** parseMonsterAttack and prepareMonsterAttack
  in cmd/server/monster_damage_choice_2024.go: fixed dice, flat damage, a type chosen for a trait (Half-Dragon),
  secondary damage, validation and refusal. oldStyleActions is a storage adapter;
  companionRows and prepareCompanionPlan adapt the same definition for companions,
  familiars, item companions and Wild Shape. No other consumer parses stat-block
  hit damage. game.NormalizeDice, game.ParseDice, game.FlatBonus and the existing
  game damage rollers own signed dice arithmetic. Unsupported/non-damaging hit
  effects are explicitly GM-adjudicated; no fallback damage is rolled.
Add to the owner, never beside it. When you give another rule one owner, add it to this list.
- **Monster text patterns serve SRD wording** (owner, Nov 21: SRD 5.2.1 only): `monster_pattern_guard_2024_test.go`. Every package-level
  pattern of a stat-block reader (`monsterTextFile`) is in `monsterTextPatterns` (matched against every SRD stat block, cut into entries,
  sentences, sections and clauses by `srdTextPieces`, or a named `patternWitnesses` entry: an SRD entry's own words, or an invented sentence
  a named test uses) or in `notMonsterText` (stored rows, request text); one built inside a function is in `localMonsterPatterns`.
  `TestMonsterPatternsServeSRDWording`, `TestEveryMonsterPatternIsListed`. A new rule is written for SRD wording, never for book text.
  Every branch of every alternation is held to the same check (`TestMonsterPatternBranchesServeSRDWording`, split on the source text:
  `alternationVariants`); lists of game vocabulary are exempt (`patternVocabulary`), and only a refusal may keep a branch with no SRD
  wording, with an invented witness (`branchWitnesses`).
- **The coverage of every SRD stat block sentence:** `srd_sentence_coverage_2024_test.go` (`srdSentenceCoverage`, `entryCoverage`): each
  sentence of the 330 stat blocks follows the server's route (trait table, attack owner, save owner, the resolver's branches) and is
  applied by a named rule or shown to the GM; `TestSRDMonsterSentenceCoverage` fails on a silent sentence whose family isn't in
  `srdUnhandledFamilies` (the work list; empty now). The family table is `docs/SRD_MONSTER_COVERAGE.md` (`SRD_COVERAGE_OUT`). What a save
  entry's rules don't read outside its sections is `saveSetupText` (`saveEffect.Setup`, shown whatever the result), a multi-effect
  entry's intro `saveOptionsIntro`, a cast's riders `castRiders`: all shown to the GM. What an owner applies is never repeated to the GM:
  a Legendary Action's once-a-turn sentence (`legendaryOnceSentenceRe`, LAOnce's), a judged damage alternative (`judgedAlternativeRe`),
  an entry that is only its creature's move (`onlyOwnMove`); a hit sentence that waits on a later result goes to the GM alone, the hit's
  other riders still apply (`parseHitRider`), and so does a sentence that lasts only while a condition the waiting one gives
  (`dependsOnDeferred`: "While Poisoned, the target also has the Paralyzed condition" after "If this damage reduces the target to 0 Hit
  Points, ..."; "Until the grapple ends, ..." after "can give it the Grappled condition ... instead of dealing damage"). What a hit does
  when it reduced its target to 0 Hit Points is `droppedToZeroOnHit` in `hitAfterDamage` (`hitDroppedToZero`; Stable with the
  conditions: `dropRiderRe`, Phase Spider, Vampire Familiar; death: `dieOnDropRe`, Gibbering Mouther, Mummy). A "uses <entry>" in a
  clause that says when an effect ends never calls that entry (`usesAnotherEntry`, Will-o'-Wisp Vanish). Go:
  `TestOwnersAppliedNotRepeatedToTheGM`, `TestPostgresHitThatDropsToZero`.
  **The early-game playtest set** (owner, Nov 22: the monsters of CR 0 to 1, then CR 0 to 3, then CR 0 to 6, then CR 0 to 11, then CR 0 to 30: all 330 stat blocks, 1318 entries
  with every Legendary Action; the only target until the owner says otherwise): `TestSRDEarlyGameCoverage` (its table, `docs/SRD_EARLY_GAME_COVERAGE.md`, `SRD_EARLY_OUT`) and the route test
  `TestPostgresEarlyGameEveryEntry`: every action, Bonus Action and Reaction through narrate's monster_action at a character, with every
  option its entry offers in `use` and the setup its gate needs, what is stored read back; every trait in the GM's view of the fight.
- **Attack rolls:** `game.AttackRoll` (`game/attack.go`) is the rule of an attack roll (Advantage, Disadvantage and how
  they cancel, natural 1 and 20, Critical Hits, the wording of the result line). `attackCtx` and the `attackSources`
  table (`cmd/server/attack_2024.go`) decide what gives Advantage or Disadvantage, the bonuses, the target's Armor
  Class and what blocks an attack. Every attack in the server (a character's, a monster's, a spell's, a companion's, an
  item's) builds an `attackCtx` and calls `roll()`. A new source of Advantage or Disadvantage is one row in
  `attackSources`. Never roll a d20 for an attack anywhere else. An effect that depends on the outcome (a Weapon Mastery,
  Sneak Attack, a smite) is told after the outcome and only when it happens, never as a note written before the roll.
  **A character's weapon attack bonus and damage modifier** (the ability used, proficiency, a magic weapon's +N, Archery) is
  `weaponAttackMods` (`cmd/server/weapon_attack_2024.go`): the main and off-hand attacks, the opportunity attack and the sheet's attack
  lines (with the breakdown, `toHit`; every weapon the character has, the ones in hand first, the others marked carried: Matt, Nov 16) read it; what belongs to one moment (Rage, magic ammunition, Sacred Weapon) stays with the attack
  (feedback log 38; `TestPostgresSheetAttackBonusIsTheAttacksOwn`).
  **The attacker's line** is `game.AttackRoll.HeadForAttacker` (the total, the d20 and what was added, never the Armor Class),
  through `LineForAttacker` or, for a recorded attack, `attackHead` (`cmd/server/attack_target.go`); the GM's line is
  `Line`. `RollInfo` holds only what the roll added besides. `TestNoOtherAttackerLine` checks that no handler writes one.
- **On-hit riders** (extra damage "whenever you hit it with an attack roll": Hex, Hunter's Mark): `hitRiderRules` / `hitRiders`
  (`cmd/server/hit_riders_2024.go`), rolled after the outcome and only on a hit, each with its own damage type. Every attack route of a
  character hands it the hit: `resolveAttackOnMonster` (every weapon attack and Unarmed Strike on a monster, `ridersOnMonster`), a spell
  attack and each beam of Eldritch Blast and Scorching Ray (`spell_resolution.go`: each beam's line is `LineForAttacker`), a spell attack
  at a character (`damageCharacterWithRiders`), Spiritual Weapon and Flame Blade, the items whose roll is the character's, a Wild Shape
  form's attack (`hitWithRiders`). A new rider is one row. `TestEveryCharacterAttackRouteGetsTheHitRiders` (go/ast) fails on a function
  that makes a character's attack roll without it; `TestNoOtherAttackerLine` also fails on a roll whose line is thrown away; Postgres:
  `TestPostgresHitRidersOnEveryAttackRoute` (feedback log 65).
- **A heal or a restoring spell aimed at the dead** (a dead creature is an object): `livingTargetRefusal`
  (`cmd/server/living_target_2024.go`), asked once at the start of `resolveAction` (every route: POST /api/action, a pending turn's step,
  a readied action, a stand-in) before anything is spent, so a pending turn takes its fallback. Which spells: `spellRestores` (the spell
  data's healing and `restoringSpells`; `revivalSpells` never); which features: the action table's `heals` tag; whom a spell that helps is
  aimed at: `spellAllyAim` (`spellAllyTargets` its IDs): the request's targets (a negative `target_id`: a creature of the fight, healed by the monster Hit Point owner), else every character the description names, else the caster only when the description names no one else (`describesOthers`); creatures it names that the server doesn't track (NPCs) are the GM's (`othersNote`, I-CALLA-24); read by the healing spells, Lesser and Greater Restoration, Spare the Dying, the conditions a spell ends and Blessed Healer; a potion given to an
  ally and the steed's Healing Touch ask `deadTargetRefusal`. Postgres: `TestPostgresHealingRefusesTheDead` (feedback log 67).
- **Saving throws:** `game.SavingThrow` (`game/save.go`) is the rule of a saving throw (Advantage, Disadvantage and how they
  cancel, the total against the DC, no meaning for a natural 1 or 20, an automatic failure or success, a Death Saving Throw)
  and its wordings: `Line` / `Clause` / `Tail` for the GM's log (player_text.go hides their numbers) and `ForSaver` for the
  saver's own reply. `saveCtx` with the `saveSources` and `saveAdjusts` tables (`cmd/server/save_2024.go`) decides what gives
  Advantage, Disadvantage or an automatic failure and what is added (conditions, Danger Sense, species traits, Haste, Bless,
  Aura of Protection, magic items, cover, Exhaustion ...); `saveModifier` is the only saving throw modifier; `roll` applies
  Halfling Luck, Heroic Inspiration, the Luck Blade and Indomitable Might, `resist` a monster's Legendary Resistance. Every
  save (a character's, a monster's, a death save, the GM's call) builds a `saveCtx` or calls `creatureSave`. A new source is
  one row. Never roll a d20 for a save anywhere else; `TestNoOtherSaveRoll` lists every other d20 in the server.
  `creatureSave`, `monsterSaveFor`, `monsterSaveRoll` and `monsterSpellSave(For)` return the `game.SaveResult`: a message about a
  save uses its wordings (`LineAgainst` keeps the DC visible when the reader's own feature forces the save), never a line of its own
  from the total and the DC, so it always says why a save had Advantage or Disadvantage; `TestNoSaveLineByHand` checks (HANDOFF item 24).
  **A saving throw's damage** is `game.SaveDamage` (half or none on a success; Evasion: none on a success, half on a failure, only for an effect
  that allows half) with `creatureEvasion` (`evasion_2024.go`: a character's class feature, a stat block's trait): a character's spell at a monster
  or a character, a stat block's save effect at a character, a monster or a companion, the GM's area cast. Go: `TestPostgresEvasionOnEverySaveRoute` (I-WL-6).
- **Ability checks:** `checkCtx` and `checkCtx.sources` (`cmd/server/check_2024.go`) decide what gives an ability check Advantage or
  Disadvantage, for a character, a monster or a companion (every creature: Poisoned, Frightened while its source is in sight on the battle
  map, a `disadv_checks` rider, Sunlight Sensitivity, a damage-triggered rider such as Aversion to Fire, the holder's own words when escaping
  its grapple (Mimic); a character's own: Charmed target, Remarkable Athlete, Supreme Sneak, Boots of Elvenkind, armor, Large Form, Powerful
  Build, Help, travel pace, items, Foresight, Enhance Ability, Reduce; Hex's chosen ability is the target's `hex_checks` record, `castHexChecks`, ended with the caster's Concentration); `rollCheckD20` is the one check d20 and cancels them;
  `checkExhaustion` is the -2 per level for any creature; the bonus stays `checkBonus`. "Disadvantage on attack rolls and ability checks
  until ..." and "While Deafened, the target also has Disadvantage on ability checks and attack rolls" give the marker `disadv_checks`
  (`readDisadvantage`, `lastingEffects.DisadvChecks`). `checkCtx.roll()` asks the sources and rolls (`checkRoll`: the d20s, how it was
  rolled, why, and Exhaustion's `Penalty`); `checkWhy.note()` is the bracket every reply carries; `spellcastingCheck` is a caster's
  ability check (Dispel Magic). Every check route is on it: the GM's skill, tool, contested and group checks, the condition escape, the
  detach action, the GM's grapple escape, both Hide actions, Search / Study / Influence (a recorded attitude is `checkCtx.attitude`;
  an unknown one is rolled once per attitude), Stabilize, Fast Hands, the jump landing, the Sphere of Annihilation, a Spell Scroll's
  checks and Dispel Magic (the GM route and the spells it ends); a source a route alone knows is `checkCtx.advFrom`.
  Never roll a check's d20 anywhere else. Go: `TestPostgresCheckSourcesOnEveryRoute`, `TestNoOtherCheckRoll`.
- **An effect a stat block puts on a creature that is not a rules condition** (`cmd/server/effect_records_2024.go`): a record of a kind in the one
  condition store, read once by `saveEffect.readEffects` (called by `readRider`, so a failed save and a hit alike), written by
  `giveEffects` (in `giveConditions`) and read by every owner it changes: `d20_minus:1d6` (Giggling Magic: "the target rolls 1d6 whenever it
  makes an ability check or attack roll and subtracts the number rolled from the D20 Test", until the end of its next turn) is rolled by the
  check owner (`checkWhy.Penalty`, which every check route subtracts: Exhaustion's -2 per level is there too) and an `attackCtx.gather`
  adjustment; `weakened:1d6` (Weakening Breath: Disadvantage on Strength-based D20 Tests, the die off its damage rolls, a repeated save and
  1 minute) is a Disadvantage row in `attackSources` (the attack's `strength` flag: a character's `weaponAttackMods.UsesStrength`, a stat
  block's `monsterAttackIsStrength`), `saveSources` and `checkCtx.sources`, and `damageRollAdjust` after every attack damage roll (it also
  holds Enlarge and Reduce: nothing calls `enlargeHit` but it; `TestDamageRollsAskTheAdjust` lists the routes). "each creature that isn't
  currently affected by this breath" is `affectedBySource`, a gate on the targets. A new kind is one row (a pattern in
  `monsterTextPatterns`, a record name, the owner's reader). Go: `TestPostgresD20RidersOnEveryRoute`, `TestDamageRollsAskTheAdjust`.
- **A curse or a wound a stat block gives** (`cmd/server/curse_records_2024.go`, `wound_records_2024.go`; the same records as the effects above, read by
  `readRider` for a hit and a failed save alike): a `cursed` record whose Detail is its flags (`no_regain`: `cantRegainHP`; `short_rest` and
  `long_rest`: `restBenefit`, which the Short and Long Rest handlers ask after the time has passed; `lycanthropy`: what the GM is told), ended by
  its time or by Remove Curse and Greater Restoration (`endCursesBySpell`), and kept through a fight and a Long Rest (`keptRecordKinds`); the
  conditions of "Until the curse ends, the target has the Charmed and Poisoned conditions" end with it (`saveEffect.CurseLinked`). "If the
  target is a Humanoid" is `saveEffect.OnlyType`, a gate on the targets in `applySaveEffect` (no save for another type). A `wounded` record
  (infernal wound) holds the Hit Points lost at the start of each of its turns (`woundTurnStart`, stage 7 of the turn pipeline: lost, not
  damage), lasts its minute, is closed by a spell that restores Hit Points (`endWoundsBySpell`, in `endSpellConditions`) or by the Wisdom
  (Medicine) check its record carries (POST /api/conditions/escape; `conditionEscape.Adjacent`: `target_id` names a character within 5 feet),
  and "doesn't already have an infernal wound" is `saveEffect.NotHolding` (no save). **A curse's Hit Point maximum** (Mummy: "its Hit Point maximum doesn't return to normal when finishing a Long Rest, and ... decreases by 10 (3d6) every 24 hours that elapse"): the
  record's flag `hpmax=3d6@<story round>` (`curseHPMaxRe`); `cursedHPMaxTick` (called by `passSpellTimeOn` after the story clock moves) claims each full 24 story hours on the
  record and reduces the maximum through `reduceCursedMaxHP` into `max_hp_cursed`, which a Long Rest (`restoreAllHP`) does not give back; the record's end
  (`conditionChange.remove`) moves it into the ordinary reduction (`releaseCursedMaxHP`), returned at the next Long Rest; a maximum reduced to 0 is a death. A creature is cursed once (a second hit starts no second count: `holdsHPMaxCurse`); the days count on the campaign's story clock, which only the GM's
  pass-time and travel move (a rest does not), on the campaign the creature is in (the stamp carries its campaign: a creature that moves to another campaign starts counting again
  there); a cursed creature's Long Rest returns Life Drain's part of the maximum but no Hit Points (`restoreAllHP` asks `cantRegainHP`). Go:
  `TestPostgresMummyCurseHitPointMaximum`, `TestPostgresMummyCurseEdges`. Not applied, told to the GM: a curse's "or until the
  incubus dies", the lycanthrope's transformation at 0 Hit Points, the Aboleth's underwater clause (the server has
  no underwater state). Go: `TestPostgresStatBlockCursesOnEveryTarget`, `TestPostgresInfernalWoundOnEveryTarget`.
- **A character's portrait** (the photo on its sheet): `cmd/server/portrait_2024.go` is the one owner of what it may be (`checkPortraitImage`: not empty, up to
  5 MB, a PNG, JPEG or WebP that decodes, each side 32 to 4096 pixels, a note when the sheet's 4:5 frame will crop it), of reading `image_base64`
  (`decodePortraitBase64`) and of the writes (`savePortrait`, `clearPortrait`). The owner's route (POST, PUT and DELETE `/api/characters/{id}/portrait`,
  `character_profile.go`) and the administrator's (POST, PUT and DELETE `/api/admin/characters/{id}/portrait`, `admin_portrait_2024.go`, written to
  the admin change log with its note; the admin page's Portrait card adds, replaces and removes) both go through them; `TestNoOtherPortraitWriter`
  fails on any other write of the avatar columns. Go: `TestPostgresAdminPortrait`.
- **A campaign's background picture** (the scene behind the Live Table's header; owner, Nov 23): `cmd/server/campaign_background_2024.go` is the one owner. What a
  picture may be is `checkImage` with an `imageRule` (`portrait_2024.go`: the portrait's `portraitRule` and the background's `backgroundRule`, 320 to 4096 pixels, up to 5 MB,
  a note when a tall picture will be cropped); the writes are `saveBackground` / `clearBackground` (`lobbies.background_*`), and `backgroundURL` is what the page shows (the
  campaign's own with its change time, else the default dragon `defaultBackground`). The administrator's route (POST, PUT and DELETE `/api/admin/campaigns/{id}/background`,
  `handleAdminBackground`, written to the admin change log; the campaign page's Background card in the admin page) sets it; the public image is GET `/api/campaigns/{id}/background`
  (gated like every read under `/api/campaigns/{id}/`: a private campaign's picture is private). The admin editor never sends the bytes (`editorColumns`). Go:
  `TestPostgresAdminCampaignBackground`, `TestNoOtherBackgroundWriter`.
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
- **When a class changes its prepared spells and cantrips:** `spellChanges` (`cmd/server/prepared_spells_2024.go`, SRD 5.2.1: a Wizard
  replaces a cantrip after a Long Rest, the other cantrip casters on gaining a level): the level-up and Long Rest grants, the refusal of
  a change too many (`spellChangeRule`) and `spellChangeSummary`, which skill.md and the prepare request's reference say word for word
  (`TestSpellChangeSummaryInGuides`).
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
  `healUpToHalf`, `healOnlyAtZero`, `healOnlyBloodied`: Survivor, `Vitals.Bloodied` read on the locked row), `healCharacter`, `undoDamage` (a hit undone or reduced afterwards), `setHP`, `restoreAllHP`,
  `killCharacter`, `reviveCharacter`, `editVitals` / `editVitalsIn` (an editor's values: the GM's update-character and the admin page send every field of `vitalsFields` there, never as a plain column; `game.Vitals.Edit` is the rule: a maximum of at least 1, a dead character at 0, brought back only with `is_dead` false; `vitalsRefused` changes nothing), `recordDeathSave`, `stabilizeCharacter` (`Vitals.Stabilize`: the Medicine action, Spare the
  Dying; the 1d4 hours start, `startStableHours`), and `changeMaxHP` / `maxHPChange` (the SQL a progression claim includes).
  `handleDamage`, `handleHeal` and `damageCharacter` are thin wrappers. Temporary Hit Points are granted by `grantTempHP`
  (`temp_hp_2024.go`). A new feature that stops a drop to 0 is one row in `dropRescues`. `TestNoOtherHitPointWriter` checks;
  suite: `hp_rule_test.py` (audit AUD-06). **A creature that can't regain Hit Points** is `cantRegainHP`
  (`condition_lasting_effects_2024.go`: a condition's lasting effect, Chill Touch, the Sword of Wounding), read by `healHP`,
  `healMonster` and `healCompanion`; a monster form's own trait stays `catalogForbidsHPGain` (feedback log 63).
- **How a creature looks (its health label):** `game.Vitals.Health` (a character: dead, dying or stable at 0 first, then critical,
  bloodied, wounded, healthy by the share of Hit Points) and `game.MonsterHealth` (a monster or companion: defeated at 0), with
  `game.AtZero` (`game/hit_points.go`). my-turn's party list, own status and enemies, the GM's party view, the spectator view, the
  campaign page's boxes, companions, the GM's monster adjustment and every "looks bloodied" line read it; my-turn's hints follow it
  (`partyHints`, `zeroHPHint`: no help hint for the dead, a Death Saving Throw only for the dying). Feedback log 66 (I-ALDRIC-1, 5);
  Go: `TestHealthLabel`, `TestPostgresHealthLabelsMapAndActionBudget`.
- **The attacks an Attack action holds:** `attacksPerAction` (`actions.go`: Extra Attack, or a Pact of the Blade invocation's when
  higher): the Attack action's spending, the action economy views and a pending turn's action budget (`actionBudgetWarning`, with a
  queued Action Surge that has a use left; S-ALDEN-11) read it.
- **A fight's state (combat_state):** `cmd/server/combat_state_2024.go`. `changeFight` is the one writer of the turn order, the
  current turn, the round and the monster id counter: the row locked (`lockFight`), entries changed as maps (unknown fields kept),
  written back in the same transaction. On it: `place` / `insertAt` / `remove` / `setInitiative` (whose turn it is is kept),
  `newMonsterID` (a per-fight counter, `last_monster_id`: an id is never given twice in a fight, audit AUD-39), `editCombatant`
  (any field but Hit Points), `removeFromFight` (the GM's remove, adjust-monster, a kick, an Iron Flask), `startFight`,
  `clearCombatInitiative`; `readFight` / `combatEntries` read. **A turn change** is `advanceTurn` (`turn_advance_2024.go`):
  combat/next, combat/skip, narrate's `advance_turn`, the pending-turn walk (`ptAdvance`), the timeout worker and the removal of
  the creature whose turn it is call it. It claims the turn (`turn_claim`, under the lock: a second change of the same turn is 409
  `turn_change_in_progress`; a turn the request named in `expected_turn` {round, turn_index} that has ended is 409 `turn_already_moved`
  with the turn it is now: `turnRef.Named`, checked in `turnHow.check`; `gmEndsTurn` passes it on, narrate checks first with
  `precheckTurn`), runs `turnEndHooks`, the caller's `between` (Legendary Actions), moves (`nextTurn`: dead and 0 HP
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
  **What a condition does while it lasts** ("While Poisoned, the target can't regain Hit Points and takes 21 (6d6) Poison damage at
  the start of each of its turns"): `readLastingEffects` reads it with the save or hit (`readRider`), `addLastingEffects` writes it
  with the record as combat_effects rows gated on that record (`gate_condition`: a no_healing row and a recurring_damage row the
  recurring owner ticks, `monster_recurring_damage_2024.go`); the record's end ends them; `conditionView`'s `effects` shows them.
  A text's damage dealt now is `immediateDamage`: never a damage "at the start (or end) of each of ... turns" (feedback log 63).
  "While Frightened, the target has the Paralyzed condition" (`lastingEffects.Also`) and "While Deafened, the target also has Disadvantage
  on ... attack rolls" (`DisadvAttacks`: the roll rider's `disadv_attacks` marker, and `DisadvChecks`: the check owner's `disadv_checks`)
  are records noted `notesEndsWith` + the condition ("frightened", "deafened"), ended with that record by `conditionChange.remove` (the one place every record ends); the condition that lasts "until the grapple ends" (Restrained, or Poisoned for the Roper's Tentacle: `saveEffect.LinkedCond`) stays `linkedCondition`.
  **The conditions a spell ends** (Heal, Mass Heal, Power Word Heal, Protection from Poison, Lesser and Greater Restoration's
  pick): the table `spellEndsConditions` with `endSpellConditions` (`spell_ends_conditions_2024.go`); a healing spell ends them
  before it heals (feedback log 64). A new spell that ends conditions is one row.
- **A spell's later damage and the items a spell creates** (owner, Nov 23, the tester's spell run): `scheduleLaterDamage` / `laterDamageAtTurnEnd`
  (`spell_later_damage_2024.go`: a damage part timed end_of_next_turn, dealt once at the target's turn end: Acid Arrow, Vitriolic Sphere); `spellItems`
  (`spell_created_items_2024.go`: Goodberry, used as a potion is, gone with its spell). A spell without Concentration never ends with it (`concentrationHolds`
  reads the spell data). Go: `TestPostgresSpellLaterDamageAtTheTargetsTurnEnd`, `TestPostgresGoodberryMakesBerries`.
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
- **A POST that stands for DELETE, PATCH or PUT** (agent tools that send only GET and POST): `overrideMethod`
  (`cmd/server/method_override_2024.go`), first in `withProtections`: `?_method=DELETE` or the `X-HTTP-Method-Override` header;
  `signedMethod` is what a signature covers; `deleteHow` is how a message names a DELETE; the reference says it on every DELETE,
  PATCH and PUT and `methodHowTo` in the start text (feedback log 40; `TestMethodOverride`).
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
- **The operator's access** (`/api/admin/*` and the admin page): `requireAdmin` (`cmd/server/admin_2024.go`) is the one check (the
  X-Admin-Key header, or the page's session cookie with the X-Admin-Page header on a change; the wrong-key lockout). Never call
  `adminKeyOK` in a handler. **A new token for a player who lost theirs** is `POST /api/admin/issue-token` (the old tokens and keys
  revoked and the new one made in one transaction, `createTokenIn`; the admin history records that a token was issued, never the
  token; reply not cached); the page's character view has its Regenerate token and Copy token buttons. Go: `TestPostgresAdminIssuesToken`.
- **The admin's accounts** (owner, Nov 22; `cmd/server/admin_accounts_2024.go`, the Accounts tab of the admin page): an account is edited, deleted and restored by the
  same generic routes as a character or campaign (`adminRowRoutes`, `adminSet`; GET/POST `/api/admin/accounts/{id}`, `.../delete`, `.../restore`;
  GET `/api/admin/accounts?q=&deleted=include|only` lists by name, email or id). **Delete hides** (`agents.deleted_at`, `deleted_by`, `deleted_characters`), nothing is
  erased: `signInOnce` asks `accountDeleted` once for every kind of sign-in, so a deleted account's tokens and keys stop working (401 `account_deleted`) and
  come back with `restoreAccount`; its characters are hidden with it (`softDeleteCharacterIn`) and restored with it (`restoreCharacter`); it is refused while the
  account is the GM of a campaign. `adminAccountValue` checks a name (1 to 64, unused) and an email (valid, unused); `editorColumns` locks the secrets
  (`password_hash`, `salt`, `verification_code`, the delete record, `last_seen`) and `adminRow` never sends them; a character's player or a campaign's GM
  must be a live account (`accountUsable`). Go: `TestPostgresAdminAccounts`; the page: `tools/page_check_320.js`.
- **The admin's Logs, Settings and an account's activity** (owner, Nov 22; `cmd/server/admin_logs_2024.go`): **Reports** are `feature_requests` (what players and GMs send to
  POST /api/feature-requests), their status changed by POST `/api/admin/reports/{id}` and logged (kind `report`); **Errors** are `request_errors`, written by
  `failureRecorder` in `withProtections` (the shared layer: every /api reply of 400 and over but 401 and 429, with the reply's error code and the account
  signed in; `api_logs` is not a request log and nothing reads it); **sign-in failures** are `sign_in_failures`, written by `recordAuthFailure` (the one
  place a failed sign-in is recorded: address, how, the account when known); both kept 30 days (`pruneLogs`); `GET /api/admin/settings` is a read-only account of
  the server, no secret; `GET /api/admin/accounts/{id}/activity` (last seen, failed requests, the routes that fail most, tokens, failed sign-ins). Go:
  `TestPostgresAdminLogsSettingsActivity`; the page (Logs with Changes, Reports and Errors; Settings; expandable account rows): `tools/page_check_320.js`.
- **Outgoing mail:** `sendMail` (`cmd/server/mail_2024.go`), through AgentMail or Resend. Never call a mail service anywhere
  else.
- **A campaign's player seats:** `openSeats` / `campaignFull` (`cmd/server/campaign_flow.go`): the campaign list's
  `open_seats` and `?open=`, joining's `campaign_full` and the recruiting check. **The way into a campaign** is `claimSeat(tx, ...)`
  (`cmd/server/schedule_2024.go`): the campaign's row locked, the owner, one character per player and the capacity checked, the
  lowest free seat taken (`campaign_seats`, join order), in one transaction, so parallel joins for the last seat can't both win.
  Joining, the admin's add, restore and editor (`lobby_id`) and the GM's recreate call it. **Leaving** is `leaveCampaign`
  (`campaign_leave_2024.go`): out of a fight, pending turns withdrawn, `lobby_id` NULL (a seat is held only while its character is in the
  campaign); the player's `POST /api/campaigns/{id}/leave` and the GM's kick call it (feedback log 57a). `TestNoOtherWayIntoACampaign` checks.
- **A campaign's level range and a start above level 1** (Matt, Nov 16): `cmd/server/campaign_levels_2024.go`. `newLevelRange` is the
  one rule of a range the GM sets (creation, from a template's range or 1-1; `PATCH /api/campaigns/{id}/levels`); `levelMayJoin` the
  one rule of who may join by level (within min_level to max_level, or a new character below it), read by joining and
  `GET /api/campaigns?level=N`; `characterHasPlayed` (a record in a campaign's log, through `readActions` with `inCampaign`) is what
  "new" means. **`startAtLevel` is the one way a character starts above its level outside play** (`applyLevelGain` and the Starting
  Equipment at Higher Levels money of `higherLevelStart`, paid once per tier: `starting_money`): the player's
  `POST /api/characters/{id}/starting-level` (in no campaign, not played; the optional setup step `startingLevelSetupStep`), joining a
  campaign that starts higher (`startAtCampaignLevel`) and the GM's `POST /api/gm/starting-level` call it. `levelRangeView` is how
  every reply describes the range; `levelRangeAdvice` is the GM status reminder to raise min_level once the whole party has passed
  it. Go: `TestLevelMayJoin`, `TestPostgresCampaignLevelRange`, `TestPostgresStartingLevel`.
- **The turn schedule** (Matt's `campaign-scheduling-directions.md`; on for every server: a new campaign uses it from creation, an
  older one plays as before until its GM turns it on): `scheduleOf(capacity)` is the times (1 to 6
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
  a player's turn itself (combat/next, narrate's `advance_turn`: never a submitted turn, never before the final check-in;
  combat/skip never ends a player's turn with the schedule: `player_turn_not_skipped`, Matt 11.11); `turn_results` records how each turn ended, once (UNIQUE), written only by `recordResult` (the turn-end hook
  `recordTurnResult`, and stage E's resolve and stand-in; `turnHow.result` names how; `checkInAt` reads it); the round opens with the fight's round (`scheduleFightRound`) and replies say
  `round_complete`. A player's seat time is said by `seatNote`, the join reply's `your_next_step` by `joinNextStep` (the seat's
  check-in, set a few minutes early: `checkEarly`, also in `checkInNote`; feedback log 12, 37). `TestNoOtherTurnResultWriter` and `TestOnlyTheGMsRequestsRunTheWalk` check. **Stage E, rounds outside a fight:** `routeEffects`
  (`cmd/server/round_effects_2024.go`) is the one table of every player write route and what it is under the schedule: a game action
  (a submission), gear (setup until the character has played a round, then a submission: Matt 11.9) or immediate with its reason
  (talk, choices, settings, creation, account, a fight's own submission, the GM's requests); `/api/action` is a game action except
  talk (`actionIsImmediate`). `roundGate` (in `withProtections`) is the one place a request becomes a submission (`round_submissions`,
  202, one story action a round); requests the server makes for a player (`internalCall`, `replayAsPlayer`) go straight to the routes.
  A new player write route is one row; `TestEveryPlayerWriteRouteHasAnEffect` fails on a route of the reference without one.
  `noncombat_round_2024.go`: `GET|DELETE /api/campaigns/{id}/round`, `.../round/resolve` (the GM applies each step as the player's own
  request, or rules on it; a turn whose every step is refused stays unresolved) and `.../round/stand-in` (`standInAllowed`);
  `closeRoundIfDone` opens the next round; the campaign's start opens the first (`scheduleCampaignStarts`); `closeOpenRound` withdraws
  a closing round's unresolved submissions. With the schedule the round is the
  exploration beat (`beatApplies`) and the exploration skip gives way to stand-ins. The gate reads the acting character
  with `actingCharacter` (next rule). **Stage F, what everyone is told:** `roundNoteFor` (`check_in_2024.go`), a view of `checkInAt`, is my-turn's
  `round` and the heartbeat's `turn_status.round_status`; `gmReminder` is the GM's next and final check-in in the resolve replies;
  `playerNextCheckIn` the player's in the submission replies. **A round's name** is `roundName` / `roundTitle` ("campaign round 3",
  "fight round 1": the chat line, my-turn's `round.name` and `gm_says_round_name`, the heartbeat, the GM's replies; I-KESTREL-7). **A
  seat's window** is `charStatus.Window` from `checkInAt` (`opens_at`, `closes_at`, `open`, `send_now`: the turn still to send once its
  check-in has come), in the round note as `window`; a `needs_stand_in` status says the turn can still be sent until the GM's next
  check-in (I-ALDRIC-3). Suites: `turn_schedule_rule_test.py`, `initiative_order_test.py`,
  `late_gm_test.py`; suites that test mechanics, not the schedule, turn it off at campaign creation ({"state": "off"}); Go:
  `TestPostgresNoncombatRound`.
- **Before the campaign starts** (Matt, Nov 16): `cmd/server/campaign_ready_2024.go`. While a campaign recruits, players ask questions in
  the party chat, change their characters (joining with a new character takes the old one's seat: `claimSeat`'s `SwapBeforeStart` →
  `swapSeat`, schedule_2024.go) and say they are ready (`POST /api/campaigns/{id}/ready`, written by `setSeatReady` on
  `campaign_seats.ready_at`, cleared whenever a seat changes hands). The GM says it is ready with the same request once its
  preparation and any session zero are done (`setGMReady`, `lobbies.gm_ready_at`; `gmReady`); the start refuses `gm_not_ready` first. `campaignReadiness` / `readinessView` is the one answer to who is
  ready (with each player's `idle_hours`, from the log, the party chat and joining); `startWaitsOn` refuses the GM's start (409
  `players_not_ready`) until every player is, but for the idle players the GM names in `start_without` (logged `started_without`;
  guidance: only for a player who joined and went idle). Removing a player (`kick-character`) is guidance too: a last resort. A full party no longer starts the
  campaign by itself (`reconcileCampaignStatus`). `beforeStartGuide` is the one text that explains it: the creation and join replies,
  my-turn's `campaign_not_started`, the heartbeat, GM status and the campaign's `before_start`. Go: `TestPostgresPlayersReadyBeforeStart`.
- **What the GM is waiting on a player for** (playtest S-GM-12, Matt Nov 16): `cmd/server/gm_asks_2024.go`. The GM pins one ask per
  character (`POST|DELETE|GET /api/campaigns/{id}/asks`); `setGMAsk` / `clearGMAsk` are the only writers of `gm_asks`, `gmAsksIn` the
  one reader. An ask is answered when the player has posted in the party chat or said ready since it was set (worked out when read).
  `playerAskView` is the player's `gm_asks_you` (my-turn, also before the start, and the heartbeat; it stays, with `you_replied_at`, until
  the GM clears it: the GM runs the game, feedback log 57c), `gmAsksTask`
  the GM status line and `player_asks`. Go: `TestPostgresGMAsks`.
- **When a player last looked at a campaign, and its check-in** (feedback log entries 34–36, Nov 16): `cmd/server/check_in_watch_2024.go`.
  `lastCheckedIn` is the one answer (the player's requests tied to the campaign in `api_logs`, its character's action-log records, its
  party-chat messages; the heartbeat isn't tied to a campaign): `campaignReadiness`'s idle hours and `checkInWatch` (the heartbeat's
  `your_check_in`: the seat's minute, `last_checked`, `missed` while the turn is still to send) read it. `postServerMessage` is the only
  writer of a party-chat line with no agent (`openRound` posts `roundOpenedLine` for every round that opens: feedback log 35, 56); every chat reader takes NULL agent columns.
  A submitted turn's `resolves_at` is the GM's next check-in, after the round's final check-in too (`checkInAt`); `roundNoteFor` gives
  `opened_at` and `resolves_at`. A turn counts for a GM check-in only if it was in by that check-in's time (`firstAtOrAfter` takes the exact
  time; "the check-in of this minute" passes `truncMinute(now)`; feedback log 41). GM status's `player_activity` counts from
  `campaignReadiness`'s `last_active`, the same answer as the readiness view (feedback log 39). my-turn's `gm_says_at` / `gm_says_round`
  (`roundAtTime`) say when and in which round the GM narrated (feedback log 56). **The heartbeat is a digest** (`heartbeat_digest_2024.go`:
  `heartbeatLevelUp`, `heartbeatRecent`): the newest level-up notice in short with the count, the newest actions and chat lines with their
  totals (feedback log 50). Go: `TestSubmittedTurnNamesTheGMsCheckIn`, `TestPostgresPlayOpenedAndCheckInWatch`.
- **A class's Primary Ability** (`classPrimary`, a Fighter's is Strength or Dexterity): `primaryAbilityWarning` is the one check of a
  character's scores against it (the builder's review and the setup warnings), `primaryByScore` orders the suggestion of an Ability
  Score Improvement (playtest I-ALDEN-8).
- **The action log's bookkeeping records** (not play: polls, joining, leaving, following, ready, the GM opening play, level-ups): `notPlayTypes` /
  `notPlaySQL` / `isPlayRecord` (`cmd/server/action_log_2024.go`); every "when did this character last play" query, `characterHasPlayed` and
  my-turn's `since_your_last_action` (I-CALLA-28) use it.
- **The table held before round 1** (a session zero, playtest I-GM-7): `cmd/server/table_hold_2024.go`. `POST /api/campaigns/{id}/start
  {"first_round": "hold"}` starts the campaign with no round open; `tableHeld` is the one answer, `holdTable` / `releaseTable` the only
  writers of `lobbies.table_held`. While held the round gate refuses a player's game action (`refuseWhileHeld`: 409 `table_held`,
  nothing spent or saved); talk, the party chat, setup and gear go on. my-turn (`table_held`, `is_my_turn` false), the heartbeat,
  `checkInAt`'s message, GM status (`tableHeldGMTask`), the campaign's view and the exploration skip read it. The GM opens play with
  `POST /api/campaigns/{id}/round/open` (`openPlayRequest` names it in the held start's `open_play` and the GM task; `handleRoundOpen`: releases, then round 1 opens with the schedule); a fight's start
  (`scheduleFightStarts`) releases it too. Go: `TestPostgresTableHeldBeforeRoundOne`, `TestPostgresFightOpensHeldTable`.
- **Which character an agent is playing:** `actingCharacter` (`cmd/server/acting_character_2024.go`): the character named by
  `character_id` / `campaign_id` (the body first, then the query string), else the agent's character in its newest active campaign.
  POST /api/action, the round gate and its reply notes, readied actions, GET /api/my-turn, observations and pending turns read it;
  `answerNoActiveGame` is the one `no_active_game` refusal (409; my-turn 200 as a status report). `TestNoOtherActingCharacterQuery`
  checks; Go: `TestPostgresActingCharacter` (HANDOFF item 22).
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
  `/api/action`, a pending turn and a companion's action; `readiedAction` reads what a Ready holds. Something the server doesn't
  resolve is sent as the action it is with a description; a turn in the player's own words that fits none is `declare` (aliases
  `other`, `free`; Matt, Nov 16): described for the GM, it takes the action in a fight (`actionCostForms`: "bonus action" for the
  Bonus Action), the turn's story action outside one, and `declarationProblem` (`declarationCheck`, from the table) refuses one that
  casts a spell or uses a feature the character has or its resource (400 `declaration_has_its_own_action`; also in a pending turn's
  steps and the round gate). my-turn's options (`turnOptions`), the turn
  reminder, `featureAction`, Gaseous Form and Antimagic Field limits, `/commands` (`actionTypesHTML`), the API reference
  (`tools/build_swagger.py` reads the file) and skill.md's action list (between its action markers; `ACTIONS_UPDATE=1 go test
  ./cmd/server -run TestActionTypesInSkillMd` rewrites it) are made from it. A new action is one row (with its handler).
  `TestActionDispatchIsTheTable`, `TestActionTypesAreDocumented`, `TestCommandsAndReferenceListTheTable` and
  `TestNoOtherActionWordList` check; suite: `action_types_rule_test.py` (HANDOFF items 19, 29). **Who has a type** is said once (Who step 2, Matt Nov 16): the table's
  `Who` / `While`, read by `actionType.refusal` (and `openTo`, which is "no refusal") through `actorFacts` only. Every feature's
  resolver asks `actionRefusal(name, charID)` before anything else (a companion's own action: `companionRefusal(name, c)`), and the
  refusal (`whoRefusal`: a code per condition, the words "Cannot use <feature>: ...", so a refused use spends nothing and no
  exploration beat) is made from the same row. The 61 hand-written gates (`feature_gates_2024.go`) are gone; step 1's comparison had
  shown they agreed with the table for every class at every level 1-20, every SRD subclass and every feat, invocation, item, effect,
  companion, choice and state. A new limited feature is one row plus `actionRefusal` in its resolver;
  `TestEveryLimitedActionAsksTheTable` fails on a limited type no resolver asks, `TestWhoRefusalCodes` checks each kind of refusal,
  `TestWhoFeatsAreRealFeats` that every feat the table names is in `availableFeats`, `TestPostgresFeatActionsAskTheTable` a Boon's
  action through the server.
- **A readied action** (the Ready action): `cmd/server/readied_2024.go`. `holdReadied` keeps what the Ready holds with the held
  request's own fields (spell_slug, target_id ...); `fireReadied` runs it through POST /api/action itself (attack rolls and saves
  as on the turn) with the Reaction (`readiedFiring`, cost `readied_reaction`), for the player's `POST /api/trigger-readied` and the
  GM's `POST /api/gm/trigger-readied`; it ends at the start of the character's next turn (`endUntilNextTurn`) and with the fight
  (`clearCombatInitiative` → `endUntilNextTurnIn`). `readiedOf` is the one reader (my-turn). Go: `TestPostgresReadiedActionRunsAsTheAction`
  (playtest I-CALLA-10, 11, 12).
- **What players read of the log:** `actionRecord.view` (feeds) and `actionRecord.eventLine` (my-turn's `recent_events`, oldest first,
  and `since_your_last_action`) run the description and the result through `playerText`, which also hides the DC of a check the GM
  calls for; a GM's monster adjustment logs how the monster looks (`healthWord`), never its numbers (`gmOnlyChange`); `writeAction`
  never writes a character's refused request; `readActions` leaves out the my-turn polls unless a reader asks `withPolls` (only
  `lastCheckedIn`: a read is not play, I-CALLA-19); a fight's end is a `combat_end` record (my-turn's `fight_over` until the GM narrates).
  Go: `TestPostgresPlayersReadTheLogWithoutHiddenNumbers` (I-CALLA-6, 7, 13, 14, 16, 17, 18).
- **Items a character no longer carries but left somewhere:** `dropToGround` (`cmd/server/dropped_items.go`, through the inventory
  writer) with `place`: the player's drop ("" = on the ground) and the GM's `POST /api/gm/take-item` (narration used the item: it stays
  `where` the story left it, `dropped_items.left_where`). `droppedByCharacter` is the one reader (an item the GM claimed off the ground stays listed, `gone` with the GM's note, `was_left` instead of `where`, and no pick-up; a claim or return is logged by `logGroundChange`: I-CALLA-20); my-turn's `left_behind`
  (`leftBehindNote`; also the after-battle reminder) reminds the player. **A pick-up is immediate; the GM can veto it** (`.../ground/{id}/veto`: back where it was, through
  `dropToGround`); `recentPickUps` lists the last day's (GM status, the ground list). Go: `TestPostgresGMTakeItemLeavesItWhereTheStoryPutIt` (S-CALLA-9).
- **How a feature says it refuses** (feature action sweep, Nov 22): `featureRefusal` (`cmd/server/action_types_2024.go`) makes "Cannot use <feature>: <why>",
  the one form `isRefusalResult` reads: only it spends nothing in a fight, uses no exploration beat and is answered 409 `action_refused`; the table's
  own refusals (`whoRefusal`) are made by it too. A resolver never words a refusal itself (Action Surge, Second Wind, Steady Aim, Superior Defense,
  Magical Cunning, Improve Fate and Rage's Heavy armor were played as actions: 200 success, the cost spent).
  `TestFeatureRefusalsUseTheRefusalForm` (go/ast: a returned text that reads as a refusal in another form); the sweep
  `TestPostgresEveryFeatureActionResolves` (every class at levels 1, 5, 11, 17 and 20 sends every type the table opens to it, then sends it again in
  the same turn; the refusal list of an unknown word equals `actionTypesOpenTo`; `FEATURE_ACTIONS_OUT=file` writes its table). Not done: a resolver
  returning a refusal as a value, not as text (the larger fix: every feature resolver returns text today). `spendText` (`resources_2024.go`) gives every
  refused spend that form too ("Cannot use Natural Recovery: you have no expended level 3 spell slot.").
- **The spell sweep** (player-side audit, stage 3): `TestPostgresEveryClassSpellResolves` (`player_spell_sweep_2024_test.go`): a level 20 caster of each of the 8
  casting classes casts every spell of its class list (`class_spells`) at a monster and at an ally through POST /api/action, costly materials in the
  inventory, a free hand, the Warlock's Mystic Arcanum by its words; it fails on a 5xx, a spell on the list refused as not the character's, a refusal
  not in the refusal form, a cast that changed nothing and said nothing, and a damaging cast that tells damage it did not deal to the ally without an
  attack roll, save or immunity (`SPELL_SWEEP_OUT=file` writes the table). Two rules came of it: **darts** (Magic Missile: no attack roll, no save)
  go to characters as to monsters, shared round-robin by `dartShares` (`spell_resolution.go`; `dartsOnCharacters` in `spell_effects.go`, the monster
  cast's `monsterCastDirect` uses the same share), and **a spell whose dice are extra damage on the caster's later hits** (Divine Smite, Searing Smite,
  Shining Smite, Conjure Minor Elementals; Hex and Hunter's Mark) is read from its own text, `laterHitDamageRe` / `castDamageOnLaterHits`
  (`class_helpers.go`), so the cast deals none and, where no owner adds the extra damage (`hitRiderRules`, a buff's effect row), says the server
  doesn't (`laterHitDamageNote`). Not done: an owner that adds the smites' and the Elementals' extra damage to the next hit (a one-hit rider marker
  in `hitRiderRules`: a new rule, owner's decision). Go: `TestPostgresMagicMissileAtCharacters`, `TestLaterHitDamageSpellsAreReadFromTheirText`.
- **The pages at a phone's width:** `tools/page_check_320.js` (Playwright with the installed Chromium; `BASE`, `ADMIN_PATH`, `ADMIN_KEY`): the five
  views, their linked pages and the signed-in admin page at 320 px: no horizontal scroll, no text under 12 px, no control under 40 px high. The admin
  page's own stylesheet (`admin_page_2024.go`) had 36 px inputs, 20 px chip buttons and a card 415 px wide; fixed there once.
- **How to act (`GET /api/my-turn`'s `how_to_act`):** `actionHowTo` (`cmd/server/actions.go`), made from `actionRequest`'s
  fields and the character's real enemies.
- **The equipped hands:** `changeHands` (`cmd/server/equipped_weapons.go`) is the one writer of `equipped_main_hand` /
  `equipped_off_hand`: it runs the change, then `syncHeldItems`, so the inventory's "held" marks, the shop and the sheet agree
  (playtest I-ROWAN-7). **How many hands a change needs** is `handsAllow` (`hands.go`); its refusal names what fills the hands and
  the request that frees one, from `freeHandRequests` (the held-item refusal uses it too; feedback log 30). The inventory writer
  (`changeInventoryIn`) also runs `syncHands` on every save, so a path that writes the hands another way (creation's row) can't leave
  a weapon "held" in an empty hand; `repairHeldMarks` (at each start, `seedAndLoad`) brings a character saved before that sync in
  line. `TestNoOtherHandWriter` and `TestPostgresHeldWeaponViewsAgree` check.
- **The character an action is aimed at:** `aimedCharacter` (`cmd/server/actions.go`): the request's `target_character_id` or positive
  `target_id` (stored for every action in `aimedTargets` by handleAction), else a character named in the description; every feature that
  heals or picks a character reads it (feedback log 44; `TestPostgresDivineSparkHealsTheNamedAlly`).
- **The skill of a Search, Study or Influence:** `checkSkillFor` (`actions_checks_2024.go`): the request's `skill` (from that action's table;
  another is refused), else a skill named in the description, else the first keyword, else the default; the reply says how, and the GM may
  rule another fits (feedback log 45, Matt Nov 16).
- **A skill check's bonus:** `checkBonus` (`cmd/server/actions_checks_2024.go`): the ability modifier, proficiency or Expertise, Jack
  of All Trades, the Thaumaturge or Magician bonus (`orderSkillBonus`), items and Wild Shape. The player's checks, the GM's skill
  checks (`gm_checks.go`) and the sheet's `skill_bonuses` (`skillBonuses`, which the character page shows and its passives use)
  read it (playtest I-ALDEN-3).
- **Is a character finished:** `characterToFinish` (the open choices of `choiceDefs`): the sheet's and `GET /setup`'s
  `character_finished` are both it; `setup`'s `steps_left` also counts the guided steps after it (a weapon in hand),
  which can wait; the shop (asked as a question: shop for more equipment, or answer no with `{"done": true}`) and a higher starting level are
  optional (`setupStep.Optional`, `optional_steps_left`, `next_optional`) and the portrait is asked, not optional (`setupStep.Asked`, `asked_steps_left`, `next_asked`): neither
  counts in `steps_left`, readiness or joining (`setupStep.holdsBack` is the one test), and `finishRule` / `askedStepRule` (`optional_steps_2024.go`) say it once for the start
  text, the command index, skill.md and the setup reply (`TestPostgresPortraitIsAskedNotOptional`, `TestNoTextCallsThePortraitOptional`; Matt, Nov 16, playtest I-ROWAN-2; owner, Nov 23). Ready refuses while `characterToFinish` has open choices
  (`setup_not_finished`; optional steps never: feedback log 49). A step whose options another open choice changes waits for it (`choiceDef.waitsFor`, the step's `waits_for`, no suggestion until then: the spells step waits for a subclass that gives always-prepared spells, I-CALLA-21). A counted choice's step title says how many are left (`countedTitle`, from
  `choiceDefs`' counts; feedback log 51). **The highest spell level a character can prepare** is `maxSpellLevelFor` (its
  slots): the setup spells step, Magical Discoveries and the preparation check read it (playtest I-ALDEN-4).
- **Can a player send its turn now, outside a fight, with the schedule:** `roundTurnOpen` through `roundNoteFor`'s `your_turn`:
  my-turn's `is_my_turn` and the heartbeat's `your_turn` read it (playtest I-ALDEN-5). **A character seated in a campaign that
  hasn't started** is `actingCharacter`'s `waitingToStart` (a no-row error), answered `campaign_not_started` by
  `answerNoActiveGame` (playtest I-ROWAN-1).
- **Carried weight:** `inventoryWeight` and `itemUnitWeight` (`cmd/server/srd_gear.go`). **Carrying Capacity:** `carryingCapacity` (size and
  Powerful Build) with `carryingOf` (`character_gear.go`): GET /characters/encumbrance and the sheet's `carrying`, which the character
  page shows (it computes nothing itself). **A weapon is stored** with
  `setWeapon` (one property spelling); `hasProperty` is the one property check.
- **Whether a player's battle map view works:** `encounterMap.playerToken` (`battle_map.go`: `no_battle_map`, `no_character_token`):
  the player's GET, preview and move refuse with it (words from `playerMapReasons`), and my-turn gives `battle_map_url` or
  `battle_map_unavailable` from it (`playerMapView`; I-ALDRIC-2).
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
- **Whether a monster action the GM sends deals its damage:** `damageApplies` (`monster_attacks.go`): yes unless the request says
  `"apply_damage": false` (narrate's `monster_action`, a Legendary Action); a hit's own saving throw is rolled by `parseSaveEffect` /
  `applySaveEffect` (feedback log 62, I-GM-19; `TestPostgresMonsterHitAppliesByDefault`).
- **A bonus a spell's damage gets once** (Empowered Evocation, Elemental Affinity: "one damage roll of that spell"): `spellOneRollBonus`
  (`cmd/server/spell_one_roll_2024.go`) is the one reader. The cast text adds it to the damage it shows; a spell of separate beams or darts
  (Magic Missile, Scorching Ray, Eldritch Blast) gives it to the first that hits (`firstHit`) and says so on that line, at a monster and at
  characters; the GM's area cast reads it too. Go: `TestPostgresEmpoweredEvocationOnEveryRoute`.
- **A fight started before anyone is in it** (the GM mounts monsters first): an active fight with an empty turn order is valid; combat/start
  answers 200 with no current turn, combat/add fills it and the highest Initiative takes the first turn. Go: `TestPostgresFightStartedWithNoCharacters`.
- **The spell file's derived fields** (`srd521_spells.json`): a `saving_throw` is a save the target makes against the spell, never a save the spell
  gives Advantage on; a summoned creature's action keeps its whole name. `TestSpellFileFieldsSayWhatTheTextSays`. **The built-in spells are the SRD 5.2.1 as it
  is** (owner, Nov 23: the Player's Handbook corrections to Animal Shapes, Telekinesis, Flaming Sphere and Conjure Minor Elementals are gone);
  only a private server's local content folder changes a spell (`local_content.go`). `TestSpellsAreTheSRDAsItIs`.
- **The agent is the player and plays on its own** (owner, Nov 23: agents asked their human whether to shop, which weapon to hold, a portrait and when to schedule
  check-ins, and waited for "permission"): `youPlayNote` and `humanLimits` (`cmd/server/start_2024.go`) are the one definition: every choice in the game is the
  agent's, its human's instructions come first when they give any, and when there is something it cannot do itself it asks its human for help (an image for the portrait,
  a scheduler that wakes it, an email inbox, somewhere to keep its token are examples, never a list to stay within). `startIntro` (both are in it) opens the start in `startText`, `startHTML` and `startJSON`
  (`you_play`, `ask_your_human_only_for`); the recurring check is set up by the agent itself (`checkWaker`, `setUpChecks`: `checkInNote`, the join and creation replies in
  `schedule_2024.go`). A new text never tells the agent to wait for its human's permission or to ask for a choice that is its own:
  `TestNoTextSendsTheAgentToItsHumanForWhatItCanDo` scans every string literal, godoc description and guide for permission-waiting and for sending the agent to its human to make its own choice (it does not require a reason, so asking for help with a new limit is never refused); `TestTheStartSaysTheAgentPlaysOnItsOwn`.
- **The functional spell audit's rules** (owner, Nov 23, FUNC-01 to 12; the tester's notes in the test folder): `chosen_damage_type_2024.go` (a spell whose damage is "of a type you choose" is typed by the description, refused with the list
  when none is named: FUNC-07); `damagePart.OnMiss` (a spell attack that misses still deals the part's half: Acid Arrow), `partRoll` (the parts a cast shows are the parts it deals, rolled once, at monsters and characters alike: FUNC-02) and
  `isHybridSpell` (a spell attack followed by a save, hit or miss: Ice Knife, FUNC-05), all in `spell_parts.go` and the two spell resolvers; `casterHealing` (`spell_healing_2024.go`: the one amount of a healing spell, Supreme
  Healing and Disciple of Life included, Regenerate too: FUNC-04); `drainHeal` (`spell_drain_2024.go`: the caster regains half of the damage dealt, Vampiric Touch: FUNC-06); `laterHitDamageRe` (Bestow Curse's extra damage rides on
  later hits: FUNC-09); one repeated saving throw for the conditions one cast gave (`groupSave`, `condition_turns.go`: FUNC-08); `lastingSpellTargets` (`buff_spells_2024.go`: who a lasting spell lands on; a creature of the encounter
  the request names is the target or the cast says the GM applies it, never silently the caster: FUNC-11); `wardsHeld` / `endHeldWards` (a cast that makes Invisibility or Sanctuary keeps it: FUNC-03); `withoutRepeats` (a creature named
  by both `target_id` and `target_ids` is one target: FUNC-10); `killInstantly` (`hit_points_2024.go`: an effect that kills without damage is negated by Death Ward: FUNC-12). Go: `spell_families_2024_test.go`,
  `chosen_damage_type_2024_test.go`, `spell_wards_targets_2024_test.go`.
  The second notes (Nov 23): **a damaging spell's own condition** is read from its text, `castConditionOf` / `applyCastCondition` (`spell_cast_conditions_2024.go`: on a hit or a failed save,
  its duration from the text through the rider duration owner, its repeated save; a character's cast at a monster or a character, and a monster's cast through `saveEffect`; Sunburst, Sunbeam,
  Contagion, Black Tentacles, Weird, Storm of Vengeance, Ray of Sickness on a hit); **a spell rider lasts as its text says** (`spellRiderEnds`, `addCombatEffectUntil` with `combat_effects.ends`,
  ended by `expireCombatEffectsOf` at a turn start and `expireCombatEffectsAtTurnEnd`, a turn-end hook); **a lasting spell aimed at a creature only characters' effects reach** is refused before
  anything is spent (`lastingKindOf`, `lastingTargetRefusal`; Bless, Shield of Faith, Haste, Heroism, Resistance and Guidance take `lastingSpellTargets` too); **Befuddlement** is the `befuddled`
  record (`applyCastMarker`, `cannotCastSpells` for a character's cast and `monsterCastBlocked`, `endBefuddlementBySpell`); **Glibness** is the check owner's floor (`checkWhy.Floor`, `floored`).
  Go: `spell_batch2_2024_test.go`.
- **What a pick in the character builder brings with it** (agent report, Nov 23; `cmd/server/builder_followups_2024.go`): a background's or a species' follow-up question is named on its option before it is picked
  (`followUpOfBackground`, `followUpOfSpecies`, from `featFollowUps` and the species' Needs, with the fields to send in one request); the class skills a background gives are marked in the class step before
  a background is chosen (`skillSources`, `given_by_backgrounds`) and on each background's option (`classSkillsUsedBy`, `uses_up_class_skills`); a request that raises `choices_remaining` says why in
  the same reply (`countRoseText`, `choices_remaining_rose`). The builder keeps its any-order flow (owner, Nov 23: wording and option data only). Go: `TestPostgresBuilderOptionsNameTheirFollowUps`,
  `TestPostgresBuilderCountRiseIsExplained`.
- **A draft's one update and the guide's short view** (owner, Nov 23, an agent's builder report): `updateDraft` (`character_builder_2024.go`) is the one update of a draft, for
  POST /api/character-builder/{id} and the fields `/finish` takes (it creates the character in the same request once nothing is left); `guideView` / `shortenGuide` /
  `answerGuideView` (`guide_short_view_2024.go`) give the builder's and setup's replies their view: full first (a draft's start; setup's first read, `setup_full_seen`), short after,
  `?view=full` / `?view=short`. A suggested species never dead-ends its skill choice (`draftHeldSkills`). Go: `TestPostgresFinishTakesTheWholeDraftForEveryClassAndBackground`,
  `TestPostgresFinishFieldsAndGuideViews`.
- **The portrait is answered, never skipped** (owner, Nov 23: agents skipped it every time; **the starting shop is left as it was**, owner's word):
  `cmd/server/optional_steps_2024.go`. The portrait never holds a character back (not in `steps_left`, `ready` or joining: Matt, Nov 16), but it ends only with an
  image or an answer on the record: the agent's hand-off to its human (`{"ask_my_human": true}`: the step stays open and the reply has the words to pass on,
  with the whole image prompt in them so the human has only to paste it and upload the result: `askYourHuman`) or the human's own no in their words
  (`{"my_human_declined": "..."}`; `humanWords` refuses the template and one word). `{"skip": true}` is refused (`portrait_not_skipped`).
  `optionalAnswerOf` / `setOptionalAnswer` are the one reader and writer of `characters.optional_answers`; `openOptionalSteps` the one list of what is open;
  `optionalReminders` gives `answer_with_your_human`, set in my-turn (also before the start), the heartbeat's campaign entries and nowhere else.
  The start text and the command index say the shop is optional and the portrait is not skipped. Go: `TestPostgresThePortraitIsAnsweredNotSkipped`,
  `TestNoOtherOptionalAnswerWriter`.
- **The SRD monster catalogue** (owner, Nov 23): `srdMonsterCatalog` (`monsters_2024.go`), the 330 stat blocks; the creatures an item gives
  (Giant Fly, Avatar of Death: `itemCreatureSource`) share the table but are never counted, listed or offered. Every list, count, search and beast
  choice reads it; the Universe card shows its Challenge Rating groups (`crBands`). Go: `TestPostgresMonsterCatalogIsTheSRDStatBlocks`.
- **Monster traits:** `monsterTraitRules` (`cmd/server/monster_traits_2024.go`): each trait the server applies is one row (by
  name, or by wording for auras and Regeneration) saying what it does and where; `monsterHasTrait` reads the loaded stat block;
  the GM's stat block marks each trait as applied by the server or by the GM (`traitsForGM`). A row marked `Aura` acts on its own timing:
  the GM's `automatic_traits` list (`auraTraits`) and the auras switch read those rows (`TestAuraTraitsComeFromTheTable`). A new trait with mechanics is a
  row here plus its rule in the owner it belongs to (an attack source, a save source, a turn hook). A creature's own trait that gives its
  rolls Advantage on a condition (Blood Frenzy, Bloodied Fury, Bloodied Frenzy: while Bloodied, or against a target missing Hit Points;
  melee only or saves too as worded) is `rollTraitOf` / `rollTraitAdvantage`, one row each in `attackSources` and `saveSources`, for
  monsters and companions (`TestRollTraitsOverTheCatalog`, `TestPostgresRollTraitsGiveAdvantage`).
- **What a monster's cast of a spell does** when it is neither a spell attack nor a saving throw: `monster_cast_effects_2024.go`.
  - `castDealsNoDamage` (`class_helpers.go`, read by a character's cast and a monster's: Geas, Dream) says a spell's dice are dealt later or
    never by the cast. Go: `TestPostgresDreamDealsNoDamageOnCast`.
  - `lastingSpellEffect` (`buffSpells` and `gmSpellKinds`) gives the effect's kind, duration and number of targets. A character's
    Bless and Shield of Faith read it too.
  - `castEffectAnyCreature` lists the kinds a monster's cast applies.
  - `spellHealDice` is a healing spell's dice and modifier, for characters and monsters.
  - `monsterSpellcastingMod` / `monsterSpellDC` read a monster's casting ability and DC.
  - `castChoiceList`: an entry that offers several spells waits for the GM's `use`; so does a Spellcasting entry's lists (`spellTiersOf`,
    which keeps what a list adds to a spell, `spellListNote`: "Charm Monster (lasts 24 hours; ...)", shown to the GM with the cast).
  - `endWardsOnOffenseOf`: Invisibility and Sanctuary end when their creature, a character or a monster, attacks or casts.
  - `monsterCastDirect` (`monster_cast_effects_2024.go`): a spell with no attack roll and no saving throw that lands at once is resolved by the owners a character's
    cast uses: Magic Missile's darts (`beamPlan`, round-robin over the targets, the Brooch of Shielding's Immunity), the Power Words (`powerWordOn`) and Remove Curse
    (`endCursesBySpell`). Go: `TestPostgresMonsterCastDirectSpells`. What stays the GM's: utility and passive spells (Detect Magic, Commune, Teleport ...), summoning and
    Dispel Magic, whose effect the GM names; the lasting ones among them are open items.
  - A new spell effect for monsters is a row in those tables plus a kind its readers serve for any creature.
  **A Multiattack's attacks** are read outside its replacement sentence (`withoutReplaceSentence`: an option is never one more attack),
  each name whole and longest first (`wholeNameIndex`: "Claw" is not read inside "Sickening Claw"); "one of the attacks" is any attack and
  an option's name leaves out "if available" however it is written (`withoutIfAvailable`, also for `use`). Go:
  `TestMultiattackPlanReadsTheReplacementApart`, `TestPostgresMultiattackReplacementNeverAddsAnAttack` (GM doc A2, I-ALDEN-PARTY-01).
  **A monster named in a GM request** ("Bandit", "Bandit 2", its key, or "#-3" by its combat id) is `monsterIDsByKeyOrName`, for every
  caller. The adjudication fallback of `resolveMonsterDirectDamage` lists a lasting effect as an open item (`monsterHandoverEntry`; GM doc B2).
  **An entry that can replace part of itself** ("It can replace one attack with a use of Sleep Breath", the Pit Fiend's Hold Monster
  in place of one Fireball): `replaceRuleOf` (`cmd/server/monster_replace_2024.go`) reads the sentence; monster_action's `use` picks
  an option, `replaceRefusal` refuses any other word before anything is rolled, `resolveMultiattackReplacement` / `withoutReplaced`
  make a Multiattack's attacks less the replaced ones and then the option, the spell branch of `resolveMonsterEffectText` casts the
  option in place of one copy, and the GM's stat block lists them (`action_choices`, `replaceChoicesView`). **Narrate runs the
  monster action first:** a refused or unknown one records nothing, the narration included (feedback log 69).
  `TestPostgresReplaceRulesReadEverySRDEntry`, `TestPostgresNarrateRunsTheMonsterActionFirst`.
- **Reading the action log:** `readActions` (`cmd/server/action_log_2024.go`) with `actionFilter`; `actionRecord.view` is the one JSON
  form of a row and `cursorTime` the one way a feed shows its time (every fraction of a second, so `?since=` the last record's
  `created_at` returns only later ones). The feed, spectate, the web feed and log, GM status, my-turn, the nudge and a character's
  profile read through it; every column may be NULL (narration has no character). `TestNoOtherActionLogReader` checks.
  **A list read in pages** (the feed's actions and messages, the party chat) is `logPage` (`readLogPage` reads since / before /
  limit; `readActionPage`, `readMessagePage`, `pageOf`): the newest page by default, before pages back, since reads forward, every
  page oldest first with `more` (playtest I-GM-25: the feed always gave the oldest 100). `TestPostgresFeedPagesFromTheNewest`.
  **Writing it:** `writeAction(db or tx, campaign, character, type, description, result)`, the only INSERT (0 is NULL); whether the character's own player wrote a record is `by_player`, from `requestAuthor` (the request's agent; a request the server makes for an agent, a submission the GM resolves, a pending turn's step, a readied action, a standing order, goes through `serveAs`, the one way), read by `isOwnActionOf` (my-turn's `since_your_last_action`: a GM's record under the character's name is not its own action, I-CALLA-26); a rest a character finishes is written by `logRest` (`rests_2024.go`, both rest routes: I-CALLA-23); **a Long Rest's 24 hours** are story time: `longRestTooSoon` reads the campaign's clock `lobbies.story_rounds` (moved by `passSpellTime` campaign-wide: the GM's pass-time, travel), never the wall clock, and only the GM's own rest request is its ruling and isn't checked (`gmOwnRequest`); a submission the GM resolves or a stand-in runs as the player's and is checked like it (staged test ON3.1; I-CALLA-25); `eventLine` keeps a record's description beside its result for every record, a GM's too (I-CALLA-22);
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
- **Every command is documented, from the code that serves it** (Matt, Nov 16: "All commands should always be documented"). The API
  reference (`/docs`, `/docs/swagger.json`, the route list on `/commands`) is built by `python3 tools/build_swagger.py` from nothing
  each time; it never reads its own earlier output. The words of an operation are its handler's godoc block (`@Summary`,
  `@Description`, `@Param` for query and path, `@Body` for what the body is for, `@Success` for what the reply is, `@Router`) or a
  hand entry in that script for the few routes whose words live there. Everything else is read from the code by `TestAPIShapes`
  (`cmd/server/api_shapes_2024_test.go`, run by the script with `API_SHAPES_OUT`), which follows each route through its handler for
  its method and path: the **request body** is what its reader takes (`readRequest` / `gmRead` / `gmReadAs` / `noFields` /
  `parseRequest`: the type's fields with their comments, `campaign_id` of a GM command, `optionalBody`, the fields the handler
  refuses when missing; a map request's fields from `onlyFields` / `keysCheckedByHandler(names...)` and the tables in
  `dynamicFieldLists`; no reader: "Takes no request body."); the **reply** is every JSON success reply it writes (map keys and value
  types through variables and helpers, the keys `augmentJSON` wrappers add, or the content type of a file). So a request field needs
  a comment where its type declares it, and a new route needs a godoc block directly above the function that serves it; then
  rebuild. Without the variable `TestAPIShapes` fails on any gap: a field, type, required list or reply that differs from the code,
  an operation, parameter or field without a description, a `@Body` on routes that read none, a served prefix with no documented
  route. `finish_operation` (in the script) is the one check of every operation (path parameters declared, a body under content, no
  body on a GET). `TestAPIReference` checks the reference against the router, skill.md and the commands page;
  `TestGodocBlocksSitAboveTheirHandlers` checks the blocks. **An example body** for every request that reads one is made by the
  script from the request type (`example_body`) unless the hand entry gives one; `/commands` shows it under each command and the GM
  guide points there (feedback log 11; `TestEveryRequestBodyHasAnExample`). `/commands` lists every operation of the reference (a tag no `commandGroups` row names goes under Other;
  `TestCommandsPageListsEveryCommand`). **The guides** (`docs/*.md`): `TestDocsNameRealRequests` fails on a
  request they name that the reference doesn't have and on a body field they show that the request doesn't take; **the server's own
  reply words** are held to the same check by `TestServerRepliesNameRealRequests` (every string literal of `cmd/server` that names
  a request; both read `requestChecker`, playtest I-GM-18: GM status named `POST /api/gm/add-monster`, which never existed);
  `TestCheckInNoteInGuides` keeps `checkInNote` between the check-in markers of the heartbeat and experience guides
  (`START_UPDATE=1` rewrites them); `TestDocsTableRuleDefaults` checks the table rule defaults they name. Lists the server defines
  (routes, fields, action types) are generated or pointed to (/commands, /docs), not copied.
- **A character's spell lists as the sheet shows them:** `spellGroupsForSheet` (`cmd/server/prepared_spells_2024.go`) builds
  `spell_groups`: one "Spells Prepared" list (prepared spells and cantrips, plus always-prepared spells from feats, species,
  subclasses and invocations, each with `from`), and "Spellbook" only for a class whose features have the `spellbook` mechanic
  (the Wizard, in the class data). The character page shows `spell_groups`; it never names a list itself.
- **The GM guide:** `docs/gm_guide.md` (`docs.GMGuide`), sent as `gm_guide` when a campaign is created and served at
  `/gm-guide.md`, with this server's address where it says `https://YOUR-SERVER` and the schedule table from `scheduleOf` at
  its marker (`gmGuide`).
- **Every web page's frame and the five views** (the GUI rework, Matt Nov 21: the owner's mock-up
  `data/ui_preview/gui-2026-11-21/index.html`, implemented as it is): `cmd/server/site_layout_2024.go`. `framePage` puts a page into
  `baseHTML` / `pageTemplate`: the mock-up's top bar (brand, `siteNav`'s desktop navigation, the theme select `themeMenu`, the Agent
  Quickstart button), the phones' bottom tab bar with its icons and the footer, all made from `siteNav` (the one list of the five views:
  Live Table `/live`, Campaigns, Character `/character`, Universe, Commands & API; the page being read is marked active by its `Match`
  paths), and the Agent Quickstart modal (`quickstartModal`: its six steps and its cURL are `startQuickSteps` / `startCurl` in
  `start_2024.go`; the whole start text is `/start`). The mock-up's stylesheet is `designCSS` (`site_design_css_2024.go`, generated from
  the mock-up with sizes under 12 px raised); `siteCSS` is only what the real site adds (link colors, the 40 px touch targets, `.prose`
  for text pages, `a.solo-link` for a link that is its block's whole text). The five themes of the mock-up (Obsidian Gold the default,
  Crimson Maroon, Forest Emerald, Royal Sapphire, Parchment) are `siteThemes` in `themes_2024.go` (`designTheme`; the older themes stay
  for saved choices, `legacyTheme`, off the menu). `wrapView` is a view in the mock-up's own markup; `wrapHTML` a text page (`.prose`).
  The views (`site_views_2024.go`, each with the mock-up's sections and classes and real data; what the demo made up, such as Simulate
  Next Turn, is not there; real content the mock-up has no place for comes after its sections in its components): the Live Table
  (`liveTablePage`, a campaign's page `/campaign/{id}`; `/` and `/live` show `featuredCampaign`), its chronicle entries
  (`chronicleEntries`, also the whole chronicle `chroniclePage` at `/campaign/{id}/log`), the Campaign Browser (`campaignBrowserPage`),
  the Character Sheet (`characterViewHTML` and `characterProfileScript`, character_profile.go; `/character` opens the featured
  campaign's party), the Universe Compendium (`universePage` / `universeCategories`) and Commands & API (commands_page.go: every action
  type, `actionTypesHTML`, and every reference operation as the mock-up's cards, under its filter pills All / Player / GM / Campaign,
  `commandGroups`' `Filter`). A page adds only its own CSS; never paste the shared parts into a page. Mobile first: text never under
  12 px, a touch target never under 40 px, no horizontal scroll from 320 px. The design's images are served from `data/site_assets` at
  `/assets/` (`handleSiteAsset`). `classIcon` is a character's token without a portrait, `feedKind` a feed entry's kind.
- **The Campaign Story Reader** (owner, Nov 23; the mock-up `data/ui_preview/story-reader-2026-11-23/index.html`): `cmd/server/story_reader_2024.go`,
  GET `/campaign/{id}/story` (behind the watch password like the chronicle), linked from the chronicle and the campaign cards. **On the Live Table the reader IS the chronicle** (owner, Nov 23: it replaces the old Table Chronicle feed, it is not a button to another page): `storyPanel` puts `storyReaderMarkup` and the reader script in the panel and fetches the story from GET `/campaign/{id}/story.json` (`serveStoryJSON`; `loadStory` is the one reader of the log for both, `storyData` the one shape; the data's `inline` makes scroll mode scroll its own box); the party chat is not in the story, the whole log is still `/campaign/{id}/log`. A human reads
  the log as a book: Page Flip and Scroll, a chapters-and-bookmarks drawer, text size, a dice toggle, a resume banner; its place and bookmarks stay in the
  browser, one set per campaign. The story is made from `readActions`, never a copy: `storyPassages` (which records are story and in whose voice; the party
  chat and `storySkipTypes` are not) and `storyChapters` (a scene: a fight's end, a pause of `storyChapterGap`, or `storyChapterMax` passages; named by when it
  began, as the server stores no chapter names). Read oldest first, so a first visit opens Chapter I (a very long story is cut at its end, never its start). Its stylesheet and script are `data/story_reader_2024.css` / `.js` (the mock-up's own, with the site's
  12 px and 40 px floors and the site's themes in place of its theme buttons; text is escaped in the script). Go: `TestStoryPassagesAndChapters`,
  `TestPostgresStoryReaderPage`.
- **The GUI rework previews** (Matt, Nov 17 and 21): `handleUIPreview` (`cmd/server/ui_preview_2024.go`) serves the saved designs in
  `data/ui_preview/` at `/preview/` (the Nov 21 one at `/preview/gui-2026-11-21/`), frozen data, only where `AGENTRPG_UI_PREVIEW=1`
  (staging); 404 elsewhere. The real pages are built from the layout above.
- **The guide served to agents:** `docs/skill.md`, built into the program by the `docs` package (`docs.Skill`).
- **A monster attack's damage and a Multiattack's plan** (`parseMonsterAttack`, `prepareMonsterAttack`, `multiattackPlan`): every damage amount of a
  Hit is represented or the attack is refused before any roll; a Hit that deals no damage and only gives conditions (`noDamageHit`: Roper
  Tentacle) is a valid attack, its damage step skipped by `hitDamageParts` on every hit route (no Hit Point change, no Concentration save, no
  Death Saving Throw failure; `TestPostgresNoDamageHit`); a Multiattack is capped at 20 attacks and an either/or choice is never
  combined. **Absorption** is `monsterAbsorbs`, read from the trait text. See `docs/MONSTER_SYSTEM.md`.

### Commits (standing rule, owner, Nov 22)
One commit holds one kind of change; never mix kinds in a commit. The kinds: **mechanics** (rules, the game's behaviour), **admin** (the admin app: its routes,
page and logs), **GUI** (the public pages and their layout), **docs**, **tests and tools** that belong to no other change, and **deploy/config**. A change that
needs two kinds is two commits, each on its own passing the gates, in the order they depend on each other (the owner first, then what reads it). A fix to a
test or a tool goes with the change it checks, not with an unrelated one. Say the kind in the commit's first line. Check `git diff --cached --stat` before every
commit: a file of another kind means unstage it.

### Before pushing
`gofmt -l` on the files you changed, `go vet ./cmd/server ./game`, `go test ./...`, then the owner's test scripts for
character creation, leveling and combat if you have them. Do not deploy unless the owner says "deploy".
