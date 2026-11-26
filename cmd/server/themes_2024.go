package main

// Themes (owner, Nov 26: the Nov 25 mock-up is the site's design). The mock-up's three themes, Midnight (the default), Parchment and
// Contrast, are the site's only themes, with the mock-up's own token values (data/preview_table_2024.html: --bg-outer, --bg-base,
// --bg-surface, --bg-elevated, --text-main, --text-muted, --border-subtle, --gold, --gold-bright). Every page draws its colors from
// these variables: the app shell (the mock-up itself, live_table_2024.go) reads the tokens by their names, and the page shell's pages
// (site_layout_2024.go) read the older content names (--accent-gold, --text-primary, --bg-code ...), which each theme defines from its
// tokens here, once, so a page re-skins without touching its handler.
//
// The choice is kept in the browser under one key, themeStoreKey: the app shell's Settings tab writes it, and every page (the app
// shell, the page shell, the admin page) reads it through themeReadJS before it paints. A saved key that is no longer a theme reads as
// the nearest one (in themeReadJS, the one reading).

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type siteTheme struct {
	Key, Name string
	Vars      map[string]string
}

const interStack = `Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif`
const cinzelStack = `Cinzel, Georgia, serif`
const monoStack = `"JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, monospace`

// themePalette: one theme's mock-up tokens and the colors the content pages need besides them.
type themePalette struct {
	Outer, Base, Surface, Elevated, Main, Muted, Border, Gold, GoldBright string // the mock-up's tokens
	Code, Glass, BorderStrong, Secondary, Crimson, Emerald, Arcane        string // for the content pages
	Soft                                                                  string // the alpha of a soft accent ("0.15")
	ButtonBG, ButtonText, Shadow                                          string
	Dark                                                                  bool
}

// themeVars: a palette as the variables every page reads.
func themeVars(p themePalette) map[string]string {
	soft := func(hex string) string { return softColor(hex, p.Soft) }
	v := map[string]string{
		// the mock-up's tokens
		"--bg-outer": p.Outer, "--bg-base": p.Base, "--bg-surface": p.Surface, "--bg-elevated": p.Elevated, "--text-main": p.Main,
		"--text-muted": p.Muted, "--border-subtle": p.Border, "--gold": p.Gold, "--gold-bright": p.GoldBright,
		// the content pages' names, from the tokens
		"--bg-app": p.Base, "--bg-code": p.Code, "--bg-glass": p.Glass, "--bg-radial": "none", "--border-strong": p.BorderStrong,
		"--text-primary": p.Main, "--text-secondary": p.Secondary, "--accent-gold": p.Gold, "--accent-gold-soft": soft(p.Gold),
		"--accent-crimson": p.Crimson, "--accent-crimson-soft": soft(p.Crimson), "--accent-emerald": p.Emerald, "--accent-emerald-soft": soft(p.Emerald),
		"--accent-arcane": p.Arcane, "--accent-arcane-soft": soft(p.Arcane), "--btn-accent-bg": p.ButtonBG, "--btn-accent-text": p.ButtonText,
		"--panel-sheen": "transparent", "--shadow-rpg": p.Shadow, "--radius-lg": "14px", "--radius-md": "10px", "--radius-sm": "6px",
		"--font-body": interStack, "--font-sheet": interStack, "--font-heading": cinzelStack, "--font-mono": monoStack,
	}
	// the oldest names (pages written before the Nov 21 rework)
	for k, from := range map[string]string{"--bg": "--bg-app", "--fg": "--text-primary", "--muted": "--text-secondary", "--link": "--accent-gold",
		"--link-visited": "--accent-gold", "--border": "--border-subtle", "--code-bg": "--bg-code", "--note-bg": "--bg-elevated",
		"--note-border": "--border-strong", "--surface": "--bg-surface", "--panel": "--bg-elevated", "--panel-strong": "--bg-code",
		"--panel-border": "--border-subtle", "--rule": "--border-strong", "--heading": "--accent-gold", "--accent": "--accent-gold",
		"--accent-fg": "--btn-accent-text", "--ok": "--accent-emerald", "--ok-bg": "--accent-emerald-soft", "--bad": "--accent-crimson",
		"--bad-bg": "--accent-crimson-soft", "--info": "--accent-arcane", "--info-bg": "--accent-arcane-soft", "--warn": "--accent-gold",
		"--warn-bg": "--accent-gold-soft"} {
		v[k] = v[from]
	}
	v["color-scheme"] = "light"
	if p.Dark {
		v["color-scheme"] = "dark"
	}
	return v
}

// softColor: a #rrggbb color at the given alpha.
func softColor(hex, alpha string) string {
	var r, g, b int
	if len(hex) == 7 {
		fmt.Sscanf(hex[1:], "%02x%02x%02x", &r, &g, &b)
	}
	return fmt.Sprintf("rgba(%d, %d, %d, %s)", r, g, b, alpha)
}

