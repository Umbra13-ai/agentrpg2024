package main

// Themes (Nov 10). Every page draws its colors and fonts from these variables, so a theme changes the whole site, the character sheet
// included (its parchment colors were fixed in the page, so other themes didn't reach it). Obsidian Gold is the default (defaultTheme); Parchment, the look of the character
// sheets, is one of the named themes. The theme menu lists the themes by
// name; the choice is kept in the browser (localStorage).
//
// Variables: page (--bg, --fg, --muted, --link, --link-visited, --border, --code-bg, --note-bg, --note-border), sheet and panels
// (--surface, --panel, --panel-strong, --panel-border, --rule, --heading, --accent, --accent-fg), status (--ok, --warn, --bad, --info and
// their -bg), fonts (--font-body, --font-sheet).

import (
	"fmt"
	"sort"
	"strings"
)

type siteTheme struct {
	Key, Name string
	Vars      map[string]string
	Menu      bool // in the theme menu: the mock-up's five (an older one still applies for a viewer who chose it)
}

var lightStatus = map[string]string{"--ok": "#1e7b34", "--ok-bg": "#d4edda", "--warn": "#8a6100", "--warn-bg": "#fff3cd",
	"--bad": "#a3212c", "--bad-bg": "#f8d7da", "--info": "#0b5394", "--info-bg": "#d1ecf1"}
var darkStatus = map[string]string{"--ok": "#88ee88", "--ok-bg": "#2a4a2a", "--warn": "#f0d060", "--warn-bg": "#4a4a2a",
	"--bad": "#ff8888", "--bad-bg": "#4a2a2a", "--info": "#88ccff", "--info-bg": "#2a3a4a"}

const serifStack = `Georgia, serif`
const bookStack = `"Palatino Linotype", "Book Antiqua", Palatino, "Iowan Old Style", Georgia, serif`

func themeVars(base map[string]string, status map[string]string, body, sheet string) map[string]string {
	out := map[string]string{"--font-body": body, "--font-sheet": sheet}
	for k, v := range status {
		out[k] = v
	}
	for k, v := range base {
		out[k] = v
	}
	return out
}

// The design's themes (Matt, Nov 21: the "Tabletop Command & Spectator" mock-up, data/ui_preview/gui-2026-11-21): Obsidian Gold,
// Crimson Maroon, Forest Emerald, Royal Sapphire and Parchment, with the mock-up's own variables and values, exactly as its CSS
// defines them (a theme block overrides the Obsidian Gold values on :root, as the mock-up's cascade does). designTheme also derives
// the older variable names from them (--bg, --fg, --surface, --accent ...), so every page drawn with those follows the theme.
const interStack = `Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif`
const cinzelStack = `Cinzel, Georgia, serif`
const monoStack = `"JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, monospace`

// obsidianVars: the mock-up's :root (Obsidian Gold).
var obsidianVars = map[string]string{
	"--bg-app": "#08090e", "--bg-radial": "radial-gradient(circle at 50% 0%, rgba(212, 162, 58, 0.11) 0%, rgba(11, 13, 22, 0) 58%)",
	"--bg-surface": "#111420", "--bg-elevated": "#181d2e", "--bg-glass": "rgba(17, 20, 32, 0.86)", "--bg-code": "#0a0c13",
	"--border-subtle": "rgba(214, 173, 86, 0.24)", "--border-strong": "rgba(214, 173, 86, 0.62)",
	"--text-primary": "#f5efe2", "--text-secondary": "#beb7a8", "--text-muted": "#7f7a6f",
	"--accent-gold": "#e5b85c", "--accent-gold-soft": "rgba(229, 184, 92, 0.16)",
	"--accent-crimson": "#e0565b", "--accent-crimson-soft": "rgba(224, 86, 91, 0.18)",
	"--accent-emerald": "#46c284", "--accent-emerald-soft": "rgba(70, 194, 132, 0.18)",
	"--accent-arcane": "#699bf7", "--accent-arcane-soft": "rgba(105, 155, 247, 0.18)",
	"--btn-accent-bg": "linear-gradient(135deg, #f0c975, #b48224)", "--btn-accent-text": "#140f06",
	"--panel-sheen": "rgba(229, 184, 92, 0.06)",
	"--shadow-rpg":  "inset 0 1px 0 rgba(255, 225, 155, 0.12), 0 14px 36px rgba(0, 0, 0, 0.55)",
	"--radius-lg":   "14px", "--radius-md": "10px", "--radius-sm": "6px",
}

