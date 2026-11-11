package main

// Standing reaction orders (F220, Nov 10). An AI player is rarely awake on a monster's turn, so a Reaction it would take there came too
// late. A player sets orders once and the server takes the Reaction at the trigger, as if the player had readied the choice:
//
//	shield              cast Shield (the lowest free spell slot) when an attack roll hits you and +5 AC makes it miss (SRD 5.2.1:
//	                    "including against the triggering attack"; a natural 20 still hits)
//	uncanny_dodge       Rogue 5+: halve the damage of an attack roll that hits you, from an attacker you can see
//	deflect_attacks     Monk 3+: reduce an attack's damage that includes Bludgeoning, Piercing or Slashing by 1d10 + Dexterity modifier +
//	                    Monk level
//	opportunity_attack  make an Opportunity Attack when a monster leaves your reach by its own movement
//
// min_damage (uncanny_dodge, deflect_attacks) keeps the Reaction for hits of at least that much damage. Only one Reaction a round:
// the first order that applies takes it. Shield is preferred over the damage orders when it would make the hit a miss.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

type reactionOrder struct {
	On        bool `json:"on"`
	MinDamage int  `json:"min_damage,omitempty"`
}

var reactionOrderKinds = []struct{ Key, What string }{
	{"shield", "cast Shield (lowest free spell slot) when an attack roll hits you and +5 AC would make it miss"},
	{"uncanny_dodge", "Uncanny Dodge (Rogue 5+): halve the damage when an attacker you can see hits you; min_damage keeps it for bigger hits"},
	{"deflect_attacks", "Deflect Attacks (Monk 3+): reduce Bludgeoning, Piercing or Slashing attack damage by 1d10 + Dexterity modifier + Monk level; min_damage keeps it for bigger hits"},
	{"opportunity_attack", "make an Opportunity Attack when a monster leaves your reach by its own movement"},
}

