# Monster wording patterns added since a61fa12: audit

The goal: a GM imports any monster (homebrew included) and the shared rules handle it or flag it to the GM. The rule for each
pattern: keep it only if its wording appears in 5 or more SRD 5.2.1 stat blocks, or if it only reads a parameter of an existing
generic effect (damage, saving throw, condition with duration or escape, forced movement, score or HP maximum reduction,
recurring damage, an Advantage or Disadvantage source, use limits). Everything else is demoted to a GM flag.

"SRD blocks" is the number of SRD 5.2.1 stat blocks whose text the pattern matches (`cmd/server/data/srd521_monsters.json`,
measured with a go/ast tool over every `regexp.MustCompile` added since a61fa12). 0 usually means Monster Manual wording that the
SRD doesn't have.

Totals over 183 patterns: keep 87, generalize 24 (copies of one rule, to be merged), demote 49, not monster wording 23.

## Demotions I disagree with (waiting for your answer before touching them)

- **Incorporeal Movement** (`incorporealRe`, `insideObjectRe`, 4 SRD blocks): it is the standard wording for every incorporeal
  undead, so homebrew ghosts will use it word for word. I'd keep it.

## Table

| # | File:line | Pattern | SRD blocks | Verdict |
|---|---|---|---|---|
| 1 | `action_types_2024.go:424` | `castingWords` | 71 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 2 | `action_types_2024.go:425` | `slotWords` | 0 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 3 | `attack_2024.go:185` | `woundedTargetAdvantage` | 4 | generalize: duplicate of rollTraitWoundedRe; one Advantage-source reader |
| 4 | `attack_target.go:203` | `absorptionTraitRe` | 4 | keep: generic (damage type -> HP regain); 4 (golems) |
| 5 | `combat_handlers.go:109` | `monsterVisionRe` | 0 | generalize: 3 readers of the Senses line -> one |
| 6 | `condition_lasting_effects_2024.go:30` | `recurringTailRe` | 9 | keep |
| 7 | `condition_lasting_effects_2024.go:60` | `lastingGateRe` | 22 | keep |
| 8 | `condition_lasting_effects_2024.go:61` | `lastingNoRegainRe` | 13 | keep |
| 9 | `condition_lasting_effects_2024.go:62` | `lastingTickRe` | 5 | keep |
| 10 | `condition_lasting_effects_2024.go:63` | `lastingRepeatRe` | 6 | keep |
| 11 | `condition_lasting_effects_2024.go:64` | `lastingJoinRe` | 0 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 12 | `condition_lasting_effects_2024.go:66` | `lastingAlsoRe` | 144 | keep |
| 13 | `condition_lasting_effects_2024.go:68` | `lastingWakeRe` | 2 | generalize: 'ends if it takes damage' read in 3 places -> one duration reader |
| 14 | `creature_movement_2024.go:149` | `chargeSentenceRe` | 16 | generalize: 3 regexes for one charge rule -> one (distance, size gate, effect) |
| 15 | `creature_movement_2024.go:150` | `chargeAltRe` | 15 | generalize: merge into one charge reader |
| 16 | `creature_movement_2024.go:151` | `chargeAnyRe` | 18 | generalize: merge into one charge reader |
| 17 | `creature_movement_2024.go:327` | `hitForcedMoveRe` | 6 | keep |
| 18 | `creature_movement_2024.go:393` | `teleportUpToRe` | 8 | keep |
| 19 | `creature_movement_2024.go:394` | `teleportWithinRe` | 1 | demote: 1 (Dryad) |
| 20 | `creature_movement_2024.go:395` | `moveUpToRe` | 37 | keep |
| 21 | `creature_movement_2024.go:396` | `jumpSpendRe` | 5 | keep |
| 22 | `creature_movement_2024.go:397` | `moveFeetRe` | 0 | generalize: same as moveUpToRe with a number instead of 'its Speed' -> one reader |
| 23 | `creature_movement_2024.go:398` | `noOARe` | 7 | keep |
| 24 | `creature_movement_2024.go:399` | `straightEnemyRe` | 3 | demote: 3, direction constraint |
| 25 | `creature_movement_2024.go:401` | `afterBloodiedHitRe` | 2 | demote: 2 (Rampage trigger) |
| 26 | `creature_movement_2024.go:435` | `speedKindRe` | 2 | keep: reads the stat block's Speed line (a field, not a sentence) |
| 27 | `creature_movement_2024.go:596` | `noDragCostRe` | 2 | demote: 2 |
| 28 | `creature_movement_2024.go:634` | `fallsRe` | 0 | demote: 0 |
| 29 | `creature_movement_2024.go:635` | `fallReduceRe` | 0 | demote: 0 |
| 30 | `creature_senses_2024.go:20` | `devilsSightRe` | 0 | demote: 0 |
| 31 | `creature_senses_2024.go:23` | `sunlightRe` | 6 | keep |
| 32 | `disadvantage_riders_2024.go:32` | `exposedRe` | 0 | keep: generic (Advantage source + duration) |
| 33 | `disadvantage_riders_2024.go:36` | `noReactRe` | 6 | keep |
| 34 | `disadvantage_riders_2024.go:37` | `abilityNameList` | - | n/a: not stat-block wording (grammar, storage or UI helper) |
| 35 | `gm_view.go:137` | `revealsStats` | 181 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 36 | `hide_2024.go:75` | `hideLightGateRe` | 1 | demote: 1 (Shadow Stealth gate) |
| 37 | `hit_after_damage_2024.go:25` | `abilityDecreaseRe` | 1 | keep: generic (score reduction); reads ability + dice |
| 38 | `hit_after_damage_2024.go:63` | `burnOnHitRe` | 4 | keep: generic (Burning = recurring damage, SRD rules glossary) |
| 39 | `hp_max_drain_2024.go:20` | `hpMaxDrainRe` | 7 | generalize: two regexes for one drain -> one |
| 40 | `hp_max_drain_2024.go:24` | `hpMaxDrainSentenceRe` | 7 | generalize: merge with hpMaxDrainRe |
| 41 | `hp_max_drain_2024.go:27` | `drainRegainRe` | 2 | keep: generic (HP regain = the drain's amount), parameter of the drain |
| 42 | `item_state_2024.go:31` | `armorOnHitRe` | 2 | demote: 2 (item corrosion state) |
| 43 | `item_state_2024.go:32` | `objectOnFailureRe` | 1 | demote: 1 |
| 44 | `item_state_2024.go:33` | `corrosiveWeaponRe` | 2 | demote: 2 |
| 45 | `item_state_2024.go:34` | `corrosiveAmmoRe` | 2 | demote: 2 |
| 46 | `item_state_2024.go:35` | `metalObjectOnlyRe` | 1 | demote: 1 |
| 47 | `item_state_2024.go:164` | `mendingRemovesRe` | 3 | demote: 3 |
| 48 | `item_state_2024.go:259` | `stuckLeadRe` | 0 | demote: 0 (Kuo-toa, not SRD) |
| 49 | `item_state_2024.go:260` | `stuckGrappleRe` | 0 | demote: 0 |
| 50 | `item_state_2024.go:261` | `stuckNoUseRe` | 0 | demote: 0 |
| 51 | `item_state_2024.go:262` | `stuckFreeRe` | 0 | demote: 0 |
| 52 | `map_spaces_2024.go:82` | `swarmSpaceRe` | 7 | keep |
| 53 | `map_spaces_2024.go:83` | `incorporealRe` | 4 | demote: 4 (Incorporeal Movement) |
| 54 | `map_spaces_2024.go:84` | `insideObjectRe` | 4 | demote: 4 (goes with Incorporeal Movement) |
| 55 | `monster_attach_2024.go:35` | `attachOnHitRe` | 2 | demote: 2 (attach state) |
| 56 | `monster_attach_2024.go:43` | `whileAttachedOnlyAdvRe` | 1 | demote: 1 |
| 57 | `monster_attach_2024.go:128` | `detachAutoRe` | 1 | demote: 1 |
| 58 | `monster_attach_2024.go:129` | `detachCheckRe` | 1 | demote: 1 |
| 59 | `monster_attach_2024.go:165` | `attachTieRe` | 3 | demote: 3 |
| 60 | `monster_attach_2024.go:191` | `attachOwnedFromRowRe` | 0 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 61 | `monster_attack_lockout_2024.go:16` | `attackLockoutRe` | 1 | demote: 1 |
| 62 | `monster_attack_lockout_2024.go:21` | `attackLockoutTailRe` | 0 | demote: 0 |
| 63 | `monster_attack_lockout_2024.go:26` | `swallowLockoutRe` | 1 | demote: 1 |
| 64 | `monster_attack_lockout_2024.go:30` | `grappleLockoutTextRe` | 0 | demote: 0 |
| 65 | `monster_attacks.go:47` | `anyCombinationRe` | 36 | keep |
| 66 | `monster_attacks.go:48` | `attackChoiceRe` | 220 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 67 | `monster_attacks.go:49` | `multiNumberRe` | 0 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 68 | `monster_attacks.go:128` | `anyCombinationSegRe` | 36 | keep |
| 69 | `monster_attacks.go:131` | `replaceAttackRe` | 28 | generalize: 3 replace readers (monster_attacks + monster_replace) -> replaceSentenceRe only |
| 70 | `monster_attacks.go:409` | `replaceAttackSentenceRe` | 28 | generalize: merge into replaceSentenceRe |
| 71 | `monster_attacks.go:436` | `meleeRe` | 328 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 72 | `monster_cast_effects_2024.go:80` | `castAbilityRe` | 64 | keep |
| 73 | `monster_cast_effects_2024.go:81` | `healUpToRe` | 4 | keep: generic (number of targets) |
| 74 | `monster_condition_traits_2024.go:19` | `conditionTriggerRe` | 0 | demote: 0 (Prone Deficiency, not SRD) |
| 75 | `monster_condition_traits_2024.go:52` | `blurredFormRe` | 1 | keep: generic (Disadvantage source); generalize the subject to any name |
| 76 | `monster_damage_choice_2024.go:52` | `chosenAttackDamageRe` | 0 | keep: damage parameter (type chosen) |
| 77 | `monster_damage_choice_2024.go:55` | `chosenInlineRe` | 0 | keep: damage parameter (type list) |
| 78 | `monster_damage_choice_2024.go:56` | `validAttackDiceRe` | 0 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 79 | `monster_damage_choice_2024.go:212` | `atkBonusRe` | 328 | keep |
| 80 | `monster_damage_choice_2024.go:213` | `hitDmgRe` | 308 | keep |
| 81 | `monster_damage_choice_2024.go:214` | `flatHitRe` | 21 | keep |
| 82 | `monster_damage_choice_2024.go:216` | `damageClauseRe` | 328 | keep |
| 83 | `monster_damage_choice_2024.go:217` | `damageWordRe` | 328 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 84 | `monster_damage_choice_2024.go:218` | `hitSentenceEnd` | 330 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 85 | `monster_damage_choice_2024.go:219` | `damageJoinRe` | 1 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 86 | `monster_damage_choice_2024.go:220` | `advantageOnlyRe` | 0 | keep: damage parameter (alternative with Advantage) |
| 87 | `monster_damage_choice_2024.go:221` | `alternativeHeadRe` | 0 | keep: damage parameter (alternative) |
| 88 | `monster_damage_choice_2024.go:223` | `twoHandedAltRe` | 0 | keep: damage parameter (two-handed alternative) |
| 89 | `monster_damage_choice_2024.go:226` | `damageConditionRe` | 234 | keep |
| 90 | `monster_damage_choice_2024.go:482` | `companionDamageChoiceRE` | 0 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 91 | `monster_damage_choice_2024.go:542` | `subjectedLeadRe` | 12 | keep |
| 92 | `monster_damage_choice_2024.go:609` | `damageBackRefRe` | 6 | keep |
| 93 | `monster_damage_choice_2024.go:629` | `chosenTypeExtraRe` | 1 | demote: 1 (Half-Dragon) |
| 94 | `monster_damage_traits_2024.go:11` | `damageDisadvantageRe` | 1 | demote: 1 (Aversion to Fire) |
| 95 | `monster_darkness_2024.go:22` | `magicalDarknessRe` | 1 | demote: 1 (Darkmantle) |
| 96 | `monster_darkness_2024.go:103` | `magicalDarknessSenseRe` | 0 | generalize: senses -> one reader |
| 97 | `monster_day_uses.go:24` | `spellListHeadRe` | 48 | keep |
| 98 | `monster_day_uses.go:25` | `spellParenRe` | 315 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 99 | `monster_death_trigger_2024.go:26` | `deathTriggerRe` | 6 | keep |
| 100 | `monster_direct_damage_2024.go:18` | `directDamageText` | 30 | keep |
| 101 | `monster_direct_damage_2024.go:19` | `embeddedDamageText` | 30 | keep |
| 102 | `monster_effects_2024.go:33` | `mfxSaveRe` | 137 | keep |
| 103 | `monster_effects_2024.go:35` | `mfxMagicalRe` | 0 | demote: 0 |
| 104 | `monster_effects_2024.go:36` | `mfxConditionDamageImmunityRe` | 0 | keep: generic gate (immunity check) on a condition |
| 105 | `monster_effects_2024.go:37` | `mfxSuccessImmunityTimeRe` | 14 | keep |
| 106 | `monster_effects_2024.go:38` | `mfxAllSourcesImmunityRe` | 0 | demote: 0 |
| 107 | `monster_effects_2024.go:39` | `mfxDamageRe` | 311 | keep |
| 108 | `monster_effects_2024.go:40` | `mfxCondRe` | 144 | keep |
| 109 | `monster_effects_2024.go:41` | `mfxEscapeRe` | 38 | keep |
| 110 | `monster_effects_2024.go:42` | `mfxRestrainRe` | 13 | keep |
| 111 | `monster_effects_2024.go:43` | `mfxExhaustRe` | 2 | keep: generic (condition: Exhaustion levels) |
| 112 | `monster_effects_2024.go:44` | `mfxAttackRe` | 51 | keep |
| 113 | `monster_effects_2024.go:45` | `mfxUsesRe` | 39 | keep |
| 114 | `monster_effects_2024.go:46` | `mfxSpellRe` | 50 | keep |
| 115 | `monster_effects_2024.go:47` | `mfxSpellDCRe` | 57 | keep |
| 116 | `monster_effects_2024.go:48` | `mfxSpellAtkRe` | 13 | keep |
| 117 | `monster_effects_2024.go:49` | `mfxEmanRe` | 30 | keep |
| 118 | `monster_effects_2024.go:50` | `mfxUntilStartRe` | 53 | keep |
| 119 | `monster_effects_2024.go:51` | `mfxUntilEndRe` | 15 | keep |
| 120 | `monster_effects_2024.go:52` | `mfxEndSaveRe` | 16 | keep |
| 121 | `monster_effects_2024.go:53` | `mfxAutoSucceedRe` | 13 | keep |
| 122 | `monster_effects_2024.go:54` | `targetIsCreatureGateRe` | 13 | keep |
| 123 | `monster_effects_2024.go:55` | `mfxDurationRe` | 102 | keep |
| 124 | `monster_effects_2024.go:57` | `condDurationRe` | 36 | keep |
| 125 | `monster_effects_2024.go:59` | `endsOnDamageRe` | 0 | generalize: duplicate of lastingWakeRe / mfxDurationRe |
| 126 | `monster_effects_2024.go:62` | `escapeCheckRe` | 0 | keep: generic (condition escape: DC, ability, skill) |
| 127 | `monster_effects_2024.go:64` | `autoSucceedTypesRe` | 0 | keep: generic save gate (creature types) |
| 128 | `monster_effects_2024.go:237` | `mfxOptionRe` | 7 | keep |
| 129 | `monster_effects_2024.go:255` | `mfxSizeGateRe` | 66 | keep |
| 130 | `monster_effects_2024.go:259` | `advantageGateRe` | 2 | keep: generic hit-rider gate (had Advantage) |
| 131 | `monster_effects_2024.go:266` | `sentenceSplitRe` | 330 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 132 | `monster_effects_2024.go:571` | `hitRiderDeferredRe` | 29 | keep |
| 133 | `monster_effects_2024.go:573` | `hitRiderChoiceRe` | 57 | keep |
| 134 | `monster_effects_2024.go:611` | `saveMarginHeading` | 2 | keep: generic save parameter (fail by N or more) |
| 135 | `monster_effects_2024.go:612` | `severePoisonText` | 0 | demote: 0, whole sentence |
| 136 | `monster_effects_2024.go:617` | `severeSleepText` | 1 | demote: 1, whole sentence |
| 137 | `monster_effects_2024.go:624` | `secondFailureText` | 1 | generalize: whole sentence -> condition + duration parameters |
| 138 | `monster_effects_2024.go:625` | `oneRepeatRe` | 8 | keep |
| 139 | `monster_effects_2024.go:626` | `secondFailureWakeText` | 4 | generalize: whole sentence -> the same ends-on-damage parameter |
| 140 | `monster_features_2024.go:69` | `regenFromZeroRe` | 2 | demote: 2 (Troll) |
| 141 | `monster_features_2024.go:76` | `saveEndAuraRe` | 1 | generalize: one aura reader (end-of-turn damage and save share it) |
| 142 | `monster_lighting_2024.go:11` | `monsterLightRe` | 6 | keep |
| 143 | `monster_lighting_2024.go:16` | `monsterLightToggleRe` | 1 | demote: 1 (Magmin) |
| 144 | `monster_reactions_2024.go:103` | `reactMissWeaponRe` | 0 | demote: 0 (Kuo-toa) |
| 145 | `monster_reactions_2024.go:124` | `reactHalveRe` | 2 | demote: 2 |
| 146 | `monster_reactions_2024.go:126` | `reactSelfHitRe` | 12 | keep |
| 147 | `monster_recurring_damage_2024.go:27` | `recurringDamageRe` | 2 | generalize: 3rd copy of 'takes N damage at the start of each of its turns' -> one recurring reader |
| 148 | `monster_recurring_damage_2024.go:84` | `recurringFromTextRe` | 0 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 149 | `monster_recurring_damage_2024.go:307` | `recurringLockoutTailRe` | 1 | demote: 1 (with attack lockout) |
| 150 | `monster_recurring_damage_2024.go:308` | `grappleGatePrefixRe` | 5 | keep |
| 151 | `monster_recurring_damage_2024.go:313` | `attachGatePrefixRe` | 1 | demote: 1 (with attach) |
| 152 | `monster_recurring_damage_2024.go:314` | `attachLockoutLeadRe` | 2 | demote: 2 (with attach) |
| 153 | `monster_replace_2024.go:40` | `replaceSentenceRe` | 46 | keep (the one replace reader) |
| 154 | `monster_replace_2024.go:41` | `replaceMarkerRe` | 10 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 155 | `monster_replace_2024.go:42` | `replaceLevelRe` | 30 | keep |
| 156 | `monster_save_reactions_2024.go:17` | `failedSaveReactionRe` | 0 | demote: 0 (Mind Corrosion, not SRD) |
| 157 | `monster_shape_shift_2024.go:22` | `shapeShiftRe` | 13 | keep |
| 158 | `monster_shape_shift_2024.go:23` | `formGateRe` | 7 | keep |
| 159 | `monster_standard_actions_2024.go:18` | `monsterStandardActionRe` | 11 | keep |
| 160 | `monster_standard_actions_2024.go:20` | `standardActionNameRe` | 16 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 161 | `monster_suffocation_2024.go:36` | `suffocationRowRe` | 0 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 162 | `monster_swallow_2024.go:27` | `swallowActionRe` | 2 | demote: 2 (swallow state) |
| 163 | `monster_swallow_2024.go:28` | `swallowAcidRe` | 1 | demote: 1 |
| 164 | `monster_swallow_2024.go:29` | `swallowCondsRe` | 12 | generalize: duplicate of mfxCondRe (goes with swallow below) |
| 165 | `monster_swallow_2024.go:30` | `swallowArmedRe` | 0 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 166 | `monster_target_prerequisites_2024.go:12` | `singleSaveTargetRe` | 30 | keep |
| 167 | `monster_target_prerequisites_2024.go:13` | `monsterBlindsightRe` | 0 | generalize: senses -> one reader |
| 168 | `monster_target_prerequisites_2024.go:14` | `pathSaveTriggerRe` | 0 | demote: 0 |
| 169 | `monster_traits_2024.go:69` | `(table row)` | 6 | keep: already a GM flag row (communication/sense trait) |
| 170 | `monster_traits_2024.go:70` | `(table row)` | 2 | keep: already a GM flag row (communication/sense trait) |
| 171 | `monster_traits_2024.go:71` | `(table row)` | 3 | keep: already a GM flag row (communication/sense trait) |
| 172 | `monster_traits_2024.go:152` | `rollTraitRe` | 4 | generalize: one roll-trait reader (who, when: Bloodied / target missing HP, melee/all, saves) |
| 173 | `monster_traits_2024.go:155` | `rollTraitBloodiedRe` | 3 | generalize: into rollTraitRe's parameters |
| 174 | `monster_traits_2024.go:156` | `rollTraitWoundedRe` | 1 | generalize: into rollTraitRe's parameters |
| 175 | `monster_traits_2024.go:157` | `rollTraitMeleeRe` | 1 | generalize: into rollTraitRe's parameters |
| 176 | `monster_traits_2024.go:158` | `rollTraitSavesRe` | 2 | generalize: into rollTraitRe's parameters |
| 177 | `obscured_areas_2024.go:66` | `obscuringTextRe` | 2 | demote: 2 (Ink Cloud) |
| 178 | `player_text.go:22` | `gmNoteRe` | 0 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 179 | `rider_duration_2024.go:34` | `riderEndRe` | 79 | keep: the duration parameter, one owner (rider_duration_2024.go) |
| 180 | `site_views_2024.go:71` | `trailingNumber` | 148 | n/a: not stat-block wording (grammar, storage or UI helper) |
| 181 | `speed_owner_2024.go:263` | `speedHalvedRe` | 6 | keep |
| 182 | `speed_owner_2024.go:264` | `speedZeroRe` | 3 | keep: generic (Speed change), 3 |
| 183 | `speed_owner_2024.go:265` | `speedMinusRe` | 5 | keep |
