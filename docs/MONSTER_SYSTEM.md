# Monster attacks and the shared compendium

How a stat-block attack becomes game mechanics, and what the server will and will not do for it. Written for developers and GMs.
Applies to SRD monsters and to monsters imported into the shared compendium (`POST /api/gm/compendium/monsters`), including
creatures that are not in the SRD (high-CR Monster Manual entries, for example). No monster is named in code: everything below
reads the stat block.

## The rule

A monster attack is dealt in full, or it is refused before anything is rolled (no dice, no Hit Points, no resource spent), with a
message that says what the GM must resolve. The server never deals a guess. Where an effect is not applied automatically, the hit's
reply says so (`[GM, not applied automatically: ...]`).

## Owners (one place for each rule)

| Rule | Owner |
| --- | --- |
| Reading the Hit damage of an attack: primary amount, chosen type, extra amounts, alternatives | `parseMonsterAttack` / `parseExtraDamage` (`monster_damage_choice_2024.go`) |
| Checking an attack before it is rolled | `prepareMonsterAttack` (every path); `prepareUnconditionalAttack` for paths that cannot judge a hit's conditions (companions, Wild Shape) |
| Which attacks a Multiattack makes | `multiattackPlan` (`monster_attacks.go`); every caller reads its refusal |
| Absorption (damage turned into Hit Points) | `monsterAbsorbs` (`attack_target.go`), applied by `hurtMonster` and a character's attack on a monster |
| Hit Points of a monster | `monster_hp_2024.go` (unchanged owner) |

## Refusals and audience (a rule every refusal follows)

A refusal answers two audiences, and the split happens BEFORE anything is persisted:

- **The GM reads the full reason.** Monster limits, remaining resources, stat-block clauses and adjudication
  details are the GM's private information. The full reason is always given to the appropriate caller - these
  routes are the GM's - in the reply, and to the GM's notices when the refusal is logged.
- **The shared log the players read keeps only the player-safe observation:** that the action was refused and
  that nothing was rolled. No limits, resources, stat-block clauses or adjudication details in the shared text.
- **Structural, not cosmetic.** The refusal is built as `"<player-safe>. Nothing was rolled. [GM: <full reason>]"`
  by `refusalAudiences` (`monster_attack_lockout_2024.go`), and `writeAction` splits the `[GM: ...]` note into the
  GM's notices before the shared log row is written (`splitGMNotes`, `player_text.go`). The stored row never
  holds the private text, whoever the caller is; `playerText` stays defense in depth on every read.
- **No silent refusals.** A refusal always says it refused and that nothing was rolled, on every surface its
  audience reads. A refusal that only the log carried, or only the reply, is a defect.
- **Numeric monster HP is never public** from any refusal or result text (damage results use the health words).
  Character HP wording follows the existing public behavior.
- **Replay refusals stay caller-only** (the 409 turn_already_moved answer is not logged).
- **Regression tests are part of the rule.** A refusal change ships with assertions on the persisted shared-log
  row (player-safe text, no private clause), the GM's notice (full reason) and a player read (no leak) -
  `TestPostgresAttackLockout` is the model.

The refusals under this rule: the attack lockout (a grapple or attachment the stat block creates), per-day use
limits, Recharge not yet rolled, and Multiattack plan or cap problems (a server limit or an adjudication detail,
never a public default).

## What is understood in an attack's Hit text

* `Hit: 12 (2d6 + 5) Slashing damage` and a flat `Hit: 9 Slashing damage`.
* More amounts joined by `plus` / `and`: `plus 10 (3d6) Fire damage`, `plus 7 Fire damage` (a flat amount is not doubled on a critical hit).
* `plus 2 (1d4) Slashing damage if the attack roll had Advantage`.
* An alternative amount with a condition: `, or 18 (4d6 + 4) Piercing damage if the chimera had Advantage on the attack roll`.
  Judged: Advantage on the roll, the attacker Bloodied, the target Bloodied, the target Grappled (by the attacker when the text says so).
  Any other condition (for example `if the goat moved 20+ feet straight toward the target`): the normal damage is dealt and the reply
  tells the GM the other amount applies if the condition was true.
* `, or ... if used with two hands`: the one-handed amount is dealt.
* A damage type the GM chooses: `damage of a type chosen by the ...: Acid, Cold, ...` and `7 (2d6) damage of the type chosen for the ...
  trait`. Send `use` (the type) in the request; without it the attack is refused with the list of types.
* `... equal to the Acid damage taken` refers back to an amount already read.

Refused before rolling: an amount joined any other way, an extra amount with a condition other than Advantage, damage words without an
amount, a Multiattack the server cannot plan.

Not applied automatically, shown to the GM with the hit: effects of the hit that are not damage (a Prone or Grappled rider without a
saving throw, a Hit Point maximum reduction, and similar). Saving-throw riders are applied by the existing save-effect owner.

## Multiattack

* A count is read from the number word or digits before an attack's name ("makes two Claw attacks").
* The whole plan is limited to 20 attacks. A larger or unreadable number is refused when attacked and **rejected when imported**.
* "one Bite attack or one Claw attack", "using A or B in any combination", "either ...": a choice the text leaves open. The server does not
  combine them. The reply says to make the attacks one by one by name; nothing is rolled.
* Optional additions ("It can replace one attack with a use of Spellcasting") are not planned; the GM adds them.

## Absorption

