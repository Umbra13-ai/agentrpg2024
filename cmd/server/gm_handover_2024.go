package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// gm_handover_2024.go: which hand-overs to the GM are LASTING (an open item, gm_open_items_2024.go) and which the GM resolves in the same breath (one-shots,
// never listed). The sites that build "the GM resolves / applies ..." text call monsterHandoverSpell or monsterHandoverEntry with the creature and the entry
// in scope; a site that hands over a one-shot says so (`// gm-one-shot: why`), checked by TestEveryGMHandoverIsKindedOrOneShot.

// gmLastingSpells: a monster's spell the server doesn't run whose effect lasts and changes the fight, and how the server sees it end. A spell not
// here is a one-shot or passive (Detect Magic, Tongues, Plane Shift, Dispel Magic, Remove Curse, Misty Step ...): the GM resolves it in the same breath.
var gmLastingSpells = map[string]struct {
	End    string
	Rounds int
}{
	"fly": {End: endConcentration}, "shapechange": {End: endConcentration}, "gaseous-form": {End: endConcentration},
	"telekinesis": {End: endConcentration}, "confusion": {End: endConcentration}, "detect-thoughts": {End: endConcentration},
	"scrying": {End: endConcentration}, "faerie-fire": {End: endConcentration}, "slow": {End: endConcentration},
	"calm-emotions": {End: endConcentration}, "dispel-evil-and-good": {End: endConcentration}, "darkness": {End: endConcentration},
	"mirror-image": {End: endRounds, Rounds: 10}, "mage-armor": {End: endFight}, "longstrider": {End: endFight},
	"etherealness": {End: endFight}, "geas": {End: endNever}, "raise-dead": {End: endNever},
}

// monsterHandoverSpell lists a lasting spell the server didn't run; false when it is a one-shot (nothing is listed).
func monsterHandoverSpell(lobby, monsterID int, spellSlug string, targets []int) bool {
	row, ok := gmLastingSpells[spellSlug]
	if !ok || lobby == 0 {
		return false
	}
	sp, found := srdSpellsMemory[spellSlug]
	if !found {
		return false
	}
	end := row.End
	if end == endConcentration && !sp.Concentration {
		end = endFight
	}
	who := creatureName(lobby, monsterID)
	title := sp.Name + " (" + who + ")"
	if len(targets) > 0 {
		names := make([]string, len(targets))
		for i, t := range targets {
			names[i] = creatureName(lobby, t)
		}
		title += " on " + strings.Join(names, ", ")
	}
	detail := strings.TrimSpace(sp.Description)
	if r := []rune(detail); len(r) > 700 {
		detail = string(r[:700]) + "..."
	}
	return handOverToGM(gmHandover{Lobby: lobby, Source: monsterID, Targets: targets, Kind: "spell", Entry: spellSlug, Title: title,
		Detail: sp.Name + ": " + detail, EndKind: end, EndKey: spellSlug, Rounds: row.Rounds}) > 0
}

// entryLastingRe: a stat-block effect's own wording that it lasts (SRD 5.2.1): "until the end of its next turn", "for 1 minute", "for 24 hours",
// "until the grapple ends", "... dies", "Concentration", "under the wraith\u2019s control" (a creature it commands).
var entryLastingRe = regexp.MustCompile(`(?i)\buntil the (?:end|start) of (?:its|the [^.,;]+?) next turn\b|\bfor (\d+) (minutes?|hours?|days?)\b|\buntil [^.;]*?\b(?:ends|dies)\b|\bConcentration\b|\bunder the [a-z ]+?\W?s control\b`)

// monsterHandoverEntry lists the lasting part of a stat-block entry the server doesn't run, from its own wording; false when the text names no
// duration (a one-shot).
func monsterHandoverEntry(lobby, monsterID int, entryName, rest string, targets []int) bool {
	m := entryLastingRe.FindStringSubmatch(rest)
	if m == nil || lobby == 0 || strings.TrimSpace(entryName) == "" {
		return false
	}
	end, rounds := endCreature, 0
	low := strings.ToLower(m[0])
	switch {
	case strings.Contains(low, "next turn"):
		end, rounds = endRounds, 2
	case m[1] != "":
		n, _ := strconv.Atoi(m[1])
		switch {
		case strings.HasPrefix(m[2], "minute") && n <= 10:
			end, rounds = endRounds, n*10
		case strings.HasPrefix(m[2], "minute"):
			end = endFight
		default: // hours, days: past the fight
			end = endNever
		}
	}
	who := creatureName(lobby, monsterID)
	title := fmt.Sprintf("%s (%s)", stripUsage(entryName), who)
	if len(targets) > 0 {
		names := make([]string, len(targets))
		for i, t := range targets {
			names[i] = creatureName(lobby, t)
		}
		title += " on " + strings.Join(names, ", ")
	}
	return handOverToGM(gmHandover{Lobby: lobby, Source: monsterID, Targets: targets, Kind: "entry", Entry: stripUsage(entryName), Title: title,
		Detail: strings.TrimSpace(rest), EndKind: end, Rounds: rounds}) > 0
}

