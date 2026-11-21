# Target prerequisites for imported saving-throw actions

The supplied reconstructed Abominable Yeti specifies one target the creature can see within 30 feet. SRD 5.2.1 defines Blinded as preventing sight. Blindsight can still see within its listed range except through Total Cover. No part of this change relies on the name of a monster.

A dedicated route probe on code `cff2140` reproduced all three targeting defects in `fresh-gaze-prerequisites-before.log`. A 35-foot target rolled and took damage. Two named in-range targets both rolled and took damage in one use. A Blinded caster with no Blindsight also rolled and injured the target. All three received Paralyzed. These findings follow the original first-run failures and do not replace the earlier record.

The proposed change checks the supported single-target range sentence in the saving-throw header. It resolves campaign targets through the existing scoped lookup and compares exact map distance if both tokens are present. The action stops before dice, damage or conditions if there are zero or multiple targets or a known range violation. A known Blinded source cannot use ordinary sight. Declared Blindsight is considered within its listed range.

The combat grid provides distance. The GM remains responsible for sight, cover, unusual senses and distances absent from the map. A missing determination produces a private instruction and does not roll. The authenticated GM can retry with `confirm_targeting: true` after checking the unresolved facts. That confirmation cannot override a known target-count or distance violation. It is available on the monster action in narration and on a Legendary Action, including one submitted with turn advancement. Unrecognized targeting sentences and broader game visibility still need review.

The proposed action resolver leaves Recharge, daily use and Legendary Action state free when the prerequisite gate stops the action. The test compares refusal followed by a legal same-window retry for Recharge and Legendary Actions. Current status is compile and Swagger verification. Route validation, prior-suite compatibility, attached-turn and resumed-turn controls and deliberate regression checks remain pending.

## Route retest

The initial post-fix batch passed the four targeting exclusions plus the Recharge and Legendary Action refusal and retry. The batch also showed that two Iron Golem controls sent both a monster and a companion in one request to effects whose invented headers specify one creature. Those controls now call each relevant effect separately for each creature. Both still assert actual saves and the presence or absence of the Magic Resistance source. The legal targeting workflow uses the GM confirmation when the fixture has no map.

The corrected batch passed those Iron Golem controls and all the fresh targeting probes. A separate initiative case observed the target's turn while Paralyzed and confirmed removal at the start of the source's next turn. The attached-turn Legendary Action test adds a second legal attempt to an ordinary failed first attempt in the same request. It checks the two replies, the timing window and exactly one HP change. That case is awaiting execution at this edit.

## Mapped wall and save-sweep compatibility

The attached-turn action passed. In the full race run of `53b77f38c86fa0c21dfe0bb0176f02ca85f9701c` there were 1,115 passes, two external-server skips and one failure. The mapless book save sweep reached its first explicit sight-and-distance prerequisite on Animal Lord. Its procedural fixture now confirms its already chosen legal target. This keeps its save, damage and condition assertions active while the dedicated tests check the missing-confirmation case. The focused sweep reran 78 damaging actions, with failure observed for all 78 and success for 72, and passed. It samples up to 40 rolls and is limited to the assertions in that test.

The existing map code can determine visibility through walls and doors. A separate negative control on `53b77f3` confirmed that the new save gate had let an explicitly confirmed target behind a mapped wall roll and take damage. Code `44472c6` delegates the wall and door decision to that same map visibility check before GM confirmation. The negative control then left the target's HP unchanged. Removing the wall still allowed the confirmed attack to roll and apply damage. This distinguishes mapped cover from scene facts the GM must confirm. No new spatial model or schema is introduced.

The full retry includes the cover change and mapless fixture correction. No complete full-suite pass has been assigned to the targeting milestone at this edit.

The first complete retry at `78186b8` passed the corrected book save sweep. It failed the fresh Yeti weapon test because the fixture assumed that every natural roll above one would beat the target's Armor Class. The actual route correctly reported 13 versus AC 14 as a miss. The fixture now validates the total and Armor Class in the roll receipt, applies the ordinary hit threshold and natural-one and natural-twenty rules, and continues to check damage on hits. No attack implementation changed for this correction.
