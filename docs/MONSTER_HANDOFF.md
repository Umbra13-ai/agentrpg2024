# Monster system: consolidated handoff

## Combined monster effects checkpoint

Code `bfae58940e238768b65e8fe1c94082f5daeeff98` was tested in tree `f8e91e0bd7af77205ebb599f6f58fb0ae2ff5b24`. The complete `go test -race ./... -count=1 -shuffle=on -json` run passed 1,086 cases, with two external-server skips and no race reports. The server package took 455.270 seconds. Vet, formatting and API generation passed. Nine direct damage, preview and type mutations and the separate Immutable Form mutation triggered test assertions and were restored. See `VALIDATION-bfae589.json` and the named design documents for narrower route and mutation scopes.

This remains a proposed patch using one application database. The expanded Iron Golem attack, recharge and Magic Resistance probes are in a later worktree. Those results are separate from this full run.


## Monster effects and rules checkpoint in validation

The proposal now includes repairs for damage-only deliveries, save previews, Legendary Action previews, retained creature type under SRD 5.2.1, and Immutable Form. The latest code and documentation commits are recorded in the checkpoint manifest. The direct-damage path uses existing HP owners for character, monster and companion targets. A pending Concentration decision keeps later targets in saved work with the original damage roll. Compound clauses needing an unresolved trigger are referred to the GM.

The named before-and-after probes are in `MONSTER_RETAINED_TYPE.md` and `MONSTER_IMMUTABLE_FORM_REVIEW.md` (the direct-damage design was archived: no SRD entry used it). Nine deliberately reintroduced defects in the direct, preview, and type repairs triggered test assertions and were restored. An additional Immutable Form mutation is recorded separately. The 20 selected-mechanics cases passed on the previous full checkpoint. A complete combined suite is being run before this code is promoted to the next validated checkpoint.


## Source-turn timing checkpoint

Code `0b1fc1e197cbbfd6d79c0529ee32e979cd67fb36` passed 1,075 cases in the complete shuffled race suite, with two external-server skips and no race reports. The server package took 412.243 seconds. Four deliberately reintroduced timing defects triggered independent assertions, and the original source was restored each time. See `SOURCE_TURN_TIMING_DESIGN.md` and `VALIDATION-0b1fc1e.json` for route scopes and evidence. The separate compendium still uses the existing database.

The next worktree reproduced damage-only issues through GM narration. Preview changed monster HP, applied actions skipped companions and did not count the daily use, and later targets took damage during pending Concentration. Those findings and subsequent repair tests are documented independently of this checkpoint.


## Completed storage-boundary checkpoint (2026-11-18)

Code `7c38ef1e88bc8bfbf3ac98b2565e2c2451ae19fb` passed a clean complete `go test -race ./... -count=1 -shuffle=on -json`: **1,066 passes, 2 external-server skips, 0 race reports**. Server elapsed 410.817s; shuffle seed 1795028088336632371. Vet/formatting pass; eight introduced regressions caught and restored; all three patches reproduce all 612 archive files. Exact documentation commit is recorded separately in the export manifest. Evidence: `VALIDATION-7c38ef1.json`, `full-journal-race.jsonl`, `journal-patch-application.json`, and the journal mutation directory.

This closes the specifically reproduced damage/Concentration storage boundary and immediate Incapacitated cleanup defects for the named controls. New source-turn probes are being developed in a separate child worktree; their results must not be attributed to this validated code. No release sign-off. The prior in-progress notes below are historical development evidence.


## Active storage-boundary repair (2026-11-18, not signed off)

Work continues in the separate `monster-durable-concentration-guard` copy. The failed Concentration write is now journaled in the same existing-database transaction as the triggering HP/condition change; later effects stop, and interrupted work survives a fresh process. The shared condition path also fixes a separately reproduced defect: Incapacitated previously left a tracked cast active. GM-only cast inspection/identity-checked voluntary ending and token-checked review of interrupted work are implemented. `reconcile` acknowledges manual completion; `reconcile_and_resume` continues the known remainder without replaying uncertain damage/costs. Both end-cast and Legendary Resistance recovery workflows pass.