var siteThemes = []siteTheme{
	{"midnight", "Midnight", themeVars(themePalette{
		Outer: "#07090f", Base: "#0b0e16", Surface: "#121726", Elevated: "#192034", Main: "#e6e9f2", Muted: "#94a3b8",
		Border: "rgba(255, 255, 255, 0.08)", Gold: "#d4a853", GoldBright: "#eab95c",
		Code: "#080b12", Glass: "rgba(11, 14, 22, 0.88)", BorderStrong: "rgba(212, 168, 83, 0.5)", Secondary: "#cbd5e1",
		Crimson: "#f87171", Emerald: "#34d399", Arcane: "#60a5fa", Soft: "0.15",
		ButtonBG: "#d4a853", ButtonText: "#0b0e16", Shadow: "0 10px 30px rgba(0, 0, 0, 0.45)", Dark: true})},
	{"parchment", "Parchment", themeVars(themePalette{
		Outer: "#e7dfd1", Base: "#f4efe6", Surface: "#fcfaf5", Elevated: "#ede5d8", Main: "#1f2430", Muted: "#5c6475",
		Border: "rgba(31, 36, 48, 0.12)", Gold: "#9a6b16", GoldBright: "#82570b",
		Code: "#ebe3d4", Glass: "rgba(244, 239, 230, 0.9)", BorderStrong: "rgba(154, 107, 22, 0.5)", Secondary: "#3d4452",
		Crimson: "#b91c1c", Emerald: "#047857", Arcane: "#1d4ed8", Soft: "0.12",
		ButtonBG: "#9a6b16", ButtonText: "#fcfaf5", Shadow: "0 6px 18px rgba(31, 36, 48, 0.12)"})},
	{"contrast", "Contrast", themeVars(themePalette{
		Outer: "#000000", Base: "#000000", Surface: "#0a0a0f", Elevated: "#14141f", Main: "#ffffff", Muted: "#cbd5e1",
		Border: "rgba(255, 255, 255, 0.24)", Gold: "#fbbf24", GoldBright: "#fcd34d",
		Code: "#000000", Glass: "rgba(0, 0, 0, 0.92)", BorderStrong: "rgba(251, 191, 36, 0.8)", Secondary: "#e2e8f0",
		Crimson: "#fca5a5", Emerald: "#6ee7b7", Arcane: "#93c5fd", Soft: "0.2",
		ButtonBG: "#fbbf24", ButtonText: "#000000", Shadow: "none", Dark: true})},
}

// defaultTheme: the theme of a viewer who chose none (the mock-up's Midnight).
const defaultTheme = "midnight"

// themeStoreKey: where the browser keeps the viewer's theme (the key's value is the theme's Key).
const themeStoreKey = "arpg_theme"

// themeReadJS: the one reading of the viewer's theme in the browser, as a JavaScript function arpgTheme() that returns a theme's Key:
// the saved choice, else the preview's and the older site's keys, an older theme read as the nearest one (Parchment for a light one); never throws.
func themeReadJS() string {
	keys := []string{}
	for _, t := range siteThemes {
		keys = append(keys, t.Key)
	}
	known, _ := json.Marshal(keys)
	return `function arpgTheme(){var k=` + string(known) + `,v="";try{v=localStorage.getItem("` + themeStoreKey + `")||localStorage.getItem("arpg_preview_theme")||localStorage.getItem("theme")||"";}catch(e){}` +
		`v=String(v).replace(/"/g,"").toLowerCase();if(k.indexOf(v)>=0)return v;` +
		`if(v==="light"||v.indexOf("latte")>=0||/-day$/.test(v)||v==="solarized-light")return "parchment";return "` + defaultTheme + `";}`
}

// themeHeadScript: the page's theme set on <html data-theme> before it paints.
func themeHeadScript() string {
	return `<script>` + themeReadJS() + `document.documentElement.setAttribute("data-theme",arpgTheme());</script>`
}

func themeBlock(selector string, vars map[string]string) string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(selector + " {")
	for _, k := range keys {
		b.WriteString(" " + k + ": " + vars[k] + ";")
	}
	b.WriteString(" }\n")
	return b.String()
}

// themeCSS: the default theme on :root, then each theme by name.
func themeCSS() string {
	var b strings.Builder
	for _, t := range siteThemes {
		if t.Key == defaultTheme {
			b.WriteString(themeBlock(":root", t.Vars))
		}
	}
	for _, t := range siteThemes {
		b.WriteString(themeBlock(fmt.Sprintf(`[data-theme="%s"]`, t.Key), t.Vars))
	}
	return b.String()
}
