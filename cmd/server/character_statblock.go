package main

// Stat block data for the public character profile, modelled on the 2024
// Monster Manual / SRD 5.2.1 stat block: Size, Speed, Alignment, Resistances,
// Vulnerabilities, Immunities, then Traits, Actions, Bonus Actions, Reactions.
// Everything here is derived from data the server already stores. Actions,
// Bonus Actions and Reactions come from the wording of each feature's own
// description ("as a bonus action", "as a reaction", "as an action"), so a
// feature is only listed under one of those headings when its text says so.

import (
	"time"
	"encoding/json"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

var allowedAlignments = []string{
	"Lawful Good", "Neutral Good", "Chaotic Good",
	"Lawful Neutral", "Neutral", "Chaotic Neutral",
	"Lawful Evil", "Neutral Evil", "Chaotic Evil", "Unaligned",
}

// canonicalAlignment returns the standard spelling, or "" if not recognised.
func canonicalAlignment(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	for _, a := range allowedAlignments {
		if strings.ToLower(a) == s {
			return a
		}
	}
	return ""
}

// playerAlignments: the nine alignments a player character can have ("Unaligned" is for creatures).
func playerAlignments() []string {
	var out []string
	for _, a := range allowedAlignments {
		if a != "Unaligned" {
			out = append(out, a)
		}
	}
	return out
}

// canonicalPlayerAlignment: the standard spelling of one of the nine alignments, or "".
func canonicalPlayerAlignment(s string) string {
	if a := canonicalAlignment(s); a != "Unaligned" {
		return a
	}
	return ""
}

// alignmentDescriptions: the SRD's summary of each alignment (The Nine Alignments), exactly as printed.
var alignmentDescriptions = srdAlignments

type sbEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source,omitempty"`
	Group       string `json:"group,omitempty"` // species, class, subclass, feat, background
	Label       string `json:"label,omitempty"` // heading for the group, e.g. "Gnome" or "Evocation"
	Kind        string `json:"kind,omitempty"`  // trait, action, bonus_action, reaction, attack
}

// classifyActionType reads a feature description and reports which heading it
// belongs under: "action", "bonus_action", "reaction", or "" for a trait.
// The earliest matching phrase wins.
func classifyActionType(desc string) string {
	d := strings.ToLower(desc)
	phrases := []struct {
		text string
		kind string
	}{
		{"as a bonus action", "bonus_action"},
		{"use a bonus action", "bonus_action"},
		{"using a bonus action", "bonus_action"},
		{"bonus action to", "bonus_action"},
		{"as a reaction", "reaction"},
		{"use your reaction", "reaction"},
		{"using your reaction", "reaction"},
		{"as an action", "action"},
		{"use your action", "action"},
		{"using your action", "action"},
		{"use an action", "action"},
	}
	best, bestKind := -1, ""
	for _, p := range phrases {
		if i := strings.Index(d, p.text); i >= 0 && (best < 0 || i < best) {
			best, bestKind = i, p.kind
		}
	}
	return bestKind
}

func titleWords(s string) string {
	s = strings.NewReplacer("_", " ", "-", " ").Replace(s)
	words := strings.Fields(s)
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
	}
	return strings.Join(words, " ")
}

