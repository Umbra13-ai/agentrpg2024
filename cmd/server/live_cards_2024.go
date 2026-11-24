package main

// The Party & Initiative cards of the Live Table (owner, Nov 23: the "Party & Initiative Cards" proposal): a portrait or class icon, the
// name and "Level N Species Class", Hit Points with Temporary Hit Points (and the bar that shows both), Hit Dice left, Armor Class, the
// Heroic Inspiration pip and a Turn mark, then one row of what is on the character: dead, dying (with the Death Saving Throw dots) or
// stable, Concentration, the rules conditions, and its companions (familiar, steed, summon, pet) with their Hit Points. liveCardExtras
// reads what the card needs beyond the party query; liveCardHTML is the one render of a card. How healthy a character looks is the
// health owner's label (game.Vitals.Health: Card.Health), never worked out again here.

import (
	"fmt"
	"html"
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

// sheetIcon: the small paper sheet in a card's top right corner (owner, Nov 24), drawn in the theme's accent with currentColor.
const sheetIcon = `<svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14 3H6.5A1.5 1.5 0 0 0 5 4.5v15A1.5 1.5 0 0 0 6.5 21h11a1.5 1.5 0 0 0 1.5-1.5V8z"/><path d="M14 3v5h5"/><path d="M8.5 12.5h7M8.5 16h7"/></svg>`

// liveCardHTML renders one party card: a tap (or Enter or Space) opens the quick sheet in the page's script; the sheet icon in its top
// right corner is the link to the character's full sheet (a link can't hold another link, so the card itself is not one).
func liveCardHTML(c liveCard) string {
	e := html.EscapeString
	dead, dying := c.Health == "dead", c.Health == "dying"
	hp := c.HP
	if dead {
		hp = 0
	}
	pool := max(c.MaxHP, hp+c.TempHP)
	hpPct := 0
	if pool > 0 {
		hpPct = max(0, min(100, (hp*100+pool/2)/pool))
	}
	thpPct := 0
	if !dead && c.TempHP > 0 && pool > 0 {
		thpPct = max(4, min(100-hpPct, (c.TempHP*100+pool/2)/pool))
	}
	color := hpFillColor(c.Health)

	cls := "party-card"
	badge := ""
	if c.IsTurn {
		cls += " is-turn"
		badge = `<span class="badge badge-recruiting party-turn">Turn</span>`
	}
	if dead {
		cls += " is-dead"
	}
	face := "<span>" + e(c.Icon) + "</span>"
	if c.HasPortrait {
		face = fmt.Sprintf(`<img src="/api/characters/%d/portrait?v=%d" alt="%s" />`, c.ID, c.PortraitAt, e(shortName(c.Name)))
	}
	pip := ""
	if c.Inspired {
		pip = `<span class="insp-pip" title="Heroic Inspiration">★</span>`
	}
	temp := ""
	if !dead && c.TempHP > 0 {
		temp = fmt.Sprintf(`<span class="font-mono party-temp" title="Temporary Hit Points">+%d</span>`, c.TempHP)
	}
	hd := ""
	if c.HitDice != "" {
		hd = `<span class="font-mono party-hd" title="Hit Point Dice left">HD ` + e(c.HitDice) + `</span>`
	}
	barTitle := fmt.Sprintf("HP %d/%d", hp, c.MaxHP)
	if !dead && c.TempHP > 0 {
		barTitle += fmt.Sprintf(" + %d Temp HP", c.TempHP)
	}
	thp := ""
	if thpPct > 0 {
		thp = fmt.Sprintf(`<div class="thp-fill-seg" style="width:%d%%;"></div>`, thpPct)
	}

	var row strings.Builder
	switch {
	case dead:
		row.WriteString(`<span class="cond-pill cond-dead">Dead</span>`)
	case dying:
		dots := func(n int, class string) string {
			var s strings.Builder
			for i := 0; i < 3; i++ {
				on := ""
				if i < n {
					on = " " + class
				}
				s.WriteString(`<span class="ds-dot` + on + `"></span>`)
			}
			return s.String()
		}
		row.WriteString(fmt.Sprintf(`<span class="cond-pill cond-dying" title="Death Saving Throws: %d successes, %d failures"><span>Dying</span><span class="ds-track">%s<span class="ds-sep">/</span>%s</span></span>`,
			c.DeathSucc, c.DeathFail, dots(c.DeathSucc, "is-succ"), dots(c.DeathFail, "is-fail")))
	case c.Health == "stable":
		row.WriteString(`<span class="cond-pill cond-gold">Stable</span>`)
	}
	for _, cd := range c.Conds {
		row.WriteString(`<span class="cond-pill cond-` + cd.Tone + `">` + e(cd.Label) + `</span>`)
	}
	var comps strings.Builder
	for _, cm := range c.Comps {
		hpText := fmt.Sprintf("%d/%d", cm.HP, cm.MaxHP)
		if cm.Away {
			hpText = "away"
		}
		comps.WriteString(fmt.Sprintf(`<span class="companion-pill" title="%s"><span>%s %s</span><span class="font-mono party-comp-hp">%s</span></span>`, e(cm.Title), cm.Icon, e(cm.Label), hpText))
	}
	footer := ""
	if row.Len() > 0 || comps.Len() > 0 {
		footer = `<div class="party-bottom-row"><div class="cond-list">` + row.String() + `</div>`
		if comps.Len() > 0 {
			footer += `<div class="comp-group">` + comps.String() + `</div>`
		}
		footer += `</div>`
	}
	return fmt.Sprintf(`
        <div class="%s" role="button" tabindex="0" data-inspect-char="%d" title="%s">
          <a class="party-sheet-link" href="/character/%d" title="%s's character sheet" aria-label="Open %s's character sheet">%s</a>
          <div>
            <div class="party-card-row">
              <div class="party-avatar-col">
                <div class="party-avatar-wrap"><div class="party-avatar">%s</div>%s</div>
                %s
              </div>
              <div class="party-meta-col">
                <strong class="font-rpg party-name">%s</strong>
                <div class="party-meta">%s</div>
                <div class="party-stats-line">
                  <span><span class="font-mono" style="color:%s; font-weight:600;">HP %d/%d</span>%s</span>
                  %s
                  <span class="font-mono party-ac">AC %d</span>
                </div>
              </div>
            </div>
            <div class="hp-bar-wrap" title="%s"><div class="hp-fill-seg" style="width:%d%%; background:%s;"></div>%s</div>
          </div>%s
        </div>`, cls, c.ID, e(c.Activity), c.ID, e(c.Name), e(c.Name), sheetIcon, face, pip, badge, e(c.Name), e(c.Meta), color, hp, c.MaxHP, temp, hd, c.AC, e(barTitle), hpPct, color, thp, footer)
}
