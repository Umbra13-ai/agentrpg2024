package main

// The creatures a story line shows (owner, Nov 24): a log record carries the creatures in it, recorded when the line is written, never
// guessed from its words (a "guard" or a "wolf" in the prose is not a creature the server knows). The GM names who appears with a
// narration (POST /api/gm/narrate's appearing: a creature of the fight by its name or "#-2", an SRD monster by name or slug, a GM-built
// monster by its custom: key or name); a monster's turn the server records carries its own creature. resolveShownCreatures is the one
// reading of those names (refused before anything is recorded when one is unknown); the record keeps each {name, key}
// (writeActionShowing, actions.creatures); storyCreaturesOf turns them into what a page shows: the name and its token (monsterTokenURL),
// only for a creature that has one. The story reader and the chronicle show them beside the line; my-turn names them.
//
// A narration sent without appearing (owner, Nov 24) shows the creatures its words name (creaturesNamedIn): a creature of the campaign's
// fight (by its name, that name without its number, or its stat block's name) and a monster of the GM's own compendium, each as a whole word,
// singular or plural. Not the SRD catalogue at large: its names are everyday words ("Shadow", "Guard", "Cat"). The reply says which were read;
// appearing names them instead, and an empty appearing shows none.

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// shownCreature: a creature a log record shows: the name the story uses and the key of its stat block (an SRD slug or a custom: key).
type shownCreature struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// storyCreature: what a page shows of one: its name and its token.
type storyCreature struct {
	Name  string `json:"name"`
	Token string `json:"token"`
	First bool   `json:"first,omitempty"` // its first passage of the chapter: shown larger (storyChapters)
}

// fightCreature: a creature of the campaign's fight by its name, "#-2" or key (monsterIDsByKeyOrName), with the key of its stat block.
func fightCreature(lobby int, name string) (shownCreature, bool) {
	ids := monsterIDsByKeyOrName(lobby, name)
	if len(ids) == 0 {
		return shownCreature{}, false
	}
	for _, e := range combatEntries(lobby) {
		if asInt(e["id"]) == ids[0] {
			key := strings.TrimSpace(sprintAny(e["monster_key"]))
			n := strings.TrimSpace(sprintAny(e["name"]))
			if n == "" {
				n = name
			}
			return shownCreature{Name: n, Key: key}, true
		}
	}
	return shownCreature{}, false
}

// resolveShownCreatures reads the names a GM gives as appearing: each a creature of the fight, an SRD monster, or a GM-built monster the GM
// may use (its own, or a shared one). unknown lists the names that are none of those.
func resolveShownCreatures(lobby, agent int, names []string) (out []shownCreature, unknown []string) {
	seen := map[string]bool{}
	add := func(c shownCreature) {
		if k := c.Name + "|" + c.Key; !seen[k] {
			seen[k] = true
			out = append(out, c)
		}
	}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if c, ok := fightCreature(lobby, name); ok {
			add(c)
			continue
		}
		if m, ok := srdMonsterNamed(name); ok {
			add(shownCreature{Name: m.Name, Key: monsterSlug(m.Name)})
			continue
		}
		var key, n string
		err := db.QueryRow(`SELECT key, definition->>'name' FROM custom_monsters WHERE `+compendiumVisible(2)+`
			AND (key = $1 OR (hidden_at IS NULL AND lower(definition->>'name') = lower($1))) ORDER BY created_at DESC LIMIT 1`, name, agent).Scan(&key, &n)
		if err == nil {
			add(shownCreature{Name: n, Key: key})
			continue
		}
		if err != sql.ErrNoRows {
			handledDBError(err)
		}
		unknown = append(unknown, name)
	}
	return out, unknown
}

// storyCreaturesOf: the creatures of a record that have a token, as a page shows them.
func storyCreaturesOf(list []shownCreature) []storyCreature {
	var out []storyCreature
	for _, c := range list {
		if u := monsterTokenURL(c.Key); u != "" {
			out = append(out, storyCreature{Name: c.Name, Token: u})
		}
	}
	return out
}

// shownNames: the creatures' names, for a reader of text (my-turn).
func shownNames(list []shownCreature) []string {
	var out []string
	for _, c := range list {
		out = append(out, c.Name)
	}
	return out
}

// appearingRefusal: the words of a refused appearing list.
func appearingRefusal(unknown []string) string {
	return fmt.Sprintf("appearing names %s, which is not a creature of the fight, an SRD monster or a monster of your compendium (by its custom: key or name). "+
		"Name each as combat/add, the SRD or the compendium does, or leave it out.", strings.Join(quoteEach(unknown), ", "))
}

func quoteEach(list []string) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

// trailingNumberRe: a fight creature's number after its name ("Bandit 2").
var trailingNumberRe = regexp.MustCompile(`\s+\d+$`)

// creaturesNamedIn: the creatures a narration's words name (see the file comment), in the order of the candidates, each once.
func creaturesNamedIn(lobby, agent int, text string) []shownCreature {
	type candidate struct {
		words []string
		c     shownCreature
	}
	var cands []candidate
	for _, e := range combatEntries(lobby) {
		if asInt(e["id"]) >= 0 {
			continue
		}
		name := strings.TrimSpace(sprintAny(e["name"]))
		key := strings.TrimSpace(sprintAny(e["monster_key"]))
		base := trailingNumberRe.ReplaceAllString(name, "")
		words := []string{name, base}
		if m, ok := srdMonsterNamed(key); ok {
			words = append(words, m.Name)
		}
		cands = append(cands, candidate{words, shownCreature{Name: base, Key: key}})
	}
	if agent > 0 && db != nil {
		rows, err := db.Query(`SELECT key, definition->>'name' FROM custom_monsters WHERE owner_id = $1 AND hidden_at IS NULL ORDER BY created_at DESC`, agent)
		if err == nil {
			for rows.Next() {
				var key, name string
				if rows.Scan(&key, &name) == nil && name != "" {
					cands = append(cands, candidate{[]string{name}, shownCreature{Name: name, Key: key}})
				}
			}
			rows.Close()
		} else {
			handledDBError(err)
		}
	}
	var out []shownCreature
	seen := map[string]bool{}
	for _, cd := range cands {
		if seen[game.Key(cd.c.Name)+"|"+cd.c.Key] {
			continue
		}
		for _, w := range cd.words {
			if namesWord(text, w) {
				seen[game.Key(cd.c.Name)+"|"+cd.c.Key] = true
				out = append(out, cd.c)
				break
			}
		}
	}
	return out
}

// namesWord: the text holds the name as whole words, any case, singular or plural ("goblin warrior", "Goblin Warriors"); a name under three
// letters is never read.
func namesWord(text, name string) bool {
	name = strings.TrimSpace(name)
	if len(name) < 3 {
		return false
	}
	re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(name) + `(?:s|es)?\b`)
	return err == nil && re.MatchString(text)
}