Current focused result: 53 passes, zero failures (`journal-focused-4.jsonl`). The prior run's single failure was a fixture rejected for importing save +200 (allowed -10..30); +30 is legal and still guarantees the intended DC10 success. Production validation was not weakened. Eight deliberate regressions were caught by assertions (including a paired status+token mutation); restored source hashes match. A clean complete race/shuffle suite is still pending; no current full-suite or monster-acceptance claim. See `CONCENTRATION_STORAGE_BOUNDARY_DESIGN.md`. Exact code/docs hashes will be captured in the next external checkpoint manifest after validation.

All other existing open labels remain unless a named newer test closes them. Original/upstream/live systems and other AIs' artifacts remain untouched.


## Continuation checkpoint: shared combat pause (2026-11-18, WIP)

Latest fully run code checkpoint: `59f89e816320706f337491fc07c2e28dbf87c09f` (1,053 race/shuffle passes, 2 external-server skips, no race reports; vet passes). The three patches reproduce its 608-file archive exactly. Twenty-five deliberate regressions were caught in two working-tree batches. This documentation commit adds the exact result; its hash is recorded in the external checkpoint manifest.

**A newer storage-failure probe FAILS on that code:** HP commits, Concentration persistence fails and rolls back the continuation, then Command still executes. See `COMBAT_PAUSE_DESIGN.md`'s final section and the separate `journal-work` branch/worktree. This is an explicit release blocker. The preceding full-suite pass does not cover that subsequently written probe. No sign-off.


The earlier completed race/shuffle checkpoint was `3c62f5ce75e9e57906581f8629b30034b11c633c`; the named 59f89e8 run above supersedes it for that code. These following working notes describe development before that run and are not release approval. It adds campaign command/clock guards, typed persisted turn/attack/save/companion continuations, GM resume/reconciliation, and an explicit boundary for failed request costs. All schema additions remain in the existing database. No upstream, original Drive artifact or live server was changed.

Read `COMBAT_PAUSE_DESIGN.md` for exact before/after probes, fixture mistakes and corrections, mutation evidence and remaining paths. Twenty mutations were caught before the later private-notice/companion changes. Focused current tests cover real player attacks, monster multiattacks, saving-throw components/targets, multiple aura sources, turn countdowns, cold-process resume, private GM notices, companion/summon plans and Wild Shape.

The first full shuffled run had 1,046 passes and two integration-test failures, corrected in focused runs. The following race/shuffle run stopped after 800 passes / 2 external-server skips at a fixture panic; a diagnostic proved a fixed character name caused a 409, and the fixture now uses a unique campaign name and checks status (repeat run passes). That checkpoint subsequently passed the named full run above; the separate newer journal repair needs its own clean complete suite. Exact code/docs commit, logs, source snapshot and patch belong in a separate checkpoint manifest, not a self-referential hash in this file.

Continue in the separate copy: finish Flurry, spell/item/reflection/map-entry sequences; reproduce/close persistence-failure and source-turn rider gaps; mutation-check the later additions; then re-run the complete suite. The pending lifecycle boundary is not yet closed. Existing concentration/reaction undo, durable notice fallback and other open labels below remain open unless a newer named test proves otherwise.

None is a new behavioral pass. Never call import/selected-attack sweeps a full monster pass.


## Validated repair checkpoint (2026-11-18)

Six reproduced defects are repaired in the separate successor copy: the five concentration failures plus stale snapshots restoring resistance uses. The complete race-enabled suite passed (1,031 cases; 2 external-server skips), vet and formatting passed, and 18 targeted mutations were caught. Code and documentation are committed together; exact commit and patch baselines are in the checkpoint bundle manifest. See MONSTER_REPAIR_LOG.md for scope, failed-run evidence and mutation limitations. The earlier work-in-progress notes below are historical; this validation update supersedes their test-status statements.

## 2026-11-18 repair checkpoint (work in progress)

