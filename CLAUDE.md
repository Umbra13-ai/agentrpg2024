# CLAUDE.md

For AI coding sessions working on the Agent RPG 2024 server (Go; code in `cmd/server` and `game/`). The rules are the
D&D SRD 5.2.1 only. Multiclassing is not supported and must never be offered or documented as an option (`multiclassAllowed`
always answers no). `AGENTS.md` describes the repository and `BUILD.md` how to build, run and test. If the owner's
notes are attached to your session, read their handoff file first for the standing rules and the current state.

With every step you propose or report as next, and with every set of options you offer, say which model and effort you
recommend for it (the owner's standing rule).

## How to fix a bug in this code

### How this codebase goes wrong
Most bugs here come from one rule being written in several places that drift apart. Examples: attack rolls were made
in 29 places, each with its own idea of Advantage, until they were given one owner; a builder step and the final review
each check the same answer in their own way; a choice's options, its suggestion, its validation and its refusal message
are often separate code; each request reads its body in its own way. A fix made in only one of those places leaves the
bug alive in the others and adds one more copy to maintain. So the goal of every fix is to correct the rule once, for
the whole server, and leave the code with fewer copies than before.

### The method
1. **Reproduce it, then name the cause as a rule.** The report is one symptom ("Wizard Scholar accepts any skill"). Ask
   what general rule is missing or wrong ("the server doesn't know that each Expertise feature has its own list of
   allowed skills"). Write that sentence down before you write code.
2. **Find every path the rule applies to.** Search the whole server for every handler, setup step, suggestion, message
   and doc that does the same job. List them. If the rule exists in more than one place, that duplication is the bug you
   are fixing.
3. **Give the rule one owner.** Put it in one function or one data table that every path calls, move every path onto
   it, and delete the copies. Prefer data to branches: a feature declares its allowed options in a table; a handler does
   not get an "if class is wizard". Don't key a fix to one class, monster, spell or feature name. If the SRD rule really
   is about one thing, that still belongs in the data for that thing, not in a handler.
4. **Derive everything from that owner.** Options shown, the suggestion, the check, the refusal text and the note that
   tells a player which request to send must all come from the same definition, so they cannot disagree.
5. **Guarantees belong in shared layers.** Something every request should do (refuse unknown fields, accept a displayed
   name or its slug, answer errors with 4xx) is built once where all requests pass through, not pasted into handlers one
   at a time.
6. **If the correct fix is large or risky** (for example moving every request onto one reader), stop and ask the owner
   before starting. Say what the full fix is and what it would need retested. Never quietly do the narrow version
   instead.
7. **Test the rule, not the example.** The test must go through every path that uses the rule (every class, every
   request that takes that kind of choice, every attack route), then the full regression suites, because a shared change
   reaches everything.
8. **Report the reach of the fix:** the cause in one sentence, the paths you found, which now use the shared rule, what
   you deleted, anything not yet moved and why, and what was tested where (local or live).

### Signs you are making a narrow fix (stop and go back to step 1)
- A condition on a specific class, subclass, monster, spell or feat inside a handler.
- The same check pasted into a second handler.
- Editing a hard-coded message or path that could be generated from the real definition.
- A new helper that is called from only one place while similar checks exist elsewhere.
- A fix that only adds code and removes none.
- A test that only checks the reported case.

### Worked example
Report: "Wizard Scholar accepts any skill for Expertise."
- Narrow fix (wrong): add "if the class is wizard, allow only these six skills" to the Expertise request, and filter the
  setup step's options separately.
- Correct fix: define each Expertise feature (Rogue, Bard, Ranger's Deft Explorer, Wizard's Scholar) with its level,
  count and allowed skills in one table. The Expertise request, the setup step's options, its suggestion and the refusal
  message all read that table. Remove the per-class code. Test every class that gets Expertise at each level it gets it.

### Rules that already have one owner
Add to the owner, never beside it. When you give another rule one owner, add it to this list.
- **Attack rolls:** `game.AttackRoll` (`game/attack.go`) is the rule of an attack roll (Advantage, Disadvantage and how
  they cancel, natural 1 and 20, Critical Hits, the wording of the result line). `attackCtx` and the `attackSources`
  table (`cmd/server/attack_2024.go`) decide what gives Advantage or Disadvantage, the bonuses, the target's Armor
  Class and what blocks an attack. Every attack in the server (a character's, a monster's, a spell's, a companion's, an
  item's) builds an `attackCtx` and calls `roll()`. A new source of Advantage or Disadvantage is one row in
  `attackSources`. Never roll a d20 for an attack anywhere else. An effect that depends on the outcome (a Weapon Mastery,
  Sneak Attack, a smite) is told after the outcome and only when it happens, never as a note written before the roll.
- **The 18 skills:** `game/skills.go`.
- **Expertise:** `game/expertise.go`, with its database side in `cmd/server/expertise_2024.go`.
- **Which feats a character can take:** `cmd/server/feats_2024.go` (`featRefusal`, `featsOpenTo`, `featTakerOf`, `featHowTo`).
  **A feat's choices** (Skilled, Magic Initiate) are checked by `resolveFeat` (`cmd/server/origins.go`) at creation and in the
  feat request (`takeOriginFeat`), and recorded in the origin record with their source; what they give comes from
  `originFeat.grants`. A Fighting Style feat taken with an Ability Score Improvement is one more Fighting Style (the
  `more` count of the Fighting Style row in `choiceDefs`).
- **The choices a character makes after creation:** `choiceDefs` (`cmd/server/choices_2024.go`): who has each choice, how
  many picks at each class level, how picks are counted, the request, the setup step. The level-up note, the sheet's
  `to_finish`, `GET /setup` and the level-up notice are made from it. A feature that picks one option from a list is a
  row in `featurePicks` (`cmd/server/feature_picks_2024.go`); both pick requests and character creation read it.
- **Reading a player's answer (a name or a slug):** `game.Key` (`game/match.go`) is the one fold: case, spaces, hyphens,
  underscores, "/" and apostrophes. `game.Match` matches an answer to a list of options; `game.Slug` gives the hyphen
  form spells and monsters are stored in; `lookupSpell` and `game.LookupSubclass`, `game.AbilityKey` and
  `game.FindTool` are built on it. Don't write another fold.
- **The spells a character already has:** `spellsHeld` (`cmd/server/prepared_spells_2024.go`).
- **Reading a request's body:** `readRequest` (`cmd/server/request_body_2024.go`) is the only reader. It needs one JSON
  object (an empty body only with `optionalBody()`), reads field names with `game.Key`, refuses an unknown field (also
  inside objects), a wrong type, malformed JSON and data after the object, takes an id that repeats the path's, and
  answers every refusal in one shape (`unknown_field`, `wrong_type`, `invalid_json`, `body_required`, `id_mismatch`, with
  `fields`). A map request names its fields with `onlyFields` (or `keysCheckedByHandler` where the handler explains each
  key: creation, the builder). A GM command reads with `gmRead` / `gmReadAs` (next rule). A request that takes
  no fields calls `noFields`; a wrapper that needs a field uses `peekBody`; a pending turn's steps are read with
  `parseRequest` against `actionFields`, the same type `/api/action` reads. Never decode `r.Body` anywhere else.
- **Which campaign a GM command is for:** `cmd/server/gm_campaign_2024.go`. A GM command reads with `gmRead` / `gmReadAs`
  (a GET without a body: `gmCampaignFor`). The campaign is the one named (`campaign_id`, body or query), else the
  campaign of the characters the command acts on (`campaignFromTarget(&req.CharacterID, ...)` when it needs one,
  `targetsIn(...)` when it may name some; `campaignOf(id, what)` for something its path names), else the GM's only
  campaign; it must be the GM's and active (`campaignBeforeStart()` for set-up and repair tools). Refusals: `not_gm`,
  `campaign_not_active`, `campaign_id_required`, `character_not_found`, `character_not_in_campaign`, `not_in_campaign`,
  `targets_in_different_campaigns`. Never look up `dm_id` or compare a character's `lobby_id` with the campaign in a GM
  handler.

### Before pushing
`gofmt -l` on the files you changed, `go vet ./cmd/server ./game`, `go test ./...`, then the owner's test scripts for
character creation, leveling and combat if you have them. Do not deploy unless the owner says "deploy".