// tabletopDark: the mock-up's maroon, emerald and royal blocks (they share all but their accent).
func tabletopDark(radial, borderStrong, accent, accentSoft, btnBG, btnFG, sheen string) map[string]string {
	return map[string]string{"--bg-app": "#0b0d13", "--bg-radial": radial, "--bg-surface": "#121520", "--bg-elevated": "#191e2d",
		"--bg-glass": "rgba(18, 21, 32, 0.9)", "--bg-code": "#0a0c12", "--border-subtle": "rgba(255, 255, 255, 0.11)", "--border-strong": borderStrong,
		"--text-primary": "#f3efe8", "--text-secondary": "#b6b1a8", "--text-muted": "#7c7870", "--accent-gold": accent, "--accent-gold-soft": accentSoft,
		"--btn-accent-bg": btnBG, "--btn-accent-text": btnFG, "--panel-sheen": sheen,
		"--shadow-rpg": "inset 0 1px 0 rgba(255, 255, 255, 0.06), 0 14px 36px rgba(0, 0, 0, 0.55)"}
}

// parchmentVars: the mock-up's Parchment block.
var parchmentVars = map[string]string{
	"--bg-app": "#efe4cc", "--bg-radial": "radial-gradient(circle at 50% 0%, rgba(168, 112, 34, 0.14) 0%, rgba(239, 228, 204, 0) 60%)",
	"--bg-surface": "#fbf5e6", "--bg-elevated": "#f1e5ca", "--bg-glass": "rgba(251, 245, 230, 0.92)", "--bg-code": "#e6d7b9",
	"--border-subtle": "rgba(132, 90, 38, 0.28)", "--border-strong": "rgba(132, 90, 38, 0.65)",
	"--text-primary": "#261b12", "--text-secondary": "#584431", "--text-muted": "#826d58",
	"--accent-gold": "#9a6412", "--accent-gold-soft": "rgba(154, 100, 18, 0.14)",
	"--accent-crimson": "#a82828", "--accent-crimson-soft": "rgba(168, 40, 40, 0.14)",
	"--accent-emerald": "#206e47", "--accent-emerald-soft": "rgba(32, 110, 71, 0.14)",
	"--accent-arcane": "#26569e", "--accent-arcane-soft": "rgba(38, 86, 158, 0.14)",
	"--btn-accent-bg": "linear-gradient(135deg, #b87e24, #8a590f)", "--btn-accent-text": "#fffaf0",
	"--panel-sheen": "rgba(154, 100, 18, 0.05)",
	"--shadow-rpg":  "inset 0 1px 0 rgba(255, 255, 255, 0.6), 0 10px 26px rgba(66, 44, 18, 0.14)",
}

// designTheme: a mock-up theme block over the Obsidian Gold values, with the older names derived from the result.
func designTheme(over map[string]string) map[string]string {
	v := map[string]string{}
	for k, x := range obsidianVars {
		v[k] = x
	}
	for k, x := range over {
		v[k] = x
	}
	for k, from := range map[string]string{"--bg": "--bg-app", "--fg": "--text-primary", "--muted": "--text-secondary", "--link": "--accent-gold",
		"--link-visited": "--accent-gold", "--border": "--border-subtle", "--code-bg": "--bg-code", "--note-bg": "--bg-elevated",
		"--note-border": "--border-strong", "--surface": "--bg-surface", "--panel": "--bg-elevated", "--panel-strong": "--bg-code",
		"--panel-border": "--border-subtle", "--rule": "--border-strong", "--heading": "--accent-gold", "--accent": "--accent-gold",
		"--accent-fg": "--btn-accent-text", "--ok": "--accent-emerald", "--ok-bg": "--accent-emerald-soft", "--bad": "--accent-crimson",
		"--bad-bg": "--accent-crimson-soft", "--info": "--accent-arcane", "--info-bg": "--accent-arcane-soft", "--warn": "--accent-gold",
		"--warn-bg": "--accent-gold-soft"} {
		v[k] = v[from]
	}
	v["--font-body"], v["--font-sheet"], v["--font-heading"], v["--font-mono"] = interStack, interStack, cinzelStack, monoStack
	return v
}

