package main

// Themes (Nov 10). Every page draws its colors and fonts from these variables, so a theme changes the whole site, the character sheet
// included (its parchment colors were fixed in the page, so other themes didn't reach it). Dark is the default; Parchment, the look of the character
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

var siteThemes = []siteTheme{
	{"parchment", "Parchment", themeVars(map[string]string{
		"--bg": "#f4ecd8", "--fg": "#1f1b16", "--muted": "#6b645a", "--link": "#58180d", "--link-visited": "#7a2a18",
		"--border": "#c9b994", "--code-bg": "#ece2c8", "--note-bg": "#f1e8d4", "--note-border": "#c9a24a",
		"--surface": "#fbf8f1", "--panel": "#f1e8d4", "--panel-strong": "#e4d9bd", "--panel-border": "#b9a47e", "--rule": "#c9a24a",
		"--heading": "#58180d", "--accent": "#58180d", "--accent-fg": "#ffffff"}, lightStatus, bookStack, bookStack)},
	{"light", "Light", themeVars(map[string]string{
		"--bg": "#ffffff", "--fg": "#222222", "--muted": "#666666", "--link": "#0645ad", "--link-visited": "#0b0080",
		"--border": "#cccccc", "--code-bg": "#f5f5f5", "--note-bg": "#fffbdd", "--note-border": "#e6d9a6",
		"--surface": "#ffffff", "--panel": "#f6f6f6", "--panel-strong": "#e9e9e9", "--panel-border": "#cccccc", "--rule": "#bbbbbb",
		"--heading": "#222222", "--accent": "#0645ad", "--accent-fg": "#ffffff"}, lightStatus, serifStack, bookStack)},
	{"dark", "Dark", themeVars(map[string]string{
		"--bg": "#111111", "--fg": "#dddddd", "--muted": "#888888", "--link": "#6699ff", "--link-visited": "#cc99ff",
		"--border": "#444444", "--code-bg": "#1a1a1a", "--note-bg": "#1a1a1a", "--note-border": "#444444",
		"--surface": "#181818", "--panel": "#202020", "--panel-strong": "#2a2a2a", "--panel-border": "#444444", "--rule": "#666666",
		"--heading": "#eeeeee", "--accent": "#6699ff", "--accent-fg": "#111111"}, darkStatus, serifStack, bookStack)},
	{"tokyonight", "Tokyo Night", themeVars(map[string]string{
		"--bg": "#1a1b26", "--fg": "#c0caf5", "--muted": "#565f89", "--link": "#7aa2f7", "--link-visited": "#bb9af7",
		"--border": "#3b4261", "--code-bg": "#24283b", "--note-bg": "#1f2335", "--note-border": "#3b4261",
		"--surface": "#1f2335", "--panel": "#24283b", "--panel-strong": "#292e42", "--panel-border": "#3b4261", "--rule": "#565f89",
		"--heading": "#bb9af7", "--accent": "#7aa2f7", "--accent-fg": "#1a1b26"}, darkStatus, serifStack, bookStack)},
	{"tokyonight-day", "Tokyo Night Day", themeVars(map[string]string{
		"--bg": "#e1e2e7", "--fg": "#3760bf", "--muted": "#6172b0", "--link": "#2e7de9", "--link-visited": "#9854f1",
		"--border": "#c4c8da", "--code-bg": "#d0d5e3", "--note-bg": "#d0d5e3", "--note-border": "#c4c8da",
		"--surface": "#e9e9ed", "--panel": "#d0d5e3", "--panel-strong": "#c4c8da", "--panel-border": "#a8aecb", "--rule": "#a8aecb",
		"--heading": "#9854f1", "--accent": "#2e7de9", "--accent-fg": "#ffffff"}, lightStatus, serifStack, bookStack)},
	{"catppuccin-latte", "Catppuccin Latte", themeVars(map[string]string{
		"--bg": "#eff1f5", "--fg": "#4c4f69", "--muted": "#8c8fa1", "--link": "#1e66f5", "--link-visited": "#8839ef",
		"--border": "#ccd0da", "--code-bg": "#e6e9ef", "--note-bg": "#e6e9ef", "--note-border": "#ccd0da",
		"--surface": "#eff1f5", "--panel": "#e6e9ef", "--panel-strong": "#dce0e8", "--panel-border": "#bcc0cc", "--rule": "#bcc0cc",
		"--heading": "#8839ef", "--accent": "#1e66f5", "--accent-fg": "#ffffff"}, lightStatus, serifStack, bookStack)},
	{"catppuccin-mocha", "Catppuccin Mocha", themeVars(map[string]string{
		"--bg": "#1e1e2e", "--fg": "#cdd6f4", "--muted": "#6c7086", "--link": "#89b4fa", "--link-visited": "#cba6f7",
		"--border": "#45475a", "--code-bg": "#313244", "--note-bg": "#313244", "--note-border": "#45475a",
		"--surface": "#181825", "--panel": "#313244", "--panel-strong": "#45475a", "--panel-border": "#585b70", "--rule": "#6c7086",
		"--heading": "#cba6f7", "--accent": "#89b4fa", "--accent-fg": "#1e1e2e"}, darkStatus, serifStack, bookStack)},
	{"solarized-light", "Solarized Light", themeVars(map[string]string{
		"--bg": "#fdf6e3", "--fg": "#657b83", "--muted": "#93a1a1", "--link": "#268bd2", "--link-visited": "#6c71c4",
		"--border": "#eee8d5", "--code-bg": "#eee8d5", "--note-bg": "#eee8d5", "--note-border": "#93a1a1",
		"--surface": "#fdf6e3", "--panel": "#eee8d5", "--panel-strong": "#e4ddc8", "--panel-border": "#93a1a1", "--rule": "#93a1a1",
		"--heading": "#cb4b16", "--accent": "#268bd2", "--accent-fg": "#fdf6e3"}, lightStatus, serifStack, bookStack)},
	{"solarized-dark", "Solarized Dark", themeVars(map[string]string{
		"--bg": "#002b36", "--fg": "#839496", "--muted": "#586e75", "--link": "#268bd2", "--link-visited": "#6c71c4",
		"--border": "#073642", "--code-bg": "#073642", "--note-bg": "#073642", "--note-border": "#586e75",
		"--surface": "#073642", "--panel": "#0a4050", "--panel-strong": "#11505f", "--panel-border": "#586e75", "--rule": "#586e75",
		"--heading": "#cb4b16", "--accent": "#268bd2", "--accent-fg": "#002b36"}, darkStatus, serifStack, bookStack)},
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

// themeCSS: Dark on :root (the default, the project owner Nov 10), then each theme by name.
func themeCSS() string {
	var b strings.Builder
	b.WriteString(themeBlock(":root", siteThemes[2].Vars))
	for _, t := range siteThemes {
		b.WriteString(themeBlock(fmt.Sprintf(`[data-theme="%s"]`, t.Key), t.Vars))
	}
	return b.String()
}

// themeMenu: the theme picker, by name.
func themeMenu() string {
	var b strings.Builder
	b.WriteString(`<label class="theme-pick">Theme <select id="theme-select" onchange="setTheme(this.value)" aria-label="Theme">`)
	for _, t := range siteThemes {
		b.WriteString(fmt.Sprintf(`<option value="%s">%s</option>`, t.Key, t.Name))
	}
	b.WriteString(`</select></label>`)
	return b.String()
}

func init() {
	baseHTML = strings.Replace(baseHTML, "/*THEMES*/", themeCSS(), 1)
	baseHTML = strings.Replace(baseHTML, "<!--THEME-MENU-->", themeMenu(), 1)
}
