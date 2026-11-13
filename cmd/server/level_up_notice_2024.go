package main

// The server's own level-up notice to the player (Nov 13). Matt: the GM only tells the server the character reaches a level
// (POST /api/gm/update-character {"character_id": N, "level": L}); the notice comes from the server, not the DM, and presents every
// new feature with its SRD text, what the species gains at that level, and each choice to make with its options and explanations.
// One notice per level gained. It shows in GET /api/my-turn and GET /api/heartbeat until the player marks it read:
// GET /api/characters/{id}/level-up-notices lists them; POST {"seen": true} marks them read.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

var levelUpNoticeOnce sync.Once

func ensureLevelUpNotices() {
	levelUpNoticeOnce.Do(func() {
		db.Exec(`CREATE TABLE IF NOT EXISTS level_up_notices (id SERIAL PRIMARY KEY, character_id INT NOT NULL, level INT NOT NULL,
			hp_gained INT NOT NULL DEFAULT 0, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), seen_at TIMESTAMPTZ)`)
	})
}

// recordLevelUpNotices saves one notice for each level gained.
func recordLevelUpNotices(charID, oldLevel, newLevel, hp int) {
	if db == nil || newLevel <= oldLevel {
		return
	}
	ensureLevelUpNotices()
	levels := newLevel - oldLevel
	for l := oldLevel + 1; l <= newLevel; l++ {
		share := hp / levels
		if l == newLevel {
			share = hp - (hp/levels)*(levels-1)
		}
		db.Exec(`INSERT INTO level_up_notices (character_id, level, hp_gained) VALUES ($1, $2, $3)`, charID, l, share)
	}
}

func proficiencyBonusAt(level int) int { return (level-1)/4 + 2 }