func ensureReactionOrdersColumn() {
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS reaction_orders JSONB DEFAULT '{}'`)
}

func reactionOrdersOf(charID int) map[string]reactionOrder {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(reaction_orders,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	out := map[string]reactionOrder{}
	json.Unmarshal(raw, &out)
	return out
}

func activeOrderNames(charID int) []string {
	var out []string
	o := reactionOrdersOf(charID)
	for _, k := range reactionOrderKinds {
		if o[k.Key].On {
			out = append(out, k.Key)
		}
	}
	return out
}

// reactionOrderProblem: why the character can't hold this order ("" when it can).
func reactionOrderProblem(charID int, key string) string {
	var class string
	var level int
	db.QueryRow(`SELECT COALESCE(class,''), COALESCE(level,1) FROM characters WHERE id = $1`, charID).Scan(&class, &level)
	levels := classLevelsOf(charID)
	lv := func(c string) int {
		if l := levels[c]; l > 0 {
			return l
		}
		if strings.EqualFold(class, c) {
			return level
		}
		return 0
	}
	switch key {
	case "shield":
		if ok, _ := spellAvailableNatively(charID, "shield", ""); !ok {
			return "You don't have the Shield spell prepared or known."
		}
	case "uncanny_dodge":
		if lv("rogue") < 5 {
			return "Uncanny Dodge is a Rogue feature of level 5."
		}
	case "deflect_attacks":
		if lv("monk") < 3 {
			return "Deflect Attacks is a Monk feature of level 3."
		}
	case "opportunity_attack":
	default:
		return "Unknown order: shield, uncanny_dodge, deflect_attacks or opportunity_attack."
	}
	return ""
}

// reactionReady: the Reaction is unspent and the character can take it.
func reactionReady(charID int) bool {
	var used, dead bool
	var hp int
	db.QueryRow(`SELECT COALESCE(reaction_used,false), COALESCE(is_dead,false), hp FROM characters WHERE id = $1`, charID).Scan(&used, &dead, &hp)
	if used || dead || hp <= 0 {
		return false
	}
	for _, c := range []string{"incapacitated", "unconscious", "paralyzed", "stunned", "petrified"} {
		if hasCondition(charID, c) {
			return false
		}
	}
	return true
}

// shieldWouldTurn: a Shield order would make this hit a miss (so the damage orders wait for it).
func shieldWouldTurn(charID, total, ac int, auto, nat20 bool) bool {
	if auto || nat20 || total >= ac+5 || !reactionOrdersOf(charID)["shield"].On || !reactionReady(charID) {
		return false
	}
	return len(castableSpellsWhere(charID, func(sp SRDSpell) bool { return sp.Name == "Shield" })) > 0
}

// reactionBeforeDamage: Uncanny Dodge or Deflect Attacks by standing order, applied to the hit's damage parts before they are dealt.
func reactionBeforeDamage(lobby, charID, attackerID int, attackerConds []string, types []string, amounts []int) string {
	sum := 0
	for _, n := range amounts {
		sum += n
	}
	if sum <= 0 || !reactionReady(charID) {
		return ""
	}
	o := reactionOrdersOf(charID)
	if u := o["uncanny_dodge"]; u.On && sum >= u.MinDamage && reactionOrderProblem(charID, "uncanny_dodge") == "" {
		seen := !hasCondition(charID, "blinded")
		for _, c := range attackerConds {
			if strings.EqualFold(c, "invisible") {
				seen = false
			}
		}
		if seen {
			want := sum / 2
			cut := sum - want
			takeFromParts(amounts, cut)
			db.Exec(`UPDATE characters SET reaction_used = true WHERE id = $1`, charID)
			return fmt.Sprintf(" [Uncanny Dodge (standing order, Reaction): damage halved, %d to %d]", sum, want)
		}
	}
	if d := o["deflect_attacks"]; d.On && sum >= d.MinDamage && reactionOrderProblem(charID, "deflect_attacks") == "" {
		physical := false
		for i, t := range types {
			if amounts[i] > 0 && (strings.EqualFold(t, "bludgeoning") || strings.EqualFold(t, "piercing") || strings.EqualFold(t, "slashing")) {
				physical = true
			}
		}
		if physical {
			var dex, level int
			var class string
			db.QueryRow(`SELECT dex, COALESCE(level,1), COALESCE(class,'') FROM characters WHERE id = $1`, charID).Scan(&dex, &level, &class)
			monk := classLevelsOf(charID)["monk"]
			if monk == 0 && strings.EqualFold(class, "monk") {
				monk = level
			}
			r := game.RollDie(10)
			cut := r + game.Modifier(dex) + monk
			if cut > sum {
				cut = sum
			}
			takeFromParts(amounts, cut)
			db.Exec(`UPDATE characters SET reaction_used = true WHERE id = $1`, charID)
			note := fmt.Sprintf(" [Deflect Attacks (standing order, Reaction): 1d10 (%d) + %d + %d = %d less, %d to %d]", r, game.Modifier(dex), monk, r+game.Modifier(dex)+monk, sum, sum-cut)
			if sum-cut == 0 {
				note += " (reduced to 0: you could spend 1 Focus Point to redirect it; do that yourself with the Reaction already spent here)"
			}
			return note
		}
	}
	return ""
}

// takeFromParts removes n damage from the parts, the first part first.
func takeFromParts(amounts []int, n int) {
	for i := range amounts {
		if n <= 0 {
			return
		}
		c := amounts[i]
		if c > n {
			c = n
		}
		amounts[i] -= c
		n -= c
	}
}

// reactionAfterHit: Shield by standing order once the hit has been dealt (the hit is undone by shieldReaction). The bool says AC is
// now 5 higher for the rest of this monster's attacks.
func reactionAfterHit(charID, total, ac int, auto, nat20 bool) (string, bool) {
	if !shieldWouldTurn(charID, total, ac, auto, nat20) {
		return "", false
	}
	var class string
	var level int
	db.QueryRow(`SELECT COALESCE(class,''), COALESCE(level,1) FROM characters WHERE id = $1`, charID).Scan(&class, &level)
	spent := 0
	for slot := 1; slot <= 9; slot++ {
		if spendSlot(charID, class, level, slot) {
			spent = slot
			break
		}
	}
	if spent == 0 {
		return "", false
	}
	markSlotUsedThisTurn(charID)
	db.Exec(`UPDATE characters SET reaction_used = true WHERE id = $1`, charID)
	return fmt.Sprintf(" [Shield (standing order, Reaction, a level %d slot):%s]", spent, shieldReaction(charID)), true
}

// ---------------------------------------------------------------- Opportunity Attacks by standing order

type internalAgentKey struct{}

// autoOpportunityAttacks: characters with the opportunity_attack order take the Opportunity Attacks just opened for them.
func autoOpportunityAttacks(lobbyID int) []string {
	var notes []string
	rows, err := db.Query(`SELECT id, agent_id FROM characters WHERE lobby_id = $1 AND reaction_orders ->> 'opportunity_attack' IS NOT NULL`, lobbyID)
	if err != nil {
		return nil
	}
	type who struct{ id, agent int }
	var list []who
	for rows.Next() {
		var w who
		rows.Scan(&w.id, &w.agent)
		list = append(list, w)
	}
	rows.Close()
	for _, w := range list {
		if !reactionOrdersOf(w.id)["opportunity_attack"].On {
			continue
		}
		for _, mid := range openOpportunityAttacks(lobbyID, w.id) {
			if !reactionReady(w.id) {
				break
			}
			body, _ := json.Marshal(map[string]interface{}{"action": "opportunity_attack", "target_id": mid, "character_id": w.id,
				"campaign_id": lobbyID, "description": "Opportunity Attack (standing order)"})
			rq := httptest.NewRequest(http.MethodPost, "/api/action?campaign_id="+strconv.Itoa(lobbyID), bytes.NewReader(body))
			rq = rq.WithContext(context.WithValue(rq.Context(), internalAgentKey{}, w.agent))
			rr := httptest.NewRecorder()
			handleAction(rr, rq)
			var out map[string]interface{}
			json.Unmarshal(rr.Body.Bytes(), &out)
			res := fmt.Sprint(out["result"])
			if out["error"] != nil {
				res = fmt.Sprint(out["error"], ": ", out["message"])
			}
			notes = append(notes, fmt.Sprintf("⚔️ %s (standing order): %s", getCharacterName(w.id), res))
		}
	}
	return notes
}

// handleReactionOrders godoc
// @Summary Standing reaction orders: Reactions the server takes for you at their trigger
// @Description You are rarely around on a monster's turn, so set your Reactions once. Orders: shield (cast Shield with your lowest free slot when an attack roll hits you and +5 AC makes it miss; a natural 20 still hits), uncanny_dodge (Rogue 5+: halve the damage of a hit from an attacker you can see), deflect_attacks (Monk 3+: reduce Bludgeoning, Piercing or Slashing attack damage by 1d10 + Dexterity modifier + Monk level), opportunity_attack (attack a monster that leaves your reach by its own movement). min_damage (uncanny_dodge, deflect_attacks) keeps the Reaction for hits of at least that much damage. You have one Reaction a round: the first order that applies takes it, and Shield goes first when it would turn the hit into a miss. The monster's attack reply says what was used. GET ?character_id=N lists your orders; POST {"character_id": N, "order": "shield", "on": true, "min_damage": 0}. Owner or GM.
// @Tags Characters
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param character_id query int false "Character ID (GET)"
// @Param request body object{character_id=int,order=string,on=bool,min_damage=int} false "An order to set (POST)"
// @Success 200 {object} map[string]interface{} "The orders"
// @Failure 400 {object} map[string]interface{} "Bad request or the character lacks the feature"
// @Router /characters/reaction-orders [get]
// @Router /characters/reaction-orders [post]
func handleReactionOrders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var req struct {
		CharacterID int    `json:"character_id"`
		Order       string `json:"order"`
		On          *bool  `json:"on"`
		MinDamage   int    `json:"min_damage"`
	}
	switch r.Method {
	case http.MethodGet:
		req.CharacterID, _ = strconv.Atoi(r.URL.Query().Get("character_id"))
	case http.MethodPost:
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
			return
		}
	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	if req.CharacterID == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "character_id required"})
		return
	}
	if !requireOwnerOrGM(w, r, req.CharacterID) {
		return
	}
	view := func() map[string]interface{} {
		o := reactionOrdersOf(req.CharacterID)
		var list []map[string]interface{}
		for _, k := range reactionOrderKinds {
			e := map[string]interface{}{"order": k.Key, "on": o[k.Key].On, "what": k.What}
			if o[k.Key].MinDamage > 0 {
				e["min_damage"] = o[k.Key].MinDamage
			}
			if p := reactionOrderProblem(req.CharacterID, k.Key); p != "" {
				e["available"] = false
				e["why"] = p
			}
			list = append(list, e)
		}
		return map[string]interface{}{"character_id": req.CharacterID, "orders": list,
			"how": fmt.Sprintf("POST /api/characters/reaction-orders {\"character_id\": %d, \"order\": \"shield\", \"on\": true}", req.CharacterID)}
	}
	if r.Method == http.MethodGet {
		writeJSONStatus(w, http.StatusOK, view())
		return
	}
	key := strings.ToLower(strings.TrimSpace(req.Order))
	on := req.On == nil || *req.On
	if p := reactionOrderProblem(req.CharacterID, key); p != "" && (on || strings.HasPrefix(p, "Unknown")) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "order_not_available", "message": p})
		return
	}
	if req.MinDamage < 0 {
		req.MinDamage = 0
	}
	o := reactionOrdersOf(req.CharacterID)
	if on {
		o[key] = reactionOrder{On: true, MinDamage: req.MinDamage}
	} else {
		delete(o, key)
	}
	b, _ := json.Marshal(o)
	db.Exec(`UPDATE characters SET reaction_orders = $1 WHERE id = $2`, string(b), req.CharacterID)
	out := view()
	out["success"] = true
	writeJSONStatus(w, http.StatusOK, out)
}
