# Creature type during transformations

The supplied SRD 5.2.1 gives explicit type retention for Wild Shape (p. 43) and Polymorph (p. 153). Earlier code inferred a type change from the new form. It rejected Hold Person for Wild Shape and permitted Beast-only targeting after Polymorph. The earlier route test encoded those outcomes. The revised test follows the source text and retains the original unknown-species control.

`retained-type-before.log` shows that the revised checks fail on the prior code. The authenticated GM narration route treated a Human in Wild Shape as unknown and a Polymorphed Human as a Beast. An imported Humanoid in a Wolf form received the same incorrect handling. The imported monster and player forms are controlled fixture state for targeting. These tests exercise the target eligibility and resulting saving throws and conditions, separately from casting Polymorph or entering Wild Shape.

The original species determines the character's type for these two transformations. The shared monster type reader uses the original stat-block key from the existing Polymorph snapshot when a Beast form currently occupies the turn entry. This is a read of the same application database and does not advance or clear spell effects. Invalid or missing original data stays unresolved. Untransformed Beast companions keep the existing stat-block lookup. Companion forms whose chosen type differs from their base stat block, and other transformations or recasts that overwrite the original snapshot, still need separate checks.

The revised route case and its imported monster subcase passed (`retained-type-after.log`). This validates those targeting checks. It does not validate all transformation statistics, temporary HP, or every ability of the test monsters.
