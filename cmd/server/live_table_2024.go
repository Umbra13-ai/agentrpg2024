package main

// The site's front pages (owner, Nov 26: "Replace the whole site's design" with the Nov 25 mock-up, "Agent RPG 2024 — Redesigned UI
// Mockup": its Current, Campaigns, Universe and Settings tabs). The page is the mock-up's own file (data/preview_table_2024.html) with
// its stylesheet compiled from it (data/preview_table_2024.css: Tailwind 3.4.17, as the mock-up's CDN script would make it) and its demo
// constants replaced by a campaign's: fillPreviewTable fills CAMPAIGNS, PARTY, ENEMIES, CHRONICLE_PAGES and the rest from the Live
// Table's loader (loadLiveTable), the story reader's (loadStory) and the stat blocks. Every text is HTML-escaped here, as the mock-up's
// script puts the values into the page as markup. serveAppShell serves it on the site's routes, each opening its tab: / and /live
// (Current, the featured campaign), /campaign/{id} (Current, that campaign), /campaigns, /universe and /settings.
//
// The owner's file is used as it is; its only changes are hooks, each marked "hook (site, Nov 26)" in it: the tab the route opens
// (LIVE.initialTab) and the address a tab click sets (history), a campaign opened by its page (/campaign/{id}), the theme kept under the
// site's one key (themes_2024.go), the status pills on the party cards (cardPills), the SRD compendium's lists on the Universe tab
// (LIVE.compendium), the site's other pages on the Settings tab (LIVE.guides, settingsLinks), the full sheet's link in the quick sheet and the
// server's start in the page and its Agent Quickstart (startQuickSteps: an agent reading the home page reads the start).

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agentrpg/agentrpg/game"
	"github.com/lib/pq"
)

//go:embed data/preview_table_2024.html
var previewTableHTML string

//go:embed data/preview_table_2024.css
var previewTableCSS string

// previewTablePath: where the preview was served, under /preview/ (it sends the address to the site's front page now).
const previewTablePath = "gui-2026-11-25/"

type previewAction struct {
	Name   string `json:"name"`
	Hit    string `json:"hit"`
	Detail string `json:"detail"`
	Dmg    string `json:"dmg"`
}

// previewUnit is one PARTY, ENEMIES or CREATURES entry of the mock-up. HPText and Pct are the Hit Points as the page may show them: a
// character's numbers; a monster's look only (the health owner's label: a spectator never sees a monster's Hit Points).
type previewUnit struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	ShortName string          `json:"shortName"`
	Subtitle  string          `json:"subtitle,omitempty"`
	Role      string          `json:"role"`
	HP        int             `json:"hp"`
	MaxHP     int             `json:"maxHp"`
	HPText    string          `json:"hpText"`
	Pct       int             `json:"pct"`
	HD        string          `json:"hd"`
	AC        string          `json:"ac"`
	Init      string          `json:"init"`
	IsEnemy   bool            `json:"isEnemy,omitempty"`
	Avatar    string          `json:"avatar"`
	Tag       string          `json:"tag,omitempty"`
	Actions   []previewAction `json:"actions"`
	Pills     []livePill      `json:"pills"`           // what is on it (cardPills; a monster: its conditions only)
	Model     *livePlayer     `json:"model,omitempty"` // the AI model its player says it plays with (self-reported: agent_model_2024.go); a character's only
}

// livePlayer is a character's player's model as a page shows it: the family's symbol and color for the token card, the text for the sheet.
type livePlayer struct {
	Symbol string `json:"symbol"`
	Color  string `json:"color"`
	Label  string `json:"label"`
	Text   string `json:"text"`
}