// speciesAtLevel: what the character's species gives at this character level (SRD 5.2.1), each with the trait's SRD text.
func speciesAtLevel(charID int, species string, level int) []map[string]interface{} {
	sp := strings.ToLower(species)
	var out []map[string]interface{}
	add := func(trait, now string) {
		m := map[string]interface{}{"trait": trait, "srd_text": srdTrait(sp, trait)}
		if now != "" {
			m["now"] = now
		}
		out = append(out, m)
	}
	switch sp {
	case "dragonborn":
		if dice := map[int]string{5: "2d10", 11: "3d10", 17: "4d10"}[level]; dice != "" {
			add("Breath Weapon", "Your Breath Weapon now deals "+dice+" damage.")
		}
		if level == 5 {
			add("Draconic Flight", "You can use Draconic Flight from now on.")
		}
	case "goliath":
		if level == 5 {
			add("Large Form", "You can use Large Form from now on.")
		}
	case "dwarf":
		add("Dwarven Toughness", "Your Hit Point maximum went up by 1 more for this level (included in the Hit Points above).")
	case "elf", "tiefling":
		if level == 3 || level == 5 {
			trait := map[string]string{"elf": "Elven Lineage", "tiefling": "Fiendish Legacy"}[sp]
			var raw []byte
			db.QueryRow(`SELECT COALESCE(origin, '{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
			now := ""
			if rec, ok := loadOriginRecord(raw); ok {
				var names []string
				for _, s := range rec.Species.LevelSpells[level] {
					if x, ok := srdSpellsMemory[s]; ok {
						names = append(names, x.Name)
					} else {
						names = append(names, s)
					}
				}
				if len(names) > 0 {
					now = "You learn " + strings.Join(names, ", ") + ": always prepared, castable once without a spell slot per Long Rest."
				}
			}
			add(trait, now)
		}
	}
	return out
}

// levelUpNotice builds the notice for one level.
func levelUpNotice(charID, level, hp int, created time.Time) map[string]interface{} {
	var name, class, species string
	var subclass *string
	var maxHP, agentID, curLevel int
	db.QueryRow(`SELECT name, COALESCE(class,''), COALESCE(species,''), subclass, COALESCE(max_hp,0), COALESCE(agent_id,0), COALESCE(level,1) FROM characters WHERE id = $1`, charID).
		Scan(&name, &class, &species, &subclass, &maxHP, &agentID, &curLevel)
	sub := ""
	if subclass != nil {
		sub = *subclass
	}
	n := map[string]interface{}{
		"from":    "the server",
		"level":   level,
		"message": fmt.Sprintf("%s, you reached level %d! You are now a level %d %s. Your Hit Point maximum went up by %d (it is %d now).", name, level, level, titleCase(class), hp, maxHP),
		"when":    created.UTC().Format(time.RFC3339),
	}
	if pb, before := proficiencyBonusAt(level), proficiencyBonusAt(level-1); pb != before {
		n["proficiency_bonus"] = fmt.Sprintf("Your Proficiency Bonus went up from +%d to +%d.", before, pb)
	}
	var feats []map[string]string
	for _, f := range game.GetActiveClassFeatures(class, level) {
		if f.Level == level {
			feats = append(feats, map[string]string{"name": f.Name, "what_it_does": f.Description})
		}
	}
	// the SRD prints Ability Score Improvement (and some others) once, with "you gain this feature again at levels ...": name it at each of those levels
	if containsInt(asiLevelsFor(class), level) && level < 19 {
		hasASI := false
		for _, f := range feats {
			if f["name"] == "Ability Score Improvement" {
				hasASI = true
			}
		}
		if !hasASI {
			feats = append(feats, map[string]string{"name": "Ability Score Improvement", "what_it_does": classFeatureText(class, "Ability Score Improvement")})
		}
	}
	// Expertise is printed once with "at level N you gain Expertise in two more": name it at those levels too
	if expertiseAllowed(class, level) > expertiseAllowed(class, level-1) {
		hasExp := false
		for _, f := range feats {
			if strings.Contains(f["name"], "Expertise") || strings.Contains(f["name"], "Deft Explorer") || strings.Contains(f["name"], "Scholar") {
				hasExp = true
			}
		}
		if !hasExp {
			feats = append(feats, map[string]string{"name": "Expertise", "what_it_does": classFeatureText(class, "Expertise")})
		}
	}
	// a feature from an earlier level that its SRD text says improves at this level ("starting at level 17", "at Fighter levels 13 and 17")
	for _, f := range game.GetActiveClassFeatures(class, level) {
		if f.Level >= level || f.Name == "Ability Score Improvement" || f.Name == "Expertise" || (f.Name == "Wild Shape" && strings.EqualFold(class, "druid")) {
			continue
		}
		if improvesAt(f.Description, level) {
			dup := false
			for _, x := range feats {
				if strings.HasPrefix(x["name"], f.Name) {
					dup = true
				}
			}
			if !dup {
				feats = append(feats, map[string]string{"name": f.Name + " (improves at this level)", "what_it_does": f.Description})
			}
		}
	}
	if strings.EqualFold(class, "druid") && (level == 4 || level == 8) { // the Known Forms rows of Wild Shape grow at 4 and 8
		kn, maxCR, fly := wildShapeLimits(level)
		feats = append(feats, map[string]string{"name": "Wild Shape (more known forms)", "what_it_does": fmt.Sprintf("You now know %d Beast forms, of Challenge Rating %s or lower%s. %s", kn, strconv.FormatFloat(maxCR, 'g', -1, 64), map[bool]string{true: ", Fly Speed allowed", false: ", no Fly Speed"}[fly], classFeatureText("druid", "Wild Shape"))})
	}
	n["new_class_features"] = feats
	if note := cantripsCappedNote(charID, class, level); note != "" && level == curLevel {
		n["cantrips_note"] = note
	}
	// what changed in the class's features table (Rages, Rage Damage, Sneak Attack, spell slots ...)
	var table []string
	before, now := game.ClassTableRow(class, level-1), game.ClassTableRow(class, level)
	var cols []string
	for c := range now {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	for _, c := range cols {
		if c == "Level" || c == "Class Features" || c == "Proficiency Bonus" || c == "Features" {
			continue
		}
		if b := before[c]; b != now[c] {
			label := c
			if len(c) == 1 && c[0] >= '1' && c[0] <= '9' {
				label = "Level " + c + " spell slots" // the Spell Slots per Spell Level columns are named 1 to 9
			}
			if b == "" || b == "—" {
				table = append(table, fmt.Sprintf("%s: %s (new)", label, now[c]))
			} else {
				table = append(table, fmt.Sprintf("%s: %s, was %s", label, now[c], b))
			}
		}
	}
	if len(table) > 0 {
		n["class_table_changes"] = table
	}
	if sub != "" {
		var sf []map[string]string
		for _, f := range getActiveSubclassFeatures(sub, level) {
			if f.Level == level {
				sf = append(sf, map[string]string{"name": f.Name, "what_it_does": f.Description})
			}
		}
		if len(sf) > 0 {
			n["new_subclass_features"] = sf
		}
	}
	if s := speciesAtLevel(charID, species, level); len(s) > 0 {
		n["species"] = s
	} else {
		n["species"] = "Your species gains nothing new at this level."
	}
	// the choices still open, with their options, from the setup guide (only the newest level's notice carries them)
	if level == curLevel && agentID > 0 {
		_, setup := internalCall(agentID, "GET", fmt.Sprintf("/api/characters/%d/setup", charID), nil)
		var open []interface{}
		if steps, ok := setup["steps"].([]interface{}); ok {
			for _, s := range steps {
				if m, ok := s.(map[string]interface{}); ok {
					if d, _ := m["done"].(bool); !d {
						open = append(open, m)
					}
				}
			}
		}
		if len(open) > 0 {
			n["choices_to_make"] = open
		} else {
			n["choices_to_make"] = "None: this level asks for no choices."
		}
	} else {
		n["choices_to_make"] = fmt.Sprintf("See the notice for level %d (your current level): it lists every choice still open.", curLevel)
	}
	n["when_you_have_read_this"] = fmt.Sprintf("POST /api/characters/%d/level-up-notices {\"seen\": true}", charID)
	return n
}

// unseenLevelUpNotices: the notices the player hasn't marked read, oldest first.
func unseenLevelUpNotices(charID int) []map[string]interface{} {
	if db == nil {
		return nil
	}
	ensureLevelUpNotices()
	rows, err := db.Query(`SELECT level, hp_gained, created_at FROM level_up_notices WHERE character_id = $1 AND seen_at IS NULL ORDER BY level`, charID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	type row struct {
		level, hp int
		at        time.Time
	}
	var list []row
	for rows.Next() {
		var x row
		rows.Scan(&x.level, &x.hp, &x.at)
		list = append(list, x)
	}
	var out []map[string]interface{}
	for _, x := range list {
		out = append(out, levelUpNotice(charID, x.level, x.hp, x.at))
	}
	return out
}

// handleLevelUpNotices: GET lists the unread notices (?all=true: every notice); POST {"seen": true} marks them read.
func handleLevelUpNotices(w http.ResponseWriter, r *http.Request, charID int) {
	agent, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var owner int
	if db.QueryRow(`SELECT COALESCE(agent_id,0) FROM characters WHERE id = $1`, charID).Scan(&owner) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	if owner != agent {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_your_character"})
		return
	}
	ensureLevelUpNotices()
	if r.Method == http.MethodPost {
		var req struct {
			Seen bool `json:"seen"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if !req.Seen {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "send_seen_true", "message": "Send {\"seen\": true} to mark your level-up notices read."})
			return
		}
		res, _ := db.Exec(`UPDATE level_up_notices SET seen_at = NOW() WHERE character_id = $1 AND seen_at IS NULL`, charID)
		n, _ := res.RowsAffected()
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "marked_read": n,
			"message": fmt.Sprintf("Marked %d level-up notice(s) read. Choices still open stay on GET /api/characters/%d/setup.", n, charID)})
		return
	}
	if r.URL.Query().Get("all") == "true" {
		rows, _ := db.Query(`SELECT level, hp_gained, created_at FROM level_up_notices WHERE character_id = $1 ORDER BY level`, charID)
		var out []map[string]interface{}
		if rows != nil {
			defer rows.Close()
			for rows.Next() {
				var l, hp int
				var at time.Time
				rows.Scan(&l, &hp, &at)
				out = append(out, levelUpNotice(charID, l, hp, at))
			}
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"notices": out})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"notices": unseenLevelUpNotices(charID)})
}

func nilIfNoNotices(n []map[string]interface{}) interface{} {
	if len(n) == 0 {
		return nil
	}
	return n
}


var levelMention = regexp.MustCompile(`(?i)(?:at|starting at|when you reach|reach)\s+(?:[A-Z][a-z]+\s+)?levels?\s+(\d+(?:\s*\([^)]*\))?(?:\s*,\s*\d+(?:\s*\([^)]*\))?)*(?:,?\s*and\s+\d+(?:\s*\([^)]*\))?)?)`)
var digitsRe = regexp.MustCompile(`\d+`)

// improvesAt: the text names this level in an "at level N" phrase (numbers inside parentheses, such as dice, don't count).
func improvesAt(text string, level int) bool {
	for _, m := range levelMention.FindAllStringSubmatch(text, -1) {
		list := regexp.MustCompile(`\([^)]*\)`).ReplaceAllString(m[1], "")
		for _, d := range digitsRe.FindAllString(list, -1) {
			if d == strconv.Itoa(level) {
				return true
			}
		}
	}
	return false
}