// handOverOnFailure: a failed save's GM-applied remainder (e.Extra) that lasts: a spell the server doesn't run, or a stat-block effect whose own wording
// names a duration.
func (e saveEffect) handOverOnFailure(lobbyID, tid int) {
	if e.Extra == "" || e.SourceID == 0 {
		return
	}
	if e.IsSpell {
		if e.SpellSlug != "" {
			monsterHandoverSpell(lobbyID, e.SourceID, e.SpellSlug, []int{tid})
		}
		return
	}
	if e.ImmunityName != "" {
		monsterHandoverEntry(lobbyID, e.SourceID, e.ImmunityName, e.Extra, []int{tid})
	}
}

// gmNotApplied is the bracket a hit's or a trigger's remainder the server doesn't apply carries for the GM ("[GM, not applied automatically: ...]"),
// and lists it as an open item when its own wording says it lasts (monsterHandoverEntry); a remainder with no duration stays in the reply alone.
func gmNotApplied(lobby, monsterID int, entryName, text string, targets []int) string {
	monsterHandoverEntry(lobby, monsterID, entryName, text, targets)
	return " [GM, not applied automatically: " + text + "]"
}

// recurringTraitRe: a trait's own wording that it acts every turn or on a trigger (SRD 5.2.1): "At the start of each of its turns" (Barbed Hide),
// "Whenever the golem starts its turn" (Berserk), "If the troll ends any turn" (Loathsome Limbs), "if it ends its turn in running water" (Vampire
// Weakness), "at the end of the aboleth's turn" (Mucus Cloud), "The first time it enters a creature's space on a turn" (Fire Form), "Whenever the
// hydra takes 25 damage" (Multiple Heads), "If the elemental takes Cold damage" (Freeze), "If the tarrasque is targeted by" (Reflective Carapace),
// "on every turn of combat" (Reactive).
var recurringTraitRe = regexp.MustCompile(`(?i)\bat the (?:start|end) of each of its turns\b|\bstarts its turn\b|\bends (?:any|its) turn\b|\bat the end of the [a-z]+\W?s turn\b|\bthe first time it enters a creature\W?s space on a turn\b|\bwhenever the [a-z]+ takes \d+ damage\b|\bif the [a-z]+ takes [a-z]+ damage\b|\bif the [a-z]+ is targeted by\b|\bon every turn of combat\b`)

// listRecurringTraits: when a monster enters a fight, each trait of its stat block that the server doesn't apply (monsterTraitRules has no row, or
// a GM row) and that acts every turn or on a trigger (recurringTraitRe) is an open item while the creature is in the fight. Every place that gives
// a monster its combat id calls it (TestEveryNewMonsterListsItsTraits). It returns how many it listed.
func listRecurringTraits(lobby, id int, key string) int {
	meta := monsterMetaFor(key)
	if meta == nil || lobby == 0 {
		return 0
	}
	n := 0
	who := creatureName(lobby, id)
	for _, t := range recurringGMTraits(meta.Traits) {
		if handOverToGM(gmHandover{Lobby: lobby, Source: id, Kind: "trait", Entry: t.Name, Title: fmt.Sprintf("%s (%s)", t.Name, who),
			Detail: strings.TrimSpace(t.Text), EndKind: endCreature}) > 0 {
			n++
		}
	}
	return n
}

// recurringGMTraits: the traits of a stat block that the server doesn't apply and that act every turn or on a trigger.
func recurringGMTraits(traits []srdMonsterEntry) []srdMonsterEntry {
	var out []srdMonsterEntry
	for _, t := range traits {
		if r, ok := traitRuleFor(t); ok && !r.GM {
			continue
		}
		if recurringTraitRe.MatchString(t.Text) {
			out = append(out, t)
		}
	}
	return out
}
