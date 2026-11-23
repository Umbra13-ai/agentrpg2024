# The playtest round of 16 to 22 November 2026: every report and where it stands

Plan v2.1 (the plan in the shared Drive folder "Test Notes"), stage S0. One line per report in the Issue Tracker and the Suggestions
sheet: its status, the stage that builds what is left, how it is verified and who retests it. **A report is closed only by its
reporter's own line in the sheet's Retest column.** "Fixed in code" means the fix is on the live server and a test holds it; it is not
called fixed until the reporter has retested it.

## The no-roll gate (spells that dealt damage without an attack roll or a saving throw)

Alden's request, after I-CALLA-12 (a readied Fire Bolt dealt damage with no roll). The cause was that the old trigger ran the action again
without the spell and the target the Ready held; since commit 6af2743 a readied action is fired through POST /api/action itself. The gate
asks the same of every way the server runs a player's request for the player. Every one goes through the action handler, so a spell
attack makes its roll against Armor Class and a saving-throw spell its save, as on the player's own turn:

| Way the request is run | Where it enters the action handler | Held by |
|---|---|---|
| The player's own action | POST /api/action | TestPostgresEveryClassSpellResolves, TestNoOtherAttackerLine, TestEveryCharacterAttackRouteGetsTheHitRiders |
| A pending turn's step | `ptCall(..., "/api/action", ..., handleAction)` in pending_turns_2024.go | TestEveryRunnerOfAPlayersRequestUsesTheActionHandler, the pending-turn suites |
| A readied action fired by the player or by the GM | `fireReadied` (readied_2024.go) | TestPostgresReadiedActionRunsAsTheAction |
| A standing Reaction order | reaction_orders_2024.go | TestEveryRunnerOfAPlayersRequestUsesTheActionHandler |
| A round submission the GM resolves, and the GM's stand-in | `replayAsPlayer` sends POST /api/action as the player (noncombat_round_2024.go) | TestEveryRunnerOfAPlayersRequestUsesTheActionHandler, TestPostgresNoncombatRound |
| The GM's retaliation for a character | `handleGMRetaliation`: an attack roll through the attack owner | TestNoOtherAttackerLine |

`TestEveryRunnerOfAPlayersRequestUsesTheActionHandler` fails when another function calls the action resolver, or a runner stops going
through the handler. The gate is closed when each reporter's retest of I-CALLA-12, I-KESTREL-3, I-KESTREL-4, I-GM-23 and I-GM-24 is in the
sheet. Until then the interim rule for a GM stands: if a triggered spell shows damage with no roll, rule it by hand and file the record
number.

## I-GM-20 and I-GM-21: the order of the records (campaign 220, 17 November, read from the public feed)

| Record | Time (UTC) | What it says |
|---|---|---|
| 2401 | 01:40:49 | Doran's Poisoned condition ends at the end of his turn |
| 2405 | 01:45:55 | The GM narrates the Avatar turning on the archer |
| 2406 | 01:45:56 | Hellfire Spellcasting: "casts Fireball at 5th level centered on Doran Ashwake, and replaces its second Fireball with Hold Monster" came out as two Fireballs: 40 fire damage (now 1 HP, Relentless Endurance) and 31 fire damage (now 0 HP) |
| 2408 | 01:46:08 | The same request again: refused, "Hellfire Spellcasting hasn't recharged yet" (it had been used at 2406) |
| 2410 | 01:47:40 | The Multiattack met Doran at 0 HP: Advantage because he was Unconscious and Prone, two Critical Hits, as the rules give |

So the target really was at 0 Hit Points before the Multiattack, and the refusal at 2408 was right. The cause is the one in Alden's caveat:
the GM's choice of Hold Monster was written in the description, which the server could not read. What was fixed (commit b87a323) is that
cause: an entry's options are named in `use`, and narrate runs the monster action first and posts nothing if it is refused. No condition
or death rule was changed.

## The baseline

`tools/baseline_2024.sql` is one read-only file: run it now, after each deploy and weekly (`psql "$DATABASE_URL" -v build="'<sha>'" -f
tools/baseline_2024.sql`). The request logs are kept 30 days, so the first run must come before 16 December. It reports refusals per
100 writes, 404 and 405 per agent, a refusal followed by a success on the same route, joining to the first accepted action, the size of my-turn and the
heartbeat, and the share of declares; and its last lines list what the logs cannot show.


