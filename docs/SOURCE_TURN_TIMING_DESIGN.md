# Source-turn rider timing review

Separate proposed worktree, based on code 7c38ef1. The storage repair's full suite is running against its unchanged tree. These new probes have NOT run yet; no reproduced/fixed claim at this edit.

Code reading: imported companion and Wild Shape attacks now use the shared hit rider owner, carrying positive source ids. However monsterFeaturesTurnStart and monsterFeaturesTurnEnd expire source-anchored conditions only for negative monster ids; conditionSourceFighting also excludes all positive ids. A companion or character source can therefore be incorrectly reported absent, and the timed effect may never end. Test through companion reaction attack and authenticated turn advance, plus a character-source component/route control. Do not confuse a helper-created rider with attack-route proof.

The source's identity, start/end boundary and current-turn skip marker must have one owner for all initiative creatures. Source HP/status comes from the existing initiative-death owner. Retain legacy name fallback only for old monster records; positive ids must not match by name. Campaign filtering remains required.

Companions without an individual initiative entry are an explicit server-setting limit. Do not infer that an owner's turn is the companion's next turn. Keep the effect and immediately notify the GM privately that its source timing requires adjudication; the existing GM condition editor can end it. A real individual companion turn may expire it automatically. No new rules or per-monster exceptions.

The legacy `TestPostgresTwentyMonsterAcceptance` name is retained for existing scripts. Its comment/log now explicitly says selected attacks/defenses only; it cannot establish every-ability acceptance or unique replacement passes. Existing assertions are unchanged. No book-derived test text is added.

## Reproduction and repair

The original three probes failed. A real companion attack stored the positive source ID, then the server reported the living companion absent despite its own initiative slot. The character component probes produced the same false absence at both edges. A companion without a slot did not create a private notice when the condition was applied. The original log is `source-turn-before.log`.

`condition_source_turn_2024.go` owns expiry for the existing start and end hooks. Exact source IDs work for monster, character and companion slots. Legacy name matching is restricted to monster entries and rows without an ID. The stored skip marker preserves the effect through the current source turn. The condition writer reads the turn before opening its write transaction. Companion sources with no initiative slot receive an immediate private ruling request after the condition commits. The holder keeps the condition through an unrelated turn. The character setup uses the shared rider parser as a component fixture. The companion setup exercises the actual attack and GM turn-advance routes.

First focused run passed the new probes and exposed an existing notice wording check. The change now preserves the established indication that the effect remains. The second focused run passed nine top-level cases and two subcases, including old monster ownership, legacy reuse, and GM-only notice controls. See `source-turn-after-1.log` and `source-turn-after-2.log`. Mutation and complete suite checks follow. Complete per-monster ability conformance and first-run replacement rounds remain open.

## Source-turn timing checkpoint

Code `0b1fc1e197cbbfd6d79c0529ee32e979cd67fb36` passed 1,075 cases in the complete shuffled race suite, with two external-server skips and no race reports. The server package took 412.243 seconds. The 20 selected-mechanics monster cases passed with all four private fixtures enabled. Four deliberately reintroduced timing defects triggered independent assertions, and the original source was restored each time. See `SOURCE_TURN_TIMING_DESIGN.md` and `VALIDATION-0b1fc1e.json` for route scopes and evidence. The separate compendium still uses the existing database. Complete ability coverage for twenty monsters and the unique replacement rounds remain open.

The next worktree reproduced damage-only issues through GM narration. Preview changed monster HP, applied actions skipped companions and did not count the daily use, and later targets took damage during pending Concentration. Those findings and subsequent repair tests are documented independently of this checkpoint.