A trait written as "Whenever the X is subjected to Fire damage, it regains a number of Hit Points equal to the Fire damage dealt" turns
damage of that type into healing (up to the maximum). It is read from the trait text, for any monster. Used by GM damage
(`hurtMonster`), a character's weapon attack on the monster, and the weapon-attack riders (`adjustRiders`, healing capped at the
maximum). Every extra amount a weapon hit carries now goes through `adjustMonsterExtra` (attack_target.go): same defenses as
`adjustMonsterDamage`, and absorbed damage comes back as a negative amount with how "absorbed" that the hit applies as healing (capped at
the maximum in `resolveAttackOnMonster`). Covered: Flame Tongue, Divine Favor, Helm of Brilliance, Giant Slayer, Oathbow, Staff of
Thunder and Lightning, Sword of Wounding, Frost Brand, Dagger of Venom, Vicious Weapon, Sword of Sharpness, Mace of Disruption, Divine
Strike, Holy Avenger, the Rod's flaming blade, Drain Life, and the Sphere of Annihilation's force damage (which uses `hurtMonster`).
Sun Blade and Staff of Striking previously ignored every defense of the target; they now apply them. The Graze mastery on a
miss now uses `adjustMonsterExtra` too (an absorbing monster heals); the decision is `grazeHP` plus `adjustMonsterExtra`, tested by TestGrazeHP and TestPostgresGrazeAbsorbed (an imported absorbing monster). The full Fighter-with-Graze attack is still not played end to end. Tests: TestPostgresExtraDamageAbsorbed,
TestPostgresWeaponHitEffectsAbsorbs, TestAdjustRidersAbsorbs. Not tested end to end: a full attack with each item (the item
state needed is large); the helper results and the shared aggregator are tested.

## Shared catalog

The compendium is shared among all authenticated GMs (not private to a campaign). Do not put private campaign notes or spoilers in
`source_note` or a definition.

## Tests

`go test ./...` with `TEST_DATABASE_URL` naming a disposable database that contains `test` and `REQUIRE_TEST_DATABASE=1`. Rule tests:
`monster_attack_rules_test.go` (parser, alternatives, Multiattack bounds), `monster_request_rules_test.go` (through the GM route),
`monster_absorption_test.go`, `monster_damage_choice_2024_test.go` (all 423 SRD attacks). The three non-SRD reference monsters need
`REFERENCE_FIXTURES_DIR` (`private_reference_combat_test.go`).

## Known limits

Spellcasting, legendary and lair effects, recharge details, auras and most traits are still the GM's to adjudicate unless another
owner handles them. Passing these tests does not mean every printed monster ability works. Unverified against a live server.


## Book-based verification and coverage (2026-11-17)

Source: the Monster Manual split into Word and HTML chapters that Matt supplied (Drive folder 1OG2yZevRofRaY_7ZDep7Vg1hv5YFnZAn). This is
the "reconstructed" text, not a scan of the printed book; every statement below is about that text.

- `tools/verify_fixture_against_book.py fixture.json chapter.txt` checks a fixture's trait/action text and key stats against a chapter.
  The three fixtures ChatGPT transcribed (Arch-hag, Dracolich, Elemental Cataclysm) all match the chapter text (61 items, 0 missing).
- `tools/parse_mm_statblocks.py out_dir min_cr chapter.txt...` reads the stat-block layout itself (no monster names) and writes importable
  JSON. 91 monsters with CR 10 or higher parsed from chapters 2a to 2z; blocks that do not parse are listed and skipped.
  Its output for the three transcribed monsters agrees with those fixtures (the parser also keeps the full Legendary Actions intro).
  Word text (chapter 2a) has no page breaks, so text after the final entry of a block is dropped there. Checked for all four CR 10+ blocks in 2a (Aboleth, Animal Lord, Arcanaloth, Arch-hag): the dropped lines are the next monster's heading or lore/quote, and the kept last entries are complete. The Arch-hag page (p. 21 of the supplied Cover - 25.pdf, which is itself a reconstruction) was compared by eye with the parsed block: same stats, same final entry. The supplied chapter PDFs have no text layer, so the other three were compared against the Word text only.
- `TestMonsterBookCoverage` (cmd/server/monster_book_coverage_test.go, needs MM_FIXTURES_DIR) imports every parsed block through the
  shared validator and reports attack rolls automated, attack rolls refused, and actions left to the GM. Importing is not coverage.
  Latest report: COVERAGE_REPORT_CR10plus.md next to this file.
- The parsed fixtures contain the book's text, so they are kept out of the source tree (private Drive zip), not committed.
- `TestPostgresBookRouteSweep` (cmd/server/monster_book_route_test.go, needs MM_FIXTURES_DIR and PostgreSQL) plays every parsed monster through
  the real routes: import, combat/add with stats checked, then every attack-roll action through POST /api/gm/narrate against an AC 1
  target. Tiers, kept separate: parsed 91, imported 91, attack rolls automated 123, route-exercised 123 attack rolls (not saving-throw
  actions, spellcasting, traits or legendary actions). Report: ROUTE_SWEEP_REPORT_CR10plus.md.

## Save actions (parse tier)

