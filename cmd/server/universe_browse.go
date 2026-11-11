package main

// Browsing the long /universe lists: a search box, a dropdown per facet, group headings and a live count, all client side.
// The lists keep the SRD names and text; only how they are shown changes. Monster lists show no Challenge Rating or other
// statistics (players see a monster's description and lore only).

import (
	"fmt"
	"sort"
	"strings"
)

type browseItem struct {
	Href, Name, Meta, Group string
	Facets                  map[string][]string // facet key -> values (a spell has several classes)
}

type browseFacet struct {
	Key, Label string
	Order      []string // display order of the values; others follow alphabetically
}

const browseStyle = `<style>
.browse-bar{display:flex;flex-wrap:wrap;gap:8px;align-items:center;margin:12px 0}
.browse-bar select,.browse-bar input{padding:6px 8px;border-radius:6px;border:1px solid var(--border);background:inherit;color:inherit}
.browse-bar input{flex:1;min-width:180px}
.browse-count{opacity:.7;font-size:.9em}
.browse-group{margin:18px 0 6px;border-bottom:1px solid rgba(128,128,128,.4);padding-bottom:2px}
</style>`

const browseScript = `<script>
function browseApply(){
  var q=(document.getElementById('browse-q').value||'').toLowerCase();
  var sel={};document.querySelectorAll('.browse-bar select').forEach(function(s){if(s.value)sel[s.dataset.key]=s.value});
  var n=0;
  document.querySelectorAll('#browse-list .list-item').forEach(function(el){
    var ok=el.dataset.name.indexOf(q)>=0;
    for(var k in sel){var v=(el.getAttribute('data-f-'+k)||'').split('|');if(v.indexOf(sel[k])<0){ok=false}}
    el.style.display=ok?'block':'none';if(ok)n++;
  });
  document.querySelectorAll('#browse-list .browse-group').forEach(function(h){
    var any=false,el=h.nextElementSibling;
    while(el&&!el.classList.contains('browse-group')){if(el.style.display!=='none')any=true;el=el.nextElementSibling}
    h.style.display=any?'block':'none';
  });
  document.getElementById('browse-count').textContent=n+' shown';
}
</script>`

func orderedValues(values map[string]bool, order []string) []string {
	rank := map[string]int{}
	for i, v := range order {
		rank[v] = i + 1
	}
	var out []string
	for v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		ri, rj := rank[out[i]], rank[out[j]]
		if ri != rj {
			if ri == 0 {
				return false
			}
			if rj == 0 {
				return true
			}
			return ri < rj
		}
		return out[i] < out[j]
	})
	return out
}

func facetAttr(k string) string { return strings.ReplaceAll(strings.ToLower(k), " ", "-") }

// browseList renders a filterable list. Items are grouped by Group in groupOrder (other groups follow alphabetically).
func browseList(icon, title string, items []browseItem, facets []browseFacet, groupOrder []string) string {
	var b strings.Builder
	b.WriteString(browseStyle)
	b.WriteString(fmt.Sprintf(`<h1>%s %s</h1><div class="browse-bar"><input id="browse-q" type="text" placeholder="Search %s..." oninput="browseApply()">`, icon, e(title), e(strings.ToLower(title))))
	for _, f := range facets {
		vals := map[string]bool{}
		for _, it := range items {
			for _, v := range it.Facets[f.Key] {
				vals[v] = true
			}
		}
		b.WriteString(fmt.Sprintf(`<select data-key="%s" onchange="browseApply()"><option value="">%s: all</option>`, facetAttr(f.Key), e(f.Label)))
		for _, v := range orderedValues(vals, f.Order) {
			b.WriteString(fmt.Sprintf(`<option value="%s">%s</option>`, e(v), e(v)))
		}
		b.WriteString(`</select>`)
	}
	b.WriteString(fmt.Sprintf(`<span id="browse-count" class="browse-count">%d shown</span></div><div id="browse-list">`, len(items)))
	groups := map[string]bool{}
	for _, it := range items {
		groups[it.Group] = true
	}
	for _, g := range orderedValues(groups, groupOrder) {
		b.WriteString(`<h3 class="browse-group">` + e(g) + `</h3>`)
		for _, it := range items {
			if it.Group != g {
				continue
			}
			var attrs strings.Builder
			for k, vs := range it.Facets {
				attrs.WriteString(fmt.Sprintf(` data-f-%s="%s"`, facetAttr(k), e(strings.Join(vs, "|"))))
			}
			b.WriteString(fmt.Sprintf(`<div class="list-item" data-name="%s"%s><a href="%s"><strong>%s</strong></a> <span class="muted">%s</span></div>`,
				e(strings.ToLower(it.Name)), attrs.String(), e(it.Href), e(it.Name), e(it.Meta)))
		}
	}
	b.WriteString(`</div>` + browseScript)
	return b.String()
}

