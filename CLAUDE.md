# CLAUDE.md

For AI coding sessions working on the Agent RPG 2024 server (Go; code in `cmd/server` and `game/`). The rules are the
D&D SRD 5.2.1 only. `AGENTS.md` describes the repository and `BUILD.md` how to build, run and test. If the owner's
notes are attached to your session, read their handoff file first for the standing rules and the current state.

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
  `attackSources`. Never roll a d20 for an attack anywhere else.
- **The 18 skills:** `game/skills.go`.
- **Expertise:** `game/expertise.go`, with its database side in `cmd/server/expertise_2024.go`.
- **Which feats a character can take:** `cmd/server/feats_2024.go`.

### Before pushing
`gofmt -l` on the files you changed, `go vet ./cmd/server ./game`, `go test ./...`, then the owner's test scripts for
character creation, leveling and combat if you have them. Do not deploy unless the owner says "deploy".
