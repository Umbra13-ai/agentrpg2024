package main

// Auto-equip for a new character (Nov 12): characters never put their gear on or armed their weapons, so the server asks.
//
//	GET  /api/characters/auto-equip?character_id=N     the question, what the server would do, and any choices it would need from you
//	POST /api/characters/auto-equip                    {"character_id": N, "answer": "yes" | "no", "choices": {...}}
//
// "yes" puts on the best armor the character is trained in, takes a weapon in the main hand, a Shield or a Light weapon in the other hand,
// and wears clothing and jewelry. Where there is more than one way to do it (more weapons than hands, two items for the same body place,
// such as two Robes) nothing is changed: the reply lists each question with its options, and the player answers with "choices"
// ({"main_hand": "<item id or none>", "off_hand": "<shield | item id | none>", "worn_body": "<item id>"}). "no" leaves everything as it is.
// The question stays in the character's to_finish list (and in /setup) until it is answered.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

var autoEquipOnce sync.Once

func ensureAutoEquipColumn() {
	autoEquipOnce.Do(func() {
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS auto_equip_answered BOOLEAN DEFAULT false`)
	})
}

type equipAction struct {
	Kind  string // armor, main_hand, off_hand, shield, worn
	Name  string
	ID    string
	Label string
}

// weaponDescription: what a weapon is, so the player can choose between them.
func weaponDescription(it invItem) string {
	name := itemStr(it, "name")
	_, w, ok := findWeapon(name)
	if !ok {
		return name
	}
	hands := "one hand"
	if hasProperty(w.Properties, "two-handed") {
		hands = "two hands"
	}
	props := ""
	if len(w.Properties) > 0 {
		props = ", " + strings.Join(w.Properties, ", ")
	}
	extra := ""
	if hasProperty(w.Properties, "light") {
		extra = " " + srdLightProperty
	}
	if hasProperty(w.Properties, "two-handed") {
		extra += " " + srdTwoHandedProperty
	}
	return fmt.Sprintf("%s (id %s): %s %s damage%s; held in %s.%s", name, itemStr(it, "id"), w.Damage, w.DamageType, props, hands, extra)
}

// equipPlan: what "yes" would do without asking (auto), and the questions it cannot answer for the player. choices answers questions.
func equipPlan(charID int, choices map[string]string) (auto []string, questions []map[string]interface{}, actions []equipAction) {
	inv := inventoryOf(charID)
	lost := lostChecker(inv)
	var profs string
	var current sql.NullString
	db.QueryRow(`SELECT COALESCE(armor_proficiencies,''), equipped_armor FROM characters WHERE id = $1`, charID).Scan(&profs, &current)
	curMain, curOff := equippedWeapons(charID)
	// a spellcaster needs a free hand for spells with gestures (S) (hands_2024.go), so the other hand is never filled without asking
	var cls string
	var lvl int
	db.QueryRow(`SELECT COALESCE(class,''), COALESCE(level,1) FROM characters WHERE id = $1`, charID).Scan(&cls, &lvl)
	cantripCaster := game.ClassTableInt(strings.ToLower(cls), lvl, "Cantrips") > 0
	caster := cantripCaster || game.MaxPreparedSpells(strings.ToLower(cls), lvl, 10, 10, 10) > 0
	shield := shieldInHand(charID)

	// armor
	for _, it := range inv {
		if lost(it) || itemBool(it, "destroyed") {
			continue
		}
		if _, info := armorValueFor(itemStr(it, "name")); info != nil && !sameArmor(itemStr(it, "name"), current.String) {
			auto = append(auto, "Put on the best armor you are trained in, if it raises your AC.")
			actions = append(actions, equipAction{Kind: "armor"})
			break
		}
	}

	// hands
	var weapons []invItem
	for _, it := range inv {
		if lost(it) || itemBool(it, "destroyed") {
			continue
		}
		if _, _, ok := findWeapon(itemStr(it, "name")); ok {
			weapons = append(weapons, it)
		}
	}
	main := curMain
	mainPending := false
	if main == "" && len(weapons) > 0 {
		switch {
		case len(weapons) == 1 && !(cantripCaster && shield) && choices["main_hand"] == "":
			main = itemStr(weapons[0], "name")
			auto = append(auto, "Hold your "+main+" in your main hand.")
			actions = append(actions, equipAction{Kind: "main_hand", Name: main, ID: itemStr(weapons[0], "id")})
		case choices["main_hand"] == "none":
		case choices["main_hand"] != "" && weaponByID(weapons, choices["main_hand"]) != nil:
			it := weaponByID(weapons, choices["main_hand"])
			main = itemStr(it, "name")
			actions = append(actions, equipAction{Kind: "main_hand", Name: main, ID: itemStr(it, "id")})
		default:
			mainPending = true
			opts := []map[string]string{}
			ask := "You carry more than one weapon. Which one do you want in your main hand?"
			if cantripCaster && shield {
				ask = "You hold a Shield and you cast spells. A spell with a Somatic component (S) needs a free hand, so with a weapon in your other hand you can't cast most of your spells until you put it away (one weapon change a turn). Do you want a weapon in hand?"
				opts = append(opts, map[string]string{"value": "none", "label": "Keep this hand free for spells; draw a weapon when you want to attack (recommended for a spellcaster with a Shield)."})
			}
			for _, it := range weapons {
				opts = append(opts, map[string]string{"value": itemStr(it, "id"), "label": weaponDescription(it)})
			}
			if !(cantripCaster && shield) {
				opts = append(opts, map[string]string{"value": "none", "label": "Keep your main hand empty."})
			}
			q := map[string]interface{}{"key": "main_hand", "ask": ask, "options": opts}
			if v := choices["main_hand"]; v != "" {
				q["problem"] = fmt.Sprintf("%q is not one of the options: send the value of one of them", v)
			}
			questions = append(questions, q)
		}
	}
	// the other hand (asked after the main hand, because the choices depend on it)
	if !mainPending && curOff == "" && !shield && (main == "" || !isTwoHandedWeapon(main)) {
		type cand struct{ value, label, kind, name string }
		var cands []cand
		for _, it := range inv {
			if lost(it) || itemBool(it, "destroyed") {
				continue
			}
			n := itemStr(it, "name")
			if strings.Contains(strings.ToLower(n), "shield") && shieldTrained(profs) {
				if _, info := armorValueFor(n); info != nil || strings.EqualFold(n, "shield") {
					cands = append(cands, cand{"shield", "Your Shield. " + srdShieldRule, "shield", n})
					break
				}
			}
		}
		// the weapon already in (or going to) the main hand is one carried item: skip that one, but not a second of the same kind
		mainID := mainIDOf(actions)
		skippedMain := false
		for _, it := range weapons {
			_, w, _ := findWeapon(itemStr(it, "name"))
			if mainID != "" && itemStr(it, "id") == mainID {
				skippedMain = true
				continue
			}
			if mainID == "" && !skippedMain && main != "" && strings.EqualFold(itemStr(it, "name"), main) {
				skippedMain = true
				continue
			}
			if !hasProperty(w.Properties, "light") {
				continue
			}
			cands = append(cands, cand{itemStr(it, "id"), weaponDescription(it), "off_hand", itemStr(it, "name")})
		}
		pick := func(c cand) {
			if c.kind == "shield" {
				auto = append(auto, "Take up your Shield in your other hand.")
				actions = append(actions, equipAction{Kind: "shield", Name: c.name})
			} else {
				actions = append(actions, equipAction{Kind: "off_hand", Name: c.name, ID: c.value})
			}
		}
		switch {
		case len(cands) == 1 && !caster:
			pick(cands[0])
		case len(cands) > 1 || (caster && len(cands) == 1):
			matched := choices["off_hand"] == "none"
			for _, c := range cands {
				if c.value == choices["off_hand"] {
					pick(c)
					matched = true
				}
			}
			if !matched {
				opts := []map[string]string{}
				ask := "More than one thing could go in your other hand. Which do you want?"
				if caster {
					ask = "What do you want in your other hand? You cast spells: a spell with a Somatic component (S) needs a free hand, and a Spellcasting Focus in hand does the gestures only for a spell with a Material component that has no cost. With something in both hands you can't cast most of your spells until you free a hand (one weapon change a turn)."
					opts = append(opts, map[string]string{"value": "none", "label": "Keep your other hand free, so you can cast spells with gestures (recommended for a spellcaster)."})
				}
				for _, c := range cands {
					opts = append(opts, map[string]string{"value": c.value, "label": c.label})
				}
				if !caster {
					opts = append(opts, map[string]string{"value": "none", "label": "Keep your other hand free."})
				}
				q := map[string]interface{}{"key": "off_hand", "ask": ask, "options": opts}
				if v := choices["off_hand"]; v != "" {
					q["problem"] = fmt.Sprintf("%q is not one of the options: send the value of one of them, or \"none\"", v)
				}
				questions = append(questions, q)
			}
		}
	}

	// clothing and jewelry
	var places []string
	cands := map[string][]invItem{}
	limitedPlace := map[string]bool{}
	for _, it := range inv {
		if lost(it) || itemBool(it, "destroyed") || itemStr(it, "location") == "worn" {
			continue
		}
		name := itemStr(it, "name")
		if _, info := armorValueFor(name); info != nil {
			continue
		}
		if _, _, isWeapon := findWeapon(name); isWeapon || strings.Contains(strings.ToLower(name), "shield") {
			continue
		}
		place, limited, _ := bodyPlaceOf(name)
		if place == "" {
			continue
		}
		if itemNeedsAttunement(name) || itemNeedsAttunement(baseItemName(name)) {
			auto = append(auto, fmt.Sprintf("Not put on: %s needs attunement (done during a Short Rest).", name))
			continue
		}
		if limited && wornConflict(charID, inv, it) != "" {
			continue
		}
		if _, seen := cands[place]; !seen {
			places = append(places, place)
		}
		cands[place] = append(cands[place], it)
		limitedPlace[place] = limited
	}
	for _, place := range places {
		list := cands[place]
		if !limitedPlace[place] || len(list) == 1 {
			for _, it := range list {
				auto = append(auto, fmt.Sprintf("Wear your %s (%s).", itemStr(it, "name"), place))
				actions = append(actions, equipAction{Kind: "worn", Name: itemStr(it, "name"), ID: itemStr(it, "id")})
			}
			continue
		}
		key := "worn_" + place
		if v := choices[key]; v != "" {
			var it invItem
			for _, x := range list {
				if itemStr(x, "id") == v {
					it = x
				}
			}
			if it != nil {
				actions = append(actions, equipAction{Kind: "worn", Name: itemStr(it, "name"), ID: itemStr(it, "id")})
				continue
			}
		}
		opts := []map[string]string{}
		for _, it := range list {
			src := itemStr(it, "source")
			if src == "" {
				src = "your gear"
			} else {
				src = "your " + src
			}
			opts = append(opts, map[string]string{"value": itemStr(it, "id"), "label": fmt.Sprintf("%s (id %s), from %s.", itemStr(it, "name"), itemStr(it, "id"), src)})
		}
		q := map[string]interface{}{"key": key, "ask": fmt.Sprintf("You can wear only one item on your %s, and you carry %d. Which one do you want to wear?", place, len(list)), "options": opts}
		if v := choices[key]; v != "" {
			q["problem"] = fmt.Sprintf("%q is not one of the options: send the value of one of them", v)
		}
		questions = append(questions, q)
	}
	return
}

func mainIDOf(actions []equipAction) string {
	for _, a := range actions {
		if a.Kind == "main_hand" {
			return a.ID
		}
	}
	return ""
}

func internalCall(agentID int, method, path string, body interface{}) (int, map[string]interface{}) {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req = req.WithContext(context.WithValue(req.Context(), internalAgentKey{}, agentID))
	rec := httptest.NewRecorder()
	http.DefaultServeMux.ServeHTTP(rec, req)
	out := map[string]interface{}{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// runEquip: do the actions; every one reports what happened.
func runEquip(charID, agentID int, actions []equipAction) []map[string]interface{} {
	var results []map[string]interface{}
	for _, a := range actions {
		var code int
		var out map[string]interface{}
		what := ""
		switch a.Kind {
		case "armor":
			what = "Put on armor"
			if r := autoEquipArmor(charID); r != nil {
				code, out = 200, r
				what = fmt.Sprintf("Put on the %v", r["armor"])
			} else {
				code, out = 200, map[string]interface{}{"note": "Your armor was already on or no armor in your pack raises your AC."}
			}
		case "main_hand":
			what = "Hold the " + a.Name + " in your main hand"
			code, out = internalCall(agentID, "POST", "/api/characters/equip-weapon", map[string]interface{}{"character_id": charID, "weapon": a.Name, "slot": "main_hand"})
		case "off_hand":
			what = "Hold the " + a.Name + " in your other hand"
			code, out = internalCall(agentID, "POST", "/api/characters/equip-weapon", map[string]interface{}{"character_id": charID, "weapon": a.Name, "slot": "off_hand"})
		case "shield":
			what = "Take up your Shield"
			code, out = internalCall(agentID, "POST", "/api/characters/equip-armor", map[string]interface{}{"character_id": charID, "shield": true})
		case "worn":
			what = "Wear the " + a.Name
			code, out = internalCall(agentID, "POST", fmt.Sprintf("/api/characters/%d/equipment/move", charID), map[string]interface{}{"item": a.ID, "to": "worn"})
		}
		ok := code < 400 && out["error"] == nil
		r := map[string]interface{}{"did": what, "done": ok}
		if !ok {
			r["problem"] = out
		}
		results = append(results, r)
	}
	return results
}

func autoEquipAnswered(charID int) bool {
	ensureAutoEquipColumn()
	var done bool
	db.QueryRow(`SELECT COALESCE(auto_equip_answered,false) FROM characters WHERE id = $1`, charID).Scan(&done)
	return done
}

// autoEquipOpen: the line for the character's to_finish list ("" once answered or when there is nothing to equip).
func autoEquipOpen(charID int) string {
	if autoEquipAnswered(charID) {
		return ""
	}
	auto, questions, actions := equipPlan(charID, nil)
	if len(auto) == 0 && len(questions) == 0 && len(actions) == 0 {
		return ""
	}
	return fmt.Sprintf("Equipment: do you want me to put on your armor, arm your weapons and wear your gear for you? POST /api/characters/auto-equip {\"character_id\": %d, \"answer\": \"yes\"} (or \"no\"). If you have a choice to make (two weapons, two Robes) I will ask you which.", charID)
}

func handleAutoEquip(w http.ResponseWriter, r *http.Request) {
	agent, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var req struct {
		CharacterID int               `json:"character_id"`
		Answer      string            `json:"answer"`
		Choices     map[string]string `json:"choices"`
	}
	if r.Method == http.MethodGet {
		fmt.Sscanf(r.URL.Query().Get("character_id"), "%d", &req.CharacterID)
	} else {
		json.NewDecoder(r.Body).Decode(&req)
	}
	var owner int
	if req.CharacterID == 0 || db.QueryRow(`SELECT agent_id FROM characters WHERE id = $1`, req.CharacterID).Scan(&owner) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	if owner != agent {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_your_character"})
		return
	}
	ensureAutoEquipColumn()
	question := "Do you want me to equip everything for you? I will put on your armor, take your weapon (and Shield or a second Light weapon) in hand, and wear your clothing and jewelry. If you have more than one choice (more weapons than hands, two items for the same place), I will ask you which to use."
	answer := strings.ToLower(strings.TrimSpace(req.Answer))
	auto, questions, actions := equipPlan(req.CharacterID, req.Choices)
	if r.Method == http.MethodGet || answer == "" {
		out := map[string]interface{}{"character_id": req.CharacterID, "question": question, "answer_with": fmt.Sprintf("POST /api/characters/auto-equip {\"character_id\": %d, \"answer\": \"yes\"} or \"no\"", req.CharacterID),
			"i_would_do": auto, "answered_before": autoEquipAnswered(req.CharacterID)}
		if len(questions) > 0 {
			out["i_would_need_you_to_choose"] = questions
		}
		if len(auto) == 0 && len(questions) == 0 && len(actions) == 0 {
			out["nothing_to_equip"] = true
		}
		writeJSONStatus(w, http.StatusOK, out)
		return
	}
	switch answer {
	case "no", "n":
		db.Exec(`UPDATE characters SET auto_equip_answered = true WHERE id = $1`, req.CharacterID)
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "equipped": false, "message": "Nothing was changed. You can put on and arm your gear yourself, or ask again with POST /api/characters/auto-equip."})
	case "yes", "y":
		if len(questions) > 0 {
			writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": false, "nothing_was_changed": true, "needs_choices": questions,
				"how_to_answer": fmt.Sprintf("POST /api/characters/auto-equip {\"character_id\": %d, \"answer\": \"yes\", \"choices\": {\"<key>\": \"<value of the option you want>\"}} with one entry per question key (use \"none\" where an option says to keep a hand empty).", req.CharacterID)})
			return
		}
		results := runEquip(req.CharacterID, agent, actions)
		db.Exec(`UPDATE characters SET auto_equip_answered = true WHERE id = $1`, req.CharacterID)
		mh, oh := equippedWeapons(req.CharacterID)
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "equipped": true, "results": results,
			"now": map[string]interface{}{"main_hand": mh, "off_hand": oh, "shield": shieldInHand(req.CharacterID)}})
	default:
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "answer_must_be_yes_or_no", "question": question})
	}
}

func weaponByID(weapons []invItem, id string) invItem {
	for _, it := range weapons {
		if itemStr(it, "id") == id {
			return it
		}
	}
	return nil
}
