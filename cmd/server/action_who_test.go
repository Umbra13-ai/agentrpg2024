package main

// TestActionWhoMatchesGates: the action table (action_types_2024.go) says who has each action type (Who) and in what state (While);
// each feature's resolver checks the same thing in its own gate (feature_gates_2024.go). The test asks both the same question, "may
// this character use this action type?", for every class at every level 1-20 with and without its SRD subclass, each with every
// feat, invocation, magic item, spell effect, companion, subclass choice and state the table names, and fails on every answer that
// differs (the type, the character, what the table says, what the resolver says). No database: the table is asked through
// actorFacts, each gate with what its resolver reads.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

// whoChar: a character as the comparison describes it.
type whoChar struct {
	class, sub                                          string
	level                                               int
	feats, invocations, effects                         map[string]bool
	held, attuned                                       map[string]bool // magic items by lower-case name
	companions                                          []companion
	choices                                             map[string]string
	raging, wildShaped, prone, dying, burning, inspired bool
	extra                                               string // what the variant adds, for the report
}

func (c whoChar) String() string {
	s := fmt.Sprintf("%s %d", c.class, c.level)
	if c.sub != "" {
		s += " (" + c.sub + ")"
	}
	if c.extra != "" {
		s += " + " + c.extra
	}
	return s
}

func (c whoChar) hasFeat(slug string) bool       { return c.feats[slug] }
func (c whoChar) hasInvocation(slug string) bool { return c.invocations[slug] }
func (c whoChar) hasEffect(kind string) bool     { return c.effects[kind] }
func (c whoChar) heldAttuned(item string) (bool, bool) {
	return c.held[strings.ToLower(item)], c.attuned[strings.ToLower(item)]
}

// levelIn: the character's level in a class (classLevelIn for a single-class character).
func (c whoChar) levelIn(class string) int {
	if c.class == class {
		return c.level
	}
	return 0
}

// facts: the character as the table reads it.
func (c whoChar) facts() actorFacts {
	return actorFacts{class: c.class, subclass: c.sub, level: c.level, raging: c.raging, wildShaped: c.wildShaped, prone: c.prone,
		dying: c.dying, burning: c.burning, inspired: c.inspired, choices: c.choices, hasFeat: c.hasFeat, hasInvocation: c.hasInvocation,
		hasEffect: c.hasEffect, heldAttuned: c.heldAttuned, companions: func() []companion { return c.companions }}
}

// anyCompanion: a companion's own action is open when one of the character's companions passes the gate.
func anyCompanion(c whoChar, gate func(companion) string) string {
	why := "no companion"
	for _, co := range c.companions {
		if w := gate(co); w == "" {
			return ""
		} else {
			why = w
		}
	}
	return why
}