// legacyTheme: an older theme (kept for a viewer who chose it before the rework; not in the menu) given the design's variable names
// from its own colors.
func legacyTheme(v map[string]string) map[string]string {
	for k, from := range map[string]string{"--bg-app": "--bg", "--bg-surface": "--surface", "--bg-elevated": "--panel", "--bg-code": "--code-bg",
		"--bg-glass": "--bg", "--border-subtle": "--border", "--border-strong": "--rule", "--text-primary": "--fg", "--text-secondary": "--muted",
		"--text-muted": "--muted", "--accent-gold": "--accent", "--accent-crimson": "--bad", "--accent-crimson-soft": "--bad-bg",
		"--accent-emerald": "--ok", "--accent-emerald-soft": "--ok-bg", "--accent-arcane": "--info", "--accent-arcane-soft": "--info-bg",
		"--btn-accent-text": "--accent-fg"} {
		v[k] = v[from]
	}
	v["--accent-gold-soft"] = "color-mix(in srgb, " + v["--accent"] + " 16%, transparent)"
	v["--btn-accent-bg"] = v["--accent"]
	v["--bg-radial"], v["--panel-sheen"], v["--shadow-rpg"] = "none", "transparent", "0 1px 4px rgba(0, 0, 0, 0.2)"
	v["--font-heading"], v["--font-mono"] = cinzelStack, monoStack
	return v
}

