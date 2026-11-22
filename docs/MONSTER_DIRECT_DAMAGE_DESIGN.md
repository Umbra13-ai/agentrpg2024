# Damage-only stat-block actions

Separate proposed worktree based on `0b1fc1e`. The shared effect resolver has a branch for damage with neither an attack roll nor a saving throw. Code review found that it calls monster damage during a preview, treats companion IDs as character IDs, and advances through all targets even when one needs a Concentration decision. It also fails to mark a completed delivery for the per-day counter. The new route probes are awaiting execution. No repair is claimed at this edit.

The intended scope is the same existing supported wording and one damage roll shared by valid targets. The repair should use the current HP owners for all three target types, preserve the damage roll and remaining targets across a pause, and report inaccessible targets. Additional mechanics in the source text still need their own checks. Book-derived content stays outside the source archive.

## Reproduced through GM narration

`direct-damage-before.log` records two failing route probes on the source-turn checkpoint. Preview reduced the monster's HP while leaving character and companion HP unchanged. Applying the effect damaged the character and monster with the same roll but skipped the companion. The daily use remained unspent despite damage. The separate Concentration probe applied damage to a later target before the GM could decide on the first target's save.

The proposed code recognizes an unconditional instruction to damage a named target or targets. It uses the shared damage owners and saves the rolled amount and the next target with the existing continuation mechanism. A preview displays the amount without calling a damage owner. A performed effect marks execution for the existing per-day accounting owner. The GM receives an explicit adjudication notice for a clause embedded in a more complex instruction. It is not behavioral evidence for those abilities.

A third route probe checks that a conditional clause creates a GM notice and does not roll, change HP or spend its daily use. The implementation must still pass these route probes and mutations, then the complete suite. Other damage entry paths and broader Legendary Action accounting require their own verification.

Two additional preview probes are prepared. The save preview will check conditions on character, monster and companion targets, the monster's Legendary Resistance, and the ordinary Recharge counter. The Legendary Action preview will check its window and both kinds of use before applying the same action through the actual route. These are open probes until run. Existing damage-only results stay distinct from the broader preview path.

## Focused preview and direct damage results

The first follow-up run passed the direct-damage and embedded-clause cases. It also reproduced the two broader preview failures (`direct-damage-and-preview-1.log`). Save preview applied Frightened to character and companion targets, used one monster Legendary Resistance and marked the recharge spent. The separate Legendary Action preview used the timing window and a Legendary Action use.

The shared save path now uses the existing peek mode to roll previews without spending defenses. All target kinds use a shared final step for applied riders and immunity. Finite item effects and reflection are not invoked by this shared save preview. The ordinary stat-block command leaves Recharge and once-per-turn state alone in previews. The Legendary Action command shows current uses and keeps its timing window available. The follow-up focused run passed both preview probes, all three direct damage checks and the selected save, condition and companion regressions (`direct-damage-and-preview-2.log`). This result does not cover every monster spell or reaction preview. Special item and reflection controls need their own full route tests.