// featureGates: each type's own gate, given what its resolver reads. Every type the table limits (Who or While) is here or in
// gatesNotCompared.
var featureGates = map[string]func(c whoChar) string{
	"rage":        func(c whoChar) string { return rageGate(c.class, c.levelIn("barbarian"), c.raging) },
	"extend_rage": func(c whoChar) string { return extendRageGate(c.class, c.raging) },
	"end_rage":    func(c whoChar) string { return endRageGate(c.class, c.raging) },
	"frenzy":      func(c whoChar) string { return frenzyGate(c.class, c.sub, c.level) },

	"bardic_inspiration":     func(c whoChar) string { return bardicInspirationGate(c.class) },
	"countercharm":           func(c whoChar) string { return countercharmGate(c.class, c.level) },
	"use_bardic_inspiration": func(c whoChar) string { return bardicDieGate(map[bool]int{true: 8}[c.inspired], nil, time.Now()) },

	"turn_undead":    func(c whoChar) string { return turnUndeadGate(c.class, c.level) },
	"divine_spark":   func(c whoChar) string { return divineSparkGate(c.class, c.level) },
	"preserve_life":  func(c whoChar) string { return preserveLifeGate(c.class, c.sub, c.level) },
	"sacred_weapon":  func(c whoChar) string { return sacredWeaponGate(c.class, c.sub, c.level) },
	"divine_sense":   func(c whoChar) string { return divineSenseGate(c.class, c.level) },
	"lay_on_hands":   func(c whoChar) string { return layOnHandsGate(c.class) },
	"holy_nimbus":    func(c whoChar) string { return holyNimbusGate(c.class, c.sub, c.level) },
	"horde_breaker":  func(c whoChar) string { return hordeBreakerGate(c.class, c.sub, c.level, c.choices["hunters_prey"]) },
	"tireless":       func(c whoChar) string { return tirelessGate(c.class, c.level) },
	"natures_veil":   func(c whoChar) string { return naturesVeilGate(c.class, c.level) },
	"cunning_action": func(c whoChar) string { return cunningActionGate(c.class, c.level) },
	"steady_aim":     func(c whoChar) string { return steadyAimGate(c.class, c.level) },

	"wild_shape":        func(c whoChar) string { return wildShapeGate(c.class, c.level) },
	"revert_wild_shape": func(c whoChar) string { return revertWildShapeGate(c.class, c.level, c.wildShaped) },
	"wild_resurgence":   func(c whoChar) string { return wildResurgenceGate(c.class, c.level) },
	"lands_aid":         func(c whoChar) string { return landsAidGate(c.class, c.sub, c.level) },
	"natural_recovery":  func(c whoChar) string { return naturalRecoveryGate(c.class, c.sub, c.level) },
	"natures_sanctuary": func(c whoChar) string { return naturesSanctuaryGate(c.class, c.sub, c.level) },
	"move_sanctuary":    func(c whoChar) string { return naturesSanctuaryGate(c.class, c.sub, c.level) },
	"nature_magician":   func(c whoChar) string { return natureMagicianGate(c.class, c.level) },

	"second_wind":  func(c whoChar) string { return secondWindGate(c.class) },
	"action_surge": func(c whoChar) string { return actionSurgeGate(c.class, c.level) },

	"martial_arts":         func(c whoChar) string { return martialArtsGate(c.class, c.level) },
	"flurry_of_blows":      func(c whoChar) string { return flurryOfBlowsGate(c.class, c.level) },
	"patient_defense":      func(c whoChar) string { return patientDefenseGate(c.class, c.level) },
	"step_of_the_wind":     func(c whoChar) string { return stepOfTheWindGate(c.class, c.level) },
	"stunning_strike":      func(c whoChar) string { return stunningStrikeGate(c.class, c.level) },
	"self_restoration":     func(c whoChar) string { return selfRestorationGate(c.class, c.level) },
	"disciplined_survivor": func(c whoChar) string { return disciplinedSurvivorGate(c.class, c.level) },
	"superior_defense":     func(c whoChar) string { return superiorDefenseGate(c.class, c.level) },
	"quivering_palm":       func(c whoChar) string { return quiveringPalmGate(c.class, c.sub, c.level) },
	"wholeness_of_body":    func(c whoChar) string { return wholenessOfBodyGate(c.class, c.sub, c.level) },

	"innate_sorcery":        func(c whoChar) string { return innateSorceryGate(c.class) },
	"font_of_magic":         func(c whoChar) string { return fontOfMagicGate(c.class, c.level) },
	"font_of_magic_convert": func(c whoChar) string { return fontOfMagicGate(c.class, c.level) },
	"dragon_wings":          func(c whoChar) string { return dragonWingsGate(c.class, c.sub, c.level) },

	"magical_cunning":        func(c whoChar) string { return magicalCunningGate(c.class, c.level) },
	"gaze_of_two_minds":      func(c whoChar) string { return gazeOfTwoMindsGate(c.class, c.hasInvocation) },
	"gift_of_the_protectors": func(c whoChar) string { return giftOfTheProtectorsGate(c.class, c.hasInvocation) },

	"improve_fate":       func(c whoChar) string { return improveFateGate(c.hasFeat) },
	"merge_with_shadows": func(c whoChar) string { return mergeWithShadowsGate(c.hasFeat) },
	"blink_steps":        func(c whoChar) string { return blinkStepsGate(c.hasFeat) },

	"bonus_attack": func(c whoChar) string { return scimitarOfSpeedGate(c.heldAttuned) },

	"spiritual_weapon": func(c whoChar) string { return spiritualWeaponGate(c.hasEffect) },
	"flame_blade":      func(c whoChar) string { return flameBladeGate(c.hasEffect) },
	"gaseous_form":     func(c whoChar) string { return endGaseousFormGate(c.hasEffect) },

	"breath":                   func(c whoChar) string { return companionAction(c, "breath") },
	"healing_touch":            func(c whoChar) string { return companionAction(c, "healing_touch") },
	"fey_step":                 func(c whoChar) string { return companionAction(c, "fey_step") },
	"fell_glare":               func(c whoChar) string { return companionAction(c, "fell_glare") },
	"dismiss_familiar":         func(c whoChar) string { return anyCompanion(c, familiarCommandGate) },
	"recall_familiar":          func(c whoChar) string { return anyCompanion(c, familiarCommandGate) },
	"dismiss_familiar_forever": func(c whoChar) string { return anyCompanion(c, familiarCommandGate) },

	"stand":      func(c whoChar) string { return standGate(c.prone) },
	"extinguish": func(c whoChar) string { return extinguishGate(c.burning) },
}