// buildStatBlock assembles the stat block data for one character.
func buildStatBlock(charID int, species, class string, level int, subclassSlug, background,
	draconicAncestry, fiendishResilience, alignment string, conMod int, featsJSON []byte, originJSON []byte) map[string]interface{} {

	sb := map[string]interface{}{}
	rec, hasOrigin := loadOriginRecord(originJSON)

	// Size and Speed (species)
	sb["size"] = game.GetSpeciesSize(species)
	sb["speed_feet"] = game.GetDefaultSpeed(species)
	if hasOrigin {
		sb["size"] = rec.Species.Size
		sb["speed_feet"] = rec.Species.Speed
		sb["darkvision_feet"] = rec.Species.Darkvision
		// effects that are running right now (Large Form, Draconic Flight, Stonecunning)
		active := map[string]string{}
		wall := loadSpeciesUses(charID).activeEffects(time.Now())
		for k := range speciesTraitEffects {
			if !speciesEffectActive(charID, k) {
				continue
			}
			active[k] = "running"
			if until, ok := wall[k]; ok {
				active[k] = until.Format(time.RFC3339)
			}
			switch k {
			case "large_form":
				sb["size"] = "Large"
				sb["speed_feet"] = rec.Species.Speed + 10
			case "draconic_flight":
				sb["fly_speed_feet"] = rec.Species.Speed
			case "stonecunning":
				sb["tremorsense_feet"] = 60
			}
		}
		if len(active) > 0 {
			sb["active_effects"] = active
		}
	}
	sb["alignment"] = alignment

	// Resistances / Vulnerabilities / Immunities that are always on
	resist, vuln, immDamage, immCond := []string{}, []string{}, []string{}, []string{}
	if hasOrigin {
		for _, r := range rec.Species.Resistances {
			resist = append(resist, titleWords(r))
		}
	}
	if !hasOrigin && game.HasHellishResistance(species) {
		resist = append(resist, "Fire")
	}
	if !hasOrigin && game.HasDwarvenResilience(species) {
		resist = append(resist, "Poison")
	}
	if !hasOrigin && game.IsDragonborn(species) && draconicAncestry != "" {
		if a := game.GetDragonAncestry(draconicAncestry); a != nil {
			resist = append(resist, titleWords(a.DamageType))
		}
	}
	if strings.EqualFold(class, "warlock") && fiendishResilience != "" {
		resist = append(resist, titleWords(fiendishResilience))
	}
	if strings.EqualFold(class, "monk") && level >= 10 {
		immDamage = append(immDamage, "Poison")
		immCond = append(immCond, "Poisoned")
	}
	sb["resistances"] = dedupe(resist)
	sb["vulnerabilities"] = vuln
	sb["immunities_damage"] = immDamage
	sb["immunities_conditions"] = immCond

	traits, actions, bonus, reactions := []sbEntry{}, []sbEntry{}, []sbEntry{}, []sbEntry{}
	all := []sbEntry{}
	place := func(e sbEntry) {
		e.Kind = classifyActionType(e.Description)
		if e.Kind == "" {
			e.Kind = "trait"
		}
		switch e.Kind {
		case "action":
			actions = append(actions, e)
		case "bonus_action":
			bonus = append(bonus, e)
		case "reaction":
			reactions = append(reactions, e)
		default:
			traits = append(traits, e)
		}
		all = append(all, e)
	}

	// Species traits, with their rule text (from the SRD traits table)
	speciesLabel := titleWords(species)
	if hasOrigin {
		for _, tr := range rec.Species.Traits {
			place(sbEntry{Name: tr.Name, Description: tr.Description, Source: "Species", Group: "species", Label: speciesLabel})
		}
		place(sbEntry{Name: titleWords(rec.Background) + " (Background)", Description: backgroundSummary(rec), Source: "Background", Group: "background", Label: titleWords(rec.Background)})
	} else {
		for _, e := range speciesTraits(species, draconicAncestry) {
			e.Group, e.Label = "species", speciesLabel
			place(e)
		}
	}

	// Dragonborn breath weapon is an Action
	if !hasOrigin && game.HasBreathWeapon(species) && draconicAncestry != "" {
		if a := game.GetDragonAncestry(draconicAncestry); a != nil {
			ability := "Dexterity"
			if a.SaveAbility == "con" {
				ability = "Constitution"
			}
			breath := sbEntry{
				Name: "Breath Weapon",
				Description: "You exhale destructive energy in a " + a.BreathArea + ". Each creature in the area makes a " +
					ability + " saving throw, taking " + game.BreathWeaponDamage(level) + " " + titleWords(a.DamageType) +
					" damage on a failed save, or half as much on a success. Save DC " +
					itoa(8+conMod+game.ProficiencyBonus(level)) + ". You can use it once per short or long rest.",
				Source: "Species", Group: "species", Label: speciesLabel, Kind: "action"}
			actions = append(actions, breath)
			all = append(all, breath)
		}
	}

	// Class features
	for _, f := range characterClassFeatures(charID, class, level) {
		place(sbEntry{Name: f.Name, Description: f.Description, Source: titleWords(class) + " " + itoa(f.Level), Group: "class", Label: titleWords(class)})
	}
	// Subclass features
	if subclassSlug != "" {
		if sc := game.GetSubclass(subclassSlug); sc != nil {
			for _, f := range game.GetActiveSubclassFeatures(subclassSlug, level) {
				place(sbEntry{Name: f.Name, Description: f.Description, Source: sc.Name, Group: "subclass", Label: sc.Name})
			}
		}
	}
	// Feats
	var feats []string
	json.Unmarshal(featsJSON, &feats)
	for _, slugF := range feats {
		if ft, ok := lookupFeat(slugF); ok {
			text := ft.Description
			for _, b := range ft.Benefits {
				if !strings.Contains(text, b) {
					text += " " + b
				}
			}
			place(sbEntry{Name: ft.Name, Description: text, Source: "Feat", Group: "feat", Label: "Feats"})
		}
	}

	sb["features"] = all
	sb["traits"] = traits
	sb["actions"] = actions
	sb["bonus_actions"] = bonus
	sb["reactions"] = reactions
	return sb
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
