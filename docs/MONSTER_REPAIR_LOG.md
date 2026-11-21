# Concentration repair log

## Combined monster effects checkpoint

Code `bfae58940e238768b65e8fe1c94082f5daeeff98` was tested in tree `f8e91e0bd7af77205ebb599f6f58fb0ae2ff5b24`. The complete `go test -race ./... -count=1 -shuffle=on -json` run passed 1,086 cases, with two external-server skips and no race reports. The server package took 455.270 seconds. All 20 selected-mechanics cases passed with the four private fixtures enabled. Vet, formatting and API generation passed. Nine direct damage, preview and type mutations and the separate Immutable Form mutation triggered test assertions and were restored. See `VALIDATION-bfae589.json` and the named design documents for narrower route and mutation scopes.

This remains a proposed patch using one application database. The expanded Iron Golem attack, recharge and Magic Resistance probes are in a later worktree. Those results are separate from this full run. The complete ability and unique replacement acceptance requirements remain open.


## Monster effects and rules checkpoint in validation

The proposal now includes repairs for damage-only deliveries, save previews, Legendary Action previews, retained creature type under SRD 5.2.1, and Immutable Form. The latest code and documentation commits are recorded in the checkpoint manifest. The direct-damage path uses existing HP owners for character, monster and companion targets. A pending Concentration decision keeps later targets in saved work with the original damage roll. Compound clauses needing an unresolved trigger are referred to the GM.

The named before-and-after probes are in `MONSTER_DIRECT_DAMAGE_DESIGN.md`, `MONSTER_RETAINED_TYPE.md`, and `MONSTER_IMMUTABLE_FORM_REVIEW.md`. Nine deliberately reintroduced defects in the direct, preview, and type repairs triggered test assertions and were restored. An additional Immutable Form mutation is recorded separately. The supplied reconstructed Iron Golem has route tests for Fire Absorption and Immutable Form. The 20 selected-mechanics cases passed on the previous full checkpoint. Complete ability testing for all required monsters and fresh replacement rounds remain open. A complete combined suite is being run before this code is promoted to the next validated checkpoint.


## Source-turn timing checkpoint

Code `0b1fc1e197cbbfd6d79c0529ee32e979cd67fb36` passed 1,075 cases in the complete shuffled race suite, with two external-server skips and no race reports. The server package took 412.243 seconds. The 20 selected-mechanics monster cases passed with all four private fixtures enabled. Four deliberately reintroduced timing defects triggered independent assertions, and the original source was restored each time. See `SOURCE_TURN_TIMING_DESIGN.md` and `VALIDATION-0b1fc1e.json` for route scopes and evidence. The separate compendium still uses the existing database. Complete ability coverage for twenty monsters and the unique replacement rounds remain open.

The next worktree reproduced damage-only issues through GM narration. Preview changed monster HP, applied actions skipped companions and did not count the daily use, and later targets took damage during pending Concentration. Those findings and subsequent repair tests are documented independently of this checkpoint.


Working baseline: supplied archive 570f8bba2d245907f049a2d229d376ab6374c7d2. Work is a new copy in `/workspace/monster-lifecycle-repair/source`. No deployment, upstream writes, or changes to existing Drive artifacts. This log records work in progress, not release approval.

## 2026-11-18: reproduced failures and first repair

Independent review preserved five failing route probes: same-name recast retains earlier targets; deferred saves use later stats; a failed decision write panics; failed deletion partially removes effects; and damage saves omit the Concentration context. Exact baseline evidence is in the separate independent-review package in Matt's patch folder.

Rule source: supplied SRD 5.2.1, printed p.179, Rules Glossary / Concentration. Another Concentration cast ends the previous effect when casting starts; damage requires a Constitution save with the stated DC; Incapacitated/dead ends it. No new per-monster exception or rule inferred from memory.

Implementation in the shared concentration owner:

- Each new cast has a random identity. Old recorded effects are cleaned up before the new spell resolves, including a same-name recast and a cast at shielded targets. A blocked caster does not start a new cast.
- Each damage save uses `saveCtx` with `concentration: true` and stores the complete `game.SaveResult`, including gathered modifiers/reasons, dice and outcome. Later GM decisions do not reroll it.
- Lifecycle changes use a transaction and a per-caster advisory lock. Cleanup calls the existing condition owner inside that transaction so character condition lists stay synchronized. The record, spell effects and conditions roll back together on a failure.
- Legendary Resistance spending, the decision claim, queued outcomes and action log are committed together. Decision tokens identify individual failures; clients should echo the token from the private notice, preventing a retry from answering a later failure.
- Three additive columns in the existing application database store cast and decision tokens plus the first pending save as JSONB. This introduces no new database.
- Legacy DC-only or malformed queues are retained and require GM adjudication. The server refuses a resistance decision with `queue_needs_adjudication`; ending the cast remains available. No state is guessed or silently discarded.

First focused run: seven tests passed against a fresh PostgreSQL database (`first-repair.log`): the five review probes plus existing lifecycle and tracking tests. The lifecycle test's queue assertions now decode stored results and verify the original -10 bonus and failed outcomes rather than comparing DC-only serialization. At this stage no full-suite, race-suite, mutation, or concurrency sign-off is claimed.

Still open: pending-dependent progression boundary, concurrency controls, restart and legacy-queue controls, shielded/blocked cast coverage, final API/docs generation and broad validation. Full monster ability conformance and Matt's replacement-monster loop remain incomplete.

## Broad-run findings, isolation and additional shared-count repair

The first full shuffled run of the repair failed in two existing tests (`full-suite.jsonl`, retained). `TestPostgresMonsterSpellCharacterType` counted a legitimate same-name recast cleanup notice as a type-eligibility warning: its scenarios now explicitly end the previous cast before measuring the next isolated scenario, preserving all eligibility assertions. `TestPostgresCompanionConditionScope` assumed character and companion sequences happened to collide: its fixture now explicitly creates a minimal colliding character only when needed, advances the sequence safely and cleans that row up. Both pass in the focused rerun. These findings do not explain the historically unexplained failures listed in the older handoff.

A new fresh-process test starts the real startup/schema/rules and router in a child process using the same disposable database, with no inherited package globals or connection pool. It reloads the first failed save, queued save, rule sources and identities, then answers one decision. It passes. This is a cold-process persistence test, not a live deployment or crash-recovery test midway through a transaction.

Thirteen deliberate concentration regressions were caught by their intended tests (`mutations/results.json`). These cover recast replacement, deferred saving throws, write-failure panic, cleanup after a failed delete, missing Concentration context, stale tokens, split transactions, legacy queues, missing tokens, alternate-route wiring, first-save persistence and shielded-target casting. The transaction-splitting mutants fail state invariants but do not isolate the precise injected SQL trigger: they deliberately commit too early and then fail further work. Other mutations target their named scenario directly. No probabilistic roll is needed for the changed-state save assertions (-10 always fails DC 30; +30 always passes).

A sixth defect was reproduced deterministically (`resistance-stale-before.log`): a general monster-state snapshot restored a Legendary Resistance use spent after that snapshot was read. `monster_resistance_2024.go` is now the one locked count owner. Concentration joins that owner to its lifecycle transaction; automatic saves, the ordinary GM command and the item consumer use its transaction wrapper. The general state writer preserves the existing count. Direct test-fixture counter resets are explicit fixture SQL, not production rules. The regression passes; a consumer test drives the ordinary GM command and the automatic monster-save effect route, and the Vorpal consumer at helper level. It does NOT claim a complete equipped-item attack route.

At this checkpoint, the five original review probes and focused concurrency, storage rollback, token, cold-process, legacy-queue and cast controls pass. Final broad/race validation and exact commit identifiers will be recorded after they finish.

## Remaining blockers and limits

- Pending Concentration retains its effects while awaiting GM adjudication. There is still no comprehensive enforced boundary across dependent actions, saves, timers, turn changes and time passing. A private instruction to resolve it first is not equivalent to that boundary.
- Damage application precedes the concentration hook; the complete damage request is not one transaction with the concentration record and notices. The new atomicity guarantee covers concentration decisions and recorded-effect cleanup, not every statement in a cast/damage route.
- Per-hit versus per-component damage grouping follows existing hook calls. This change does not establish new rules for grouping damage packets. No claim is made that every entry route groups them correctly.
- Concurrent decisions with tokens are tested. Concurrent cast/damage/turn progression as a whole is not proven; a cast identity prevents an old damage hook from changing a later cast.
- Notices still have the supplied implementation's memory-only fallback on failed insertion. Durable fallback, deterministic stale-hit coverage, legendary/lair completeness, all-ability coverage and the unique replacement-monster acceptance loop remain open.

