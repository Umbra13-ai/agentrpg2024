package game

// Tools from the 2024 Player's Handbook (chapter 6, "Tools", p. 220-221).
// The SRD 5.2.1 describes tools only in prose, so weights, costs and the
// governing ability come from the Player's Handbook table of tools.

import "strings"

// Tool is one tool or tool variant.
type Tool struct {
	Name     string  // lowercase, e.g. "smith's tools"
	Category string  // "artisan", "instrument", "gaming", "other"
	Ability  string  // governing ability
	WeightLb float64 // -1 if none listed
	CostGP   float64
}

const (
	ToolArtisan    = "artisan"
	ToolInstrument = "instrument"
	ToolGaming     = "gaming"
	ToolOther      = "other"
)

// Tools lists every tool in the 2024 Player's Handbook, with the variants of
// Gaming Set and Musical Instrument listed separately.
var Tools = []Tool{
	// Artisan's Tools (each needs a separate proficiency)
	{"alchemist's supplies", ToolArtisan, "intelligence", 8, 50},
	{"brewer's supplies", ToolArtisan, "intelligence", 9, 20},
	{"calligrapher's supplies", ToolArtisan, "dexterity", 5, 10},
	{"carpenter's tools", ToolArtisan, "strength", 6, 8},
	{"cartographer's tools", ToolArtisan, "wisdom", 6, 15},
	{"cobbler's tools", ToolArtisan, "dexterity", 5, 5},
	{"cook's utensils", ToolArtisan, "wisdom", 8, 1},
	{"glassblower's tools", ToolArtisan, "intelligence", 5, 30},
	{"jeweler's tools", ToolArtisan, "intelligence", 2, 25},
	{"leatherworker's tools", ToolArtisan, "dexterity", 5, 5},
	{"mason's tools", ToolArtisan, "strength", 8, 10},
	{"painter's supplies", ToolArtisan, "wisdom", 5, 10},
	{"potter's tools", ToolArtisan, "intelligence", 3, 10},
	{"smith's tools", ToolArtisan, "strength", 8, 20},
	{"tinker's tools", ToolArtisan, "dexterity", 10, 50},
	{"weaver's tools", ToolArtisan, "dexterity", 5, 1},
	{"woodcarver's tools", ToolArtisan, "dexterity", 5, 1},

	// Musical Instrument variants (Charisma)
	{"bagpipes", ToolInstrument, "charisma", 6, 30},
	{"drum", ToolInstrument, "charisma", 3, 6},
	{"dulcimer", ToolInstrument, "charisma", 10, 25},
	{"flute", ToolInstrument, "charisma", 1, 2},
	{"horn", ToolInstrument, "charisma", 2, 3},
	{"lute", ToolInstrument, "charisma", 2, 35},
	{"lyre", ToolInstrument, "charisma", 2, 30},
	{"pan flute", ToolInstrument, "charisma", 2, 12},
	{"shawm", ToolInstrument, "charisma", 1, 2},
	{"viol", ToolInstrument, "charisma", 1, 30},

	// Gaming Set variants (Wisdom, no weight listed)
	{"dice", ToolGaming, "wisdom", -1, 0.1},
	{"dragonchess", ToolGaming, "wisdom", -1, 1},
	{"playing cards", ToolGaming, "wisdom", -1, 0.5},
	{"three-dragon ante", ToolGaming, "wisdom", -1, 1},

	// Other tools
	{"disguise kit", ToolOther, "charisma", 3, 25},
	{"forgery kit", ToolOther, "dexterity", 5, 15},
	{"herbalism kit", ToolOther, "intelligence", 3, 5},
	{"navigator's tools", ToolOther, "wisdom", 2, 25},
	{"poisoner's kit", ToolOther, "intelligence", 2, 50},
	{"thieves' tools", ToolOther, "dexterity", 1, 25},
}

// NormalizeToolName lowercases and trims a tool name and tidies apostrophes.
func NormalizeToolName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.NewReplacer("’", "'", "_", " ").Replace(n)
	return strings.Join(strings.Fields(n), " ")
}