var siteThemes = []siteTheme{
	{"obsidian", "Obsidian Gold", designTheme(nil), true},
	{"maroon", "Crimson Maroon", designTheme(tabletopDark("radial-gradient(circle at 50% 0%, rgba(168, 44, 68, 0.14) 0%, rgba(11, 13, 19, 0) 55%)",
		"rgba(196, 62, 92, 0.55)", "#d95b76", "rgba(217, 91, 118, 0.15)", "linear-gradient(135deg, #9e2a45, #731a30)", "#fff5f7", "rgba(217, 91, 118, 0.04)")), true},
	{"emerald", "Forest Emerald", designTheme(tabletopDark("radial-gradient(circle at 50% 0%, rgba(34, 128, 78, 0.14) 0%, rgba(11, 13, 19, 0) 55%)",
		"rgba(56, 168, 108, 0.55)", "#46b87c", "rgba(70, 184, 124, 0.15)", "linear-gradient(135deg, #24784c, #175233)", "#f2fff7", "rgba(70, 184, 124, 0.04)")), true},
	{"royal", "Royal Sapphire", designTheme(tabletopDark("radial-gradient(circle at 50% 0%, rgba(44, 92, 196, 0.15) 0%, rgba(11, 13, 19, 0) 55%)",
		"rgba(76, 128, 235, 0.55)", "#5d92f5", "rgba(93, 146, 245, 0.15)", "linear-gradient(135deg, #2a56b8, #1b3a82)", "#f3f7ff", "rgba(93, 146, 245, 0.04)")), true},
	{"parchment", "Parchment", designTheme(parchmentVars), true},
	{"light", "Light", legacyTheme(themeVars(map[string]string{
		"--bg": "#ffffff", "--fg": "#222222", "--muted": "#666666", "--link": "#0645ad", "--link-visited": "#0b0080",
		"--border": "#cccccc", "--code-bg": "#f5f5f5", "--note-bg": "#fffbdd", "--note-border": "#e6d9a6",
		"--surface": "#ffffff", "--panel": "#f6f6f6", "--panel-strong": "#e9e9e9", "--panel-border": "#cccccc", "--rule": "#bbbbbb",
		"--heading": "#222222", "--accent": "#0645ad", "--accent-fg": "#ffffff"}, lightStatus, serifStack, bookStack)), false},
	{"dark", "Dark", legacyTheme(themeVars(map[string]string{
		"--bg": "#111111", "--fg": "#dddddd", "--muted": "#888888", "--link": "#6699ff", "--link-visited": "#cc99ff",
		"--border": "#444444", "--code-bg": "#1a1a1a", "--note-bg": "#1a1a1a", "--note-border": "#444444",
		"--surface": "#181818", "--panel": "#202020", "--panel-strong": "#2a2a2a", "--panel-border": "#444444", "--rule": "#666666",
		"--heading": "#eeeeee", "--accent": "#6699ff", "--accent-fg": "#111111"}, darkStatus, serifStack, bookStack)), false},
	{"tokyonight", "Tokyo Night", legacyTheme(themeVars(map[string]string{
		"--bg": "#1a1b26", "--fg": "#c0caf5", "--muted": "#565f89", "--link": "#7aa2f7", "--link-visited": "#bb9af7",
		"--border": "#3b4261", "--code-bg": "#24283b", "--note-bg": "#1f2335", "--note-border": "#3b4261",
		"--surface": "#1f2335", "--panel": "#24283b", "--panel-strong": "#292e42", "--panel-border": "#3b4261", "--rule": "#565f89",
		"--heading": "#bb9af7", "--accent": "#7aa2f7", "--accent-fg": "#1a1b26"}, darkStatus, serifStack, bookStack)), false},
	{"tokyonight-day", "Tokyo Night Day", legacyTheme(themeVars(map[string]string{
		"--bg": "#e1e2e7", "--fg": "#3760bf", "--muted": "#6172b0", "--link": "#2e7de9", "--link-visited": "#9854f1",
		"--border": "#c4c8da", "--code-bg": "#d0d5e3", "--note-bg": "#d0d5e3", "--note-border": "#c4c8da",
		"--surface": "#e9e9ed", "--panel": "#d0d5e3", "--panel-strong": "#c4c8da", "--panel-border": "#a8aecb", "--rule": "#a8aecb",
		"--heading": "#9854f1", "--accent": "#2e7de9", "--accent-fg": "#ffffff"}, lightStatus, serifStack, bookStack)), false},
	{"catppuccin-latte", "Catppuccin Latte", legacyTheme(themeVars(map[string]string{
		"--bg": "#eff1f5", "--fg": "#4c4f69", "--muted": "#8c8fa1", "--link": "#1e66f5", "--link-visited": "#8839ef",
		"--border": "#ccd0da", "--code-bg": "#e6e9ef", "--note-bg": "#e6e9ef", "--note-border": "#ccd0da",
		"--surface": "#eff1f5", "--panel": "#e6e9ef", "--panel-strong": "#dce0e8", "--panel-border": "#bcc0cc", "--rule": "#bcc0cc",
		"--heading": "#8839ef", "--accent": "#1e66f5", "--accent-fg": "#ffffff"}, lightStatus, serifStack, bookStack)), false},
	{"catppuccin-mocha", "Catppuccin Mocha", legacyTheme(themeVars(map[string]string{
		"--bg": "#1e1e2e", "--fg": "#cdd6f4", "--muted": "#6c7086", "--link": "#89b4fa", "--link-visited": "#cba6f7",
		"--border": "#45475a", "--code-bg": "#313244", "--note-bg": "#313244", "--note-border": "#45475a",
		"--surface": "#181825", "--panel": "#313244", "--panel-strong": "#45475a", "--panel-border": "#585b70", "--rule": "#6c7086",
		"--heading": "#cba6f7", "--accent": "#89b4fa", "--accent-fg": "#1e1e2e"}, darkStatus, serifStack, bookStack)), false},
	{"solarized-light", "Solarized Light", legacyTheme(themeVars(map[string]string{
		"--bg": "#fdf6e3", "--fg": "#657b83", "--muted": "#93a1a1", "--link": "#268bd2", "--link-visited": "#6c71c4",
		"--border": "#eee8d5", "--code-bg": "#eee8d5", "--note-bg": "#eee8d5", "--note-border": "#93a1a1",
		"--surface": "#fdf6e3", "--panel": "#eee8d5", "--panel-strong": "#e4ddc8", "--panel-border": "#93a1a1", "--rule": "#93a1a1",
		"--heading": "#cb4b16", "--accent": "#268bd2", "--accent-fg": "#fdf6e3"}, lightStatus, serifStack, bookStack)), false},
	{"solarized-dark", "Solarized Dark", legacyTheme(themeVars(map[string]string{
		"--bg": "#002b36", "--fg": "#839496", "--muted": "#586e75", "--link": "#268bd2", "--link-visited": "#6c71c4",
		"--border": "#073642", "--code-bg": "#073642", "--note-bg": "#073642", "--note-border": "#586e75",
		"--surface": "#073642", "--panel": "#0a4050", "--panel-strong": "#11505f", "--panel-border": "#586e75", "--rule": "#586e75",
		"--heading": "#cb4b16", "--accent": "#268bd2", "--accent-fg": "#002b36"}, darkStatus, serifStack, bookStack)), false},
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

// defaultTheme: the theme of a viewer who chose none (Obsidian Gold since the GUI rework, Matt Nov 21; Dark before).
const defaultTheme = "obsidian"

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

// themeMenu: the mock-up's theme select, its five themes by name (the layout's script applies and keeps the choice).
func themeMenu() string {
	var b strings.Builder
	b.WriteString(`<select id="theme-select" class="theme-select" aria-label="Theme selector">`)
	for _, t := range siteThemes {
		if t.Menu {
			b.WriteString(fmt.Sprintf(`<option value="%s">%s</option>`, t.Key, t.Name))
		}
	}
	b.WriteString(`</select>`)
	return b.String()
}
