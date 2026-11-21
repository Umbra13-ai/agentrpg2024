# Prepared fresh replacement test

The Brazen Gorgon is a CR 9 private reconstruction. It was selected from the previously unused pool because it is among the highest-CR candidates outside both the 330-name SRD catalog and the earlier 91-name sweep. The runtime fixture SHA-256 is `1fce406f1fa66266ec4cfbd388dda613a7c08b8b4a9051bf045cd4990b0b4515`. The stat block remains outside this source tree.

The first-run suite uses the authenticated GM import and combat API and independent encounters for the named entries. Flame Aura compares an adjacent player character, monster and companion with a distant monster at the source's turn end. Illumination compares actual player attacks at a creature inside the bright area and a creature outside the light while ambient lighting is darkness. Gore and Multiattack check roll counts, Piercing and Fire delivery, and actual HP change. Smelting Charge checks that the server does not roll or spend Recharge for an out-of-range creature before the required movement.

The tests use the user's supplied reconstruction as the reference. They do not embed those ability descriptions. The first-run tests are prepared. No imported-monster result exists at this edit. A complete pass would additionally require valid charge movement, the target-size and grapple-occupancy branches, linked restraint and escape, Recharge recovery, near dim light, defenses, movement, vision, and further attack controls.

## Recorded first attempt

The first imported run used `96807f0fc6ac2255979682b51894cce668adbca5`. It is retained in `fresh-brazen-first-run.log` with SHA-256 `1197a3438be3c4b3395eec70bf33d14d4704a3c215204c206b6a5e5716516ae9`. The candidate failed its first run.

| Probe | Result |
| --- | --- |
| Gore and Multiattack | Passed the roll count, both damage components and actual HP assertions. |
| Smelting Charge | Rolled the out-of-range target's Dexterity save and spent Recharge before any required movement. The target succeeded, so this run did not assert HP or condition errors on a failed save. |
| Flame Aura | The test stopped before the turn advance. Its existing companion had Fire resistance, which contradicted the test's initial assumption. The corrected control checks the companion's expected mitigated share of the same roll. |
| Illumination | The test stopped before its first attack roll because the test character had a Shortbow in inventory but had not equipped it. The corrected control uses the real equip route. |

The corrected fixtures must run on the same combat implementation to establish behavioral results for the last two rows. This does not replace the first-run record or turn the candidate into a fresh pass.

## Movement-triggered save guard

The confirmed Smelting Charge failure exposed a generic execution error. A save clause could be applied independently of the preceding movement that selects its targets. A save rolled in this state also spent Recharge. The proposed prerequisite check recognizes the explicit wording about entering a creature's space during the move. It stops before any roll, condition, damage or use. The GM receives the missing prerequisite in a private instruction. The ordinary `confirm_targeting` flag covers sight or distance. The new test checks that this flag cannot authorize a different movement rule.

This repair is awaiting a database run. It covers premature automatic resolution for recognized text. The full manual or automatic handling of a completed charge, grapple occupancy, position changes and linked restraint is still open. No candidate pass is assigned.

## Corrected controls and shared repairs

The second fixture run passed its map and reached the source's real turn end. The character received 14 Fire damage. Both the adjacent fight monster and Fire-resistant companion lost zero HP. The companion's control expected seven after Resistance. The distant monster was unchanged. The Illumination control initially needed a second fixture correction because its main hand was occupied. Using the actual drop and equip routes produced a successful player attack request with the global darkness Disadvantage still present in the declared bright area. The unlit control also had the darkness penalty. Both failures are retained in the baseline logs.

The mapped plain damage aura now uses the common typed damage sequence against all eligible mapped creatures. The private Gorgon retest passed the adjacent character, monster and companion and the out-of-range control. The invented two-aura test suspended later targets and a later aura while a Concentration decision was pending. The GM ended the cast and resumed. Each target lost exactly its two expected packets and the repeat did not change HP. The prior Burning-specific character path is still limited and needs its own audit.

The movement guard passed the reference and invented tests. Preview and application both left HP, conditions and Recharge intact while the required path was unverified. The single-target sight and range confirmation cannot bypass this guard. The invented control's unrelated save still rolled and applied damage.

The GM can record a manually resolved Recharge use on the existing monster-features route with `spend_recharge`. Its focused route test confirmed denial to a player, rejection for an ordinary action, a single stored use for the declared action, refusal on a duplicate, and the normal action's spent-use check. Recording a use did not deliver damage. This is one step of the manual charge workflow. The map's ownership of shared token space and the complete grapple and movement path still need verification.

The proposed continuous-light rule reads the supported stat-block wording and applies it to a mapped attack target through the existing lighting and vision owner. Map cover blocks light. Conditional sources require an activation rule. The bright and dim boundaries, wall, defeating the source, and the reference attacks are prepared for retesting. No code with these light changes has passed the full suite yet.

## Manual charge state

The map previously refused even an explicitly adjudicated target inside its grappler's space. The proposed map field `share_space_with` represents the GM's placement after such an effect. It requires a different host token with its entire source footprint on the map. The affected token must be wholly inside that footprint and at the same altitude. Ordinary overlaps remain invalid. The attachment identifier stays in the GM map. The GM updates both positions when moving the pair.

The existing GM condition route accepts the campaign-scoped `source_id` and a Restrained record marked `ends_with_grapple`. The latter needs the matching existing Grappled record. They use the established condition owner and the same database. The test composes the actual GM dice, HP, condition, map and Recharge routes for an independently adjudicated failed save. It checks moves into and along the same space and the condition release. The test has been prepared. Successful saves, additional targets, Recharge recovery and the remaining reference checks are not counted at this edit.


## Corrected checkpoint results

The reference illumination probes passed. The real attack within bright light lost the global darkness penalty and the attack outside retained it. Invented controls also checked the bright and dim boundaries, a blocking wall, unplaced source and defeated source. These are attack-visibility checks. Other sense, passive and magical-darkness interactions require their own tests.

The manual charge route used a valid deterministic Dexterity save modifier of −10. The first fixture's −12 was outside the import range and stopped there. The corrected failed-save case used the actual GM dice route, applied both damage types to the target, stored the Grappled and tied Restrained conditions, recorded the shared space and moved both tokens. It recorded one Recharge use and released the linked restraint with the grapple. The player map did not expose the private attachment. The initial complete suite required a central registration of the new condition source field. That guard and the complete manual path passed on `3f1d93b8497129b0e61305e53d46160da9fb42b3`, along with the 1,127-case race suite.

A later branch exercised both the named Charge and Cold Breath Recharge on actual initiative turns. Each qualifying die restored an available use, which was spent again. The test bounds its random search at 500 advances. This run is separate from the verified checkpoint. Full candidate coverage still requires the successful save path, other eligible targets, occupied grapple capacity, source speed and special vision.

## Deliberate regression checks

Eight isolated changes were made against the tested code. Each was restored after its intended assertion failed. The checks caught the removal of the movement prerequisite, omission of companions from the aura, loss of subsequent auras after resumption, use of global darkness for the mapped attack, omission of the recorded Recharge use, rejection of valid adjudicated shared space, loss of the linked grapple, and exposure of the shared-space attachment in the player map. The private fixture tests and logs stayed outside the distributable source. The mutation test names and results are recorded in the bundle's validation manifest. Vet and whitespace checks passed.