// FindTool looks a tool up by name. It also accepts a Gaming Set or Musical
// Instrument variant written with its kind ("Gaming Set (Dice)", "musical
// instrument: lute") or the older way ("dice set").
func FindTool(name string) (Tool, bool) {
	n := NormalizeToolName(name)
	for _, kind := range []string{"gaming set", "musical instrument"} {
		switch {
		case strings.HasPrefix(n, kind+" (") && strings.HasSuffix(n, ")"):
			n = strings.TrimSpace(n[len(kind)+2 : len(n)-1])
		case strings.HasPrefix(n, kind+": "):
			n = strings.TrimSpace(n[len(kind)+2:])
		}
	}
	n = strings.TrimSuffix(n, " set")
	for _, t := range Tools {
		if t.Name == n || t.Name == strings.TrimSuffix(n, "s") || t.Name == n+"s" {
			return t, true
		}
	}
	return Tool{}, false
}

// FullName is the name a tool proficiency is stored under. A Gaming Set or
// Musical Instrument variant carries its kind, the way the SRD 5.2.1 names them
// ("gaming set (dice)", "musical instrument (lute)"); other tools keep their name.
func (t Tool) FullName() string {
	switch t.Category {
	case ToolGaming:
		return "gaming set (" + t.Name + ")"
	case ToolInstrument:
		return "musical instrument (" + t.Name + ")"
	}
	return t.Name
}

// toolKinds are the names of whole kinds of tools, as a GM may ask for them.
var toolKinds = map[string]string{
	"gaming set": ToolGaming, "gaming sets": ToolGaming,
	"musical instrument": ToolInstrument, "musical instruments": ToolInstrument, "instrument": ToolInstrument,
	"artisan's tools": ToolArtisan, "artisans' tools": ToolArtisan, "artisan tools": ToolArtisan,
}

// ToolKind returns the category when a name is a whole kind of tool ("gaming set",
// "musical instrument", "artisan's tools") rather than one tool.
func ToolKind(name string) (string, bool) {
	c, ok := toolKinds[NormalizeToolName(name)]
	return c, ok
}

// CanonicalToolName is how a tool proficiency is stored: the tool's FullName when it
// is a known tool, otherwise the name lowercased and tidied.
func CanonicalToolName(name string) string {
	if t, ok := FindTool(name); ok {
		return t.FullName()
	}
	return NormalizeToolName(name)
}

// toolKey is what two names of the same tool have in common: "dice", "Dice Set"
// and "Gaming Set (Dice)" all give "dice".
func toolKey(name string) string {
	if t, ok := FindTool(name); ok {
		return t.Name
	}
	return NormalizeToolName(name)
}

// SameTool reports whether two names mean the same tool.
func SameTool(a, b string) bool {
	return toolKey(a) == toolKey(b)
}

// HasToolProficiency reports whether a list of tool proficiencies covers a tool.
// Asking for a whole kind ("gaming set", "musical instrument", "artisan's tools")
// is covered by any tool of that kind.
func HasToolProficiency(owned []string, name string) bool {
	kind, isKind := ToolKind(name)
	for _, o := range owned {
		if isKind {
			if t, ok := FindTool(o); ok && t.Category == kind {
				return true
			}
			continue
		}
		if SameTool(o, name) {
			return true
		}
	}
	return false
}

// MergeToolList adds tools to a stored list of tool proficiencies (comma-separated).
// Every entry is stored once, under its canonical name, so older short names
// ("dice", "lute") become the full ones.
func MergeToolList(existing string, add []string) string {
	seen := map[string]bool{}
	var out []string
	for _, p := range append(ParseProficiencyList(existing), add...) {
		c := CanonicalToolName(p)
		if c == "" || seen[toolKey(c)] {
			continue
		}
		seen[toolKey(c)] = true
		out = append(out, c)
	}
	return strings.Join(out, ", ")
}

// ToolDisplayName writes a tool with capitals: "Gaming Set (Three-Dragon Ante)",
// "Thieves' Tools".
func ToolDisplayName(name string) string {
	b := []byte(CanonicalToolName(name))
	up := true
	for i, c := range b {
		if up && c >= 'a' && c <= 'z' {
			b[i] = c - 'a' + 'A'
		}
		up = c == ' ' || c == '(' || c == '-'
	}
	return string(b)
}

// ToolsInCategory returns the names of every tool in a category.
func ToolsInCategory(category string) []string {
	var out []string
	for _, t := range Tools {
		if t.Category == category {
			out = append(out, t.Name)
		}
	}
	return out
}

// IsToolOfCategory reports whether a name is a tool in the category.
func IsToolOfCategory(name, category string) bool {
	t, ok := FindTool(name)
	return ok && t.Category == category
}
