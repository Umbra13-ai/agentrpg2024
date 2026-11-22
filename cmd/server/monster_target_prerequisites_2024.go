package main

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var (
	// singleSaveTargetRe: "one creature [it can see] within N feet", with what the creature must be: a condition it has ("one Frightened
	// creature the hag can see within 30 feet": SRD 5.2.1 Sea Hag Death Glare), or living at 0 Hit Points ("one living creature the wisp
	// can see within 5 feet that has 0 Hit Points": Will-o'-Wisp Consume Life). Groups: 1 living, 2 the condition, 3 the sight clause,
	// 4 the range, 5 "that has 0 Hit Points".
	singleSaveTargetRe  = regexp.MustCompile(`(?i)^one (living )?(?:([a-z]+) )?(?:creature|target)(?: ((?:it|the [\pL][\pL '’\-]*?) can see))? within ([0-9]+) (?:feet|ft\.?)( that has 0 Hit Points)?\.?$`)
	monsterBlindsightRe = regexp.MustCompile(`(?i)\bblindsight\s+(\d+)\s*(?:feet|ft\b)`)
)

// singleSaveTarget: the single target a save entry's header names (singleSaveTargetRe's groups), nil when it names another kind.
func singleSaveTarget(text string) []string {
	match := mfxSaveRe.FindStringIndex(text)
	if match == nil {
		return nil
	}
	header := strings.TrimSpace(text[match[1]:])
	if !strings.HasPrefix(header, ",") {
		return nil
	}
	spec, _, _ := strings.Cut(strings.TrimSpace(header[1:]), " Failure:")
	parts := singleSaveTargetRe.FindStringSubmatch(strings.TrimSpace(spec))
	if parts == nil || (parts[2] != "" && !mfxConditions[strings.ToLower(parts[2])]) {
		return nil
	}
	return parts
}

// livingAtZero: a creature at 0 Hit Points that is still alive: a character that isn't dead (dying or Stable). A monster or a companion
// at 0 Hit Points is dead or destroyed (it has no Death Saving Throws).
func livingAtZero(lobby, id int) bool {
	if id <= 0 || isCompanionCombatID(id) {
		return false
	}
	var dead bool
	db.QueryRow(`SELECT COALESCE(is_dead,false) FROM characters WHERE id=$1`, id).Scan(&dead)
	return !dead
}

// For a single ranged target, validate the supported header and map facts.
func monsterSaveTargetIssue(r *http.Request, lobby, source int, meta *monsterMeta, text string, targets []int) string {
	parts := singleSaveTarget(text)
	if parts == nil {
		return ""
	}
	if len(targets) != 1 {
		return "Select exactly one creature for this action."
	}
	// what the target must be, read from the same header: a condition it has, or living at 0 Hit Points
	if cond := strings.ToLower(parts[2]); cond != "" && !hasCondMarker(lobby, targets[0], cond) {
		return fmt.Sprintf("%s doesn't have the %s condition: this action targets only a %s creature.", creatureName(lobby, targets[0]), titleCase(cond), titleCase(cond))
	}
	if parts[5] != "" {
		if hp, _ := creatureHP(lobby, targets[0]); hp != 0 {
			return fmt.Sprintf("%s has %d Hit Points: this action targets only a living creature that has 0 Hit Points.", creatureName(lobby, targets[0]), hp)
		}
		if !livingAtZero(lobby, targets[0]) {
			return fmt.Sprintf("%s isn't living: this action targets only a living creature that has 0 Hit Points.", creatureName(lobby, targets[0]))
		}
	}
	maxRange, err := strconv.Atoi(parts[4])
	if err != nil || maxRange < 1 {
		return "The server could not read this action's range. Adjudicate the targeting before applying the effect."
	}
	var distance int
	var placed bool
	needsSight := parts[3] != ""
	if battle, ok := activeEncounterMap(lobby); ok {
		a, hasSource := battle.token(source)
		b, hasTarget := battle.token(targets[0])
		placed = hasSource && hasTarget
		if placed {
			distance = tokenDistanceFeet(a, b)
			if needsSight && !battle.canSee(a, b) {
				return "The target has Total Cover on the battle map. Move into clear sight first."
			}
		}
	}
	if placed && distance > maxRange {
		return fmt.Sprintf("The target is %d feet away. This action has a range of %d feet.", distance, maxRange)
	}
	if needsSight && areaBlocksSight(lobby, source, targets[0]) { // fog, ink or Magical Darkness between them (obscured_areas_2024.go)
		return "The creature can't see the target: a Heavily Obscured area or Magical Darkness is in the way (only Blindsight, or Truesight in darkness, perceives through it)."
	}
	if needsSight && placed && !hasCondMarker(lobby, source, "blinded") {
		// The same lighting sight model the attack path uses (combat_handlers.go):
		// darkness the creature's Darkvision, Blindsight or Truesight doesn't reach
		// hides the target. Blinded is handled below instead: it removes ordinary
		// sight in ANY light, which the lighting model cannot express.
		if lighting := monsterLightingAt(lobby, targets[0]); !creatureSeesInLighting(lobby, source, lighting, distance) {
			darkvision, blindsight, truesight := creatureVision(lobby, source)
			return fmt.Sprintf("The target is %d feet away in %s the creature can't make out (its Darkvision reaches %d ft, Blindsight %d ft, Truesight %d ft).", distance, lighting, darkvision, blindsight, truesight)
		}
	}
	if needsSight && hasCondMarker(lobby, source, "blinded") {
		blindsight := monsterBlindsightRe.FindStringSubmatch(meta.Senses)
		if blindsight == nil {
			return "The creature is Blinded and its stat block lists no Blindsight. Resolve any other applicable sight source with the GM."
		}
		limit, _ := strconv.Atoi(blindsight[1])
		if placed && distance > limit {
			return fmt.Sprintf("The creature is Blinded and the target is beyond its %d-foot Blindsight.", limit)
		}
	}
	rec := execIn(r)
	if (needsSight || !placed) && (rec == nil || !rec.confirmTargeting) {
		switch {
		case needsSight && !placed:
			return fmt.Sprintf("Confirm the creature can see the target and that it is within %d feet. Then retry with confirm_targeting true.", maxRange)
		case needsSight:
			return "Confirm the creature can see the target. Then retry with confirm_targeting true."
		default:
			return fmt.Sprintf("Confirm the target is within %d feet. Then retry with confirm_targeting true.", maxRange)
		}
	}
	return ""
}
