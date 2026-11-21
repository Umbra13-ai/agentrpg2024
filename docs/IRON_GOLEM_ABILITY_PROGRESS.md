# Supplied Iron Golem ability review

Source is the supplied reconstructed reference. The private fixture is passed to the automated tests at runtime and stays outside the source archive. The earlier selected-mechanics gate checked an imported Iron Golem's basic numbers and defenses, one attack and its save modifiers. It did not verify every ability.

Fire Absorption passed through the GM damage route on a wounded Iron Golem. The controls distinguish healing, immunity, ordinary damage, and the HP maximum. Immutable Form failed before correction, then passed through a player Polymorph cast. The mutation reproduced the failure. The ordinary mutable import still changes form. See the named logs in `MONSTER_IMMUTABLE_FORM_REVIEW.md`.

The next route probes cover Bladed Arm and Fiery Bolt, the GM workflow for the four ordered Multiattack choices, and Poison Breath with a failed save, successful save, cone exclusion and refusal while spent. The probes are prepared and waiting for the current complete suite. A complete result also needs recharge recovery, Magic Resistance under spells and other magical effects, the remaining defenses and the relevant movement and vision behavior. The supplied reference has not received a complete ability pass.

The prepared Magic Resistance probe pairs a spell, an explicitly magical nonspell save, and an explicitly nonmagical save. The same supplied target faces all three. The source is the supplied trait plus SRD 5.2.1 Rules Glossary definition of a Magical Effect. This tests Advantage's source rather than confusing a higher random roll with Advantage. These results are pending execution.

## Route results and repair

The first set of probes found two fixture errors. An absent battle map initially returns 404, which is expected before the test creates it. The original control caster's name contaminated the search for the Advantage source. After correcting both, the attack and Multiattack cases passed. The first breath run gave equal HP loss because the failed-save character retained a Dwarf origin record with poison resistance even after its displayed species was changed. The isolated control now checks the actual damage owner for no poison resistance. The corrected baseline passed cone exclusion, full and half damage, and the spent-use refusal. The new recharge test also advances real turns and checks the reported successful die and the next use. It has a bounded randomized roll sequence, which must be reported together with its validation scope.

The corrected baseline showed a game-rule defect for Magic Resistance. The spell produced Advantage and the explicitly nonmagical effect did not. The explicitly magical action produced no Advantage for either the monster or companion target. The shared save context now distinguishes a magical nonspell effect from a spell. The imported-effect parser recognizes explicit self-labeling sentences for actions, attacks, effects and traits. The named Magic Resistance source reads either case. Spell-only protections continue to use the separate spell flag. The focused follow-up passed on both target kinds and kept the nonmagical control negative. See `iron-golem-named-abilities-before-3.log` and `iron-golem-named-abilities-after.log`.

SRD 5.2.1 p. 185 defines a Magical Effect. This change handles the verified initial saving-throw route and explicit forms above. Other wording, magic items, and persistent effects' later saves need independent tests and coverage. Movement, vision, and the rest of the acceptance review remain open.

The recharge follow-up observed a six on the source's turn and confirmed the next use spent the attack again. Its fixture plays up to 500 real initiative advances if needed. This is randomized game sampling with a bounded limit, so coverage is recorded separately from deterministic save controls.

The first tests of all six listed condition immunities found that the Iron Golem companion acquired Charmed, Frightened, Paralyzed, Petrified and Poisoned while the fight monster correctly resisted them. Both forms asked the GM to track Exhaustion even though their stat block lists immunity. The shared condition owner now reads SRD and custom catalog immunities for companions, and the Exhaustion delivery checks the same rule before tracking or referring a case to the GM. All six cases passed for both forms on the focused route run. An earlier fixture log and the corrected result are in `iron-golem-immunities-before.log` and `iron-golem-immunities-after.log`.

All seven named entries now have reference-driven behavioral evidence (the Multiattack uses the documented GM choice workflow). Complete acceptance still needs remaining movement and vision checks, defense and attack interactions beyond the named probes, and the rest of the twenty-monster roster. The initial Iron Golem's reproduced failures also require a fresh unique replacement in the acceptance ledger.

## Validated checkpoint

Code `16d3c4c4a3d8a1141ef9eb067e236b0152f64d81` passed 1,099 cases in the complete shuffled race run with two external-server skips and no race reports. The PostgreSQL server suite took 473.486 seconds. The server shuffle seed was 1795043492519877964. The private fixture runs and twenty selected-mechanics cases were enabled.

Three isolated mutations triggered the intended assertions and were restored. Dropping the nonspell magical flag failed the explicitly magical save case. Removing the companion catalog immunity failed the companion condition controls. Bypassing the Exhaustion immunity check failed both forms' Exhaustion cases. Full details are in the checkpoint validation manifest.

The next distinct replacement fixture's first run is preserved separately. That candidate reproduced damage-triggered Disadvantage and saving-throw rider issues that are outside this checkpoint.