`TestMonsterBookSaveActions` (MM_FIXTURES_DIR; MM_SAVES_OUT writes a report) reads every "Saving Throw:" action of the CR 10+ fixtures
and compares `parseSaveEffect` with the text: every damage amount must be in the parsed effect or the rest must be flagged to the GM
(`saveEffect.Extra`). Result: 121 save actions, 53 fully represented, 68 with leftover text (pushes, riders, recharge, "Failure or
Success" effects) shown to the GM, 0 lost. This proves the parsing and the flagging only. It does not play the saves through the
route; the save, half damage, Evasion, conditions and absorption of a character target are the existing `applySaveEffect` rules.
Not automated: saves against monsters as targets, area targeting without a battle map. Recharge is already tracked by the shared code (a used Recharge action is refused until the monster's start-of-turn roll).

An attack with a "Hit or Miss:" effect (Dao's Earth Burst) rolls the attack and, on a hit, the target's save. The effect on a miss and
on the other creatures in the area is passed to the GM by `hitOrMissNote` (monster_attacks.go); it used to be dropped silently.

The book parser removes a page-break heading ("Adult Gold Dragon (continued)") that split a sentence in three stat blocks.

`TestPostgresBookSaveRoute` (MM_FIXTURES_DIR; MM_SAVE_ROUTE_OUT writes a report) plays every save action with damage (67 in the CR 10+
set) through POST /api/gm/narrate against a character, up to 40 times each: the reply must name the stat block's ability and DC; a failed
save deals an amount inside the parsed dice range; a success deals nothing or at most half (nothing when the text has no half-damage
rule); a spent Recharge action must be refused on reuse with no damage. Result: 67 with a failed save seen, 61 with a success seen
(the others had too low a chance in 40 tries), reuse refused 314 times. Conditions given on a failure are checked by name (245 lines). Not checked: durations, riders, defenses, Evasion, monster targets.
A "Failure or Success: One of the following effects occurs:" text now tells the GM the options are the entries after the action.

Saves with a monster as the target are automated (monster_save_targets.go, `applySaveEffectOnMonster`, called from `applySaveEffect`): the
target of a monster_action may now be another monster of the fight (name, "#id" or a negative id in target_ids). Its save is `saveCtx.resist`
(Magic Resistance, Legendary Resistance, its stat block's bonus), the damage goes through `hurtMonster` (resistance, immunity, absorption,
Undead Fortitude), conditions through `addCondition` (duration, repeat save, source), Evasion is honored as a trait row, and Exhaustion is told to
the GM (a character rule). Plain "takes N damage" effects reach monsters too. NPCs that are characters keep the character path. Tests:
TestPostgresSaveEffectOnMonsterTargets (DC 30 / DC 1 test monsters: failed save, half damage on success, Evasion, absorber heals, stored
Frightened and Charmed records with their duration and repeat save, expiry, target_ids with two monsters, no-target refusal); a mutation that
turned Evasion off failed it. Also tested: resistance halves the failed-save damage of a monster that resists the type, and Legendary Resistance (2/Day) turns a failure that would give a condition into a success twice (half damage, no condition) and the third failure stands. Not tested: Magic Resistance on a spell effect.
Character-target defenses (resistance, immunity, Evasion of characters) are owned by `damageHP` / `hasEvasion`; the save sweep does not re-verify them (it only checks the damage lies in the dice range with the sweep character, who has none).


Stored condition state (TestPostgresBookSaveRoute, checkConditionState): after each failed save that gave a condition (229 records over the 67
actions, none of them immune), the character's condition record must exist with the stat block's duration (1 round "until the end of its next
turn", 10 "for 1 minute"), the repeated save (ability and DC) iff the text says "repeats the save", the until-start-of-next-turn note iff the
text says so, and the monster as its source; a timed record must be gone after its rounds pass (`passConditionTime`). A mutation (expected
duration +1) failed 244 checks, so the check bites. The rolling of an end-of-turn repeat save and the removal on success are the condition
owner's (condition_turns.go, tests in condition_records_test.go); this sweep stores the right save, it does not roll it again.
Not checked: riders ("pushed", "Speed halved", Disadvantage ...) are GM text; Failure-damage defenses (resistance, immunity, Evasion) of the
target; Exhaustion levels.

"Can't take this action again until the start of its next turn" (14 of the flagged save actions, e.g. the metallic dragons' breaths): an action or
bonus action with that sentence is refused with "Nothing was rolled" on a second use and given back at the start of the monster's next turn,
in the same list a Legendary Action's use is kept (`LAOnce`, monster_features_2024.go). Test: TestPostgresSaveEffectOnMonsterTargets
(Lingering Burst). Flagged save actions still carry GM text for pushes, Speed changes, Disadvantage, Grappled/Restrained riders and "one of the
following" effects: 28 "Failure or Success" sentences in the CR 10+ set.

"the Grappled condition (escape DC N)" in a save effect (Behir, Marilith, Gulthias Blight ...): the stored Grappled record now carries the escape
checks (Strength (Athletics) or Dexterity (Acrobatics) against N), as the unarmed-grapple rule stores them (`saveEffect.conditionAdd`, one
function for character and monster targets). "…and it has the Restrained condition until the grapple ends" (Behir, Marilith Constrict) is stored as a Restrained record with the note `ends_with_grapple` and the same source; `conditionChange.remove` (the one place condition rows are deleted) ends it together with the Grappled record of that source, whether by escape, release or the grappler being Incapacitated. Save-action parse sweep is now 55 represented / 66 flagged / 0 lost. Tests: the route
test checks the escape DC of every stored Grappled record; TestPostgresSaveEffectOnMonsterTargets (Squeeze).

## Save-route test: intermittent failure, cause and fix
`TestPostgresBookSaveRoute` failed rarely (about 1 in 10 runs). Captured output: Guardian Naga / Poisonous Spittle, failed save dealt 5, expected 7-56. Cause: the test's sweep character is a Dwarf (Resistance to Poison damage), so a low poison roll halves below the test's lower bound. The game code was right; the test bound ignored the character's resistance. Fix: the lower bound halves poison dice. Seven poison save actions are affected (Guardian Naga, green dragons, Beholder, Death Tyrant, Iron Golem, Spy Master). The 40-try "no failed save" assertion was checked: at DC 14+ against a +5 save the chance is negligible.

## Leftover rules text reaches the GM (route-tested)
The save-route sweep now also checks, for every played save action, that any text the shared rules do not automate (pushes, Speed riders, Disadvantage riders, "Failure or Success" riders, area wording) appears whole in the reply with a "the GM" line, on a failed save (or any save for "Failure or Success" text). 156 replies checked in the last run. Mutation: removing the GM line failed 149 checks. This proves the text is surfaced, not that it is automated: pushes, Speed and Disadvantage riders are still GM-applied.

## Magic Resistance on imported monsters (tested)
An imported stat block with the Magic Resistance trait gets Advantage on a save against a spell through the one save owner (`saveSources`, save_2024.go); a monster without it, or a save against a monster's own action, gets none. `TestPostgresSaveEffectOnMonsterTargets` checks the Advantage sources through `saveCtx.gather` (nothing rolled). Mutation: switching the row off failed the test. Not covered: a spell cast by a character played end to end against a Magic Resistance monster (the spell paths call the same `monsterSpellSaveResult`).

## Size-gated conditions
"If the target is a Huge or smaller creature, it is pushed ... and has the Prone condition" (Marid, Water Jet) is read into `saveEffect.CondMaxSize`. `saveEffect.conditionsFor` is the one rule for characters and monster targets: a bigger target keeps the damage, does not get the condition, and the reply says "not Prone (larger than Huge)". The push itself stays GM text. Test: `TestPostgresSaveEffectOnMonsterTargets` (Medium target stored Prone, Gargantuan target not; mutation removing the gate fails it). Only one book action has this shape (Marid); other "if the target is" clauses are not about conditions. Characters are Small or Medium by species.

## Multi-effect actions and staged effects (found by audit)
Audit of every CR 10+ save action found two silent over-simplifications in the shared parser, now fixed:
1. **Actions that offer several effects** (Beholder and Death Tyrant Eye Rays, 10 rays each; Elemental Cataclysm, 4 effects). The parser read the first listed ray and rolled it as if it had been chosen, with the rest not shown. Now `splitSaveOptions` splits the text by named effect, and `resolveMonsterEffectText` refuses the action before rolling ("offers N effects ... name the one that happened in use. Nothing was rolled.") until the GM names one in `monster_action.use`; the named effect is then resolved alone through the same save rules. Tests: `TestSplitSaveOptionsAndStages`, `TestPostgresSaveEffectOnMonsterTargets` (refusal, no HP change, no condition; named effect applied alone; mutation fails), and the route sweep (3 actions refused, each option played).
2. **Staged effects** ("Failure: Incapacitated ... repeats the save. Second Failure: Unconscious / Paralyzed", Brass and Silver dragon breaths). Only the first stage was applied and the Second Failure text was dropped without a note. Now it goes to the GM in the reply ("Second Failure (after the repeat save the first Failure gives): ..."). The repeat save and second stage are not automated.
Parse sweep after the change: 141 save entries (multi-effect actions counted per effect): 55 represented, 86 flagged to the GM, 0 lost. Route sweep: 79 played (each ray/effect separately).
Not done: choosing the random effect (the GM rolls or picks), the second stage of a staged effect, and area targeting without a battle map.

## Conditions a hit gives (new: applied, not left to the GM)
Before this change a stat-block attack applied only its damage; every rider ("If the target is a Large or smaller creature, it has the Prone condition", "Grappled (escape DC 14)", "Stunned until the start of the empyrean's next turn") was shown to the GM as "not applied automatically". Now `parseHitRider` (monster_effects_2024.go) reads the conditions of a hit with the same rule as a failed save (`readRider`), and `saveEffect.giveConditions` gives them to the target (condition records with the monster as the source; immunity honoured; size gate; escape DC; Restrained that ends with the grapple). The monster attack path (monster_attacks.go) calls it after the damage; the shared owners are unchanged (one condition builder, one size rule).
- Still the GM's, shown in the reply with "[GM, not applied automatically: ...]": a choice or a roll of the condition ("the githzerai's choice of (A) ... or (B)", "determined by rolling 1d4"), curses, a second condition sentence ("Until the grapple ends, the target has the Restrained condition and can't teleport"), "While Poisoned, the target can't regain Hit Points", pushes and pulls, Speed and Disadvantage riders, Hit Point maximum changes.
- "until the start of the <monster>'s next turn" is now the monster's turn start (same as "its"); before it was a one-round duration. `TestParseSaveEffect` was updated for that.
- Tests: `TestPostgresMonsterHitRiders` (invented attacks: Prone stored from the monster, size gate with the reason in the reply, Grappled escape DC 17 + linked Restrained, Stunned until start of its next turn, a choice of conditions left to the GM, Poisoned applied while the remaining sentence stays with the GM; mutation switching the apply off fails it) and the route sweep (123 book attack actions hit; 23 hit-rider conditions checked as stored or explained). The sweep clears the character's conditions between attacks (stored riders would otherwise give Advantage and critical hits).
- Not covered: monsters as the target of a monster attack's riders (this path is for characters); end-of-turn expiry of the hit-given Stunned was not replayed (the condition owner's time passing is tested in condition_records_test.go and in the save sweep).

## "Until the start of its next turn" ends on the source's turn (bug found by the hit-rider test)
The condition owner ended a record noted `until_start_of_next_turn` at the start of the holder's own turn. For a monster's effect ("Stunned until the start of the empyrean's next turn") that is the wrong creature: a character who acts before the monster's next turn was freed early. Now `monsterFeaturesTurnStart` ends, at the start of a monster's turn, the records whose source is that monster's ("<name>'s <action>") wherever they are held. Source-turn timing does not depend on the source's HP.

Open adjudication (not decided by the supplied rules, so nothing is shortened by a guess):
- **Source down (0 HP) or removed from the fight:** it has no next turn. The record stays stored; at each turn start of the holder the reply carries a "[GM] ... Nothing was changed; the GM decides whether it ends (remove it with the conditions route)" line naming the condition and its source. Tested for a downed and for a removed monster.
- **Explicit end text** ("ends if the dragon is Incapacitated", etc.) is not read by the parser; such sentences are GM text.
- **Combat teardown (`combat/end`):** monsters' own records are cleared; a character's source-timed record is kept (`TestPostgresMonsterHitRiders` asserts the current behaviour so a change is noticed). The GM removes it.
- Death versus downed: the fight stores 0 HP for both here; no separate path was tested.

## Spellcasting entries (route-played)
`TestPostgresBookSpellcastingRoute` plays every spell listed in the CR 10+ Spellcasting entries (323 spells) through POST /api/gm/narrate with `monster_action.use`: 104 rolled by the shared spell rules (a save or an attack roll), 219 handed to the GM with the spell named and "GM" in the reply, 0 refused, 0 silent. This proves nothing was dropped silently; it does not prove the 219 are automated (utility, buff and out-of-combat spells stay with the GM), nor that the 104 effects are right beyond what the spell tests cover. The rolled/GM split moves by a few spells between runs (the reply text depends on the dice), so only the 0-silent result is a fixed claim. Report: SPELLCASTING_ROUTE_REPORT_CR10plus.md.

## Per-day uses (new, tested)
Before: nothing counted "(1/Day)" actions or the "2/Day Each" / "1/Day" lists of a Spellcasting entry (only reactions, in memory). Now `monster_day_uses.go` reads the tiers (`spellTiersOf`): "At Will" is unlimited, "N/Day Each" counts each spell, "N/Day" is one count for the whole list, and "(N/Day)" in an action name counts that action. The counts live in a ledger per creature (`monster_day_uses` table, keyed by campaign and `creature_id`), so they last from one fight to the next and through a restart. Identity is explicit: `combat/add` gives every monster a fresh `creature_id` (returned in `combatants_added`, shown by monster-features). Two creatures with one name never share uses, and a rename changes nothing. A returning creature is linked on purpose: give its `creature_id` in `combat/add` (same stat block, known to the campaign, not already in the fight; otherwise 400 `unknown_creature_id` / `creature_already_in_fight`). A monster added without one is a NEW creature, so forgetting to link gives it fresh uses (GET /api/gm/monster-features without combatant_id lists the known creatures). Monsters the fight makes by itself (no creature_id) are counted for that fight only.
Counting is explicit and atomic. The limit is checked and the use reserved in ONE database transaction (`reserveDayUse`, row locked), so two requests cannot both take the last use (eight concurrent requests spend exactly one; removing the lock fails the test). A reserved use stays counted only when the code that carried it out says so, not from the reply text or the damage: the executors call `markExecuted` on a per-request execution record (`monster_day_uses.go`): a saving throw effect applied to its targets (a success, zero damage, an immune target and a condition-only effect all count), a Command or spell save rolled, a spell attack made, a reaction that took effect; a limited-use attack counts once the attack code made it against a target (it may miss, be blocked or deal nothing). Anything else gives the reservation back (`releaseDayUse`): a refusal, a target the fight does not know, a hand-off where the server rolled nothing and the reply only tells the GM what to do, a reaction that found no trigger. `apply_damage:false` is a preview (the word "apply" means only this execution mode): it reads the count and reserves nothing. "Day use counted for <name>: n of N/Day" is said for an effect; the reply for a hand-off says "Not counted as a day use" and names the key so the GM can count it (`day_uses` {key: n}). The state save never overwrites the counts (they belong to the ledger). A Multiattack counts no day use itself; its single chosen limited sub-attack counts under its own name, and a plan with several attacks is not counted (open). Other controls: `new_day: true` (every count in the campaign comes back; the server does not track days). Reactions with "(N/Day)" use the same ledger now (the in-memory count is gone). Tests: `TestPostgresMonsterReactionDayUses` (a refused Split and a reaction with no trigger are not counted, an executed one is, and it outlasts the fight; mutation checked) and `TestPostgresMonsterDayUses` (a missed attack, a save that succeeds for no damage and a condition-only effect each spend the use; 1/Day action, 2/Day Each, At Will, shared 1/Day, stored counts, second fight, same name not linked = new creature, link under another name, bad link refused, one creature twice, two same-name creatures in one fight, preview and hand-off not counted, GM count and give-back, new_day; mutations (one shared id, counting hand-offs, counting previews) each fail it). The save and spell sweeps reset the counts between tries.
Limits: a mixed Spellcasting reply (part rolled, part GM) is counted as executed because the server rolled; a spell whose effect text the server cannot read is never counted automatically. Per-day limits: a single limited attack is checked and counted in `resolveMonsterAttack` (narrate, a Legendary Action's called attack, the battle map's attack); a limited effect or spell in `monsterAction` (narrate); a Legendary Action's own "(N/Day)" entry in `takeLegendaryAction` (`TestPostgresLegendaryDayUses`: the called limited attack, the entry's own limit, and a refused one uses no Legendary Action use). A limited non-attack action called by another entry ("uses Howl") is checked in the shared effect entry (tested through a Legendary Action). A limited spell a Legendary Action casts is checked in the same entry (tested). The map route's Opportunity Attack uses the shared attack entry (tested: a spent 1/Day attack is refused and the Reaction is not used up; a fresh one is counted). A reaction's riposte attack and a reaction that calls another action use the same shared entries (resolveMonsterAttack, resolveMonsterActionByName) but have no test of their own, so they are not claimed. A battle-map turn that uses an effect (not an attack) has no route of its own in the code today; the immune-target outcome is covered by the code path but not by a dedicated test. Spells cast "before combat" or listed with a note ("included in AC") are not special-cased.

## Monster targets and the Speed owner (built; tiers stated)
**Monster attacks on monster targets** (`monster_vs_monster.go`). A monster's attack action naming another monster or NPC of the fight ("Goblin 2", "#-3") now resolves through the same owners as a character target: the roll is `attackCtx` (Advantage and Disadvantage from both sides' conditions, the target's Armor Class from the fight), damage is `hurtMonster` (resistance, immunity, absorption, Undead Fortitude), conditions a hit gives are `giveConditions` (now the one function for characters and monsters; Exhaustion on a monster goes to the GM), a hit's own saving throw is `applySaveEffect`, and conditional damage (Bloodied, Grappled) reads the monster target. A limited-use attack counts when it is made against a monster target. Tested: `TestPostgresMonsterAttacksMonster` (resistance, stored condition with source, source-timed Stunned, the hit's save, preview changes nothing, Bloodied damage choice, no self-attack). A test found and fixed one bug: damage type must be lower case for resistance. Reactions of the target: a hit on a monster target is recorded like a character's hit on a monster (`noteCharHitOnMonster`, with the attacker's name), so the target's stat-block Reactions that answer a hit (Parry, Protective Magic's Shield, Deflect Missile, Redirect Attack, Protection) resolve against it when the GM calls them with POST /api/gm/narrate monster_action right after the hit; the reply names the target's Reactions and whether its Reaction is spent (`monsterTargetReactionNote`). Route-tested with Parry: the hit becomes a miss and its damage is given back, a natural 20 stays a hit, a second Reaction the same round is refused; mutation-checked (without the recording, Parry finds no hit). The server does NOT choose to react by itself (the GM chooses when a monster reacts, as for character attackers), and standing orders and Opportunity Attacks of this path stay the GM's; Shield, Deflect Missile, Redirect Attack and Protection use the same recorded hit but have no test of their own here. NOT covered, and the reply says so: Multiattack's non-attack parts, total cover on the map, a day limit is now tested for this path: a (1/Day) attack aimed at a monster target is counted when made, and the second try is refused before rolling (mutation-checked on the `daySettled` line in `resolveMonsterAttack`; the `markExecuted` call in `monsterAttacksMonster` is redundant for this count, so a mutation on it alone is not caught and nothing is claimed for it).

**Speed owner** (`speed_owner_2024.go`; `characterSpeed` asks it). Sources: Grappled and Restrained (Speed 0), the Slow spell (halved, not against Freedom of Movement), Haste (doubled), Haste's lethargy (0), Slow and Hamstring Blow masteries and Frost's Chill (flat reductions, now enforced; before they were text), and stat-block riders ("Speed is halved", "Speed is 0 / reduced to 0", "Speed decreases by N feet") stored as markers `speed_halved`, `speed_zero`, `speed_minus` that end at the end of the target's next turn (counted down at the end of each of its turns, twice when the target is acting when it is applied). A rider is applied only when the text gives that duration; one that ends on the attacker's turn or has no duration stays in the GM text (a short Speed sentence used to be dropped silently there; fixed).

*Base Speed is not an effect.* `characterBaseSpeed` is one calculation (species or Wild Shape form, Large Form, class bonuses, Longstrider, Boots, armor Strength penalty, Exhaustion, a Fly Speed from a spell). Exhaustion's reduction is the glossary's own ("Your Speed is reduced by 5 times your Exhaustion level"). Before the owner, the Slow spell halved and Haste doubled the budget after that calculation (`spellTurnStart`, now removed); the owner keeps that order, so ordinary combinations (Slow with Exhaustion, Haste with Longstrider, Slow on a Fly Speed) are applied and never refused (tested with Exhaustion; the other bases are by the same code path).

*Source wording used (SRD 5.2.1 glossary in `cmd/server/data/srd521_glossary.json`: entries Speed, Dash, Exhaustion, Flying, Condition, Grappled, Restrained, Paralyzed ...).* Settled and automated: "Speed 0. Your Speed is 0 and can't increase" (0 wins over Haste and bonuses); "Changes to Your Speeds": an effect that changes the Speed changes every special speed by the same amount (the effects are applied to the whole base); Dash: "The increase equals your Speed after applying any modifiers" (the Dash code reads the Speed now; tested through POST /api/action under a halving); one effect alone applies as its text says; flat reductions from different effects each say "decreases by N" and are summed (a repeat of one effect, such as Hamstring Blow, replaces itself as its text says).
Concrete ambiguity, left to the GM, only when the result depends on it: the server evaluates the operations (sum of reductions, one halving, one doubling) in every order and compares. A reduction with a halving gives different Speeds by order ((30-10)/2 = 10, 30/2-10 = 5), so does a reduction with a doubling (40 vs 50), and Slow with Haste on an odd Speed (35: 17*2 = 34, 70/2 = 35); no text supplied says which comes first (the Slow spell and Haste texts say only "halved" and "doubled"; the glossary has no rule for combining Speed effects). Slow with Haste on an even Speed gives the same Speed in every order and is applied. Two halvings (or two doublings): no text says whether the second stacks (the "doesn't stack with itself" sentence of the glossary entry Condition is about conditions, not Speed effects). In those cases the Speed is left unchanged by the effects, movement is refused with the effects named (my-turn and the refusal say so), and the GM rules with `POST /api/gm/conditions {"condition":"speed_ruling","speed_ft":N,"duration_rounds":R}` (DELETE removes it). The player is not asked to choose an order: no rule supplied offers one. If a source that settles these is supplied, `speedFactors.apply` is the one place to change.

*Mid-turn.* `movement_speed_base` remembers the Speed the budget came from; `settleMovement` (my-turn, action checks, map move) moves the budget by the change, so effects that arrive or end during the turn count. The budget may go below 0 internally and shows as 0: movement already spent stays spent, and effects that come and go give nothing back (tested: 25 of 30 spent, Slow arrives and ends). Dash under a halving is tested through the route (the halved budget plus the halved Speed).

Tested: `TestSpeedFactorsCombine` (settled and unsettled combinations, the GM ruling), `TestSpeedRiderParsing`, `TestPostgresSpeedOwner` (marker stored and read, halved/0/-10, mid-turn budget, settle twice, effect ending mid-turn, spent movement not returned by toggling, turn start at Speed 0, attacker-turn rider kept for the GM, timing on another creature's turn and on its own, baseline Slow and Haste with Exhaustion never refused, Grappled with other effects, Slow, Slow with Hamstring Blow refused until the GM rules through the route and back, Haste); the ambiguity table and the own-turn rounds branch are mutation-checked. NOT covered: a monster's Speed is not a budget (the GM moves tokens; `monsterSpeedNote` lists the markers and a needed ruling, not yet shown on the map move); mounts reset the budget baseline; separate Fly/Swim budgets (one pool, effects applied to it); Beholder-style "can't take Reactions / action or Bonus Action, not both" parts stay with the GM; "Failure or Success" Speed text is still the GM's.

## Disadvantage riders (`cmd/server/disadvantage_riders_2024.go`)

Read by `readRider` from a hit or a failed save, applied by `giveConditions`, by the one owner for characters and monsters.

| Text | What the server stores | Read by | Ends |
|---|---|---|---|
| "Disadvantage on its next attack roll before the start of the X's next turn" | combat effect `sap` | `attackSources` (the Sap row), consumed by the next attack roll | the source's next turn start (`expireCombatEffectsOf`) |
| "Disadvantage on attack rolls until the end of its/the target's next turn" | marker condition `disadv_attacks` | `attackSources` | end of the target's next turn (2 rounds if it is acting now) |
| "Disadvantage on saving throws to maintain Concentration until the end of its/the target's next turn" | marker `disadv_concentration` | `saveSources`, only for Concentration saves | as above |

Left to the GM, listed in the reply's Extra and never dropped: Disadvantage on ability checks (including the second half of "attack rolls and ability checks"), an end anchored to the attacker's turn for a Speed or Disadvantage rider ("until the end of the cyclops's next turn"; a CONDITION with such an end is applied, see MONSTER_REMAINING_WORK.md), a rider with no duration, and Disadvantage on Strength-based D20 Tests (Weakening Breath).

Tested (`TestDisadvantageRiderParsing`, `TestPostgresSpeedOwner`): parsing of each text including the GM-left parts; the stored marker; the attack and Concentration saves reading it (an ordinary Constitution save does not); ending with the target's next turn; the Sap-like rider consumed once. Mutation-checked on the Concentration row. Not tested through a monster attack route end to end; not tested for source-anchored ends (GM).

## "Failure or Success:" riders

`parseSaveEffect` reads the "Failure or Success:" section with the same `readRider` as a failure. Conditions, a Speed rider and a Disadvantage rider found there are stored in `saveEffect.Always` and given by `giveAlways` to every target that rolled the save, whether it passed or failed, in both the character path (`applySaveEffect`) and the monster-target path (`applySaveEffectOnMonster`). The source-anchored end ("until the start of the lich's next turn") uses the same stored note as a failure condition. What is left after that (for example "can't take this action again", "can't take Reactions", a "cursed" choice, memory or corpse effects) stays in Extra for the GM, with the "Failure or Success:" prefix; the recharge restriction is the shared owner's (`monster_features_2024.go`).

Tested: `TestPostgresSpeedOwner` (parse into Always, what is left for the GM, source-timed Frightened and Speed 0 stored, an effect without the section gives nothing). Route-tested (`TestPostgresFailureOrSuccessRiders`, POST /api/gm/narrate): on a character target and on a monster target, with the save passed (DC 1) and failed (DC 40): Frightened stored source-timed from the caster and Speed 0 stored for 1 round in all four cases, the Failure damage follows the save (none on a pass), nothing applied is repeated to the GM, Speed 0 ends at the end of the target's next turn, Frightened ends at the start of the caster's next turn. Mutation-checked: removing the call from either path fails the test. The test found a real bug, fixed: a condition given by a save action is stored with the source "Caster (Action)", but the start-of-turn hook looked only for "Caster's Action", so source-timed conditions from save actions never ended (`sourceIsCreature` now accepts both). Not applied to hit riders (a hit has no "Failure or Success" part).

## Condition ownership by identity, and test isolation

- `condition_records.source_id` (new column, `conditionRecord.SourceID`) stores the creature that caused a condition (a monster's negative id). The start-of-turn hook (`monsterFeaturesTurnStart`) and `conditionSourceFighting` end and check "until the start of its next turn" conditions by that id. Records without an id (written before it existed) still end by the display text, in both forms the code writes ("Caster's Action" from attacks, "Caster (Action)" from save actions: `sourceIsCreature`). Tested in `TestPostgresFailureOrSuccessRiders`: the route-stored record carries the caster's id; a record whose text names nothing known ends by identity; both legacy text forms end; a record caused by another creature does not end at the caster's turn start. Mutation-checked (disabling the identity match fails the route test). Not yet moved to identity: the Sap combat effect (already id-based), the Speed/Disadvantage markers (target-anchored), other readers of `Source` text such as display and the GM conditions route.
- Test isolation: `TestPostgresCustomCompendium` counts and pages through the one global custom-monster table. It now sets aside the rows other tests left and restores them afterwards (`isolateCustomMonsters`), instead of relying on file order. Two full shuffled runs (`-shuffle=on`) passed. `TestPostgresBookRouteSweep` had a separate random flake (a critical hit plus a failed "Hit or Miss" save exceeds the hit-only damage bound), fixed in the test's bound.
- The GM compendium list is one catalog shared by all GMs (ChatGPT's proposal, kept as is; campaign-private storage is a different thing). Documented and unchanged.
- `source_id` namespace: positive = a character id (stable), negative = a monster id of the current fight; every lookup is also scoped by the campaign (`lobby_id`), so ids of different campaigns never meet. Monster ids are reused by the next fight, so `clearMonsterConditionRecords` (fight start and end) deletes the monsters' own records and sets the `source_id` of a character's records caused by a monster to `goneSourceID`: the text stays, nothing matches it, the hook never ends it, and the holder's turn tells the GM the source is gone (tested). Limitations: a character's condition from a character source is matched by id only while that id's creature is a monster (character-sourced `until_start_of_next_turn` is not read by this hook; none is written today); the display and GM conditions routes still show the Source text.

## Taking a hit back (Parry, Shield, Redirect Attack)

Owner: `cmd/server/monster_hit_undo_2024.go` (`snapshotTarget`, `recordChanges`, `undoRecordedHit`). The attacker (monster-vs-monster and the character attack route) snapshots the target before the hit and records what the hit changed on `charHitOnMonster`.

Given back when a Reaction turns the hit into a miss:
- The target's whole entry (Hit Points, Temporary Hit Points, Dead, form) when it is unchanged since the hit. Absorbed Hit Points and a hit that took it to 0 are covered this way.
- Condition records and combat effects the hit created (Prone, the hit's own save condition, linked Grappled+Restrained, Speed and Disadvantage markers, Sap).
- Records the damage removed (Sleep, Hypnotic Pattern, Turn Undead, charm on damage).
- Regeneration's block when this hit set it.

Kept: the attack's use (a (1/Day) attack stays counted) and any "Hit or Miss:" effect (GM note, not automated here).

Safety rule: the correction is tied to this hit's own snapshot and never guesses. If the target's entry changed after the hit (a heal, Temporary Hit Points from another source, another hit), nothing is restored and the reply says RECONCILIATION NEEDED: no Hit Point difference is ever applied. The records the hit created are removed by id; records the damage ended (Sleep ...) are put back only if the target's other conditions are exactly as the hit left them, otherwise the reply says RECONCILIATION NEEDED for them. The recorded hit is consumed by the first Reaction, so a second Reaction finds no hit and changes nothing (idempotent). Only the latest hit on a target is recorded.

Not covered, and said in the reply: effects of the hit this code does not track are the GM's to take back. Tracked monster Concentration now has a separate lifecycle owner (`monster_concentration_2024.go`), but undoing a recorded hit does not yet restore its Concentration consequences; that interaction remains an open review/test gap. A creature at 0 Hit Points cannot take a Reaction, so a lethal hit is tested on the undo directly, not through Parry.

Tests: `TestPostgresCharacterHitTakenBack` (character-attacker route: Temp HP restore, stale hit, duplicate Reaction) and `TestPostgresMonsterAttacksMonster` (route: stale and duplicate Reactions, Hit or Miss kept, Slam/Prone/Temp HP, Sting save, Sleep put back, absorption, (1/Day) counted; direct: lethal). Mutation-checked: records removal, Sleep restore, exact entry restore. Hit-or-Miss retention is tested as: the effect reaches the GM on the attack and the Parry reply keeps it with the GM (it is a GM note, never automated). Not yet tested at route level: Shield, Redirect Attack.

## Monster movement against Speed

Owner: `cmd/server/monster_move_2024.go` (`noteMonsterMoves`), called from the GM's battle-map update (PUT /battle-map) for each living monster token whose square changed, unless the update is `forced_movement`. It is a GM note (action type `monster_move`), not a refusal: the server cannot know how much Speed the monster already used this turn, Dash, Difficult Terrain along the path or a Fly/Swim/Climb Speed. It compares the straight-line distance (diagonals 5 ft, a lower bound of the path) with the walking Speed from the stat block after Speed effects (`effectiveSpeed`), flags a move over the Speed, a move at Speed 0, and a Speed combination that needs the GM's ruling. Test: `TestPostgresMonsterMoveSpeed` (route; mutation-checked by disabling the call).

Shield (a monster's "casts Counterspell or Shield" Reaction) is route-tested against a monster attacker in `TestPostgresMonsterAttacksMonster`: a hit the +5 AC turns into a miss is taken back; a hit that still beats AC + 5 stays a hit with nothing taken back (mutation-checked on the +5). Deflect Missile, Redirect Attack and Protection are not route-tested against monster attackers.

Deflect Missile ("reduces the damage it takes from the attack by ...") goes through `refundHitDamage`: it refunds Hit Points first and then Temporary Hit Points (damage emptied Temporary Hit Points first), never more than the hit took, and only while the target is as the hit left it (otherwise RECONCILIATION NEEDED, nothing applied). Before this, a hit absorbed by Temporary Hit Points got no reduction. The recorded hit is updated, so a later Parry on the same hit still restores the pre-hit entry exactly. Route-tested in `TestPostgresMonsterAttacksMonster` (mutation-checked). Redirect Attack and Protection: Redirect Attack is route-tested (the ally takes the damage the hit dealt including Temporary Hit Points; mutation-checked); Protection is not route-tested against monster attackers.

## "The target can't take Reactions"

Read in `disadvantage_riders_2024.go` (`noReactRe`): applied only when the same effect text gives an end of the TARGET's next turn (Slowing Ray, Slowing Breath style). It is a marker condition `no_reactions`, counted down like the Speed and Disadvantage markers. An end anchored to the attacker ("until the start of the balor's next turn", "until the curse ends") is not applied and is listed for the GM; before this change such a sentence was dropped silently when it was short (found by `TestPostgresCantTakeReactionsRider`, fixed in `readRider`'s remainder).

Enforced through shared checks: `checkActionEconomy` (POST /api/action), `featureTurnCost` and `turnCost.spendIn` (every feature route that spends the Reaction through them, e.g. `spendTurnResource`), `reactionReady` and `reactionSpent`, plus the monster Reaction resolver, the Opportunity Attack window and the routes that read `reaction_used` themselves (readied actions in actions.go and gm_reactions.go, Uncanny Dodge / Retaliation routes in gm_reactions.go, counterspell in gm_2024.go, Deflect Attacks in monk_extras_2024.go, Superior Hunter's Defense in sweep_2024.go, Slow Fall in gm_environment.go). Entry-point proof: `TestPostgresCantTakeReactionsRider` exercises the monster resolver, `reactionReady`, `checkActionEconomy`, `featureTurnCost` and `spendTurnResource` (a refused Reaction is not marked used). The individual routes in the second list are wired by one line each and are NOT individually route-tested. Companions' own Reactions are not covered. The GM remainder of a handled sentence still repeats the whole sentence (applied parts included); the non-applied part, e.g. "it can take either an action or a Bonus Action, not both", is in it.


## Redirect Attack and Protection: what is carried to the new target

Redirect Attack (supplied wording: "The goblin chooses a Small or Medium ally within 5 feet of itself. The goblin and that ally swap places, and the ally becomes the target of the attack instead.") makes the ally the target of the SAME attack roll. So: the roll's total is compared with the ally's AC (a natural 20 hits); the first target keeps nothing of the hit (`undoRecordedHit`); the ally takes the damage as rolled BEFORE the first target's defenses (`charHitOnMonster.Parts`) through the normal damage owner, so the ally's resistance, immunity, vulnerability, absorption and Temporary Hit Points decide the result; the stat block's hit rider is given to the ally by the normal rules (size gate, immunities). Not transferred, flagged to the GM: a character attacker's hit (its damage is built in attack_target.go, no raw parts are recorded) and a critical hit that came from the first target's state (it is not a natural 20). The roll's own Advantage or Disadvantage was decided against the first target and is kept as rolled. Route-tested (`TestPostgresMonsterAttacksMonster`): the first target resists, the ally does not (the ally takes the full damage), the ally gets Prone, and an AC the roll cannot beat is a miss with nothing changed (mutation-checked on the damage and on the rider).

Protection ("An attack roll hits the wearer of the guardian's amulet while the wearer is within 5 feet of the guardian. Response: The wearer gains a +5 bonus to AC ...") is a monster guardian (defender-side Reaction) for a CHARACTER wearer hit by a MONSTER attacker. `TestPostgresProtectionVersusMonsterAttacker` covers it: a hit the +5 turns into a miss is undone (Hit Points and the hit's rider conditions, by id), a hit that still beats AC + 5 is left alone (mutation-checked on the rider removal). The same applies to a cast Shield (`shieldReaction`). Found by this work: a hit's conditions were applied after the hit was recorded, so a Shield or Protection that made it a miss left them on the wearer; and a Shield cast by standing order still got the hit's riders applied. Both are fixed and both are route-tested (the standing-order case with a wizard that has a Shield standing order; mutation-checked on the "hit became a miss" guard). Not covered: a wearer that is a monster hit by a character or monster (the wearer must be a character today: a monster wearer is not supported and not tested).
