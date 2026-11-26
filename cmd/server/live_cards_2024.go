package main

// What a party card on the Live Table says about its character (owner, Nov 23: the cards proposal; Nov 26: the cards are the Nov 25
// mock-up's, with small status pills added): liveCardExtras reads what the card needs beyond the party query (Temporary Hit Points,
// Heroic Inspiration, the Death Saving Throws, Hit Dice left, Concentration and the conditions, the companions) and cardPills is the one
// wording of it, shown on the cards by live_table_2024.go. How healthy a character looks is the health owner's label
// (game.Vitals.Health: Card.Health), never worked out again here.

import (
	"fmt"
	"sort"
	"strings"
)

// liveCond is one pill of a card's condition row; Tone is its CSS class suffix (arcane, gold, crimson).
type liveCond struct{ Label, Tone string }

// liveComp is one companion pill: its icon, name, Hit Points and a tooltip saying what it is and whose.
type liveComp struct {
	Icon, Label, Title string
	HP, MaxHP          int
	Away               bool // a familiar in its pocket dimension
}

// companionIcon: the pill's icon by what the companion is.
func companionIcon(kind string) string {
	switch kind {
	case "steed":
		return "🐎"
	case "familiar":
		return "🦉"
	case "summon":
		return "✨"
	}
	return "🐾"
}

// companionPillLabel: a companion's name on its owner's card, without the owner's name ("Aldren's riding horse" on Aldren's card is
// "riding horse"). Only a leading "<the owner's full or first name>'s " goes (either apostrophe, any case); the stored name and the
// tooltip stay, and another character's possessive is left as it is.
func companionPillLabel(label, owner string) string {
	l := strings.TrimSpace(label)
	names := []string{strings.TrimSpace(owner)}
	if first, _, ok := strings.Cut(strings.TrimSpace(owner), " "); ok {
		names = append(names, first)
	}
	for _, n := range names {
		if n == "" {
			continue
		}
		for _, apos := range []string{"'s ", "\u2019s "} {
			if len(l) > len(n)+len(apos) && strings.EqualFold(l[:len(n)], n) && l[len(n):len(n)+len(apos)] == apos {
				if rest := strings.TrimSpace(l[len(n)+len(apos):]); rest != "" {
					return rest
				}
			}
		}
	}
	return label
}

// liveCardExtras fills in what a card shows beyond the party query: Temporary Hit Points, Inspiration, the Death Saving Throws, Hit Dice
// left, Concentration and the conditions from the character's own record list, and its companions.
func liveCardExtras(c *liveCard) error {
	var succ, fail int
	var conc string
	if err := db.QueryRow(`SELECT COALESCE(temp_hp, 0), COALESCE(inspiration, false), COALESCE(death_save_successes, 0), COALESCE(death_save_failures, 0),
		COALESCE(concentrating_on, '') FROM characters WHERE id = $1`, c.ID).Scan(&c.TempHP, &c.Inspired, &succ, &fail, &conc); err != nil {
		return err
	}
	c.DeathSucc, c.DeathFail = succ, fail
	if conc != "" {
		c.Conds = append(c.Conds, liveCond{"Conc: " + titleCase(strings.ReplaceAll(conc, "-", " ")), "arcane"})
	}
	seen := map[string]bool{}
	for _, cond := range getCharConditions(c.ID) { // the condition owner's list: the records, plus exhaustion:N and the markers
		base, detail := splitConditionEntry(cond)
		if seen[base] { // two sources of one condition are one pill
			continue
		}
		seen[base] = true
		switch {
		case base == "exhaustion":
			label := "Exhaustion"
			if detail != "" {
				label += " " + detail
			}
			c.Conds = append(c.Conds, liveCond{label, "gold"})
		case ruleConditions[base]:
			c.Conds = append(c.Conds, liveCond{titleCase(base), "crimson"})
		}
	}
	left := hitDiceLeftByType(c.ID)
	var dice []string
	for die, n := range left {
		dice = append(dice, fmt.Sprintf("%d%s", n, die))
	}
	sort.Strings(dice)
	c.HitDice = strings.Join(dice, "+")
	for _, comp := range companionsOf(c.ID) {
		label := comp.Name
		if comp.Nickname != "" {
			label = comp.Nickname
		}
		label = companionPillLabel(label, c.Name) // it is on its owner's card: the owner's name is not repeated
		c.Comps = append(c.Comps, liveComp{Icon: companionIcon(comp.Kind), Label: label, Title: titleCase(comp.Kind) + " of " + c.Name,
			HP: comp.HP, MaxHP: comp.MaxHP, Away: comp.Status == "dismissed"})
	}
	return nil
}

// livePill is one status pill of a party card on the Live Table (owner, Nov 26: the cards keep the mock-up's design with small pills
// added): its words and its tone (crimson, gold, arcane, emerald), which picks the pill's color class in the page.
type livePill struct {
	Label string `json:"label"`
	Tone  string `json:"tone"`
}

// cardPills: what is on a character, as its card's pills, from the card liveCardExtras filled: dead, dying with its Death Saving
// Throws, or stable (the health owner's label), Concentration and the conditions, Temporary Hit Points, Heroic Inspiration and its
// companions with their Hit Points. The one wording of a card's state.
func cardPills(c liveCard) []livePill {
	pills := []livePill{}
	dead := c.Health == "dead"
	switch c.Health {
	case "dead":
		pills = append(pills, livePill{"Dead", "crimson"})
	case "dying":
		pills = append(pills, livePill{fmt.Sprintf("Dying · saves %d✓ %d✗", c.DeathSucc, c.DeathFail), "crimson"})
	case "stable":
		pills = append(pills, livePill{"Stable", "gold"})
	}
	for _, cd := range c.Conds {
		pills = append(pills, livePill{cd.Label, cd.Tone})
	}
	if !dead && c.TempHP > 0 {
		pills = append(pills, livePill{fmt.Sprintf("+%d Temp HP", c.TempHP), "arcane"})
	}
	if c.Inspired {
		pills = append(pills, livePill{"★ Inspiration", "gold"})
	}
	for _, cm := range c.Comps {
		hp := fmt.Sprintf("%d/%d", cm.HP, cm.MaxHP)
		if cm.Away {
			hp = "away"
		}
		pills = append(pills, livePill{cm.Icon + " " + cm.Label + " " + hp, "emerald"})
	}
	return pills
}
