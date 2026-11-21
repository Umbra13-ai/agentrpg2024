package main

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var (
	singleSaveTargetRe  = regexp.MustCompile(`(?i)^one (?:creature|target)(?: ((?:it|the [\pL][\pL '’\-]*?) can see))? within ([0-9]+) (?:feet|ft\.?)\.?$`)
	monsterBlindsightRe = regexp.MustCompile(`(?i)\bblindsight\s+(\d+)\s*(?:feet|ft\b)`)
	pathSaveTriggerRe   = regexp.MustCompile(`(?i)\b(?:each time|whenever) [^.!?]+\benters (?:a|the|another) creature['’]s space[^.!?]+\b(?:this|that) move\b`)
)

// A movement-triggered save needs the traversed path and its movement rules.
// For a single ranged target, validate the supported header and map facts.
func monsterSaveTargetIssue(r *http.Request, lobby, source int, meta *monsterMeta, text string, targets []int) string {
	match := mfxSaveRe.FindStringIndex(text)
	if match == nil {
		return ""
	}
	if pathSaveTriggerRe.MatchString(text[:match[0]]) {
		return "Resolve the required movement through creature spaces and its effects together as written in the compendium. The server does not verify this action's movement path or resolve the resulting save."
	}
	header := strings.TrimSpace(text[match[1]:])
	if !strings.HasPrefix(header, ",") {
		return ""
	}
	spec, _, _ := strings.Cut(strings.TrimSpace(header[1:]), " Failure:")
	parts := singleSaveTargetRe.FindStringSubmatch(strings.TrimSpace(spec))
	if parts == nil {
		return ""
	}
	if len(targets) != 1 {
		return "Select exactly one creature for this action."
	}
	maxRange, err := strconv.Atoi(parts[2])
	if err != nil || maxRange < 1 {
		return "The server could not read this action's range. Adjudicate the targeting before applying the effect."
	}
	var distance int
	var placed bool
	needsSight := parts[1] != ""
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