A separate successor copy repairs five independently reproduced concentration defects. Cast and decision identities, damage-time saved outcomes, transactional cleanup and resistance spending are implemented in the shared owner. Five regression probes and focused lifecycle/concurrency/storage/cast controls pass. See MONSTER_REPAIR_LOG.md for evidence and limitations. Broad validation and mutations are still pending at this edit. The pending-dependent progression boundary remains OPEN; do not describe the lifecycle or the overall monster patch as release-ready. Earlier sections below describe the supplied 570f8bb checkpoint unless this update supersedes them.


Working copy: branch monster-fix-instinct (separate copy of the code). Nothing was deployed, pushed or changed upstream; the original code,
other AIs' files and the live server are untouched. Detail and history: docs/MONSTER_REMAINING_WORK.md and docs/MONSTER_SYSTEM.md.

## Objective (owner, Nov 21: scope change)
Every one of the 330 SRD 5.2.1 stat blocks (`cmd/server/data/srd521_monsters.json`) works through the shared combat rules, with no
monster-specific code: every sentence is applied by a named rule or flagged to the GM, nothing silent (`TestSRDMonsterSentenceCoverage`).
The earlier 2024 Monster Manual work is stopped and archived outside the repository; every monster text pattern must match an SRD stat
block or an invented test monster (`TestMonsterPatternsServeSRDWording`). The GM import route stays, as a generic feature for a GM's own
monsters.

## Provenance
Rules are read from the SRD 5.2.1 data in the repository. Tests use the SRD stat blocks or invented text with SRD wording.

## What is automated (route-tested: a test drives the real HTTP route and checks stored state)
- Attack rolls from stat-block text against characters, companions and monsters: damage, resistance, immunity, vulnerability, absorption,
  Temp HP, Multiattack, damage alternatives, per-day uses.
- Save actions and hit riders: conditions (size-gated, immunity-aware), Speed, Disadvantage riders, "can't take Reactions", source-timed ends.
- Reactions to a monster's hit: Parry, Shield, Deflect Missile, Redirect Attack, Protection and Shield for a character wearer.
- Area effects (Emanation, Sphere, Line, Cone with a point) reach characters and companions, campaign-scoped.
- Companions as targets: reach and range, resistances, saves, conditions, Magic Resistance, AC, creature-type eligibility for type-restricted spells.
- Type-restricted spells (Hold Person and four more): eligibility from stat-block type, companion form, or a character's SRD species type;
  Wild Shape and Polymorph retain the creature's type (SRD 5.2.1 pp. 43, 153). An unknown original type needs a GM ruling. See `MONSTER_RETAINED_TYPE.md`.
- Audience: the shared action log never stores a bracketed GM note (split before storage, also at the log writer); the GM gets it as a
  private notice. Player log reads strip notes as a second layer.

- Monster Concentration (server-resolved spells): recorded, one at a time, Constitution save on damage, ended by a failed save, death or Incapacitated, with the conditions that spell put on others; private notices only.

## Explicit GM-only handoffs (the reply or a private notice says so; nothing is dropped)
"Hit or Miss" effects on other creatures, pushes, unparsed sentence parts, attacker-anchored Speed/Disadvantage ends, ability-check
Disadvantage, Speed combinations that need a ruling, Protection with a monster wearer, spell attacks and Command at a companion,
unknown-type spell targets, size-gated conditions on companions, Legendary Resistance on a companion.

## Unsupported
- Monster Concentration for anything outside the server's spell path (a trait, GM words, an item): only the conditional GM reminder exists there. Server-resolved Concentration spells ARE tracked (see below).
- Legendary and lair actions, recharge details, auras and most free-form traits beyond what the shared owners handle.
- Monster movement enforcement (a note only).
- Spell restrictions beyond five creature-type spells, forms other than Polymorph and Wild Shape.

## Coverage levels (never merged)
Not mechanics coverage.
- Attack-roll route sweep: 123 attack rolls automated and route-exercised. Spellcasting route sweep: 103 rolled by shared rules, 219 handed to
  the GM with the spell named, 1 refused (ineligible type), 0 silent.