## Shared counter consumer checks

The stale-count regression passed after centralization. `TestPostgresMonsterResistanceConsumers` passes: ordinary GM use, an automatic save-effect use through `gm/narrate`, the Vorpal helper consuming the final use, and exhaustion refusal. Four additional deliberate defects (stale-state overwrite and each spending consumer bypassing the count owner) each failed that test's relevant assertions. Together with the first concentration pass this is 17 purposeful mutations caught; concentration mutations are also being rerun against the integrated owner before final broad validation.

New pending decisions now require `decision_token`; missing tokens on both GM decision endpoints are refused without spending. This is an intentional proposed API contract change for the unreleased feature. Legacy stored pending rows without a token remain adjudicable, while legacy DC-only queues are never automatically rerolled.

## Final validation of this repair checkpoint

- Complete `go test -race ./... -count=1 -shuffle=on -json`: **1,031 test cases passed, 2 skipped, no failures or race reports**. Server package 312.814 seconds. The skips are the external running-server Health and Version tests; no live server was used.
- A fresh PostgreSQL test database was used with `REQUIRE_TEST_DATABASE=1`, `REQUIRE_TWENTY_MONSTERS=1`, all four original reference fixtures, and the private 91-monster set enabled. These case counts include subtests and do not mean 1,031 monsters or full ability coverage.
- `go vet ./...` passed. All 14 changed/new Go files were formatted. Swagger regenerated successfully (304 operations, 290 paths).
- **18 distinct deliberate regressions caught**. The 13 concentration mutations were rerun after counter integration; the four counter-consumer mutations and private-token delivery mutation also failed their intended tests. Logs and transformation scripts are in the checkpoint evidence bundle.
- The GM receives the required token through the real private notices route; a campaign player is refused that route and their `my-turn` response contains no token. Omitting token delivery makes the positive control fail.

Server shuffle: -test.shuffle 1794997869726373527. The exact code/docs commit is recorded in the bundle's checkpoint manifest and Git archive comment (one commit contains both code and these docs). The first failed broad run is retained separately from the passing final run.

This remains a **repair checkpoint, not release approval**. Pending-dependent progression is still an open blocker. Full monster ability conformance, daily reset/carryover validation and the unique replacement-monster loop remain incomplete. Next implementation priority: the shared pending-adjudication boundary; recommended model/effort: GPT-6.2-sol, high.

## Pending execution boundary in progress (2026-11-18)

Continuation work now has retained before/after probes for turn effects and monster saving-throw damage/targets. See `COMBAT_PAUSE_DESIGN.md` for exact assertions, fixture failures, evidence filenames and gaps. It is uncommitted and has not passed the full suite. The preceding checkpoint's race/shuffle result must not be attributed to this working tree. This work does not satisfy the 20-monster every-ability/replacement acceptance requirement yet.


## Pause checkpoint 59f89e8 and a newly reproduced blocker

Code commit 59f89e816320706f337491fc07c2e28dbf87c09f passes the complete shuffled race suite (1,053 cases, two external-server skips, zero race reports) and vet. All three patch exports reproduce all 608 archived source files. Twenty-five introduced regressions were caught across two batches; their exact evidence/limits are in COMBAT_PAUSE_DESIGN.md.

After that green run, a new scoped SQL-failure probe on the same code reproduced a storage boundary defect: Burning HP commits, a failed Concentration write rolls back the continuation and decision, then Command executes. Its 500 response does not prevent that extra state change. The probe is retained separately for the next repair and is NOT counted in the earlier green suite. This does not resolve the original handoff's other open/unexplained items or the 20-monster full-ability/replacement acceptance. No release approval.

## Storage-boundary checkpoint 7c38ef1, complete suite

