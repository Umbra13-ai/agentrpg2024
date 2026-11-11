package main

// /universe/feats (the SRD 5.2.1 feats, by category: Origin, General, Fighting Style, Epic Boon) and /universe/rules (the SRD 5.2.1
// Rules Glossary, by kind: Action, Condition, Hazard, Attitude, Area of Effect ...), with the filterable list and a page per entry.
// The text is the SRD's, word for word.

import (
	"fmt"
	"sort"
	"strings"
)

func allFeatsSRD() map[string]Feat {
	out := map[string]Feat{}
	for k, f := range availableFeats {
		out[k] = f
	}
	for k, f := range originFeatsSRD() {
		out[k] = f
	}
	return out
}

func featsPage(rest string) string {
	feats := allFeatsSRD()
	if rest != "" {
		f, ok := feats[strings.ReplaceAll(rest, "-", "_")]
		if !ok {
			return "<h1>Feat not found</h1><p><a href=\"/universe/feats\">All feats</a></p>"
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf(`<p><a href="/universe/feats">← Feats</a></p><h1>%s</h1><p class="muted"><em>%s Feat`, e(f.Name), e(f.Category)))
		if p := featPrereqText(f.Prerequisite); p != "" {
			b.WriteString(" (Prerequisite: " + e(p) + ")")
		}
		if f.Repeatable {
			b.WriteString("; repeatable")
		}
		b.WriteString("</em></p>")
		if f.Description != "" {
			b.WriteString("<p>" + e(f.Description) + "</p>")
		}
		for _, x := range f.Benefits {
			if i := strings.Index(x, ". "); i > 0 && i < 50 {
				b.WriteString(fmt.Sprintf("<p><strong><em>%s.</em></strong> %s</p>", e(x[:i]), e(x[i+2:])))
			} else {
				b.WriteString("<p>" + e(x) + "</p>")
			}
		}
		b.WriteString(`<p class="muted">Take a feat with POST /api/characters/{id}/feat {"feat": "` + e(strings.ReplaceAll(rest, "-", "_")) + `"}.</p>`)
		return b.String()
	}
	var items []browseItem
	for k, f := range feats {
		meta := featPrereqText(f.Prerequisite)
		items = append(items, browseItem{Href: "/universe/feats/" + strings.ReplaceAll(k, "_", "-"), Name: f.Name, Meta: meta, Group: f.Category,
			Facets: map[string][]string{"category": {f.Category}}})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	order := []string{"Origin", "General", "Fighting Style", "Epic Boon"}
	return browseList("🎖️", "Feats", items, []browseFacet{{Key: "category", Label: "Category", Order: order}}, order)
}

func glossarySlug(name string) string {
	s := strings.ToLower(name)
	s = strings.NewReplacer("’", "", "'", "", "[", "", "]", "", "(", "", ")", "").Replace(s)
	return strings.Join(strings.Fields(s), "-")
}

func rulesPage(rest string) string {
	entries := srdGlossary()
	if rest != "" {
		for _, en := range entries {
			if glossarySlug(en.Name) == rest {
				var b strings.Builder
				kind := en.Kind
				if kind == "" {
					kind = "Rule"
				}
				b.WriteString(fmt.Sprintf(`<p><a href="/universe/rules">← Rules Glossary</a></p><h1>%s</h1><p class="muted"><em>%s</em></p>`, e(en.Name), e(kind)))
				for _, p := range en.Text {
					b.WriteString("<p>" + e(p) + "</p>")
				}
				return b.String()
			}
		}
		return "<h1>Rule not found</h1><p><a href=\"/universe/rules\">Rules Glossary</a></p>"
	}
	var items []browseItem
	for _, en := range entries {
		kind := en.Kind
		if kind == "" {
			kind = "Rule"
		}
		items = append(items, browseItem{Href: "/universe/rules/" + glossarySlug(en.Name), Name: en.Name, Group: kind, Facets: map[string][]string{"kind": {kind}}})
	}
	order := []string{"Rule", "Action", "Condition", "Hazard", "Attitude", "Area of Effect"}
	return browseList("📖", "Rules Glossary", items, []browseFacet{{Key: "kind", Label: "Kind", Order: order}}, order)
}