type previewCampaign struct {
	ID         int    `json:"id"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Mode       string `json:"mode"`
	Levels     string `json:"levels"`
	GM         string `json:"gm"`
	Players    string `json:"players"`
	Cadence    string `json:"cadence"`
	Pages      int    `json:"pages"` // its story's passages, for the unread count this browser keeps
	Unread     int    `json:"unread"`
	ResumePage int    `json:"resumePage"`
	Teaser     string `json:"teaser"`
	Pinned     bool   `json:"pinned"`
	HeroImg    string `json:"heroImg"`
}

type previewCreatureRef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Alias  string `json:"alias"`
	Tag    string `json:"tag"`
	Avatar string `json:"avatar"`
}

type previewPage struct {
	Page        int                 `json:"page"`
	ID          string              `json:"id"`
	Chapter     string              `json:"chapter"`
	At          string              `json:"at"` // RFC 3339: the page writes the exact and relative times in the viewer's chosen zone
	SpeakerID   string              `json:"speakerId"`
	SpeakerName string              `json:"speakerName"`
	RoundTag    string              `json:"roundTag"`
	Roll        string              `json:"roll"`
	Starred     bool                `json:"starred"`
	Featured    *previewCreatureRef `json:"featuredCreature,omitempty"`
	Text        string              `json:"text"`
}

type previewLore struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Body  string `json:"body"` // markup, escaped here
}

// appTabs: the app shell's tabs, as the routes name them (siteNav's Tab).
var appTabs = map[string]bool{"current": true, "campaigns": true, "universe": true, "settings": true}

// compendiumLink: one list of the SRD compendium on the Universe tab.
type compendiumLink struct {
	Name  string `json:"name"`
	Count string `json:"count"`
	Href  string `json:"href"`
}

// serveAppShell writes the app shell opened on tab, with campaignID's table (0: none; the tabs still work). The caller checked the
// campaign may be watched; a campaign that doesn't exist is 404.
func serveAppShell(w http.ResponseWriter, r *http.Request, tab string, campaignID int) {
	if !appTabs[tab] {
		tab = "current"
	}
	e := html.EscapeString
	comp := []compendiumLink{{Name: "Browse and search the whole compendium", Href: "/universe/srd"}}
	for _, c := range universeCategories() {
		comp = append(comp, compendiumLink{Name: e(c.Name), Count: e(c.Count), Href: c.Href})
	}
	guides := make([]map[string]string, 0, len(settingsLinks))
	for _, l := range settingsLinks {
		guides = append(guides, map[string]string{"label": e(l.Label), "href": l.Href, "note": e(l.Note)})
	}
	data := map[string]interface{}{"campaignId": 0, "campaigns": previewCampaigns(r, campaignID), "party": []previewUnit{}, "enemies": []previewUnit{},
		"creatures": []previewUnit{}, "chronicle": []previewPage{}, "lore": []previewLore{}, "gm": "", "combat": false, "round": 0, "roundLabel": "",
		"fallbackAvatar": previewIconAvatar("🛡️"), "registerCurl": html.EscapeString(withBaseURL(startCurl())), "registerRequest": html.EscapeString(strings.Join(strings.Fields(registerRequest)[:2], " ")),
		"initialTab": tab, "compendium": comp, "guides": guides, "themeKey": themeStoreKey, "version": e("v" + version)}
	title := "Agent RPG 2024"
	if campaignID > 0 {
		t, what, err := loadLiveTable(campaignID)
		if err == sql.ErrNoRows {
			http.Error(w, "Campaign not found", http.StatusNotFound)
			return
		}
		if err != nil {
			writeDBErrorPage(w, err, what)
			return
		}
		_, _, chapters, _, ok := loadStory(w, campaignID)
		if !ok {
			return
		}
		title = t.Name + " - Agent RPG 2024"
		fillPreviewTable(data, t, chapters)
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	js, _ := json.Marshal(data)
	page := strings.Replace(previewTableHTML, "{{TITLE}}", html.EscapeString(title), 1)
	page = strings.Replace(page, "{{TAILWIND}}", previewTableCSS, 1)
	page = strings.Replace(page, "{{THEME_SCRIPT}}", themeHeadScript(), 1)
	var start strings.Builder // the server's start, the same steps the page shell's Agent Quickstart shows
	for _, step := range startQuickSteps() {
		start.WriteString("<li>" + step + "</li>")
	}
	page = strings.Replace(page, "{{AGENT_START}}", withBaseURL(start.String()), 1)
	page = strings.Replace(page, "{{DATA}}", string(js), 1)
	fmt.Fprint(w, page)
}

// handleSettingsPage serves the Settings tab (GET /settings).
func handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	serveAppShell(w, r, "settings", featuredCampaign(w, r))
}

func fillPreviewTable(data map[string]interface{}, t liveTable, chapters []storyChapter) {
	e := html.EscapeString
	data["campaignId"], data["gm"], data["combat"], data["round"] = t.ID, e(t.GM.Name), t.Combat, t.Round
	if t.Combat {
		data["roundLabel"] = fmt.Sprintf("Round %d", t.Round)
	}
	inits := map[int]int{}
	var order []map[string]interface{}
	json.Unmarshal(t.TurnOrder, &order)
	if t.Combat {
		for _, en := range order {
			inits[asInt(en["id"])] = asInt(en["initiative"])
		}
	}

	party := []previewUnit{}
	byName := map[string]string{}
	for _, c := range t.Party {
		u := previewUnit{ID: fmt.Sprintf("c%d", c.ID), Name: e(c.Name), ShortName: e(shortName(c.Name)), Role: e(c.Meta), HP: c.HP, MaxHP: c.MaxHP,
			HPText: fmt.Sprintf("%d/%d", c.HP, c.MaxHP), Pct: pct(c.HP, c.MaxHP), HD: e(c.HitDice), AC: strconv.Itoa(c.AC), Actions: []previewAction{}, Pills: []livePill{}}
		if c.HasPortrait {
			u.Avatar = fmt.Sprintf("/api/characters/%d/portrait?v=%d", c.ID, c.PortraitAt)
		} else {
			u.Avatar = previewIconAvatar(c.Icon)
		}
		var dex, stored, level int
		var class string
		db.QueryRow(`SELECT COALESCE(dex, 10), COALESCE(initiative_bonus, 0), COALESCE(level, 1), COALESCE(class, '') FROM characters WHERE id = $1`, c.ID).
			Scan(&dex, &stored, &level, &class)
		u.Init = signedBonus(game.Modifier(dex) + initiativeBonus(c.ID, class, level, stored))
		if n, ok := inits[c.ID]; ok {
			u.Init += fmt.Sprintf(" (%d)", n)
		}
		if sb := characterStatBlock(c.ID); sb != nil {
			if attacks, ok := sb["attacks"].([]sbEntry); ok {
				for _, a := range attacks {
					if a.line != nil {
						u.Actions = append(u.Actions, previewAction{Name: e(a.Name), Hit: e(a.line.Hit), Detail: e(a.line.Detail), Dmg: e(a.line.Damage)})
					}
				}
			}
		}
		for _, p := range cardPills(c) {
			u.Pills = append(u.Pills, livePill{e(p.Label), p.Tone})
		}
		mb := characterModelBadge(c.ID)
		u.Model = &livePlayer{Symbol: e(mb.Symbol), Color: e(mb.Color), Label: e(mb.Label), Text: e(mb.Text)}
		byName[strings.ToLower(c.Name)] = u.ID
		party = append(party, u)
	}
	data["party"] = party

	// the fight's other creatures (monsters: negative ids; companions are their owners')
	enemies := []previewUnit{}
	inFight := map[string]string{}     // a stat block's key → the first creature of the fight with it
	enemyByName := map[string]string{} // a creature's name in the fight → its id
	for _, en := range order {
		id := asInt(en["id"])
		if !t.Combat || id >= 0 || isCompanionCombatID(id) || asInt(en["hp"]) <= 0 {
			continue
		}
		key := sprintAny(en["monster_key"])
		u := previewMonster(key)
		u.ID = fmt.Sprintf("m%d", -id)
		u.Name, u.ShortName = e(fmt.Sprint(en["name"])), e(shortName(fmt.Sprint(en["name"])))
		look := game.MonsterHealth(asInt(en["hp"]), asInt(en["max_hp"]))
		u.HP, u.MaxHP, u.HPText, u.Pct = 0, 0, e(titleCase(look)), lookPct(look)
		u.IsEnemy = true
		if u.Avatar == "" { // a monster with no token (a GM's own without one): the old stage's emoji
			u.Avatar = previewIconAvatar("👹")
		}
		for _, cond := range creatureConditions(t.ID, id) { // its conditions only: never its Hit Points
			if base, _ := splitConditionEntry(cond); ruleConditions[base] {
				u.Pills = append(u.Pills, livePill{e(titleCase(base)), "crimson"})
			}
		}
		if n, ok := inits[id]; ok {
			u.Init = strings.TrimSpace(u.Init + fmt.Sprintf(" (%d)", n))
		}
		if _, ok := inFight[key]; !ok && key != "" {
			inFight[key] = u.ID
		}
		enemies = append(enemies, u)
		enemyByName[strings.ToLower(fmt.Sprint(en["name"]))] = u.ID
	}
	data["enemies"] = enemies

	// the story, one page a passage; a creature it shows is the fight's when it is in it, else one of the encountered creatures
	rounds := previewRounds(t.ID)
	stars := []previewPage{}
	creatures := []previewUnit{}
	known := map[string]string{}
	page := 0
	for _, ch := range chapters {
		chapter := e(ch.Num + " — " + ch.Title)
		for _, p := range ch.Entries {
			page++
			pg := previewPage{Page: page, ID: p.ID, Chapter: chapter, At: p.at.UTC().Format(time.RFC3339), Text: e(p.Text), Roll: e(p.Dice)}
			switch {
			case p.Type == "gm" && p.Speaker == "":
				pg.SpeakerID, pg.SpeakerName = "narrator", e(gmNarrationName(t.GM.Name))
			case p.Type == "gm": // a monster's turn: its token when it is in the fight, else the GM's tile
				pg.SpeakerID, pg.SpeakerName = enemyByName[strings.ToLower(p.Speaker)], e(p.Speaker)
				if pg.SpeakerID == "" {
					pg.SpeakerID = "gm"
				}
			default:
				pg.SpeakerID, pg.SpeakerName = byName[strings.ToLower(p.Speaker)], e(p.Speaker)
				if pg.SpeakerID == "" {
					pg.SpeakerID = "former"
				}
			}
			kind := titleCase(strings.ReplaceAll(p.kind, "_", " "))
			if r := rounds(p.at); r != "" {
				kind = r + " · " + kind
			}
			pg.RoundTag = e(kind)
			if len(p.Creatures) > 0 {
				c := p.Creatures[0]
				ref := &previewCreatureRef{Name: e(c.Name), Avatar: c.Token}
				if id, ok := inFight[c.key]; ok {
					ref.ID = id
				} else if id, ok := known[c.key]; ok {
					ref.ID = id
				} else {
					u := previewMonster(c.key)
					u.ID, u.Avatar = fmt.Sprintf("cr%d", len(creatures)+1), c.Token
					u.Name, u.ShortName = e(c.Name), e(shortName(c.Name))
					u.HPText, u.Pct = fmt.Sprintf("%d", u.MaxHP), 100 // a creature out of the fight: its stat block's Hit Points
					known[c.key], ref.ID = u.ID, u.ID
					creatures = append(creatures, u)
				}
				for _, u := range append(append([]previewUnit{}, enemies...), creatures...) {
					if u.ID == ref.ID {
						ref.Tag = u.Tag
						if u.Name != ref.Name {
							ref.Alias = u.Name
						}
					}
				}
				pg.Featured = ref
			}
			stars = append(stars, pg)
		}
	}
	data["chronicle"], data["creatures"] = stars, creatures

	var lore []previewLore
	if strings.TrimSpace(t.Setting) != "" {
		lore = append(lore, previewLore{"setting", "SETTING", strings.ReplaceAll(e(t.Setting), "\n", "<br>")})
	}
	if t.TableRules != "" {
		lore = append(lore, previewLore{"rules", "TABLE RULES", t.TableRules})
	}
	if len(t.Observations) > 0 {
		var b strings.Builder
		for i, o := range t.Observations {
			if i > 0 {
				b.WriteString("<br><br>")
			}
			b.WriteString(e(o.Content) + ` <span class="text-theme-muted">— ` + e(o.Observer) + ", " + e(o.Time) + `</span>`)
		}
		lore = append(lore, previewLore{"observations", "OBSERVATIONS", b.String()})
	}
	if lore == nil {
		lore = []previewLore{}
	}
	data["lore"] = lore
}

// gmNarrationName: who a narration is by, in the Chronicle: "GM Alden's Narration" (owner, Nov 26: not "Narrator"); "GM's Narration" with no GM yet.
func gmNarrationName(gm string) string {
	if strings.TrimSpace(gm) == "" {
		return "GM's Narration"
	}
	return "GM " + strings.TrimSpace(gm) + "'s Narration"
}

// previewMonster: a stat block's public face for the page (its size, type and alignment, Armor Class, Hit Point Dice, Initiative and its
// attacks as the attack owner reads them); a key with no stat block gives an empty one.
func previewMonster(key string) previewUnit {
	e := html.EscapeString
	u := previewUnit{Actions: []previewAction{}, Pills: []livePill{}, Avatar: monsterTokenURL(key)}
	if u.Avatar == "" {
		u.Avatar = previewIconAvatar("👹")
	}
	var name, size, typ, align, hd string
	var ac, hp, init int
	var entries []byte
	if monsterQueryRow(`SELECT name, COALESCE(size,''), COALESCE(type,''), COALESCE(alignment,''), COALESCE(ac,10), COALESCE(hp,0), COALESCE(hit_dice,''),
		COALESCE(initiative_mod,0), COALESCE(action_entries,'[]') FROM monsters WHERE slug = $1`, key).
		Scan(&name, &size, &typ, &align, &ac, &hp, &hd, &init, &entries) != nil {
		return u
	}
	kind := strings.TrimSpace(titleCase(size) + " " + titleCase(typ))
	u.Tag, u.Role, u.Subtitle = e(kind), e(kind), e(name+" · "+kind)
	if align != "" {
		u.Role = e(kind + " · " + align)
	}
	u.AC, u.HD, u.MaxHP, u.Init = strconv.Itoa(ac), e(hd), hp, signedBonus(init)
	var list []struct{ Name, Text string }
	json.Unmarshal(entries, &list)
	for _, a := range list {
		pa := parseMonsterAttack(a.Name, a.Text) // the one reader of a stat block's attack (monster_damage_choice_2024.go)
		act := previewAction{Name: e(a.Name), Detail: e(previewClip(a.Text, 140))}
		if pa.AttackBonus != 0 || pa.DamageDice != "" || pa.FlatDamage != nil {
			act.Hit = signedBonus(pa.AttackBonus)
			switch {
			case pa.DamageDice != "":
				act.Dmg = e(pa.DamageDice + " " + strings.ToLower(pa.DamageType))
			case pa.FlatDamage != nil:
				act.Dmg = e(fmt.Sprintf("%d %s", *pa.FlatDamage, strings.ToLower(pa.DamageType)))
			}
		}
		u.Actions = append(u.Actions, act)
	}
	return u
}

// previewRounds: the round a moment of the story fell in, from the campaign's round record read once ("Campaign round 3"; "" before the
// first or without the schedule).
func previewRounds(lobby int) func(time.Time) string {
	type rnd struct {
		at   time.Time
		name string
	}
	var list []rnd
	clock, err := clockOf(db, lobby)
	if err == nil {
		if rows, err := db.Query(`SELECT opened_at, number, kind FROM campaign_rounds WHERE lobby_id = $1 ORDER BY opened_at, id`, lobby); err == nil {
			for rows.Next() {
				var at time.Time
				var n int
				var kind string
				if rows.Scan(&at, &n, &kind) == nil {
					list = append(list, rnd{at, roundTitle(kind, n)})
				}
			}
			rows.Close()
		}
	}
	return func(t time.Time) string {
		if len(list) == 0 {
			return ""
		}
		ct := clock.at(t)
		i := sort.Search(len(list), func(i int) bool { return list[i].at.After(ct) })
		if i == 0 {
			return ""
		}
		return list[i-1].name
	}
}

// previewCampaigns: the campaigns this browser may watch (the Live Table's featured order), with what the Campaigns tab shows of each.
func previewCampaigns(r *http.Request, active int) []previewCampaign {
	e := html.EscapeString
	out := []previewCampaign{}
	rows, err := db.Query(`SELECT l.id, l.name, COALESCE(l.status, ''), COALESCE(l.min_level, 1), COALESCE(l.max_level, 1), COALESCE(a.name, ''),
			(SELECT COUNT(*) FROM characters c WHERE c.lobby_id = l.id), COALESCE(l.max_players, 0),
			COALESCE((SELECT cs.active FROM combat_state cs WHERE cs.lobby_id = l.id), false),
			COALESCE((SELECT cs.round_number FROM combat_state cs WHERE cs.lobby_id = l.id), 0),
			(SELECT MAX(x.created_at) FROM actions x WHERE x.lobby_id = l.id AND x.action_type <> ALL($1)),
			(SELECT COUNT(*) FROM actions x WHERE x.lobby_id = l.id AND x.action_type <> ALL($1))
		FROM lobbies l LEFT JOIN agents a ON a.id = l.dm_id
		WHERE COALESCE(l.status, '') IN ('active', 'recruiting')
		ORDER BY CASE WHEN l.status = 'active' THEN 0 ELSE 1 END,
			(SELECT MAX(x.created_at) FROM actions x WHERE x.lobby_id = l.id) DESC NULLS LAST, l.created_at DESC
		LIMIT 50`, pq.Array(storySkipTypes()))
	if err != nil {
		return out
	}
	type row struct {
		c      previewCampaign
		latest *time.Time
	}
	var list []row
	for rows.Next() {
		var c previewCampaign
		var status, gm string
		var minL, maxL, players, maxP int
		var combat bool
		var round int
		var latest *time.Time
		if rows.Scan(&c.ID, &c.Title, &status, &minL, &maxL, &gm, &players, &maxP, &combat, &round, &latest, &c.Pages) != nil {
			continue
		}
		c.Title, c.Status, c.Levels, c.GM = e(strings.ToUpper(c.Title)), strings.ToUpper(status), e(strings.ToUpper(formatLevelRequirement(minL, maxL))), e(gm)
		c.Players = fmt.Sprintf("%d/%d", players, maxP)
		c.Mode = "EXPLORATION"
		if combat {
			c.Mode = "COMBAT"
		}
		if status == "recruiting" {
			c.Mode = "RECRUITING"
		}
		c.Teaser = e(previewClip(previewLatestNarration(c.ID), 110))
		if combat && round > 0 {
			c.Teaser = fmt.Sprintf("Round %d · %s", round, c.Teaser)
		}
		c.HeroImg = backgroundURL(c.ID)
		list = append(list, row{c, latest})
	}
	rows.Close()
	pinned := pinnedID(r, pinCampaignCookie)
	for _, x := range list {
		if campaignIsDeleted(x.c.ID) || !canWatch(r, x.c.ID) {
			continue
		}
		c := x.c
		c.Pinned = c.ID == pinned
		state, _ := scheduleStateOf(db, c.ID)
		switch {
		case state == "paused":
			c.Status, c.Cadence = "PAUSED", "Paused"
			if x.latest != nil {
				c.Cadence += " · " + previewAgo(*x.latest)
			}
		case x.latest != nil:
			c.Cadence = "Async · Last turn " + previewAgo(*x.latest)
		default:
			c.Cadence = "Async · No turns yet"
		}
		out = append(out, c)
	}
	return out
}

// previewAgo: "18m ago", "3h ago", "2d ago".
func previewAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// lookPct: how full a monster's bar is drawn for its look (the bar says the look, not a number).
func lookPct(look string) int {
	switch look {
	case "healthy":
		return 100
	case "wounded":
		return 70
	case "bloodied":
		return 40
	case "critical":
		return 15
	}
	return 0
}

// previewIconAvatar: an image for a token with no picture: the class's (or creature's) icon on the mock-up's dark tile, as the mock-up
// draws every token as an image.
func previewIconAvatar(icon string) string {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="10" fill="#192034"/><text x="32" y="42" font-size="30" text-anchor="middle">` +
		html.EscapeString(icon) + `</text></svg>`
	return "data:image/svg+xml;charset=utf-8," + url.PathEscape(svg)
}

// previewClip cuts a text at a word to at most n bytes, with "…".
func previewClip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndex(s[:n], " ")
	if cut < n/2 {
		cut = n
	}
	return strings.TrimRight(s[:cut], " ,.;:") + "…"
}

// previewLatestNarration: the newest narration of a campaign as players read it (the log's one reader, readActions; "" for none).
func previewLatestNarration(lobby int) string {
	recs, err := readActions(actionFilter{lobby: lobby, newestFirst: true, limit: 40})
	if err != nil {
		return ""
	}
	for _, a := range recs {
		if a.Type == actionNarration {
			return playerText(a.Description)
		}
	}
	return ""
}
