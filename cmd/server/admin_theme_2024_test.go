package main

import (
	"strings"
	"testing"
)

// The admin page follows the theme chosen on the site (owner, Nov 23).
func TestAdminPageFollowsTheSiteTheme(t *testing.T) {
	css := adminThemeCSS()
	for _, th := range siteThemes {
		if !strings.Contains(css, `html[data-theme="`+th.Key+`"]`) {
			t.Errorf("no block for the theme %s", th.Key)
		}
	}
	if !strings.Contains(css, "html[data-theme]{--bg:var(--bg-app") || strings.Contains(css, ":root {") {
		t.Errorf("the page's colors aren't named from the theme, or the theme's :root block leaked in")
	}
	if !strings.Contains(adminPageHTML, "/*ADMIN_THEMES*/") || !strings.Contains(adminPageHTML, `localStorage.getItem("theme")`) {
		t.Error("the page has no place for the themes or doesn't read the saved choice")
	}
}
