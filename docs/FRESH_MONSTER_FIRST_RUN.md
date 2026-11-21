# First fresh replacement

The supplied reconstructed CR 9 Abominable Yeti was selected because it was outside the SRD catalog and outside the earlier CR 10+ import sweep. The first recorded run used code `16d3c4c4a3d8a1141ef9eb067e236b0152f64d81`. The private fixture is provided at runtime via `FRESH_MONSTER_FIXTURES_DIR`. The archived source includes invented regression inputs and a fixture reader. The original stat block remains private.

| Probe | First-run outcome |
| --- | --- |
| Cold immunity | Cold damage through the GM adjustment route changed no HP. |
| Fear of Fire | The same route applied Fire damage but the next Claw roll omitted Disadvantage. |
| Claw and Ice Throw | Each named attack produced a roll receipt and the tested hit changed HP. |
| Multiattack | The action named the attack choice and optional Gaze for the GM and left HP unchanged before a choice. |
| Chilling Gaze | Normal failure injured and paralyzed. Cold immunity excluded damage while Paralyzed still applied. Success changed no HP and granted 24 hours of immunity, where the supplied text says an hour. The immediate repeat recognized immunity. |
| Cold Breath | Successful and failed target saves, cone exclusion, and refusal of a second spent use passed. |

The original failed run is `fresh-abominable-yeti-first-run.log`. These tests cover the named entries at the scopes above. Movement, vision, attack choice combinations, recharge recovery and further condition and timing interactions remain open for this monster. The candidate counts as a failed fresh first run. No replacement pass is claimed.

## Saving-throw rider and immunity

The shared effect resolver recognizes the tested clause making a condition conditional on the target lacking a named damage immunity. The check reads the existing damage defense owner for each target type. Resistance remains distinct. Unrecognized damage types remain on the GM path. The one-hour successful save duration is parsed into 600 combat rounds. The existing timer store tracks independent entries for the same creature and source. Unknown durations and failed writes produce a private GM instruction. New timed aura immunity uses that same store. Legacy encounter aura-immunity records remain readable.

`fresh-gaze-after.log` records passing checks for ordinary failure, cold immunity, cold resistance and success. It also records an invented control verifying that independent one-hour and 24-hour entries coexist, that the first is still honored after 599 rounds and expires after 600, and that an unspecified duration does not create immunity. The elapsed-time step drives the shared timer directly during fixture combat. Narration is exercised before and after it. The unchanged Fear of Fire probe remains red in that run. Initiative timing, target kinds beyond the monster fixture and aura automation require additional focused controls.

## Damage-triggered Disadvantage

The supplied reference requires Disadvantage on attack rolls and ability checks after taking a specified damage type, through the end of the creature's next turn. SRD 5.2.1 pp. 16–17 distinguishes damage, damage immunity and Temporary Hit Points. The shared trait registry recognizes a complete sentence for this timing and damage trigger. No monster name is part of the matching rule.

The existing monster and companion HP owners write the attack marker in the HP transaction. The marker starts only from positive typed damage. It remains independent from other Disadvantage markers. Existing initiative timing expires it at the creature's next turn end and skips its current end when damage recurs during its turn. The shared attack source reads the marker. A private GM notice covers manual NPC ability checks. A companion without its own turn receives the existing explicit timing handoff. The tested rule requires no schema migration.

`fresh-damage-trait-first-check.log` and `fresh-encounter-followup.log` passed the repaired Yeti probes. The GM damage route was exercised with mitigation and with an exact typed packet. The latter consumed Temporary Hit Points without reducing normal HP and refreshed the penalty during the Yeti's turn. Real GM turn advancement kept the penalty at the current turn end and removed it at the next turn end. The GM notice is visible to the GM, and the player was refused access. The GM dice route discarded the higher die and included the supplied ability modifier.

The invented Thunder controls covered unrelated damage, immunity and an explicit exact typed GM adjustment. The imported companion probe exercised actual HP change and confirmed its stored penalty and GM timing handoff. Companion attack consumption of this new trait, player weapon and item entry points, and unusual mixed packets still need independent route controls. The source-turn code for Chilling Gaze's Paralyzed condition also needs a route test distinct from the Fear of Fire timing.

The two invented auras passed a real turn start, created independent one-minute and 24-hour immunity records, and rerolled only the expired aura after a direct call to the elapsed-time owner. Historical encounter-level aura immunity remains recognized for encounters already in progress. This did not investigate an immunity across separate fights, where instance IDs are replaced.

## Remaining prerequisites

The imported Gaze stipulates one visible target within 30 feet. The current effect resolver's area check does not appear to enforce that single-target sentence. A route probe is needed before assigning a result. The original first run stays failed, and any next distinct replacement needs its own first-run record after fixes. Testing every entry of the original twenty and replacement acceptance remain open.

An unknown-duration aura produced a separate GM instruction in the authenticated advance reply. The player's current-turn and combat-log routes did not contain that instruction. The same route checked that the two known durations still worked. The turn resolver separates the note before building the shared output and stores it through the private notices owner. This targeted run and the source-timing regression checks are recorded in `fresh-aura-audience.log`.

The rider-gate, one-day default and same-caster overwrite mutations each caused their intended assertion to fail. All three source restorations were verified. Their log names begin with `fresh-mutation-`.

## Complete checkpoint validation

Code `f1b3fb2a0075219a934a62be617c91faabb9f71c` completed the shuffled PostgreSQL race run with 1,108 passes, two external-server skips and zero race reports. The source was vetted. Five additional isolated mutations were caught and restored. Dropping the damage trigger omitted the attack penalty. Removing the current-turn skip made the refreshed penalty expire too early. Using an incoming amount triggered the trait despite immunity. Dropping companion delivery removed the companion's penalty. Bypassing the private turn-note split removed its GM notice. The manifest records the full run and all eight mutations across both sets.

These results cover the scoped checks above. Separate targeting work addresses the subsequently reproduced range, target-count and sight defects. The original replacement run stays failed in the acceptance ledger.