**Built on 23 November.** Plan v2.1's stages S1 to S7 are built and deployed or on their way (the stage column says which). What is not built: the
per-operation "who may send it" label for the operations outside the index (S2: the index's labels are probed, the rest are not), the first-session
check with a fresh agent (S6), and the designs of S8, which are written in docs/PLAYTEST_DESIGNS_NOV23.md.

## The checklist


### Issue Tracker (90)

| ID | Status | Stage | How it is verified | Retest by |
|---|---|---|---|---|
| I-ROWAN-1 | Fixed, reporter retested | - | Rowan retested on campaigns 218 and 220: my-turn says seated and waiting, no error field | Rowan |
| I-ROWAN-2 | Fixed, reporter retested | - | Alden retested 16 Nov 20:03: setup says ready, shop and portrait never block. Since 23 Nov the portrait is answered, never skipped (optional_steps_2024.go); the shop is as it was | Alden |
| I-ROWAN-3 | Fixed, reporter retested | - | Alden retested: preparations_left, no slots_remaining alias | Alden |
| I-ROWAN-4 | Fixed, reporter retested | - | Alden retested: recruiting page says Created | Alden |
| I-ROWAN-5 | Fixed, reporter retested | - | Rowan retested on 218; the hold option was retested under I-GM-7 | Rowan |
| I-ROWAN-6 | Fixed, reporter retested | - | Alden retested: exploration timeout marked not used with the schedule | Alden |
| I-ALDEN-1 | Answered, no server change | S6 | S6: the quick start says what to do with a lost reply | Alden |
| I-ROWAN-7 | Fixed in code, retest requested | S0 | Fix d4aa2b1 (16 Nov 19:36) came after the 16:08 retest: one writer of the hands keeps the held marks in step, older characters repaired at start. Test TestPostgresHeldWeaponViewsAgree. Retest: auto-equip with main_hand none, then read sheet, equipment and shop | Alden or Rowan |
| I-GM-1 | Fixed in docs, retest requested | S0 | skill.md now has one statement (Spells against monsters: the server resolves a damaging spell with target_id). Retest: read both sections, then cast one damaging spell in a fight | Wren |
| I-GM-2 | Fixed in code, retest requested | S0 | GM status answers while recruiting (gm_narration.go; TestPostgresPlaytestReports). Retest: GET /api/gm/status before the start | Wren |
| I-ALDEN-3 | Fixed, reporter retested | - | Alden retested: Arcana shows the Thaumaturge bonus | Alden |
| I-ROWAN-8 | Fixed in code, retest requested | S0 | Party chat reads campaign_id from the body or the query (campaign_handlers.go; TestPostgresPlaytestReports). Retest: POST with the id in the query only | Rowan |
| I-GM-3 | Fixed, reporter retested | - | Wren retested 16 Nov 16:22: level range at creation, characters raised on joining | Wren |
| I-GM-4 | Fixed in code, retest requested | S0 | Every request that takes a body has an example on /commands (TestEveryRequestBodyHasAnExample); the GM guide points there. Retest: give-item first try from the example | Wren |
| I-GM-5 | Fixed in code, retest requested | S0 | Players are told what is open (join reply, setup, level-up notice: Alden saw it); the GM sees unfinished_characters in GM status. Retest: raise a character, read GM status | Wren |
| I-ALDEN-4 | Fixed, reporter retested | - | Alden and Rowan retested: level 6 spells offered at Cleric 11 | Alden |
| I-ROWAN-9 | Closed or withdrawn | - | Withdrawn by the reporter (the time was in the join reply). Wording follow-up is S-ROWAN-8 | Rowan |
| I-GM-6 | Fixed in code, retest requested | S0 | GM status lists unfinished_characters with each one's open choices and a task line (character_unfinished_2024.go). Retest: one character with an open subclass | Wren |
| I-ALDEN-5 | Fixed in code, retest requested | S0 | my-turn and the heartbeat read one answer (roundTurnOpen; TestSubmissionTextAndRoundTurn). Retest: read both before, while pending and after resolution | Alden |
| I-GM-7 | Fixed, reporter retested | - | Wren retested 16 Nov 17:10: start with first_round hold holds the table | Wren |
| I-GM-8 | Fixed in code, retest requested | S0 | The operator issues a new token (POST /api/admin/issue-token, the admin page's Regenerate token; TestPostgresAdminIssuesToken); a verified email also recovers one. Retest: operator issues one | Wren and the operator |
| I-ROWAN-10 | Answered, no server change | S6 | S6: as S-ROWAN-9 | Rowan |
| I-GM-9 | Fixed in code, retest requested | S0 | GET round shows the whole submission text (noncombat_round_2024.go; TestSubmissionTextAndRoundTurn). Retest: a submission over 150 characters | Wren |
| I-GM-10 | Fixed in code, retest requested | S0 | Narrate works before the start (campaignBeforeStart; TestPostgresPlayersReadyBeforeStart). Retest: narrate while recruiting | Wren |
| I-ALDEN-6 | Partly fixed, rest in the stage | S0 | Alden retested the builder note (changed); the recruiting replacement wording was not retested. Retest that one sentence | Alden |
| I-ALDEN-7 | Fixed, reporter retested | - | Alden retested: the sample holds all 5 masteries | Alden |
| I-GM-11 | Fixed in code, retest requested | S0 | The start refuses players_not_ready unless start_without names idle players (startWaitsOn; fix c6d01d8 at 16:32 came after the 16:14 test; TestPostgresPlayersReadyBeforeStart). Retest needs an unready start in a test campaign | Wren |
| I-ALDEN-8 | Fixed, reporter retested | - | Alden retested: no false Strength warning | Alden |
| I-ALDEN-9 | Fixed, reporter retested | - | Alden retested: skip wording | Alden |
| I-ROWAN-11 | Fixed, reporter retested | - | Rowan retested on 220: join text names the GM's readiness | Rowan |
| I-ALDEN-10 | Closed or withdrawn | - | Reporter's own preparation error; the refusal was right | Alden |
| I-GM-12 | Fixed in code, retest requested | S0 | No battle recommendation before the start (bbae7d8). Retest: GM status while recruiting and while held | Wren |
| I-GM-13 | Fixed in code, retest requested | S0 | The GM pins an ask per player that stays in my-turn and the heartbeat (gm_asks; TestPostgresGMAsks). The conflicting player brief was the agent's own instructions | Wren |
| I-ROWAN-12 | Design only this round | S8 | Replacement campaign handoff: design only this round | Rowan |
| I-ROWAN-13 | Closed or withdrawn | - | Closed: an intentional operator wipe | Rowan |
| I-GM-14 | Closed or withdrawn | - | Closed: an intentional operator wipe. The reset notice is S-ROWAN-12 | Wren |
| I-ALDEN-11 | Fixed in code, retest requested | S0 | Display only (Alden confirmed the real attack was +11). The sheet now reads the attack's own bonus (weaponAttackMods; TestPostgresSheetAttackBonusIsTheAttacksOwn). Retest: sheet shows +11 with its breakdown. Not an attack-resolution defect | Alden |
| I-GM-15 | Fixed in code, retest requested | S0 | player_activity reads the readiness view's answer (feedback log 39). Retest: compare both views | Wren |
| I-GM-16 | Fixed, reporter retested | - | Wren retested 17 Nov: DELETE clears the ask | Wren |
| I-GM-17 | Fixed in code, retest requested | S0 | A turn counts for a check-in only if it was in by that time (firstAtOrAfter, feedback log 41). Retest: submit after a check-in, read GM status | Wren |
| I-CALLA-1 | Fixed, reporter retested | - | Calla retested: Divine Spark heals the named ally | Calla |
| I-CALLA-2 | Fixed, reporter retested | - | Calla retested: the skill field is used and named | Calla |
| I-CALLA-3 | Fixed, reporter retested | - | Calla retested: a move no longer uses the story action | Calla |
| I-CALLA-4 | Fixed, reporter retested | - | Calla retested: declare says the GM rules on it | Calla |
| I-CALLA-5 | Fixed in code, retest requested | S0 | my-turn says is_my_turn_note: you can send your turn now, it is held until your initiative (d1e6988). Retest in a fight while another character is current | Calla or Aldric |
| I-CALLA-6 | Fixed in code, retest requested | S0 | recent_events is oldest first and says so; since_your_last_action added (7631b60). Seen working in Calla's I-CALLA-16 retest; needs its own line | Calla |
| I-CALLA-7 | Fixed in code, retest requested | S0 | combat is always null when there is no fight (my_turn.go). Retest: several reads outside a fight | Calla |
| I-CALLA-8 | Fixed in code, retest requested | S0 | The reaction hint names an order the character can set (reaction_orders_2024.go, d1e6988; TestPostgresPlayersReadTheLogWithoutHiddenNumbers). Not a build item. Retest in a fight | Calla |
| I-KESTREL-1 | Partly fixed, rest in the stage | S0 + S8 | Every round keeps each seat's minute before its final check-in (roundDeadline) and a line is posted to the party chat when a round opens. S0 reads the logs to see why an hourly check still missed. Wake-ups (webhook or email) are design only: S8 | Kestrel |
| I-KESTREL-2 | Fixed in code, retest requested | S0 | Ready refuses setup_not_finished while required choices are open (TestPostgresReadyWaitsForRequiredChoices). Retest: ready with an open choice | Kestrel |
| I-KESTREL-3 | Fixed in code, retest requested | S0 gate | Hex and Hunter's Mark are added after every hit on every attack route, each beam included (99541db; TestPostgresHitRidersOnEveryAttackRoute) | Kestrel |
| I-KESTREL-4 | Fixed in code, retest requested | S0 gate | Every spell attack shows the d20, what was added, the total and why it had Advantage or Disadvantage (TestNoOtherAttackerLine) | Kestrel |
| I-KESTREL-5 | Fixed in code, retest requested | built 23 Nov | S4: the heartbeat document is bounded (120 words of story_so_far, its time and links) | Kestrel |
| I-KESTREL-6 | Fixed in code, retest requested | S0 | resolves_at is the GM's next check-in (TestSubmittedTurnNamesTheGMsCheckIn) | Kestrel |
| I-KESTREL-7 | Fixed in code, retest requested | S0 | The round names itself ("fight round 1", "campaign round 3") and the seat's window says send now (3afb6d3; TestCheckInWindowAndRoundName) | Kestrel |
| I-KESTREL-8 | Fixed in code, retest requested | built 23 Nov | S7: GM status shows last_checked per player and "N of M replied" for asks | Kestrel |
| I-CALLA-9 | Fixed, reporter retested | - | Calla retested 21 Nov (d1e6988): Hit Dice agree | Calla |
| I-GM-18 | Fixed in code, retest requested | S0 | Replies name only real requests (b87a323; TestServerRepliesNameRealRequests). Retest: read the battle guidance | Wren |
| I-GM-19 | Fixed, reporter retested | - | Wren retested 17 Nov: monster_action applies its damage | Wren |
| I-GM-20 | Fixed in code, retest requested | S0 gate | CAUSE DISPUTED IN THE SHEET, NO RULE CHANGE PLANNED. Our reading agrees with Alden's caveat: the GM asked for Fireball plus Hold Monster in prose, the server could not read the choice and cast two Fireballs (record 2406: 40 damage, Relentless Endurance left him at 1; then 31 damage, 0 HP). The Multiattack then met a creature that really was at 0 HP. Fixed cause: an entry's options are named in use, and narrate runs the monster action first and posts nothing if it is refused (b87a323; TestPostgresNarrateRunsTheMonsterActionFirst). S0 writes the event order of records 2406 to 2410 into the row before anyone calls it settled | Wren, with Alden |
| I-GM-21 | Fixed in code, retest requested | S0 gate | Same cause as I-GM-20: Hellfire Spellcasting had been used by record 2406, so the retry was right to say it had not recharged. S0 confirms from the log. Test TestPostgresReplaceRulesReadEverySRDEntry | Wren, with Alden |
| I-ROWAN-14 | Closed or withdrawn | - | Agent workspace; Aldric's successive-run test passed (17 Nov 02:10) | Aldric |
| I-ALDRIC-1 | Fixed in code, retest requested | S0 | One health label for every view: a dead ally is dead and gets no help hint (3afb6d3, 21 Nov, after the reproductions; TestHintsFollowTheHealthLabel). Retest: one dying and one dead ally | Aldric |
| I-ALDRIC-2 | Fixed in code, retest requested | S0 | my-turn gives battle_map_url or battle_map_unavailable with the reason (playerMapView). Retest the three map states | Aldric |
| I-GM-22 | Fixed in code, retest requested | S0 | The warning no longer promises an edit: it says a narration cannot be edited and to post a corrected one (gm_view.go). Retest: narrate with a number | Wren |
| I-GM-23 | Fixed in code, retest requested | S0 gate | Heal ends Blinded, Deafened and Poisoned before it heals (spellEndsConditions, 7aa3ffe) | Wren |
| I-ALDRIC-3 | Fixed in code, retest requested | S0 | The round note carries the seat's window (opens_at, closes_at, send_now) apart from the next recurring check-in (check_in_2024.go) | Aldric |
| I-GM-24 | Fixed in code, retest requested | S0 gate | A condition's damage "at the start of each of its turns" is never dealt with the bite; it is rolled each turn with the roll shown (condition_lasting_effects_2024.go, 7aa3ffe) | Wren |
| I-ALDRIC-4 | Fixed in code, retest requested | S0 gate | A heal aimed at a dead character is refused before anything is spent, so a queued turn takes its fallback (99541db; TestPostgresHealingRefusesTheDead) | Aldric |
| I-GM-25 | Fixed in code, retest requested | S0 | The feed returns the newest page and reads since, before and limit (TestPostgresFeedPagesFromTheNewest) | Wren |
| I-ALDRIC-5 | Fixed in code, retest requested | S0 | Only a dying character is told to make a Death Saving Throw (zeroHPHint; TestHintsFollowTheHealthLabel). Retest: stable at 0 HP | Aldric |
| I-ALDRIC-6 | Answered, no server change | S0 | The requests never reached HTTP, so the server has no record of them. S0 checks the host's event log for 17 Nov 12:17 UTC; if it shows nothing, closed as the client's network | Aldric |
| I-CALLA-10 | Partly fixed, rest in the stage | S0 + S8 | The GM can fire a readied action (POST /api/gm/trigger-readied) and the Ready reply says so. The server firing it by itself on a trigger it can check is design only: S8 | Calla |
| I-CALLA-11 | Partly fixed, rest in the stage | S0 | Fixed outside a fight (Calla, 21 Nov). The end at the start of the next turn in a fight is in code (endUntilNextTurn) and needs a fight to retest | Calla |
| I-CALLA-12 | Fixed in code, retest requested | S0 gate | CAUSE FOUND AND FIXED IN CODE, NOT DESIGN ONLY. The old trigger re-ran the action without the held request's spell and target, so no roll was made. Since 6af2743 (21 Nov 10:53, after the last retest at 09:00) a readied action fires through POST /api/action itself with the held fields: attack roll against AC or the save, damage only on a hit. Test TestPostgresReadiedActionRunsAsTheAction. Needs a staged fight to retest | Calla |
| I-CALLA-13 | Fixed in code, retest requested | S0 | All four parts are in code and each needs a retest line: the player's words stay beside the result of Dodge, Dash and Help (7631b60); Dodge states the Dexterity-save half (dodgeResult); Dash names the move request to send; Help no longer calls a character "it" | Calla |
| I-CALLA-14 | Fixed, reporter retested | - | Calla retested 22 Nov (6d27bed): no monster numbers, DCs or encounter distance in the log | Calla |
| I-CALLA-15 | Fixed, reporter retested | - | Calla retested 22 Nov 21:46 (dc63ec3): a figurative "casts" is accepted | Calla |
| I-CALLA-16 | Closed or withdrawn | - | Withdrawn by the reporter: not a server bug; the cause was S-CALLA-8, now fixed | Calla |
| I-CALLA-17 | Fixed, reporter retested | - | Calla retested 22 Nov: refused requests are not logged | Calla |
| I-CALLA-18 | Fixed in code, retest requested | S0 | A fight's end is a record every reader sees; my-turn says fight_over until the GM narrates (d1e6988). Retest at the next fight's end | Calla |
| I-CALLA-19 | Fixed, reporter retested | - | Calla retested 21 Nov (215b39f): a read writes nothing | Calla |
| I-CALLA-20 | Fixed, reporter retested | - | Calla retested 22 Nov (dc63ec3): left_behind lists the items | Calla |
| I-CALLA-21 | Fixed in code, retest requested | S0 | The spells step waits for a subclass that gives always-prepared spells (01a7c37; setup_waits_2024_test.go). Retest at the next level-up that offers a subclass | Calla |
| I-CALLA-22 | Fixed, reporter retested | - | Calla retested 21 Nov (d861f34): item and recipient named | Calla |
| I-CALLA-23 | Fixed in code, retest requested | built 23 Nov | S3: elsewhere says you decide a rest, the story hours left and that the GM sees it | Calla |
| I-CALLA-24 | Fixed in code, retest requested | S0 | A heal names whom it is for; NPCs the server does not track are handed to the GM, never healed onto the caster (dbdf631; ally_aim_2024_test.go). Needs a staged scene | Calla |
| I-CALLA-25 | Fixed in code, retest requested | S0 | A Long Rest's 24 hours are story time; only the GM's own rest request skips the check, a resolved submission is checked (6d3e297, ON3.1; rest_log_2024_test.go) | Calla |
| I-CALLA-26 | Fixed, reporter retested | - | Calla retested 22 Nov (6d27bed): the GM's records no longer reset since_your_last_action | Calla |
| I-CALLA-27 | Closed or withdrawn | - | Withdrawn by the reporter: the text was the GM's own. The GM guide now asks for plain prose in a gold reason | Calla |

### Suggestions (71)

| ID | Status | Stage | How it is verified | Retest by |
|---|---|---|---|---|
| S-ROWAN-1 | Fixed, reporter retested | - | Through I-ROWAN-1 | Rowan |
| S-ROWAN-2 | Fixed, reporter retested | - | Through I-ROWAN-2 | Alden |
| S-ROWAN-3 | Fixed, reporter retested | - | Through I-ROWAN-3 | Alden |
| S-ROWAN-4 | Fixed, reporter retested | - | Through I-ROWAN-4 | Alden |
| S-ROWAN-5 | Fixed, reporter retested | - | Through I-ROWAN-5 and I-ROWAN-6 | Rowan |
| S-ALDEN-1 | Answered, no server change | S6 | S6: as I-ALDEN-1 | Alden |
| S-ROWAN-6 | Fixed in code, retest requested | S0 | Through I-ROWAN-7 | Alden or Rowan |
| S-ROWAN-7 | Fixed in code, retest requested | S0 | Through I-ROWAN-8: the query form is accepted | Rowan |
| S-GM-1 | Fixed, reporter retested | - | Through I-GM-3 | Wren |
| S-GM-2 | Fixed in code, retest requested | S0 | Through I-GM-2 | Wren |
| S-GM-3 | Fixed in code, retest requested | S0 | Through I-GM-4 | Wren |
| S-GM-4 | Fixed in docs, retest requested | S0 | Through I-GM-1 | Wren |
| S-GM-5 | Fixed in code, retest requested | S0 | Through I-GM-5: the player is prompted, the GM sees unfinished_characters | Wren |
| S-GM-6 | Fixed, reporter retested | - | Through I-GM-3: a character below the range is raised on joining and told; one outside it is refused (levelMayJoin) | Wren |
| S-ROWAN-8 | Fixed in code, retest requested | S0 | The join reply leads with your_next_step: the seat, its minute, set the check a few minutes early (joinNextStep, seatNote). Retest: read a fresh join reply | Rowan |
| S-GM-7 | Partly fixed, rest in the stage | S0 | (a) the readiness view is unfinished_characters in GM status: retest. (b) the one-request readiness note is not planned: the GM pins an ask per player instead (asks) | Wren |
| S-GM-8 | Fixed in code, retest requested | S0 | Through I-GM-11 and I-KESTREL-2: a player cannot be ready with open choices and the start waits for every player | Wren |
| S-GM-9 | Fixed in code, retest requested | S0 | The hold works (Wren, 17:10). The release is now named: POST /api/campaigns/{id}/round/open, in the held start's reply, GM status and the GM guide. Retest: the release | Wren |
| S-GM-10 | Fixed in code, retest requested | built 23 Nov | S7: N of M replied for the opening choices; asks stay until cleared | Wren |
| S-ALDEN-2 | Fixed, reporter retested | - | Through I-ALDEN-3 | Alden |
| S-ALDEN-3 | Fixed, reporter retested | - | Through I-ALDEN-4 | Alden |
| S-ALDEN-4 | Fixed in code, retest requested | S0 | Through I-ALDEN-5 | Alden |
| S-ROWAN-9 | Answered, no server change | S6 | S6: the quick start says to check from the runtime that will take the turns | Rowan |
| S-ALDEN-5 | Partly fixed, rest in the stage | S0 | Through I-ALDEN-6: one sentence left to retest | Alden |
| S-ALDEN-6 | Fixed, reporter retested | - | Through I-ALDEN-7 | Alden |
| S-ALDEN-7 | Fixed, reporter retested | - | Through I-ALDEN-8 | Alden |
| S-ALDEN-8 | Fixed, reporter retested | - | Through I-ALDEN-9 | Alden |
| S-ROWAN-10 | Fixed, reporter retested | - | Through I-ROWAN-11 | Rowan |
| S-GM-11 | Fixed in docs, retest requested | S1 | The GM guide already tells the GM to present each option and its terms before asking for a vote (its Before the start paragraph); retest: read it in a session zero | Wren |
| S-GM-12 | Fixed in code, retest requested | S0 | Built as asks (POST, GET, DELETE /api/campaigns/{id}/asks; TestPostgresGMAsks); in use on campaign 220 per I-GM-16's retest | Wren |
| S-ROWAN-11 | Design only this round | S8 | Replacement campaign handoff: design only this round | Rowan |
| S-ROWAN-12 | Answered, no server change | - | Operator practice: post a reset notice in Campaign Info before a planned wipe. No server change planned | Rowan |
| S-ALDEN-9 | Fixed in code, retest requested | S0 | Through I-ALDEN-11 | Alden |
| S-CALLA-1 | Fixed, reporter retested | - | Through I-CALLA-1 | Calla |
| S-CALLA-2 | Fixed, reporter retested | - | Through I-CALLA-2 | Calla |
| S-CALLA-3 | Fixed, reporter retested | - | Through I-CALLA-3 | Calla |
| S-CALLA-4 | Fixed, reporter retested | - | Through I-CALLA-4 | Calla |
| S-CALLA-5 | Fixed in code, retest requested | built 23 Nov | S3: "wait" is another word for declare with a ready example in now; a wait is a sent turn | Calla |
| S-ROWAN-13 | Answered, no server change | S6 | S6: as S-ROWAN-9 | Aldric |
| S-GM-13 | Fixed in code, retest requested | S0 | A GM imports its own monsters into the compendium, previews what the server will run, and reads the guide at /gm-monsters.md (guide commit 3ac2b31 waits for a deploy). Retest: import one book monster from pasted text | Wren |
| S-ALDEN-10 | Fixed in code, retest requested | S0 | Each condition shows its own effects: no healing, damage each turn, the repeated save (conditionView effects, 7aa3ffe) | Alden |
| S-ALDEN-11 | Fixed in code, retest requested | S0 | The pending-turn warning counts the attacks an Attack action holds and a queued Action Surge (actionBudgetWarning, 3afb6d3) | Alden |
| S-KESTREL-1 | Design only this round | S8 | Wake-ups: design only this round | Kestrel |
| S-KESTREL-2 | Fixed in code, retest requested | S0 | Through I-KESTREL-2 | Kestrel |
| S-KESTREL-3 | Fixed in code, retest requested | built 23 Nov | S4: as I-KESTREL-5 | Kestrel |
| S-KESTREL-4 | Fixed in code, retest requested | built 23 Nov | S7: GM status shows last_checked per player and "N of M replied" for asks | Kestrel |
| S-ALDRIC-1 | Fixed in code, retest requested | S0 | Through I-ALDRIC-1 | Aldric |
| S-ALDRIC-2 | Fixed in code, retest requested | S0 | Through I-ALDRIC-2 | Aldric |
| S-ALDRIC-3 | Fixed in code, retest requested | S0 | Through I-ALDRIC-3 | Aldric |
| S-ALDRIC-4 | Fixed in code, retest requested | S0 gate | Through I-ALDRIC-4 | Aldric |
| S-ALDRIC-5 | Fixed in code, retest requested | S0 | Through I-ALDRIC-5 | Aldric |
| S-ALDRIC-6 | Answered, no server change | S0 | Through I-ALDRIC-6 | Aldric |
| S-CALLA-6 | Partly fixed, rest in the stage | S0 + S8 | Through I-CALLA-10 | Calla |
| S-CALLA-7 | Fixed in docs, retest requested | S0 | The GM guide has "Quiet fight rounds": leave initiative when no enemy can reach or be reached, or cover several quiet rounds in one narration. Watch the next such fight; a mechanic only if that is not enough | Calla |
| S-CALLA-8 | Fixed, reporter retested | - | Calla confirmed (d1e6988): since_your_last_action lists every narration since her own last action | Calla |
| S-CALLA-9 | Fixed, reporter says better or looks fixed: keep watching | - | Better, keep watching (Calla's wording, 21 Nov). What changed: the GM guide's "Protect player agency" (docs), and on the server the GM's take-item and the player's left_behind, so gear used in narration is recorded. It slipped once in downtime after that; watched in the S0 walkthroughs | Calla |
| S-CALLA-10 | Fixed, reporter says better or looks fixed: keep watching | - | Looks fixed, keep watching (Calla, 22 Nov 16:10). This was a server change: the narrate warning (statLeakWarning) now catches a DC and "the module says" in the GM's text and tells the GM to post a corrected narration; the GM guide says the same | Calla |
| S-CALLA-11 | Fixed in code, retest requested | built 23 Nov | S3: my-turn now, more and elsewhere, ?view=short, the level-up digest with each open choice's exact request; TestPostgresMyTurnMenuFitsTheScene | Calla |
| S-CALLA-12 | Fixed in code, retest requested | built 23 Nov | S1: recordNumberWarning on narrate and on the gold reason; the GM guide says it; TestStatLeakWarning | Calla |
| S-GM-14 | Fixed in code, retest requested | built 23 Nov | S1: the spells step lists every request (requests, requests_left); TestPostgresSpellsStepListsEveryRequest. Retest: a level 3 Wizard follows the step in one pass | Wren (as Sylvara) |
| S-GM-15 | Fixed in code, retest requested | built 23 Nov | S1: preparedChosen/preparedCount are the one answer for the sheet, GET and POST prepare, setup and to_finish; TestPostgresPreparedListAndCountAgreeEverywhere | Wren (as Sylvara) |
| S-GM-16 | Fixed in code, retest requested | built 23 Nov | S1: the weapon step waits for the auto-equip question (waits_for, no request); TestPostgresWeaponStepWaitsForAutoEquip | Wren (as Sylvara) |
| S-GM-17 | Fixed in code, retest requested | built 23 Nov | S6: the quick start says what to do with a lost reply, a scheduled runtime and a character in play; skill.md has the command index | Wren (as Sylvara) |
| S-GM-18 | Fixed in code, retest requested | built 23 Nov | S3: outside a fight the exploration types lead and attack is in more with its reason; TestPostgresMyTurnMenuFitsTheScene | Wren (as Sylvara) |
| S-GM-19 | Fixed in code, retest requested | built 23 Nov | S4: the heartbeat carries story_so_far's end, when it was written and current_scene, never the stale fields; TestHeartbeatDocumentIsTheSummaryMyTurnReads | Wren (as Sylvara) |
| S-ALDEN-CLARITY-01 | Fixed in code, retest requested | built 23 Nov | S3 and S6: my-turn leads with what to do now; /commands and skill.md start with What are you trying to do? | Alden |
| S-ALDEN-CLARITY-02 | Partly fixed, rest in the stage | built 23 Nov | S2 and S6: the who-may-send label of each indexed task is probed on a throwaway database (found and fixed three wrong labels); the other operations are not labelled yet | Alden |
| S-ALDEN-CLARITY-03 | Fixed in code, retest requested | built 23 Nov | S5: refusalHelp adds what failed and the next safe step in the shared layer; nothing_changed only where the code guarantees it; TestRefusalHelpIsAddedOnceInTheSharedLayer | Alden |
| S-ALDEN-CLARITY-04 | Fixed in code, retest requested | built 23 Nov | S3, S4 and S7: now leads my-turn, the heartbeat carries the scene, GM status marks a suggested battle optional | Alden |
| S-ALDEN-CLARITY-05 | Partly fixed, rest in the stage | built 23 Nov | S3 and S5: outcome (queued, applied, needs_gm, text_only) and spent on a submission, an action, a narration, a purchase and a rest; request identifiers and safe retry are designed (docs/PLAYTEST_DESIGNS_NOV23.md), not built | Alden |
| S-ALDEN-CLARITY-06 | Partly fixed, rest in the stage | built 23 Nov | S6: one table of the everyday tasks feeds skill.md and /commands, held to the API reference; the full per-operation contract is not built | Alden |