A same-transaction journal in the existing application database now persists the obligation to settle a tracked monster cast before committing triggering damage/death/Incapacitated changes. Original save facts persist before the lifecycle transaction. The shared pause owner stops dependent progression on failure; GM review/cast identities prevent stale recovery, and known remainder can resume without repeating uncertain damage. A separate probe reproduced and fixed immediate Incapacitated cleanup. See CONCENTRATION_STORAGE_BOUNDARY_DESIGN.md for ownership, real route/helper scopes, error controls, fresh-process recovery, paired mutation limits and retained failing logs.

Code 7c38ef1e88bc8bfbf3ac98b2565e2c2451ae19fb: full race/shuffle 1,066 passes, two external-server skips, zero race reports; vet/formatting pass. Eight deliberate regressions caught by assertions, originals restored. Three baseline patches reproduce 612 archived files exactly. No live/upstream changes and no second application database. Other open paths and twenty-monster all-ability/unique replacement gate remain incomplete; not release-ready.

## Reference ability checkpoint 16d3c4c

Code `16d3c4c4a3d8a1141ef9eb067e236b0152f64d81` passed 1,099 shuffled race cases with two external-server skips and zero race reports. Three isolated mutations detected the regressions in explicitly magical saves, companion condition immunity and Exhaustion immunity. The originals were restored. See `IRON_GOLEM_ABILITY_PROGRESS.md` for the exact scope of the seven named ability checks, fixture corrections and open interactions.

The twenty selected-mechanics cases passed. The every-ability gate and first-run replacement loop are open. A fresh CR 9 private import reproduced further rule defects after this checkpoint. The sources and evidence for that work are tracked separately until validated.

## Fresh-candidate combat checkpoint f1b3fb2

The full PostgreSQL suite at code `f1b3fb2a0075219a934a62be617c91faabb9f71c` passed 1,108 cases with two external-server skips and no race reports. The server suite took 510.581 seconds. Its shuffle seed was 1795044778117150366. Static vet and whitespace checks passed. The 20 selected-mechanics cases and the named fresh candidate probes ran with the private fixtures enabled.

The shared HP and attack owners now apply the tested damage-triggered Disadvantage rule. Condition riders can honor a damage immunity prerequisite, and success immunities use their stated duration and separate action keys. Unresolved immunity duration and applicable manual ability checks reach the GM privately. See `FRESH_MONSTER_FIRST_RUN.md` and `MONSTER_ACCEPTANCE_LEDGER.md` for the evidence and scopes.

The first fresh candidate failed its first run. Its targeted rerun covered the repaired damage trigger and save effects. Later independent targeting checks uncovered missing range, target-count and sight requirements. Those targeting changes are being tested in a separate worktree and are outside this complete-run result. The every-ability and fresh replacement acceptance gates remain open.


## Movement, aura and GM charge checkpoint

Code `3f1d93b8497129b0e61305e53d46160da9fb42b3` passed 1,127 cases in a clean PostgreSQL race and shuffle run. The two skips are external-server endpoint checks. No race reports occurred. The 20 selected-mechanics cases and the named reconstructed-monster checks ran with the private fixtures. An earlier full run at `8126c1b` had 1,126 passes, two skips and one failure from the central GM scope registry. The condition source is registered with that guard in the validated code. The failing and corrected runs are retained.

The patch gates saves that require a verified move, delivers supported mapped end-of-turn damage to characters, monsters and companions through the shared damage owner, and resumes later aura effects after a pending Concentration decision. It also applies continuous mapped monster light to attack visibility, records a GM-adjudicated Recharge use and preserves campaign-scoped source and grapple links. The approved manual charge workflow updates affected HP, mapped position, linked conditions and Recharge in the same application database. See `BRAZEN_GORGON_FIRST_RUN.md` for the exact route and fixture scope.

The supported failed-save charge path was tested. Successful saves, additional eligible targets and an occupied grapple slot still require checks. Separate range and real initiative Recharge probes ran on a successor test branch. The first-run attempts for the Yeti, Gorgon and Gray Slaad all failed. The Gray Slaad fixes remain in a separate worktree. Zero complete every-ability passes and zero fully passing first-run replacements have been certified.
