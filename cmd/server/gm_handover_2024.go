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
	"etherealness": {End: endFight}, "power-word-stun": {End: endFight}, "geas": {End: endNever}, "raise-dead": {End: endNever},
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
	if len(detail) > 700 {
		detail = detail[:700] + "..."
	}
	return handOverToGM(gmHandover{Lobby: lobby, Source: monsterID, Targets: targets, Kind: "spell", Entry: spellSlug, Title: title,
		Detail: sp.Name + ": " + detail, EndKind: end, EndKey: spellSlug, Rounds: row.Rounds}) > 0
}

// entryLastingRe: a stat-block effect's own wording that it lasts (SRD 5.2.1): "until the end of its next turn", "for 1 minute", "for 24 hours",
// "until the grapple ends", "... dies", "Concentration".
var entryLastingRe = regexp.MustCompile(`(?i)\buntil the end of (?:its|the [^.,;]+?) next turn\b|\bfor (\d+) (minutes?|hours?|days?)\b|\buntil [^.;]*?\b(?:ends|dies)\b|\bConcentration\b`)

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