func companionAction(c whoChar, action string) string {
	return anyCompanion(c, func(co companion) string { return companionSpecialGate(action, co) })
}

// gatesNotCompared: types the table limits whose resolver is outside this comparison, and why.
var gatesNotCompared = map[string]string{
	"death_save": "the dying state is the Hit Points owner's (recordDeathSave) and the turn schedule's (turnNeeds); not a feature gate",
}

// whoChars: every class at every level 1-20, without a subclass and with its SRD subclass, each plain and with each feat,
// invocation, item, effect, companion, subclass choice and state the table names.
func whoChars() []whoChar {
	feats, invocations, effects, items := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	choices := map[string][]string{"hunters_prey": {"colossus_slayer"}}
	for _, t := range actionTypes {
		w := t.Who
		feats[w.Feat], invocations[w.Invocation], effects[w.Effect], items[strings.ToLower(w.Item)] = true, true, true, true
		if k, v, ok := strings.Cut(w.Choice, "="); ok {
			choices[k] = append(choices[k], v)
		}
	}
	type variant struct {
		name string
		add  func(c *whoChar)
	}
	variants := []variant{{"", func(*whoChar) {}}}
	for _, f := range sortedTrue(feats) {
		f := f
		variants = append(variants, variant{"feat " + f, func(c *whoChar) { c.feats = map[string]bool{f: true} }})
	}
	for _, i := range sortedTrue(invocations) {
		i := i
		variants = append(variants, variant{"invocation " + i, func(c *whoChar) { c.invocations = map[string]bool{i: true} }})
	}
	for _, e := range sortedTrue(effects) {
		e := e
		variants = append(variants, variant{"effect " + e, func(c *whoChar) { c.effects = map[string]bool{e: true} }})
	}
	for _, it := range sortedTrue(items) {
		it := it
		variants = append(variants,
			variant{it + " in hand, attuned", func(c *whoChar) { c.held, c.attuned = map[string]bool{it: true}, map[string]bool{it: true} }},
			variant{it + " in hand, not attuned", func(c *whoChar) { c.held = map[string]bool{it: true} }},
			variant{it + " attuned, not in hand", func(c *whoChar) { c.attuned = map[string]bool{it: true} }})
	}
	companions := []companion{
		{Kind: "familiar", Name: "Owl", Form: "owl", CreatureType: "fey", Status: "active"},
		{Kind: "familiar", Name: "Imp", Form: "imp", CreatureType: "fiend", Status: "dismissed"},
		{Kind: "steed", Name: "Steed", Form: "otherworldly-steed", CreatureType: "celestial", Status: "active"},
		{Kind: "steed", Name: "Steed", Form: "otherworldly-steed", CreatureType: "fey", Status: "active"},
		{Kind: "steed", Name: "Steed", Form: "otherworldly-steed", CreatureType: "fiend", Status: "active"},
		{Kind: "summon", Name: "Draconic Spirit", Form: "draconic-spirit", CreatureType: "dragon", Status: "active"},
		{Kind: "summon", Name: "Giant Insect", Form: "giant-insect", CreatureType: "beast", Status: "active"},
	}
	for _, co := range companions {
		co := co
		variants = append(variants, variant{fmt.Sprintf("%s %s (%s, %s)", co.Kind, co.Form, co.CreatureType, co.Status),
			func(c *whoChar) { c.companions = []companion{co} }})
	}
	for k, vs := range choices {
		for _, v := range vs {
			k, v := k, v
			variants = append(variants, variant{k + "=" + v, func(c *whoChar) { c.choices = map[string]string{k: v} }})
		}
	}
	states := map[string]func(c *whoChar){
		"raging": func(c *whoChar) { c.raging = true }, "wild shaped": func(c *whoChar) { c.wildShaped = true },
		"prone": func(c *whoChar) { c.prone = true }, "dying": func(c *whoChar) { c.dying = true },
		"burning": func(c *whoChar) { c.burning = true }, "holding a Bardic Inspiration die": func(c *whoChar) { c.inspired = true },
	}
	for name, add := range states {
		variants = append(variants, variant{name, add})
	}

	var out []whoChar
	for sub, s := range game.AvailableSubclasses {
		for level := 1; level <= 20; level++ {
			for _, withSub := range []string{"", sub} {
				for _, v := range variants {
					c := whoChar{class: s.Class, sub: withSub, level: level, extra: v.name}
					v.add(&c)
					out = append(out, c)
				}
			}
		}
	}
	return out
}