- Repo test functions: 505 in cmd/server. Last clean full run: see the latest commit in the report (-shuffle=on).
- Many scenarios are single-scenario route tests with mutation checks; each doc entry says whether the mutation isolates the scenario.
- Random rolls: attack tests retry on a natural 1 and some re-roll up to 250 times; probabilistic, not forced.

## Unresolved
- UNEXPLAINED, still open: TestPostgresCustomCompendium and TestPostgresImportedAttackRulesThroughTheGMRoute (seen once, ~6:08, not
  reproduced), and the first full run on commit c5e4d3d.
- Flakes with INFERRED causes only: spell test after tests that overwrite the spell cache; concentration low-HP fixture; stale
  thisTurnHitOnMonster record in a retry loop (not proven either way; the keyed-record argument does not rule out contamination).
- Held notices are memory-only if the notices insert fails; whether a durable fallback is needed is open, a failure-injection test is not written.
- The characters table has no creature_type column; getTargetCreatureType's read of it is dead (left unchanged).

## Highest-value remaining implementable gap
Legendary and lair actions: today most are GM-handled; the shared owners could run the stat block's legendary action list on the right turn boundary. Also: Legendary Resistance choice on the Concentration save, a deterministic stale-hit-record test, a database-failure injection test for held notices.

## Artifacts (Matt's shared Drive folder 1qX-RHYDTni2ItuKzAplS5ZtOg6p_Lfja)
Source zip, combined patch vs a61fa12, patch of the fixes on ChatGPT 1.5.0, MONSTER_REMAINING_WORK.md, this file. Stale and not refreshed:
SAVE_ROUTE, SAVE_ACTIONS, ROUTE_SWEEP, SPELLCASTING, README, MONSTER_SYSTEM.md copies in Drive (the repo versions are current).

## Reference ability checkpoint 16d3c4c

Code `16d3c4c4a3d8a1141ef9eb067e236b0152f64d81` passed 1,099 shuffled race cases with two external-server skips and zero race reports. Three isolated mutations detected the regressions in explicitly magical saves, companion condition immunity and Exhaustion immunity. The originals were restored. See `IRON_GOLEM_ABILITY_PROGRESS.md` for the exact scope of the seven named ability checks, fixture corrections and open interactions.

The twenty selected-mechanics cases passed. The sources and evidence for that work are tracked separately until validated.

## Fresh-candidate combat checkpoint f1b3fb2

The full PostgreSQL suite at code `f1b3fb2a0075219a934a62be617c91faabb9f71c` passed 1,108 cases with two external-server skips and no race reports. The server suite took 510.581 seconds. Its shuffle seed was 1795044778117150366. Static vet and whitespace checks passed.

The shared HP and attack owners now apply the tested damage-triggered Disadvantage rule. Success immunities use their stated duration and separate action keys. Unresolved immunity duration and applicable manual ability checks reach the GM privately.

The first fresh candidate failed its first run. Its targeted rerun covered the repaired damage trigger and save effects. Later independent targeting checks uncovered missing range, target-count and sight requirements. Those targeting changes are being tested in a separate worktree and are outside this complete-run result.


## Movement, aura and GM charge checkpoint

Code `3f1d93b8497129b0e61305e53d46160da9fb42b3` passed 1,127 cases in a clean PostgreSQL race and shuffle run. The two skips are external-server endpoint checks. No race reports occurred. An earlier full run at `8126c1b` had 1,126 passes, two skips and one failure from the central GM scope registry. The condition source is registered with that guard in the validated code. The failing and corrected runs are retained.

The patch gates saves that require a verified move, delivers supported mapped end-of-turn damage to characters, monsters and companions through the shared damage owner, and resumes later aura effects after a pending Concentration decision. It also applies continuous mapped monster light to attack visibility, records a GM-adjudicated Recharge use and preserves campaign-scoped source and grapple links. The approved manual charge workflow updates affected HP, mapped position, linked conditions and Recharge in the same application database. See `BRAZEN_GORGON_FIRST_RUN.md` for the exact route and fixture scope.

The supported failed-save charge path was tested. Successful saves, additional eligible targets and an occupied grapple slot still require checks. Separate range and real initiative Recharge probes ran on a successor test branch.