// ---- adventuring gear categories (the SRD lists gear alphabetically; these groups only help to find things)

var gearGroups = []struct{ Group string; Words []string }{
	{"Ammunition", []string{"ammunition", "arrow", "bolt", "bullet", "needle"}},
	{"Spellcasting Focuses", []string{"arcane focus", "druidic focus", "holy symbol", "crystal", "orb", "rod", "staff", "wand", "amulet", "emblem", "reliquary", "sprig", "totem", "yew", "component pouch", "spellbook"}},
	{"Packs", []string{" pack"}},
	{"Containers", []string{"backpack", "barrel", "basket", "bottle", "bucket", "case", "chest", "flask", "jug", "pot,", "pouch", "sack", "vial", "quiver", "waterskin"}},
	{"Light", []string{"candle", "lamp", "lantern", "torch", "tinderbox"}},
	{"Clothing", []string{"clothes", "costume", "robe"}},
	{"Climbing and Travel", []string{"rope", "chain", "grappling", "piton", "ladder", "pole", "climber", "bedroll", "blanket", "tent", "hunting trap", "manacles", "spikes"}},
	{"Healing and Remedies", []string{"healer", "antitoxin", "potion", "holy water"}},
	{"Alchemical and Thrown", []string{"acid", "alchemist", "oil", "poison", "ball bearings", "caltrops", "net"}},
	{"Writing and Knowledge", []string{"book", "ink", "map", "paper", "parchment", "scroll", "perfume", "signal whistle"}},
	{"Food and Lodging", []string{"rations"}},
	{"Tools and Odds", []string{"crowbar", "hammer", "shovel", "lock", "mirror", "magnifying", "spyglass", "string", "bell", "chalk", "iron", "hourglass", "scale", "soap", "whetstone", "playing", "dice", "ram,"}},
}

func gearGroup(name string) string {
	l := " " + strings.ToLower(name)
	for _, g := range gearGroups {
		for _, w := range g.Words {
			if strings.Contains(l, w) {
				return g.Group
			}
		}
	}
	return "Other Gear"
}

func priceBand(gp float64) string {
	switch {
	case gp <= 0:
		return "No price listed"
	case gp < 1:
		return "Under 1 GP"
	case gp < 10:
		return "1 to 9 GP"
	case gp < 100:
		return "10 to 99 GP"
	}
	return "100 GP or more"
}

var priceOrder = []string{"Under 1 GP", "1 to 9 GP", "10 to 99 GP", "100 GP or more", "No price listed"}

// spellCastingTimeKind groups a casting time: Action, Bonus Action, Reaction, 1 Minute or longer.
func spellCastingTimeKind(ct string) string {
	l := strings.ToLower(ct)
	switch {
	case strings.HasPrefix(l, "bonus action"):
		return "Bonus Action"
	case strings.HasPrefix(l, "reaction"):
		return "Reaction"
	case strings.HasPrefix(l, "action"):
		return "Action"
	}
	return "1 Minute or longer"
}