func sortedTrue(m map[string]bool) []string {
	var out []string
	for k, v := range m {
		if v && k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func TestActionWhoMatchesGates(t *testing.T) {
	for _, at := range actionTypes {
		limited := at.Who != (actionWho{}) || at.While != ""
		_, compared := featureGates[at.Name]
		_, skipped := gatesNotCompared[at.Name]
		if limited && !compared && !skipped {
			t.Errorf("%s: the table limits who has it (Who or While) but featureGates has no gate for it: extract its resolver's gate into feature_gates_2024.go and add it", at.Name)
		}
	}
	for name := range featureGates {
		if _, ok := lookupActionType(name); !ok {
			t.Errorf("featureGates names %q, which is not in the action table", name)
		}
	}

	chars := whoChars()
	if len(chars) < 10000 {
		t.Fatalf("only %d characters described", len(chars))
	}
	type disagreement struct{ char, table, resolver string }
	found := map[string][]disagreement{}
	for _, at := range actionTypes {
		gate, ok := featureGates[at.Name]
		if !ok {
			continue
		}
		for _, c := range chars {
			f := c.facts()
			tableSays, why := at.openTo(&f), gate(c)
			if tableSays != (why == "") {
				d := disagreement{c.String(), "no", "yes"}
				if tableSays {
					d = disagreement{c.String(), "yes", "no: " + why}
				}
				found[at.Name] = append(found[at.Name], d)
			}
		}
	}
	var names []string
	for n := range found {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		ds := found[n]
		var lines []string
		for i, d := range ds {
			if i == 5 {
				lines = append(lines, fmt.Sprintf("    ... and %d more", len(ds)-5))
				break
			}
			lines = append(lines, fmt.Sprintf("    %s: table says %s, resolver says %s", d.char, d.table, d.resolver))
		}
		t.Errorf("%s: the table's Who/While and the resolver's gate disagree for %d characters:\n%s", n, len(ds), strings.Join(lines, "\n"))
	}
}

// TestFeatureGatesAreCalled: every gate in feature_gates_2024.go is used by the server outside its own declaration (a resolver calls
// it, or passes it to the helper that does), so the comparison above reads the gate the resolver really applies.
func TestFeatureGatesAreCalled(t *testing.T) {
	fset := token.NewFileSet()
	gates := map[string]bool{}
	src, err := parser.ParseFile(fset, "feature_gates_2024.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range src.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && strings.HasSuffix(fd.Name.Name, "Gate") {
			gates[fd.Name.Name] = false
		}
	}
	files, _ := filepath.Glob("*.go")
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fd, isFunc := d.(*ast.FuncDecl)
			ast.Inspect(d, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					if _, isGate := gates[id.Name]; isGate && !(isFunc && fd.Name == id) {
						gates[id.Name] = true
					}
				}
				return true
			})
		}
	}
	for g, used := range gates {
		if !used {
			t.Errorf("%s (feature_gates_2024.go) is used nowhere: its resolver must call it", g)
		}
	}
	if _, err := os.Stat("feature_gates_2024.go"); err != nil {
		t.Fatal(err)
	}
}
